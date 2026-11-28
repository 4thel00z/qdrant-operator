package usecases

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// ReconcileCluster applies the Helm release of a QdrantCluster.
//
// Execute returns ScaleDownPendingError, RolloutInProgressError or ReleaseApplyError, each carrying
// the status to write before the caller retries, and plain errors for everything else.
type ReconcileCluster struct {
	Helm       ports.Helm
	Kubernetes ports.Kubernetes
	Qdrant     ports.Qdrant
	Clock      Clock
	Random     io.Reader
}

// IsApplyFailed reports whether the conditions record a failed helm apply.
func IsApplyFailed(conditions []domain.Condition) bool {
	for _, condition := range conditions {
		if condition.Type == "Progressing" && condition.Status == domain.ConditionFalse && condition.Reason == "HelmApplyFailed" {
			return true
		}
	}
	return false
}

func (r ReconcileCluster) Execute(ctx context.Context, spec domain.ClusterSpec, generation int64, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	if err := (GenerateAPIKeys{Kubernetes: r.Kubernetes, Random: r.Random}).Execute(ctx, spec); err != nil {
		return current, err
	}
	live, err := r.Kubernetes.StatefulSetStatus(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("read statefulset: %w", err)
	}
	current, err = r.prepareScaleDown(ctx, spec, live, current)
	if err != nil {
		return current, err
	}
	held := spec.Rollout == domain.RolloutShardHealth
	recreated, err := ExpandVolumes{Kubernetes: r.Kubernetes}.Execute(ctx, spec, held && live != nil && live.Rolling())
	if err != nil {
		return current, err
	}
	values := spec.HelmValues()
	chartVersion := domain.ChartVersion(spec.Version)
	digest, err := domain.ValuesDigest(chartVersion, values)
	if err != nil {
		return current, fmt.Errorf("digest helm values: %w", err)
	}
	exists, err := r.Helm.ReleaseExists(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("check helm release: %w", err)
	}
	if exists && !recreated && current.AppliedHash == digest && !IsApplyFailed(current.Conditions) {
		current.ObservedGeneration = generation
		return current, nil
	}
	err = r.Helm.Apply(ctx, ports.ReleaseRequest{
		Name:         spec.ReleaseName(),
		Namespace:    spec.Namespace,
		ChartVersion: chartVersion,
		Values:       values,
		StatefulSet:  spec.ReleaseName(),
		Partition:    domain.HoldPartition(spec.Rollout, spec.Replicas),
	})
	if err != nil && ctx.Err() != nil {
		return current, fmt.Errorf("apply helm release: %w", err)
	}
	if err != nil {
		message := lastChars(err.Error(), HelmErrorChars)
		failed := withProgress(r.Clock, current, "Progressing", false, "HelmApplyFailed", message)
		failed.ObservedGeneration = generation
		return current, &ReleaseApplyError{Message: message, Status: failed, Cause: err}
	}
	if spec.Scaling.DrainOnScaleDown && live != nil && live.Replicas > spec.Replicas {
		if err := r.deleteDepartedClaims(ctx, spec); err != nil {
			return current, err
		}
	}
	phase := domain.PhasePending
	if exists {
		phase = domain.PhaseUpgrading
	}
	applied := withProgress(r.Clock, current, "Progressing", true, "HelmReleaseApplied", fmt.Sprintf("Helm release %s applied", spec.ReleaseName()))
	applied.Phase = phase
	applied.Replicas = spec.Replicas
	applied.HelmRelease = spec.ReleaseName()
	applied.Endpoint = spec.ServiceURL()
	applied.Version = spec.Version
	applied.ObservedGeneration = generation
	applied.AppliedHash = digest
	return applied, nil
}

func lastChars(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[len(runes)-limit:])
}

// deleteDepartedClaims removes the volumes of drained pods: their peers left consensus, so a later
// scale-up must start them empty instead of as a removed peer with no cluster.
func (r ReconcileCluster) deleteDepartedClaims(ctx context.Context, spec domain.ClusterSpec) error {
	claims, err := r.Kubernetes.ListVolumeClaims(ctx, spec.Namespace, spec.VolumeSelector())
	if err != nil {
		return fmt.Errorf("list volume claims: %w", err)
	}
	names := make([]string, 0, len(claims))
	for _, claim := range claims {
		names = append(names, claim.Name)
	}
	for _, name := range spec.DepartedClaims(names) {
		if err := r.Kubernetes.DeleteVolumeClaim(ctx, name, spec.Namespace); err != nil {
			return fmt.Errorf("delete claim %s: %w", name, err)
		}
	}
	return nil
}

// prepareScaleDown drains the peers a scale-down removes, or flags the shards it would strand.
func (r ReconcileCluster) prepareScaleDown(ctx context.Context, spec domain.ClusterSpec, live *domain.StatefulSetStatus, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	if live == nil || live.Replicas <= spec.Replicas {
		return current, nil
	}
	if spec.Scaling.DrainOnScaleDown && spec.Rollout == domain.RolloutShardHealth && live.Rolling() {
		message := "scale-down waits for the pod restarts to finish"
		waiting := withProgress(r.Clock, current, "ScalingDown", true, "WaitingForRollout", message)
		waiting.Phase = domain.PhaseUpgrading
		return current, &RolloutInProgressError{Message: message, Status: &waiting}
	}
	connection, err := ResolveCluster{Kubernetes: r.Kubernetes}.Connect(ctx, spec)
	if err != nil {
		return current, err
	}
	if len(connection.Nodes) == 0 {
		return current, nil
	}
	node := connection.Nodes[0]
	if spec.Scaling.DrainOnScaleDown {
		return r.drain(ctx, spec, node, current)
	}
	return r.warnUnsafe(ctx, spec, node, current)
}

func (r ReconcileCluster) drain(ctx context.Context, spec domain.ClusterSpec, node domain.QdrantNode, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	plan, err := DrainPeers{Qdrant: r.Qdrant}.Execute(ctx, spec, node)
	var unsafe *ShardsUnsafeError
	if errors.As(err, &unsafe) {
		blocked := withProgress(r.Clock, current, "ScalingDown", true, "ShardsUnsafe", unsafe.Message)
		return current, &ScaleDownPendingError{Message: unsafe.Message, Status: blocked, Cause: err}
	}
	if err != nil {
		return current, err
	}
	if planIdle(plan) {
		return withProgress(r.Clock, current, "ScalingDown", false, "PeersRemoved", "removed peers are empty"), nil
	}
	message := DescribePlan(plan)
	pending := withProgress(r.Clock, current, "ScalingDown", true, "DrainingPeers", message)
	pending.Phase = domain.PhaseUpgrading
	return current, &ScaleDownPendingError{Message: message, Status: pending}
}

func (r ReconcileCluster) warnUnsafe(ctx context.Context, spec domain.ClusterSpec, node domain.QdrantNode, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	topology, err := r.Qdrant.ClusterTopology(ctx, node)
	var survey []domain.CollectionShards
	if err == nil {
		survey, err = SurveyCollections(ctx, r.Qdrant, node)
	}
	if errors.Is(err, ports.ErrUnreachable) {
		return current, nil
	}
	if err != nil {
		return current, fmt.Errorf("survey shards before scale-down: %w", err)
	}
	plan := domain.PlanEvacuation(
		survey,
		domain.DepartingPeers(topology, spec.ReleaseName(), spec.Replicas),
		domain.SurvivingPeers(topology, spec.ReleaseName(), spec.Replicas),
	)
	stranded := []string{}
	for _, operation := range plan.Operations {
		if operation.Kind != domain.ShardMove {
			continue
		}
		stranded = append(stranded, fmt.Sprintf("%s/%d", operation.Collection, operation.ShardID))
	}
	for _, loss := range plan.Lost {
		stranded = append(stranded, loss.Describe())
	}
	if len(stranded) == 0 {
		return current, nil
	}
	message := "scale-down strands shard replicas: " + joinLimited(stranded, 10)
	return withProgress(r.Clock, current, "ScaleDownUnsafe", true, "ShardsOnRemovedPeers", message), nil
}

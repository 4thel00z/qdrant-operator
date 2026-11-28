package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// RebalanceShards brings every collection to its replication factor and spreads replicas evenly.
// It takes one step per collection per call, only while the StatefulSet is settled and every pod answers.
type RebalanceShards struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
	Clock      Clock
}

func (r RebalanceShards) Execute(ctx context.Context, spec domain.ClusterSpec, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	statefulSet, err := r.Kubernetes.StatefulSetStatus(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("read statefulset: %w", err)
	}
	settled := statefulSet != nil &&
		statefulSet.Replicas == spec.Replicas &&
		statefulSet.ReadyReplicas >= spec.Replicas &&
		!statefulSet.Rolling()
	if !settled {
		return current, nil
	}
	connection, err := ResolveCluster{Kubernetes: r.Kubernetes}.Connect(ctx, spec)
	if err != nil {
		return current, err
	}
	if len(connection.Nodes) == 0 {
		return current, nil
	}
	live := []uint64{}
	for _, node := range connection.Nodes {
		topology, err := r.Qdrant.ClusterTopology(ctx, node)
		if errors.Is(err, ports.ErrUnreachable) {
			return current, nil
		}
		if err != nil {
			return current, fmt.Errorf("cluster topology: %w", err)
		}
		live = append(live, topology.PeerID)
	}
	slices.Sort(live)
	node := connection.Nodes[0]
	survey, err := SurveyCollections(ctx, r.Qdrant, node)
	if errors.Is(err, ports.ErrUnreachable) {
		return current, nil
	}
	if err != nil {
		return current, err
	}
	plan := domain.PlanRebalance(survey, live)
	for _, operation := range plan.Operations {
		if err := r.Qdrant.ApplyShardOperation(ctx, node, operation); err != nil {
			return current, fmt.Errorf("apply %s: %w", operation.Describe(), err)
		}
	}
	if len(plan.Operations) > 0 || plan.InFlight > 0 {
		return withProgress(r.Clock, current, "Rebalancing", true, "Rebalancing", DescribePlan(plan)), nil
	}
	return withProgress(r.Clock, current, "Rebalancing", false, "Balanced", "replicas match the replication factor and are spread evenly"), nil
}

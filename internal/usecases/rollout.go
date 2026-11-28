package usecases

import (
	"context"
	"fmt"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// StepRollout moves the StatefulSet partition one pod at a time, each only once the cluster is healthy.
//
// It is level-triggered: the only state is the partition and the pods' revision labels, so a restarted
// operator resumes where it stopped. The Readiness strategy just lifts the partition.
type StepRollout struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
	Clock      Clock
}

func (s StepRollout) Execute(ctx context.Context, spec domain.ClusterSpec, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	live, err := s.Kubernetes.StatefulSetStatus(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("read statefulset: %w", err)
	}
	if live == nil {
		return current, nil
	}
	strategy := spec.Rollout
	if strategy == domain.RolloutReadiness {
		target, ok := domain.NextPartition(strategy, bareSnapshot(*live, spec))
		return current, s.apply(ctx, spec, live.Partition, target, ok)
	}
	states, err := s.Kubernetes.ListPodStates(ctx, spec.Namespace, spec.PodLabelSelector())
	if err != nil {
		return current, fmt.Errorf("list pods: %w", err)
	}
	pods := domain.PodRevisions(spec.ReleaseName(), spec.Replicas, states)
	snapshot := bareSnapshot(*live, spec)
	snapshot.Pods = pods
	behind := len(snapshot.Behind()) > 0
	if behind {
		connection, err := ResolveCluster{Kubernetes: s.Kubernetes}.Connect(ctx, spec)
		if err != nil {
			return current, err
		}
		snapshot.Health, err = AssessHealth(ctx, s.Qdrant, spec, connection.Nodes, pods)
		if err != nil {
			return current, err
		}
	}
	target, ok := domain.NextPartition(strategy, snapshot)
	if err := s.apply(ctx, spec, live.Partition, target, ok); err != nil {
		return current, err
	}
	if !behind {
		return current, nil
	}
	progress, waiting := snapshot.Wait()
	if !waiting {
		progress = domain.RolloutWait{Reason: "RestartingPod", Message: fmt.Sprintf("releasing pod %d", target)}
	}
	stepped := withProgress(s.Clock, current, "Progressing", true, progress.Reason, progress.Message)
	stepped.Phase = domain.PhaseUpgrading
	return stepped, nil
}

func bareSnapshot(live domain.StatefulSetStatus, spec domain.ClusterSpec) domain.RolloutSnapshot {
	return domain.RolloutSnapshot{Partition: live.Partition, Replicas: spec.Replicas, UpdateRevision: live.UpdateRevision}
}

func (s StepRollout) apply(ctx context.Context, spec domain.ClusterSpec, partition int, target int, ok bool) error {
	if !ok || target == partition {
		return nil
	}
	if err := s.Kubernetes.SetStatefulSetPartition(ctx, spec.ReleaseName(), spec.Namespace, target); err != nil {
		return fmt.Errorf("set partition %d: %w", target, err)
	}
	return nil
}

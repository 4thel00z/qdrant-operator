package usecases

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// RecoverDeadPeers re-replicates shards off peers no pod answers for, then removes those peers.
//
// Two cases count as dead. With every pod answering: a peer whose ordinal now reports another peer id,
// or is beyond the replica count. With pods not answering: a peer that one of them names in Qdrant's join
// error ("peer URI ... already used by peer N"), which a pod rebuilt on an empty volume logs while it
// crash-loops. A pod that is merely slow is never judged. Nothing is done while the StatefulSet is
// resizing. Under Readiness nothing is done while it rolls either; under ShardHealth only while a pod the
// StatefulSet controller may restart (ordinal at or above the partition) is behind or not Ready, so a held
// rollout does not stall on a leftover dead peer.
type RecoverDeadPeers struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
	Clock      Clock
}

type answeringNode struct {
	ordinal  int
	topology domain.ClusterTopology
}

func (r RecoverDeadPeers) Execute(ctx context.Context, spec domain.ClusterSpec, current domain.ClusterStatus) (domain.ClusterStatus, error) {
	statefulSet, err := r.Kubernetes.StatefulSetStatus(ctx, spec.ReleaseName(), spec.Namespace)
	if err != nil {
		return current, fmt.Errorf("read statefulset: %w", err)
	}
	if statefulSet == nil || statefulSet.Replicas != spec.Replicas {
		return current, nil
	}
	restarting, err := r.restarting(ctx, spec, *statefulSet)
	if err != nil || restarting {
		return current, err
	}
	connection, err := ResolveCluster{Kubernetes: r.Kubernetes}.Connect(ctx, spec)
	if err != nil {
		return current, err
	}
	answering := []answeringNode{}
	for ordinal, node := range connection.Nodes {
		topology, err := r.Qdrant.ClusterTopology(ctx, node)
		if errors.Is(err, ports.ErrUnreachable) {
			continue
		}
		if err != nil {
			return current, fmt.Errorf("cluster topology of node %d: %w", ordinal, err)
		}
		answering = append(answering, answeringNode{ordinal: ordinal, topology: topology})
	}
	if len(answering) == 0 {
		return current, nil
	}
	everyoneAnswers := len(answering) == spec.Replicas
	if everyoneAnswers && statefulSet.ReadyReplicas < spec.Replicas {
		return current, nil
	}
	first := answering[0]
	dead, err := r.findDead(ctx, spec, answering)
	if err != nil {
		return current, err
	}
	if len(dead) == 0 && !everyoneAnswers {
		return current, nil
	}
	if len(dead) == 0 {
		return withProgress(r.Clock, current, "DeadPeers", false, "NoDeadPeers", "every peer has a pod"), nil
	}
	node := connection.Nodes[first.ordinal]
	survey, err := SurveyCollections(ctx, r.Qdrant, node)
	if err != nil {
		return current, err
	}
	live := []uint64{}
	for _, answered := range answering {
		live = append(live, answered.topology.PeerID)
	}
	slices.Sort(live)
	plan := domain.PlanRecovery(survey, dead, live)
	if len(plan.Lost) > 0 {
		return withProgress(r.Clock, current, "DeadPeers", true, "ShardsLost", "no healthy replica to restore from: "+describeLosses(plan.Lost)), nil
	}
	for _, operation := range plan.Operations {
		if err := r.Qdrant.ApplyShardOperation(ctx, node, operation); err != nil {
			return current, fmt.Errorf("apply %s: %w", operation.Describe(), err)
		}
	}
	if !planIdle(plan) {
		return withProgress(r.Clock, current, "DeadPeers", true, "RestoringReplicas", DescribePlan(plan)), nil
	}
	for _, peerID := range dead {
		if err := r.Qdrant.RemovePeer(ctx, node, peerID, true); err != nil {
			return current, fmt.Errorf("remove peer %d: %w", peerID, err)
		}
	}
	return withProgress(r.Clock, current, "DeadPeers", false, "DeadPeersRemoved", fmt.Sprintf("removed %d dead peers", len(dead))), nil
}

func (r RecoverDeadPeers) restarting(ctx context.Context, spec domain.ClusterSpec, statefulSet domain.StatefulSetStatus) (bool, error) {
	if spec.Rollout == domain.RolloutReadiness {
		return statefulSet.Rolling(), nil
	}
	states, err := r.Kubernetes.ListPodStates(ctx, spec.Namespace, spec.PodLabelSelector())
	if err != nil {
		return false, fmt.Errorf("list pods: %w", err)
	}
	pods := domain.PodRevisions(spec.ReleaseName(), spec.Replicas, states)
	return !domain.ReleasedPodsSettled(statefulSet.Partition, statefulSet.UpdateRevision, pods), nil
}

func (r RecoverDeadPeers) findDead(ctx context.Context, spec domain.ClusterSpec, answering []answeringNode) ([]uint64, error) {
	podPeerIDs := map[int]uint64{}
	for _, answered := range answering {
		podPeerIDs[answered.ordinal] = answered.topology.PeerID
	}
	topology := answering[0].topology
	silent := []int{}
	for ordinal := range spec.Replicas {
		if _, ok := podPeerIDs[ordinal]; !ok {
			silent = append(silent, ordinal)
		}
	}
	if len(silent) == 0 {
		return domain.DeadPeers(topology, podPeerIDs, spec.ReleaseName(), spec.Replicas), nil
	}
	known := map[uint64]bool{}
	for _, peer := range topology.Peers {
		known[peer.PeerID] = true
	}
	dead := []uint64{}
	for _, ordinal := range silent {
		log, err := r.Kubernetes.CrashLog(ctx, fmt.Sprintf("%s-%d", spec.ReleaseName(), ordinal), spec.Namespace, CrashLogLines)
		if err != nil {
			return nil, fmt.Errorf("crash log of pod %d: %w", ordinal, err)
		}
		peerID, ok := domain.ReplacedPeer(log)
		if !ok || !known[peerID] || slices.Contains(dead, peerID) {
			continue
		}
		dead = append(dead, peerID)
	}
	slices.Sort(dead)
	return dead, nil
}

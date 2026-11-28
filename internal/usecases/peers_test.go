package usecases_test

import (
	"context"
	"slices"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func recoverOK(t *testing.T, use usecases.RecoverDeadPeers, spec domain.ClusterSpec, current domain.ClusterStatus) domain.ClusterStatus {
	t.Helper()
	status, err := use.Execute(context.Background(), spec, current)
	clusterOK(t, err)
	return status
}

func rebalanceOK(t *testing.T, use usecases.RebalanceShards, spec domain.ClusterSpec, current domain.ClusterStatus) domain.ClusterStatus {
	t.Helper()
	status, err := use.Execute(context.Background(), spec, current)
	clusterOK(t, err)
	return status
}

func shardPeers(qdrant *fakes.Qdrant) [][2]uint64 {
	pairs := [][2]uint64{}
	for _, replica := range qdrant.Replicas["docs"] {
		pairs = append(pairs, [2]uint64{uint64(replica.ShardID), replica.PeerID})
	}
	slices.SortFunc(pairs, func(a [2]uint64, b [2]uint64) int {
		if a[0] != b[0] {
			return int(a[0]) - int(b[0])
		}
		return int(a[1]) - int(b[1])
	})
	return pairs
}

func recoverySpec(t *testing.T) domain.ClusterSpec {
	return clusterSpec(t, 3, domain.JSON{"scaling": domain.JSON{"removeDeadPeers": true}})
}

func TestDeadPeerIsRestoredFromActiveReplicasThenRemoved(t *testing.T) {
	kubernetes := clusterKubernetes()
	settledStatefulSet(kubernetes, 3)
	qdrant := rebuiltPodQdrant()
	qdrant.SlowTransfers = true
	use := usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}
	spec := recoverySpec(t)

	restoring := recoverOK(t, use, spec, clusterPending())

	clusterAssertEqual(t, conditionsOf(restoring)["DeadPeers"], conditionState{domain.ConditionTrue, "RestoringReplicas"})
	clusterAssertEqual(t, len(qdrant.ShardOperations), 1)
	first := qdrant.ShardOperations[0]
	clusterAssertEqual(t, []any{first.Kind, first.ShardID, first.PeerID}, []any{domain.ShardReplicate, 0, uint64(100)})
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)

	clusterOK(t, qdrant.Settle())
	second := recoverOK(t, use, spec, restoring)
	issued := [][2]uint64{}
	for _, operation := range qdrant.ShardOperations {
		issued = append(issued, [2]uint64{uint64(operation.ShardID), operation.PeerID})
	}
	clusterAssertEqual(t, issued, [][2]uint64{{0, 100}, {1, 102}})
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)

	clusterOK(t, qdrant.Settle())
	done := recoverOK(t, use, spec, second)

	clusterAssertEqual(t, qdrant.RemovedPeers, []fakes.RemovedPeer{{PeerID: 101, Force: true}})
	clusterAssertEqual(t, conditionsOf(done)["DeadPeers"], conditionState{domain.ConditionFalse, "DeadPeersRemoved"})
	clusterAssertEqual(t, shardPeers(qdrant), [][2]uint64{{0, 100}, {0, 201}, {1, 100}, {1, 102}})
}

func TestDeadPeerWithTheOnlyCopyIsReportedNotRemoved(t *testing.T) {
	kubernetes := clusterKubernetes()
	settledStatefulSet(kubernetes, 3)
	qdrant := rebuiltPodQdrant()
	qdrant.Replicas["docs"] = []domain.Replica{{ShardID: 0, PeerID: 101, State: domain.ShardActive}}

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, conditionsOf(status)["DeadPeers"].Reason, "ShardsLost")
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)
}

func TestPeersAreNeverCalledDeadWhileAPodIsUnreachableOrNotReady(t *testing.T) {
	kubernetes := clusterKubernetes()
	qdrant := rebuiltPodQdrant()
	use := usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}

	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 2}
	notReady := recoverOK(t, use, clusterSpec(t, 3, nil), clusterPending())
	settledStatefulSet(kubernetes, 3)
	delete(qdrant.NodePeerIDs, clusterPodURL(2))
	unreachable := recoverOK(t, use, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, notReady, clusterPending())
	clusterAssertEqual(t, unreachable, clusterPending())
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)
}

func TestHealthyClusterReportsNoDeadPeers(t *testing.T) {
	kubernetes := clusterKubernetes()
	settledStatefulSet(kubernetes, 3)

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: peeredQdrant(3), Kubernetes: kubernetes, Clock: clusterClock}, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, conditionsOf(status)["DeadPeers"], conditionState{domain.ConditionFalse, "NoDeadPeers"})
}

func TestAPodCrashLoopingOnAnEmptyVolumeGetsItsOldPeerRepaired(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 2, Partition: 3}
	kubernetes.PodLogs[clusterNS+"/"+clusterRelease+"-1"] = "peer URI " + clusterPeerURI(1) + " already used by peer 101 which still has shards"
	qdrant := crashLoopingPodQdrant()
	use := usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}
	spec := recoverySpec(t)

	first := recoverOK(t, use, spec, clusterPending())
	second := recoverOK(t, use, spec, first)
	done := recoverOK(t, use, spec, second)

	issued := [][2]any{}
	for _, operation := range qdrant.ShardOperations {
		issued = append(issued, [2]any{operation.Kind, operation.ShardID})
	}
	clusterAssertEqual(t, issued, [][2]any{{domain.ShardReplicate, 0}, {domain.ShardReplicate, 1}})
	clusterAssertEqual(t, qdrant.RemovedPeers, []fakes.RemovedPeer{{PeerID: 101, Force: true}})
	clusterAssertEqual(t, conditionsOf(done)["DeadPeers"], conditionState{domain.ConditionFalse, "DeadPeersRemoved"})
}

func TestASilentPodWithoutTheJoinErrorIsLeftAlone(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 2}
	kubernetes.PodLogs[clusterNS+"/"+clusterRelease+"-1"] = "OOMKilled"
	qdrant := crashLoopingPodQdrant()

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, recoverySpec(t), clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
}

func TestRebalanceSpreadsShardsOntoANewPeerAfterScaleUp(t *testing.T) {
	kubernetes := clusterKubernetes()
	settledStatefulSet(kubernetes, 3)
	qdrant := peeredQdrant(3)
	qdrant.Replicas["docs"] = []domain.Replica{}
	for shard := range 4 {
		qdrant.Replicas["docs"] = append(qdrant.Replicas["docs"], domain.Replica{ShardID: shard, PeerID: uint64(100 + shard%2), State: domain.ShardActive})
	}
	use := usecases.RebalanceShards{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}
	spec := clusterSpec(t, 3, domain.JSON{"scaling": domain.JSON{"rebalance": true}})

	moving := rebalanceOK(t, use, spec, clusterPending())
	balanced := rebalanceOK(t, use, spec, moving)

	clusterAssertEqual(t, conditionsOf(moving)["Rebalancing"], conditionState{domain.ConditionTrue, "Rebalancing"})
	clusterAssertEqual(t, len(qdrant.ShardOperations), 1)
	operation := qdrant.ShardOperations[0]
	clusterAssertEqual(t, []any{operation.Kind, operation.PeerID, *operation.TargetPeerID}, []any{domain.ShardMove, uint64(101), uint64(102)})
	clusterAssertEqual(t, conditionsOf(balanced)["Rebalancing"], conditionState{domain.ConditionFalse, "Balanced"})
}

func TestRebalanceEvensOutReplicasLeftUnevenByARestore(t *testing.T) {
	kubernetes := clusterKubernetes()
	settledStatefulSet(kubernetes, 3)
	qdrant := peeredQdrant(3)
	qdrant.ReplicationFactors["docs"] = 2
	qdrant.Replicas["docs"] = []domain.Replica{
		{ShardID: 0, PeerID: 100, State: domain.ShardActive},
		{ShardID: 0, PeerID: 102, State: domain.ShardActive},
		{ShardID: 1, PeerID: 101, State: domain.ShardActive},
		{ShardID: 1, PeerID: 102, State: domain.ShardActive},
		{ShardID: 2, PeerID: 101, State: domain.ShardActive},
		{ShardID: 2, PeerID: 102, State: domain.ShardActive},
	}
	use := usecases.RebalanceShards{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}
	spec := clusterSpec(t, 3, domain.JSON{"scaling": domain.JSON{"rebalance": true}})
	perPeer := func() map[uint64]int {
		counts := map[uint64]int{}
		for _, replica := range qdrant.Replicas["docs"] {
			counts[replica.PeerID]++
		}
		return counts
	}

	clusterAssertEqual(t, perPeer(), map[uint64]int{100: 1, 101: 2, 102: 3})
	moving := rebalanceOK(t, use, spec, clusterPending())
	balanced := rebalanceOK(t, use, spec, moving)
	again := rebalanceOK(t, use, spec, balanced)

	clusterAssertEqual(t, len(qdrant.ShardOperations), 1)
	operation := qdrant.ShardOperations[0]
	clusterAssertEqual(t, []any{operation.Kind, operation.ShardID, operation.PeerID, *operation.TargetPeerID}, []any{domain.ShardMove, 1, uint64(102), uint64(100)})
	clusterAssertEqual(t, perPeer(), map[uint64]int{100: 2, 101: 2, 102: 2})
	clusterAssertEqual(t, conditionsOf(moving)["Rebalancing"], conditionState{domain.ConditionTrue, "Rebalancing"})
	clusterAssertEqual(t, conditionsOf(balanced)["Rebalancing"], conditionState{domain.ConditionFalse, "Balanced"})
	clusterAssertEqual(t, conditionsOf(again)["Rebalancing"], conditionState{domain.ConditionFalse, "Balanced"})
}

func TestRebalanceWaitsWhileTheStatefulSetIsNotSettled(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 2}
	qdrant := peeredQdrant(3)
	qdrant.Replicas["docs"] = []domain.Replica{}
	for shard := range 3 {
		qdrant.Replicas["docs"] = append(qdrant.Replicas["docs"], domain.Replica{ShardID: shard, PeerID: 100, State: domain.ShardActive})
	}

	status := rebalanceOK(t, usecases.RebalanceShards{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
}

package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const (
	revisionOld = "rev-old"
	revisionNew = "rev-new"
)

func rolloutStatefulSet(kubernetes *fakes.Kubernetes, partition int, revisions []string, current string) {
	updated := 0
	pods := []domain.PodState{}
	for ordinal, revision := range revisions {
		if revision == revisionNew {
			updated++
		}
		pods = append(pods, domain.PodState{Name: fmt.Sprintf("%s-%d", clusterRelease, ordinal), Revision: revision, Ready: true})
	}
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{
		Replicas:        len(revisions),
		ReadyReplicas:   len(revisions),
		UpdatedReplicas: updated,
		CurrentRevision: current,
		UpdateRevision:  revisionNew,
		Partition:       partition,
	}
	kubernetes.Pods[clusterNS] = pods
}

func rolloutSetPod(kubernetes *fakes.Kubernetes, ordinal int, revision string, ready bool) {
	name := fmt.Sprintf("%s-%d", clusterRelease, ordinal)
	pods := []domain.PodState{}
	for _, pod := range kubernetes.Pods[clusterNS] {
		if pod.Name == name {
			pod = domain.PodState{Name: name, Revision: revision, Ready: ready}
		}
		pods = append(pods, pod)
	}
	kubernetes.Pods[clusterNS] = pods
}

func rolloutPartition(kubernetes *fakes.Kubernetes) int {
	return kubernetes.StatefulSets[clusterStsKey].Partition
}

func stepOK(t *testing.T, kubernetes *fakes.Kubernetes, qdrant *fakes.Qdrant, spec domain.ClusterSpec, current domain.ClusterStatus) domain.ClusterStatus {
	t.Helper()
	status, err := usecases.StepRollout{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}.Execute(context.Background(), spec, current)
	clusterOK(t, err)
	return status
}

func readinessSpec(t *testing.T, extra domain.JSON) domain.ClusterSpec {
	spec := domain.JSON{"rollout": domain.JSON{"strategy": "Readiness"}}
	for key, value := range extra {
		spec[key] = value
	}
	return clusterSpec(t, 3, spec)
}

func TestStepReleasesOnePodAndReportsProgress(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)

	status := stepOK(t, kubernetes, peeredQdrant(3), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	clusterAssertEqual(t, status.Phase, domain.PhaseUpgrading)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"], conditionState{domain.ConditionTrue, "RestartingPod"})
	if !strings.Contains(conditionMessage(status, "Progressing"), "2") {
		t.Errorf("message %q", conditionMessage(status, "Progressing"))
	}
}

func TestStepWaitsForShardsAndNamesTheReplicas(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	qdrant := peeredQdrant(3)
	qdrant.Replicas["docs"] = []domain.Replica{{ShardID: 0, PeerID: 100, State: domain.ShardActive}, {ShardID: 1, PeerID: 102, State: "Partial"}}

	status := stepOK(t, kubernetes, qdrant, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"], conditionState{domain.ConditionTrue, "WaitingForShards"})
	if !strings.Contains(conditionMessage(status, "Progressing"), "docs/1@102 is Partial") {
		t.Errorf("message %q", conditionMessage(status, "Progressing"))
	}
}

func TestStepWaitsWhileAShardTransferRuns(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	qdrant := peeredQdrant(3)
	target := uint64(101)
	qdrant.Pending = append(qdrant.Pending, domain.ShardOperation{Kind: domain.ShardReplicate, Collection: "docs", ShardID: 0, PeerID: 100, TargetPeerID: &target})

	status := stepOK(t, kubernetes, qdrant, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"].Reason, "WaitingForShards")
}

func TestStepWaitsWhenANodeDoesNotAnswer(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	qdrant := peeredQdrant(3)
	delete(qdrant.NodePeerIDs, clusterPodURL(1))

	status := stepOK(t, kubernetes, qdrant, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	if !strings.Contains(conditionMessage(status, "Progressing"), "node 1 does not answer") {
		t.Errorf("message %q", conditionMessage(status, "Progressing"))
	}
}

func TestStepWaitsWhenAPeerIsMissing(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	qdrant := peeredQdrant(3)
	delete(qdrant.Peers, 102)

	status := stepOK(t, kubernetes, qdrant, clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"].Reason, "WaitingForShards")
}

func TestStepWaitsWhenAPodIsNotReady(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	rolloutSetPod(kubernetes, 2, revisionNew, false)

	status := stepOK(t, kubernetes, peeredQdrant(3), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"].Reason, "RestartingPod")
}

func TestFullRolloutWalksTheOrdinalsThenHolds(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)
	spec := clusterSpec(t, 3, nil)
	qdrant := peeredQdrant(3)
	status := clusterPending()

	for _, ordinal := range []int{2, 1, 0} {
		status = stepOK(t, kubernetes, qdrant, spec, status)
		clusterAssertEqual(t, rolloutPartition(kubernetes), ordinal)
		rolloutSetPod(kubernetes, ordinal, revisionNew, true)
	}
	stepOK(t, kubernetes, qdrant, spec, status)

	clusterAssertEqual(t, rolloutPartition(kubernetes), 3)
	clusterAssertEqual(t, kubernetes.Partitions, []int{2, 1, 0, 3})
}

func TestIdleClusterGetsItsHoldAndNoProgressCondition(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 0, []string{revisionNew, revisionNew, revisionNew}, revisionNew)

	status := stepOK(t, kubernetes, peeredQdrant(3), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 3)
	clusterAssertEqual(t, status, clusterPending())
}

func TestIdleClusterDoesNotQueryQdrant(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionNew, revisionNew, revisionNew}, revisionNew)

	stepOK(t, kubernetes, fakes.NewQdrant(), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, len(kubernetes.Partitions), 0)
}

func TestResumedOperatorContinuesFromTheClusterState(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 1, []string{revisionOld, revisionNew, revisionNew}, revisionOld)

	stepOK(t, kubernetes, peeredQdrant(3), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 0)
}

func TestReadinessStrategyOnlyLiftsTheHold(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)

	status := stepOK(t, kubernetes, fakes.NewQdrant(), readinessSpec(t, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 0)
	clusterAssertEqual(t, status, clusterPending())
}

func TestStepDoesNothingBeforeTheStatefulSetExists(t *testing.T) {
	kubernetes := clusterKubernetes()

	status := stepOK(t, kubernetes, fakes.NewQdrant(), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(kubernetes.Partitions), 0)
}

func TestSingleNonDistributedReplicaRollsOnReadinessAlone(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 1, []string{revisionOld}, revisionOld)
	spec := clusterSpec(t, 1, domain.JSON{"cluster": domain.JSON{"enabled": false}})

	stepOK(t, kubernetes, fakes.NewQdrant(), spec, clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 0)
}

func TestSwitchingToShardHealthReachesThePartitionThroughTheStep(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 0, []string{revisionNew, revisionNew, revisionNew}, revisionNew)

	stepOK(t, kubernetes, peeredQdrant(3), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 3)
}

func TestSwitchingToReadinessReachesThePartitionThroughTheStep(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)

	stepOK(t, kubernetes, fakes.NewQdrant(), readinessSpec(t, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 0)
}

func TestReconcileRendersThePartitionIntoTheRelease(t *testing.T) {
	cases := []struct {
		name      string
		partition int
		revisions []string
		current   string
		spec      domain.ClusterSpec
		want      int
		bare      bool
	}{
		{name: "hold on an idle cluster", partition: 0, revisions: []string{revisionNew, revisionNew, revisionNew}, current: revisionNew, spec: clusterSpec(t, 3, nil), want: 3},
		{name: "hold mid rollout", partition: 1, revisions: []string{revisionOld, revisionNew, revisionNew}, current: revisionOld, spec: clusterSpec(t, 3, nil), want: 3},
		{name: "new replica count on scale up", partition: 3, revisions: []string{revisionNew, revisionNew, revisionNew}, current: revisionNew, spec: clusterSpec(t, 4, nil), want: 4},
		{name: "hold on first install", spec: clusterSpec(t, 3, nil), want: 3, bare: true},
		{name: "zero with readiness", partition: 3, revisions: []string{revisionOld, revisionOld, revisionOld}, current: revisionOld, spec: readinessSpec(t, nil), want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			helm := fakes.NewCountingHelm()
			kubernetes := clusterKubernetes()
			if !tc.bare {
				rolloutStatefulSet(kubernetes, tc.partition, tc.revisions, tc.current)
			}

			reconcileOK(t, clusterReconciler(helm, kubernetes, fakes.NewQdrant()), tc.spec, 1, clusterPending())

			release, found := helm.Release(clusterRelease, clusterNS)
			if !found {
				t.Fatal("no release applied")
			}
			clusterAssertEqual(t, []any{release.StatefulSet, release.Partition}, []any{clusterRelease, tc.want})
			clusterAssertEqual(t, len(kubernetes.Partitions), 0)
		})
	}
}

func TestUnchangedReconcileLeavesARunningRolloutAlone(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	use := clusterReconciler(helm, kubernetes, fakes.NewQdrant())
	spec := clusterSpec(t, 3, nil)
	first := reconcileOK(t, use, spec, 1, clusterPending())
	rolloutStatefulSet(kubernetes, 1, []string{revisionOld, revisionNew, revisionNew}, revisionOld)
	kubernetes.Partitions = nil

	reconcileOK(t, use, spec, 1, first)

	clusterAssertEqual(t, rolloutPartition(kubernetes), 1)
	clusterAssertEqual(t, len(kubernetes.Partitions), 0)
}

func TestScaleDownDrainWaitsForARunningRollout(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	qdrant := peeredQdrant(3)
	spec := clusterSpec(t, 2, domain.JSON{"scaling": domain.JSON{"drainOnScaleDown": true}})

	_, err := clusterReconciler(helm, kubernetes, qdrant).Execute(context.Background(), spec, 1, clusterPending())

	var waiting *usecases.RolloutInProgressError
	if !errors.As(err, &waiting) || waiting.Status == nil {
		t.Fatalf("want RolloutInProgressError with a status, got %v", err)
	}
	clusterAssertEqual(t, helm.ApplyCalls(), 0)
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
	clusterAssertEqual(t, conditionsOf(*waiting.Status)["ScalingDown"], conditionState{domain.ConditionTrue, "WaitingForRollout"})
}

func TestVolumeRecreationWaitsForARunningRollout(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "10Gi"}
	kubernetes.VolumeClaims[clusterNS] = claimSet(3)
	spec := clusterSpec(t, 3, domain.JSON{"snapshotPersistence": domain.JSON{"enabled": true, "size": "5Gi"}})

	_, err := clusterReconciler(helm, kubernetes, fakes.NewQdrant()).Execute(context.Background(), spec, 1, clusterPending())

	var waiting *usecases.RolloutInProgressError
	if !errors.As(err, &waiting) {
		t.Fatalf("want RolloutInProgressError, got %v", err)
	}
	clusterAssertEqual(t, len(kubernetes.Orphaned), 0)
	clusterAssertEqual(t, helm.ApplyCalls(), 0)
}

func TestObserveReportsAHeldRolloutAsNotReady(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)

	status, err := usecases.ObserveCluster{Kubernetes: kubernetes, Clock: clusterClock}.Execute(context.Background(), clusterSpec(t, 3, nil), clusterPending())
	clusterOK(t, err)

	if status.Phase == domain.PhaseRunning {
		t.Error("held rollout reported as Running")
	}
	clusterAssertEqual(t, conditionsOf(status)["Ready"].Status, domain.ConditionFalse)
}

func TestObserveReportsCompleteOnceEveryPodIsUpdated(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 3, CurrentRevision: revisionNew, UpdateRevision: revisionNew, Partition: 3}

	status, err := usecases.ObserveCluster{Kubernetes: kubernetes, Clock: clusterClock}.Execute(context.Background(), clusterSpec(t, 3, nil), clusterPending())
	clusterOK(t, err)

	clusterAssertEqual(t, status.Phase, domain.PhaseRunning)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"], conditionState{domain.ConditionFalse, "RolloutComplete"})
}

func TestRebalanceWaitsForAHeldRollout(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)
	qdrant := peeredQdrant(3)
	qdrant.Replicas["docs"] = []domain.Replica{{ShardID: 0, PeerID: 100, State: domain.ShardActive}}
	spec := clusterSpec(t, 3, domain.JSON{"scaling": domain.JSON{"rebalance": true}})

	status := rebalanceOK(t, usecases.RebalanceShards{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, spec, clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
}

func TestHeldRolloutWithALeftoverDeadPeerGetsThePeerRemoved(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)
	qdrant := rebuiltPodQdrant()
	spec := recoverySpec(t)
	recovery := usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}
	status := clusterPending()

	for range 4 {
		status = recoverOK(t, recovery, spec, status)
		clusterOK(t, qdrant.Settle())
	}

	clusterAssertEqual(t, qdrant.RemovedPeers, []fakes.RemovedPeer{{PeerID: 101, Force: true}})
	clusterAssertEqual(t, conditionsOf(status)["DeadPeers"], conditionState{domain.ConditionFalse, "NoDeadPeers"})
	stepOK(t, kubernetes, qdrant, spec, status)
	clusterAssertEqual(t, rolloutPartition(kubernetes), 2)
}

func TestRolloutWaitsOnADeadPeerUntilItIsRemoved(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 3, []string{revisionOld, revisionOld, revisionOld}, revisionOld)

	status := stepOK(t, kubernetes, rebuiltPodQdrant(), clusterSpec(t, 3, nil), clusterPending())

	clusterAssertEqual(t, rolloutPartition(kubernetes), 3)
	clusterAssertEqual(t, conditionsOf(status)["Progressing"].Reason, "WaitingForShards")
}

func TestNoRecoveryWhileAReleasedPodIsMidRestart(t *testing.T) {
	cases := []struct {
		name  string
		ready []bool
	}{
		{name: "last pod not ready", ready: []bool{true, true, false}},
		{name: "last pod on the old revision", ready: []bool{true, true, true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kubernetes := clusterKubernetes()
			rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionOld}, revisionOld)
			for ordinal, flag := range tc.ready {
				rolloutSetPod(kubernetes, ordinal, revisionOld, flag)
			}
			qdrant := rebuiltPodQdrant()

			status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, recoverySpec(t), clusterPending())

			clusterAssertEqual(t, status, clusterPending())
			clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
			clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)
		})
	}
}

func TestRecoveryResumesOnceTheReleasedPodIsUpdatedAndReady(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 2, []string{revisionOld, revisionOld, revisionNew}, revisionOld)

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: rebuiltPodQdrant(), Kubernetes: kubernetes, Clock: clusterClock}, recoverySpec(t), clusterPending())

	clusterAssertEqual(t, conditionsOf(status)["DeadPeers"].Reason, "RestoringReplicas")
}

func TestRecoveryWaitsWhenTheStatefulSetSizeDiffersFromTheSpec(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 4, []string{revisionOld, revisionOld, revisionOld, revisionOld}, revisionOld)
	qdrant := rebuiltPodQdrant()

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, recoverySpec(t), clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)
}

func TestReadinessRecoveryStillWaitsForARollingStatefulSet(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 0, []string{revisionOld, revisionOld, revisionOld}, revisionOld)
	qdrant := rebuiltPodQdrant()
	spec := readinessSpec(t, domain.JSON{"scaling": domain.JSON{"removeDeadPeers": true}})

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: qdrant, Kubernetes: kubernetes, Clock: clusterClock}, spec, clusterPending())

	clusterAssertEqual(t, status, clusterPending())
	clusterAssertEqual(t, len(qdrant.RemovedPeers), 0)
}

func TestReadinessRecoveryRunsOnceTheStatefulSetSettles(t *testing.T) {
	kubernetes := clusterKubernetes()
	rolloutStatefulSet(kubernetes, 0, []string{revisionNew, revisionNew, revisionNew}, revisionNew)
	spec := readinessSpec(t, domain.JSON{"scaling": domain.JSON{"removeDeadPeers": true}})

	status := recoverOK(t, usecases.RecoverDeadPeers{Qdrant: rebuiltPodQdrant(), Kubernetes: kubernetes, Clock: clusterClock}, spec, clusterPending())

	clusterAssertEqual(t, conditionsOf(status)["DeadPeers"].Reason, "RestoringReplicas")
}

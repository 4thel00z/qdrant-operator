package usecases_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

type counterReader struct{ next byte }

func (r *counterReader) Read(p []byte) (int, error) {
	for index := range p {
		r.next++
		p[index] = r.next
	}
	return len(p), nil
}

func clusterReconciler(helm ports.Helm, kubernetes *fakes.Kubernetes, qdrant *fakes.Qdrant) usecases.ReconcileCluster {
	return usecases.ReconcileCluster{Helm: helm, Kubernetes: kubernetes, Qdrant: qdrant, Clock: clusterClock, Random: &counterReader{}}
}

func clusterOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func reconcileOK(t *testing.T, use usecases.ReconcileCluster, spec domain.ClusterSpec, generation int64, current domain.ClusterStatus) domain.ClusterStatus {
	t.Helper()
	status, err := use.Execute(context.Background(), spec, generation, current)
	clusterOK(t, err)
	return status
}

func TestReconcileAppliesReleaseAndReportsPendingThenUpgrading(t *testing.T) {
	helm := fakes.NewCountingHelm()
	spec := clusterSpec(t, 2, domain.JSON{"version": "v1.16.3"})
	use := clusterReconciler(helm, fakes.NewKubernetes(), fakes.NewQdrant())

	first := reconcileOK(t, use, spec, 1, clusterPending())
	spec.Replicas = 3
	second := reconcileOK(t, use, spec, 2, first)

	release, _ := helm.Release(clusterRelease, clusterNS)
	if release.ChartVersion != "1.16.3" || release.Values["replicaCount"] != 3 {
		t.Errorf("release %+v", release)
	}
	if first.Phase != domain.PhasePending || second.Phase != domain.PhaseUpgrading {
		t.Errorf("phases %s, %s", first.Phase, second.Phase)
	}
	if second.ObservedGeneration != 2 || second.HelmRelease != clusterRelease {
		t.Errorf("status %+v", second)
	}
	if len(second.Conditions) != 1 || second.Conditions[0].Type != "Progressing" {
		t.Errorf("conditions %+v", second.Conditions)
	}
}

func TestObserveLiftsStatefulSetReadinessIntoStatus(t *testing.T) {
	ctx := context.Background()
	kubernetes := clusterKubernetes()
	spec := clusterSpec(t, 2, nil)
	use := usecases.ObserveCluster{Kubernetes: kubernetes, Clock: clusterClock}

	waiting, err := use.Execute(ctx, spec, domain.ClusterStatus{Phase: domain.PhaseUpgrading})
	clusterOK(t, err)
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2}
	running, err := use.Execute(ctx, spec, waiting)
	clusterOK(t, err)

	if waiting.Phase != domain.PhaseUpgrading || waiting.ReadyReplicas != 0 {
		t.Errorf("waiting %+v", waiting)
	}
	if running.Phase != domain.PhaseRunning {
		t.Errorf("running phase %s", running.Phase)
	}
	clusterAssertEqual(t, conditionsOf(running), map[string]conditionState{
		"Ready":       {domain.ConditionTrue, "AllReplicasReady"},
		"Progressing": {domain.ConditionFalse, "RolloutComplete"},
	})
}

func TestObserveKeepsHelmApplyFailureUntilApplySucceeds(t *testing.T) {
	ctx := context.Background()
	kubernetes := clusterKubernetes()
	helm := fakes.NewCountingHelm()
	spec := clusterSpec(t, 2, nil)
	reconcile := clusterReconciler(helm, kubernetes, fakes.NewQdrant())
	observe := usecases.ObserveCluster{Kubernetes: kubernetes, Clock: clusterClock}
	applied := reconcileOK(t, reconcile, spec, 1, clusterPending())
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2}
	helm.ApplyErr = errors.New("rejected by helm")
	grown := clusterSpec(t, 3, nil)

	_, err := reconcile.Execute(ctx, grown, 2, applied)
	var failed *usecases.ReleaseApplyError
	if !errors.As(err, &failed) {
		t.Fatalf("want ReleaseApplyError, got %v", err)
	}
	observed, err := observe.Execute(ctx, spec, failed.Status)
	clusterOK(t, err)
	clusterAssertEqual(t, conditionsOf(observed)["Progressing"], conditionState{domain.ConditionFalse, "HelmApplyFailed"})

	helm.ApplyErr = nil
	recovered := reconcileOK(t, reconcile, grown, 2, observed)
	clusterAssertEqual(t, conditionsOf(recovered)["Progressing"].Reason, "HelmReleaseApplied")
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3}
	healed, err := observe.Execute(ctx, grown, recovered)
	clusterOK(t, err)
	clusterAssertEqual(t, conditionsOf(healed)["Progressing"].Reason, "RolloutComplete")
}

func TestReconcileReappliesWhenSpecReturnsToAppliedAfterAFailure(t *testing.T) {
	ctx := context.Background()
	helm := fakes.NewCountingHelm()
	spec := clusterSpec(t, 2, nil)
	reconcile := clusterReconciler(helm, clusterKubernetes(), fakes.NewQdrant())
	applied := reconcileOK(t, reconcile, spec, 1, clusterPending())
	helm.ApplyErr = errors.New("rejected by helm")
	_, err := reconcile.Execute(ctx, clusterSpec(t, 3, nil), 2, applied)
	var failed *usecases.ReleaseApplyError
	if !errors.As(err, &failed) {
		t.Fatalf("want ReleaseApplyError, got %v", err)
	}
	helm.ApplyErr = nil

	reverted := reconcileOK(t, reconcile, spec, 3, failed.Status)

	clusterAssertEqual(t, conditionsOf(reverted)["Progressing"].Reason, "HelmReleaseApplied")
	clusterAssertEqual(t, helm.ApplyCalls(), 3)

	again := reconcileOK(t, reconcile, spec, 4, reverted)

	clusterAssertEqual(t, helm.ApplyCalls(), 3)
	clusterAssertEqual(t, again.ObservedGeneration, int64(4))
}

func TestDeleteClusterOnlyUninstallsExistingRelease(t *testing.T) {
	ctx := context.Background()
	helm := fakes.NewHelm()
	kubernetes := clusterKubernetes()
	spec := clusterSpec(t, 1, nil)
	kubernetes.VolumeClaims[clusterNS] = map[string]string{"qdrant-storage-qdrant-db-0": "10Gi"}
	use := usecases.DeleteCluster{Helm: helm, Kubernetes: kubernetes}

	clusterOK(t, use.Execute(ctx, spec))
	clusterOK(t, helm.Apply(ctx, ports.ReleaseRequest{Name: clusterRelease, Namespace: clusterNS, ChartVersion: "1.16.3", Values: domain.JSON{}, StatefulSet: clusterRelease}))
	clusterOK(t, use.Execute(ctx, spec))

	clusterAssertEqual(t, len(helm.Releases()), 0)
	clusterAssertEqual(t, kubernetes.VolumeClaims[clusterNS], map[string]string{"qdrant-storage-qdrant-db-0": "10Gi"})
}

func TestDeleteClusterDeletesVolumeClaimsWhenAsked(t *testing.T) {
	ctx := context.Background()
	helm := fakes.NewHelm()
	kubernetes := clusterKubernetes()
	spec := clusterSpec(t, 1, domain.JSON{"persistence": domain.JSON{"reclaimPolicy": "Delete"}})
	clusterOK(t, helm.Apply(ctx, ports.ReleaseRequest{Name: clusterRelease, Namespace: clusterNS, ChartVersion: "1.16.3", Values: domain.JSON{}, StatefulSet: clusterRelease}))
	kubernetes.VolumeClaims[clusterNS] = map[string]string{
		"qdrant-storage-qdrant-db-0":   "10Gi",
		"qdrant-snapshots-qdrant-db-0": "10Gi",
	}

	clusterOK(t, usecases.DeleteCluster{Helm: helm, Kubernetes: kubernetes}.Execute(ctx, spec))

	clusterAssertEqual(t, len(helm.Releases()), 0)
	clusterAssertEqual(t, len(kubernetes.VolumeClaims[clusterNS]), 0)
}

func TestReconcileSkipsHelmWhenTheRenderedValuesAreUnchanged(t *testing.T) {
	helm := fakes.NewCountingHelm()
	use := clusterReconciler(helm, clusterKubernetes(), fakes.NewQdrant())
	spec := clusterSpec(t, 1, nil)

	first := reconcileOK(t, use, spec, 1, clusterPending())
	resumed := reconcileOK(t, use, spec, 1, first)
	changed := reconcileOK(t, use, clusterSpec(t, 1, domain.JSON{"resources": domain.JSON{"limits": domain.JSON{"cpu": "1"}}}), 2, resumed)

	clusterAssertEqual(t, helm.ApplyCalls(), 2)
	clusterAssertEqual(t, resumed.Phase, first.Phase)
	if first.AppliedHash == "" || first.AppliedHash == changed.AppliedHash {
		t.Errorf("hashes %q, %q", first.AppliedHash, changed.AppliedHash)
	}
}

func TestReconcileReappliesWhenTheReleaseIsGone(t *testing.T) {
	ctx := context.Background()
	helm := fakes.NewCountingHelm()
	use := clusterReconciler(helm, clusterKubernetes(), fakes.NewQdrant())
	first := reconcileOK(t, use, clusterSpec(t, 1, nil), 1, clusterPending())
	clusterOK(t, helm.Uninstall(ctx, clusterRelease, clusterNS))

	reconcileOK(t, use, clusterSpec(t, 1, nil), 1, first)

	clusterAssertEqual(t, helm.ApplyCalls(), 2)
}

func TestVolumeGrowthPatchesEachClaimAndOrphansTheStatefulSet(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "5Gi"}
	kubernetes.VolumeClaims[clusterNS] = map[string]string{"qdrant-storage-qdrant-db-0": "5Gi", "qdrant-storage-qdrant-db-1": "5Gi"}
	use := clusterReconciler(helm, kubernetes, fakes.NewQdrant())
	spec := clusterSpec(t, 2, domain.JSON{"persistence": domain.JSON{"size": "40Gi"}})

	status := reconcileOK(t, use, spec, 2, domain.ClusterStatus{Phase: domain.PhaseRunning})

	clusterAssertEqual(t, kubernetes.Resized, []fakes.ClaimResize{
		{Name: "qdrant-storage-qdrant-db-0", Size: "40Gi"},
		{Name: "qdrant-storage-qdrant-db-1", Size: "40Gi"},
	})
	clusterAssertEqual(t, kubernetes.Orphaned, []string{clusterStsKey})
	clusterAssertEqual(t, helm.ApplyCalls(), 1)
	clusterAssertEqual(t, status.Phase, domain.PhasePending)
}

func TestVolumeGrowthSkipsClaimsAlreadyLargeEnough(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "5Gi"}
	kubernetes.VolumeClaims[clusterNS] = map[string]string{"qdrant-storage-qdrant-db-0": "80Gi"}
	use := clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant())

	reconcileOK(t, use, clusterSpec(t, 1, domain.JSON{"persistence": domain.JSON{"size": "40Gi"}}), 1, clusterPending())

	clusterAssertEqual(t, len(kubernetes.Resized), 0)
	clusterAssertEqual(t, kubernetes.Orphaned, []string{clusterStsKey})
}

func TestMatchingClaimsLeaveTheStatefulSetAlone(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "10Gi"}
	kubernetes.VolumeClaims[clusterNS] = map[string]string{"qdrant-storage-qdrant-db-0": "10Gi"}

	reconcileOK(t, clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant()), clusterSpec(t, 1, nil), 1, clusterPending())

	clusterAssertEqual(t, len(kubernetes.Orphaned), 0)
	clusterAssertEqual(t, len(kubernetes.Resized), 0)
}

func TestShrinkingAClaimIsRefused(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "40Gi"}
	use := clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant())

	_, err := use.Execute(context.Background(), clusterSpec(t, 1, domain.JSON{"persistence": domain.JSON{"size": "10Gi"}}), 1, clusterPending())

	if err == nil || !strings.Contains(err.Error(), "cannot shrink") {
		t.Fatalf("want a shrink refusal, got %v", err)
	}
	clusterAssertEqual(t, len(kubernetes.Orphaned), 0)
}

func TestEnablingSnapshotPersistenceRecreatesTheStatefulSet(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.ClaimTemplates[clusterStsKey] = map[string]string{"qdrant-storage": "10Gi"}
	kubernetes.VolumeClaims[clusterNS] = map[string]string{"qdrant-storage-qdrant-db-0": "10Gi"}
	spec := clusterSpec(t, 1, domain.JSON{"snapshotPersistence": domain.JSON{"enabled": true, "size": "100Gi"}})

	reconcileOK(t, clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant()), spec, 1, clusterPending())

	clusterAssertEqual(t, kubernetes.Orphaned, []string{clusterStsKey})
}

func TestScaleDownWithoutDrainFlagsShardsOnTheRemovedPeer(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3}
	kubernetes.VolumeClaims[clusterNS] = claimSet(3)
	qdrant := peeredQdrant(3)

	status := reconcileOK(t, clusterReconciler(helm, kubernetes, qdrant), clusterSpec(t, 2, nil), 2, clusterPending())

	clusterAssertEqual(t, helm.ApplyCalls(), 1)
	clusterAssertEqual(t, kubernetes.VolumeClaims[clusterNS], claimSet(3))
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
	clusterAssertEqual(t, conditionsOf(status)["ScaleDownUnsafe"], conditionState{domain.ConditionTrue, "ShardsOnRemovedPeers"})
	if !strings.Contains(conditionMessage(status, "ScaleDownUnsafe"), "docs/2") {
		t.Errorf("message %q", conditionMessage(status, "ScaleDownUnsafe"))
	}
}

func TestScaleDownDrainMovesShardsThenRemovesPeersThenApplies(t *testing.T) {
	ctx := context.Background()
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3}
	kubernetes.VolumeClaims[clusterNS] = claimSet(3)
	qdrant := peeredQdrant(3)
	qdrant.SlowTransfers = true
	use := clusterReconciler(helm, kubernetes, qdrant)
	spec := clusterSpec(t, 2, domain.JSON{"scaling": domain.JSON{"drainOnScaleDown": true}})

	_, err := use.Execute(ctx, spec, 2, clusterPending())
	var pending *usecases.ScaleDownPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("want ScaleDownPendingError, got %v", err)
	}
	clusterAssertEqual(t, helm.ApplyCalls(), 0)
	if len(qdrant.ShardOperations) != 1 {
		t.Fatalf("operations %+v", qdrant.ShardOperations)
	}
	operation := qdrant.ShardOperations[0]
	clusterAssertEqual(t, []any{operation.Kind, operation.ShardID, operation.PeerID}, []any{domain.ShardMove, 2, uint64(102)})
	clusterAssertEqual(t, conditionsOf(pending.Status)["ScalingDown"], conditionState{domain.ConditionTrue, "DrainingPeers"})

	_, err = use.Execute(ctx, spec, 2, pending.Status)
	if !errors.As(err, &pending) {
		t.Fatalf("want ScaleDownPendingError, got %v", err)
	}
	clusterAssertEqual(t, len(qdrant.ShardOperations), 1)

	clusterOK(t, qdrant.Settle())
	status := reconcileOK(t, use, spec, 2, pending.Status)

	clusterAssertEqual(t, qdrant.RemovedPeers, []fakes.RemovedPeer{{PeerID: 102, Force: false}})
	if _, present := qdrant.Peers[102]; present {
		t.Error("peer 102 still registered")
	}
	release, _ := helm.Release(clusterRelease, clusterNS)
	clusterAssertEqual(t, release.Values["replicaCount"], any(2))
	clusterAssertEqual(t, kubernetes.VolumeClaims[clusterNS], claimSet(2))
	clusterAssertEqual(t, conditionsOf(status)["ScalingDown"], conditionState{domain.ConditionFalse, "PeersRemoved"})
	holders := []uint64{}
	for _, replica := range qdrant.Replicas["docs"] {
		holders = append(holders, replica.PeerID)
	}
	slices.Sort(holders)
	clusterAssertEqual(t, holders, []uint64{100, 100, 101})
}

func TestScaleDownDrainRefusesToMoveAReplicaThatIsNotActive(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3}
	qdrant := peeredQdrant(3)
	qdrant.Replicas["docs"][2] = domain.Replica{ShardID: 2, PeerID: 102, State: "Dead"}
	spec := clusterSpec(t, 2, domain.JSON{"scaling": domain.JSON{"drainOnScaleDown": true}})

	_, err := clusterReconciler(helm, kubernetes, qdrant).Execute(context.Background(), spec, 2, clusterPending())

	var pending *usecases.ScaleDownPendingError
	if !errors.As(err, &pending) {
		t.Fatalf("want ScaleDownPendingError, got %v", err)
	}
	var unsafe *usecases.ShardsUnsafeError
	if !errors.As(err, &unsafe) {
		t.Errorf("want ShardsUnsafeError in the chain, got %v", err)
	}
	clusterAssertEqual(t, helm.ApplyCalls(), 0)
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
	clusterAssertEqual(t, conditionsOf(pending.Status)["ScalingDown"].Reason, "ShardsUnsafe")
}

func TestScaleUpAppliesWithoutTouchingPeers(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 2, ReadyReplicas: 2}
	qdrant := peeredQdrant(2)

	reconcileOK(t, clusterReconciler(helm, kubernetes, qdrant), clusterSpec(t, 3, domain.JSON{"scaling": domain.JSON{"drainOnScaleDown": true}}), 2, clusterPending())

	clusterAssertEqual(t, helm.ApplyCalls(), 1)
	clusterAssertEqual(t, len(qdrant.ShardOperations), 0)
}

func TestGeneratedAPIKeyIsCreatedOnceAndHandedToHelmByReference(t *testing.T) {
	helm := fakes.NewCountingHelm()
	kubernetes := clusterKubernetes()
	use := clusterReconciler(helm, kubernetes, fakes.NewQdrant())
	autoGenerate := domain.JSON{"apiKey": domain.JSON{"autoGenerate": true}}

	first := reconcileOK(t, use, clusterSpec(t, 1, autoGenerate), 1, clusterPending())
	key := kubernetes.Secrets[clusterNS+"/qdrant-db-generated-keys"]["api-key"]
	reconcileOK(t, use, clusterSpec(t, 2, autoGenerate), 2, first)

	release, _ := helm.Release(clusterRelease, clusterNS)
	clusterAssertEqual(t, len(key), 32)
	clusterAssertEqual(t, kubernetes.Secrets[clusterNS+"/qdrant-db-generated-keys"]["api-key"], key)
	clusterAssertEqual(t, release.Values["apiKey"], any(domain.JSON{
		"valueFrom": domain.JSON{"secretKeyRef": domain.JSON{"name": "qdrant-db-generated-keys", "key": "api-key"}},
	}))
	clusterAssertEqual(t, kubernetes.SecretOwners[clusterNS+"/qdrant-db-generated-keys"].Kind, "QdrantCluster")
}

func TestGeneratedAPIKeyCarriesOverTheKeyTheChartMade(t *testing.T) {
	kubernetes := clusterKubernetes()
	kubernetes.Secrets[clusterNS+"/qdrant-db-apikey"] = map[string]string{"api-key": "chart-made-key"}

	reconcileOK(t, clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant()), clusterSpec(t, 1, domain.JSON{"apiKey": domain.JSON{"autoGenerate": true}}), 1, clusterPending())

	clusterAssertEqual(t, kubernetes.Secrets[clusterNS+"/qdrant-db-generated-keys"], map[string]string{"api-key": "chart-made-key"})
}

func TestClusterWithoutGeneratedKeysCreatesNoSecret(t *testing.T) {
	kubernetes := clusterKubernetes()

	reconcileOK(t, clusterReconciler(fakes.NewHelm(), kubernetes, fakes.NewQdrant()), clusterSpec(t, 1, nil), 1, clusterPending())

	if _, present := kubernetes.Secrets[clusterNS+"/qdrant-db-generated-keys"]; present {
		t.Error("generated keys secret was created")
	}
}

func TestGeneratedAPIKeyWaitsForTheClusterResource(t *testing.T) {
	use := clusterReconciler(fakes.NewHelm(), fakes.NewKubernetes(), fakes.NewQdrant())

	_, err := use.Execute(context.Background(), clusterSpec(t, 1, domain.JSON{"apiKey": domain.JSON{"autoGenerate": true}}), 1, clusterPending())

	if !errors.Is(err, usecases.ErrClusterNotFound) {
		t.Fatalf("want ErrClusterNotFound, got %v", err)
	}
}

func TestAFailedHelmApplyIsReportedInTheStatus(t *testing.T) {
	helm := fakes.NewCountingHelm()
	helm.ApplyErr = errors.New("helm upgrade failed: StatefulSet.apps is invalid")

	_, err := clusterReconciler(helm, clusterKubernetes(), fakes.NewQdrant()).Execute(context.Background(), clusterSpec(t, 3, nil), 4, clusterPending())

	var failed *usecases.ReleaseApplyError
	if !errors.As(err, &failed) {
		t.Fatalf("want ReleaseApplyError, got %v", err)
	}
	clusterAssertEqual(t, conditionsOf(failed.Status)["Progressing"], conditionState{domain.ConditionFalse, "HelmApplyFailed"})
	clusterAssertEqual(t, failed.Status.ObservedGeneration, int64(4))
	clusterAssertEqual(t, failed.Status.AppliedHash, clusterPending().AppliedHash)
}

func TestObserveReportsARollingUpdateUntilEveryPodIsOnTheNewRevision(t *testing.T) {
	ctx := context.Background()
	kubernetes := clusterKubernetes()
	use := usecases.ObserveCluster{Kubernetes: kubernetes, Clock: clusterClock}
	spec := clusterSpec(t, 3, nil)
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 1, CurrentRevision: "rev-1", UpdateRevision: "rev-2"}

	rolling, err := use.Execute(ctx, spec, domain.ClusterStatus{Phase: domain.PhaseRunning})
	clusterOK(t, err)
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: 3, ReadyReplicas: 3, UpdatedReplicas: 3, CurrentRevision: "rev-2", UpdateRevision: "rev-2"}
	done, err := use.Execute(ctx, spec, rolling)
	clusterOK(t, err)

	if rolling.Phase == domain.PhaseRunning {
		t.Error("rolling update reported as Running")
	}
	clusterAssertEqual(t, []any{rolling.UpdatedReplicas, rolling.UpdateRevision}, []any{1, "rev-2"})
	if !strings.Contains(conditionMessage(rolling, "Ready"), "rolling update 1/3") {
		t.Errorf("message %q", conditionMessage(rolling, "Ready"))
	}
	clusterAssertEqual(t, done.Phase, domain.PhaseRunning)
	clusterAssertEqual(t, done.CurrentRevision, "rev-2")
}

func TestPodLabelSelectorNamesTheRelease(t *testing.T) {
	clusterAssertEqual(t, clusterSpec(t, 3, nil).PodLabelSelector(), "app.kubernetes.io/name=qdrant,app.kubernetes.io/instance="+clusterRelease)
}

func TestRandomKeyDrawsThirtyTwoAlphanumericCharacters(t *testing.T) {
	key, err := usecases.RandomKey(&counterReader{})
	clusterOK(t, err)
	clusterAssertEqual(t, len(key), 32)
	for _, char := range key {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", char) {
			t.Errorf("unexpected character %q", char)
		}
	}
}

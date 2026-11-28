package controller_test

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
)

const peerPods = 3

type peerQdrant struct {
	*fakes.Qdrant
	mu          sync.Mutex
	operations  []domain.ShardOperation
	removed     []fakes.RemovedPeer
	collections atomic.Int64
}

func (q *peerQdrant) ApplyShardOperation(ctx context.Context, node domain.QdrantNode, operation domain.ShardOperation) error {
	q.mu.Lock()
	q.operations = append(q.operations, operation)
	q.mu.Unlock()
	return q.Qdrant.ApplyShardOperation(ctx, node, operation)
}

func (q *peerQdrant) RemovePeer(ctx context.Context, node domain.QdrantNode, peerID uint64, force bool) error {
	if err := q.Qdrant.RemovePeer(ctx, node, peerID, force); err != nil {
		return err
	}
	q.mu.Lock()
	q.removed = append(q.removed, fakes.RemovedPeer{PeerID: peerID, Force: force})
	q.mu.Unlock()
	return nil
}

func (q *peerQdrant) ListCollections(ctx context.Context, node domain.QdrantNode) ([]string, error) {
	q.collections.Add(1)
	return q.Qdrant.ListCollections(ctx, node)
}

func (q *peerQdrant) shardOperations() []domain.ShardOperation {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.operations)
}

func (q *peerQdrant) removedPeers() []fakes.RemovedPeer {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.removed)
}

func (q *peerQdrant) replicas(t *testing.T, w *world) []domain.Replica {
	t.Helper()
	node := domain.QdrantNode{URL: nodeURL(0, "db", w.Namespace)}
	shards, err := q.CollectionShards(context.Background(), node, "docs")
	if err != nil {
		t.Fatal(err)
	}
	return shards.Replicas
}

func registerPeerClusters(qdrant *peerQdrant) Register {
	return func(manager ctrl.Manager, deps controller.Dependencies) error {
		deps.Qdrant = qdrant
		return (&controller.ClusterReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
	}
}

func peerURI(index int, namespace string) string {
	return fmt.Sprintf("http://qdrant-db-%d.qdrant-db-headless.%s.svc.cluster.local:6335/", index, namespace)
}

func healthyPeers(w *world, qdrant *fakes.Qdrant) {
	qdrant.AddCollection(nodeURL(0, "db", w.Namespace), "docs", 0, nil)
	for index := range peerPods {
		qdrant.Peers[uint64(100+index)] = peerURI(index, w.Namespace)
		qdrant.NodePeerIDs[nodeURL(index, "db", w.Namespace)] = uint64(100 + index)
	}
}

func rebuiltPod(w *world, qdrant *fakes.Qdrant) {
	healthyPeers(w, qdrant)
	qdrant.ReplicationFactors["docs"] = 2
	qdrant.Replicas["docs"] = []domain.Replica{
		{ShardID: 0, PeerID: 100, State: domain.ShardActive},
		{ShardID: 0, PeerID: 101, State: "Dead"},
		{ShardID: 1, PeerID: 101, State: "Dead"},
		{ShardID: 1, PeerID: 102, State: domain.ShardActive},
	}
	qdrant.Peers[201] = peerURI(1, w.Namespace)
	qdrant.NodePeerIDs[nodeURL(1, "db", w.Namespace)] = 201
}

func lopsidedShards(w *world, qdrant *fakes.Qdrant) {
	healthyPeers(w, qdrant)
	qdrant.ReplicationFactors["docs"] = 1
	for shard := range peerPods {
		qdrant.Replicas["docs"] = append(qdrant.Replicas["docs"], domain.Replica{ShardID: shard, PeerID: 100, State: domain.ShardActive})
	}
}

func evenShards(w *world, qdrant *fakes.Qdrant) {
	healthyPeers(w, qdrant)
	qdrant.ReplicationFactors["docs"] = 1
	for shard := range peerPods {
		qdrant.Replicas["docs"] = append(qdrant.Replicas["docs"], domain.Replica{ShardID: shard, PeerID: uint64(100 + shard), State: domain.ShardActive})
	}
}

func claimTemplate(name string) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
			},
		},
	}
}

func settledStatefulSet(t *testing.T, w *world) {
	t.Helper()
	labels := map[string]string{"app": "qdrant-db"}
	partition := int32(peerPods)
	replicas := int32(peerPods)
	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "qdrant-db", Namespace: w.Namespace},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: "qdrant-db-headless",
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "qdrant", Image: "qdrant/qdrant:v1.16.3"}}},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{claimTemplate("qdrant-storage")},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: &partition},
			},
		},
	}
	if err := w.Direct.Create(context.Background(), statefulSet); err != nil {
		t.Fatal(err)
	}
	statefulSet.Status.Replicas = replicas
	statefulSet.Status.ReadyReplicas = replicas
	statefulSet.Status.UpdatedReplicas = replicas
	if err := w.Direct.Status().Update(context.Background(), statefulSet); err != nil {
		t.Fatal(err)
	}
}

func peerClusterSpec(scaling qdrantv1alpha1.ClusterScaling) qdrantv1alpha1.QdrantClusterSpec {
	spec := clusterSpec(peerPods)
	spec.Scaling = &scaling
	return spec
}

func enabled() *bool {
	on := true
	return &on
}

func statusCondition(object *qdrantv1alpha1.QdrantCluster, conditionType string) (qdrantv1alpha1.Condition, bool) {
	index := slices.IndexFunc(object.Status.Conditions, func(condition qdrantv1alpha1.Condition) bool {
		return condition.Type == conditionType
	})
	if index < 0 {
		return qdrantv1alpha1.Condition{}, false
	}
	return object.Status.Conditions[index], true
}

func awaitRunning(t *testing.T, w *world) {
	t.Helper()
	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		return object.Status.Phase == "Running", fmt.Sprintf("status %+v", object.Status)
	})
}

func TestClusterControllerRemovesADeadPeerAfterRestoringItsShards(t *testing.T) {
	qdrant := &peerQdrant{Qdrant: fakes.NewQdrant()}
	w := startWorld(t, func(w *world) { rebuiltPod(w, qdrant.Qdrant) }, registerPeerClusters(qdrant))
	settledStatefulSet(t, w)
	create(t, w, clusterObject("db", peerClusterSpec(qdrantv1alpha1.ClusterScaling{RemoveDeadPeers: enabled()})))

	eventually(t, func() (bool, string) {
		removed := qdrant.removedPeers()
		return len(removed) > 0, fmt.Sprintf("operations %+v, removed %+v", qdrant.shardOperations(), removed)
	})
	if removed := qdrant.removedPeers(); !slices.Equal(removed, []fakes.RemovedPeer{{PeerID: 101, Force: true}}) {
		t.Errorf("removed %+v", removed)
	}
	for _, operation := range qdrant.shardOperations() {
		if operation.Kind != domain.ShardReplicate {
			t.Errorf("operation %+v", operation)
		}
	}
	holders := map[int][]uint64{}
	for _, replica := range qdrant.replicas(t, w) {
		holders[replica.ShardID] = append(holders[replica.ShardID], replica.PeerID)
	}
	for shard, peers := range holders {
		if len(peers) != 2 || slices.Contains(peers, 101) {
			t.Errorf("shard %d on %v", shard, peers)
		}
	}
	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		condition, found := statusCondition(object, "DeadPeers")
		return found && condition.Status == "False", fmt.Sprintf("conditions %+v", object.Status.Conditions)
	})
}

func TestClusterControllerLeavesADeadPeerAloneWithoutRemoveDeadPeers(t *testing.T) {
	qdrant := &peerQdrant{Qdrant: fakes.NewQdrant()}
	w := startWorld(t, func(w *world) { rebuiltPod(w, qdrant.Qdrant) }, registerPeerClusters(qdrant))
	settledStatefulSet(t, w)
	create(t, w, clusterObject("db", peerClusterSpec(qdrantv1alpha1.ClusterScaling{})))
	awaitRunning(t, w)

	consistently(t, 3*time.Second, func() (bool, string) {
		operations, removed := qdrant.shardOperations(), qdrant.removedPeers()
		return len(operations) == 0 && len(removed) == 0, fmt.Sprintf("operations %+v, removed %+v", operations, removed)
	})
	if _, found := statusCondition(clusterFetch(t, w, "db"), "DeadPeers"); found {
		t.Error("DeadPeers condition written without removeDeadPeers")
	}
}

func TestClusterControllerMovesShardsToIdlePeersWhenRebalancing(t *testing.T) {
	qdrant := &peerQdrant{Qdrant: fakes.NewQdrant()}
	w := startWorld(t, func(w *world) { lopsidedShards(w, qdrant.Qdrant) }, registerPeerClusters(qdrant))
	settledStatefulSet(t, w)
	create(t, w, clusterObject("db", peerClusterSpec(qdrantv1alpha1.ClusterScaling{Rebalance: enabled()})))

	eventually(t, func() (bool, string) {
		object := clusterFetch(t, w, "db")
		condition, found := statusCondition(object, "Rebalancing")
		return found && condition.Status == "False" && condition.Reason == "Balanced", fmt.Sprintf("conditions %+v, replicas %+v", object.Status.Conditions, qdrant.replicas(t, w))
	})
	operations := qdrant.shardOperations()
	if len(operations) != 2 {
		t.Fatalf("operations %+v", operations)
	}
	for _, operation := range operations {
		if operation.Kind != domain.ShardMove || operation.PeerID != 100 || operation.TargetPeerID == nil || *operation.TargetPeerID == 100 {
			t.Errorf("operation %+v", operation)
		}
	}
	load := map[uint64]int{}
	for _, replica := range qdrant.replicas(t, w) {
		load[replica.PeerID]++
	}
	for _, peer := range []uint64{100, 101, 102} {
		if load[peer] != 1 {
			t.Errorf("load %v", load)
		}
	}
}

func TestClusterControllerLeavesUnevenShardsAloneWithoutRebalance(t *testing.T) {
	qdrant := &peerQdrant{Qdrant: fakes.NewQdrant()}
	w := startWorld(t, func(w *world) { lopsidedShards(w, qdrant.Qdrant) }, registerPeerClusters(qdrant))
	settledStatefulSet(t, w)
	create(t, w, clusterObject("db", peerClusterSpec(qdrantv1alpha1.ClusterScaling{})))
	awaitRunning(t, w)

	consistently(t, 3*time.Second, func() (bool, string) {
		operations := qdrant.shardOperations()
		return len(operations) == 0, fmt.Sprintf("operations %+v", operations)
	})
	if _, found := statusCondition(clusterFetch(t, w, "db"), "Rebalancing"); found {
		t.Error("Rebalancing condition written without rebalance")
	}
}

func TestClusterControllerThrottlesRebalancingToThePeerRecoveryInterval(t *testing.T) {
	qdrant := &peerQdrant{Qdrant: fakes.NewQdrant()}
	w := startWorld(t, func(w *world) { evenShards(w, qdrant.Qdrant) }, registerPeerClusters(qdrant))
	settledStatefulSet(t, w)
	create(t, w, clusterObject("db", peerClusterSpec(qdrantv1alpha1.ClusterScaling{Rebalance: enabled()})))
	eventually(t, func() (bool, string) {
		return qdrant.collections.Load() > 0, "no rebalance run yet"
	})

	window := 5 * time.Second
	before := qdrant.collections.Load()
	time.Sleep(window)
	runs := qdrant.collections.Load() - before
	limit := int64(window/fastTiming().PeerRecovery) + 1
	if runs < 2 || runs > limit {
		t.Errorf("%d rebalance runs in %s, want between 2 and %d", runs, window, limit)
	}
	if operations := qdrant.shardOperations(); len(operations) != 0 {
		t.Errorf("operations on a balanced cluster: %+v", operations)
	}
}

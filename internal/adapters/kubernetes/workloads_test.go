package kubernetes_test

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func clientKey(namespace, name string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}

func podTemplate(labels map[string]string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: labels},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "qdrant", Image: "qdrant/qdrant"}}},
	}
}

func newStatefulSet(name, namespace string, partition int32) *appsv1.StatefulSet {
	labels := map[string]string{"app": name}
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr.To(int32(3)),
			ServiceName: name,
			Selector:    &metav1.LabelSelector{MatchLabels: labels},
			Template:    podTemplate(labels),
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type:          appsv1.RollingUpdateStatefulSetStrategyType,
				RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{Partition: ptr.To(partition)},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{{
				ObjectMeta: metav1.ObjectMeta{Name: "qdrant-storage"},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
					Resources:   corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")}},
				},
			}},
		},
	}
}

func TestStatefulSetAbsent(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()

	status, err := env.adapter.StatefulSetStatus(ctx, "missing", namespace)
	check(t, err)
	if status != nil {
		t.Fatalf("absent statefulset returned %v", status)
	}
	claims, err := env.adapter.StatefulSetClaims(ctx, "missing", namespace)
	check(t, err)
	if claims != nil {
		t.Fatalf("absent statefulset returned claims %v", claims)
	}
	if err := env.adapter.SetStatefulSetPartition(ctx, "missing", namespace, 1); err == nil {
		t.Fatal("partition of an absent statefulset did not fail")
	}
	check(t, env.adapter.OrphanStatefulSet(ctx, "missing", namespace))
}

func TestStatefulSetStatusPartitionAndClaims(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	statefulSet := newStatefulSet("qdrant", namespace, 2)
	check(t, env.direct.Create(ctx, statefulSet))
	statefulSet.Status = appsv1.StatefulSetStatus{Replicas: 3, ReadyReplicas: 2, UpdatedReplicas: 1, CurrentRevision: "qdrant-a", UpdateRevision: "qdrant-b"}
	check(t, env.direct.Status().Update(ctx, statefulSet))

	status, err := env.adapter.StatefulSetStatus(ctx, "qdrant", namespace)
	check(t, err)
	equal(t, 3, status.Replicas)
	equal(t, 2, status.ReadyReplicas)
	equal(t, 1, status.UpdatedReplicas)
	equal(t, "qdrant-a", status.CurrentRevision)
	equal(t, "qdrant-b", status.UpdateRevision)
	equal(t, 2, status.Partition)

	check(t, env.adapter.SetStatefulSetPartition(ctx, "qdrant", namespace, 0))
	status, err = env.adapter.StatefulSetStatus(ctx, "qdrant", namespace)
	check(t, err)
	equal(t, 0, status.Partition)
	stored := &appsv1.StatefulSet{}
	check(t, env.direct.Get(ctx, clientKey(namespace, "qdrant"), stored))
	equal(t, 3, int(*stored.Spec.Replicas))

	claims, err := env.adapter.StatefulSetClaims(ctx, "qdrant", namespace)
	check(t, err)
	equal(t, 1, len(claims))
	equal(t, "10Gi", claims["qdrant-storage"])
}

func TestOrphanStatefulSetWaitsForRemoval(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	check(t, env.direct.Create(ctx, newStatefulSet("orphaned", namespace, 0)))
	check(t, env.direct.Create(ctx, newStatefulSet("stuck", namespace, 0)))

	stopCollector := make(chan struct{})
	defer close(stopCollector)
	go collectOrphans(env, namespace, "orphaned", stopCollector)

	env.adapter.OrphanTimeout = 10 * time.Second
	env.adapter.OrphanPoll = 20 * time.Millisecond
	check(t, env.adapter.OrphanStatefulSet(ctx, "orphaned", namespace))
	status, err := env.adapter.StatefulSetStatus(ctx, "orphaned", namespace)
	check(t, err)
	if status != nil {
		t.Fatalf("statefulset still present: %v", status)
	}

	env.adapter.OrphanTimeout = 300 * time.Millisecond
	if err := env.adapter.OrphanStatefulSet(ctx, "stuck", namespace); err == nil {
		t.Fatal("orphan delete without a garbage collector did not time out")
	}
	stuck := &appsv1.StatefulSet{}
	check(t, env.direct.Get(ctx, clientKey(namespace, "stuck"), stuck))
	if stuck.DeletionTimestamp == nil {
		t.Fatal("delete was not issued")
	}
	equal(t, "orphan", stuck.Finalizers[0])
}

func collectOrphans(env *cluster, namespace, name string, stop <-chan struct{}) {
	ctx := context.Background()
	for {
		select {
		case <-stop:
			return
		case <-time.After(20 * time.Millisecond):
		}
		statefulSet := &appsv1.StatefulSet{}
		if err := env.direct.Get(ctx, clientKey(namespace, name), statefulSet); err != nil || statefulSet.DeletionTimestamp == nil {
			continue
		}
		statefulSet.Finalizers = nil
		_ = env.direct.Update(ctx, statefulSet)
	}
}

func TestListPodStates(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	spec := podTemplate(nil).Spec
	pods := []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "qdrant-0", Namespace: namespace, Labels: map[string]string{"app": "q", "controller-revision-hash": "rev-1"}}, Spec: spec},
		{ObjectMeta: metav1.ObjectMeta{Name: "qdrant-1", Namespace: namespace, Labels: map[string]string{"app": "q"}}, Spec: spec},
		{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: namespace, Labels: map[string]string{"app": "other"}}, Spec: spec},
	}
	for _, pod := range pods {
		check(t, env.direct.Create(ctx, pod))
	}
	pods[0].Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	check(t, env.direct.Status().Update(ctx, pods[0]))

	states, err := env.adapter.ListPodStates(ctx, namespace, "app=q")
	check(t, err)
	equal(t, 2, len(states))
	byName := map[string]bool{}
	for _, state := range states {
		byName[state.Name] = state.Ready
		if state.Name == "qdrant-0" {
			equal(t, "rev-1", state.Revision)
		}
	}
	equal(t, true, byName["qdrant-0"])
	equal(t, false, byName["qdrant-1"])
}

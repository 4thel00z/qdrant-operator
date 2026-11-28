package kubernetes_test

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/utils/ptr"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func claimBody(name, namespace, size string, labels domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": "v1",
		"kind":       "PersistentVolumeClaim",
		"metadata":   domain.JSON{"name": name, "namespace": namespace, "labels": labels},
		"spec": domain.JSON{
			"accessModes":      []any{"ReadWriteOnce"},
			"storageClassName": "expandable-" + namespace,
			"resources":        domain.JSON{"requests": domain.JSON{"storage": size}},
		},
	}
}

func TestVolumeClaims(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()

	class := &storagev1.StorageClass{
		ObjectMeta:           metav1.ObjectMeta{Name: "expandable-" + namespace},
		Provisioner:          "example.com/none",
		AllowVolumeExpansion: ptr.To(true),
	}
	check(t, env.direct.Create(ctx, class))
	check(t, env.adapter.CreateVolumeClaim(ctx, claimBody("data-0", namespace, "5Gi", domain.JSON{"app": "q"})))
	check(t, env.adapter.CreateVolumeClaim(ctx, claimBody("data-0", namespace, "5Gi", domain.JSON{"app": "q"})))
	check(t, env.adapter.CreateVolumeClaim(ctx, claimBody("data-1", namespace, "5Gi", domain.JSON{"app": "other"})))

	claims, err := env.adapter.ListVolumeClaims(ctx, namespace, "app=q")
	check(t, err)
	equal(t, 1, len(claims))
	equal(t, "data-0", claims[0].Name)
	equal(t, "5Gi", claims[0].Size)
	equal(t, "expandable-"+namespace, claims[0].StorageClass)
	equal(t, 1, len(claims[0].AccessModes))
	equal(t, "ReadWriteOnce", claims[0].AccessModes[0])

	bindClaim(t, env, "data-0", namespace)
	check(t, env.adapter.ResizeVolumeClaim(ctx, "data-0", namespace, "8Gi"))
	claims, err = env.adapter.ListVolumeClaims(ctx, namespace, "app=q")
	check(t, err)
	equal(t, "8Gi", claims[0].Size)

	check(t, env.adapter.DeleteVolumeClaim(ctx, "data-0", namespace))
	check(t, env.adapter.DeleteVolumeClaim(ctx, "absent", namespace))
	stored := &corev1.PersistentVolumeClaim{}
	err = env.direct.Get(ctx, clientKey(namespace, "data-0"), stored)
	if err == nil && stored.DeletionTimestamp == nil {
		t.Fatal("claim was not deleted")
	}
}

func bindClaim(t *testing.T, env *cluster, name, namespace string) {
	t.Helper()
	ctx := context.Background()
	claim := &corev1.PersistentVolumeClaim{}
	check(t, env.direct.Get(ctx, clientKey(namespace, name), claim))
	claim.Spec.VolumeName = "pv-" + name
	check(t, env.direct.Update(ctx, claim))
	claim.Status.Phase = corev1.ClaimBound
	check(t, env.direct.Status().Update(ctx, claim))
}

func newVolume(name string, affinity *corev1.VolumeNodeAffinity) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			Capacity:               corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			AccessModes:            []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			PersistentVolumeSource: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/tmp/" + name}},
			NodeAffinity:           affinity,
		},
	}
}

func zoneAffinity(key string, operator corev1.NodeSelectorOperator, values ...string) *corev1.VolumeNodeAffinity {
	return &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{NodeSelectorTerms: []corev1.NodeSelectorTerm{{
		MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "other", Operator: corev1.NodeSelectorOpExists}, {Key: key, Operator: operator, Values: values}},
	}}}}
}

func TestVolumeZone(t *testing.T) {
	env := startCluster(t)
	ctx := context.Background()
	prefix := fmt.Sprintf("zone-%d", len(t.Name()))
	cases := map[string]*corev1.VolumeNodeAffinity{
		prefix + "-pinned":   zoneAffinity("topology.kubernetes.io/zone", corev1.NodeSelectorOpIn, "eu-1a"),
		prefix + "-custom":   zoneAffinity("topology.cinder.csi.openstack.org/zone", corev1.NodeSelectorOpIn, "az-2"),
		prefix + "-multi":    zoneAffinity("topology.kubernetes.io/zone", corev1.NodeSelectorOpIn, "a", "b"),
		prefix + "-notin":    zoneAffinity("topology.kubernetes.io/zone", corev1.NodeSelectorOpNotIn, "a"),
		prefix + "-nonzone":  zoneAffinity("topology.kubernetes.io/region", corev1.NodeSelectorOpIn, "r"),
		prefix + "-noaffine": nil,
	}
	for name, affinity := range cases {
		check(t, env.direct.Create(ctx, newVolume(name, affinity)))
	}

	zone, err := env.adapter.VolumeZone(ctx, prefix+"-pinned")
	check(t, err)
	equal(t, domain.Zone{Label: "topology.kubernetes.io/zone", Name: "eu-1a"}, *zone)
	zone, err = env.adapter.VolumeZone(ctx, prefix+"-custom")
	check(t, err)
	equal(t, domain.Zone{Label: "topology.cinder.csi.openstack.org/zone", Name: "az-2"}, *zone)
	for _, name := range []string{"-multi", "-notin", "-nonzone", "-noaffine", "-absent"} {
		zone, err = env.adapter.VolumeZone(ctx, prefix+name)
		check(t, err)
		if zone != nil {
			t.Fatalf("%s: unexpected zone %v", name, *zone)
		}
	}
}

func newNode(name, zone string, unschedulable bool) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: map[string]string{"test.qdrant.io/zone": zone}},
		Spec:       corev1.NodeSpec{Unschedulable: unschedulable},
	}
}

func TestNodeInZone(t *testing.T) {
	env := startCluster(t)
	ctx := context.Background()
	zone := domain.Zone{Label: "test.qdrant.io/zone", Name: "z-node-test"}
	nodes := []struct {
		node  *corev1.Node
		ready corev1.ConditionStatus
	}{
		{newNode("node-a-notready", zone.Name, false), corev1.ConditionFalse},
		{newNode("node-b-cordoned", zone.Name, true), corev1.ConditionTrue},
		{newNode("node-d-ready", zone.Name, false), corev1.ConditionTrue},
		{newNode("node-c-ready", zone.Name, false), corev1.ConditionTrue},
		{newNode("node-e-elsewhere", "z-other", false), corev1.ConditionTrue},
	}

	none, err := env.adapter.NodeInZone(ctx, zone)
	check(t, err)
	equal(t, "", none)

	for _, entry := range nodes {
		check(t, env.direct.Create(ctx, entry.node))
		entry.node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: entry.ready}}
		check(t, env.direct.Status().Update(ctx, entry.node))
	}
	name, err := env.adapter.NodeInZone(ctx, zone)
	check(t, err)
	equal(t, "node-c-ready", name)
}

func snapshotBody(name, namespace string, labels domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": "snapshot.storage.k8s.io/v1",
		"kind":       "VolumeSnapshot",
		"metadata":   domain.JSON{"name": name, "namespace": namespace, "labels": labels},
		"spec":       domain.JSON{"source": domain.JSON{"persistentVolumeClaimName": "data-0"}},
	}
}

func setSnapshotStatus(t *testing.T, env *cluster, name, namespace string, status domain.JSON) {
	t.Helper()
	object := &unstructured.Unstructured{Object: snapshotBody(name, namespace, domain.JSON{})}
	check(t, env.direct.Get(context.Background(), clientKey(namespace, name), object))
	check(t, unstructured.SetNestedField(object.Object, status, "status"))
	check(t, env.direct.Status().Update(context.Background(), object))
}

func TestVolumeSnapshots(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()

	absent, err := env.adapter.VolumeSnapshot(ctx, "missing", namespace)
	check(t, err)
	if absent != nil {
		t.Fatalf("absent snapshot returned %v", absent)
	}

	check(t, env.adapter.CreateVolumeSnapshot(ctx, snapshotBody("snap-a", namespace, domain.JSON{"backup": "one"})))
	check(t, env.adapter.CreateVolumeSnapshot(ctx, snapshotBody("snap-a", namespace, domain.JSON{"backup": "one"})))
	check(t, env.adapter.CreateVolumeSnapshot(ctx, snapshotBody("snap-b", namespace, domain.JSON{"backup": "two"})))

	state, err := env.adapter.VolumeSnapshot(ctx, "snap-a", namespace)
	check(t, err)
	equal(t, false, state.Ready)
	equal(t, "", state.Error)

	setSnapshotStatus(t, env, "snap-a", namespace, domain.JSON{"readyToUse": true})
	setSnapshotStatus(t, env, "snap-b", namespace, domain.JSON{"readyToUse": false, "error": domain.JSON{"message": "driver exploded"}})
	state, err = env.adapter.VolumeSnapshot(ctx, "snap-a", namespace)
	check(t, err)
	equal(t, true, state.Ready)
	equal(t, "snap-a", state.Name)
	state, err = env.adapter.VolumeSnapshot(ctx, "snap-b", namespace)
	check(t, err)
	equal(t, false, state.Ready)
	equal(t, "driver exploded", state.Error)

	names, err := env.adapter.ListVolumeSnapshots(ctx, namespace, "backup=two")
	check(t, err)
	equal(t, 1, len(names))
	equal(t, "snap-b", names[0])
	names, err = env.adapter.ListVolumeSnapshots(ctx, namespace, "")
	check(t, err)
	equal(t, 2, len(names))

	check(t, env.adapter.DeleteVolumeSnapshot(ctx, "snap-a", namespace))
	check(t, env.adapter.DeleteVolumeSnapshot(ctx, "snap-a", namespace))
	state, err = env.adapter.VolumeSnapshot(ctx, "snap-a", namespace)
	check(t, err)
	if state != nil {
		t.Fatalf("deleted snapshot returned %v", state)
	}
}

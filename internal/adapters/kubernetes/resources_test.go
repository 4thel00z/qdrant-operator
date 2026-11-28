package kubernetes_test

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func accessKeyBody(name, namespace string, labels domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       domain.AccessKeys.Kind,
		"metadata":   domain.JSON{"name": name, "namespace": namespace, "labels": labels},
		"spec":       domain.JSON{"access": "r", "clusterRef": domain.JSON{"name": "demo"}},
	}
}

func TestSecretValueAbsent(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()

	_, err := env.adapter.SecretValue(ctx, domain.SecretRef{Name: "missing", Key: "k", Namespace: namespace})
	if !errors.Is(err, ports.ErrSecretKeyNotFound) {
		t.Fatalf("missing secret: %v", err)
	}

	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "present", Namespace: namespace}, Data: map[string][]byte{"k": []byte("v")}}
	check(t, env.direct.Create(ctx, secret))
	_, err = env.adapter.SecretValue(ctx, domain.SecretRef{Name: "present", Key: "other", Namespace: namespace})
	if !errors.Is(err, ports.ErrSecretKeyNotFound) {
		t.Fatalf("missing key: %v", err)
	}
	value, err := env.adapter.SecretValue(ctx, domain.SecretRef{Name: "present", Key: "k", Namespace: namespace})
	check(t, err)
	equal(t, "v", value)
}

func TestApplySecretOwnership(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	owner := domain.OwnerReference{APIVersion: domain.APIVersion, Kind: "QdrantAccessKey", Name: "key", UID: "uid-one"}
	other := domain.OwnerReference{APIVersion: domain.APIVersion, Kind: "QdrantAccessKey", Name: "key", UID: "uid-two"}

	check(t, env.adapter.ApplySecret(ctx, "token", namespace, map[string]string{"token": "first"}, owner))
	stored := &corev1.Secret{}
	check(t, env.direct.Get(ctx, clientKey(namespace, "token"), stored))
	equal(t, "first", string(stored.Data["token"]))
	equal(t, 1, len(stored.OwnerReferences))
	equal(t, true, *stored.OwnerReferences[0].Controller)
	equal(t, true, *stored.OwnerReferences[0].BlockOwnerDeletion)

	check(t, env.adapter.ApplySecret(ctx, "token", namespace, map[string]string{"token": "second"}, owner))
	value, err := env.adapter.SecretValue(ctx, domain.SecretRef{Name: "token", Key: "token", Namespace: namespace})
	check(t, err)
	equal(t, "second", value)

	err = env.adapter.ApplySecret(ctx, "token", namespace, map[string]string{"token": "stolen"}, other)
	if !errors.Is(err, ports.ErrSecretNotOwned) {
		t.Fatalf("foreign owner: %v", err)
	}

	foreign := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "foreign", Namespace: namespace}, StringData: map[string]string{"k": "v"}}
	check(t, env.direct.Create(ctx, foreign))
	err = env.adapter.ApplySecret(ctx, "foreign", namespace, map[string]string{"k": "overwritten"}, owner)
	if !errors.Is(err, ports.ErrSecretNotOwned) {
		t.Fatalf("unowned secret: %v", err)
	}
	value, err = env.adapter.SecretValue(ctx, domain.SecretRef{Name: "foreign", Key: "k", Namespace: namespace})
	check(t, err)
	equal(t, "v", value)
}

func TestCustomResourceLifecycle(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	ref := domain.ResourceRef{Kind: domain.AccessKeys, Name: "one", Namespace: namespace}

	absent, err := env.adapter.CustomResource(ctx, ref)
	check(t, err)
	if absent != nil {
		t.Fatalf("absent resource returned %v", absent)
	}

	created, err := env.adapter.CreateCustomResource(ctx, accessKeyBody("one", namespace, domain.JSON{"tier": "a"}))
	check(t, err)
	metadata, _ := created["metadata"].(map[string]any)
	if metadata["uid"] == "" || metadata["uid"] == nil {
		t.Fatalf("created body has no uid: %v", created)
	}

	_, err = env.adapter.CreateCustomResource(ctx, accessKeyBody("two", namespace, domain.JSON{"tier": "b"}))
	check(t, err)

	fetched, err := env.adapter.CustomResource(ctx, ref)
	check(t, err)
	equal(t, "one", fetched["metadata"].(map[string]any)["name"].(string))

	all, err := env.adapter.ListCustomResources(ctx, domain.AccessKeys, namespace, "")
	check(t, err)
	equal(t, 2, len(all))
	selected, err := env.adapter.ListCustomResources(ctx, domain.AccessKeys, namespace, "tier=b")
	check(t, err)
	equal(t, 1, len(selected))
	equal(t, "two", selected[0]["metadata"].(map[string]any)["name"].(string))
	none, err := env.adapter.ListCustomResources(ctx, domain.AccessKeys, namespace, "tier=c")
	check(t, err)
	equal(t, 0, len(none))
	if _, err := env.adapter.ListCustomResources(ctx, domain.AccessKeys, namespace, "tier in ("); err == nil {
		t.Fatal("malformed selector accepted")
	}

	check(t, env.adapter.DeleteCustomResource(ctx, ref))
	gone, err := env.adapter.CustomResource(ctx, ref)
	check(t, err)
	if gone != nil {
		t.Fatalf("deleted resource returned %v", gone)
	}
	check(t, env.adapter.DeleteCustomResource(ctx, ref))
}

func TestPatchStatusMergesAndClears(t *testing.T) {
	env := startCluster(t)
	namespace := newNamespace(t, env)
	ctx := context.Background()
	ref := domain.ResourceRef{Kind: domain.AccessKeys, Name: "status", Namespace: namespace}
	_, err := env.adapter.CreateCustomResource(ctx, accessKeyBody("status", namespace, domain.JSON{}))
	check(t, err)

	check(t, env.adapter.PatchStatus(ctx, ref, domain.JSON{"phase": "Failed", "error": "boom"}))
	check(t, env.adapter.PatchStatus(ctx, ref, domain.JSON{"phase": "Ready", "error": nil}))

	body, err := env.adapter.CustomResource(ctx, ref)
	check(t, err)
	status, _ := body["status"].(map[string]any)
	equal(t, "Ready", status["phase"].(string))
	if _, present := status["error"]; present {
		t.Fatalf("nil value did not clear the field: %v", status)
	}

	missing := domain.ResourceRef{Kind: domain.AccessKeys, Name: "never-created", Namespace: namespace}
	check(t, env.adapter.PatchStatus(ctx, missing, domain.JSON{"phase": "Ready"}))
}

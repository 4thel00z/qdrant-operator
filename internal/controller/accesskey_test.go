package controller_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/controller"
)

func registerAccessKeys(manager ctrl.Manager, deps controller.Dependencies) error {
	return (&controller.AccessKeyReconciler{Dependencies: deps}).SetupWithManager(manager, 2)
}

func accessKeyCluster(name string, jwtRbac bool) *qdrantv1alpha1.QdrantCluster {
	spec := qdrantv1alpha1.QdrantClusterSpec{
		Version: "1.16.3",
		APIKey: &qdrantv1alpha1.ClusterAPIKey{
			JWTRbac:   ptr.To(jwtRbac),
			SecretRef: &qdrantv1alpha1.ClusterRequiredSecretKeyRef{Name: "api-keys", Key: "key"},
		},
	}
	return clusterObject(name, spec)
}

func accessKeySigningSecret(t *testing.T, w *world) {
	t.Helper()
	create(t, w, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "api-keys"},
		StringData: map[string]string{"key": "topsecret"},
	})
}

func accessKeyObject(name, cluster string) *qdrantv1alpha1.QdrantAccessKey {
	return &qdrantv1alpha1.QdrantAccessKey{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: qdrantv1alpha1.QdrantAccessKeySpec{
			ClusterRef: qdrantv1alpha1.ClusterRef{Name: cluster},
			Access:     "r",
		},
	}
}

func accessKeyPhase(t *testing.T, w *world, name string) qdrantv1alpha1.AccessKeyPhase {
	t.Helper()
	return fetch(t, w, name, &qdrantv1alpha1.QdrantAccessKey{}).Status.Phase
}

func TestAccessKeyControllerIssuesATokenIntoASecretAndReportsReady(t *testing.T) {
	w := startWorld(t, nil, registerAccessKeys)
	accessKeySigningSecret(t, w)
	create(t, w, accessKeyCluster("db", true))
	create(t, w, accessKeyObject("app", "db"))

	eventually(t, func() (bool, string) {
		object := fetch(t, w, "app", &qdrantv1alpha1.QdrantAccessKey{})
		return object.Status.Phase == "Ready", fmt.Sprintf("status %+v", object.Status)
	})
	object := fetch(t, w, "app", &qdrantv1alpha1.QdrantAccessKey{})
	if object.Status.SecretRef == nil {
		t.Fatalf("status without secretRef: %+v", object.Status)
	}
	secret := &corev1.Secret{}
	key := client.ObjectKey{Namespace: w.Namespace, Name: object.Status.SecretRef.Name}
	if err := w.Direct.Get(context.Background(), key, secret); err != nil {
		t.Fatal(err)
	}
	if len(secret.Data["token"]) == 0 || string(secret.Data["url"]) != serviceURL("db", w.Namespace) {
		t.Errorf("secret data: token %d bytes, url %q", len(secret.Data["token"]), secret.Data["url"])
	}
	if len(secret.OwnerReferences) != 1 || secret.OwnerReferences[0].UID != object.UID || secret.OwnerReferences[0].Kind != "QdrantAccessKey" {
		t.Errorf("owner references: %+v", secret.OwnerReferences)
	}
	if object.Status.ObservedGeneration != object.Generation {
		t.Errorf("observedGeneration %d, generation %d", object.Status.ObservedGeneration, object.Generation)
	}
}

func TestAccessKeyControllerRetriesWhileTheClusterIsMissing(t *testing.T) {
	w := startWorld(t, nil, registerAccessKeys)
	accessKeySigningSecret(t, w)
	create(t, w, accessKeyObject("app", "db"))

	consistently(t, 3*time.Second, func() (bool, string) {
		phase := accessKeyPhase(t, w, "app")
		return phase != "Failed" && phase != "Ready", fmt.Sprintf("phase %s", phase)
	})
	create(t, w, accessKeyCluster("db", true))
	eventually(t, func() (bool, string) {
		phase := accessKeyPhase(t, w, "app")
		return phase == "Ready", fmt.Sprintf("phase %s", phase)
	})
}

func TestAccessKeyControllerReportsPendingWhileJWTRBACIsDisabled(t *testing.T) {
	w := startWorld(t, nil, registerAccessKeys)
	accessKeySigningSecret(t, w)
	create(t, w, accessKeyCluster("db", false))
	create(t, w, accessKeyObject("app", "db"))

	eventually(t, func() (bool, string) {
		object := fetch(t, w, "app", &qdrantv1alpha1.QdrantAccessKey{})
		return object.Status.Phase == "Pending", fmt.Sprintf("status %+v", object.Status)
	})
	consistently(t, 3*time.Second, func() (bool, string) {
		phase := accessKeyPhase(t, w, "app")
		return phase == "Pending", fmt.Sprintf("phase %s", phase)
	})
	object := fetch(t, w, "app", &qdrantv1alpha1.QdrantAccessKey{})
	if len(object.Status.Conditions) != 1 || object.Status.Conditions[0].Reason != "JwtRbacDisabled" {
		t.Errorf("conditions: %+v", object.Status.Conditions)
	}
}

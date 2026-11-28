package controller

import (
	"context"
	"fmt"
	"reflect"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

// AccessKeyReconciler keeps a QdrantAccessKey's token Secret current, on every spec change and
// every Timing.AccessKeyRenew.
type AccessKeyReconciler struct {
	Dependencies
}

func (r *AccessKeyReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantaccesskey").
		For(&qdrantv1alpha1.QdrantAccessKey{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *AccessKeyReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantAccessKey{}
	if err := r.Client.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	current, err := translate.AccessKeyStatusFromAPI(object.Namespace, object.Status)
	if err != nil {
		return r.fail(ctx, object, domain.AccessKeyStatus{}, err)
	}
	spec, err := translate.AccessKeySpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.fail(ctx, object, current, err)
	}
	owner := domain.OwnerReference{
		APIVersion: domain.APIVersion,
		Kind:       domain.AccessKeys.Kind,
		Name:       object.Name,
		UID:        string(object.UID),
	}
	result, err := usecases.IssueAccessKey{Kubernetes: r.Kubernetes, Token: r.Token}.Execute(ctx, spec, object.Generation, current, owner, r.Clock())
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if usecases.Retryable(err) {
		return RetryAfter(ctx, err, r.Timing.Retry)
	}
	if err != nil {
		return r.fail(ctx, object, current, err)
	}
	if reflect.DeepEqual(result, current) {
		return Every(r.Timing.AccessKeyRenew)
	}
	if err := r.WriteStatus(ctx, object, translate.AccessKeyStatusToAPI(result)); err != nil {
		return ctrl.Result{}, err
	}
	if !result.IssuedAt.Equal(current.IssuedAt) {
		log.FromContext(ctx).Info(fmt.Sprintf("Token for %s written to secret %s", object.Name, spec.SecretName))
	}
	return Every(r.Timing.AccessKeyRenew)
}

// fail records the error as a Failed phase. The renewal interval still requeues, as kopf's timer
// kept firing after a PermanentError.
func (r *AccessKeyReconciler) fail(ctx context.Context, object *qdrantv1alpha1.QdrantAccessKey, current domain.AccessKeyStatus, cause error) (ctrl.Result, error) {
	r.Metrics.ReconcileFailed(domain.AccessKeys)
	log.FromContext(ctx).Error(cause, "Access key failed")
	current.Phase = domain.AccessKeyFailed
	current.Error = cause.Error()
	if err := r.WriteStatus(ctx, object, translate.AccessKeyStatusToAPI(current)); err != nil {
		return ctrl.Result{}, err
	}
	return Every(r.Timing.AccessKeyRenew)
}

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

// CollectionReconciler keeps a QdrantCollection's collection, payload indexes and aliases in line
// with its spec, on every spec change and every Timing.CollectionReconcile.
type CollectionReconciler struct {
	Dependencies
}

func (r *CollectionReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantcollection").
		For(&qdrantv1alpha1.QdrantCollection{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *CollectionReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantCollection{}
	if err := r.Client.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.delete(ctx, object)
	}
	if err := EnsureFinalizer(ctx, r.Client, object); err != nil {
		return ctrl.Result{}, err
	}
	current, err := translate.CollectionStatusFromAPI(object.Status)
	if err != nil {
		return r.fail(ctx, object, domain.CollectionStatus{}, err)
	}
	spec, err := translate.CollectionSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.fail(ctx, object, current, err)
	}
	result, err := usecases.ReconcileCollection{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, object.Generation, current)
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if usecases.Retryable(err) {
		return RetryAfter(ctx, err, r.Timing.Retry)
	}
	if err != nil {
		return r.fail(ctx, object, current, err)
	}
	if result.Phase != current.Phase {
		log.FromContext(ctx).Info(fmt.Sprintf("Collection %s is %s: %s", result.CollectionName, result.Phase, detail(result.Error)))
	}
	if reflect.DeepEqual(result, current) {
		return Every(r.Timing.CollectionReconcile)
	}
	if err := r.WriteStatus(ctx, object, translate.CollectionStatusToAPI(result)); err != nil {
		return ctrl.Result{}, err
	}
	return Every(r.Timing.CollectionReconcile)
}

// fail records the error as a Failed phase. The interval still requeues, as kopf's timer kept
// firing after a PermanentError, so a cluster that comes back heals the collection.
func (r *CollectionReconciler) fail(ctx context.Context, object *qdrantv1alpha1.QdrantCollection, current domain.CollectionStatus, cause error) (ctrl.Result, error) {
	r.Metrics.ReconcileFailed(domain.Collections)
	log.FromContext(ctx).Error(cause, "Collection reconcile failed")
	current.Phase = domain.CollectionFailed
	current.Error = cause.Error()
	if err := r.WriteStatus(ctx, object, translate.CollectionStatusToAPI(current)); err != nil {
		return ctrl.Result{}, err
	}
	return Every(r.Timing.CollectionReconcile)
}

func (r *CollectionReconciler) delete(ctx context.Context, object *qdrantv1alpha1.QdrantCollection) (ctrl.Result, error) {
	spec, err := translate.CollectionSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		log.FromContext(ctx).Error(err, "Spec does not translate; nothing to drop")
		return ctrl.Result{}, ReleaseFinalizer(ctx, r.Client, object)
	}
	dropped, err := usecases.DeleteCollection{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes}.Execute(ctx, spec)
	if err != nil {
		return ctrl.Result{}, err
	}
	verb := "retained"
	if dropped {
		verb = "dropped"
	}
	log.FromContext(ctx).Info(fmt.Sprintf("Collection %s %s on delete", spec.CollectionName, verb))
	return ctrl.Result{}, ReleaseFinalizer(ctx, r.Client, object)
}

func detail(message string) string {
	if message == "" {
		return "ok"
	}
	return message
}

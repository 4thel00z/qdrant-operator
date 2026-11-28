package controller

import (
	"context"
	"fmt"
	"time"

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

// RestoreReconciler runs a QdrantRestore once.
type RestoreReconciler struct {
	Dependencies
}

func (r *RestoreReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantrestore").
		For(&qdrantv1alpha1.QdrantRestore{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *RestoreReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantRestore{}
	if err := r.Live.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	current, err := translate.RestoreStatusFromAPI(object.Status)
	if err != nil {
		return r.fail(ctx, object, time.Now(), err)
	}
	if current.Phase == domain.RestoreCompleted || current.Phase == domain.RestoreFailed {
		return ctrl.Result{}, nil
	}
	started := time.Now()
	spec, err := translate.RestoreSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.fail(ctx, object, started, err)
	}
	ref := domain.ResourceRef{Kind: domain.Restores, Name: object.Name, Namespace: object.Namespace}
	result, err := usecases.NewExecuteRestore(r.Qdrant, r.Storage, r.Kubernetes, r.Clock).Execute(ctx, spec, ref)
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if usecases.Retryable(err) {
		return RetryAfter(ctx, err, r.Timing.Retry)
	}
	if err != nil {
		return r.fail(ctx, object, started, err)
	}
	r.Metrics.OperationFinished(domain.Restores, string(result.Phase), time.Since(started))
	if err := WriteStatus(ctx, r.Client, object, translate.RestoreStatusToAPI(result), r.Clock()); err != nil {
		return ctrl.Result{}, err
	}
	log.FromContext(ctx).Info(fmt.Sprintf("Restore %s from %s", result.Phase, result.SourceBackup))
	return ctrl.Result{}, nil
}

func (r *RestoreReconciler) fail(ctx context.Context, object *qdrantv1alpha1.QdrantRestore, started time.Time, cause error) (ctrl.Result, error) {
	r.Metrics.OperationFinished(domain.Restores, "failed", time.Since(started))
	failed := domain.RestoreStatus{Phase: domain.RestoreFailed, Error: cause.Error()}
	if err := WriteStatus(ctx, r.Client, object, translate.RestoreStatusToAPI(failed), r.Clock()); err != nil {
		return ctrl.Result{}, err
	}
	return Stop(fmt.Errorf("restore failed: %w", cause))
}

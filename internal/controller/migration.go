package controller

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"k8s.io/apimachinery/pkg/types"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

// MigrationReconciler runs a QdrantMigration once. An interrupted run resumes from the offsets in
// status, and setting RetryAnnotation on a Failed migration runs it again.
type MigrationReconciler struct {
	Dependencies

	// UnreachableDelay is how long to wait after the given number of unreachable attempts; it
	// defaults to domain.RetryDelay.
	UnreachableDelay func(attempt int) time.Duration

	mutex    sync.Mutex
	attempts map[types.NamespacedName]int
}

func (r *MigrationReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	changed := predicate.Or(predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{})
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantmigration").
		For(&qdrantv1alpha1.QdrantMigration{}, builder.WithPredicates(changed)).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *MigrationReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantMigration{}
	if err := r.Live.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	switch object.Status.Phase {
	case "", "Pending", "Running":
	case "Failed":
		if _, requested := object.Annotations[RetryAnnotation]; !requested {
			return ctrl.Result{}, nil
		}
		if err := r.consumeRetry(ctx, object); err != nil {
			return ctrl.Result{}, err
		}
	default:
		return ctrl.Result{}, nil
	}
	return r.run(ctx, request.NamespacedName, object)
}

func (r *MigrationReconciler) consumeRetry(ctx context.Context, object *qdrantv1alpha1.QdrantMigration) error {
	base := object.DeepCopy()
	delete(object.Annotations, RetryAnnotation)
	if err := client.IgnoreNotFound(r.Client.Patch(ctx, object, client.MergeFrom(base))); err != nil {
		return fmt.Errorf("remove %s from %s/%s: %w", RetryAnnotation, object.Namespace, object.Name, err)
	}
	return nil
}

func (r *MigrationReconciler) run(ctx context.Context, key types.NamespacedName, object *qdrantv1alpha1.QdrantMigration) (ctrl.Result, error) {
	started := time.Now()
	current, err := translate.MigrationStatusFromAPI(object.Status)
	if err != nil {
		return r.fail(ctx, key, object, domain.MigrationStatus{}, started, err)
	}
	spec, err := translate.MigrationSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.fail(ctx, key, object, current, started, err)
	}
	ref := domain.ResourceRef{Kind: domain.Migrations, Name: object.Name, Namespace: object.Namespace}
	result, err := usecases.ExecuteMigration{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, ref, current)
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if usecases.Retryable(err) {
		return RetryAfter(ctx, err, r.Timing.Retry)
	}
	if errors.Is(err, ports.ErrUnreachable) || errors.Is(err, context.DeadlineExceeded) {
		return RetryAfter(ctx, err, r.unreachableDelay(r.nextAttempt(key)))
	}
	if err != nil {
		return r.fail(ctx, key, object, current, started, err)
	}
	r.resetAttempts(key)
	r.Metrics.OperationFinished(domain.Migrations, string(result.Phase), time.Since(started))
	status, err := translate.MigrationStatusToAPI(result)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.WriteStatus(ctx, object, status); err != nil {
		return ctrl.Result{}, err
	}
	progress := domain.ProgressOf(result.Collections)
	log.FromContext(ctx).Info(fmt.Sprintf("Migration %s: %d/%d points across %d collections from %s",
		result.Phase, progress.PointsCopied, progress.PointsTotal, progress.CollectionsTotal, result.Source))
	return ctrl.Result{}, nil
}

func (r *MigrationReconciler) fail(ctx context.Context, key types.NamespacedName, object *qdrantv1alpha1.QdrantMigration, current domain.MigrationStatus, started time.Time, cause error) (ctrl.Result, error) {
	r.resetAttempts(key)
	r.Metrics.OperationFinished(domain.Migrations, "failed", time.Since(started))
	current.Phase = domain.MigrationFailed
	current.Error = cause.Error()
	status, err := translate.MigrationStatusToAPI(current)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.WriteStatus(ctx, object, status); err != nil {
		return ctrl.Result{}, err
	}
	return Stop(fmt.Errorf("migration failed: %w", cause))
}

func (r *MigrationReconciler) nextAttempt(key types.NamespacedName) int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if r.attempts == nil {
		r.attempts = map[types.NamespacedName]int{}
	}
	attempt := r.attempts[key]
	r.attempts[key] = attempt + 1
	return attempt
}

func (r *MigrationReconciler) resetAttempts(key types.NamespacedName) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	delete(r.attempts, key)
}

func (r *MigrationReconciler) unreachableDelay(attempt int) time.Duration {
	if r.UnreachableDelay == nil {
		return domain.RetryDelay(attempt)
	}
	return r.UnreachableDelay(attempt)
}

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

// BackupReconciler runs a QdrantBackup once and deletes it when its retention lapses.
type BackupReconciler struct {
	Dependencies
}

func (r *BackupReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantbackup").
		For(&qdrantv1alpha1.QdrantBackup{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *BackupReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantBackup{}
	if err := r.Live.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.delete(ctx, object)
	}
	if err := EnsureFinalizer(ctx, r.Client, object); err != nil {
		return ctrl.Result{}, err
	}
	ref := domain.ResourceRef{Kind: domain.Backups, Name: object.Name, Namespace: object.Namespace}
	current, err := translate.BackupStatusFromAPI(object.Status)
	if err != nil {
		return r.fail(ctx, object, time.Now(), err)
	}
	if !current.Finished() {
		spec, err := translate.BackupSpecFromAPI(object.Name, object.Namespace, object.Spec)
		if err != nil {
			return r.fail(ctx, object, time.Now(), err)
		}
		started := time.Now()
		result, err := usecases.ExecuteBackup{Qdrant: r.Qdrant, Storage: r.Storage, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, ref, &current)
		if Interrupted(ctx) {
			return ctrl.Result{}, ctx.Err()
		}
		if usecases.Retryable(err) {
			return RetryAfter(ctx, err, r.Timing.Retry)
		}
		if err != nil {
			return r.fail(ctx, object, started, err)
		}
		r.Metrics.OperationFinished(domain.Backups, string(result.Phase), time.Since(started))
		if err := WriteStatus(ctx, r.Client, object, translate.BackupStatusToAPI(result), r.Clock()); err != nil {
			return ctrl.Result{}, err
		}
		log.FromContext(ctx).Info(fmt.Sprintf("Backup %s: %s (%s)", result.Phase, result.S3Path, result.TotalSize))
		current = result
	}
	return r.expire(ctx, ref, current)
}

func (r *BackupReconciler) fail(ctx context.Context, object *qdrantv1alpha1.QdrantBackup, started time.Time, cause error) (ctrl.Result, error) {
	r.Metrics.OperationFinished(domain.Backups, "failed", time.Since(started))
	failed := domain.BackupStatus{Phase: domain.BackupFailed, Error: cause.Error()}
	if err := WriteStatus(ctx, r.Client, object, translate.BackupStatusToAPI(failed), r.Clock()); err != nil {
		return ctrl.Result{}, err
	}
	return Stop(fmt.Errorf("backup failed: %w", cause))
}

// expire deletes the backup once expiresAt has passed and otherwise requeues for the earlier of
// Timing.BackupExpiry and that moment.
func (r *BackupReconciler) expire(ctx context.Context, ref domain.ResourceRef, status domain.BackupStatus) (ctrl.Result, error) {
	if status.ExpiresAt.IsZero() {
		return ctrl.Result{}, nil
	}
	now := r.Clock()
	expired, err := usecases.ExpireBackup{Kubernetes: r.Kubernetes}.Execute(ctx, ref, status, now)
	if err != nil {
		return ctrl.Result{}, err
	}
	if expired {
		log.FromContext(ctx).Info(fmt.Sprintf("Backup %s passed retentionDays and was deleted", ref.Name))
		return ctrl.Result{}, nil
	}
	return Every(min(r.Timing.BackupExpiry, status.ExpiresAt.Sub(now)))
}

func (r *BackupReconciler) delete(ctx context.Context, object *qdrantv1alpha1.QdrantBackup) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	spec, err := translate.BackupSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		logger.Error(err, "Spec does not translate; no backup data to delete")
		return ctrl.Result{}, ReleaseFinalizer(ctx, r.Client, object)
	}
	status, err := translate.BackupStatusFromAPI(object.Status)
	if err != nil {
		logger.Error(err, "Status does not translate; treating it as empty")
		status = domain.BackupStatus{}
	}
	cleaned, err := usecases.CleanupBackupSnapshots{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes}.Execute(ctx, spec, status)
	if err != nil {
		logger.Info(fmt.Sprintf("Warning: leaving node snapshots in place: %s", err))
	}
	if cleaned > 0 {
		logger.Info(fmt.Sprintf("Deleted %d leftover node snapshots of %s", cleaned, object.Name))
	}
	deleted, err := usecases.DeleteBackupData{Storage: r.Storage, Kubernetes: r.Kubernetes}.Execute(ctx, spec)
	if err != nil {
		logger.Info(fmt.Sprintf("Warning: leaving backup data in place: %s", err))
		return ctrl.Result{}, ReleaseFinalizer(ctx, r.Client, object)
	}
	logger.Info(fmt.Sprintf("Deleted %d objects of backup %s", deleted, object.Name))
	return ctrl.Result{}, ReleaseFinalizer(ctx, r.Client, object)
}

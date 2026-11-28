package controller

import (
	"context"
	"fmt"

	apiequality "k8s.io/apimachinery/pkg/api/equality"
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

// ScheduleReconciler launches the backups a QdrantBackupSchedule is due for, on every spec change
// and every Timing.ScheduleTick.
type ScheduleReconciler struct {
	Dependencies
}

func (r *ScheduleReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantbackupschedule").
		For(&qdrantv1alpha1.QdrantBackupSchedule{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *ScheduleReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantBackupSchedule{}
	if err := r.Live.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}
	spec, err := translate.ScheduleSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.fail(ctx, err)
	}
	current, err := translate.ScheduleStatusFromAPI(object.Status)
	if err != nil {
		return r.fail(ctx, err)
	}
	owner := usecases.ScheduleOwner{
		Reference: domain.OwnerReference{
			APIVersion: domain.APIVersion,
			Kind:       domain.Schedules.Kind,
			Name:       object.Name,
			UID:        string(object.UID),
		},
		CreationTime: object.CreationTimestamp.Time,
	}
	result, err := usecases.ProcessSchedule{Kubernetes: r.Kubernetes, Cron: r.Cron}.Execute(ctx, spec, current, owner, r.Clock())
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if usecases.Retryable(err) {
		return RetryAfter(ctx, err, r.Timing.Retry)
	}
	if err != nil {
		return r.fail(ctx, err)
	}
	if result.ActiveBackup != "" && result.ActiveBackup != current.ActiveBackup {
		log.FromContext(ctx).Info(fmt.Sprintf("Scheduled backup %s created", result.ActiveBackup))
	}
	status := translate.ScheduleStatusToAPI(result)
	if apiequality.Semantic.DeepEqual(status, object.Status) {
		return Every(r.Timing.ScheduleTick)
	}
	if err := WriteStatus(ctx, r.Client, object, status, r.Clock()); err != nil {
		return ctrl.Result{}, err
	}
	return Every(r.Timing.ScheduleTick)
}

// fail keeps the tick, as kopf's timer kept firing after an error.
func (r *ScheduleReconciler) fail(ctx context.Context, cause error) (ctrl.Result, error) {
	r.Metrics.ReconcileFailed(domain.Schedules)
	log.FromContext(ctx).Error(cause, "Schedule reconcile failed")
	return Every(r.Timing.ScheduleTick)
}

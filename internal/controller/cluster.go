package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/types"
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

// ClusterReconciler applies the Helm release of a QdrantCluster when its spec changes or a failed
// apply is due a retry, and otherwise observes, steps rollouts and recovers peers every
// Timing.ClusterObserve.
type ClusterReconciler struct {
	Dependencies
	memory clusterRegistry
}

func (r *ClusterReconciler) SetupWithManager(manager ctrl.Manager, concurrency int) error {
	return ctrl.NewControllerManagedBy(manager).
		Named("qdrantcluster").
		For(&qdrantv1alpha1.QdrantCluster{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		WithOptions(controller.Options{MaxConcurrentReconciles: concurrency}).
		Complete(r)
}

func (r *ClusterReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	object := &qdrantv1alpha1.QdrantCluster{}
	if err := r.Client.Get(ctx, request.NamespacedName, object); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !object.DeletionTimestamp.IsZero() {
		return r.delete(ctx, object)
	}
	if err := EnsureFinalizer(ctx, r.Client, object); err != nil {
		return ctrl.Result{}, err
	}
	current, err := translate.ClusterStatusFromAPI(object.Status)
	if err != nil {
		return r.invalid(err)
	}
	spec, err := translate.ClusterSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		return r.invalid(err)
	}
	now := r.Clock()
	memory := r.memory.load(request.NamespacedName, now)
	if r.applyDue(object, current, memory, now) {
		return r.apply(ctx, request.NamespacedName, spec, object, current, memory, now)
	}
	return r.observe(ctx, request.NamespacedName, spec, object, current, memory, now)
}

func (r *ClusterReconciler) applyDue(object *qdrantv1alpha1.QdrantCluster, current domain.ClusterStatus, memory clusterMemory, now time.Time) bool {
	if object.Generation != current.ObservedGeneration || memory.lastApply.IsZero() {
		return true
	}
	return usecases.IsApplyFailed(current.Conditions) && now.Sub(memory.lastApply) >= r.Timing.HelmRetry
}

func (r *ClusterReconciler) invalid(cause error) (ctrl.Result, error) {
	r.Metrics.ReconcileFailed(domain.Clusters)
	return Stop(cause)
}

func (r *ClusterReconciler) apply(ctx context.Context, key types.NamespacedName, spec domain.ClusterSpec, object *qdrantv1alpha1.QdrantCluster, current domain.ClusterStatus, memory clusterMemory, now time.Time) (ctrl.Result, error) {
	memory.lastApply = now
	r.memory.store(key, memory)
	reconcile := usecases.ReconcileCluster{Helm: r.Helm, Kubernetes: r.Kubernetes, Qdrant: r.Qdrant, Clock: r.Clock, Random: r.Random}
	result, err := reconcile.Execute(ctx, spec, object.Generation, current)
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	var pending *usecases.ScaleDownPendingError
	var rolling *usecases.RolloutInProgressError
	var release *usecases.ReleaseApplyError
	switch {
	case errors.As(err, &pending):
		return r.writeAndRetry(ctx, object, &pending.Status, err, r.Timing.ScaleDownPoll)
	case errors.As(err, &rolling):
		return r.writeAndRetry(ctx, object, rolling.Status, err, r.Timing.ScaleDownPoll)
	case errors.As(err, &release):
		r.Metrics.ReconcileFailed(domain.Clusters)
		return r.writeAndRetry(ctx, object, &release.Status, err, r.Timing.HelmRetry)
	case err != nil:
		r.Metrics.ReconcileFailed(domain.Clusters)
		return ctrl.Result{}, err
	}
	if err := r.WriteStatus(ctx, object, translate.ClusterStatusToAPI(result)); err != nil {
		return ctrl.Result{}, err
	}
	r.Metrics.SetClusterPhase(spec.Namespace, spec.Name, result.Phase)
	log.FromContext(ctx).Info(fmt.Sprintf("Helm release %s reconciled; phase %s", spec.ReleaseName(), result.Phase))
	return Every(r.Timing.ClusterObserve)
}

func (r *ClusterReconciler) writeAndRetry(ctx context.Context, object *qdrantv1alpha1.QdrantCluster, status *domain.ClusterStatus, cause error, delay time.Duration) (ctrl.Result, error) {
	if status != nil {
		if err := r.WriteStatus(ctx, object, translate.ClusterStatusToAPI(*status)); err != nil {
			return ctrl.Result{}, err
		}
	}
	return RetryAfter(ctx, cause, delay)
}

func (r *ClusterReconciler) observe(ctx context.Context, key types.NamespacedName, spec domain.ClusterSpec, object *qdrantv1alpha1.QdrantCluster, current domain.ClusterStatus, memory clusterMemory, now time.Time) (ctrl.Result, error) {
	stepped, err := usecases.StepRollout{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, current)
	if err != nil {
		return r.failed(ctx, err)
	}
	result, err := usecases.ObserveCluster{Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, stepped)
	if err != nil {
		return r.failed(ctx, err)
	}
	r.Metrics.SetClusterPhase(spec.Namespace, spec.Name, result.Phase)
	result, peerErr := r.recoverPeers(ctx, key, spec, result, memory, now)
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	if !reflect.DeepEqual(result, current) {
		if err := r.WriteStatus(ctx, object, translate.ClusterStatusToAPI(result)); err != nil {
			return ctrl.Result{}, err
		}
	}
	if peerErr != nil {
		r.Metrics.ReconcileFailed(domain.Clusters)
		return ctrl.Result{}, peerErr
	}
	return Every(r.nextPass(spec, result, r.memory.load(key, now), now))
}

func (r *ClusterReconciler) failed(ctx context.Context, err error) (ctrl.Result, error) {
	if Interrupted(ctx) {
		return ctrl.Result{}, ctx.Err()
	}
	r.Metrics.ReconcileFailed(domain.Clusters)
	return ctrl.Result{}, err
}

func (r *ClusterReconciler) recoverPeers(ctx context.Context, key types.NamespacedName, spec domain.ClusterSpec, status domain.ClusterStatus, memory clusterMemory, now time.Time) (domain.ClusterStatus, error) {
	if spec.Scaling.RemoveDeadPeers && now.Sub(memory.lastRecover) >= r.Timing.PeerRecovery {
		memory.lastRecover = now
		r.memory.store(key, memory)
		recovered, err := usecases.RecoverDeadPeers{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, status)
		if err != nil {
			return status, err
		}
		status = recovered
	}
	if spec.Scaling.Rebalance && now.Sub(memory.lastRebalance) >= r.Timing.PeerRecovery {
		memory.lastRebalance = now
		r.memory.store(key, memory)
		rebalanced, err := usecases.RebalanceShards{Qdrant: r.Qdrant, Kubernetes: r.Kubernetes, Clock: r.Clock}.Execute(ctx, spec, status)
		if err != nil {
			return status, err
		}
		status = rebalanced
	}
	return status, nil
}

func (r *ClusterReconciler) nextPass(spec domain.ClusterSpec, status domain.ClusterStatus, memory clusterMemory, now time.Time) time.Duration {
	next := r.Timing.ClusterObserve
	if usecases.IsApplyFailed(status.Conditions) {
		next = min(next, r.Timing.HelmRetry-now.Sub(memory.lastApply))
	}
	if spec.Scaling.RemoveDeadPeers {
		next = min(next, r.Timing.PeerRecovery-now.Sub(memory.lastRecover))
	}
	if spec.Scaling.Rebalance {
		next = min(next, r.Timing.PeerRecovery-now.Sub(memory.lastRebalance))
	}
	return max(next, 100*time.Millisecond)
}

func (r *ClusterReconciler) delete(ctx context.Context, object *qdrantv1alpha1.QdrantCluster) (ctrl.Result, error) {
	key := client.ObjectKeyFromObject(object)
	spec, err := translate.ClusterSpecFromAPI(object.Name, object.Namespace, object.Spec)
	if err != nil {
		log.FromContext(ctx).Error(err, "Spec does not translate; nothing to uninstall")
		return ctrl.Result{}, r.release(ctx, key, object)
	}
	if err := (usecases.DeleteCluster{Helm: r.Helm, Kubernetes: r.Kubernetes}).Execute(ctx, spec); err != nil {
		return ctrl.Result{}, err
	}
	log.FromContext(ctx).Info(fmt.Sprintf("Helm release %s uninstalled on delete", spec.ReleaseName()))
	return ctrl.Result{}, r.release(ctx, key, object)
}

func (r *ClusterReconciler) release(ctx context.Context, key types.NamespacedName, object *qdrantv1alpha1.QdrantCluster) error {
	r.Metrics.ForgetCluster(key.Namespace, key.Name)
	r.memory.forget(key)
	return ReleaseFinalizer(ctx, r.Client, object)
}

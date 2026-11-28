package controller

import (
	"context"
	"time"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// RetryAfter is kopf's TemporaryError: log the reason and reconcile again after delay.
func RetryAfter(ctx context.Context, err error, delay time.Duration) (ctrl.Result, error) {
	log.FromContext(ctx).Info("retrying later", "reason", err.Error(), "after", delay.String())
	return ctrl.Result{RequeueAfter: delay}, nil
}

// Interrupted reports a cancelled context, from shutdown or a lost lease, so the caller returns
// without recording the run as failed.
func Interrupted(ctx context.Context) bool {
	return ctx.Err() != nil
}

// Every requeues a level-triggered kind after interval.
func Every(interval time.Duration) (ctrl.Result, error) {
	return ctrl.Result{RequeueAfter: interval}, nil
}

// Stop is kopf's PermanentError for one-shot kinds: no requeue until the object changes.
func Stop(err error) (ctrl.Result, error) {
	return ctrl.Result{}, reconcile.TerminalError(err)
}

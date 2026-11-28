// Package controller holds one controller-runtime reconciler per custom resource kind. Reconcilers
// translate API objects, call use cases and write the status those return.
package controller

import (
	"io"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const (
	Finalizer       = domain.Group + "/finalizer"
	RetryAnnotation = domain.Group + "/retry"
)

// Timing holds the requeue intervals, the kopf timer intervals and retry delays of the Python operator.
type Timing struct {
	ClusterObserve      time.Duration
	ScheduleTick        time.Duration
	CollectionReconcile time.Duration
	AccessKeyRenew      time.Duration
	BackupExpiry        time.Duration
	PeerRecovery        time.Duration
	Retry               time.Duration
	ScaleDownPoll       time.Duration
	HelmRetry           time.Duration
}

func DefaultTiming() Timing {
	return Timing{
		ClusterObserve:      30 * time.Second,
		ScheduleTick:        time.Minute,
		CollectionReconcile: time.Minute,
		AccessKeyRenew:      time.Minute,
		BackupExpiry:        5 * time.Minute,
		PeerRecovery:        time.Minute,
		Retry:               30 * time.Second,
		ScaleDownPoll:       15 * time.Second,
		HelmRetry:           time.Minute,
	}
}

// Dependencies are shared by every reconciler. Client reads from the informer cache and writes
// status and finalizers; Live reads straight from the API server, for guards a stale cache would
// get wrong.
type Dependencies struct {
	Client     client.Client
	Live       client.Reader
	Kubernetes ports.Kubernetes
	Helm       ports.Helm
	Qdrant     ports.Qdrant
	Storage    ports.Storage
	Token      ports.Token
	Cron       usecases.CronParser
	Clock      usecases.Clock
	Random     io.Reader
	Metrics    *Metrics
	Timing     Timing
}

// Concurrency bounds parallel reconciles per kind. OneShots covers backups, restores and
// migrations; Levels covers every other kind.
type Concurrency struct {
	OneShots int
	Levels   int
}

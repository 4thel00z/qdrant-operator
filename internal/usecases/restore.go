package usecases

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

const DefaultRestoreIndexingPollInterval = 5 * time.Second

// RestoreSource is where a restore reads its snapshots and manifest from.
type RestoreSource struct {
	Storage     domain.S3StorageSpec
	RootKey     string
	Description string
}

// ExecuteRestore recovers the collections of a backup into a cluster.
// A collection that fails to restore is recorded in the returned status; only infrastructure errors,
// an unusable source and cancellation are returned. A backup that has not completed yet wraps ErrSourceNotReady.
type ExecuteRestore struct {
	Qdrant               ports.Qdrant
	Storage              ports.Storage
	Kubernetes           ports.Kubernetes
	Clock                Clock
	IndexingPollInterval time.Duration
}

func NewExecuteRestore(qdrant ports.Qdrant, storage ports.Storage, kubernetes ports.Kubernetes, clock Clock) ExecuteRestore {
	return ExecuteRestore{
		Qdrant:               qdrant,
		Storage:              storage,
		Kubernetes:           kubernetes,
		Clock:                clock,
		IndexingPollInterval: DefaultRestoreIndexingPollInterval,
	}
}

func (e ExecuteRestore) Execute(ctx context.Context, spec domain.RestoreSpec, ref domain.ResourceRef) (domain.RestoreStatus, error) {
	backup, err := e.readBackup(ctx, spec)
	if err != nil {
		return domain.RestoreStatus{}, err
	}
	if backup != nil && backup.Spec.Method == domain.BackupMethodVolumeSnapshot {
		return RestoreVolumeSnapshots{Kubernetes: e.Kubernetes, Clock: e.Clock}.Execute(ctx, spec, *backup)
	}
	startTime := backupNow(e.Clock)
	source, err := e.resolveSource(spec, backup)
	if err != nil {
		return domain.RestoreStatus{}, err
	}
	credentials, err := ReadCredentials(ctx, e.Kubernetes, source.Storage.CredentialsSecretRef)
	if err != nil {
		return domain.RestoreStatus{}, err
	}
	connection, err := ResolveCluster{Kubernetes: e.Kubernetes}.Execute(ctx, spec.TargetClusterRef)
	if err != nil {
		return domain.RestoreStatus{}, err
	}
	downloading := domain.RestoreStatus{Phase: domain.RestoreDownloading, StartTime: startTime, SourceBackup: source.Description}
	if err := patchRestoreStatus(ctx, e.Kubernetes, ref, downloading); err != nil {
		return domain.RestoreStatus{}, err
	}
	raw, err := e.Storage.GetObject(ctx, source.Storage, credentials, domain.JoinKey(source.RootKey, domain.ManifestKey))
	if err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("read manifest of %s: %w", source.Description, err)
	}
	manifest, err := domain.ParseManifest(raw)
	if err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("manifest of %s: %w", source.Description, err)
	}
	if manifest.NodeCount != len(connection.Nodes) {
		return domain.RestoreStatus{}, fmt.Errorf(
			"Backup was taken from %d nodes but target cluster has %d; node counts must match",
			manifest.NodeCount, len(connection.Nodes),
		)
	}
	collections, err := spec.SelectCollections(manifest.CollectionNames())
	if err != nil {
		return domain.RestoreStatus{}, err
	}

	restored := []domain.RestoredCollection{}
	for index, collection := range collections {
		restoring := domain.RestoreStatus{
			Phase:               domain.RestoreRestoring,
			StartTime:           startTime,
			SourceBackup:        source.Description,
			RestoredCollections: restored,
			Progress:            domain.RestoreProgress{CollectionsTotal: len(collections), CollectionsCompleted: index, CurrentCollection: collection},
		}
		if err := patchRestoreStatus(ctx, e.Kubernetes, ref, restoring); err != nil {
			return domain.RestoreStatus{}, err
		}
		outcome, err := e.restoreCollection(ctx, spec, source, credentials, connection, manifest, collection)
		if err != nil {
			return domain.RestoreStatus{}, err
		}
		restored = append(restored, outcome)
	}

	failed := []domain.RestoredCollection{}
	for _, outcome := range restored {
		if outcome.Status == domain.RestoreFailed {
			failed = append(failed, outcome)
		}
	}
	completionTime := backupNow(e.Clock)
	result := domain.RestoreStatus{
		Phase:               domain.RestoreCompleted,
		StartTime:           startTime,
		CompletionTime:      completionTime,
		SourceBackup:        source.Description,
		RestoredCollections: restored,
		Progress:            domain.RestoreProgress{CollectionsTotal: len(collections), CollectionsCompleted: len(collections) - len(failed)},
	}
	if len(failed) > 0 {
		result.Phase = domain.RestoreFailed
		result.Error = failed[0].Error
	}
	message := fmt.Sprintf("Restored %d/%d", len(restored)-len(failed), len(collections))
	result.Conditions = []domain.Condition{backupCondition(len(failed) == 0, completionTime, "RestoreCompleted", "RestoreFailed", message)}
	return result, nil
}

func patchRestoreStatus(ctx context.Context, kubernetes ports.Kubernetes, ref domain.ResourceRef, status domain.RestoreStatus) error {
	body, err := EncodeObject(translate.RestoreStatusToAPI(status))
	if err != nil {
		return fmt.Errorf("encode restore status: %w", err)
	}
	if err := kubernetes.PatchStatus(ctx, ref, body); err != nil {
		return fmt.Errorf("patch status of %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return nil
}

func (e ExecuteRestore) readBackup(ctx context.Context, spec domain.RestoreSpec) (*SourceBackup, error) {
	if spec.SourceS3 != nil || spec.BackupRef == nil {
		return nil, nil
	}
	ref := *spec.BackupRef
	body, err := e.Kubernetes.CustomResource(ctx, ref.ResourceRef())
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, fmt.Errorf("QdrantBackup %s/%s not found", ref.Namespace, ref.Name)
	}
	backup, err := DecodeBody[qdrantv1alpha1.QdrantBackup](body)
	if err != nil {
		return nil, err
	}
	status, err := translate.BackupStatusFromAPI(backup.Status)
	if err != nil {
		return nil, fmt.Errorf("status of QdrantBackup %s: %w", ref.Name, err)
	}
	if status.Phase != domain.BackupCompleted {
		return nil, fmt.Errorf("QdrantBackup %s is %s, not Completed: %w", ref.Name, status.Phase, ErrSourceNotReady)
	}
	backupSpec, err := translate.BackupSpecFromAPI(backup.Name, backup.Namespace, backup.Spec)
	if err != nil {
		return nil, err
	}
	return &SourceBackup{Spec: backupSpec, Status: status}, nil
}

func (e ExecuteRestore) resolveSource(spec domain.RestoreSpec, backup *SourceBackup) (RestoreSource, error) {
	if spec.SourceS3 != nil {
		return RestoreSource{Storage: *spec.SourceS3, RootKey: spec.SourceS3.Prefix, Description: spec.SourceS3.URI()}, nil
	}
	if backup == nil {
		return RestoreSource{}, fmt.Errorf("QdrantRestore needs either spec.backupRef or spec.source.s3")
	}
	storage, err := backup.Spec.S3Storage()
	if err != nil {
		return RestoreSource{}, err
	}
	rootKey, err := backup.Spec.RootKey()
	if err != nil {
		return RestoreSource{}, err
	}
	return RestoreSource{Storage: storage, RootKey: rootKey, Description: backup.Spec.Name}, nil
}

func (e ExecuteRestore) restoreCollection(
	ctx context.Context,
	spec domain.RestoreSpec,
	source RestoreSource,
	credentials domain.S3Credentials,
	connection domain.ClusterConnection,
	manifest domain.BackupManifest,
	collection string,
) (domain.RestoredCollection, error) {
	target := spec.TargetName(collection)
	original := ""
	if collection != target {
		original = collection
	}
	backup, ok := manifest.Collection(collection)
	if !ok {
		return domain.RestoredCollection{
			Name:         target,
			OriginalName: original,
			Status:       domain.RestoreFailed,
			Error:        fmt.Sprintf("Collection %s missing from manifest", collection),
		}, nil
	}
	failure := func(err error) (domain.RestoredCollection, error) {
		if ctx.Err() != nil {
			return domain.RestoredCollection{}, ctx.Err()
		}
		return domain.RestoredCollection{Name: target, OriginalName: original, Status: domain.RestoreFailed, Error: err.Error()}, nil
	}
	for _, record := range backup.Snapshots {
		if err := e.recoverNode(ctx, spec, source, credentials, connection, target, record); err != nil {
			return failure(err)
		}
	}
	points, err := e.AwaitIndexing(ctx, spec, connection.Service, target)
	if err != nil {
		return failure(err)
	}
	return domain.RestoredCollection{
		Name:         target,
		OriginalName: original,
		Status:       domain.RestoreCompleted,
		Size:         domain.FormatSize(backup.SizeBytes()),
		PointsCount:  points,
	}, nil
}

// recoverNode hands the node a presigned URL so the snapshot flows bucket to node without touching the operator.
func (e ExecuteRestore) recoverNode(
	ctx context.Context,
	spec domain.RestoreSpec,
	source RestoreSource,
	credentials domain.S3Credentials,
	connection domain.ClusterConnection,
	target string,
	record domain.SnapshotRecord,
) error {
	if record.NodeIndex < 0 || record.NodeIndex >= len(connection.Nodes) {
		return fmt.Errorf("snapshot %s belongs to node %d, which the target cluster does not have", record.SnapshotName, record.NodeIndex)
	}
	location, err := e.Storage.PresignedGetURL(ctx, source.Storage, credentials, record.Key, PresignedURLTTL)
	if err != nil {
		return fmt.Errorf("presign %s: %w", record.Key, err)
	}
	if err := e.Qdrant.RecoverSnapshot(ctx, connection.Nodes[record.NodeIndex], target, location, spec.Priority, record.Checksum); err != nil {
		return fmt.Errorf("recover %s into %s on node %d: %w", record.SnapshotName, target, record.NodeIndex, err)
	}
	return nil
}

// AwaitIndexing polls until the collection is green, or once when the spec does not wait; it returns the point count.
func (e ExecuteRestore) AwaitIndexing(ctx context.Context, spec domain.RestoreSpec, node domain.QdrantNode, collection string) (int64, error) {
	deadline := time.Now().Add(spec.IndexingTimeout)
	for {
		info, err := e.Qdrant.CollectionInfo(ctx, node, collection)
		if err != nil {
			return 0, fmt.Errorf("read collection %s: %w", collection, err)
		}
		if !spec.WaitForIndexing || info["status"] == "green" {
			return restorePointsCount(info), nil
		}
		if !time.Now().Before(deadline) {
			return 0, fmt.Errorf("%s", domain.IndexingTimeoutMessage(spec, collection, info))
		}
		if err := backupSleep(ctx, e.IndexingPollInterval); err != nil {
			return 0, err
		}
	}
}

func restorePointsCount(info domain.JSON) int64 {
	switch count := info["points_count"].(type) {
	case int:
		return int64(count)
	case int64:
		return count
	case float64:
		return int64(count)
	case json.Number:
		parsed, err := count.Int64()
		if err != nil {
			return 0
		}
		return parsed
	}
	return 0
}

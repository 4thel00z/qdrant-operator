package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// ExecuteBackup snapshots every node's shards of each collection and streams them into the bucket.
// A failing collection is recorded in the returned status; only infrastructure errors and cancellation are returned.
type ExecuteBackup struct {
	Qdrant     ports.Qdrant
	Storage    ports.Storage
	Kubernetes ports.Kubernetes
	Clock      Clock
}

func (e ExecuteBackup) Execute(ctx context.Context, spec domain.BackupSpec, ref domain.ResourceRef, previous *domain.BackupStatus) (domain.BackupStatus, error) {
	if spec.Method == domain.BackupMethodVolumeSnapshot {
		return NewExecuteVolumeSnapshotBackup(e.Kubernetes, e.Clock).Execute(ctx, spec, ref, previous)
	}
	storage, err := spec.S3Storage()
	if err != nil {
		return domain.BackupStatus{}, err
	}
	rootKey, err := spec.RootKey()
	if err != nil {
		return domain.BackupStatus{}, err
	}
	startTime := backupStartTime(previous, e.Clock)
	connection, err := ResolveCluster{Kubernetes: e.Kubernetes}.Execute(ctx, spec.ClusterRef)
	if err != nil {
		return domain.BackupStatus{}, err
	}
	credentials, err := ReadCredentials(ctx, e.Kubernetes, storage.CredentialsSecretRef)
	if err != nil {
		return domain.BackupStatus{}, err
	}
	if err := patchBackupStatus(ctx, e.Kubernetes, ref, domain.BackupStatus{Phase: domain.BackupInProgress, StartTime: startTime}); err != nil {
		return domain.BackupStatus{}, err
	}

	collections := spec.Collections
	if len(collections) == 0 {
		collections, err = e.Qdrant.ListCollections(ctx, connection.Service)
		if err != nil {
			return domain.BackupStatus{}, fmt.Errorf("list collections: %w", err)
		}
	}
	var marker *domain.InProgressSnapshot
	if previous != nil {
		marker = previous.InProgressSnapshot
	}
	cleanup := CleanupBackupSnapshots{Qdrant: e.Qdrant, Kubernetes: e.Kubernetes}
	if _, err := cleanup.DeleteLeftovers(ctx, connection, collections, startTime, marker); err != nil {
		return domain.BackupStatus{}, err
	}
	if _, err := e.Storage.AbortUploads(ctx, storage, credentials, rootKey); err != nil {
		return domain.BackupStatus{}, fmt.Errorf("abort unfinished uploads under %s: %w", rootKey, err)
	}

	statuses := make([]domain.CollectionBackupStatus, 0, len(collections))
	for _, collection := range collections {
		status, err := e.backupCollection(ctx, spec, ref, credentials, connection, collection)
		if err != nil {
			return domain.BackupStatus{}, err
		}
		statuses = append(statuses, status)
	}
	completed := []domain.CollectionBackupStatus{}
	failed := []domain.CollectionBackupStatus{}
	for _, status := range statuses {
		switch status.Status {
		case domain.BackupCompleted:
			completed = append(completed, status)
		case domain.BackupFailed:
			failed = append(failed, status)
		}
	}

	manifest := domain.BackupManifest{
		BackupName: spec.Name,
		Cluster:    spec.ClusterRef,
		NodeCount:  len(connection.Nodes),
		CreatedAt:  startTime,
	}
	for _, status := range completed {
		manifest.Collections = append(manifest.Collections, domain.CollectionBackup{Name: status.Name, Snapshots: status.Snapshots})
	}
	raw, err := json.Marshal(manifest.JSON())
	if err != nil {
		return domain.BackupStatus{}, fmt.Errorf("encode manifest: %w", err)
	}
	if err := e.Storage.PutObject(ctx, storage, credentials, domain.JoinKey(rootKey, domain.ManifestKey), raw); err != nil {
		return domain.BackupStatus{}, fmt.Errorf("write manifest of %s: %w", spec.Name, err)
	}

	completionTime := backupNow(e.Clock)
	result := domain.BackupStatus{
		Phase:          domain.BackupCompleted,
		StartTime:      startTime,
		CompletionTime: completionTime,
		S3Path:         storage.URI(spec.Name),
		TotalSize:      domain.FormatSize(manifest.SizeBytes()),
		Collections:    statuses,
	}
	result.ExpiresAt, _ = spec.ExpiresAt(completionTime)
	if len(failed) > 0 {
		result.Phase = domain.BackupFailed
		result.Error = failed[0].Error
	}
	message := fmt.Sprintf("Backed up %d/%d collections", len(completed), len(collections))
	result.Conditions = []domain.Condition{backupCondition(len(failed) == 0, completionTime, "BackupCompleted", "BackupFailed", message)}
	return result, nil
}

func (e ExecuteBackup) backupCollection(
	ctx context.Context,
	spec domain.BackupSpec,
	ref domain.ResourceRef,
	credentials domain.S3Credentials,
	connection domain.ClusterConnection,
	collection string,
) (domain.CollectionBackupStatus, error) {
	records := make([]domain.SnapshotRecord, 0, len(connection.Nodes))
	for index, node := range connection.Nodes {
		record, err := e.backupNode(ctx, spec, ref, credentials, node, index, collection)
		if ctx.Err() != nil {
			return domain.CollectionBackupStatus{}, ctx.Err()
		}
		if err != nil {
			return domain.CollectionBackupStatus{Name: collection, Status: domain.BackupFailed, Error: err.Error()}, nil
		}
		records = append(records, record)
	}
	return domain.CollectionBackupStatus{Name: collection, Status: domain.BackupCompleted, Snapshots: records}, nil
}

// backupNode snapshots one node's shards of a collection and streams them straight into the bucket.
// The node snapshot is deleted and the marker cleared whether or not the upload succeeded.
func (e ExecuteBackup) backupNode(
	ctx context.Context,
	spec domain.BackupSpec,
	ref domain.ResourceRef,
	credentials domain.S3Credentials,
	node domain.QdrantNode,
	index int,
	collection string,
) (domain.SnapshotRecord, error) {
	storage, err := spec.S3Storage()
	if err != nil {
		return domain.SnapshotRecord{}, err
	}
	snapshot, err := e.Qdrant.CreateSnapshot(ctx, node, collection)
	if err != nil {
		return domain.SnapshotRecord{}, fmt.Errorf("snapshot collection %s on node %d: %w", collection, index, err)
	}
	key, err := spec.SnapshotKey(collection, index, snapshot.Name)
	if err != nil {
		return domain.SnapshotRecord{}, err
	}
	marker := domain.InProgressSnapshot{NodeIndex: index, Collection: collection, Snapshot: snapshot.Name}
	size, streamErr := e.stream(ctx, storage, credentials, ref, node, marker, key)
	cleanupCtx := context.WithoutCancel(ctx)
	cleanupErr := errors.Join(
		e.Qdrant.DeleteSnapshot(cleanupCtx, node, collection, snapshot.Name),
		patchInProgressSnapshot(cleanupCtx, e.Kubernetes, ref, nil),
	)
	if err := errors.Join(streamErr, cleanupErr); err != nil {
		return domain.SnapshotRecord{}, err
	}
	return domain.SnapshotRecord{
		NodeIndex:    index,
		Key:          key,
		SnapshotName: snapshot.Name,
		SizeBytes:    size,
		Checksum:     snapshot.Checksum,
	}, nil
}

func (e ExecuteBackup) stream(
	ctx context.Context,
	storage domain.S3StorageSpec,
	credentials domain.S3Credentials,
	ref domain.ResourceRef,
	node domain.QdrantNode,
	marker domain.InProgressSnapshot,
	key string,
) (int64, error) {
	if err := patchInProgressSnapshot(ctx, e.Kubernetes, ref, &marker); err != nil {
		return 0, err
	}
	body, err := e.Qdrant.StreamSnapshot(ctx, node, marker.Collection, marker.Snapshot)
	if err != nil {
		return 0, fmt.Errorf("download snapshot %s: %w", marker.Snapshot, err)
	}
	defer body.Close()
	size, err := e.Storage.UploadStream(ctx, storage, credentials, key, body)
	if err != nil {
		return 0, fmt.Errorf("upload snapshot %s to %s: %w", marker.Snapshot, storage.URI(key), err)
	}
	return size, nil
}

// CleanupBackupSnapshots deletes node snapshots an interrupted backup attempt left behind.
type CleanupBackupSnapshots struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
}

// Execute returns how many snapshots were deleted.
func (c CleanupBackupSnapshots) Execute(ctx context.Context, spec domain.BackupSpec, status domain.BackupStatus) (int, error) {
	if spec.Method != domain.BackupMethodS3 {
		return 0, nil
	}
	if status.StartTime.IsZero() || status.Phase == domain.BackupCompleted {
		return 0, nil
	}
	connection, err := ResolveCluster{Kubernetes: c.Kubernetes}.Execute(ctx, spec.ClusterRef)
	if err != nil {
		return 0, err
	}
	collections := spec.Collections
	if len(collections) == 0 {
		collections, err = c.Qdrant.ListCollections(ctx, connection.Service)
		if err != nil {
			return 0, fmt.Errorf("list collections: %w", err)
		}
	}
	return c.DeleteLeftovers(ctx, connection, collections, status.StartTime, status.InProgressSnapshot)
}

type leftoverSnapshot struct {
	node       domain.QdrantNode
	collection string
	name       string
}

// DeleteLeftovers removes snapshots created at or after startTime, plus the one the marker names.
func (c CleanupBackupSnapshots) DeleteLeftovers(
	ctx context.Context,
	connection domain.ClusterConnection,
	collections []string,
	startTime time.Time,
	marker *domain.InProgressSnapshot,
) (int, error) {
	targets := []leftoverSnapshot{}
	for index, node := range connection.Nodes {
		for _, collection := range collections {
			names, err := c.leftoverNames(ctx, node, index, collection, startTime, marker)
			if err != nil {
				return 0, err
			}
			for _, name := range names {
				targets = append(targets, leftoverSnapshot{node: node, collection: collection, name: name})
			}
		}
	}
	for _, target := range targets {
		if err := c.Qdrant.DeleteSnapshot(ctx, target.node, target.collection, target.name); err != nil {
			return 0, fmt.Errorf("delete leftover snapshot %s of %s: %w", target.name, target.collection, err)
		}
	}
	return len(targets), nil
}

func (c CleanupBackupSnapshots) leftoverNames(
	ctx context.Context,
	node domain.QdrantNode,
	index int,
	collection string,
	startTime time.Time,
	marker *domain.InProgressSnapshot,
) ([]string, error) {
	listed, err := c.Qdrant.ListSnapshots(ctx, node, collection)
	if err != nil {
		return nil, fmt.Errorf("list snapshots of %s on node %d: %w", collection, index, err)
	}
	names := []string{}
	for _, snapshot := range listed {
		if !snapshot.CreatedAt.IsZero() && !snapshot.CreatedAt.Before(startTime) {
			names = append(names, snapshot.Name)
		}
	}
	if marker != nil && marker.NodeIndex == index && marker.Collection == collection && !slices.Contains(names, marker.Snapshot) {
		names = append(names, marker.Snapshot)
	}
	slices.Sort(names)
	return names, nil
}

type DeleteBackupData struct {
	Storage    ports.Storage
	Kubernetes ports.Kubernetes
}

// Execute deletes the backup's objects or VolumeSnapshots and aborts its unfinished uploads; it returns how many were deleted.
func (d DeleteBackupData) Execute(ctx context.Context, spec domain.BackupSpec) (int, error) {
	if spec.Method == domain.BackupMethodVolumeSnapshot {
		return DeleteVolumeSnapshots{Kubernetes: d.Kubernetes}.Execute(ctx, spec)
	}
	storage, err := spec.S3Storage()
	if err != nil {
		return 0, err
	}
	rootKey, err := spec.RootKey()
	if err != nil {
		return 0, err
	}
	credentials, err := ReadCredentials(ctx, d.Kubernetes, storage.CredentialsSecretRef)
	if err != nil {
		return 0, err
	}
	if _, err := d.Storage.AbortUploads(ctx, storage, credentials, rootKey); err != nil {
		return 0, fmt.Errorf("abort unfinished uploads under %s: %w", rootKey, err)
	}
	deleted, err := d.Storage.DeletePrefix(ctx, storage, credentials, rootKey)
	if err != nil {
		return 0, fmt.Errorf("delete objects under %s: %w", rootKey, err)
	}
	return deleted, nil
}

// ExpireBackup deletes a QdrantBackup once its retentionDays have passed.
type ExpireBackup struct {
	Kubernetes ports.Kubernetes
}

func (e ExpireBackup) Execute(ctx context.Context, ref domain.ResourceRef, status domain.BackupStatus, now time.Time) (bool, error) {
	if status.ExpiresAt.IsZero() || status.ExpiresAt.After(now) {
		return false, nil
	}
	if err := e.Kubernetes.DeleteCustomResource(ctx, ref); err != nil {
		return false, fmt.Errorf("delete expired %s %s/%s: %w", ref.Kind.Kind, ref.Namespace, ref.Name, err)
	}
	return true, nil
}

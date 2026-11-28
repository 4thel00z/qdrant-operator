package translate

import (
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func RestoreSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantRestoreSpec) (domain.RestoreSpec, error) {
	hasSource := spec.Source != nil && spec.Source.S3 != nil
	if spec.BackupRef == nil && !hasSource {
		return domain.RestoreSpec{}, fmt.Errorf("QdrantRestore needs either spec.backupRef or spec.source.s3")
	}
	restore := domain.DefaultRestoreSpec(name, namespace, backupClusterRef(spec.TargetClusterRef, namespace))
	restore.Priority = domain.RestorePriority(stringOr(string(spec.Priority), string(domain.PrioritySnapshot)))
	restore.WaitForIndexing = boolOr(spec.WaitForIndexing, restore.WaitForIndexing)
	if spec.BackupRef != nil {
		restore.BackupRef = &domain.BackupRef{Name: spec.BackupRef.Name, Namespace: stringOr(spec.BackupRef.Namespace, namespace)}
	}
	if hasSource {
		s3 := spec.Source.S3
		storage := backupS3Storage(s3.Bucket, s3.Path, s3.Region, s3.Endpoint, s3.ForcePathStyle, s3.CredentialsSecretRef, namespace)
		restore.SourceS3 = &storage
	}
	if spec.Collections != nil {
		restore.Collections = spec.Collections
	}
	if spec.CollectionMapping != nil {
		restore.CollectionMapping = spec.CollectionMapping
	}
	if spec.IndexingTimeoutSeconds != nil {
		restore.IndexingTimeout = time.Duration(*spec.IndexingTimeoutSeconds) * time.Second
	}
	return restore, nil
}

func RestoreStatusFromAPI(status qdrantv1alpha1.QdrantRestoreStatus) (domain.RestoreStatus, error) {
	restore := domain.RestoreStatus{
		Phase:        domain.RestorePhase(stringOr(string(status.Phase), string(domain.RestorePending))),
		SourceBackup: status.SourceBackup,
		Error:        status.Error,
	}
	var err error
	if restore.StartTime, err = domain.ParseTime(status.StartTime); err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("startTime: %w", err)
	}
	if restore.CompletionTime, err = domain.ParseTime(status.CompletionTime); err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("completionTime: %w", err)
	}
	if restore.Conditions, err = readBackupConditions(status.Conditions); err != nil {
		return domain.RestoreStatus{}, err
	}
	for _, collection := range status.RestoredCollections {
		restore.RestoredCollections = append(restore.RestoredCollections, domain.RestoredCollection{
			Name:         collection.Name,
			Status:       domain.RestorePhase(collection.Status),
			OriginalName: collection.OriginalName,
			Size:         collection.Size,
			PointsCount:  collection.PointsCount,
			Error:        collection.Error,
		})
	}
	if status.Progress != nil {
		restore.Progress = domain.RestoreProgress{
			CollectionsTotal:     int(status.Progress.CollectionsTotal),
			CollectionsCompleted: int(status.Progress.CollectionsCompleted),
			CurrentCollection:    status.Progress.CurrentCollection,
		}
	}
	return restore, nil
}

func RestoreStatusToAPI(status domain.RestoreStatus) qdrantv1alpha1.QdrantRestoreStatus {
	api := qdrantv1alpha1.QdrantRestoreStatus{
		Phase:          qdrantv1alpha1.RestorePhase(status.Phase),
		StartTime:      optionalTimeString(status.StartTime),
		CompletionTime: optionalTimeString(status.CompletionTime),
		SourceBackup:   status.SourceBackup,
		Error:          status.Error,
		Conditions:     writeBackupConditions(status.Conditions),
		Progress: &qdrantv1alpha1.RestoreProgress{
			CollectionsTotal:     int32(status.Progress.CollectionsTotal),
			CollectionsCompleted: int32(status.Progress.CollectionsCompleted),
			CurrentCollection:    status.Progress.CurrentCollection,
			Percentage:           int32(status.Progress.Percentage()),
		},
	}
	for _, collection := range status.RestoredCollections {
		api.RestoredCollections = append(api.RestoredCollections, qdrantv1alpha1.RestoredCollection{
			Name:         collection.Name,
			OriginalName: collection.OriginalName,
			Status:       qdrantv1alpha1.RestorePhase(collection.Status),
			Size:         collection.Size,
			PointsCount:  collection.PointsCount,
			Error:        collection.Error,
		})
	}
	return api
}

package translate

import (
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func BackupSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantBackupSpec) (domain.BackupSpec, error) {
	backup := domain.DefaultBackupSpec(name, namespace, backupClusterRef(spec.ClusterRef, namespace))
	backup.Method = domain.BackupMethod(stringOr(string(spec.Method), string(domain.BackupMethodS3)))
	backup.VolumeSnapshotClass = spec.VolumeSnapshotClassName
	if spec.Collections != nil {
		backup.Collections = spec.Collections
	}
	if spec.RetentionDays != nil {
		backup.RetentionDays = int(*spec.RetentionDays)
	}
	if backup.Method == domain.BackupMethodVolumeSnapshot {
		return backup, nil
	}
	if spec.Storage == nil {
		return domain.BackupSpec{}, fmt.Errorf("QdrantBackup %s: spec.storage.s3 is required when method is %s", name, backup.Method)
	}
	s3 := spec.Storage.S3
	storage := backupS3Storage(s3.Bucket, s3.Prefix, s3.Region, s3.Endpoint, s3.ForcePathStyle, s3.CredentialsSecretRef, namespace)
	backup.Storage = &storage
	return backup, nil
}

func backupClusterRef(ref qdrantv1alpha1.ClusterRef, namespace string) domain.ClusterRef {
	return domain.ClusterRef{Name: ref.Name, Namespace: stringOr(ref.Namespace, namespace)}
}

func backupS3Storage(
	bucket string,
	prefix string,
	region string,
	endpoint string,
	forcePathStyle bool,
	credentials qdrantv1alpha1.S3CredentialsSecretRef,
	namespace string,
) domain.S3StorageSpec {
	storage := domain.DefaultS3StorageSpec(bucket, backupCredentials(credentials, namespace))
	storage.Prefix = prefix
	storage.Region = stringOr(region, storage.Region)
	storage.Endpoint = endpoint
	storage.ForcePathStyle = forcePathStyle
	return storage
}

func backupCredentials(ref qdrantv1alpha1.S3CredentialsSecretRef, namespace string) domain.CredentialsSecretRef {
	credentials := domain.DefaultCredentialsSecretRef(ref.Name, namespace)
	credentials.AccessKeyIDKey = stringOr(ref.AccessKeyIDKey, credentials.AccessKeyIDKey)
	credentials.SecretAccessKeyKey = stringOr(ref.SecretAccessKeyKey, credentials.SecretAccessKeyKey)
	return credentials
}

func RetentionPolicyFromAPI(policy *qdrantv1alpha1.RetentionPolicy) domain.RetentionPolicy {
	if policy == nil {
		return domain.RetentionPolicy{}
	}
	return domain.RetentionPolicy{
		KeepLast:    optionalCount(policy.KeepLast),
		KeepDaily:   optionalCount(policy.KeepDaily),
		KeepWeekly:  optionalCount(policy.KeepWeekly),
		KeepMonthly: optionalCount(policy.KeepMonthly),
	}
}

func optionalCount(value *int32) *int {
	if value == nil {
		return nil
	}
	count := int(*value)
	return &count
}

// BackupRecordFromAPI reduces a QdrantBackup to what its schedule tracks.
func BackupRecordFromAPI(backup qdrantv1alpha1.QdrantBackup) (domain.BackupRecord, error) {
	status, err := BackupStatusFromAPI(backup.Status)
	if err != nil {
		return domain.BackupRecord{}, err
	}
	return domain.BackupRecord{
		Name:           backup.Name,
		CreationTime:   backup.CreationTimestamp.Time,
		Phase:          status.Phase,
		CompletionTime: status.CompletionTime,
		TotalSize:      status.TotalSize,
	}, nil
}

func RecentBackupToAPI(record domain.BackupRecord) qdrantv1alpha1.RecentBackup {
	return qdrantv1alpha1.RecentBackup{
		Name:           record.Name,
		CreationTime:   optionalTimeString(record.CreationTime),
		CompletionTime: optionalTimeString(record.CompletionTime),
		Status:         string(record.Phase),
		Size:           record.TotalSize,
	}
}

// BackupStatusFromAPI leaves Collections empty, as the API form does not carry the snapshot records.
func BackupStatusFromAPI(status qdrantv1alpha1.QdrantBackupStatus) (domain.BackupStatus, error) {
	backup := domain.BackupStatus{
		Phase:     domain.BackupPhase(stringOr(string(status.Phase), string(domain.BackupPending))),
		S3Path:    status.S3Path,
		TotalSize: status.TotalSize,
		Error:     status.Error,
	}
	var err error
	if backup.StartTime, err = domain.ParseTime(status.StartTime); err != nil {
		return domain.BackupStatus{}, fmt.Errorf("startTime: %w", err)
	}
	if backup.CompletionTime, err = domain.ParseTime(status.CompletionTime); err != nil {
		return domain.BackupStatus{}, fmt.Errorf("completionTime: %w", err)
	}
	if backup.ExpiresAt, err = domain.ParseTime(status.ExpiresAt); err != nil {
		return domain.BackupStatus{}, fmt.Errorf("expiresAt: %w", err)
	}
	if backup.Conditions, err = readBackupConditions(status.Conditions); err != nil {
		return domain.BackupStatus{}, err
	}
	backup.InProgressSnapshot = inProgressSnapshotFromAPI(status.InProgressSnapshot)
	for _, record := range status.VolumeSnapshots {
		backup.VolumeSnapshots = append(backup.VolumeSnapshots, VolumeSnapshotRecordFromAPI(record))
	}
	return backup, nil
}

func inProgressSnapshotFromAPI(snapshot *qdrantv1alpha1.BackupInProgressSnapshot) *domain.InProgressSnapshot {
	if snapshot == nil || *snapshot == (qdrantv1alpha1.BackupInProgressSnapshot{}) {
		return nil
	}
	return &domain.InProgressSnapshot{NodeIndex: int(snapshot.NodeIndex), Collection: snapshot.Collection, Snapshot: snapshot.Snapshot}
}

func BackupStatusToAPI(status domain.BackupStatus) qdrantv1alpha1.QdrantBackupStatus {
	api := qdrantv1alpha1.QdrantBackupStatus{
		Phase:          qdrantv1alpha1.BackupPhase(status.Phase),
		StartTime:      optionalTimeString(status.StartTime),
		CompletionTime: optionalTimeString(status.CompletionTime),
		ExpiresAt:      optionalTimeString(status.ExpiresAt),
		S3Path:         status.S3Path,
		TotalSize:      status.TotalSize,
		Error:          status.Error,
		Conditions:     writeBackupConditions(status.Conditions),
	}
	if status.InProgressSnapshot != nil {
		api.InProgressSnapshot = &qdrantv1alpha1.BackupInProgressSnapshot{
			NodeIndex:  int32(status.InProgressSnapshot.NodeIndex),
			Collection: status.InProgressSnapshot.Collection,
			Snapshot:   status.InProgressSnapshot.Snapshot,
		}
	}
	for _, collection := range status.Collections {
		api.Collections = append(api.Collections, CollectionBackupStatusToAPI(collection))
	}
	for _, record := range status.VolumeSnapshots {
		api.VolumeSnapshots = append(api.VolumeSnapshots, VolumeSnapshotRecordToAPI(record))
	}
	return api
}

func CollectionBackupStatusToAPI(collection domain.CollectionBackupStatus) qdrantv1alpha1.BackupCollectionStatus {
	api := qdrantv1alpha1.BackupCollectionStatus{
		Name:   collection.Name,
		Size:   collection.Size(),
		Status: qdrantv1alpha1.BackupPhase(collection.Status),
		Error:  collection.Error,
	}
	for _, snapshot := range collection.Snapshots {
		api.Snapshots = append(api.Snapshots, qdrantv1alpha1.BackupNodeSnapshot{
			Node:         fmt.Sprintf("node-%d", snapshot.NodeIndex),
			SnapshotName: snapshot.SnapshotName,
			Key:          snapshot.Key,
		})
	}
	return api
}

func VolumeSnapshotRecordFromAPI(record qdrantv1alpha1.BackupVolumeSnapshot) domain.VolumeSnapshotRecord {
	volume := domain.VolumeSnapshotRecord{
		NodeIndex:    int(record.NodeIndex),
		ClaimName:    record.ClaimName,
		SnapshotName: record.SnapshotName,
		Size:         record.Size,
		StorageClass: record.StorageClassName,
		AccessModes:  []string{domain.DefaultAccessMode},
	}
	if len(record.AccessModes) > 0 {
		volume.AccessModes = record.AccessModes
	}
	if record.Zone != "" && record.ZoneLabel != "" {
		volume.Zone = &domain.VolumeZone{Label: record.ZoneLabel, Name: record.Zone}
	}
	return volume
}

func VolumeSnapshotRecordToAPI(record domain.VolumeSnapshotRecord) qdrantv1alpha1.BackupVolumeSnapshot {
	api := qdrantv1alpha1.BackupVolumeSnapshot{
		NodeIndex:        int32(record.NodeIndex),
		ClaimName:        record.ClaimName,
		SnapshotName:     record.SnapshotName,
		Size:             record.Size,
		StorageClassName: record.StorageClass,
		AccessModes:      record.AccessModes,
	}
	if record.Zone != nil {
		api.Zone = record.Zone.Name
		api.ZoneLabel = record.Zone.Label
	}
	return api
}

func readBackupConditions(conditions []qdrantv1alpha1.Condition) ([]domain.Condition, error) {
	result := make([]domain.Condition, 0, len(conditions))
	for _, condition := range conditions {
		transition, err := domain.ParseTime(condition.LastTransitionTime)
		if err != nil {
			return nil, fmt.Errorf("condition %s lastTransitionTime: %w", condition.Type, err)
		}
		result = append(result, domain.Condition{
			Type:               condition.Type,
			Status:             domain.ConditionStatus(condition.Status),
			LastTransitionTime: transition,
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return result, nil
}

func writeBackupConditions(conditions []domain.Condition) []qdrantv1alpha1.Condition {
	var result []qdrantv1alpha1.Condition
	for _, condition := range conditions {
		result = append(result, qdrantv1alpha1.Condition{
			Type:               condition.Type,
			Status:             qdrantv1alpha1.ConditionStatus(condition.Status),
			LastTransitionTime: optionalTimeString(condition.LastTransitionTime),
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return result
}

func optionalTimeString(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return domain.FormatTime(value)
}

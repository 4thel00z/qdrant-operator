package translate

import (
	"fmt"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func ScheduleSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantBackupScheduleSpec) (domain.BackupScheduleSpec, error) {
	schedule := domain.DefaultBackupScheduleSpec(name, namespace, spec.Schedule, backupClusterRef(spec.ClusterRef, namespace))
	schedule.Method = domain.BackupMethod(stringOr(string(spec.Method), string(domain.BackupMethodS3)))
	schedule.VolumeSnapshotClass = spec.VolumeSnapshotClassName
	schedule.RetentionPolicy = RetentionPolicyFromAPI(spec.RetentionPolicy)
	schedule.Suspend = spec.Suspend
	schedule.DeleteBackupsWithSchedule = spec.DeleteBackupsWithSchedule
	schedule.ConcurrencyPolicy = domain.ConcurrencyPolicy(stringOr(string(spec.ConcurrencyPolicy), string(domain.ConcurrencyForbid)))
	if spec.Collections != nil {
		schedule.Collections = spec.Collections
	}
	if spec.StartingDeadlineSeconds != nil {
		deadline := time.Duration(*spec.StartingDeadlineSeconds) * time.Second
		schedule.StartingDeadline = &deadline
	}
	if schedule.Method == domain.BackupMethodVolumeSnapshot {
		return schedule, nil
	}
	if spec.Storage == nil {
		return domain.BackupScheduleSpec{}, fmt.Errorf("QdrantBackupSchedule %s: spec.storage.s3 is required when method is %s", name, schedule.Method)
	}
	s3 := spec.Storage.S3
	storage := backupS3Storage(s3.Bucket, s3.Prefix, s3.Region, s3.Endpoint, s3.ForcePathStyle, s3.CredentialsSecretRef, namespace)
	schedule.Storage = &storage
	return schedule, nil
}

// ScheduleStatusFromAPI leaves RecentBackups empty: the operator rebuilds them from the live backups.
func ScheduleStatusFromAPI(status qdrantv1alpha1.QdrantBackupScheduleStatus) (domain.BackupScheduleStatus, error) {
	schedule := domain.BackupScheduleStatus{
		Phase:            domain.SchedulePhase(stringOr(string(status.Phase), string(domain.ScheduleActive))),
		LastBackupName:   status.LastBackupName,
		LastBackupStatus: domain.BackupPhase(status.LastBackupStatus),
		ActiveBackup:     status.ActiveBackup,
	}
	var err error
	if schedule.LastScheduleTime, err = domain.ParseTime(status.LastScheduleTime); err != nil {
		return domain.BackupScheduleStatus{}, fmt.Errorf("lastScheduleTime: %w", err)
	}
	if schedule.LastBackupTime, err = domain.ParseTime(status.LastBackupTime); err != nil {
		return domain.BackupScheduleStatus{}, fmt.Errorf("lastBackupTime: %w", err)
	}
	if schedule.NextBackupTime, err = domain.ParseTime(status.NextBackupTime); err != nil {
		return domain.BackupScheduleStatus{}, fmt.Errorf("nextBackupTime: %w", err)
	}
	if schedule.Conditions, err = readBackupConditions(status.Conditions); err != nil {
		return domain.BackupScheduleStatus{}, err
	}
	return schedule, nil
}

func ScheduleStatusToAPI(status domain.BackupScheduleStatus) qdrantv1alpha1.QdrantBackupScheduleStatus {
	api := qdrantv1alpha1.QdrantBackupScheduleStatus{
		Phase:            qdrantv1alpha1.SchedulePhase(status.Phase),
		LastScheduleTime: optionalTimeString(status.LastScheduleTime),
		LastBackupTime:   optionalTimeString(status.LastBackupTime),
		LastBackupName:   status.LastBackupName,
		LastBackupStatus: qdrantv1alpha1.BackupPhase(status.LastBackupStatus),
		NextBackupTime:   optionalTimeString(status.NextBackupTime),
		ActiveBackup:     status.ActiveBackup,
		Conditions:       writeBackupConditions(status.Conditions),
	}
	for _, record := range status.RecentBackups {
		api.RecentBackups = append(api.RecentBackups, RecentBackupToAPI(record))
	}
	return api
}

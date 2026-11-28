package usecases

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

// CronParser turns a cron expression into its slots.
type CronParser func(expression string) (domain.CronSchedule, error)

// ScheduleOwner is the QdrantBackupSchedule a backup is spawned for: its reference and when it was created.
type ScheduleOwner struct {
	Reference    domain.OwnerReference
	CreationTime time.Time
}

// ProcessSchedule prunes a schedule's backups, launches the slot that is due and reports the schedule's status.
type ProcessSchedule struct {
	Kubernetes ports.Kubernetes
	Cron       CronParser
}

func (p ProcessSchedule) Execute(
	ctx context.Context,
	spec domain.BackupScheduleSpec,
	status domain.BackupScheduleStatus,
	owner ScheduleOwner,
	now time.Time,
) (domain.BackupScheduleStatus, error) {
	records, err := p.listBackups(ctx, spec)
	if err != nil {
		return domain.BackupScheduleStatus{}, err
	}
	records, err = p.applyRetention(ctx, spec, records)
	if err != nil {
		return domain.BackupScheduleStatus{}, err
	}
	active := []domain.BackupRecord{}
	finished := []domain.BackupRecord{}
	for _, record := range records {
		if record.Finished() {
			finished = append(finished, record)
			continue
		}
		active = append(active, record)
	}
	if spec.Suspend {
		return scheduleStatus(domain.ScheduleSuspended, status, records, active, time.Time{}), nil
	}

	baseline := status.LastScheduleTime
	if baseline.IsZero() {
		baseline = owner.CreationTime
	}
	cron, err := p.Cron(spec.Schedule)
	if err != nil {
		return domain.BackupScheduleStatus{}, fmt.Errorf("schedule %q: %w", spec.Schedule, err)
	}
	slot, due, err := spec.Due(cron, now, baseline)
	if err != nil {
		return domain.BackupScheduleStatus{}, err
	}
	launched := ""
	if due {
		launched, err = p.launch(ctx, spec, owner, slot, active)
		if err != nil {
			return domain.BackupScheduleStatus{}, err
		}
	}
	lastScheduleTime := status.LastScheduleTime
	if launched != "" {
		lastScheduleTime = slot
	}
	next, err := spec.NextSlot(cron, now)
	if err != nil {
		return domain.BackupScheduleStatus{}, err
	}
	result := scheduleStatus(domain.ScheduleActive, status, records, active, next)
	result.LastScheduleTime = lastScheduleTime
	if launched == "" {
		return result, nil
	}
	result.ActiveBackup = launched
	if len(finished) == 0 {
		result.LastBackupName = launched
	}
	return result, nil
}

func (p ProcessSchedule) listBackups(ctx context.Context, spec domain.BackupScheduleSpec) ([]domain.BackupRecord, error) {
	bodies, err := p.Kubernetes.ListCustomResources(ctx, domain.Backups, spec.Namespace, spec.LabelSelector())
	if err != nil {
		return nil, fmt.Errorf("list backups of schedule %s: %w", spec.Name, err)
	}
	records := make([]domain.BackupRecord, 0, len(bodies))
	for _, body := range bodies {
		backup, err := DecodeBody[qdrantv1alpha1.QdrantBackup](body)
		if err != nil {
			return nil, err
		}
		record, err := translate.BackupRecordFromAPI(backup)
		if err != nil {
			return nil, fmt.Errorf("backup %s: %w", backup.Name, err)
		}
		records = append(records, record)
	}
	sort.SliceStable(records, func(i int, j int) bool { return records[i].CreationTime.After(records[j].CreationTime) })
	return records, nil
}

func (p ProcessSchedule) applyRetention(ctx context.Context, spec domain.BackupScheduleSpec, records []domain.BackupRecord) ([]domain.BackupRecord, error) {
	finished := []domain.BackupRecord{}
	for _, record := range records {
		if record.Finished() {
			finished = append(finished, record)
		}
	}
	for _, expired := range spec.RetentionPolicy.Expired(finished) {
		ref := domain.ResourceRef{Kind: domain.Backups, Name: expired.Name, Namespace: spec.Namespace}
		if err := p.Kubernetes.DeleteCustomResource(ctx, ref); err != nil {
			return nil, fmt.Errorf("delete expired backup %s: %w", expired.Name, err)
		}
		if err := p.deleteVolumeSnapshots(ctx, spec, expired.Name); err != nil {
			return nil, err
		}
		records = slices.DeleteFunc(slices.Clone(records), func(record domain.BackupRecord) bool { return record.Name == expired.Name })
	}
	return records, nil
}

func (p ProcessSchedule) deleteVolumeSnapshots(ctx context.Context, spec domain.BackupScheduleSpec, backup string) error {
	if spec.Method != domain.BackupMethodVolumeSnapshot {
		return nil
	}
	backupSpec := domain.BackupSpec{Name: backup, Namespace: spec.Namespace, ClusterRef: spec.ClusterRef, Method: spec.Method}
	_, err := DeleteVolumeSnapshots{Kubernetes: p.Kubernetes}.Execute(ctx, backupSpec)
	return err
}

func (p ProcessSchedule) launch(
	ctx context.Context,
	spec domain.BackupScheduleSpec,
	owner ScheduleOwner,
	slot time.Time,
	active []domain.BackupRecord,
) (string, error) {
	if len(active) > 0 && spec.ConcurrencyPolicy == domain.ConcurrencyForbid {
		return "", nil
	}
	if len(active) > 0 && spec.ConcurrencyPolicy == domain.ConcurrencyReplace {
		for _, record := range active {
			ref := domain.ResourceRef{Kind: domain.Backups, Name: record.Name, Namespace: spec.Namespace}
			if err := p.Kubernetes.DeleteCustomResource(ctx, ref); err != nil {
				return "", fmt.Errorf("replace running backup %s: %w", record.Name, err)
			}
		}
	}
	name := spec.BackupName(slot)
	if _, err := p.Kubernetes.CreateCustomResource(ctx, spec.ToBackupResource(name, owner.Reference)); err != nil {
		return "", fmt.Errorf("create backup %s: %w", name, err)
	}
	return name, nil
}

func scheduleStatus(
	phase domain.SchedulePhase,
	status domain.BackupScheduleStatus,
	records []domain.BackupRecord,
	active []domain.BackupRecord,
	next time.Time,
) domain.BackupScheduleStatus {
	result := domain.BackupScheduleStatus{
		Phase:            phase,
		LastScheduleTime: status.LastScheduleTime,
		NextBackupTime:   next,
		RecentBackups:    records[:min(RecentBackupsLimit, len(records))],
	}
	if len(active) > 0 {
		result.ActiveBackup = active[0].Name
	}
	for _, record := range records {
		if !record.Finished() {
			continue
		}
		result.LastBackupTime = record.CompletionTime
		result.LastBackupName = record.Name
		result.LastBackupStatus = record.Phase
		break
	}
	return result
}

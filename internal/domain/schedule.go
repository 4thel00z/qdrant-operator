package domain

import (
	"fmt"
	"time"
)

type SchedulePhase string

const (
	ScheduleActive    SchedulePhase = "Active"
	ScheduleSuspended SchedulePhase = "Suspended"
)

type ConcurrencyPolicy string

const (
	ConcurrencyAllow   ConcurrencyPolicy = "Allow"
	ConcurrencyForbid  ConcurrencyPolicy = "Forbid"
	ConcurrencyReplace ConcurrencyPolicy = "Replace"
)

// CronSchedule resolves the slots of one cron expression; the adapter that parses the expression implements it.
type CronSchedule interface {
	Previous(after time.Time) (time.Time, error)
	Next(after time.Time) (time.Time, error)
}

type BackupScheduleSpec struct {
	Name                      string
	Namespace                 string
	Schedule                  string
	ClusterRef                ClusterRef
	Storage                   *S3StorageSpec
	Collections               []string
	RetentionPolicy           RetentionPolicy
	Suspend                   bool
	ConcurrencyPolicy         ConcurrencyPolicy
	StartingDeadline          *time.Duration
	DeleteBackupsWithSchedule bool
	Method                    BackupMethod
	VolumeSnapshotClass       string
}

func DefaultBackupScheduleSpec(name string, namespace string, schedule string, cluster ClusterRef) BackupScheduleSpec {
	return BackupScheduleSpec{
		Name:              name,
		Namespace:         namespace,
		Schedule:          schedule,
		ClusterRef:        cluster,
		Collections:       []string{},
		ConcurrencyPolicy: ConcurrencyForbid,
		Method:            BackupMethodS3,
	}
}

func (s BackupScheduleSpec) LabelSelector() string {
	return fmt.Sprintf("%s=%s", ScheduleLabel, s.Name)
}

func (s BackupScheduleSpec) BackupName(at time.Time) string {
	return s.Name + "-" + at.UTC().Format("20060102-150405")
}

func (s BackupScheduleSpec) PreviousSlot(cron CronSchedule, now time.Time) (time.Time, error) {
	slot, err := cron.Previous(now)
	if err != nil {
		return time.Time{}, fmt.Errorf("previous slot of %q: %w", s.Schedule, err)
	}
	return slot, nil
}

func (s BackupScheduleSpec) NextSlot(cron CronSchedule, now time.Time) (time.Time, error) {
	slot, err := cron.Next(now)
	if err != nil {
		return time.Time{}, fmt.Errorf("next slot of %q: %w", s.Schedule, err)
	}
	return slot, nil
}

// Due returns the missed slot after baseline (the last run or the creation time), or false when up to date.
func (s BackupScheduleSpec) Due(cron CronSchedule, now time.Time, baseline time.Time) (time.Time, bool, error) {
	slot, err := s.PreviousSlot(cron, now)
	if err != nil {
		return time.Time{}, false, err
	}
	if !baseline.IsZero() && !baseline.Before(slot) {
		return time.Time{}, false, nil
	}
	if s.StartingDeadline != nil && now.Sub(slot) > *s.StartingDeadline {
		return time.Time{}, false, nil
	}
	return slot, true, nil
}

// OwnerReference makes the owner garbage-collect the object that carries it.
type OwnerReference struct {
	APIVersion string
	Kind       string
	Name       string
	UID        string
}

func (o OwnerReference) JSON() JSON {
	return JSON{
		"apiVersion":         o.APIVersion,
		"kind":               o.Kind,
		"name":               o.Name,
		"uid":                o.UID,
		"controller":         true,
		"blockOwnerDeletion": true,
	}
}

// ToBackupResource is the QdrantBackup this schedule spawns.
// It is owned by the schedule only when DeleteBackupsWithSchedule is set; otherwise backups outlive the schedule.
func (s BackupScheduleSpec) ToBackupResource(backupName string, owner OwnerReference) JSON {
	spec := JSON{"clusterRef": clusterRefJSON(s.ClusterRef), "method": string(s.Method)}
	if s.Storage != nil {
		spec["storage"] = JSON{"s3": s.Storage.JSON()}
	}
	if s.VolumeSnapshotClass != "" {
		spec["volumeSnapshotClassName"] = s.VolumeSnapshotClass
	}
	if len(s.Collections) > 0 {
		spec["collections"] = append([]string{}, s.Collections...)
	}
	metadata := JSON{
		"name":      backupName,
		"namespace": s.Namespace,
		"labels":    JSON{ScheduleLabel: s.Name},
	}
	if s.DeleteBackupsWithSchedule {
		metadata["ownerReferences"] = []JSON{owner.JSON()}
	}
	return JSON{"apiVersion": APIVersion, "kind": Backups.Kind, "metadata": metadata, "spec": spec}
}

type BackupScheduleStatus struct {
	Phase            SchedulePhase
	LastScheduleTime time.Time
	LastBackupTime   time.Time
	LastBackupName   string
	LastBackupStatus BackupPhase
	NextBackupTime   time.Time
	ActiveBackup     string
	RecentBackups    []BackupRecord
	Conditions       []Condition
}

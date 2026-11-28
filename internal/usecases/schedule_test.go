package usecases_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/adapters/cron"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func scheduleCron(expression string) (domain.CronSchedule, error) {
	return cron.Parse(expression)
}

func scheduleSpecOf(t *testing.T, extra domain.JSON) domain.BackupScheduleSpec {
	t.Helper()
	merged := domain.JSON{
		"schedule":   "0 2 * * *",
		"clusterRef": domain.JSON{"name": "db"},
		"storage":    domain.JSON{"s3": backupS3()},
	}
	for key, value := range extra {
		merged[key] = value
	}
	api, err := usecases.DecodeBody[qdrantv1alpha1.QdrantBackupScheduleSpec](merged)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := translate.ScheduleSpecFromAPI("sched", backupNamespace, api)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

func scheduleOwner(created time.Time) usecases.ScheduleOwner {
	return usecases.ScheduleOwner{
		Reference:    domain.OwnerReference{APIVersion: domain.APIVersion, Kind: "QdrantBackupSchedule", Name: "sched", UID: "sched-uid"},
		CreationTime: created,
	}
}

func (w backupWorld) processSchedule() usecases.ProcessSchedule {
	return usecases.ProcessSchedule{Kubernetes: w.kubernetes, Cron: scheduleCron}
}

func (w backupWorld) spawnedBackups() []string {
	names := []string{}
	for key, body := range w.kubernetes.Resources {
		if strings.HasPrefix(key, "qdrantbackups/") {
			names = append(names, body["metadata"].(domain.JSON)["name"].(string))
		}
	}
	slices.Sort(names)
	return names
}

func scheduledBackup(name string, created time.Time, status domain.JSON) domain.JSON {
	return domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantBackup",
		"metadata": domain.JSON{
			"name":              name,
			"namespace":         backupNamespace,
			"labels":            domain.JSON{domain.ScheduleLabel: "sched"},
			"creationTimestamp": domain.FormatTime(created),
		},
		"spec":   domain.JSON{},
		"status": status,
	}
}

func TestScheduleFiresMissedSlotOnceAndRecordsIt(t *testing.T) {
	ctx := context.Background()
	world := newBackupWorld()
	use := world.processSchedule()
	spec := scheduleSpecOf(t, nil)
	owner := scheduleOwner(backupTestNow.Add(-24 * time.Hour))

	first, err := use.Execute(ctx, spec, domain.BackupScheduleStatus{Phase: domain.ScheduleActive}, owner, backupTestNow)
	if err != nil {
		t.Fatal(err)
	}
	second, err := use.Execute(ctx, spec, first, owner, backupTestNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	if names := world.spawnedBackups(); !slices.Equal(names, []string{"sched-20260923-020000"}) {
		t.Fatalf("backups %v", names)
	}
	body := world.kubernetes.Resources["qdrantbackups/tenant-a/sched-20260923-020000"]
	if labels := body["metadata"].(domain.JSON)["labels"]; !reflect.DeepEqual(labels, domain.JSON{"qdrant.io/schedule": "sched"}) {
		t.Errorf("labels %v", labels)
	}
	slot := time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)
	if !first.LastScheduleTime.Equal(slot) || first.ActiveBackup != "sched-20260923-020000" {
		t.Errorf("first %+v", first)
	}
	if want := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC); !first.NextBackupTime.Equal(want) {
		t.Errorf("next %s", first.NextBackupTime)
	}
	if second.ActiveBackup != "sched-20260923-020000" || second.LastBackupName != "" {
		t.Errorf("second %+v", second)
	}
}

func TestNewScheduleDoesNotFireSlotsOlderThanItself(t *testing.T) {
	world := newBackupWorld()

	status, err := world.processSchedule().Execute(context.Background(), scheduleSpecOf(t, nil), domain.BackupScheduleStatus{Phase: domain.ScheduleActive}, scheduleOwner(backupTestNow), backupTestNow)
	if err != nil {
		t.Fatal(err)
	}

	if names := world.spawnedBackups(); len(names) != 0 {
		t.Errorf("backups %v", names)
	}
	if !status.LastScheduleTime.IsZero() {
		t.Errorf("last schedule time %s", status.LastScheduleTime)
	}
	if want := time.Date(2026, 9, 24, 2, 0, 0, 0, time.UTC); !status.NextBackupTime.Equal(want) {
		t.Errorf("next %s", status.NextBackupTime)
	}
}

func TestScheduleConcurrencyPolicies(t *testing.T) {
	tests := []struct {
		policy       string
		wantBackups  []string
		wantActive   string
		wantLaunched bool
	}{
		{policy: "Forbid", wantBackups: []string{"sched-old"}, wantActive: "sched-old"},
		{policy: "Allow", wantBackups: []string{"sched-20260923-020000", "sched-old"}, wantActive: "sched-20260923-020000", wantLaunched: true},
		{policy: "Replace", wantBackups: []string{"sched-20260923-020000"}, wantActive: "sched-20260923-020000", wantLaunched: true},
	}
	for _, test := range tests {
		t.Run(test.policy, func(t *testing.T) {
			world := newBackupWorld()
			world.kubernetes.PutResource(scheduledBackup("sched-old", backupTestNow.Add(-time.Hour), domain.JSON{"phase": "InProgress"}))
			spec := scheduleSpecOf(t, domain.JSON{"concurrencyPolicy": test.policy})

			status, err := world.processSchedule().Execute(context.Background(), spec, domain.BackupScheduleStatus{Phase: domain.ScheduleActive}, scheduleOwner(backupTestNow.Add(-24*time.Hour)), backupTestNow)
			if err != nil {
				t.Fatal(err)
			}

			if names := world.spawnedBackups(); !slices.Equal(names, test.wantBackups) {
				t.Errorf("backups %v, want %v", names, test.wantBackups)
			}
			if status.LastScheduleTime.IsZero() == test.wantLaunched {
				t.Errorf("last schedule time %s, launched %v", status.LastScheduleTime, test.wantLaunched)
			}
			if status.ActiveBackup != test.wantActive {
				t.Errorf("active %s, want %s", status.ActiveBackup, test.wantActive)
			}
		})
	}
}

func TestScheduleAppliesRetentionAndSuspend(t *testing.T) {
	world := newBackupWorld()
	for daysAgo := range 4 {
		completion := backupTestNow.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		world.kubernetes.PutResource(scheduledBackup(fmt.Sprintf("sched-%d", daysAgo), completion.Add(-time.Hour), domain.JSON{
			"phase":          "Completed",
			"completionTime": domain.FormatTime(completion),
			"totalSize":      "1.0MB",
		}))
	}
	spec := scheduleSpecOf(t, domain.JSON{"retentionPolicy": domain.JSON{"keepLast": 2}, "suspend": true})

	status, err := world.processSchedule().Execute(context.Background(), spec, domain.BackupScheduleStatus{Phase: domain.ScheduleActive}, scheduleOwner(backupTestNow.Add(-24*time.Hour)), backupTestNow)
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.ScheduleSuspended {
		t.Errorf("phase %s", status.Phase)
	}
	if names := world.spawnedBackups(); !slices.Equal(names, []string{"sched-0", "sched-1"}) {
		t.Errorf("backups %v", names)
	}
	deleted := []string{}
	for _, ref := range world.kubernetes.Deleted {
		deleted = append(deleted, ref.Name)
	}
	if !slices.Equal(deleted, []string{"sched-2", "sched-3"}) {
		t.Errorf("deleted %v", deleted)
	}
	if status.LastBackupName != "sched-0" || status.LastBackupStatus != domain.BackupCompleted || !status.NextBackupTime.IsZero() {
		t.Errorf("status %+v", status)
	}
	recent := []string{}
	for _, record := range status.RecentBackups {
		recent = append(recent, record.Name)
	}
	if !slices.Equal(recent, []string{"sched-0", "sched-1"}) {
		t.Errorf("recent %v", recent)
	}
}

func TestScheduleRetentionDeletesTheVolumeSnapshotsOfPrunedBackups(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(2)
	for daysAgo := range 3 {
		name := fmt.Sprintf("sched-%d", daysAgo)
		backupRun(t, volumeBackupSpecOf(t, name), world)
		completion := backupTestNow.Add(-time.Duration(daysAgo) * 24 * time.Hour)
		world.kubernetes.PutResource(scheduledBackup(name, completion.Add(-time.Hour), domain.JSON{
			"phase":          "Completed",
			"completionTime": domain.FormatTime(completion),
		}))
	}
	spec := scheduleSpecOf(t, domain.JSON{
		"method":                  "volumeSnapshot",
		"volumeSnapshotClassName": volumeSnapshotClass,
		"retentionPolicy":         domain.JSON{"keepLast": 1},
		"suspend":                 true,
	})

	_, err := world.processSchedule().Execute(context.Background(), spec, domain.BackupScheduleStatus{Phase: domain.ScheduleActive}, scheduleOwner(backupTestNow.Add(-24*time.Hour)), backupTestNow)
	if err != nil {
		t.Fatal(err)
	}

	deleted := []string{}
	for _, ref := range world.kubernetes.Deleted {
		deleted = append(deleted, ref.Name)
	}
	if !slices.Equal(deleted, []string{"sched-1", "sched-2"}) {
		t.Errorf("deleted %v", deleted)
	}
	if keys := world.snapshotKeys(); !slices.Equal(keys, []string{"tenant-a/sched-0-node-0", "tenant-a/sched-0-node-1"}) {
		t.Errorf("snapshots %v", keys)
	}
}

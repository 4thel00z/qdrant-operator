package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

var backupNow = time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)

type scriptedCron struct {
	previous time.Time
	next     time.Time
	err      error
}

func (c scriptedCron) Previous(time.Time) (time.Time, error) { return c.previous, c.err }

func (c scriptedCron) Next(time.Time) (time.Time, error) { return c.next, c.err }

func backupRecord(name string, daysAgo int, phase domain.BackupPhase) domain.BackupRecord {
	return domain.BackupRecord{Name: name, CreationTime: backupNow.AddDate(0, 0, -daysAgo), Phase: phase}
}

func expiredNames(policy domain.RetentionPolicy, backups []domain.BackupRecord) []string {
	names := []string{}
	for _, backup := range policy.Expired(backups) {
		names = append(names, backup.Name)
	}
	return names
}

func retentionCount(value int) *int { return &value }

func TestRetentionKeepsNewestAndOnePerDailyBucket(t *testing.T) {
	backups := []domain.BackupRecord{}
	for _, name := range []string{"b0", "b1", "b2", "b3", "b4", "b5"} {
		backups = append(backups, backupRecord(name, int(name[1]-'0'), domain.BackupCompleted))
	}
	if got := expiredNames(domain.RetentionPolicy{}, backups); len(got) != 0 {
		t.Errorf("no policy expires nothing: %v", got)
	}
	if got := expiredNames(domain.RetentionPolicy{KeepLast: retentionCount(2)}, backups); !reflect.DeepEqual(got, []string{"b2", "b3", "b4", "b5"}) {
		t.Errorf("keep last: %v", got)
	}
	if got := expiredNames(domain.RetentionPolicy{KeepLast: retentionCount(1), KeepDaily: retentionCount(3)}, backups); !reflect.DeepEqual(got, []string{"b3", "b4", "b5"}) {
		t.Errorf("keep daily: %v", got)
	}
}

func TestRetentionNeverLetsFailedBackupsPushOutCompletedOnes(t *testing.T) {
	backups := []domain.BackupRecord{
		backupRecord("f0", 0, domain.BackupFailed),
		backupRecord("f1", 1, domain.BackupFailed),
		backupRecord("c2", 2, domain.BackupCompleted),
		backupRecord("c3", 3, domain.BackupCompleted),
	}
	if got := expiredNames(domain.RetentionPolicy{KeepLast: retentionCount(2)}, backups); !reflect.DeepEqual(got, []string{"f1"}) {
		t.Errorf("expired: %v", got)
	}
}

func TestDueFollowsCronJobSemantics(t *testing.T) {
	slot := time.Date(2026, 9, 23, 2, 0, 0, 0, time.UTC)
	cron := scriptedCron{previous: slot, next: slot.AddDate(0, 0, 1)}
	spec := domain.DefaultBackupScheduleSpec("demo", "ns", "0 2 * * *", domain.ClusterRef{Name: "db", Namespace: "ns"})

	if got, ok, err := spec.Due(cron, backupNow, time.Time{}); err != nil || !ok || !got.Equal(slot) {
		t.Errorf("never run: %v %v %v", got, ok, err)
	}
	if _, ok, _ := spec.Due(cron, backupNow, slot); ok {
		t.Error("baseline at the slot is up to date")
	}
	if _, ok, _ := spec.Due(cron, backupNow, slot.AddDate(0, 0, -1)); !ok {
		t.Error("baseline before the slot is due")
	}
	deadline := time.Minute
	spec.StartingDeadline = &deadline
	if _, ok, _ := spec.Due(cron, backupNow, time.Time{}); ok {
		t.Error("a slot past its deadline is skipped")
	}
	deadline = 10 * time.Hour
	if _, ok, _ := spec.Due(cron, backupNow, time.Time{}); !ok {
		t.Error("a slot within its deadline is due")
	}
}

func TestSlotErrorsNameTheExpression(t *testing.T) {
	failing := errors.New("bad expression")
	spec := domain.DefaultBackupScheduleSpec("demo", "ns", "nonsense", domain.ClusterRef{})
	_, _, err := spec.Due(scriptedCron{err: failing}, backupNow, time.Time{})
	if !errors.Is(err, failing) {
		t.Fatalf("error should wrap the cron error: %v", err)
	}
	if _, err := spec.NextSlot(scriptedCron{err: failing}, backupNow); !errors.Is(err, failing) {
		t.Errorf("next slot error: %v", err)
	}
}

func TestBackupNameStampsTheSlotInUTC(t *testing.T) {
	spec := domain.DefaultBackupScheduleSpec("demo", "ns", "0 2 * * *", domain.ClusterRef{})
	at := time.Date(2026, 9, 23, 4, 0, 0, 0, time.FixedZone("CEST", 2*3600))
	if got := spec.BackupName(at); got != "demo-20260923-020000" {
		t.Errorf("backup name: %s", got)
	}
}

func TestManifestParseRejectsDocumentsWithoutAName(t *testing.T) {
	if _, err := domain.ParseManifest([]byte(`{"nodeCount": 1}`)); err == nil {
		t.Error("expected an error for a manifest without backupName")
	}
	if _, err := domain.ParseManifest([]byte(`not json`)); err == nil {
		t.Error("expected an error for malformed JSON")
	}
	if _, err := domain.ParseManifest([]byte(`{"backupName": "b", "createdAt": "yesterday"}`)); err == nil {
		t.Error("expected an error for a malformed createdAt")
	}
}

func TestVolumeBackupHasNoS3Storage(t *testing.T) {
	spec := domain.DefaultBackupSpec("nightly", "ns", domain.ClusterRef{Name: "db", Namespace: "ns"})
	spec.Method = domain.BackupMethodVolumeSnapshot
	if _, err := spec.S3Storage(); err == nil {
		t.Error("expected an error without S3 storage")
	}
	location, err := spec.Location()
	if err != nil || location != "VolumeSnapshots labelled qdrant.io/backup=nightly" {
		t.Errorf("location: %q %v", location, err)
	}
}

func TestRestorePercentageRoundsHalfToEven(t *testing.T) {
	cases := []struct {
		total     int
		completed int
		want      int
	}{{0, 0, 0}, {8, 1, 12}, {8, 3, 38}, {8, 5, 62}, {3, 1, 33}, {3, 2, 67}}
	for _, testCase := range cases {
		progress := domain.RestoreProgress{CollectionsTotal: testCase.total, CollectionsCompleted: testCase.completed}
		if got := progress.Percentage(); got != testCase.want {
			t.Errorf("%d of %d: want %d, got %d", testCase.completed, testCase.total, testCase.want, got)
		}
	}
}

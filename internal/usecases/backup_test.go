package usecases_test

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func backupSnapshotNames(world backupWorld) []string {
	names := []string{}
	for key := range world.qdrant.Snapshots {
		names = append(names, key.Name)
	}
	slices.Sort(names)
	return names
}

func backupManifestOf(t *testing.T, world backupWorld, name string) domain.BackupManifest {
	t.Helper()
	raw, ok := world.storage.Objects[fakes.ObjectID{Bucket: "bucket", Key: "backups/" + name + "/manifest.json"}]
	if !ok {
		t.Fatalf("no manifest for %s in %v", name, world.storage.Keys("bucket"))
	}
	manifest, err := domain.ParseManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestBackupStreamsEveryNodeSnapshotAndWritesManifest(t *testing.T) {
	world := newBackupWorld()
	spec := backupSpecOf(t, "nightly", domain.JSON{"retentionDays": 7})

	status := backupRun(t, spec, world)

	if status.Phase != domain.BackupCompleted {
		t.Fatalf("phase %s", status.Phase)
	}
	if status.S3Path != "s3://bucket/backups/nightly" {
		t.Errorf("s3 path %s", status.S3Path)
	}
	if status.CompletionTime.IsZero() {
		t.Error("no completion time")
	}
	if want := status.CompletionTime.Add(7 * 24 * time.Hour); !status.ExpiresAt.Equal(want) {
		t.Errorf("expires at %s, want %s", status.ExpiresAt, want)
	}
	if first := world.kubernetes.StatusPatches[0].Status["phase"]; first != "InProgress" {
		t.Errorf("first patch phase %v", first)
	}
	keys := world.storage.Keys("bucket")
	if !slices.Contains(keys, "backups/nightly/manifest.json") {
		t.Errorf("no manifest in %v", keys)
	}
	snapshots := 0
	node0Docs := ""
	for _, key := range keys {
		if strings.HasSuffix(key, ".snapshot") {
			snapshots++
		}
		if strings.Contains(key, "/docs/node-0/") {
			node0Docs = key
		}
	}
	if snapshots != 4 {
		t.Errorf("%d snapshot objects in %v", snapshots, keys)
	}
	payload := world.storage.Objects[fakes.ObjectID{Bucket: "bucket", Key: node0Docs}]
	if !bytes.Equal(payload, fakes.SnapshotPayload(backupNodeURL(0, "db"), "docs")) {
		t.Errorf("payload of %s differs", node0Docs)
	}
	if len(world.qdrant.Snapshots) != 0 {
		t.Errorf("node snapshots left: %v", backupSnapshotNames(world))
	}
	manifest := backupManifestOf(t, world, "nightly")
	if manifest.NodeCount != 2 || !slices.Equal(manifest.CollectionNames(), []string{"docs", "images"}) {
		t.Errorf("manifest %+v", manifest)
	}
	api := translate.CollectionBackupStatusToAPI(status.Collections[0])
	if api.Snapshots[1].Node != "node-1" {
		t.Errorf("snapshots %+v", api.Snapshots)
	}
}

func TestBackupMarksFailedCollectionAndKeepsTheRest(t *testing.T) {
	world := newBackupWorld()
	world.qdrant.FailingCollections["images"] = true
	spec := backupSpecOf(t, "nightly", domain.JSON{"collections": []string{"docs", "images"}})

	status := backupRun(t, spec, world)

	if status.Phase != domain.BackupFailed {
		t.Fatalf("phase %s", status.Phase)
	}
	if !strings.HasSuffix(status.Error, "snapshot of images refused") {
		t.Errorf("error %q", status.Error)
	}
	phases := []domain.BackupPhase{status.Collections[0].Status, status.Collections[1].Status}
	if !slices.Equal(phases, []domain.BackupPhase{domain.BackupCompleted, domain.BackupFailed}) {
		t.Errorf("collection phases %v", phases)
	}
	if names := backupManifestOf(t, world, "nightly").CollectionNames(); !slices.Equal(names, []string{"docs"}) {
		t.Errorf("manifest collections %v", names)
	}
	condition := status.Conditions[0]
	if condition.Status != domain.ConditionFalse || condition.Reason != "BackupFailed" || condition.Message != "Backed up 1/2 collections" {
		t.Errorf("condition %+v", condition)
	}
}

func backupSeedSnapshots(world backupWorld, started time.Time) {
	world.qdrant.AddSnapshot(backupNodeURL(0, "db"), "docs", "old.snapshot", started.Add(-time.Hour), nil)
	world.qdrant.AddSnapshot(backupNodeURL(0, "db"), "docs", "leftover-0.snapshot", started.Add(5*time.Minute), nil)
	world.qdrant.AddSnapshot(backupNodeURL(1, "db"), "images", "leftover-1.snapshot", started.Add(9*time.Minute), nil)
}

func TestBackupRerunAfterCrashDeletesLeftoversKeepsStartTimeAndCompletes(t *testing.T) {
	world := newBackupWorld()
	started := time.Now().UTC().Add(-30 * time.Minute)
	backupSeedSnapshots(world, started)
	world.storage.Uploads[fakes.ObjectID{Bucket: "bucket", Key: "backups/nightly/docs/node-0/interrupted.snapshot"}] = true
	spec := backupSpecOf(t, "nightly", nil)
	previous := domain.BackupStatus{Phase: domain.BackupInProgress, StartTime: started}

	status, err := world.executeBackup().Execute(context.Background(), spec, backupRef("nightly"), &previous)
	if err != nil {
		t.Fatal(err)
	}

	if len(world.storage.Uploads) != 0 {
		t.Errorf("uploads left: %v", world.storage.Uploads)
	}
	if status.Phase != domain.BackupCompleted || !status.StartTime.Equal(started) || status.InProgressSnapshot != nil {
		t.Errorf("status %+v", status)
	}
	if got := world.kubernetes.StatusPatches[0].Status["startTime"]; got != domain.FormatTime(started) {
		t.Errorf("start time patch %v", got)
	}
	if names := backupSnapshotNames(world); !slices.Equal(names, []string{"old.snapshot"}) {
		t.Errorf("snapshots %v", names)
	}
	patches := world.kubernetes.StatusPatches
	marker, present := patches[len(patches)-1].Status["inProgressSnapshot"]
	if !present || marker != nil {
		t.Errorf("last patch %v", patches[len(patches)-1].Status)
	}
}

func TestBackupRecordsInProgressSnapshotWhileStreaming(t *testing.T) {
	world := newBackupWorld()
	spec := backupSpecOf(t, "nightly", domain.JSON{"collections": []string{"docs"}})

	backupRun(t, spec, world)

	markers := []any{}
	for _, patch := range world.kubernetes.StatusPatches[1:] {
		if marker, ok := patch.Status["inProgressSnapshot"]; ok {
			markers = append(markers, marker)
		}
	}
	want := domain.JSON{"nodeIndex": 0, "collection": "docs", "snapshot": "docs-1.snapshot"}
	if !reflect.DeepEqual(markers[0], want) {
		t.Errorf("first marker %v, want %v", markers[0], want)
	}
	if markers[len(markers)-1] != nil {
		t.Errorf("last marker %v", markers[len(markers)-1])
	}
}

func TestBackupRerunDeletesTheRecordedSnapshotEvenIfListedBeforeStart(t *testing.T) {
	world := newBackupWorld()
	started := time.Now().UTC()
	world.qdrant.AddSnapshot(backupNodeURL(1, "db"), "docs", "marked.snapshot", started.Add(-time.Minute), nil)
	previous := domain.BackupStatus{
		Phase:              domain.BackupInProgress,
		StartTime:          started,
		InProgressSnapshot: &domain.InProgressSnapshot{NodeIndex: 1, Collection: "docs", Snapshot: "marked.snapshot"},
	}
	spec := backupSpecOf(t, "nightly", domain.JSON{"collections": []string{"docs"}})

	if _, err := world.executeBackup().Execute(context.Background(), spec, backupRef("nightly"), &previous); err != nil {
		t.Fatal(err)
	}

	if len(world.qdrant.Snapshots) != 0 {
		t.Errorf("snapshots left: %v", backupSnapshotNames(world))
	}
}

func TestCleanupOfIncompleteBackupDeletesOnlySnapshotsSinceStart(t *testing.T) {
	world := newBackupWorld()
	started := time.Now().UTC().Add(-30 * time.Minute)
	backupSeedSnapshots(world, started)
	cleanup := usecases.CleanupBackupSnapshots{Qdrant: world.qdrant, Kubernetes: world.kubernetes}

	deleted, err := cleanup.Execute(context.Background(), backupSpecOf(t, "nightly", nil), domain.BackupStatus{Phase: domain.BackupInProgress, StartTime: started})
	if err != nil {
		t.Fatal(err)
	}

	if deleted != 2 {
		t.Errorf("deleted %d", deleted)
	}
	if names := backupSnapshotNames(world); !slices.Equal(names, []string{"old.snapshot"}) {
		t.Errorf("snapshots %v", names)
	}
}

func TestCleanupSkipsCompletedAndUnstartedBackups(t *testing.T) {
	started := time.Now().UTC().Add(-30 * time.Minute)
	for name, status := range map[string]domain.BackupStatus{
		"completed": {Phase: domain.BackupCompleted, StartTime: started},
		"unstarted": {Phase: domain.BackupPending},
	} {
		t.Run(name, func(t *testing.T) {
			world := newBackupWorld()
			backupSeedSnapshots(world, started)
			cleanup := usecases.CleanupBackupSnapshots{Qdrant: world.qdrant, Kubernetes: world.kubernetes}

			deleted, err := cleanup.Execute(context.Background(), backupSpecOf(t, "nightly", nil), status)
			if err != nil {
				t.Fatal(err)
			}

			if deleted != 0 || len(world.qdrant.Snapshots) != 3 {
				t.Errorf("deleted %d, %d snapshots left", deleted, len(world.qdrant.Snapshots))
			}
		})
	}
}

func TestDeleteBackupDataRemovesOnlyThatBackupPrefix(t *testing.T) {
	world := newBackupWorld()
	for _, key := range []string{"backups/nightly/manifest.json", "backups/nightly/docs/node-0/a.snapshot", "backups/nightly-2/manifest.json"} {
		world.storage.Objects[fakes.ObjectID{Bucket: "bucket", Key: key}] = []byte("x")
	}
	world.storage.Uploads[fakes.ObjectID{Bucket: "bucket", Key: "backups/nightly/docs/node-1/b.snapshot"}] = true
	other := fakes.ObjectID{Bucket: "bucket", Key: "backups/nightly-2/docs/node-0/c.snapshot"}
	world.storage.Uploads[other] = true

	deleted, err := usecases.DeleteBackupData{Storage: world.storage, Kubernetes: world.kubernetes}.Execute(context.Background(), backupSpecOf(t, "nightly", nil))
	if err != nil {
		t.Fatal(err)
	}

	if deleted != 2 {
		t.Errorf("deleted %d", deleted)
	}
	if keys := world.storage.Keys("bucket"); !slices.Equal(keys, []string{"backups/nightly-2/manifest.json"}) {
		t.Errorf("keys %v", keys)
	}
	if len(world.storage.Uploads) != 1 || !world.storage.Uploads[other] {
		t.Errorf("uploads %v", world.storage.Uploads)
	}
}

func TestExpireBackupDeletesResourceOncePastExpiry(t *testing.T) {
	world := newBackupWorld()
	ref := backupRef("nightly")
	world.kubernetes.PutResource(domain.JSON{"kind": "QdrantBackup", "metadata": domain.JSON{"name": "nightly", "namespace": backupNamespace}})
	status := domain.BackupStatus{Phase: domain.BackupCompleted, ExpiresAt: backupTestNow.Add(time.Hour)}
	expire := usecases.ExpireBackup{Kubernetes: world.kubernetes}

	early, err := expire.Execute(context.Background(), ref, status, backupTestNow)
	if err != nil || early {
		t.Fatalf("expired early: %v, %v", early, err)
	}
	late, err := expire.Execute(context.Background(), ref, status, backupTestNow.Add(2*time.Hour))
	if err != nil || !late {
		t.Fatalf("not expired: %v, %v", late, err)
	}
	if !slices.Equal(world.kubernetes.Deleted, []domain.ResourceRef{ref}) {
		t.Errorf("deleted %v", world.kubernetes.Deleted)
	}
}

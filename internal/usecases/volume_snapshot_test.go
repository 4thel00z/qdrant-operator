package usecases_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const (
	volumeSnapshotClass = "csi-snapshots"
	cinderZoneLabel     = "topology.cinder.csi.openstack.org/zone"
)

func volumeBackupSpecOf(t *testing.T, name string) domain.BackupSpec {
	t.Helper()
	spec := backupSpecOf(t, name, domain.JSON{"method": "volumeSnapshot", "volumeSnapshotClassName": volumeSnapshotClass})
	spec.Storage = nil
	return spec
}

func (w backupWorld) seedDataClaims(replicas int) {
	w.kubernetes.VolumeClaims[backupNamespace] = map[string]string{}
	for index := range replicas {
		name := fmt.Sprintf("qdrant-storage-qdrant-db-%d", index)
		w.kubernetes.VolumeClaims[backupNamespace][name] = "20Gi"
		if _, present := w.kubernetes.ClaimDetails[name]; present {
			continue
		}
		claim := domain.DefaultVolumeClaim(name, "20Gi")
		claim.StorageClass = "cinder-csi"
		w.kubernetes.ClaimDetails[name] = claim
	}
}

func (w backupWorld) snapshotKeys() []string {
	return slices.Sorted(maps.Keys(w.kubernetes.VolumeSnapshots))
}

func TestVolumeSnapshotBackupSnapshotsEveryDataClaimAndRecordsThem(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(2)
	spec := volumeBackupSpecOf(t, "nightly")

	status := backupRun(t, spec, world)

	if status.Phase != domain.BackupCompleted {
		t.Fatalf("status %+v", status)
	}
	if keys := world.snapshotKeys(); !slices.Equal(keys, []string{"tenant-a/nightly-node-0", "tenant-a/nightly-node-1"}) {
		t.Fatalf("snapshots %v", keys)
	}
	body := world.kubernetes.VolumeSnapshots["tenant-a/nightly-node-1"]
	if labels := body["metadata"].(domain.JSON)["labels"]; !reflect.DeepEqual(labels, domain.JSON{"qdrant.io/backup": "nightly"}) {
		t.Errorf("labels %v", labels)
	}
	snapshotSpec := body["spec"].(domain.JSON)
	if snapshotSpec["volumeSnapshotClassName"] != volumeSnapshotClass {
		t.Errorf("class %v", snapshotSpec["volumeSnapshotClassName"])
	}
	if want := (domain.JSON{"persistentVolumeClaimName": "qdrant-storage-qdrant-db-1"}); !reflect.DeepEqual(snapshotSpec["source"], want) {
		t.Errorf("source %v", snapshotSpec["source"])
	}
	records := []string{}
	for _, record := range status.VolumeSnapshots {
		records = append(records, strings.Join([]string{record.ClaimName, record.SnapshotName, record.Size, record.StorageClass}, ","))
	}
	want := []string{
		"qdrant-storage-qdrant-db-0,nightly-node-0,20Gi,cinder-csi",
		"qdrant-storage-qdrant-db-1,nightly-node-1,20Gi,cinder-csi",
	}
	if !slices.Equal(records, want) {
		t.Errorf("records %v", records)
	}
	if status.TotalSize != "40.0GB" {
		t.Errorf("total size %s", status.TotalSize)
	}
	roundTrip, err := translate.BackupStatusFromAPI(translate.BackupStatusToAPI(status))
	if err != nil || !reflect.DeepEqual(roundTrip.VolumeSnapshots, status.VolumeSnapshots) {
		t.Errorf("round trip %+v, %v", roundTrip.VolumeSnapshots, err)
	}
	if len(world.qdrant.Snapshots) != 0 {
		t.Errorf("node snapshots taken: %v", backupSnapshotNames(world))
	}
}

func TestVolumeSnapshotBackupWaitsUntilReadyToUse(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(2)
	world.kubernetes.SnapshotReady = false
	spec := volumeBackupSpecOf(t, "nightly")
	backup := usecases.NewExecuteVolumeSnapshotBackup(world.kubernetes, backupTestClock)
	backup.ReadyTimeout = 0
	backup.PollInterval = 0

	status, err := backup.Execute(context.Background(), spec, backupRef("nightly"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.BackupFailed || !strings.Contains(status.Error, "not readyToUse") {
		t.Errorf("status %+v", status)
	}
	if len(world.kubernetes.VolumeSnapshots) != 0 {
		t.Errorf("snapshots left: %v", world.snapshotKeys())
	}
}

func TestVolumeSnapshotBackupFailsAndCleansUpOnASnapshotError(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(2)
	world.kubernetes.SnapshotErrors["nightly-node-1"] = "driver refused"
	spec := volumeBackupSpecOf(t, "nightly")
	backup := usecases.NewExecuteVolumeSnapshotBackup(world.kubernetes, backupTestClock)
	backup.PollInterval = 0

	status, err := backup.Execute(context.Background(), spec, backupRef("nightly"), nil)
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.BackupFailed || status.Error != "VolumeSnapshot nightly-node-1 failed: driver refused" {
		t.Errorf("status %+v", status)
	}
	if len(world.kubernetes.VolumeSnapshots) != 0 {
		t.Errorf("snapshots left: %v", world.snapshotKeys())
	}
}

func TestVolumeSnapshotBackupNeedsAClaimPerNode(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(1)
	backup := usecases.NewExecuteVolumeSnapshotBackup(world.kubernetes, backupTestClock)
	backup.PollInterval = 0

	_, err := backup.Execute(context.Background(), volumeBackupSpecOf(t, "nightly"), backupRef("nightly"), nil)

	if err == nil || !strings.Contains(err.Error(), "qdrant-storage-qdrant-db-1") {
		t.Fatalf("want a missing claim error, got %v", err)
	}
}

func TestDeletingAVolumeSnapshotBackupDeletesOnlyItsSnapshots(t *testing.T) {
	world := newBackupWorld()
	world.seedDataClaims(2)
	for _, name := range []string{"nightly", "weekly"} {
		backupRun(t, volumeBackupSpecOf(t, name), world)
	}

	deleted, err := usecases.DeleteBackupData{Storage: world.storage, Kubernetes: world.kubernetes}.Execute(context.Background(), volumeBackupSpecOf(t, "nightly"))
	if err != nil {
		t.Fatal(err)
	}
	cleanup := usecases.CleanupBackupSnapshots{Qdrant: world.qdrant, Kubernetes: world.kubernetes}
	leftovers, err := cleanup.Execute(context.Background(), volumeBackupSpecOf(t, "nightly"), domain.BackupStatus{Phase: domain.BackupInProgress, StartTime: backupTestNow})
	if err != nil {
		t.Fatal(err)
	}

	if deleted != 2 || leftovers != 0 {
		t.Errorf("deleted %d, leftovers %d", deleted, leftovers)
	}
	if keys := world.snapshotKeys(); !slices.Equal(keys, []string{"tenant-a/weekly-node-0", "tenant-a/weekly-node-1"}) {
		t.Errorf("snapshots %v", keys)
	}
}

func (w backupWorld) completedVolumeBackup(t *testing.T) {
	t.Helper()
	w.seedDataClaims(2)
	status := backupRun(t, volumeBackupSpecOf(t, "nightly"), w)
	w.kubernetes.PutResource(backupResource(
		"nightly",
		domain.JSON{"clusterRef": domain.JSON{"name": "db"}, "method": "volumeSnapshot", "volumeSnapshotClassName": volumeSnapshotClass},
		backupStatusBody(t, status),
	))
	w.kubernetes.VolumeClaims[backupNamespace] = map[string]string{}
}

func volumeRestoreSpecOf(t *testing.T, target string, extra domain.JSON) domain.RestoreSpec {
	t.Helper()
	spec := domain.JSON{"targetClusterRef": domain.JSON{"name": target}, "backupRef": domain.JSON{"name": "nightly"}}
	for key, value := range extra {
		spec[key] = value
	}
	return restoreSpecOf(t, spec)
}

func (w backupWorld) zonedClaims() {
	for index, zone := range []string{"eu01-2", "eu01-3"} {
		name := fmt.Sprintf("qdrant-storage-qdrant-db-%d", index)
		volume := fmt.Sprintf("pv-%d", index)
		claim := domain.DefaultVolumeClaim(name, "20Gi")
		claim.StorageClass = "cinder-csi"
		claim.VolumeName = volume
		w.kubernetes.ClaimDetails[name] = claim
		w.kubernetes.VolumeZones[volume] = domain.Zone{Label: cinderZoneLabel, Name: zone}
	}
}

func TestVolumeSnapshotRestorePrecreatesClaimsFromTheSnapshots(t *testing.T) {
	world := newBackupWorld()
	world.completedVolumeBackup(t)

	status, err := world.executeRestore().Execute(context.Background(), volumeRestoreSpecOf(t, "db", nil), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreCompleted || status.SourceBackup != "nightly" {
		t.Fatalf("status %+v", status)
	}
	created := slices.Sorted(maps.Keys(world.kubernetes.CreatedClaims))
	if !slices.Equal(created, []string{"tenant-a/qdrant-storage-qdrant-db-0", "tenant-a/qdrant-storage-qdrant-db-1"}) {
		t.Fatalf("claims %v", created)
	}
	claim := world.kubernetes.CreatedClaims["tenant-a/qdrant-storage-qdrant-db-1"]
	spec := claim["spec"].(domain.JSON)
	wantSource := domain.JSON{"apiGroup": "snapshot.storage.k8s.io", "kind": "VolumeSnapshot", "name": "nightly-node-1"}
	if !reflect.DeepEqual(spec["dataSource"], wantSource) {
		t.Errorf("dataSource %v", spec["dataSource"])
	}
	if spec["storageClassName"] != "cinder-csi" {
		t.Errorf("storage class %v", spec["storageClassName"])
	}
	if want := (domain.JSON{"requests": domain.JSON{"storage": "20Gi"}}); !reflect.DeepEqual(spec["resources"], want) {
		t.Errorf("resources %v", spec["resources"])
	}
	labels := claim["metadata"].(domain.JSON)["labels"].(domain.JSON)
	if labels["app.kubernetes.io/instance"] != "qdrant-db" {
		t.Errorf("labels %v", labels)
	}
}

func TestVolumeSnapshotRestoreProvisionsEachClaimInItsSnapshotZone(t *testing.T) {
	world := newBackupWorld()
	world.zonedClaims()
	world.completedVolumeBackup(t)
	world.kubernetes.ZoneNodes = map[domain.Zone]string{
		{Label: cinderZoneLabel, Name: "eu01-2"}: "node-z2",
		{Label: cinderZoneLabel, Name: "eu01-3"}: "node-z3",
	}

	status, err := world.executeRestore().Execute(context.Background(), volumeRestoreSpecOf(t, "db", nil), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreCompleted {
		t.Fatalf("status %+v", status)
	}
	for index, node := range []string{"node-z2", "node-z3"} {
		claim := world.kubernetes.CreatedClaims[fmt.Sprintf("tenant-a/qdrant-storage-qdrant-db-%d", index)]
		annotations := claim["metadata"].(domain.JSON)["annotations"]
		if want := (domain.JSON{domain.SelectedNodeAnnotation: node}); !reflect.DeepEqual(annotations, want) {
			t.Errorf("claim %d annotations %v", index, annotations)
		}
	}
}

func TestVolumeSnapshotRestoreFailsWithoutANodeInTheSnapshotZone(t *testing.T) {
	world := newBackupWorld()
	world.zonedClaims()
	world.completedVolumeBackup(t)
	world.kubernetes.ZoneNodes = map[domain.Zone]string{{Label: cinderZoneLabel, Name: "eu01-2"}: "node-z2"}

	status, err := world.executeRestore().Execute(context.Background(), volumeRestoreSpecOf(t, "db", nil), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreFailed || !strings.Contains(status.Error, "eu01-3") {
		t.Errorf("status %+v", status)
	}
	if len(world.kubernetes.CreatedClaims) != 0 {
		t.Errorf("claims created: %v", world.kubernetes.CreatedClaims)
	}
}

func TestVolumeSnapshotRestoreReportsTheBrokenConstraintAsACondition(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		extra   domain.JSON
		message string
	}{
		{name: "renamed target", target: "other", message: "must be named db"},
		{name: "collection selection", target: "db", extra: domain.JSON{"collections": []string{"docs"}}, message: "restore whole volumes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			world := newBackupWorld()
			world.completedVolumeBackup(t)
			world.kubernetes.PutResource(backupClusterBody(test.target, 2, nil))

			status, err := world.executeRestore().Execute(context.Background(), volumeRestoreSpecOf(t, test.target, test.extra), restoreRef())
			if err != nil {
				t.Fatal(err)
			}

			if status.Phase != domain.RestoreFailed || !strings.Contains(status.Error, test.message) {
				t.Errorf("status %+v", status)
			}
			if len(status.Conditions) == 0 || status.Conditions[0].Status != domain.ConditionFalse || status.Conditions[0].Reason != "IncompatibleTarget" {
				t.Errorf("conditions %+v", status.Conditions)
			}
			if len(world.kubernetes.CreatedClaims) != 0 {
				t.Errorf("claims created: %v", world.kubernetes.CreatedClaims)
			}
		})
	}
}

func TestVolumeSnapshotRestoreRefusesADeployedOrResizedTarget(t *testing.T) {
	world := newBackupWorld()
	world.completedVolumeBackup(t)
	restore := world.executeRestore()

	world.kubernetes.PutResource(backupClusterBody("db", 3, nil))
	resized, err := restore.Execute(context.Background(), volumeRestoreSpecOf(t, "db", nil), restoreRef())
	if err != nil {
		t.Fatal(err)
	}
	world.kubernetes.PutResource(backupClusterBody("db", 2, nil))
	world.kubernetes.StatefulSets["tenant-a/qdrant-db"] = domain.StatefulSetStatus{}
	deployed, err := restore.Execute(context.Background(), volumeRestoreSpecOf(t, "db", nil), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(resized.Error, "3 replicas") {
		t.Errorf("resized: %q", resized.Error)
	}
	if !strings.Contains(deployed.Error, "already deployed") {
		t.Errorf("deployed: %q", deployed.Error)
	}
	if len(world.kubernetes.CreatedClaims) != 0 {
		t.Errorf("claims created: %v", world.kubernetes.CreatedClaims)
	}
}

func TestRestoreOfAMissingBackupFailsWithoutRetry(t *testing.T) {
	world := newBackupWorld()
	spec := volumeRestoreSpecOf(t, "db", nil)

	_, err := world.executeRestore().Execute(context.Background(), spec, restoreRef())

	if err == nil || errors.Is(err, usecases.ErrSourceNotReady) {
		t.Fatalf("want a not found error, got %v", err)
	}
}

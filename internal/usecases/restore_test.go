package usecases_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

func restoreRef() domain.ResourceRef {
	return domain.ResourceRef{Kind: domain.Restores, Name: "rs", Namespace: backupNamespace}
}

func restoreSpecOf(t *testing.T, spec domain.JSON) domain.RestoreSpec {
	t.Helper()
	api, err := usecases.DecodeBody[qdrantv1alpha1.QdrantRestoreSpec](spec)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := translate.RestoreSpecFromAPI("rs", backupNamespace, api)
	if err != nil {
		t.Fatal(err)
	}
	return restore
}

func (w backupWorld) completedBackup(t *testing.T) {
	t.Helper()
	spec := backupSpecOf(t, "nightly", nil)
	status := backupRun(t, spec, w)
	w.kubernetes.PutResource(backupResource(
		"nightly",
		domain.JSON{"clusterRef": domain.JSON{"name": "db"}, "storage": domain.JSON{"s3": backupS3()}},
		backupStatusBody(t, status),
	))
}

func restoreIndexing(t *testing.T, timeout time.Duration) domain.RestoreSpec {
	t.Helper()
	spec := restoreSpecOf(t, domain.JSON{"targetClusterRef": domain.JSON{"name": "db"}, "backupRef": domain.JSON{"name": "nightly"}})
	spec.IndexingTimeout = timeout
	return spec
}

func TestRestoreFromBackupRefRecoversEachNodeFromPresignedURLs(t *testing.T) {
	world := newBackupWorld()
	world.completedBackup(t)
	world.kubernetes.PutResource(backupClusterBody("target", 2, nil))
	world.qdrant.Routes[backupServiceURL("target")] = backupNodeURL(0, "target")
	spec := restoreSpecOf(t, domain.JSON{
		"targetClusterRef":  domain.JSON{"name": "target"},
		"backupRef":         domain.JSON{"name": "nightly"},
		"collections":       []string{"docs"},
		"collectionMapping": domain.JSON{"docs": "docs-restored"},
		"priority":          "replica",
	})

	status, err := world.executeRestore().Execute(context.Background(), spec, restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreCompleted || status.SourceBackup != "nightly" || status.Progress.Percentage() != 100 {
		t.Fatalf("status %+v", status)
	}
	restored := status.RestoredCollections[0]
	if restored.Name != "docs-restored" || restored.OriginalName != "docs" || restored.PointsCount != 42 {
		t.Errorf("restored %+v", restored)
	}
	recovered := world.qdrant.Recovered
	if len(recovered) != 2 {
		t.Fatalf("recovered %+v", recovered)
	}
	urls := []string{recovered[0].NodeURL, recovered[1].NodeURL}
	slices.Sort(urls)
	if !slices.Equal(urls, []string{backupNodeURL(0, "target"), backupNodeURL(1, "target")}) {
		t.Errorf("nodes %v", urls)
	}
	for _, recovery := range recovered {
		if recovery.Collection != "docs-restored" || recovery.Priority != domain.PriorityReplica || recovery.Checksum == "" {
			t.Errorf("recovery %+v", recovery)
		}
		if !strings.Contains(recovery.Location, "/docs/node-") || !strings.HasPrefix(recovery.Location, "https://presigned.test/bucket/") {
			t.Errorf("location %s", recovery.Location)
		}
	}
	phases := []any{}
	for _, patch := range world.kubernetes.StatusPatches {
		if patch.Ref.Kind == domain.Restores {
			phases = append(phases, patch.Status["phase"])
		}
	}
	if !slices.Equal(phases, []any{"Downloading", "Restoring"}) {
		t.Errorf("phases %v", phases)
	}
}

func TestRestoreGivesUpOnACollectionThatNeverTurnsGreen(t *testing.T) {
	world := newBackupWorld()
	world.completedBackup(t)
	world.qdrant.RestoreBodies["docs"] = domain.JSON{
		"vectors":           domain.JSON{"size": 4, "distance": "Cosine"},
		"optimizers_config": domain.JSON{"indexing_threshold": 0},
	}
	world.qdrant.YellowPolls["docs"] = 1_000_000_000
	callsBefore := world.qdrant.InfoCalls
	restore := world.executeRestore()
	restore.IndexingPollInterval = 5 * time.Millisecond

	status, err := restore.Execute(context.Background(), restoreIndexing(t, 50*time.Millisecond), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	outcomes := map[string]domain.RestoredCollection{}
	for _, outcome := range status.RestoredCollections {
		outcomes[outcome.Name] = outcome
	}
	if world.qdrant.InfoCalls-callsBefore <= 2 {
		t.Errorf("only %d info calls", world.qdrant.InfoCalls-callsBefore)
	}
	wantError := "Collection docs did not turn green within 0.05s (status yellow); " +
		"indexing is disabled in the restored config (optimizer_config.indexing_threshold is 0)"
	if outcomes["docs"].Status != domain.RestoreFailed || outcomes["docs"].Error != wantError {
		t.Errorf("docs %+v", outcomes["docs"])
	}
	if outcomes["images"].Status != domain.RestoreCompleted {
		t.Errorf("images %+v", outcomes["images"])
	}
	if status.Phase != domain.RestoreFailed || status.Error != wantError || status.Progress.Percentage() != 50 || status.CompletionTime.IsZero() {
		t.Errorf("status %+v", status)
	}
	complete := domain.FindCondition(status.Conditions, "Complete")
	if complete == nil || complete.Status != domain.ConditionFalse || complete.Reason != "RestoreFailed" || complete.Message != "Restored 1/2" {
		t.Errorf("condition %+v", complete)
	}
}

func TestRestoreWaitsForIndexingThatFinishesBeforeTheTimeout(t *testing.T) {
	world := newBackupWorld()
	world.completedBackup(t)
	world.qdrant.YellowPolls["docs"] = 3
	world.qdrant.YellowPolls["images"] = 2
	callsBefore := world.qdrant.InfoCalls

	status, err := world.executeRestore().Execute(context.Background(), restoreIndexing(t, time.Minute), restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreCompleted || status.Error != "" {
		t.Errorf("status %+v", status)
	}
	if calls := world.qdrant.InfoCalls - callsBefore; calls != 7 {
		t.Errorf("%d info calls, want 7", calls)
	}
	for _, restored := range status.RestoredCollections {
		if restored.PointsCount != 42 {
			t.Errorf("restored %+v", restored)
		}
	}
}

func TestRestoreRefusesUnfinishedBackupAndMismatchedNodeCount(t *testing.T) {
	world := newBackupWorld()
	world.completedBackup(t)
	restore := world.executeRestore()
	world.kubernetes.PutResource(backupClusterBody("small", 1, nil))
	spec := restoreSpecOf(t, domain.JSON{"targetClusterRef": domain.JSON{"name": "small"}, "backupRef": domain.JSON{"name": "nightly"}})

	_, err := restore.Execute(context.Background(), spec, restoreRef())
	if err == nil || !strings.Contains(err.Error(), "node counts must match") {
		t.Fatalf("want a node count error, got %v", err)
	}

	status := world.kubernetes.Resources["qdrantbackups/"+backupNamespace+"/nightly"]["status"].(domain.JSON)
	status["phase"] = "InProgress"
	_, err = restore.Execute(context.Background(), spec, restoreRef())
	if !errors.Is(err, usecases.ErrSourceNotReady) || !usecases.Retryable(err) {
		t.Fatalf("want ErrSourceNotReady, got %v", err)
	}
}

func TestRestoreFromDirectS3Path(t *testing.T) {
	world := newBackupWorld()
	world.completedBackup(t)
	source := backupS3()
	source["path"] = "backups/nightly"
	spec := restoreSpecOf(t, domain.JSON{
		"targetClusterRef": domain.JSON{"name": "db"},
		"source":           domain.JSON{"s3": source},
		"waitForIndexing":  false,
	})

	status, err := world.executeRestore().Execute(context.Background(), spec, restoreRef())
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.RestoreCompleted || status.SourceBackup != "s3://bucket/backups/nightly" {
		t.Fatalf("status %+v", status)
	}
	names := []string{}
	for _, restored := range status.RestoredCollections {
		names = append(names, restored.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"docs", "images"}) {
		t.Errorf("restored %v", names)
	}
}

func TestRestoreIndexingTimeoutReportsStatusAndWhatIsDisabled(t *testing.T) {
	tests := []struct {
		name       string
		optimizers domain.JSON
		check      func(message string) bool
	}{
		{
			name:       "indexing disabled",
			optimizers: domain.JSON{"indexing_threshold": 0, "max_optimization_threads": 0},
			check: func(message string) bool {
				return strings.Contains(message, "did not turn green within 0s (status yellow)") &&
					strings.Contains(message, "indexing is disabled in the restored config") &&
					strings.Contains(message, "optimizer_config.indexing_threshold") &&
					strings.Contains(message, "optimizer_config.max_optimization_threads")
			},
		},
		{
			name:       "indexing enabled",
			optimizers: domain.JSON{"indexing_threshold": 5000},
			check: func(message string) bool {
				return message == "Collection docs did not turn green within 0s (status yellow)"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			world := newBackupWorld()
			body := domain.JSON{"vectors": domain.JSON{"size": 4, "distance": "Cosine"}, "optimizers_config": test.optimizers}
			world.qdrant.AddCollection(backupNodeURL(0, "db"), "docs", 0, body)
			world.qdrant.Collections[backupNodeURL(0, "db")]["docs"]["status"] = "yellow"
			node := domain.QdrantNode{URL: backupNodeURL(0, "db")}

			_, err := world.executeRestore().AwaitIndexing(context.Background(), restoreIndexing(t, 0), node, "docs")

			if err == nil || !test.check(err.Error()) {
				t.Fatalf("message %v", err)
			}
		})
	}
}

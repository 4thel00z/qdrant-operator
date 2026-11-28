package usecases_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func migrationSyncing(f *collectionFixture, updates [][]domain.Point, deletes [][]uint64) {
	f.routeTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, migrationBody())
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(7))
	key := fakes.PointsKey{NodeURL: migrationSource(), Collection: "docs"}
	f.qdrant.LatePoints[key] = updates
	removals := [][]domain.PointID{}
	for _, ids := range deletes {
		wave := []domain.PointID{}
		for _, id := range ids {
			wave = append(wave, domain.NumberPointID(id))
		}
		removals = append(removals, wave)
	}
	f.qdrant.LateDeletes[key] = removals
}

func migrationIDList(ids ...uint64) []domain.PointID {
	list := []domain.PointID{}
	for _, id := range ids {
		list = append(list, domain.NumberPointID(id))
	}
	return list
}

func TestMigrationSyncUpdatesAPointChangedOnTheSourceAfterTheCopy(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(3, `{"n":33}`, "")}}, nil)

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true}), migrationFresh)

	docs := status.Collections[0]
	if status.Phase != domain.MigrationCompleted {
		t.Errorf("phase: %s (%s)", status.Phase, status.Error)
	}
	if got := string(f.stored(migrationTarget(), "docs", 3).Payload); got != `{"n":33}` {
		t.Errorf("payload: %s", got)
	}
	if last := f.qdrant.Upserts[len(f.qdrant.Upserts)-1]; last != migrationUpsert("docs", domain.PointShardKey{}, 1) {
		t.Errorf("last upsert: %+v", last)
	}
	if docs.PointsUpdated != 1 || docs.PointsDeleted != 0 || docs.SyncPass != 1 {
		t.Errorf("sync counters: %+v", docs)
	}
	if *docs.SourceCount != 7 || *docs.TargetCount != 7 {
		t.Errorf("counts: %d %d", *docs.SourceCount, *docs.TargetCount)
	}
}

func TestMigrationSyncUpdatesAPointWhoseVectorChanged(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(2, "", "[9.0,9.0]")}}, nil)

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true}), migrationFresh)

	if status.Collections[0].PointsUpdated != 1 {
		t.Errorf("points updated: %d", status.Collections[0].PointsUpdated)
	}
	if got := string(f.stored(migrationTarget(), "docs", 2).Vector); got != "[9.0,9.0]" {
		t.Errorf("vector: %s", got)
	}
}

func TestMigrationSyncDeletesTargetPointsRemovedFromTheSource(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, nil, [][]uint64{{5, 7}})

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true}), migrationFresh)

	docs := status.Collections[0]
	if status.Phase != domain.MigrationCompleted {
		t.Errorf("phase: %s", status.Phase)
	}
	if got := f.storedIDs(migrationTarget(), "docs"); !reflect.DeepEqual(got, []string{"1", "2", "3", "4", "6"}) {
		t.Errorf("target ids: %v", got)
	}
	if want := []fakes.Deletion{{Collection: "docs", IDs: migrationIDList(5, 7)}}; !reflect.DeepEqual(f.qdrant.Deletions, want) {
		t.Errorf("deletions: %v", f.qdrant.Deletions)
	}
	if docs.PointsUpdated != 0 || docs.PointsDeleted != 2 || *docs.SourceCount != 5 || *docs.TargetCount != 5 {
		t.Errorf("docs: %+v", docs)
	}
}

func TestMigrationSyncDoesNotRewriteIdenticalPoints(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, nil, nil)

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true}), migrationFresh)

	docs := status.Collections[0]
	if got := migrationUpserts(f, "docs"); !reflect.DeepEqual(got, []int{3, 3, 1}) {
		t.Errorf("upserts: %v", got)
	}
	if len(f.qdrant.Deletions) != 0 {
		t.Errorf("deletions: %v", f.qdrant.Deletions)
	}
	if docs.PointsUpdated != 0 || docs.PointsDeleted != 0 || docs.SyncPass != 1 {
		t.Errorf("docs: %+v", docs)
	}
}

func TestMigrationSyncFollowsTheCollectionMapping(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(4, `{"n":44}`, "")}}, [][]uint64{{1}})

	status := f.run(t, migrationSpec(t, domain.JSON{
		"batchSize":         3,
		"sync":              true,
		"collectionMapping": domain.JSON{"docs": "docs_v2"},
	}), migrationFresh)

	docs := status.Collections[0]
	if docs.Name != "docs" || docs.TargetName != "docs_v2" || docs.PointsUpdated != 1 || docs.PointsDeleted != 1 {
		t.Errorf("docs: %+v", docs)
	}
	if got := f.storedIDs(migrationTarget(), "docs_v2"); !reflect.DeepEqual(got, []string{"2", "3", "4", "5", "6", "7"}) {
		t.Errorf("target ids: %v", got)
	}
	if got := string(f.stored(migrationTarget(), "docs_v2", 4).Payload); got != `{"n":44}` {
		t.Errorf("payload: %s", got)
	}
	if want := []fakes.Deletion{{Collection: "docs_v2", IDs: migrationIDList(1)}}; !reflect.DeepEqual(f.qdrant.Deletions, want) {
		t.Errorf("deletions: %v", f.qdrant.Deletions)
	}
}

func TestMigrationSyncRepeatsUntilAPassChangesNothing(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(3, `{"n":33}`, "")}}, nil)

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true, "syncMaxPasses": 5}), migrationFresh)

	docs := status.Collections[0]
	if docs.SyncPass != 2 || docs.PointsUpdated != 1 || docs.SyncPassChanges != 0 || docs.Message != "" {
		t.Errorf("docs: %+v", docs)
	}
}

func TestMigrationSyncStopsAtThePassCap(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(3, `{"n":33}`, "")}, {migrationChanged(2, `{"n":22}`, "")}}, [][]uint64{{6}})

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3, "sync": true, "syncMaxPasses": 2}), migrationFresh)

	docs := status.Collections[0]
	if docs.SyncPass != 2 || docs.PointsUpdated != 2 || docs.PointsDeleted != 1 {
		t.Errorf("docs: %+v", docs)
	}
	if docs.Message != "sync pass 2 still changed 1 points" || status.Phase != domain.MigrationCompleted {
		t.Errorf("message %q, phase %s", docs.Message, status.Phase)
	}
}

func TestMigrationSyncRestartResumesThePassFromItsCheckpoint(t *testing.T) {
	f := newCollectionFixture()
	migrationSyncing(f, [][]domain.Point{{migrationChanged(2, "", "[7.0,7.0]"), migrationChanged(6, "", "[8.0,8.0]")}}, nil)
	four := 4
	f.qdrant.UnreachableAfterUpserts = &four
	use := f.migrate()
	spec := migrationSpec(t, domain.JSON{"batchSize": 3, "checkpointBatches": 1, "sync": true})

	_, err := use.Execute(context.Background(), spec, migrationRef(), migrationFresh)
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}

	saved := migrationSaved(t, f)
	checkpoint := saved.Collections[0]
	if checkpoint.SyncPass != 1 || checkpoint.SyncOffset != domain.NumberPointID(4) || checkpoint.PointsUpdated != 1 {
		t.Errorf("checkpoint: %+v", checkpoint)
	}
	f.qdrant.UnreachableAfterUpserts = nil
	resumed, err := use.Execute(context.Background(), spec, migrationRef(), saved)
	if err != nil {
		t.Fatal(err)
	}

	docs := resumed.Collections[0]
	if resumed.Phase != domain.MigrationCompleted {
		t.Errorf("phase: %s", resumed.Phase)
	}
	if want := []fakes.Upsert{migrationUpsert("docs", domain.PointShardKey{}, 1)}; !reflect.DeepEqual(f.qdrant.Upserts[4:], want) {
		t.Errorf("upserts after restart: %v", f.qdrant.Upserts[4:])
	}
	if docs.PointsUpdated != 2 || docs.SyncPass != 1 {
		t.Errorf("docs: %+v", docs)
	}
	for id, want := range map[int]string{2: "[7.0,7.0]", 6: "[8.0,8.0]"} {
		if got := string(f.stored(migrationTarget(), "docs", id).Vector); got != want {
			t.Errorf("vector of %d: %s", id, got)
		}
	}
}

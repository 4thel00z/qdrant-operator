package usecases_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

var migrationFresh = domain.MigrationStatus{Phase: domain.MigrationPending}

func migrationSource() string {
	return collectionNodeURL(0, "db")
}

func migrationTarget() string {
	return collectionNodeURL(0, "target")
}

func migrationVectors(size int) domain.JSON {
	return domain.JSON{"size": size, "distance": "Dot"}
}

func migrationBody() domain.JSON {
	return domain.JSON{"vectors": migrationVectors(2), "shard_number": 1, "replication_factor": 1}
}

func migrationPoint(id int) domain.Point {
	return domain.Point{
		ID:      domain.NumberPointID(uint64(id)),
		Vector:  json.RawMessage(fmt.Sprintf("[%d.0,0.5]", id)),
		Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, id)),
	}
}

func migrationPoints(count int, shardKeys ...string) []domain.Point {
	points := []domain.Point{}
	for id := 1; id <= count; id++ {
		point := migrationPoint(id)
		if len(shardKeys) > 0 {
			point.ShardKey = json.RawMessage(fmt.Sprintf("%q", shardKeys[id%len(shardKeys)]))
		}
		points = append(points, point)
	}
	return points
}

func migrationWave(first int, count int) []domain.Point {
	points := []domain.Point{}
	for _, point := range migrationPoints(count) {
		point.ID = domain.NumberPointID(uint64(first) + uint64(len(points)))
		points = append(points, point)
	}
	return points
}

func migrationChanged(id int, payload string, vector string) domain.Point {
	point := migrationPoint(id)
	if payload != "" {
		point.Payload = json.RawMessage(payload)
	}
	if vector != "" {
		point.Vector = json.RawMessage(vector)
	}
	return point
}

func migrationIDs(points []domain.Point) []string {
	ids := []string{}
	for _, point := range points {
		ids = append(ids, point.ID.String())
	}
	return ids
}

func migrationRange(first int, last int) []string {
	ids := []string{}
	for id := first; id <= last; id++ {
		ids = append(ids, fmt.Sprint(id))
	}
	return ids
}

func (f *collectionFixture) migrate() usecases.ExecuteMigration {
	return usecases.ExecuteMigration{Qdrant: f.qdrant, Kubernetes: f.kubernetes, Clock: f.clock()}
}

func (f *collectionFixture) run(t *testing.T, spec domain.MigrationSpec, current domain.MigrationStatus) domain.MigrationStatus {
	t.Helper()
	status, err := f.migrate().Execute(context.Background(), spec, migrationRef(), current)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func (f *collectionFixture) storedIDs(url string, collection string) []string {
	return migrationIDs(f.qdrant.StoredPoints(url, collection))
}

func (f *collectionFixture) stored(url string, collection string, id int) domain.Point {
	for _, point := range f.qdrant.StoredPoints(url, collection) {
		if point.ID == domain.NumberPointID(uint64(id)) {
			return point
		}
	}
	return domain.Point{}
}

func migrationRunning(f *collectionFixture) []map[string]any {
	running := []map[string]any{}
	for _, patch := range f.kubernetes.StatusPatches {
		collections, _ := patch.Status["collections"].([]any)
		for _, collection := range collections {
			entry, _ := collection.(map[string]any)
			if entry["status"] == "Running" {
				running = append(running, entry)
			}
		}
	}
	return running
}

func migrationRecorded(f *collectionFixture) [][2]string {
	recorded := [][2]string{}
	for _, entry := range migrationRunning(f) {
		recorded = append(recorded, [2]string{fmt.Sprint(entry["pointsCopied"]), fmt.Sprint(entry["offset"])})
	}
	return recorded
}

func migrationSaved(t *testing.T, f *collectionFixture) domain.MigrationStatus {
	t.Helper()
	patches := f.kubernetes.StatusPatches
	api, err := usecases.DecodeBody[qdrantv1alpha1.QdrantMigrationStatus](patches[len(patches)-1].Status)
	if err != nil {
		t.Fatal(err)
	}
	status, err := translate.MigrationStatusFromAPI(api)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func migrationUpsert(collection string, key domain.PointShardKey, count int) fakes.Upsert {
	return fakes.Upsert{Collection: collection, ShardKey: key, Count: count}
}

func migrationUpserts(f *collectionFixture, collection string) []int {
	counts := []int{}
	for _, upsert := range f.qdrant.Upserts {
		if upsert.Collection == collection {
			counts = append(counts, upsert.Count)
		}
	}
	return counts
}

func migrationNumberIs(value any, want int) bool {
	return fmt.Sprint(value) == fmt.Sprint(want)
}

func TestMigrationCopiesCollectionsInBatchesAndCreatesTargets(t *testing.T) {
	f := newCollectionFixture()
	f.kubernetes.PutResource(collectionClusterBody("target", 3, nil))
	f.qdrant.Routes[collectionServiceURL("target")] = migrationTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, migrationBody())
	f.qdrant.AddCollection(migrationSource(), "logs", 0, migrationBody())
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(7))
	f.qdrant.AddPoints(migrationSource(), "logs", migrationPoints(2))
	if err := f.qdrant.CreatePayloadIndex(context.Background(), domain.QdrantNode{URL: migrationSource()}, "docs", "n", "integer"); err != nil {
		t.Fatal(err)
	}
	spec := migrationSpec(t, domain.JSON{
		"batchSize":         3,
		"collectionMapping": domain.JSON{"docs": "docs_v2"},
		"target":            domain.JSON{"replicationFactor": 2},
	})

	status := f.run(t, spec, migrationFresh)

	if status.Phase != domain.MigrationCompleted || status.Source != collectionFixtureNS+"/db" {
		t.Errorf("status: %+v", status)
	}
	type row struct {
		name, target  string
		total, copied int64
	}
	rows := []row{}
	for _, collection := range status.Collections {
		rows = append(rows, row{collection.Name, collection.TargetName, collection.PointsTotal, collection.PointsCopied})
	}
	if want := []row{{"docs", "docs_v2", 7, 7}, {"logs", "logs", 2, 2}}; !reflect.DeepEqual(rows, want) {
		t.Errorf("collections: %v", rows)
	}
	if status.Progress().Percentage() != 100 {
		t.Errorf("percentage: %d", status.Progress().Percentage())
	}
	if got := f.storedIDs(migrationTarget(), "docs_v2"); !reflect.DeepEqual(got, migrationRange(1, 7)) {
		t.Errorf("target ids: %v", got)
	}
	if got := string(f.stored(migrationTarget(), "docs_v2", 1).Payload); got != `{"n":1}` {
		t.Errorf("payload: %s", got)
	}
	if got := migrationUpserts(f, "docs_v2"); !reflect.DeepEqual(got, []int{3, 3, 1}) {
		t.Errorf("upserts: %v", got)
	}
	created, _ := f.qdrant.Collections[migrationTarget()]["docs_v2"]["config"].(domain.JSON)
	params, _ := created["params"].(domain.JSON)
	if !reflect.DeepEqual(params["vectors"], migrationVectors(2)) || params["replication_factor"] != 2 {
		t.Errorf("created params: %v", params)
	}
	schema, _ := f.qdrant.Collections[migrationTarget()]["docs_v2"]["payload_schema"].(domain.JSON)
	if _, indexed := schema["n"]; !indexed {
		t.Errorf("payload schema: %v", schema)
	}
	patches := f.kubernetes.StatusPatches
	if len(patches) != 3 || patches[0].Status["phase"] != "Running" {
		t.Errorf("patches: %v", patches)
	}
}

func migrationGrowing(f *collectionFixture, waves [][]domain.Point, targetPoints int) {
	f.routeTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, migrationBody())
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(7))
	f.qdrant.LatePoints[fakes.PointsKey{NodeURL: migrationSource(), Collection: "docs"}] = waves
	if targetPoints == 0 {
		return
	}
	f.qdrant.AddCollection(migrationTarget(), "docs", 0, migrationBody())
	existing := []domain.Point{}
	for _, point := range migrationPoints(targetPoints) {
		point.ID = domain.NumberPointID(uint64(1000) + uint64(len(existing)) + 1)
		existing = append(existing, point)
	}
	f.qdrant.AddPoints(migrationTarget(), "docs", existing)
}

func TestMigrationVerifyPassCopiesPointsWrittenDuringTheCopy(t *testing.T) {
	f := newCollectionFixture()
	migrationGrowing(f, [][]domain.Point{migrationWave(8, 2)}, 0)

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 3}), migrationFresh)

	docs := status.Collections[0]
	if status.Phase != domain.MigrationCompleted {
		t.Errorf("phase: %s", status.Phase)
	}
	if docs.VerifyPass != 1 || *docs.SourceCount != 9 || *docs.TargetCount != 9 {
		t.Errorf("counts: %+v", docs)
	}
	if docs.PointsTotal != 9 || docs.PointsCopied != 9 || docs.Message != "" {
		t.Errorf("progress: %+v", docs)
	}
	if got := len(f.qdrant.StoredPoints(migrationTarget(), "docs")); got != 9 {
		t.Errorf("target points: %d", got)
	}
	running := migrationRunning(f)
	if len(running) == 0 || !migrationNumberIs(running[len(running)-1]["pointsTotal"], 9) {
		t.Errorf("running entries: %v", running)
	}
}

func TestMigrationFailsWithBothCountsWhenVerifyPassesRunOut(t *testing.T) {
	f := newCollectionFixture()
	migrationGrowing(f, [][]domain.Point{migrationWave(8, 2), migrationWave(10, 3)}, 0)

	status := f.run(t, migrationSpec(t, nil), migrationFresh)

	docs := status.Collections[0]
	if status.Phase != domain.MigrationFailed || docs.Status != domain.MigrationFailed {
		t.Errorf("phases: %s %s", status.Phase, docs.Status)
	}
	if docs.Error != "target has 9 of 12 source points after 1 verify pass" {
		t.Errorf("error: %q", docs.Error)
	}
	if *docs.SourceCount != 12 || *docs.TargetCount != 9 || status.Error != docs.Error {
		t.Errorf("counts: %+v, status error %q", docs, status.Error)
	}
}

func TestMigrationWithoutVerifyPassesStillComparesCounts(t *testing.T) {
	f := newCollectionFixture()
	migrationGrowing(f, [][]domain.Point{migrationWave(8, 2)}, 0)

	status := f.run(t, migrationSpec(t, domain.JSON{"verifyPasses": 0}), migrationFresh)

	if got := status.Collections[0].Error; got != "target has 7 of 9 source points after 0 verify passes" {
		t.Errorf("error: %q", got)
	}
}

func TestMigrationNotesATargetWithMorePointsThanTheSource(t *testing.T) {
	f := newCollectionFixture()
	migrationGrowing(f, nil, 3)

	status := f.run(t, migrationSpec(t, nil), migrationFresh)

	docs := status.Collections[0]
	if status.Phase != domain.MigrationCompleted || *docs.SourceCount != 7 || *docs.TargetCount != 10 {
		t.Errorf("status: %+v", status)
	}
	if docs.Message != "target has 10 points, more than the 7 on the source" {
		t.Errorf("message: %q", docs.Message)
	}
}

func TestMigrationRestartDuringAVerifyPassResumesThatPassAndCompletes(t *testing.T) {
	f := newCollectionFixture()
	migrationGrowing(f, [][]domain.Point{migrationWave(8, 2)}, 0)
	five := 5
	f.qdrant.UnreachableAfterUpserts = &five
	use := f.migrate()
	spec := migrationSpec(t, domain.JSON{"batchSize": 2, "checkpointBatches": 1})

	_, err := use.Execute(context.Background(), spec, migrationRef(), migrationFresh)
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}

	saved := migrationSaved(t, f)
	checkpoint := saved.Collections[0]
	if checkpoint.VerifyPass != 1 || checkpoint.Offset != domain.NumberPointID(3) {
		t.Errorf("checkpoint: %+v", checkpoint)
	}
	f.qdrant.UnreachableAfterUpserts = nil
	resumed, err := use.Execute(context.Background(), spec, migrationRef(), saved)
	if err != nil {
		t.Fatal(err)
	}

	docs := resumed.Collections[0]
	if resumed.Phase != domain.MigrationCompleted || *docs.SourceCount != 9 || *docs.TargetCount != 9 {
		t.Errorf("resumed: %+v", resumed)
	}
}

func TestMigrationDropsSourceShardKeysForAutoShardedTargetsAndReadsEndpoints(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	f.kubernetes.Secrets[collectionFixtureNS+"/cloud"] = map[string]string{"api-key": "cloud-key"}
	cloud := "https://x.cloud.qdrant.io:6333"
	vectors := domain.JSON{"vectors": migrationVectors(2)}
	f.qdrant.AddCollection(cloud, "docs", 0, vectors)
	f.qdrant.AddPoints(cloud, "docs", migrationPoints(4, "eu", "us"))
	f.qdrant.AddCollection(migrationTarget(), "docs", 0, vectors)
	spec := migrationSpec(t, domain.JSON{
		"source": domain.JSON{"endpoint": domain.JSON{
			"url":             cloud,
			"apiKeySecretRef": domain.JSON{"name": "cloud", "key": "api-key"},
		}},
		"batchSize": 10,
	})

	status := f.run(t, spec, migrationFresh)

	if status.Phase != domain.MigrationCompleted || status.Source != cloud {
		t.Errorf("status: %+v", status)
	}
	if want := []fakes.Upsert{migrationUpsert("docs", domain.PointShardKey{}, 4)}; !reflect.DeepEqual(f.qdrant.Upserts, want) {
		t.Errorf("upserts: %v", f.qdrant.Upserts)
	}
	if shardKey := f.stored(migrationTarget(), "docs", 1).ShardKey; len(shardKey) != 0 {
		t.Errorf("shard key carried over: %s", shardKey)
	}
}

func TestMigrationReportsPerCollectionFailuresAndMissingSources(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, nil)
	f.qdrant.AddCollection(migrationSource(), "logs", 0, nil)
	f.qdrant.AddPoints(migrationSource(), "logs", migrationPoints(1))
	use := f.migrate()
	ctx := context.Background()

	status := f.run(t, migrationSpec(t, domain.JSON{"createMissing": false}), migrationFresh)
	_, missing := use.Execute(ctx, migrationSpec(t, domain.JSON{"collections": []any{"ghost"}}), migrationRef(), migrationFresh)
	_, ghost := use.Execute(ctx, migrationSpec(t, domain.JSON{"targetClusterRef": domain.JSON{"name": "ghost"}}), migrationRef(), migrationFresh)

	if status.Phase != domain.MigrationFailed {
		t.Errorf("phase: %s", status.Phase)
	}
	for _, collection := range status.Collections {
		if collection.Status != domain.MigrationFailed {
			t.Errorf("collection %s: %s", collection.Name, collection.Status)
		}
	}
	if !strings.Contains(status.Error, "createMissing is false") || status.Conditions[0].Reason != "MigrationFailed" {
		t.Errorf("error %q, conditions %+v", status.Error, status.Conditions)
	}
	if missing == nil || !strings.Contains(missing.Error(), "not present") {
		t.Errorf("missing source: %v", missing)
	}
	if !errors.Is(ghost, usecases.ErrClusterNotFound) {
		t.Errorf("ghost target: %v", ghost)
	}
}

func TestMigrationResumesFromRecordedOffsetsAndSkipsFinishedCollections(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	for _, name := range []string{"docs", "logs"} {
		f.qdrant.AddCollection(migrationSource(), name, 0, nil)
		f.qdrant.AddPoints(migrationSource(), name, migrationPoints(6))
		f.qdrant.AddCollection(migrationTarget(), name, 0, nil)
		f.qdrant.AddPoints(migrationTarget(), name, migrationPoints(4))
	}
	previous := domain.MigrationStatus{
		Phase:     domain.MigrationRunning,
		StartTime: collectionFixtureNow,
		Collections: []domain.CollectionMigration{
			{Name: "docs", TargetName: "docs", Status: domain.MigrationCompleted, PointsTotal: 6, PointsCopied: 6},
			{Name: "logs", TargetName: "logs", Status: domain.MigrationRunning, PointsTotal: 6, PointsCopied: 4, Offset: domain.NumberPointID(5)},
		},
	}

	status := f.run(t, migrationSpec(t, domain.JSON{"batchSize": 2}), previous)

	if status.Phase != domain.MigrationCompleted || !status.StartTime.Equal(collectionFixtureNow) {
		t.Errorf("status: %+v", status)
	}
	for index, want := range []int64{6, 6} {
		collection := status.Collections[index]
		if collection.PointsCopied != want || !collection.Offset.IsZero() {
			t.Errorf("collection %s: copied %d, offset %v", collection.Name, collection.PointsCopied, collection.Offset)
		}
	}
	if want := []fakes.Upsert{migrationUpsert("logs", domain.PointShardKey{}, 2)}; !reflect.DeepEqual(f.qdrant.Upserts, want) {
		t.Errorf("upserts: %v", f.qdrant.Upserts)
	}
	if got := f.storedIDs(migrationTarget(), "logs"); !reflect.DeepEqual(got, migrationRange(1, 6)) {
		t.Errorf("target ids: %v", got)
	}
}

func TestMigrationRecordsOffsetsWhileCopying(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, nil)
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(45))

	f.run(t, migrationSpec(t, domain.JSON{"batchSize": 2}), migrationFresh)

	if want := [][2]string{{"40", "41"}}; !reflect.DeepEqual(migrationRecorded(f), want) {
		t.Errorf("recorded: %v", migrationRecorded(f))
	}
}

func TestMigrationCheckpointIntervalComesFromTheSpec(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	f.qdrant.AddCollection(migrationSource(), "docs", 0, nil)
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(10))

	f.run(t, migrationSpec(t, domain.JSON{"batchSize": 2, "checkpointBatches": 2}), migrationFresh)

	if want := [][2]string{{"4", "5"}, {"8", "9"}}; !reflect.DeepEqual(migrationRecorded(f), want) {
		t.Errorf("recorded: %v", migrationRecorded(f))
	}
}

func TestMigrationLetsConnectionErrorsEscapeAndKeepsTheCheckpoint(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	three := 3
	f.qdrant.UnreachableAfterUpserts = &three
	f.qdrant.AddCollection(migrationSource(), "docs", 0, nil)
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(10))
	use := f.migrate()

	_, err := use.Execute(context.Background(), migrationSpec(t, domain.JSON{"batchSize": 2, "checkpointBatches": 1}), migrationRef(), migrationFresh)
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("want ErrUnreachable, got %v", err)
	}

	stored := migrationSaved(t, f).Collections[0]
	if stored.Status != domain.MigrationRunning || stored.Offset != domain.NumberPointID(7) || stored.PointsCopied != 6 {
		t.Errorf("checkpoint: %+v", stored)
	}
	f.qdrant.UnreachableAfterUpserts = nil
	status, err := use.Execute(context.Background(), migrationSpec(t, domain.JSON{"batchSize": 2}), migrationRef(), migrationSaved(t, f))
	if err != nil {
		t.Fatal(err)
	}

	if status.Phase != domain.MigrationCompleted {
		t.Errorf("phase: %s", status.Phase)
	}
	if got := f.storedIDs(migrationTarget(), "docs"); !reflect.DeepEqual(got, migrationRange(1, 10)) {
		t.Errorf("target ids: %v", got)
	}
}

func TestMigrationPreservesCustomShardKeysAndRoutesByPayloadField(t *testing.T) {
	f := newCollectionFixture()
	f.kubernetes.PutResource(collectionClusterBody("target", 3, nil))
	f.qdrant.Routes[collectionServiceURL("target")] = migrationTarget()
	custom := domain.JSON{"vectors": migrationVectors(2), "sharding_method": "custom"}
	f.qdrant.AddCollection(migrationSource(), "docs", 0, custom)
	f.qdrant.AddCollection(migrationSource(), "tiers", 0, custom)
	f.qdrant.Collections[migrationSource()]["docs"]["shard_keys"] = []any{"eu", "us"}
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(4, "eu", "us"))
	tiers := []domain.Point{}
	for index, point := range migrationPoints(4) {
		tier := "free"
		if (index+1)%2 == 1 {
			tier = "gold"
		}
		point.Payload = json.RawMessage(fmt.Sprintf(`{"tier":%q}`, tier))
		tiers = append(tiers, point)
	}
	f.qdrant.AddPoints(migrationSource(), "tiers", tiers)

	preserved := f.run(t, migrationSpec(t, domain.JSON{"collections": []any{"docs"}, "target": domain.JSON{"replicationFactor": 2}}), migrationFresh)
	routed := f.run(t, migrationSpec(t, domain.JSON{"collections": []any{"tiers"}, "target": domain.JSON{"shardKeyField": "tier"}}), migrationFresh)

	if preserved.Phase != domain.MigrationCompleted || routed.Phase != domain.MigrationCompleted {
		t.Fatalf("phases: %s %s (%s, %s)", preserved.Phase, routed.Phase, preserved.Error, routed.Error)
	}
	docs := f.qdrant.Collections[migrationTarget()]["docs"]
	config, _ := docs["config"].(domain.JSON)
	params, _ := config["params"].(domain.JSON)
	if params["sharding_method"] != "custom" {
		t.Errorf("sharding method: %v", params["sharding_method"])
	}
	docKeys := []string{}
	for _, key := range docs["shard_keys"].([]any) {
		docKeys = append(docKeys, fmt.Sprint(key))
	}
	slices.Sort(docKeys)
	if !reflect.DeepEqual(docKeys, []string{"eu", "us"}) {
		t.Errorf("docs shard keys: %v", docKeys)
	}
	wantBodies := []fakes.ShardKeyBody{
		{Collection: "docs", Body: domain.JSON{"shard_key": "us", "replication_factor": 2}},
		{Collection: "docs", Body: domain.JSON{"shard_key": "eu", "replication_factor": 2}},
	}
	if got := f.qdrant.ShardKeyBodies[:2]; !reflect.DeepEqual(got, wantBodies) {
		t.Errorf("shard key bodies: %v", got)
	}
	tierKeys := []string{}
	for _, key := range f.qdrant.Collections[migrationTarget()]["tiers"]["shard_keys"].([]any) {
		tierKeys = append(tierKeys, fmt.Sprint(key))
	}
	slices.Sort(tierKeys)
	if !reflect.DeepEqual(tierKeys, []string{"free", "gold"}) {
		t.Errorf("tiers shard keys: %v", tierKeys)
	}
	upserts := []string{}
	for _, upsert := range f.qdrant.Upserts {
		upserts = append(upserts, fmt.Sprintf("%s/%s/%d", upsert.Collection, upsert.ShardKey.Text, upsert.Count))
	}
	slices.Sort(upserts)
	if want := []string{"docs/eu/2", "docs/us/2", "tiers/free/2", "tiers/gold/2"}; !reflect.DeepEqual(upserts, want) {
		t.Errorf("upserts: %v", upserts)
	}
}

func TestMigrationChecksVectorLayoutAndEnsuresIndexesOnExistingTargets(t *testing.T) {
	f := newCollectionFixture()
	f.routeTarget()
	narrow := domain.JSON{"vectors": migrationVectors(2)}
	f.qdrant.AddCollection(migrationSource(), "docs", 0, narrow)
	f.qdrant.AddCollection(migrationSource(), "wide", 0, domain.JSON{"vectors": migrationVectors(9)})
	if err := f.qdrant.CreatePayloadIndex(context.Background(), domain.QdrantNode{URL: migrationSource()}, "docs", "n", "integer"); err != nil {
		t.Fatal(err)
	}
	f.qdrant.AddPoints(migrationSource(), "docs", migrationPoints(1))
	f.qdrant.AddCollection(migrationTarget(), "docs", 0, narrow)
	f.qdrant.AddCollection(migrationTarget(), "wide", 0, narrow)
	f.qdrant.IndexChanges = nil

	status := f.run(t, migrationSpec(t, nil), migrationFresh)

	byName := map[string]domain.CollectionMigration{}
	for _, collection := range status.Collections {
		byName[collection.Name] = collection
	}
	if byName["docs"].Status != domain.MigrationCompleted {
		t.Errorf("docs: %+v", byName["docs"])
	}
	if want := []fakes.IndexChange{{Action: "create", Collection: "docs", Field: "n"}}; !reflect.DeepEqual(f.qdrant.IndexChanges, want) {
		t.Errorf("index changes: %v", f.qdrant.IndexChanges)
	}
	if byName["wide"].Status != domain.MigrationFailed || !strings.Contains(byName["wide"].Error, "vector sizes or distances") {
		t.Errorf("wide: %+v", byName["wide"])
	}
	if got := migrationUpserts(f, "wide"); len(got) != 0 {
		t.Errorf("wide was upserted: %v", got)
	}
}

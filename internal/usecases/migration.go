package usecases

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

// ExecuteMigration copies collections point by point from any Qdrant into a managed cluster.
//
// Scroll pages on the source are upserted on the target, grouped by the shard key each point lands
// under (its source key by default, or a fixed key or payload field from spec.Target). Missing
// target collections are created from the source configuration and payload schema, with shard and
// replication counts left to the target cluster unless the spec overrides them. Progress and the
// scroll offset are patched into the status every spec.CheckpointBatches batches, so a re-run after
// an interruption resumes there; upserts are idempotent by point id. After a scroll reaches the
// end, exact source and target counts are compared; while the target holds fewer points, up to
// spec.VerifyPasses further passes scroll the source from the start. A collection is Completed
// only when the target holds at least as many points as the source, otherwise Failed with both counts.
//
// With spec.Sync the verify passes are replaced by sync passes: source and target are scrolled in
// point-ID order window by window, points missing or different on the target are upserted again
// and target points the source no longer has are deleted. Another pass runs only while the previous
// one changed something, up to spec.SyncMaxPasses.
//
// An unreachable Qdrant and a cancelled context are returned as errors so the caller retries from
// the recorded checkpoint instead of recording the collection as failed.
type ExecuteMigration struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
	Clock      Clock
}

type migrationRun struct {
	spec   domain.MigrationSpec
	source domain.QdrantNode
	target domain.QdrantNode
}

type migrationReport func(progress domain.CollectionMigration) error

type migrationShardKeys map[domain.PointShardKey]bool

// migrationReportError marks a failed status patch, which must not be recorded as a failed copy.
type migrationReportError struct {
	err error
}

func (e *migrationReportError) Error() string {
	return fmt.Sprintf("patch migration status: %v", e.err)
}

func (e *migrationReportError) Unwrap() error {
	return e.err
}

// Execute returns the final status, Completed or Failed. Progress is patched into the resource status
// through ref while it runs; the returned status is for the caller to record.
func (m ExecuteMigration) Execute(ctx context.Context, spec domain.MigrationSpec, ref domain.ResourceRef, current domain.MigrationStatus) (domain.MigrationStatus, error) {
	startTime := current.StartTime
	if startTime.IsZero() {
		startTime = m.now()
	}
	source, err := m.resolveSource(ctx, spec)
	if err != nil {
		return domain.MigrationStatus{}, err
	}
	connection, err := ResolveCluster{Kubernetes: m.Kubernetes}.Execute(ctx, spec.TargetClusterRef)
	if err != nil {
		return domain.MigrationStatus{}, err
	}
	target := connection.Service
	if !m.Qdrant.Ready(ctx, target) {
		return domain.MigrationStatus{}, fmt.Errorf("QdrantCluster %s: %w", spec.TargetClusterRef.Name, ErrClusterNotReady)
	}

	available, err := m.Qdrant.ListCollections(ctx, source)
	if err != nil {
		return domain.MigrationStatus{}, fmt.Errorf("list source collections: %w", err)
	}
	names, err := spec.SelectCollections(available)
	if err != nil {
		return domain.MigrationStatus{}, err
	}
	run := migrationRun{spec: spec, source: source, target: target}
	collections := make([]domain.CollectionMigration, 0, len(names))
	for _, name := range names {
		planned, err := m.planCollection(ctx, run, current, name)
		if err != nil {
			return domain.MigrationStatus{}, err
		}
		collections = append(collections, planned)
	}

	running := domain.MigrationStatus{
		Phase:     domain.MigrationRunning,
		StartTime: startTime,
		Source:    spec.Source.Description(),
	}
	patch := func() error {
		running.Collections = append([]domain.CollectionMigration{}, collections...)
		body, err := migrationStatusBody(running)
		if err != nil {
			return err
		}
		return m.Kubernetes.PatchStatus(ctx, ref, body)
	}
	if err := patch(); err != nil {
		return domain.MigrationStatus{}, fmt.Errorf("patch migration status: %w", err)
	}

	for index := range collections {
		if collections[index].Status == domain.MigrationCompleted {
			continue
		}
		report := func(progress domain.CollectionMigration) error {
			total, err := m.Qdrant.CountPoints(ctx, source, progress.Name, false)
			if err != nil {
				return fmt.Errorf("count points of %s: %w", progress.Name, err)
			}
			progress.Status = domain.MigrationRunning
			progress.PointsTotal = total
			collections[index] = progress
			if err := patch(); err != nil {
				return &migrationReportError{err: err}
			}
			return nil
		}
		migrated, err := m.migrateCollection(ctx, run, collections[index], report)
		if err != nil {
			return domain.MigrationStatus{}, err
		}
		collections[index] = migrated
		if err := patch(); err != nil {
			return domain.MigrationStatus{}, fmt.Errorf("patch migration status: %w", err)
		}
	}

	return finishedMigration(spec, startTime, collections, m.now()), nil
}

func (m ExecuteMigration) now() time.Time {
	if m.Clock == nil {
		return SystemClock()
	}
	return m.Clock()
}

func finishedMigration(spec domain.MigrationSpec, startTime time.Time, collections []domain.CollectionMigration, completionTime time.Time) domain.MigrationStatus {
	failed := []domain.CollectionMigration{}
	for _, collection := range collections {
		if collection.Status == domain.MigrationFailed {
			failed = append(failed, collection)
		}
	}
	status := domain.MigrationStatus{
		Phase:          domain.MigrationCompleted,
		StartTime:      startTime,
		CompletionTime: completionTime,
		Source:         spec.Source.Description(),
		Collections:    collections,
	}
	condition := domain.Condition{
		Type:               "Complete",
		Status:             domain.ConditionTrue,
		LastTransitionTime: completionTime,
		Reason:             "MigrationCompleted",
		Message:            fmt.Sprintf("Copied %d/%d", len(collections)-len(failed), len(collections)),
	}
	if len(failed) > 0 {
		status.Phase = domain.MigrationFailed
		status.Error = failed[0].Error
		condition.Status = domain.ConditionFalse
		condition.Reason = "MigrationFailed"
	}
	status.Conditions = []domain.Condition{condition}
	return status
}

// migrationStatusBody is the status patch body; numbers stay exact so large point IDs survive.
func migrationStatusBody(status domain.MigrationStatus) (domain.JSON, error) {
	api, err := translate.MigrationStatusToAPI(status)
	if err != nil {
		return nil, fmt.Errorf("encode migration status: %w", err)
	}
	raw, err := json.Marshal(api)
	if err != nil {
		return nil, fmt.Errorf("encode migration status: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	body := domain.JSON{}
	if err := decoder.Decode(&body); err != nil {
		return nil, fmt.Errorf("decode migration status: %w", err)
	}
	return body, nil
}

// planCollection is a fresh entry, or the previous run's so a finished copy is kept and a partial one resumes.
func (m ExecuteMigration) planCollection(ctx context.Context, run migrationRun, current domain.MigrationStatus, name string) (domain.CollectionMigration, error) {
	previous, found := current.ResumePoint(name)
	if found && previous.Status == domain.MigrationCompleted {
		return previous, nil
	}
	total, err := m.Qdrant.CountPoints(ctx, run.source, name, false)
	if err != nil {
		return domain.CollectionMigration{}, fmt.Errorf("count points of %s: %w", name, err)
	}
	planned := domain.NewCollectionMigration(name, run.spec.TargetName(name))
	planned.PointsTotal = total
	if !found {
		return planned, nil
	}
	planned.PointsCopied = previous.PointsCopied
	planned.Offset = previous.Offset
	if previous.Status != domain.MigrationRunning {
		return planned, nil
	}
	planned.VerifyPass = previous.VerifyPass
	planned.PointsUpdated = previous.PointsUpdated
	planned.PointsDeleted = previous.PointsDeleted
	planned.SyncPass = previous.SyncPass
	planned.SyncOffset = previous.SyncOffset
	planned.SyncPassChanges = previous.SyncPassChanges
	return planned, nil
}

func (m ExecuteMigration) resolveSource(ctx context.Context, spec domain.MigrationSpec) (domain.QdrantNode, error) {
	if spec.Source.ClusterRef != nil {
		connection, err := ResolveCluster{Kubernetes: m.Kubernetes}.Execute(ctx, *spec.Source.ClusterRef)
		return connection.Service, err
	}
	endpoint := spec.Source.Endpoint
	if endpoint == nil {
		return domain.QdrantNode{}, errors.New("QdrantMigration source has neither clusterRef nor endpoint")
	}
	node := domain.QdrantNode{URL: endpoint.URL}
	if endpoint.APIKeyRef != nil {
		apiKey, err := m.Kubernetes.SecretValue(ctx, *endpoint.APIKeyRef)
		if err != nil {
			return domain.QdrantNode{}, err
		}
		node.APIKey = apiKey
	}
	if endpoint.CARef != nil {
		caCert, err := m.Kubernetes.SecretValue(ctx, *endpoint.CARef)
		if err != nil {
			return domain.QdrantNode{}, err
		}
		node.CACert = caCert
	}
	return node, nil
}

func (m ExecuteMigration) migrateCollection(ctx context.Context, run migrationRun, collection domain.CollectionMigration, report migrationReport) (domain.CollectionMigration, error) {
	finished, err := m.copyCollection(ctx, run, collection, report)
	if err == nil {
		return finished, nil
	}
	if migrationAborts(err) {
		return domain.CollectionMigration{}, err
	}
	collection.Status = domain.MigrationFailed
	collection.Error = err.Error()
	return collection, nil
}

func migrationAborts(err error) bool {
	var reportError *migrationReportError
	return errors.Is(err, ports.ErrUnreachable) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.As(err, &reportError)
}

func (m ExecuteMigration) copyCollection(ctx context.Context, run migrationRun, collection domain.CollectionMigration, report migrationReport) (domain.CollectionMigration, error) {
	shardKeys, err := m.ensureTargetCollection(ctx, run, collection)
	if err != nil {
		return domain.CollectionMigration{}, err
	}
	if run.spec.Sync {
		return m.copyAndSync(ctx, run, collection, shardKeys, report)
	}
	return m.copyAndVerify(ctx, run, collection, shardKeys, report)
}

// ensureTargetCollection creates or checks the target and returns its shard keys, or nil for auto sharding.
func (m ExecuteMigration) ensureTargetCollection(ctx context.Context, run migrationRun, collection domain.CollectionMigration) (migrationShardKeys, error) {
	info, err := m.Qdrant.CollectionInfo(ctx, run.source, collection.Name)
	if err != nil {
		return nil, fmt.Errorf("read source collection %s: %w", collection.Name, err)
	}
	config, ok := info["config"].(domain.JSON)
	if !ok {
		return nil, fmt.Errorf("source collection %s reports no config", collection.Name)
	}
	schema, _ := info["payload_schema"].(domain.JSON)
	exists, err := m.Qdrant.CollectionExists(ctx, run.target, collection.TargetName)
	if err != nil {
		return nil, fmt.Errorf("check target collection %s: %w", collection.TargetName, err)
	}
	if !exists && !run.spec.CreateMissing {
		return nil, fmt.Errorf("Collection %s missing on target and createMissing is false", collection.TargetName)
	}
	if !exists {
		body := domain.CreateBodyFromConfig(config, run.spec.Target)
		if err := m.Qdrant.CreateCollection(ctx, run.target, collection.TargetName, body); err != nil {
			return nil, fmt.Errorf("create target collection %s: %w", collection.TargetName, err)
		}
	}
	targetInfo, err := m.Qdrant.CollectionInfo(ctx, run.target, collection.TargetName)
	if err != nil {
		return nil, fmt.Errorf("read target collection %s: %w", collection.TargetName, err)
	}
	targetConfig, _ := targetInfo["config"].(domain.JSON)
	if err := checkVectorLayout(collection.TargetName, config, targetConfig); err != nil {
		return nil, err
	}
	if !exists || run.spec.EnsurePayloadIndexes {
		targetSchema, _ := targetInfo["payload_schema"].(domain.JSON)
		if err := m.ensurePayloadIndexes(ctx, run, collection, schema, targetSchema); err != nil {
			return nil, err
		}
	}
	targetParams, _ := targetConfig["params"].(domain.JSON)
	if targetParams["sharding_method"] != domain.ShardingCustom {
		return nil, nil
	}
	listed, err := m.Qdrant.ListShardKeys(ctx, run.target, collection.TargetName)
	if err != nil {
		return nil, fmt.Errorf("list shard keys of %s: %w", collection.TargetName, err)
	}
	keys := migrationShardKeys{}
	for _, listedKey := range listed {
		key, ok := migrationShardKey(listedKey)
		if !ok {
			return nil, fmt.Errorf("target collection %s has shard key %v that is neither a string nor an unsigned integer", collection.TargetName, listedKey)
		}
		keys[key] = true
	}
	return keys, nil
}

func checkVectorLayout(targetName string, source domain.JSON, target domain.JSON) error {
	sourceLayout, err := domain.VectorLayout(source)
	if err != nil {
		return fmt.Errorf("source vectors: %w", err)
	}
	targetLayout, err := domain.VectorLayout(target)
	if err != nil {
		return fmt.Errorf("target vectors: %w", err)
	}
	if !domain.IsSubset(sourceLayout, targetLayout) {
		return fmt.Errorf("Collection %s on the target has different vector sizes or distances than the source", targetName)
	}
	return nil
}

func (m ExecuteMigration) ensurePayloadIndexes(ctx context.Context, run migrationRun, collection domain.CollectionMigration, schema domain.JSON, targetSchema domain.JSON) error {
	indexes, err := domain.PayloadIndexesFromSchema(schema)
	if err != nil {
		return fmt.Errorf("source payload schema: %w", err)
	}
	for _, index := range indexes {
		if _, present := targetSchema[index.Field]; present {
			continue
		}
		if err := m.Qdrant.CreatePayloadIndex(ctx, run.target, collection.TargetName, index.Field, index.Schema); err != nil {
			return fmt.Errorf("create payload index %s on %s: %w", index.Field, collection.TargetName, err)
		}
	}
	return nil
}

func migrationShardKey(value any) (domain.PointShardKey, bool) {
	switch typed := value.(type) {
	case string:
		return domain.TextShardKey(typed), true
	case domain.PointShardKey:
		return typed, true
	case uint64:
		return domain.NumberShardKey(typed), true
	case int:
		return migrationSignedShardKey(int64(typed))
	case int64:
		return migrationSignedShardKey(typed)
	case float64:
		return migrationSignedShardKey(int64(typed))
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return domain.PointShardKey{}, false
		}
		return migrationSignedShardKey(parsed)
	}
	return domain.PointShardKey{}, false
}

func migrationSignedShardKey(number int64) (domain.PointShardKey, bool) {
	if number < 0 {
		return domain.PointShardKey{}, false
	}
	return domain.NumberShardKey(uint64(number)), true
}

// ensureShardKey creates a shard key on a custom-sharded target the first time a point needs it.
func (m ExecuteMigration) ensureShardKey(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, key domain.PointShardKey) error {
	if shardKeys == nil || !key.IsSet() || shardKeys[key] {
		return nil
	}
	body := domain.JSON{"shard_key": key.Text}
	if key.Kind == domain.ShardKeyNumber {
		body["shard_key"] = key.Number
	}
	for name, value := range run.spec.Target.ShardKeyParams() {
		body[name] = value
	}
	if err := m.Qdrant.CreateShardKey(ctx, run.target, collection.TargetName, body); err != nil {
		return fmt.Errorf("create shard key %v on %s: %w", body["shard_key"], collection.TargetName, err)
	}
	shardKeys[key] = true
	return nil
}

// writePage upserts a scrolled page: one keyless upsert for an auto-sharded target, one per shard key otherwise.
func (m ExecuteMigration) writePage(ctx context.Context, run migrationRun, collection domain.CollectionMigration, shardKeys migrationShardKeys, points []domain.Point) error {
	groups := []domain.ShardGroup{}
	if shardKeys == nil {
		group := domain.ShardGroup{}
		for _, point := range points {
			group.Points = append(group.Points, point.ToPointStruct())
		}
		groups = append(groups, group)
	}
	if shardKeys != nil {
		routed, err := domain.GroupByShardKey(points, run.spec.Target)
		if err != nil {
			return err
		}
		groups = routed
	}
	for _, group := range groups {
		if len(group.Points) == 0 {
			continue
		}
		if err := m.ensureShardKey(ctx, run, collection, shardKeys, group.Key); err != nil {
			return err
		}
		if err := m.Qdrant.UpsertPoints(ctx, run.target, collection.TargetName, group.Points, group.Key); err != nil {
			return fmt.Errorf("upsert points into %s: %w", collection.TargetName, err)
		}
	}
	return nil
}

// Package fakes holds in-memory implementations of the ports for use case tests.
package fakes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

type SnapshotKey struct {
	NodeURL    string
	Collection string
	Name       string
}

type PointsKey struct {
	NodeURL    string
	Collection string
}

type Recovery struct {
	NodeURL    string
	Collection string
	Location   string
	Priority   domain.RestorePriority
	Checksum   string
}

type CollectionUpdate struct {
	Collection string
	Body       domain.JSON
}

type IndexChange struct {
	Action     string
	Collection string
	Field      string
}

type Upsert struct {
	Collection string
	ShardKey   domain.PointShardKey
	Count      int
}

type Deletion struct {
	Collection string
	IDs        []domain.PointID
}

type ShardKeyBody struct {
	Collection string
	Body       domain.JSON
}

type RemovedPeer struct {
	PeerID uint64
	Force  bool
}

// Qdrant is an in-memory cluster of Qdrant nodes keyed by node URL; Routes maps a Service URL onto the node that answers it.
// Exported fields are for test setup and assertions and are not safe to touch while a method runs concurrently.
type Qdrant struct {
	mu sync.Mutex

	Collections        map[string]map[string]domain.JSON
	Snapshots          map[SnapshotKey][]byte
	SnapshotTimes      map[SnapshotKey]time.Time
	Recovered          []Recovery
	FailingCollections map[string]bool
	Routes             map[string]string
	Aliases            map[string]string
	Updates            []CollectionUpdate
	IndexChanges       []IndexChange
	AliasChanges       [][]domain.JSON
	Points             map[PointsKey]map[domain.PointID]domain.Point
	LatePoints         map[PointsKey][][]domain.Point
	LateDeletes        map[PointsKey][][]domain.PointID
	Upserts            []Upsert
	Deletions          []Deletion
	ShardKeyBodies     []ShardKeyBody
	Peers              map[uint64]string
	NodePeerIDs        map[string]uint64
	Replicas           map[string][]domain.Replica
	ReplicationFactors map[string]int
	SlowTransfers      bool
	// UnreachableAfterUpserts, when set, makes upserts fail as unreachable once that many have succeeded.
	UnreachableAfterUpserts *int
	Pending                 []domain.ShardOperation
	ShardOperations         []domain.ShardOperation
	RemovedPeers            []RemovedPeer
	Created                 int
	RestoreBodies           map[string]domain.JSON
	YellowPolls             map[string]int
	InfoCalls               int
}

var _ ports.Qdrant = (*Qdrant)(nil)

func NewQdrant() *Qdrant {
	return &Qdrant{
		Collections:        map[string]map[string]domain.JSON{},
		Snapshots:          map[SnapshotKey][]byte{},
		SnapshotTimes:      map[SnapshotKey]time.Time{},
		FailingCollections: map[string]bool{},
		Routes:             map[string]string{},
		Aliases:            map[string]string{},
		Points:             map[PointsKey]map[domain.PointID]domain.Point{},
		LatePoints:         map[PointsKey][][]domain.Point{},
		LateDeletes:        map[PointsKey][][]domain.PointID{},
		Peers:              map[uint64]string{},
		NodePeerIDs:        map[string]uint64{},
		Replicas:           map[string][]domain.Replica{},
		ReplicationFactors: map[string]int{},
		RestoreBodies:      map[string]domain.JSON{},
		YellowPolls:        map[string]int{},
	}
}

func SnapshotPayload(nodeURL string, collection string) []byte {
	return bytes.Repeat([]byte(nodeURL+":"+collection+"\n"), 200)
}

var defaultHNSW = domain.JSON{
	"m":                    16,
	"ef_construct":         100,
	"full_scan_threshold":  10000,
	"max_indexing_threads": 0,
	"on_disk":              false,
	"payload_m":            nil,
}

var defaultOptimizers = domain.JSON{
	"deleted_threshold":        0.2,
	"vacuum_min_vector_number": 1000,
	"default_segment_number":   0,
	"max_segment_size":         nil,
	"memmap_threshold":         nil,
	"indexing_threshold":       10000,
	"flush_interval_sec":       5,
	"max_optimization_threads": nil,
}

func orDefault(body domain.JSON, key string, fallback any) any {
	value, ok := body[key]
	if !ok {
		return fallback
	}
	return value
}

func objectOf(value any) domain.JSON {
	object, ok := value.(domain.JSON)
	if !ok || object == nil {
		return domain.JSON{}
	}
	return object
}

// ConfigFromCreate is what GET /collections/{c} reports after PUT with this body, defaults filled like Qdrant.
func ConfigFromCreate(body domain.JSON) domain.JSON {
	return domain.JSON{
		"params": domain.JSON{
			"vectors":                  orDefault(body, "vectors", domain.JSON{}),
			"shard_number":             orDefault(body, "shard_number", 1),
			"sharding_method":          body["sharding_method"],
			"replication_factor":       orDefault(body, "replication_factor", 1),
			"write_consistency_factor": orDefault(body, "write_consistency_factor", 1),
			"read_fan_out_factor":      body["read_fan_out_factor"],
			"on_disk_payload":          orDefault(body, "on_disk_payload", true),
			"sparse_vectors":           body["sparse_vectors"],
		},
		"hnsw_config":         domain.MergeJSON(defaultHNSW, objectOf(body["hnsw_config"])),
		"optimizer_config":    domain.MergeJSON(defaultOptimizers, objectOf(body["optimizers_config"])),
		"wal_config":          domain.JSON{"wal_capacity_mb": 32, "wal_segments_ahead": 0},
		"quantization_config": body["quantization_config"],
		"strict_mode_config":  body["strict_mode_config"],
		"metadata":            body["metadata"],
	}
}

func quantizationAfter(config domain.JSON, body domain.JSON) any {
	value, present := body["quantization_config"]
	if !present {
		return config["quantization_config"]
	}
	if text, ok := value.(string); ok && text == "Disabled" {
		return nil
	}
	return value
}

func nilIfEmpty(value domain.JSON) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

// ApplyUpdate folds a PATCH /collections/{c} body into a reported config the way Qdrant does.
func ApplyUpdate(config domain.JSON, body domain.JSON) domain.JSON {
	params := maps.Clone(objectOf(config["params"]))
	vectors := objectOf(params["vectors"])
	for name, diff := range objectOf(body["vectors"]) {
		if name == "" {
			vectors = domain.MergeJSON(vectors, objectOf(diff))
			continue
		}
		vectors = domain.MergeJSON(vectors, domain.JSON{name: domain.MergeJSON(objectOf(vectors[name]), objectOf(diff))})
	}
	params["vectors"] = vectors
	if sparse, ok := body["sparse_vectors"]; ok {
		params["sparse_vectors"] = domain.MergeJSON(objectOf(params["sparse_vectors"]), objectOf(sparse))
	}
	maps.Copy(params, objectOf(body["params"]))
	optimizers := domain.JSON{}
	for key, value := range objectOf(body["optimizers_config"]) {
		if text, ok := value.(string); ok && text == "auto" {
			value = nil
		}
		optimizers[key] = value
	}
	updated := maps.Clone(config)
	updated["params"] = params
	updated["hnsw_config"] = domain.MergeJSON(objectOf(config["hnsw_config"]), objectOf(body["hnsw_config"]))
	updated["optimizer_config"] = domain.MergeJSON(objectOf(config["optimizer_config"]), optimizers)
	updated["quantization_config"] = quantizationAfter(config, body)
	updated["strict_mode_config"] = nilIfEmpty(domain.MergeJSON(objectOf(config["strict_mode_config"]), objectOf(body["strict_mode_config"])))
	updated["metadata"] = nilIfEmpty(domain.MergeJSON(objectOf(config["metadata"]), objectOf(body["metadata"])))
	return updated
}

func (q *Qdrant) resolve(node domain.QdrantNode) string {
	if routed, ok := q.Routes[node.URL]; ok {
		return routed
	}
	return node.URL
}

func notFound(what string) error {
	return fmt.Errorf("fake qdrant: %w", &ports.QdrantError{Status: http.StatusNotFound, Message: what + " doesn't exist"})
}

func (q *Qdrant) entry(node domain.QdrantNode, collection string) (domain.JSON, error) {
	entry, ok := q.Collections[q.resolve(node)][collection]
	if !ok {
		return nil, notFound("collection " + collection)
	}
	return entry, nil
}

// AddCollection registers a collection on a node URL as a created one would report.
func (q *Qdrant) AddCollection(nodeURL string, collection string, points int, body domain.JSON) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.addCollection(nodeURL, collection, points, body)
}

func (q *Qdrant) addCollection(nodeURL string, collection string, points int, body domain.JSON) {
	if body == nil {
		body = domain.JSON{"vectors": domain.JSON{"size": 4, "distance": "Cosine"}}
	}
	if q.Collections[nodeURL] == nil {
		q.Collections[nodeURL] = map[string]domain.JSON{}
	}
	q.Collections[nodeURL][collection] = domain.JSON{
		"status":                "green",
		"points_count":          points,
		"indexed_vectors_count": points,
		"segments_count":        1,
		"config":                ConfigFromCreate(body),
		"payload_schema":        domain.JSON{},
	}
}

func (q *Qdrant) ListCollections(_ context.Context, node domain.QdrantNode) ([]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	names := slices.Sorted(maps.Keys(q.Collections[q.resolve(node)]))
	return append([]string{}, names...), nil
}

func (q *Qdrant) CreateSnapshot(_ context.Context, node domain.QdrantNode, collection string) (domain.Snapshot, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.FailingCollections[collection] {
		return domain.Snapshot{}, fmt.Errorf("snapshot of %s refused", collection)
	}
	q.Created++
	name := fmt.Sprintf("%s-%d.snapshot", collection, q.Created)
	payload := SnapshotPayload(node.URL, collection)
	q.addSnapshot(node.URL, collection, name, time.Now().UTC(), payload)
	return domain.Snapshot{Name: name, Collection: collection, SizeBytes: int64(len(payload)), Checksum: fmt.Sprintf("sha256:%d", q.Created)}, nil
}

// AddSnapshot registers a snapshot; a nil payload is a small leftover.
func (q *Qdrant) AddSnapshot(nodeURL string, collection string, name string, createdAt time.Time, payload []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if payload == nil {
		payload = []byte("leftover")
	}
	q.addSnapshot(nodeURL, collection, name, createdAt, payload)
}

func (q *Qdrant) addSnapshot(nodeURL string, collection string, name string, createdAt time.Time, payload []byte) {
	key := SnapshotKey{NodeURL: nodeURL, Collection: collection, Name: name}
	q.Snapshots[key] = payload
	q.SnapshotTimes[key] = createdAt
}

func (q *Qdrant) StreamSnapshot(_ context.Context, node domain.QdrantNode, collection string, snapshotName string) (io.ReadCloser, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	payload, ok := q.Snapshots[SnapshotKey{NodeURL: node.URL, Collection: collection, Name: snapshotName}]
	if !ok {
		return nil, notFound("snapshot " + snapshotName)
	}
	return io.NopCloser(bytes.NewReader(payload)), nil
}

func (q *Qdrant) ListSnapshots(_ context.Context, node domain.QdrantNode, collection string) ([]domain.SnapshotInfo, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	snapshots := []domain.SnapshotInfo{}
	for key, payload := range q.Snapshots {
		if key.NodeURL == node.URL && key.Collection == collection {
			snapshots = append(snapshots, domain.SnapshotInfo{Name: key.Name, CreatedAt: q.SnapshotTimes[key], SizeBytes: int64(len(payload))})
		}
	}
	sort.Slice(snapshots, func(a int, b int) bool { return snapshots[a].Name < snapshots[b].Name })
	return snapshots, nil
}

func (q *Qdrant) DeleteSnapshot(_ context.Context, node domain.QdrantNode, collection string, snapshotName string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := SnapshotKey{NodeURL: node.URL, Collection: collection, Name: snapshotName}
	delete(q.Snapshots, key)
	delete(q.SnapshotTimes, key)
	return nil
}

func (q *Qdrant) RecoverSnapshot(_ context.Context, node domain.QdrantNode, collection string, location string, priority domain.RestorePriority, checksum string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Recovered = append(q.Recovered, Recovery{NodeURL: node.URL, Collection: collection, Location: location, Priority: priority, Checksum: checksum})
	q.addCollection(node.URL, collection, 42, q.RestoreBodies[collection])
	return nil
}

// CollectionInfo reports "yellow" for as many calls as YellowPolls holds for the collection.
func (q *Qdrant) CollectionInfo(_ context.Context, node domain.QdrantNode, collection string) (domain.JSON, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.InfoCalls++
	info, err := q.entry(node, collection)
	if err != nil {
		return nil, err
	}
	if q.YellowPolls[collection] <= 0 {
		return maps.Clone(info), nil
	}
	q.YellowPolls[collection]--
	yellow := maps.Clone(info)
	yellow["status"] = "yellow"
	return yellow, nil
}

func (q *Qdrant) Ready(_ context.Context, _ domain.QdrantNode) bool {
	return true
}

func (q *Qdrant) CollectionExists(_ context.Context, node domain.QdrantNode, collection string) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, ok := q.Collections[q.resolve(node)][collection]
	return ok, nil
}

func (q *Qdrant) CreateCollection(_ context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, ok := q.Collections[q.resolve(node)][collection]; ok {
		return fmt.Errorf("fake qdrant: %w", &ports.QdrantError{Status: http.StatusConflict, Message: "collection " + collection + " already exists"})
	}
	q.addCollection(q.resolve(node), collection, 0, body)
	return nil
}

func (q *Qdrant) UpdateCollection(_ context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, err := q.entry(node, collection)
	if err != nil {
		return err
	}
	entry["config"] = ApplyUpdate(objectOf(entry["config"]), body)
	q.Updates = append(q.Updates, CollectionUpdate{Collection: collection, Body: body})
	return nil
}

func (q *Qdrant) DeleteCollection(_ context.Context, node domain.QdrantNode, collection string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.Collections[q.resolve(node)], collection)
	maps.DeleteFunc(q.Aliases, func(_ string, target string) bool { return target == collection })
	return nil
}

func (q *Qdrant) ListAliases(_ context.Context, _ domain.QdrantNode) (map[string]string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return maps.Clone(q.Aliases), nil
}

func (q *Qdrant) UpdateAliases(_ context.Context, _ domain.QdrantNode, actions []domain.JSON) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.AliasChanges = append(q.AliasChanges, actions)
	for _, action := range actions {
		if create, ok := action["create_alias"]; ok {
			spec := objectOf(create)
			alias, _ := spec["alias_name"].(string)
			target, _ := spec["collection_name"].(string)
			q.Aliases[alias] = target
			continue
		}
		alias, _ := objectOf(action["delete_alias"])["alias_name"].(string)
		delete(q.Aliases, alias)
	}
	return nil
}

func (q *Qdrant) CreatePayloadIndex(_ context.Context, node domain.QdrantNode, collection string, fieldName string, fieldSchema any) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, err := q.entry(node, collection)
	if err != nil {
		return err
	}
	schema := domain.JSON{}
	switch typed := fieldSchema.(type) {
	case string:
		schema["type"] = typed
	case domain.JSON:
		schema = maps.Clone(typed)
	default:
		return fmt.Errorf("fake qdrant: field schema %v is neither a type name nor an object", fieldSchema)
	}
	var params any
	if len(schema) > 1 {
		params = schema
	}
	objectOf(entry["payload_schema"])[fieldName] = domain.JSON{"data_type": schema["type"], "params": params, "points": 0}
	q.IndexChanges = append(q.IndexChanges, IndexChange{Action: "create", Collection: collection, Field: fieldName})
	return nil
}

func (q *Qdrant) DeletePayloadIndex(_ context.Context, node domain.QdrantNode, collection string, fieldName string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, err := q.entry(node, collection)
	if err != nil {
		return err
	}
	delete(objectOf(entry["payload_schema"]), fieldName)
	q.IndexChanges = append(q.IndexChanges, IndexChange{Action: "delete", Collection: collection, Field: fieldName})
	return nil
}

func (q *Qdrant) ListShardKeys(_ context.Context, node domain.QdrantNode, collection string) ([]any, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, err := q.entry(node, collection)
	if err != nil {
		return nil, err
	}
	keys, _ := entry["shard_keys"].([]any)
	return append([]any{}, keys...), nil
}

func (q *Qdrant) CreateShardKey(_ context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry, err := q.entry(node, collection)
	if err != nil {
		return err
	}
	keys, _ := entry["shard_keys"].([]any)
	key := body["shard_key"]
	if slices.ContainsFunc(keys, func(existing any) bool { return fmt.Sprint(existing) == fmt.Sprint(key) }) {
		return fmt.Errorf("fake qdrant: %w", &ports.QdrantError{Status: http.StatusConflict, Message: fmt.Sprintf("shard key %v already exists", key)})
	}
	entry["shard_keys"] = append(keys, key)
	q.ShardKeyBodies = append(q.ShardKeyBodies, ShardKeyBody{Collection: collection, Body: body})
	return nil
}

// AddPoints stores points on a node URL by ID and updates the collection's points_count.
func (q *Qdrant) AddPoints(nodeURL string, collection string, points []domain.Point) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.addPoints(nodeURL, collection, points)
}

func (q *Qdrant) addPoints(nodeURL string, collection string, points []domain.Point) {
	key := PointsKey{NodeURL: nodeURL, Collection: collection}
	if q.Points[key] == nil {
		q.Points[key] = map[domain.PointID]domain.Point{}
	}
	for _, point := range points {
		q.Points[key][point.ID] = point
	}
	if entry, ok := q.Collections[nodeURL][collection]; ok {
		entry["points_count"] = len(q.Points[key])
	}
}

// StoredPoints lists a collection's points in scroll order.
func (q *Qdrant) StoredPoints(nodeURL string, collection string) []domain.Point {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sortedPoints(PointsKey{NodeURL: nodeURL, Collection: collection})
}

func (q *Qdrant) sortedPoints(key PointsKey) []domain.Point {
	store := q.Points[key]
	ids := slices.SortedFunc(maps.Keys(store), domain.ComparePointIDs)
	points := make([]domain.Point, 0, len(ids))
	for _, id := range ids {
		points = append(points, store[id])
	}
	return points
}

func (q *Qdrant) CountPoints(_ context.Context, node domain.QdrantNode, collection string, _ bool) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return int64(len(q.Points[PointsKey{NodeURL: q.resolve(node), Collection: collection}])), nil
}

// ScrollPoints pages in scroll order; after the last page it applies the next queued late wave of points and deletions.
func (q *Qdrant) ScrollPoints(_ context.Context, node domain.QdrantNode, collection string, offset domain.PointID, limit int) ([]domain.Point, domain.PointID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	key := PointsKey{NodeURL: q.resolve(node), Collection: collection}
	all := q.sortedPoints(key)
	start := 0
	if !offset.IsZero() {
		start = len(all)
		for index, point := range all {
			if domain.ComparePointIDs(point.ID, offset) >= 0 {
				start = index
				break
			}
		}
	}
	end := min(start+limit, len(all))
	page := append([]domain.Point{}, all[start:end]...)
	if end < len(all) {
		return page, all[end].ID, nil
	}
	if waves := q.LatePoints[key]; len(waves) > 0 {
		q.addPoints(key.NodeURL, collection, waves[0])
		q.LatePoints[key] = waves[1:]
	}
	if removals := q.LateDeletes[key]; len(removals) > 0 {
		q.removePoints(key.NodeURL, collection, removals[0])
		q.LateDeletes[key] = removals[1:]
	}
	return page, domain.PointID{}, nil
}

func (q *Qdrant) removePoints(nodeURL string, collection string, ids []domain.PointID) {
	key := PointsKey{NodeURL: nodeURL, Collection: collection}
	for _, id := range ids {
		delete(q.Points[key], id)
	}
	if entry, ok := q.Collections[nodeURL][collection]; ok {
		entry["points_count"] = len(q.Points[key])
	}
}

func (q *Qdrant) DeletePoints(_ context.Context, node domain.QdrantNode, collection string, ids []domain.PointID) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.Deletions = append(q.Deletions, Deletion{Collection: collection, IDs: slices.Clone(ids)})
	q.removePoints(q.resolve(node), collection, ids)
	return nil
}

func (q *Qdrant) UpsertPoints(_ context.Context, node domain.QdrantNode, collection string, points []domain.Point, shardKey domain.PointShardKey) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.UnreachableAfterUpserts != nil && len(q.Upserts) >= *q.UnreachableAfterUpserts {
		return fmt.Errorf("qdrant at %s is unreachable: %w", node.URL, ports.ErrUnreachable)
	}
	q.Upserts = append(q.Upserts, Upsert{Collection: collection, ShardKey: shardKey, Count: len(points)})
	stored := make([]domain.Point, 0, len(points))
	for _, point := range points {
		if shardKey.IsSet() {
			encoded, err := json.Marshal(shardKey)
			if err != nil {
				return fmt.Errorf("encode shard key: %w", err)
			}
			point.ShardKey = encoded
		}
		stored = append(stored, point)
	}
	q.addPoints(q.resolve(node), collection, stored)
	return nil
}

func (q *Qdrant) ClusterTopology(_ context.Context, node domain.QdrantNode) (domain.ClusterTopology, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	own, ok := q.NodePeerIDs[q.resolve(node)]
	if !ok {
		return domain.ClusterTopology{}, fmt.Errorf("qdrant at %s is unreachable: %w", node.URL, ports.ErrUnreachable)
	}
	peers := []domain.Peer{}
	for _, id := range slices.Sorted(maps.Keys(q.Peers)) {
		peers = append(peers, domain.Peer{PeerID: id, URI: q.Peers[id]})
	}
	return domain.ClusterTopology{PeerID: own, Peers: peers}, nil
}

func (q *Qdrant) CollectionShards(_ context.Context, _ domain.QdrantNode, collection string) (domain.CollectionShards, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	replicationFactor, ok := q.ReplicationFactors[collection]
	if !ok {
		replicationFactor = 1
	}
	transfers := 0
	for _, operation := range q.Pending {
		if operation.Collection == collection {
			transfers++
		}
	}
	return domain.CollectionShards{
		Collection:        collection,
		Replicas:          slices.Clone(q.Replicas[collection]),
		ReplicationFactor: replicationFactor,
		Transfers:         transfers,
	}, nil
}

// ApplyShardOperation runs the operation at once, or queues a move or replicate until Settle when SlowTransfers is set.
func (q *Qdrant) ApplyShardOperation(_ context.Context, _ domain.QdrantNode, operation domain.ShardOperation) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ShardOperations = append(q.ShardOperations, operation)
	if q.SlowTransfers && operation.Kind != domain.ShardDrop {
		q.Pending = append(q.Pending, operation)
		return nil
	}
	return q.execute(operation)
}

func (q *Qdrant) execute(operation domain.ShardOperation) error {
	replicas := q.Replicas[operation.Collection]
	index := slices.IndexFunc(replicas, func(r domain.Replica) bool {
		return r.ShardID == operation.ShardID && r.PeerID == operation.PeerID
	})
	if index < 0 {
		return notFound(fmt.Sprintf("replica %s", operation.Describe()))
	}
	if operation.Kind == domain.ShardDrop {
		q.Replicas[operation.Collection] = slices.Delete(slices.Clone(replicas), index, index+1)
		return nil
	}
	if operation.TargetPeerID == nil {
		return errors.New("fake qdrant: transfer without a target peer")
	}
	kept := slices.Clone(replicas)
	if operation.Kind == domain.ShardMove {
		kept = slices.Delete(kept, index, index+1)
	}
	q.Replicas[operation.Collection] = append(kept, domain.Replica{ShardID: operation.ShardID, PeerID: *operation.TargetPeerID, State: domain.ShardActive})
	return nil
}

// Settle finishes every transfer that SlowTransfers held back.
func (q *Qdrant) Settle() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	pending := q.Pending
	q.Pending = nil
	for _, operation := range pending {
		if err := q.execute(operation); err != nil {
			return err
		}
	}
	return nil
}

func (q *Qdrant) RemovePeer(_ context.Context, _ domain.QdrantNode, peerID uint64, force bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	holding := 0
	for _, replicas := range q.Replicas {
		for _, replica := range replicas {
			if replica.PeerID == peerID {
				holding++
			}
		}
	}
	if holding > 0 && !force {
		return fmt.Errorf("fake qdrant: %w", &ports.QdrantError{Status: http.StatusBadRequest, Message: fmt.Sprintf("peer %d still holds %d shard replicas", peerID, holding)})
	}
	for collection, replicas := range q.Replicas {
		q.Replicas[collection] = slices.DeleteFunc(slices.Clone(replicas), func(r domain.Replica) bool { return r.PeerID == peerID })
	}
	delete(q.Peers, peerID)
	q.RemovedPeers = append(q.RemovedPeers, RemovedPeer{PeerID: peerID, Force: force})
	return nil
}

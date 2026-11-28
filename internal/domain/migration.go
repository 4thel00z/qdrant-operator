package domain

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

type MigrationPhase string

const (
	MigrationPending   MigrationPhase = "Pending"
	MigrationRunning   MigrationPhase = "Running"
	MigrationCompleted MigrationPhase = "Completed"
	MigrationFailed    MigrationPhase = "Failed"
)

// MigrationCreateOnlyParams can only be chosen when a collection is created.
var MigrationCreateOnlyParams = []string{"shard_number", "replication_factor", "write_consistency_factor"}

// EndpointSpec is a Qdrant reachable by URL that this operator does not manage.
type EndpointSpec struct {
	URL       string
	APIKeyRef *SecretRef
	CARef     *SecretRef
}

func NewEndpointSpec(url string, apiKeyRef *SecretRef, caRef *SecretRef) EndpointSpec {
	return EndpointSpec{URL: strings.TrimRight(url, "/"), APIKeyRef: apiKeyRef, CARef: caRef}
}

type MigrationSource struct {
	ClusterRef *ClusterRef
	Endpoint   *EndpointSpec
}

func (s MigrationSource) Validate() error {
	if (s.ClusterRef != nil) == (s.Endpoint != nil) {
		return fmt.Errorf("QdrantMigration source needs exactly one of clusterRef or endpoint")
	}
	return nil
}

func (s MigrationSource) Description() string {
	if s.ClusterRef != nil {
		return s.ClusterRef.Namespace + "/" + s.ClusterRef.Name
	}
	if s.Endpoint != nil {
		return s.Endpoint.URL
	}
	return ""
}

// TargetOverrides replaces the source's sharding and routing on the target; zero counts keep the target's defaults.
type TargetOverrides struct {
	ShardNumber            int
	ReplicationFactor      int
	WriteConsistencyFactor int
	ShardKey               PointShardKey
	ShardKeyField          string
}

func (o TargetOverrides) Validate() error {
	if o.ShardKey.IsSet() && o.ShardKeyField != "" {
		return fmt.Errorf("target.shardKey and target.shardKeyField are mutually exclusive")
	}
	return nil
}

// ShardKeyParams is the body of PUT /collections/{name}/shards for a key the migration creates.
func (o TargetOverrides) ShardKeyParams() JSON {
	params := JSON{}
	if o.ShardNumber != 0 {
		params["shards_number"] = o.ShardNumber
	}
	if o.ReplicationFactor != 0 {
		params["replication_factor"] = o.ReplicationFactor
	}
	return params
}

func (o TargetOverrides) ToParams() JSON {
	params := JSON{}
	if o.ShardNumber != 0 {
		params["shard_number"] = o.ShardNumber
	}
	if o.ReplicationFactor != 0 {
		params["replication_factor"] = o.ReplicationFactor
	}
	if o.WriteConsistencyFactor != 0 {
		params["write_consistency_factor"] = o.WriteConsistencyFactor
	}
	return params
}

// Route returns the shard key a source point lands under on the target.
func (o TargetOverrides) Route(point Point) (PointShardKey, error) {
	if o.ShardKeyField != "" {
		payload, err := decodeNumbers(point.Payload)
		if err != nil {
			return PointShardKey{}, fmt.Errorf("point %s payload: %w", point.ID, err)
		}
		fields, _ := payload.(map[string]any)
		key, ok := shardKeyFromValue(fields[o.ShardKeyField])
		if !ok || !key.IsSet() {
			return PointShardKey{}, fmt.Errorf("point %s has no string or non-negative integer '%s' payload field to route by", point.ID, o.ShardKeyField)
		}
		return key, nil
	}
	if o.ShardKey.IsSet() {
		return o.ShardKey, nil
	}
	recorded, err := decodeNumbers(point.ShardKey)
	if err != nil {
		return PointShardKey{}, fmt.Errorf("point %s shard_key: %w", point.ID, err)
	}
	key, ok := shardKeyFromValue(recorded)
	if !ok {
		return PointShardKey{}, fmt.Errorf("point %s has a shard_key that is neither a string nor a non-negative integer", point.ID)
	}
	return key, nil
}

type MigrationSpec struct {
	Name                 string
	Namespace            string
	Source               MigrationSource
	TargetClusterRef     ClusterRef
	Collections          []string
	CollectionMapping    map[string]string
	BatchSize            int
	BatchDelay           time.Duration
	CheckpointBatches    int
	VerifyPasses         int
	Sync                 bool
	SyncMaxPasses        int
	CreateMissing        bool
	EnsurePayloadIndexes bool
	Target               TargetOverrides
}

func DefaultMigrationSpec(name string, namespace string, source MigrationSource, targetClusterRef ClusterRef) MigrationSpec {
	return MigrationSpec{
		Name:                 name,
		Namespace:            namespace,
		Source:               source,
		TargetClusterRef:     targetClusterRef,
		CollectionMapping:    map[string]string{},
		BatchSize:            256,
		CheckpointBatches:    20,
		VerifyPasses:         1,
		SyncMaxPasses:        1,
		CreateMissing:        true,
		EnsurePayloadIndexes: true,
	}
}

func (s MigrationSpec) Validate() error {
	if err := s.Source.Validate(); err != nil {
		return err
	}
	if err := s.Target.Validate(); err != nil {
		return err
	}
	if s.SyncMaxPasses < 1 {
		return fmt.Errorf("syncMaxPasses must be at least 1")
	}
	return nil
}

func (s MigrationSpec) TargetName(collection string) string {
	if mapped, ok := s.CollectionMapping[collection]; ok {
		return mapped
	}
	return collection
}

// SelectCollections returns the requested collections, or every available one; a request for a missing one is an error.
func (s MigrationSpec) SelectCollections(available []string) ([]string, error) {
	if len(s.Collections) == 0 {
		return available, nil
	}
	missing := []string{}
	for _, name := range s.Collections {
		if !slices.Contains(available, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("collections not present in source: %s", strings.Join(missing, ", "))
	}
	return s.Collections, nil
}

// CreateBodyFromConfig turns a source collection's reported config into a CreateCollection body for the target.
// Shard and replication counts stay with the target cluster unless overridden.
func CreateBodyFromConfig(config JSON, overrides TargetOverrides) JSON {
	params, _ := config["params"].(JSON)
	body := JSON{}
	sources := []struct {
		key    string
		values JSON
		field  string
	}{
		{"vectors", params, "vectors"},
		{"sparse_vectors", params, "sparse_vectors"},
		{"sharding_method", params, "sharding_method"},
		{"on_disk_payload", params, "on_disk_payload"},
		{"hnsw_config", config, "hnsw_config"},
		{"optimizers_config", config, "optimizer_config"},
		{"wal_config", config, "wal_config"},
		{"quantization_config", config, "quantization_config"},
		{"strict_mode_config", config, "strict_mode_config"},
	}
	for _, source := range sources {
		if value := source.values[source.field]; value != nil {
			body[source.key] = value
		}
	}
	maps.Copy(body, overrides.ToParams())
	return body
}

// PayloadIndex re-creates one payload index; Schema is the index params or, when absent, the bare data type.
type PayloadIndex struct {
	Field  string
	Schema any
}

// PayloadIndexesFromSchema lists a source collection's payload indexes ordered by field name.
func PayloadIndexesFromSchema(payloadSchema JSON) ([]PayloadIndex, error) {
	indexes := make([]PayloadIndex, 0, len(payloadSchema))
	for _, field := range slices.Sorted(maps.Keys(payloadSchema)) {
		entry, ok := payloadSchema[field].(JSON)
		if !ok {
			return nil, fmt.Errorf("payload schema entry %q is not an object", field)
		}
		if params := entry["params"]; isTruthy(params) {
			indexes = append(indexes, PayloadIndex{Field: field, Schema: params})
			continue
		}
		dataType, present := entry["data_type"]
		if !present {
			return nil, fmt.Errorf("payload schema entry %q has no data_type", field)
		}
		indexes = append(indexes, PayloadIndex{Field: field, Schema: dataType})
	}
	return indexes, nil
}

// VectorLayout is the size and distance of every vector in a live config, the part two collections must share.
func VectorLayout(config JSON) (JSON, error) {
	params, _ := config["params"].(JSON)
	vectors, _ := params["vectors"].(JSON)
	if _, single := vectors["size"]; single {
		return vectorShape(vectors)
	}
	layout := JSON{}
	for name, entry := range vectors {
		named, ok := entry.(JSON)
		if !ok {
			return nil, fmt.Errorf("vector %q is not an object", name)
		}
		shape, err := vectorShape(named)
		if err != nil {
			return nil, fmt.Errorf("vector %q: %w", name, err)
		}
		layout[name] = shape
	}
	return layout, nil
}

func vectorShape(params JSON) (JSON, error) {
	size, hasSize := params["size"]
	distance, hasDistance := params["distance"]
	if !hasSize || !hasDistance {
		return nil, fmt.Errorf("vector params need size and distance")
	}
	return JSON{"size": size, "distance": distance}, nil
}

// ShardGroup is the points of one scrolled page that share a target shard key.
type ShardGroup struct {
	Key    PointShardKey
	Points []Point
}

// GroupByShardKey splits a scrolled page per target shard key, since an upsert carries one; groups keep first-seen order.
func GroupByShardKey(points []Point, target TargetOverrides) ([]ShardGroup, error) {
	groups := []ShardGroup{}
	positions := map[PointShardKey]int{}
	for _, point := range points {
		key, err := target.Route(point)
		if err != nil {
			return nil, err
		}
		position, seen := positions[key]
		if !seen {
			position = len(groups)
			positions[key] = position
			groups = append(groups, ShardGroup{Key: key})
		}
		groups[position].Points = append(groups[position].Points, point.ToPointStruct())
	}
	return groups, nil
}

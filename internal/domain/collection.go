package domain

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

type CollectionPhase string

const (
	CollectionPending  CollectionPhase = "Pending"
	CollectionReady    CollectionPhase = "Ready"
	CollectionDegraded CollectionPhase = "Degraded"
	CollectionFailed   CollectionPhase = "Failed"
)

// ParseCollectionPhase reads a phase name; empty means Pending.
func ParseCollectionPhase(value string) (CollectionPhase, error) {
	phase := CollectionPhase(value)
	switch phase {
	case "":
		return CollectionPending, nil
	case CollectionPending, CollectionReady, CollectionDegraded, CollectionFailed:
		return phase, nil
	}
	return "", fmt.Errorf("unknown collection phase %q", value)
}

type DeletionPolicy string

const (
	DeletionRetain DeletionPolicy = "Retain"
	DeletionDelete DeletionPolicy = "Delete"
)

// ParseDeletionPolicy reads a deletion policy; empty means Retain.
func ParseDeletionPolicy(value string) (DeletionPolicy, error) {
	policy := DeletionPolicy(value)
	switch policy {
	case "":
		return DeletionRetain, nil
	case DeletionRetain, DeletionDelete:
		return policy, nil
	}
	return "", fmt.Errorf("unknown deletion policy %q", value)
}

// UnnamedVector is the key of Qdrant's single unnamed vector.
const UnnamedVector = ""

const ShardingCustom = "custom"

// ResetDefault is the value to PATCH a dropped field back to, and the value the live config reports at default.
type ResetDefault struct {
	Patch any
	Live  any
}

// ResetGroup is a PATCH-able config block and the values Qdrant falls back to when the spec drops a field.
// Fields Qdrant cannot unset (a null diff means "unchanged") are left out of Defaults.
type ResetGroup struct {
	Name     string
	PatchKey string
	LiveKey  string
	Defaults map[string]ResetDefault
}

var ResetGroups = []ResetGroup{
	{
		Name:     "optimizers",
		PatchKey: "optimizers_config",
		LiveKey:  "optimizer_config",
		Defaults: map[string]ResetDefault{
			"deleted_threshold":        {Patch: 0.2, Live: 0.2},
			"vacuum_min_vector_number": {Patch: 1000, Live: 1000},
			"default_segment_number":   {Patch: 0, Live: 0},
			"indexing_threshold":       {Patch: 10000, Live: 10000},
			"flush_interval_sec":       {Patch: 5, Live: 5},
			"max_optimization_threads": {Patch: "auto", Live: nil},
		},
	},
	{
		Name:     "hnsw",
		PatchKey: "hnsw_config",
		LiveKey:  "hnsw_config",
		Defaults: map[string]ResetDefault{
			"m":                    {Patch: 16, Live: 16},
			"ef_construct":         {Patch: 100, Live: 100},
			"full_scan_threshold":  {Patch: 10000, Live: 10000},
			"max_indexing_threads": {Patch: 0, Live: 0},
			"on_disk":              {Patch: false, Live: false},
		},
	},
	{
		Name:     "params",
		PatchKey: "params",
		LiveKey:  "params",
		Defaults: map[string]ResetDefault{
			"replication_factor":       {Patch: 1, Live: 1},
			"write_consistency_factor": {Patch: 1, Live: 1},
			"on_disk_payload":          {Patch: true, Live: true},
		},
	},
	{
		Name:     "quantization",
		PatchKey: "quantization_config",
		LiveKey:  "quantization_config",
		Defaults: map[string]ResetDefault{"quantization": {Patch: "Disabled", Live: nil}},
	},
	{
		Name:     "strict_mode",
		PatchKey: "strict_mode_config",
		LiveKey:  "strict_mode_config",
		Defaults: map[string]ResetDefault{"enabled": {Patch: false, Live: false}},
	},
}

// VectorSpec is one dense vector space; an empty Name is Qdrant's single unnamed vector.
type VectorSpec struct {
	Size         int
	Distance     string
	Name         string
	OnDisk       *bool
	Memory       string
	Datatype     string
	Multivector  JSON
	HNSW         JSON
	Quantization JSON
}

func (v VectorSpec) Key() string {
	return v.Name
}

func (v VectorSpec) ImmutableParams() JSON {
	params := JSON{"size": v.Size, "distance": v.Distance}
	putString(params, "datatype", v.Datatype)
	putJSON(params, "multivector_config", v.Multivector)
	return params
}

func (v VectorSpec) MutableParams() JSON {
	params := JSON{}
	putBool(params, "on_disk", v.OnDisk)
	putString(params, "memory", v.Memory)
	putJSON(params, "hnsw_config", v.HNSW)
	putJSON(params, "quantization_config", v.Quantization)
	return params
}

func (v VectorSpec) ToParams() JSON {
	return MergeJSON(v.ImmutableParams(), v.MutableParams())
}

type SparseVectorSpec struct {
	Name     string
	Modifier string
	Index    JSON
}

func (s SparseVectorSpec) ToParams() JSON {
	params := JSON{}
	putString(params, "modifier", s.Modifier)
	putJSON(params, "index", s.Index)
	return params
}

type PayloadIndexSpec struct {
	Field  string
	Type   string
	Params JSON
}

// FieldSchema is the field_schema of the create-index request: the bare type, or the type with its parameters.
func (p PayloadIndexSpec) FieldSchema() any {
	if len(p.Params) == 0 {
		return p.Type
	}
	schema := JSON{"type": p.Type}
	maps.Copy(schema, p.Params)
	return schema
}

// SatisfiedBy compares against one entry of Qdrant's payload_schema (data_type, params, points).
func (p PayloadIndexSpec) SatisfiedBy(actual JSON) bool {
	if actual["data_type"] != p.Type {
		return false
	}
	return IsSubset(p.Params, jsonObject(actual["params"]))
}

// ShardKeySpec names a shard key to create; Key is a string or an int.
type ShardKeySpec struct {
	Key               any
	ShardsNumber      *int
	ReplicationFactor *int
}

// Body is the body of PUT /collections/{name}/shards.
func (s ShardKeySpec) Body() JSON {
	body := JSON{"shard_key": s.Key}
	putInt(body, "shards_number", s.ShardsNumber)
	putInt(body, "replication_factor", s.ReplicationFactor)
	return body
}

type CollectionSpec struct {
	Name                   string
	Namespace              string
	ClusterRef             ClusterRef
	CollectionName         string
	Vectors                []VectorSpec
	SparseVectors          []SparseVectorSpec
	ShardNumber            *int
	ShardingMethod         string
	ShardKeys              []ShardKeySpec
	ReplicationFactor      *int
	WriteConsistencyFactor *int
	ReadFanOutFactor       *int
	ReadFanOutDelayMs      *int
	OnDiskPayload          *bool
	PayloadMemory          string
	Metadata               JSON
	HNSW                   JSON
	Optimizers             JSON
	Quantization           JSON
	WAL                    JSON
	StrictMode             JSON
	PayloadIndexes         []PayloadIndexSpec
	Aliases                []string
	DeletionPolicy         DeletionPolicy
}

func (c CollectionSpec) Validate() error {
	if len(c.Vectors) == 0 && len(c.SparseVectors) == 0 {
		return errors.New("QdrantCollection needs spec.vectors or spec.sparseVectors")
	}
	if len(c.Vectors) > 1 && slices.ContainsFunc(c.Vectors, func(v VectorSpec) bool { return v.Name == "" }) {
		return errors.New("an unnamed vector must be the only vector")
	}
	return nil
}

func (c CollectionSpec) SingleUnnamed() bool {
	return len(c.Vectors) == 1 && c.Vectors[0].Name == ""
}

// VectorsConfig is Qdrant's VectorsConfig: bare params for the unnamed vector, else a name-to-params map.
func (c CollectionSpec) VectorsConfig(params func(VectorSpec) JSON) JSON {
	if len(c.Vectors) == 0 {
		return nil
	}
	if c.SingleUnnamed() {
		return params(c.Vectors[0])
	}
	config := JSON{}
	for _, vector := range c.Vectors {
		putJSON(config, vector.Key(), params(vector))
	}
	return config
}

func (c CollectionSpec) SparseConfig() JSON {
	if len(c.SparseVectors) == 0 {
		return nil
	}
	config := JSON{}
	for _, vector := range c.SparseVectors {
		config[vector.Name] = vector.ToParams()
	}
	return config
}

func (c CollectionSpec) CollectionParams() JSON {
	params := JSON{}
	putInt(params, "replication_factor", c.ReplicationFactor)
	putInt(params, "write_consistency_factor", c.WriteConsistencyFactor)
	putInt(params, "read_fan_out_factor", c.ReadFanOutFactor)
	putInt(params, "read_fan_out_delay_ms", c.ReadFanOutDelayMs)
	putBool(params, "on_disk_payload", c.OnDiskPayload)
	if c.PayloadMemory != "" {
		params["payload"] = JSON{"memory": c.PayloadMemory}
	}
	return params
}

// CreateBody is the body of PUT /collections/{name}.
func (c CollectionSpec) CreateBody() JSON {
	body := c.CollectionParams()
	putJSON(body, "vectors", c.VectorsConfig(VectorSpec.ToParams))
	putJSON(body, "sparse_vectors", c.SparseConfig())
	putInt(body, "shard_number", c.ShardNumber)
	putString(body, "sharding_method", c.ShardingMethod)
	putJSON(body, "hnsw_config", c.HNSW)
	putJSON(body, "optimizers_config", c.Optimizers)
	putJSON(body, "quantization_config", c.Quantization)
	putJSON(body, "wal_config", c.WAL)
	putJSON(body, "strict_mode_config", c.StrictMode)
	putJSON(body, "metadata", c.Metadata)
	return body
}

// DeclaredFields lists, per resettable group, the fields the spec sets; the status records them once applied.
func (c CollectionSpec) DeclaredFields() map[string][]string {
	groups := map[string]JSON{
		"optimizers":   dropNil(c.Optimizers),
		"hnsw":         dropNil(c.HNSW),
		"params":       c.CollectionParams(),
		"quantization": {},
		"strict_mode":  dropNil(c.StrictMode),
	}
	if len(c.Quantization) > 0 {
		groups["quantization"] = JSON{"quantization": true}
	}
	declared := map[string][]string{}
	for group, fields := range groups {
		if len(fields) == 0 {
			continue
		}
		declared[group] = slices.Sorted(maps.Keys(fields))
	}
	return declared
}

// Resets lists the fields applied earlier, no longer declared and off Qdrant's default, by PATCH key.
func (c CollectionSpec) Resets(config JSON, applied map[string][]string) map[string]JSON {
	declared := c.DeclaredFields()
	resets := map[string]JSON{}
	for _, group := range ResetGroups {
		live := jsonObject(config[group.LiveKey])
		for _, name := range applied[group.Name] {
			fallback, known := group.Defaults[name]
			if !known || slices.Contains(declared[group.Name], name) {
				continue
			}
			if group.Name == "quantization" && !jsonTruthy(config[group.LiveKey]) {
				continue
			}
			if group.Name != "quantization" && AtDefault(live[name], fallback.Live) {
				continue
			}
			if resets[group.PatchKey] == nil {
				resets[group.PatchKey] = JSON{}
			}
			resets[group.PatchKey][name] = fallback.Patch
		}
	}
	return resets
}

// ResetNames lists "patch_key.field" for each reset, in group order, for the status message.
func ResetNames(resets map[string]JSON) []string {
	names := []string{}
	for _, group := range ResetGroups {
		for _, name := range slices.Sorted(maps.Keys(resets[group.PatchKey])) {
			names = append(names, group.PatchKey+"."+name)
		}
	}
	return names
}

// UpdateBody is the body of PATCH /collections/{name}: only the declared blocks the live config misses.
// Sending a block Qdrant already satisfies is not free, since optimizers_config blocks until running
// optimizations finish, so an unchanged block is left out. resets adds the default values of fields
// the spec stopped declaring.
func (c CollectionSpec) UpdateBody(config JSON, resets map[string]JSON) JSON {
	liveParams := jsonObject(config["params"])
	var liveOptimizers any
	if optimizers := LiveOptimizers(config); optimizers != nil {
		liveOptimizers = optimizers
	}
	blocks := []struct {
		key     string
		desired JSON
		live    any
	}{
		{"vectors", c.VectorsConfig(VectorSpec.MutableParams), liveParams["vectors"]},
		{"sparse_vectors", c.SparseConfig(), liveParams["sparse_vectors"]},
		{"params", c.CollectionParams(), liveParams},
		{"hnsw_config", c.HNSW, config["hnsw_config"]},
		{"optimizers_config", c.Optimizers, liveOptimizers},
		{"quantization_config", c.Quantization, config["quantization_config"]},
		{"strict_mode_config", c.StrictMode, config["strict_mode_config"]},
		{"metadata", c.Metadata, config["metadata"]},
	}
	body := JSON{}
	for _, block := range blocks {
		if len(block.desired) == 0 || IsSubset(block.desired, block.live) {
			continue
		}
		body[block.key] = block.desired
	}
	if _, drifted := body["vectors"]; drifted {
		vectors := JSON{}
		for _, vector := range c.Vectors {
			putJSON(vectors, vector.Key(), vector.MutableParams())
		}
		body["vectors"] = vectors
	}
	for key, fields := range resets {
		if key == "quantization_config" {
			body[key] = fields["quantization"]
			continue
		}
		body[key] = MergeJSON(fields, jsonObject(body[key]))
	}
	return body
}

// ImmutableConfig is the part of GET /collections/{name} config that only a re-creation can change.
func (c CollectionSpec) ImmutableConfig() JSON {
	params := JSON{}
	putJSON(params, "vectors", c.VectorsConfig(VectorSpec.ImmutableParams))
	putInt(params, "shard_number", c.ShardNumber)
	putString(params, "sharding_method", c.ShardingMethod)
	return JSON{"params": params}
}

// MutableConfig is the part of config the operator keeps in line with PATCH; note optimizer_config.
func (c CollectionSpec) MutableConfig() JSON {
	params := c.CollectionParams()
	putJSON(params, "vectors", c.VectorsConfig(VectorSpec.MutableParams))
	putJSON(params, "sparse_vectors", c.SparseConfig())
	config := JSON{}
	putJSON(config, "params", params)
	putJSON(config, "hnsw_config", c.HNSW)
	putJSON(config, "optimizer_config", c.Optimizers)
	putJSON(config, "quantization_config", c.Quantization)
	putJSON(config, "strict_mode_config", c.StrictMode)
	putJSON(config, "metadata", c.Metadata)
	return config
}

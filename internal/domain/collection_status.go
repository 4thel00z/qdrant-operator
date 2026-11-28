package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	ReasonImmutableFieldMismatch = "ImmutableFieldMismatch"
	ReasonCollectionReady        = "CollectionReady"
	ConditionReady               = "Ready"
	ImmutableMismatchMessage     = "vectors, shardNumber or shardingMethod differ from the live collection; " +
		"only re-creating the collection can change them"
)

type CollectionStatus struct {
	Phase               CollectionPhase
	CollectionName      string
	Health              string
	PointsCount         *int64
	IndexedVectorsCount *int64
	SegmentsCount       *int64
	PayloadIndexes      []string
	Aliases             []string
	ShardKeys           []any
	Error               string
	ObservedGeneration  *int64
	Conditions          []Condition
	AppliedFields       map[string][]string
}

// CollectionReadyMessage is the Ready condition message once the collection matches the spec.
func CollectionReadyMessage(name string, resetNames []string) string {
	message := fmt.Sprintf("Collection %s matches the spec", name)
	if len(resetNames) == 0 {
		return message
	}
	return message + "; reset to Qdrant defaults: " + strings.Join(resetNames, ", ")
}

// Observe is the status after looking at the live collection info: counts and health come from info,
// the managed lists and applied fields carry over from current, and the Ready condition follows the phase.
func (c CollectionSpec) Observe(
	current CollectionStatus,
	info JSON,
	generation int64,
	phase CollectionPhase,
	reason string,
	message string,
	now time.Time,
) CollectionStatus {
	condition := Condition{
		Type:               ConditionReady,
		Status:             ConditionFalse,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}
	if phase == CollectionReady {
		condition.Status = ConditionTrue
	}
	status := CollectionStatus{
		Phase:               phase,
		CollectionName:      c.CollectionName,
		PointsCount:         jsonInt64(info["points_count"]),
		IndexedVectorsCount: jsonInt64(info["indexed_vectors_count"]),
		SegmentsCount:       jsonInt64(info["segments_count"]),
		PayloadIndexes:      current.PayloadIndexes,
		Aliases:             current.Aliases,
		AppliedFields:       current.AppliedFields,
		Conditions:          SetCondition(current.Conditions, condition),
	}
	if health, ok := info["status"].(string); ok {
		status.Health = health
	}
	if generation != 0 {
		status.ObservedGeneration = &generation
	}
	if phase == CollectionReady {
		return status
	}
	status.Error = message
	return status
}

type IndexOperation struct {
	Delete bool
	Field  string
	Schema any
}

type IndexPlan struct {
	Operations []IndexOperation
	Managed    []string
}

// IndexPlan creates or replaces declared indexes that the live payload schema does not satisfy and drops
// only managed indexes the spec no longer declares. Managed is the sorted declared fields for the status.
func (c CollectionSpec) IndexPlan(payloadSchema JSON, managed []string) IndexPlan {
	fields := []string{}
	desired := map[string]PayloadIndexSpec{}
	for _, index := range c.PayloadIndexes {
		if _, seen := desired[index.Field]; !seen {
			fields = append(fields, index.Field)
		}
		desired[index.Field] = index
	}
	plan := IndexPlan{Operations: []IndexOperation{}}
	for _, field := range fields {
		index := desired[field]
		actual, present := payloadSchema[field].(JSON)
		if present && len(actual) > 0 && index.SatisfiedBy(actual) {
			continue
		}
		if present && len(actual) > 0 {
			plan.Operations = append(plan.Operations, IndexOperation{Delete: true, Field: field})
		}
		plan.Operations = append(plan.Operations, IndexOperation{Field: field, Schema: index.FieldSchema()})
	}
	for _, field := range managed {
		_, declared := desired[field]
		_, live := payloadSchema[field]
		if declared || !live {
			continue
		}
		plan.Operations = append(plan.Operations, IndexOperation{Delete: true, Field: field})
	}
	plan.Managed = append([]string{}, fields...)
	slices.Sort(plan.Managed)
	return plan
}

// AliasActions points declared aliases at this collection and drops only managed aliases the spec no longer declares.
func (c CollectionSpec) AliasActions(actual map[string]string, managed []string) []JSON {
	actions := []JSON{}
	for _, alias := range c.Aliases {
		if target, present := actual[alias]; present && target == c.CollectionName {
			continue
		}
		actions = append(actions, JSON{"create_alias": JSON{"collection_name": c.CollectionName, "alias_name": alias}})
	}
	for _, alias := range managed {
		if slices.Contains(c.Aliases, alias) || actual[alias] != c.CollectionName {
			continue
		}
		actions = append(actions, JSON{"delete_alias": JSON{"alias_name": alias}})
	}
	return actions
}

// AliasNames lists the declared aliases in sorted order, as recorded in the status.
func (c CollectionSpec) AliasNames() []string {
	names := append([]string{}, c.Aliases...)
	slices.Sort(names)
	return names
}

func (c CollectionSpec) ManagesShardKeys() bool {
	return c.ShardingMethod == ShardingCustom
}

type ShardKeyPlan struct {
	Create []ShardKeySpec
	Keys   []any
}

// ShardKeyPlan creates declared shard keys that do not exist yet; it never drops one, since that deletes its points.
func (c CollectionSpec) ShardKeyPlan(existing []any) ShardKeyPlan {
	plan := ShardKeyPlan{Create: []ShardKeySpec{}, Keys: []any{}}
	if !c.ManagesShardKeys() {
		return plan
	}
	plan.Keys = append(plan.Keys, existing...)
	for _, shardKey := range c.ShardKeys {
		if slices.ContainsFunc(plan.Keys, func(key any) bool { return jsonEqual(shardKey.Key, key) }) {
			continue
		}
		plan.Create = append(plan.Create, shardKey)
		plan.Keys = append(plan.Keys, shardKey.Key)
	}
	return plan
}

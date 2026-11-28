package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

// ReconcileCollection makes the Qdrant collection, its payload indexes and aliases match the spec.
// It runs on every spec change and on a timer, so a hand-deleted collection or index comes back.
// Only fields the spec declares are compared; a field applied earlier and since dropped goes back
// to Qdrant's default, the rest is left alone.
type ReconcileCollection struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
	Clock      Clock
}

// Execute returns the new status. An immutable mismatch is reported as a Degraded status, not an error;
// a cluster that is missing or not ready is a retryable error. generation is zero when unknown.
func (r ReconcileCollection) Execute(ctx context.Context, spec domain.CollectionSpec, generation int64, current domain.CollectionStatus) (domain.CollectionStatus, error) {
	connection, err := ResolveCluster{Kubernetes: r.Kubernetes}.Execute(ctx, spec.ClusterRef)
	if err != nil {
		return domain.CollectionStatus{}, err
	}
	node := connection.Service
	if !r.Qdrant.Ready(ctx, node) {
		return domain.CollectionStatus{}, fmt.Errorf("QdrantCluster %s: %w", spec.ClusterRef.Name, ErrClusterNotReady)
	}

	name := spec.CollectionName
	exists, err := r.Qdrant.CollectionExists(ctx, node, name)
	if err != nil {
		return domain.CollectionStatus{}, fmt.Errorf("check collection %s: %w", name, err)
	}
	if !exists {
		if err := r.Qdrant.CreateCollection(ctx, node, name, spec.CreateBody()); err != nil {
			return domain.CollectionStatus{}, fmt.Errorf("create collection %s: %w", name, err)
		}
	}
	info, err := r.Qdrant.CollectionInfo(ctx, node, name)
	if err != nil {
		return domain.CollectionStatus{}, fmt.Errorf("read collection %s: %w", name, err)
	}
	config, _ := info["config"].(domain.JSON)
	now := r.now()

	if !domain.IsSubset(spec.ImmutableConfig(), config) {
		return spec.Observe(current, info, generation, domain.CollectionDegraded, domain.ReasonImmutableFieldMismatch, domain.ImmutableMismatchMessage, now), nil
	}

	resets := spec.Resets(config, current.AppliedFields)
	if update := spec.UpdateBody(config, resets); len(update) > 0 {
		if err := r.Qdrant.UpdateCollection(ctx, node, name, update); err != nil {
			return domain.CollectionStatus{}, fmt.Errorf("update collection %s: %w", name, err)
		}
	}

	shardKeys, err := r.reconcileShardKeys(ctx, node, spec)
	if err != nil {
		return domain.CollectionStatus{}, err
	}
	payloadSchema, _ := info["payload_schema"].(domain.JSON)
	indexes, err := r.reconcileIndexes(ctx, node, spec, payloadSchema, current.PayloadIndexes)
	if err != nil {
		return domain.CollectionStatus{}, err
	}
	aliases, err := r.reconcileAliases(ctx, node, spec, current.Aliases)
	if err != nil {
		return domain.CollectionStatus{}, err
	}

	message := domain.CollectionReadyMessage(name, domain.ResetNames(resets))
	status := spec.Observe(current, info, generation, domain.CollectionReady, domain.ReasonCollectionReady, message, now)
	status.AppliedFields = spec.DeclaredFields()
	status.PayloadIndexes = indexes
	status.Aliases = aliases
	status.ShardKeys = shardKeys
	return status, nil
}

func (r ReconcileCollection) now() time.Time {
	if r.Clock == nil {
		return SystemClock()
	}
	return r.Clock()
}

func (r ReconcileCollection) reconcileShardKeys(ctx context.Context, node domain.QdrantNode, spec domain.CollectionSpec) ([]any, error) {
	if !spec.ManagesShardKeys() {
		return nil, nil
	}
	listed, err := r.Qdrant.ListShardKeys(ctx, node, spec.CollectionName)
	if err != nil {
		return nil, fmt.Errorf("list shard keys of %s: %w", spec.CollectionName, err)
	}
	existing := make([]any, 0, len(listed))
	for _, key := range listed {
		existing = append(existing, normalizeShardKey(key))
	}
	plan := spec.ShardKeyPlan(existing)
	for _, shardKey := range plan.Create {
		if err := r.Qdrant.CreateShardKey(ctx, node, spec.CollectionName, shardKey.Body()); err != nil {
			return nil, fmt.Errorf("create shard key %v on %s: %w", shardKey.Key, spec.CollectionName, err)
		}
	}
	return plan.Keys, nil
}

func normalizeShardKey(key any) any {
	number, ok := key.(json.Number)
	if !ok {
		return key
	}
	parsed, err := strconv.Atoi(number.String())
	if err != nil {
		return number.String()
	}
	return parsed
}

func (r ReconcileCollection) reconcileIndexes(ctx context.Context, node domain.QdrantNode, spec domain.CollectionSpec, payloadSchema domain.JSON, managed []string) ([]string, error) {
	plan := spec.IndexPlan(payloadSchema, managed)
	for _, operation := range plan.Operations {
		if operation.Delete {
			if err := r.Qdrant.DeletePayloadIndex(ctx, node, spec.CollectionName, operation.Field); err != nil {
				return nil, fmt.Errorf("delete payload index %s: %w", operation.Field, err)
			}
			continue
		}
		if err := r.Qdrant.CreatePayloadIndex(ctx, node, spec.CollectionName, operation.Field, operation.Schema); err != nil {
			return nil, fmt.Errorf("create payload index %s: %w", operation.Field, err)
		}
	}
	return plan.Managed, nil
}

func (r ReconcileCollection) reconcileAliases(ctx context.Context, node domain.QdrantNode, spec domain.CollectionSpec, managed []string) ([]string, error) {
	actual, err := r.Qdrant.ListAliases(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("list aliases: %w", err)
	}
	if actions := spec.AliasActions(actual, managed); len(actions) > 0 {
		if err := r.Qdrant.UpdateAliases(ctx, node, actions); err != nil {
			return nil, fmt.Errorf("update aliases: %w", err)
		}
	}
	return spec.AliasNames(), nil
}

// DeleteCollection drops the collection when the deletion policy says so.
type DeleteCollection struct {
	Qdrant     ports.Qdrant
	Kubernetes ports.Kubernetes
}

// Execute reports whether the collection was dropped. A cluster that is already gone counts as nothing to drop.
func (d DeleteCollection) Execute(ctx context.Context, spec domain.CollectionSpec) (bool, error) {
	if spec.DeletionPolicy != domain.DeletionDelete {
		return false, nil
	}
	connection, err := ResolveCluster{Kubernetes: d.Kubernetes}.Execute(ctx, spec.ClusterRef)
	if errors.Is(err, ErrClusterNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := d.Qdrant.DeleteCollection(ctx, connection.Service, spec.CollectionName); err != nil {
		return false, fmt.Errorf("delete collection %s: %w", spec.CollectionName, err)
	}
	return true, nil
}

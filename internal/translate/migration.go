package translate

import (
	"encoding/json"
	"fmt"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func MigrationSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantMigrationSpec) (domain.MigrationSpec, error) {
	source := domain.MigrationSource{}
	if spec.Source.ClusterRef != nil {
		ref := accessMigrationClusterRef(*spec.Source.ClusterRef, namespace)
		source.ClusterRef = &ref
	}
	if spec.Source.Endpoint != nil {
		endpoint := domain.NewEndpointSpec(
			spec.Source.Endpoint.URL,
			endpointSecretRef(spec.Source.Endpoint.APIKeySecretRef, namespace),
			endpointSecretRef(spec.Source.Endpoint.CASecretRef, namespace),
		)
		source.Endpoint = &endpoint
	}
	migration := domain.DefaultMigrationSpec(name, namespace, source, accessMigrationClusterRef(spec.TargetClusterRef, namespace))
	migration.Collections = spec.Collections
	for from, to := range spec.CollectionMapping {
		migration.CollectionMapping[from] = to
	}
	if spec.BatchSize != nil {
		migration.BatchSize = int(*spec.BatchSize)
	}
	if spec.BatchDelayMs != nil {
		migration.BatchDelay = time.Duration(*spec.BatchDelayMs) * time.Millisecond
	}
	if spec.CheckpointBatches != nil {
		migration.CheckpointBatches = int(*spec.CheckpointBatches)
	}
	if spec.VerifyPasses != nil {
		migration.VerifyPasses = int(*spec.VerifyPasses)
	}
	migration.Sync = boolOr(spec.Sync, migration.Sync)
	if spec.SyncMaxPasses != nil {
		migration.SyncMaxPasses = int(*spec.SyncMaxPasses)
	}
	migration.CreateMissing = boolOr(spec.CreateMissing, migration.CreateMissing)
	migration.EnsurePayloadIndexes = boolOr(spec.EnsurePayloadIndexes, migration.EnsurePayloadIndexes)
	target, err := targetOverridesFromAPI(spec.Target)
	if err != nil {
		return domain.MigrationSpec{}, err
	}
	migration.Target = target
	if err := migration.Validate(); err != nil {
		return domain.MigrationSpec{}, err
	}
	return migration, nil
}

func endpointSecretRef(ref *qdrantv1alpha1.SecretKeyRef, namespace string) *domain.SecretRef {
	if ref == nil {
		return nil
	}
	return &domain.SecretRef{Name: ref.Name, Key: ref.Key, Namespace: namespace}
}

func targetOverridesFromAPI(target *qdrantv1alpha1.MigrationTarget) (domain.TargetOverrides, error) {
	if target == nil {
		return domain.TargetOverrides{}, nil
	}
	overrides := domain.TargetOverrides{ShardKeyField: target.ShardKeyField}
	if target.ShardNumber != nil {
		overrides.ShardNumber = int(*target.ShardNumber)
	}
	if target.ReplicationFactor != nil {
		overrides.ReplicationFactor = int(*target.ReplicationFactor)
	}
	if target.WriteConsistencyFactor != nil {
		overrides.WriteConsistencyFactor = int(*target.WriteConsistencyFactor)
	}
	if target.ShardKey == nil {
		return overrides, nil
	}
	if target.ShardKey.Type == intstr.String {
		overrides.ShardKey = domain.TextShardKey(target.ShardKey.StrVal)
		return overrides, nil
	}
	if target.ShardKey.IntVal < 0 {
		return domain.TargetOverrides{}, fmt.Errorf("target.shardKey %d must not be negative", target.ShardKey.IntVal)
	}
	overrides.ShardKey = domain.NumberShardKey(uint64(target.ShardKey.IntVal))
	return overrides, nil
}

func MigrationStatusFromAPI(status qdrantv1alpha1.QdrantMigrationStatus) (domain.MigrationStatus, error) {
	result := domain.MigrationStatus{
		Phase:  domain.MigrationPhase(status.Phase),
		Source: status.Source,
		Error:  status.Error,
	}
	if result.Phase == "" {
		result.Phase = domain.MigrationPending
	}
	startTime, err := domain.ParseTime(status.StartTime)
	if err != nil {
		return domain.MigrationStatus{}, fmt.Errorf("startTime: %w", err)
	}
	result.StartTime = startTime
	completionTime, err := domain.ParseTime(status.CompletionTime)
	if err != nil {
		return domain.MigrationStatus{}, fmt.Errorf("completionTime: %w", err)
	}
	result.CompletionTime = completionTime
	for _, collection := range status.Collections {
		converted, err := collectionMigrationFromAPI(collection)
		if err != nil {
			return domain.MigrationStatus{}, fmt.Errorf("collection %s: %w", collection.Name, err)
		}
		result.Collections = append(result.Collections, converted)
	}
	conditions, err := accessMigrationConditionsFromAPI(status.Conditions)
	if err != nil {
		return domain.MigrationStatus{}, err
	}
	result.Conditions = conditions
	return result, nil
}

func collectionMigrationFromAPI(status qdrantv1alpha1.MigrationCollectionStatus) (domain.CollectionMigration, error) {
	collection := domain.NewCollectionMigration(status.Name, stringOr(status.TargetName, status.Name))
	if status.Status != "" {
		collection.Status = domain.MigrationPhase(status.Status)
	}
	collection.PointsTotal = status.PointsTotal
	collection.PointsCopied = status.PointsCopied
	collection.Error = status.Error
	collection.VerifyPass = int(status.VerifyPass)
	collection.SourceCount = countOrNil(status.SourceCount)
	collection.TargetCount = countOrNil(status.TargetCount)
	collection.Message = status.Message
	collection.PointsUpdated = status.PointsUpdated
	collection.PointsDeleted = status.PointsDeleted
	collection.SyncPass = int(status.SyncPasses)
	collection.SyncPassChanges = status.SyncPassChanges
	offset, err := pointIDFromAPI(status.Offset)
	if err != nil {
		return domain.CollectionMigration{}, fmt.Errorf("offset: %w", err)
	}
	collection.Offset = offset
	syncOffset, err := pointIDFromAPI(status.SyncOffset)
	if err != nil {
		return domain.CollectionMigration{}, fmt.Errorf("syncOffset: %w", err)
	}
	collection.SyncOffset = syncOffset
	return collection, nil
}

func MigrationStatusToAPI(status domain.MigrationStatus) (qdrantv1alpha1.QdrantMigrationStatus, error) {
	progress := status.Progress()
	result := qdrantv1alpha1.QdrantMigrationStatus{
		Phase:          qdrantv1alpha1.MigrationPhase(status.Phase),
		StartTime:      accessMigrationTime(status.StartTime),
		CompletionTime: accessMigrationTime(status.CompletionTime),
		Source:         status.Source,
		Progress: &qdrantv1alpha1.MigrationProgress{
			CollectionsTotal:     int32(progress.CollectionsTotal),
			CollectionsCompleted: int32(progress.CollectionsCompleted),
			PointsTotal:          progress.PointsTotal,
			PointsCopied:         progress.PointsCopied,
			Percentage:           int32(progress.Percentage()),
		},
		Error:      status.Error,
		Conditions: accessMigrationConditionsToAPI(status.Conditions),
	}
	for _, collection := range status.Collections {
		converted, err := collectionMigrationToAPI(collection)
		if err != nil {
			return qdrantv1alpha1.QdrantMigrationStatus{}, fmt.Errorf("collection %s: %w", collection.Name, err)
		}
		result.Collections = append(result.Collections, converted)
	}
	return result, nil
}

func collectionMigrationToAPI(collection domain.CollectionMigration) (qdrantv1alpha1.MigrationCollectionStatus, error) {
	offset, err := pointIDToAPI(collection.Offset)
	if err != nil {
		return qdrantv1alpha1.MigrationCollectionStatus{}, fmt.Errorf("offset: %w", err)
	}
	syncOffset, err := pointIDToAPI(collection.SyncOffset)
	if err != nil {
		return qdrantv1alpha1.MigrationCollectionStatus{}, fmt.Errorf("syncOffset: %w", err)
	}
	return qdrantv1alpha1.MigrationCollectionStatus{
		Name:            collection.Name,
		TargetName:      collection.TargetName,
		Status:          qdrantv1alpha1.MigrationPhase(collection.Status),
		PointsTotal:     collection.PointsTotal,
		PointsCopied:    collection.PointsCopied,
		Offset:          offset,
		Error:           collection.Error,
		Message:         collection.Message,
		VerifyPass:      int32(collection.VerifyPass),
		PointsUpdated:   collection.PointsUpdated,
		PointsDeleted:   collection.PointsDeleted,
		SyncPasses:      int32(collection.SyncPass),
		SyncOffset:      syncOffset,
		SyncPassChanges: collection.SyncPassChanges,
		SourceCount:     countOrZero(collection.SourceCount),
		TargetCount:     countOrZero(collection.TargetCount),
	}, nil
}

func countOrNil(count int64) *int64 {
	if count == 0 {
		return nil
	}
	return &count
}

func countOrZero(count *int64) int64 {
	if count == nil {
		return 0
	}
	return *count
}

func pointIDFromAPI(value *apiextensionsv1.JSON) (domain.PointID, error) {
	id := domain.PointID{}
	if value == nil || len(value.Raw) == 0 {
		return id, nil
	}
	if err := json.Unmarshal(value.Raw, &id); err != nil {
		return domain.PointID{}, err
	}
	return id, nil
}

func pointIDToAPI(id domain.PointID) (*apiextensionsv1.JSON, error) {
	if id.IsZero() {
		return nil, nil
	}
	raw, err := json.Marshal(id)
	if err != nil {
		return nil, err
	}
	return &apiextensionsv1.JSON{Raw: raw}, nil
}

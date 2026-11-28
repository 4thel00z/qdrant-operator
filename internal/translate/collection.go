package translate

import (
	"fmt"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func CollectionSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantCollectionSpec) (domain.CollectionSpec, error) {
	deletionPolicy, err := domain.ParseDeletionPolicy(string(spec.DeletionPolicy))
	if err != nil {
		return domain.CollectionSpec{}, err
	}
	collection := domain.CollectionSpec{
		Name:                   name,
		Namespace:              namespace,
		ClusterRef:             domain.ClusterRef{Name: spec.ClusterRef.Name, Namespace: stringOr(spec.ClusterRef.Namespace, namespace)},
		CollectionName:         stringOr(spec.CollectionName, name),
		ShardNumber:            collectionInt(spec.ShardNumber),
		ShardingMethod:         string(spec.ShardingMethod),
		ReplicationFactor:      collectionInt(spec.ReplicationFactor),
		WriteConsistencyFactor: collectionInt(spec.WriteConsistencyFactor),
		ReadFanOutFactor:       collectionInt(spec.ReadFanOutFactor),
		ReadFanOutDelayMs:      collectionInt(spec.ReadFanOutDelayMs),
		OnDiskPayload:          spec.OnDiskPayload,
		PayloadMemory:          string(spec.PayloadMemory),
		Aliases:                append([]string{}, spec.Aliases...),
		DeletionPolicy:         deletionPolicy,
	}
	for _, vector := range spec.Vectors {
		converted, err := collectionDenseVector(vector)
		if err != nil {
			return domain.CollectionSpec{}, fmt.Errorf("vector %q: %w", vector.Name, err)
		}
		collection.Vectors = append(collection.Vectors, converted)
	}
	for _, vector := range spec.SparseVectors {
		index, err := ObjectFromAPI(vector.Index)
		if err != nil {
			return domain.CollectionSpec{}, fmt.Errorf("sparse vector %q index: %w", vector.Name, err)
		}
		collection.SparseVectors = append(collection.SparseVectors, domain.SparseVectorSpec{
			Name:     vector.Name,
			Modifier: string(vector.Modifier),
			Index:    index,
		})
	}
	for _, shardKey := range spec.ShardKeys {
		collection.ShardKeys = append(collection.ShardKeys, domain.ShardKeySpec{
			Key:               IntOrStringValue(&shardKey.Key),
			ShardsNumber:      collectionInt(shardKey.ShardsNumber),
			ReplicationFactor: collectionInt(shardKey.ReplicationFactor),
		})
	}
	for _, index := range spec.PayloadIndexes {
		params, err := ObjectFromAPI(index.Params)
		if err != nil {
			return domain.CollectionSpec{}, fmt.Errorf("payload index %q params: %w", index.Field, err)
		}
		collection.PayloadIndexes = append(collection.PayloadIndexes, domain.PayloadIndexSpec{
			Field:  index.Field,
			Type:   string(index.Type),
			Params: params,
		})
	}
	blocks := []struct {
		name   string
		source *apiextensionsv1.JSON
		target *domain.JSON
	}{
		{"metadata", spec.Metadata, &collection.Metadata},
		{"hnsw", spec.HNSW, &collection.HNSW},
		{"optimizers", spec.Optimizers, &collection.Optimizers},
		{"quantization", spec.Quantization, &collection.Quantization},
		{"wal", spec.WAL, &collection.WAL},
		{"strictMode", spec.StrictMode, &collection.StrictMode},
	}
	for _, block := range blocks {
		object, err := ObjectFromAPI(block.source)
		if err != nil {
			return domain.CollectionSpec{}, fmt.Errorf("%s: %w", block.name, err)
		}
		*block.target = object
	}
	if err := collection.Validate(); err != nil {
		return domain.CollectionSpec{}, err
	}
	return collection, nil
}

func collectionDenseVector(vector qdrantv1alpha1.DenseVector) (domain.VectorSpec, error) {
	converted := domain.VectorSpec{
		Size:     int(vector.Size),
		Distance: string(vector.Distance),
		Name:     vector.Name,
		OnDisk:   vector.OnDisk,
		Memory:   string(vector.Memory),
		Datatype: string(vector.Datatype),
	}
	if vector.Multivector != nil {
		converted.Multivector = domain.JSON{"comparator": string(vector.Multivector.Comparator)}
	}
	hnsw, err := ObjectFromAPI(vector.HNSW)
	if err != nil {
		return domain.VectorSpec{}, fmt.Errorf("hnsw: %w", err)
	}
	quantization, err := ObjectFromAPI(vector.Quantization)
	if err != nil {
		return domain.VectorSpec{}, fmt.Errorf("quantization: %w", err)
	}
	converted.HNSW = hnsw
	converted.Quantization = quantization
	return converted, nil
}

func CollectionStatusFromAPI(status qdrantv1alpha1.QdrantCollectionStatus) (domain.CollectionStatus, error) {
	phase, err := domain.ParseCollectionPhase(string(status.Phase))
	if err != nil {
		return domain.CollectionStatus{}, err
	}
	conditions, err := collectionConditionsFromAPI(status.Conditions)
	if err != nil {
		return domain.CollectionStatus{}, err
	}
	converted := domain.CollectionStatus{
		Phase:               phase,
		CollectionName:      status.CollectionName,
		Health:              status.Health,
		PointsCount:         collectionCount(status.PointsCount),
		IndexedVectorsCount: collectionCount(status.IndexedVectorsCount),
		SegmentsCount:       collectionCount(status.SegmentsCount),
		PayloadIndexes:      slices.Clone(status.PayloadIndexes),
		Aliases:             slices.Clone(status.Aliases),
		Error:               status.Error,
		ObservedGeneration:  collectionCount(status.ObservedGeneration),
		Conditions:          conditions,
	}
	for index := range status.ShardKeys {
		converted.ShardKeys = append(converted.ShardKeys, IntOrStringValue(&status.ShardKeys[index]))
	}
	for group, names := range status.AppliedFields {
		if len(names) == 0 {
			continue
		}
		if converted.AppliedFields == nil {
			converted.AppliedFields = map[string][]string{}
		}
		converted.AppliedFields[group] = slices.Clone(names)
	}
	return converted, nil
}

func CollectionStatusToAPI(status domain.CollectionStatus) qdrantv1alpha1.QdrantCollectionStatus {
	converted := qdrantv1alpha1.QdrantCollectionStatus{
		Phase:               qdrantv1alpha1.CollectionPhase(status.Phase),
		CollectionName:      status.CollectionName,
		Health:              status.Health,
		PointsCount:         collectionCountValue(status.PointsCount),
		IndexedVectorsCount: collectionCountValue(status.IndexedVectorsCount),
		SegmentsCount:       collectionCountValue(status.SegmentsCount),
		PayloadIndexes:      status.PayloadIndexes,
		Aliases:             status.Aliases,
		Error:               status.Error,
		ObservedGeneration:  collectionCountValue(status.ObservedGeneration),
	}
	for _, key := range status.ShardKeys {
		converted.ShardKeys = append(converted.ShardKeys, collectionShardKeyToAPI(key))
	}
	for _, condition := range status.Conditions {
		converted.Conditions = append(converted.Conditions, collectionConditionToAPI(condition))
	}
	for group, names := range status.AppliedFields {
		if len(names) == 0 {
			continue
		}
		if converted.AppliedFields == nil {
			converted.AppliedFields = map[string][]string{}
		}
		converted.AppliedFields[group] = names
	}
	return converted
}

func collectionShardKeyToAPI(key any) intstr.IntOrString {
	if number, ok := key.(int); ok {
		return intstr.FromInt32(int32(number))
	}
	if number, ok := key.(float64); ok {
		return intstr.FromInt32(int32(number))
	}
	return intstr.FromString(fmt.Sprint(key))
}

func collectionConditionsFromAPI(conditions []qdrantv1alpha1.Condition) ([]domain.Condition, error) {
	converted := make([]domain.Condition, 0, len(conditions))
	for _, condition := range conditions {
		transition, err := domain.ParseTime(condition.LastTransitionTime)
		if err != nil {
			return nil, fmt.Errorf("condition %s lastTransitionTime: %w", condition.Type, err)
		}
		converted = append(converted, domain.Condition{
			Type:               condition.Type,
			Status:             domain.ConditionStatus(condition.Status),
			LastTransitionTime: transition,
			Reason:             condition.Reason,
			Message:            condition.Message,
		})
	}
	return converted, nil
}

func collectionConditionToAPI(condition domain.Condition) qdrantv1alpha1.Condition {
	converted := qdrantv1alpha1.Condition{
		Type:    condition.Type,
		Status:  qdrantv1alpha1.ConditionStatus(condition.Status),
		Reason:  condition.Reason,
		Message: condition.Message,
	}
	if !condition.LastTransitionTime.IsZero() {
		converted.LastTransitionTime = domain.FormatTime(condition.LastTransitionTime)
	}
	return converted
}

func collectionInt[T int32 | int64](value *T) *int {
	if value == nil {
		return nil
	}
	converted := int(*value)
	return &converted
}

func collectionCount(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func collectionCountValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// +kubebuilder:validation:Enum=Cosine;Euclid;Dot;Manhattan
type VectorDistance string

// +kubebuilder:validation:Enum=float32;float16;uint8;turbo4
type VectorDatatype string

// +kubebuilder:validation:Enum=cold;cached
type MemoryPlacement string

// +kubebuilder:validation:Enum=max_sim
type MultivectorComparator string

// +kubebuilder:validation:Enum=none;idf
type SparseModifier string

// +kubebuilder:validation:Enum=auto;custom
type ShardingMethod string

// +kubebuilder:validation:Enum=keyword;integer;float;geo;text;bool;datetime;uuid
type PayloadIndexType string

// +kubebuilder:validation:Enum=Retain;Delete
type CollectionDeletionPolicy string

// +kubebuilder:validation:Enum=Pending;Ready;Degraded;Failed
type CollectionPhase string

type MultivectorConfig struct {
	Comparator MultivectorComparator `json:"comparator"`
}

type DenseVector struct {
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:Minimum=1
	Size     int32          `json:"size"`
	Distance VectorDistance `json:"distance"`
	// +optional
	OnDisk *bool `json:"onDisk,omitempty"`
	// turbo4 needs Qdrant 1.19+
	// +optional
	Datatype VectorDatatype `json:"datatype,omitempty"`
	// RAM placement of the vector storage (Qdrant 1.19+; supersedes onDisk; pinned is not allowed for dense vectors)
	// +optional
	Memory MemoryPlacement `json:"memory,omitempty"`
	// +optional
	Multivector *MultivectorConfig `json:"multivector,omitempty"`
	// Per-vector HNSW overrides (Qdrant HnswConfigDiff field names)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	HNSW *apiextensionsv1.JSON `json:"hnsw,omitempty"`
	// Per-vector quantization (Qdrant QuantizationConfig, e.g. {scalar: {type: int8}})
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Quantization *apiextensionsv1.JSON `json:"quantization,omitempty"`
}

type SparseVector struct {
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`
	// +optional
	Modifier SparseModifier `json:"modifier,omitempty"`
	// Sparse index settings (Qdrant SparseIndexParams field names)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Index *apiextensionsv1.JSON `json:"index,omitempty"`
}

type ShardKey struct {
	// Keyword or non-negative integer shard key
	Key intstr.IntOrString `json:"key"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	ShardsNumber *int32 `json:"shardsNumber,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	ReplicationFactor *int32 `json:"replicationFactor,omitempty"`
}

type PayloadIndex struct {
	// Payload key (JSON path) to index
	// +kubebuilder:validation:MaxLength=255
	Field string           `json:"field"`
	Type  PayloadIndexType `json:"type"`
	// Type-specific index parameters (e.g. {tokenizer: word, lowercase: true} for text)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Params *apiextensionsv1.JSON `json:"params,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.vectors) || has(self.sparseVectors)",message="declare at least one of vectors or sparseVectors"
// +kubebuilder:validation:XValidation:rule="!has(self.vectors) || self.vectors.size() == 1 || self.vectors.all(v, has(v.name))",message="an unnamed vector must be the only vector; name every vector otherwise"
// +kubebuilder:validation:XValidation:rule="self.clusterRef == oldSelf.clusterRef",message="clusterRef is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.collectionName) ? self.collectionName : \x27\x27) == (has(oldSelf.collectionName) ? oldSelf.collectionName : \x27\x27)",message="collectionName is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.shardKeys) || (has(self.shardingMethod) && self.shardingMethod == 'custom')",message="shardKeys need shardingMethod: custom"
type QdrantCollectionSpec struct {
	ClusterRef ClusterRef `json:"clusterRef"`
	// Name inside Qdrant (defaults to metadata.name; may use characters Kubernetes names cannot)
	// +optional
	// +kubebuilder:validation:MaxLength=255
	CollectionName string `json:"collectionName,omitempty"`
	// Dense vector spaces. One entry without a name is Qdrant's single unnamed vector.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	Vectors []DenseVector `json:"vectors,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=64
	SparseVectors []SparseVector `json:"sparseVectors,omitempty"`
	// Immutable after creation; defaults to the node count in distributed mode
	// +optional
	// +kubebuilder:validation:Minimum=1
	ShardNumber *int32 `json:"shardNumber,omitempty"`
	// Immutable after creation
	// +optional
	ShardingMethod ShardingMethod `json:"shardingMethod,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	ReplicationFactor *int32 `json:"replicationFactor,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	WriteConsistencyFactor *int32 `json:"writeConsistencyFactor,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	ReadFanOutFactor *int32 `json:"readFanOutFactor,omitempty"`
	// Delay before fanning reads out to remote replicas
	// +optional
	// +kubebuilder:validation:Minimum=0
	ReadFanOutDelayMs *int64 `json:"readFanOutDelayMs,omitempty"`
	// +optional
	OnDiskPayload *bool `json:"onDiskPayload,omitempty"`
	// RAM placement of the payload storage (Qdrant 1.19+; supersedes onDiskPayload)
	// +optional
	PayloadMemory MemoryPlacement `json:"payloadMemory,omitempty"`
	// Shard keys to create for custom sharding. Never dropped by the operator - dropping a key deletes its points.
	// +optional
	// +kubebuilder:validation:MaxItems=1024
	ShardKeys []ShardKey `json:"shardKeys,omitempty"`
	// Arbitrary collection metadata (merged into the live collection; keys you drop stay)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Metadata *apiextensionsv1.JSON `json:"metadata,omitempty"`
	// Collection-wide HNSW settings (Qdrant HnswConfigDiff field names)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	HNSW *apiextensionsv1.JSON `json:"hnsw,omitempty"`
	// Optimizer settings (Qdrant OptimizersConfigDiff field names)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Optimizers *apiextensionsv1.JSON `json:"optimizers,omitempty"`
	// Collection-wide quantization (Qdrant QuantizationConfig)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Quantization *apiextensionsv1.JSON `json:"quantization,omitempty"`
	// Write-ahead log settings, applied at creation only (Qdrant WalConfigDiff)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	WAL *apiextensionsv1.JSON `json:"wal,omitempty"`
	// Strict mode limits (Qdrant StrictModeConfig field names)
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	StrictMode *apiextensionsv1.JSON `json:"strictMode,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=field
	PayloadIndexes []PayloadIndex `json:"payloadIndexes,omitempty"`
	// Aliases that must point at this collection
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MaxLength=255
	Aliases []string `json:"aliases,omitempty"`
	// Whether deleting the resource drops the Qdrant collection
	// +optional
	// +kubebuilder:default=Retain
	DeletionPolicy CollectionDeletionPolicy `json:"deletionPolicy,omitempty"`
}

type QdrantCollectionStatus struct {
	// +optional
	Phase CollectionPhase `json:"phase,omitempty"`
	// +optional
	CollectionName string `json:"collectionName,omitempty"`
	// Qdrant collection status (green, yellow, grey, red)
	// +optional
	Health string `json:"health,omitempty"`
	// +optional
	PointsCount int64 `json:"pointsCount,omitempty"`
	// +optional
	IndexedVectorsCount int64 `json:"indexedVectorsCount,omitempty"`
	// +optional
	SegmentsCount int64 `json:"segmentsCount,omitempty"`
	// Payload fields whose indexes the operator manages
	// +optional
	PayloadIndexes []string `json:"payloadIndexes,omitempty"`
	// Aliases the operator manages
	// +optional
	Aliases []string `json:"aliases,omitempty"`
	// Config fields per block the operator last applied; one dropped from the spec is reset to Qdrant's default
	// +optional
	AppliedFields map[string][]string `json:"appliedFields,omitempty"`
	// Shard keys present on the collection
	// +optional
	ShardKeys []intstr.IntOrString `json:"shardKeys,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qcol
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Collection",type=string,JSONPath=`.status.collectionName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Points",type=integer,JSONPath=`.status.pointsCount`
// +kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantCollection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantCollectionSpec `json:"spec"`
	// +optional
	Status QdrantCollectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantCollectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantCollection `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantCollection{}, &QdrantCollectionList{})
}

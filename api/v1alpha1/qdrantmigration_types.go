package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
type MigrationPhase string

type SecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type MigrationEndpoint struct {
	// +kubebuilder:validation:Pattern=`^https?://`
	URL string `json:"url"`
	// +optional
	APIKeySecretRef *SecretKeyRef `json:"apiKeySecretRef,omitempty"`
	// PEM bundle to trust when the endpoint uses a private CA
	// +optional
	CASecretRef *SecretKeyRef `json:"caSecretRef,omitempty"`
}

type MigrationSource struct {
	// A QdrantCluster managed by this operator
	// +optional
	ClusterRef *ClusterRef `json:"clusterRef,omitempty"`
	// Any reachable Qdrant, e.g. Qdrant Cloud or a cluster outside Kubernetes
	// +optional
	Endpoint *MigrationEndpoint `json:"endpoint,omitempty"`
}

type MigrationTarget struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	ShardNumber *int32 `json:"shardNumber,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	ReplicationFactor *int32 `json:"replicationFactor,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	WriteConsistencyFactor *int32 `json:"writeConsistencyFactor,omitempty"`
	// Route every point to this shard key (default - keep each point's source shard key)
	// +optional
	ShardKey *intstr.IntOrString `json:"shardKey,omitempty"`
	// Route each point by this payload field (string or non-negative integer)
	// +optional
	ShardKeyField string `json:"shardKeyField,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.source.clusterRef) != has(self.source.endpoint)",message="source needs exactly one of clusterRef or endpoint"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new QdrantMigration instead"
// +kubebuilder:validation:XValidation:rule="!has(self.target) || !(has(self.target.shardKey) && has(self.target.shardKeyField))",message="target.shardKey and target.shardKeyField are mutually exclusive"
type QdrantMigrationSpec struct {
	Source           MigrationSource `json:"source"`
	TargetClusterRef ClusterRef      `json:"targetClusterRef"`
	// Collections to copy (empty = every collection on the source)
	// +optional
	Collections []string `json:"collections,omitempty"`
	// Rename collections while copying (source -> target)
	// +optional
	CollectionMapping map[string]string `json:"collectionMapping,omitempty"`
	// Points per scroll/upsert round trip
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	// +kubebuilder:default=256
	BatchSize *int32 `json:"batchSize,omitempty"`
	// Pause between batches to throttle load on both clusters
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	BatchDelayMs *int64 `json:"batchDelayMs,omitempty"`
	// Batches between progress checkpoints in status; a crash re-copies at most this many batches
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=20
	CheckpointBatches *int32 `json:"checkpointBatches,omitempty"`
	// Extra scroll passes over the source when the target has fewer points than the source after the copy (writes made during the copy are picked up); 0 disables them but the counts are still compared
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	VerifyPasses *int32 `json:"verifyPasses,omitempty"`
	// After the copy, walk source and target by point ID, re-upsert points that are missing or differ in payload or vectors and delete target points the source no longer has; replaces the verify passes. Catches up fully only once writes to the source stop
	// +optional
	// +kubebuilder:default=false
	Sync *bool `json:"sync,omitempty"`
	// Upper bound on sync passes; a further pass runs only while the previous one changed something
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=20
	// +kubebuilder:default=1
	SyncMaxPasses *int32 `json:"syncMaxPasses,omitempty"`
	// Create target collections from the source configuration when absent
	// +optional
	// +kubebuilder:default=true
	CreateMissing *bool `json:"createMissing,omitempty"`
	// Create the source's payload indexes on the target even when the target collection already exists
	// +optional
	// +kubebuilder:default=true
	EnsurePayloadIndexes *bool `json:"ensurePayloadIndexes,omitempty"`
	// Sharding, replication and shard-key routing on the target
	// +optional
	Target *MigrationTarget `json:"target,omitempty"`
}

type MigrationCollectionStatus struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	TargetName string `json:"targetName,omitempty"`
	// +optional
	Status MigrationPhase `json:"status,omitempty"`
	// +optional
	PointsTotal int64 `json:"pointsTotal,omitempty"`
	// +optional
	PointsCopied int64 `json:"pointsCopied,omitempty"`
	// Scroll offset to resume from after an interruption
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Offset *apiextensionsv1.JSON `json:"offset,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// Note on the outcome, such as the target holding more points than the source
	// +optional
	Message string `json:"message,omitempty"`
	// Extra verify passes started so far
	// +optional
	VerifyPass int32 `json:"verifyPass,omitempty"`
	// Points the sync pass re-upserted because they were missing or different on the target
	// +optional
	PointsUpdated int64 `json:"pointsUpdated,omitempty"`
	// Points the sync pass deleted from the target because the source no longer has them
	// +optional
	PointsDeleted int64 `json:"pointsDeleted,omitempty"`
	// Sync passes started so far
	// +optional
	SyncPasses int32 `json:"syncPasses,omitempty"`
	// Point ID the running sync pass resumes from after an interruption
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	SyncOffset *apiextensionsv1.JSON `json:"syncOffset,omitempty"`
	// Points the running sync pass has changed so far
	// +optional
	SyncPassChanges int64 `json:"syncPassChanges,omitempty"`
	// Exact source point count at the last verification
	// +optional
	SourceCount int64 `json:"sourceCount,omitempty"`
	// Exact target point count at the last verification
	// +optional
	TargetCount int64 `json:"targetCount,omitempty"`
}

type MigrationProgress struct {
	// +optional
	CollectionsTotal int32 `json:"collectionsTotal,omitempty"`
	// +optional
	CollectionsCompleted int32 `json:"collectionsCompleted,omitempty"`
	// +optional
	PointsTotal int64 `json:"pointsTotal,omitempty"`
	// +optional
	PointsCopied int64 `json:"pointsCopied,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Percentage int32 `json:"percentage,omitempty"`
}

type QdrantMigrationStatus struct {
	// +optional
	Phase MigrationPhase `json:"phase,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	StartTime string `json:"startTime,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	CompletionTime string `json:"completionTime,omitempty"`
	// Resolved source (cluster name or endpoint URL)
	// +optional
	Source string `json:"source,omitempty"`
	// +optional
	Collections []MigrationCollectionStatus `json:"collections,omitempty"`
	// +optional
	Progress *MigrationProgress `json:"progress,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qmig
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.status.source`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetClusterRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Progress",type=string,JSONPath=`.status.progress.percentage`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantMigration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantMigrationSpec `json:"spec"`
	// +optional
	Status QdrantMigrationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantMigrationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantMigration `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantMigration{}, &QdrantMigrationList{})
}

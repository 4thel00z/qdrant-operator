package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=Pending;Downloading;Restoring;Indexing;Completed;Failed
type RestorePhase string

// +kubebuilder:validation:Enum=snapshot;replica;no_sync
type RestorePriority string

type RestoreBackupRef struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

type RestoreS3Source struct {
	Bucket string `json:"bucket"`
	// Key prefix of the backup root (holds manifest.json)
	Path string `json:"path"`
	// +optional
	// +kubebuilder:default="us-east-1"
	Region string `json:"region,omitempty"`
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// +optional
	// +kubebuilder:default=false
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`
	// +kubebuilder:validation:Required
	CredentialsSecretRef S3CredentialsSecretRef `json:"credentialsSecretRef"`
}

type RestoreSource struct {
	// +optional
	S3 *RestoreS3Source `json:"s3,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="has(self.backupRef) != has(self.source)",message="set exactly one of backupRef or source"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new QdrantRestore instead"
type QdrantRestoreSpec struct {
	// Reference to a QdrantBackup resource
	// +optional
	BackupRef *RestoreBackupRef `json:"backupRef,omitempty"`
	// Direct S3 source (alternative to backupRef)
	// +optional
	Source           *RestoreSource `json:"source,omitempty"`
	TargetClusterRef ClusterRef     `json:"targetClusterRef"`
	// List of collections to restore (empty = all from backup)
	// +optional
	Collections []string `json:"collections,omitempty"`
	// Rename collections during restore (source -> target)
	// +optional
	CollectionMapping map[string]string `json:"collectionMapping,omitempty"`
	// Recovery priority mode
	// +optional
	// +kubebuilder:default=snapshot
	Priority RestorePriority `json:"priority,omitempty"`
	// Wait for HNSW index to be built after restore
	// +optional
	// +kubebuilder:default=true
	WaitForIndexing *bool `json:"waitForIndexing,omitempty"`
	// How long to wait for each restored collection to turn green
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=3600
	IndexingTimeoutSeconds *int32 `json:"indexingTimeoutSeconds,omitempty"`
}

type RestoredCollection struct {
	// +optional
	Name string `json:"name,omitempty"`
	// Original name if remapped
	// +optional
	OriginalName string `json:"originalName,omitempty"`
	// +optional
	Status RestorePhase `json:"status,omitempty"`
	// +optional
	Size string `json:"size,omitempty"`
	// +optional
	PointsCount int64 `json:"pointsCount,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
}

type RestoreProgress struct {
	// +optional
	CollectionsTotal int32 `json:"collectionsTotal,omitempty"`
	// +optional
	CollectionsCompleted int32 `json:"collectionsCompleted,omitempty"`
	// +optional
	CurrentCollection string `json:"currentCollection,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Percentage int32 `json:"percentage,omitempty"`
}

type QdrantRestoreStatus struct {
	// +optional
	Phase RestorePhase `json:"phase,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	StartTime string `json:"startTime,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	CompletionTime string `json:"completionTime,omitempty"`
	// Resolved backup name or S3 path
	// +optional
	SourceBackup string `json:"sourceBackup,omitempty"`
	// +optional
	RestoredCollections []RestoredCollection `json:"restoredCollections,omitempty"`
	// +optional
	Progress *RestoreProgress `json:"progress,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qr
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.backupRef.name`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetClusterRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantRestoreSpec `json:"spec"`
	// +optional
	Status QdrantRestoreStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantRestore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantRestore{}, &QdrantRestoreList{})
}

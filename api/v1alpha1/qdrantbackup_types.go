package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type BackupS3Storage struct {
	Bucket string `json:"bucket"`
	// +optional
	// +kubebuilder:default=""
	Prefix string `json:"prefix,omitempty"`
	// +optional
	// +kubebuilder:default="us-east-1"
	Region string `json:"region,omitempty"`
	// Custom endpoint for S3-compatible storage (MinIO, etc.)
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// Use path-style URLs (required for MinIO)
	// +optional
	// +kubebuilder:default=false
	ForcePathStyle bool `json:"forcePathStyle,omitempty"`
	// +kubebuilder:validation:Required
	CredentialsSecretRef S3CredentialsSecretRef `json:"credentialsSecretRef"`
}

type BackupStorage struct {
	S3 BackupS3Storage `json:"s3"`
}

// +kubebuilder:validation:XValidation:rule="self.method == 'volumeSnapshot' || has(self.storage)",message="storage.s3 is required when method is s3"
// +kubebuilder:validation:XValidation:rule="self.method != 'volumeSnapshot' || has(self.volumeSnapshotClassName)",message="volumeSnapshotClassName is required when method is volumeSnapshot"
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="spec is immutable; create a new QdrantBackup instead"
type QdrantBackupSpec struct {
	ClusterRef ClusterRef `json:"clusterRef"`
	// s3 streams node snapshots into a bucket. volumeSnapshot takes a CSI VolumeSnapshot of every data PVC (crash-consistent, restorable only into a new cluster of the same name and size in the same namespace).
	// +optional
	// +kubebuilder:default=s3
	Method BackupMethod `json:"method,omitempty"`
	// VolumeSnapshotClass to use; required when method is volumeSnapshot
	// +optional
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
	// +optional
	Storage *BackupStorage `json:"storage,omitempty"`
	// Collections to back up (empty = all). Every node is snapshotted.
	// +optional
	Collections []string `json:"collections,omitempty"`
	// Number of days to retain this backup
	// +optional
	// +kubebuilder:validation:Minimum=1
	RetentionDays *int32 `json:"retentionDays,omitempty"`
}

type BackupInProgressSnapshot struct {
	// +optional
	NodeIndex int32 `json:"nodeIndex,omitempty"`
	// +optional
	Collection string `json:"collection,omitempty"`
	// +optional
	Snapshot string `json:"snapshot,omitempty"`
}

type BackupVolumeSnapshot struct {
	// +optional
	NodeIndex int32 `json:"nodeIndex,omitempty"`
	// +optional
	ClaimName string `json:"claimName,omitempty"`
	// +optional
	SnapshotName string `json:"snapshotName,omitempty"`
	// Size requested by the snapshotted claim
	// +optional
	Size string `json:"size,omitempty"`
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// +optional
	AccessModes []string `json:"accessModes,omitempty"`
	// Zone of the snapshotted volume; a restore provisions the claim on a node in it
	// +optional
	Zone string `json:"zone,omitempty"`
	// Node label that carries the zone, taken from the volume's node affinity
	// +optional
	ZoneLabel string `json:"zoneLabel,omitempty"`
}

type BackupNodeSnapshot struct {
	// +optional
	Node string `json:"node,omitempty"`
	// +optional
	SnapshotName string `json:"snapshotName,omitempty"`
	// +optional
	Key string `json:"key,omitempty"`
}

type BackupCollectionStatus struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	Size string `json:"size,omitempty"`
	// +optional
	Status BackupPhase `json:"status,omitempty"`
	// One snapshot per cluster node; each holds that node's shards
	// +optional
	Snapshots []BackupNodeSnapshot `json:"snapshots,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
}

type QdrantBackupStatus struct {
	// +optional
	Phase BackupPhase `json:"phase,omitempty"`
	// First attempt's start; kept across operator restarts
	// +optional
	// +kubebuilder:validation:Format=date-time
	StartTime string `json:"startTime,omitempty"`
	// Node snapshot being streamed right now, cleared once deleted
	// +optional
	InProgressSnapshot *BackupInProgressSnapshot `json:"inProgressSnapshot,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	CompletionTime string `json:"completionTime,omitempty"`
	// When retentionDays elapse and the operator deletes this backup
	// +optional
	// +kubebuilder:validation:Format=date-time
	ExpiresAt string `json:"expiresAt,omitempty"`
	// One VolumeSnapshot per data PVC; set when method is volumeSnapshot
	// +optional
	VolumeSnapshots []BackupVolumeSnapshot `json:"volumeSnapshots,omitempty"`
	// Full S3 path where backup is stored
	// +optional
	S3Path string `json:"s3Path,omitempty"`
	// Total size of backup (human-readable)
	// +optional
	TotalSize string `json:"totalSize,omitempty"`
	// +optional
	Collections []BackupCollectionStatus `json:"collections,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qb
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.status.totalSize`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantBackupSpec `json:"spec"`
	// +optional
	Status QdrantBackupStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantBackup{}, &QdrantBackupList{})
}

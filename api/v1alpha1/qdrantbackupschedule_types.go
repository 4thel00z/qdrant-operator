package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=Allow;Forbid;Replace
type ConcurrencyPolicy string

// +kubebuilder:validation:Enum=Active;Suspended
type SchedulePhase string

type ScheduleS3Storage struct {
	Bucket string `json:"bucket"`
	// +optional
	// +kubebuilder:default=""
	Prefix string `json:"prefix,omitempty"`
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

type ScheduleStorage struct {
	S3 ScheduleS3Storage `json:"s3"`
}

type RetentionPolicy struct {
	// Keep the last N backups regardless of age
	// +optional
	// +kubebuilder:validation:Minimum=1
	KeepLast *int32 `json:"keepLast,omitempty"`
	// Keep one backup per day for N days
	// +optional
	// +kubebuilder:validation:Minimum=0
	KeepDaily *int32 `json:"keepDaily,omitempty"`
	// Keep one backup per week for N weeks
	// +optional
	// +kubebuilder:validation:Minimum=0
	KeepWeekly *int32 `json:"keepWeekly,omitempty"`
	// Keep one backup per month for N months
	// +optional
	// +kubebuilder:validation:Minimum=0
	KeepMonthly *int32 `json:"keepMonthly,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self.method == 'volumeSnapshot' || has(self.storage)",message="storage.s3 is required when method is s3"
// +kubebuilder:validation:XValidation:rule="self.method != 'volumeSnapshot' || has(self.volumeSnapshotClassName)",message="volumeSnapshotClassName is required when method is volumeSnapshot"
type QdrantBackupScheduleSpec struct {
	// Five-field cron expression, UTC (e.g., "0 2 * * *")
	// +kubebuilder:validation:Pattern=`^\S+\s+\S+\s+\S+\s+\S+\s+\S+$`
	Schedule   string     `json:"schedule"`
	ClusterRef ClusterRef `json:"clusterRef"`
	// s3 streams node snapshots into a bucket. volumeSnapshot takes a CSI VolumeSnapshot of every data PVC (crash-consistent, restorable only into a new cluster of the same name and size in the same namespace).
	// +optional
	// +kubebuilder:default=s3
	Method BackupMethod `json:"method,omitempty"`
	// VolumeSnapshotClass to use; required when method is volumeSnapshot
	// +optional
	VolumeSnapshotClassName string `json:"volumeSnapshotClassName,omitempty"`
	// +optional
	Storage *ScheduleStorage `json:"storage,omitempty"`
	// List of collections to backup (empty = all)
	// +optional
	Collections []string `json:"collections,omitempty"`
	// +optional
	RetentionPolicy *RetentionPolicy `json:"retentionPolicy,omitempty"`
	// Suspend scheduled backups
	// +optional
	// +kubebuilder:default=false
	Suspend bool `json:"suspend,omitempty"`
	// Delete every backup this schedule created, including its S3 data, when the schedule is deleted. By default the backups are kept.
	// +optional
	// +kubebuilder:default=false
	DeleteBackupsWithSchedule bool `json:"deleteBackupsWithSchedule,omitempty"`
	// How to handle concurrent backup runs
	// +optional
	// +kubebuilder:default=Forbid
	ConcurrencyPolicy ConcurrencyPolicy `json:"concurrencyPolicy,omitempty"`
	// Deadline for starting a missed backup
	// +optional
	// +kubebuilder:validation:Minimum=0
	StartingDeadlineSeconds *int64 `json:"startingDeadlineSeconds,omitempty"`
}

type RecentBackup struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	CreationTime string `json:"creationTime,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	CompletionTime string `json:"completionTime,omitempty"`
	// +optional
	Status string `json:"status,omitempty"`
	// +optional
	Size string `json:"size,omitempty"`
}

type QdrantBackupScheduleStatus struct {
	// +optional
	Phase SchedulePhase `json:"phase,omitempty"`
	// Cron slot the most recent backup was created for
	// +optional
	// +kubebuilder:validation:Format=date-time
	LastScheduleTime string `json:"lastScheduleTime,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	LastBackupTime string `json:"lastBackupTime,omitempty"`
	// +optional
	LastBackupName string `json:"lastBackupName,omitempty"`
	// +optional
	LastBackupStatus BackupPhase `json:"lastBackupStatus,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	NextBackupTime string `json:"nextBackupTime,omitempty"`
	// Name of currently running backup (if any)
	// +optional
	ActiveBackup string `json:"activeBackup,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=10
	RecentBackups []RecentBackup `json:"recentBackups,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qbs
// +kubebuilder:printcolumn:name="Schedule",type=string,JSONPath=`.spec.schedule`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Last Backup",type=date,JSONPath=`.status.lastBackupTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantBackupSchedule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantBackupScheduleSpec `json:"spec"`
	// +optional
	Status QdrantBackupScheduleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantBackupScheduleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantBackupSchedule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantBackupSchedule{}, &QdrantBackupScheduleList{})
}

package v1alpha1

// +kubebuilder:validation:Enum=s3;volumeSnapshot
type BackupMethod string

// +kubebuilder:validation:Enum=Pending;InProgress;Completed;Failed
type BackupPhase string

type S3CredentialsSecretRef struct {
	Name string `json:"name"`
	// +optional
	// +kubebuilder:default="AWS_ACCESS_KEY_ID"
	AccessKeyIDKey string `json:"accessKeyIdKey,omitempty"`
	// +optional
	// +kubebuilder:default="AWS_SECRET_ACCESS_KEY"
	SecretAccessKeyKey string `json:"secretAccessKeyKey,omitempty"`
}

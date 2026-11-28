package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=r;rw;prw
type CollectionAccessLevel string

// +kubebuilder:validation:Enum=Pending;Ready;Failed
type AccessKeyPhase string

type AccessKeyCollection struct {
	// Collection name inside Qdrant
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`
	// r read, rw read-write, prw points read-write (no snapshots or indexes)
	Access CollectionAccessLevel `json:"access"`
}

type AccessKeyValueMatch struct {
	// +kubebuilder:validation:MaxLength=255
	Key string `json:"key"`
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Value apiextensionsv1.JSON `json:"value"`
}

type AccessKeyValueExists struct {
	// +kubebuilder:validation:MaxLength=255
	Collection string `json:"collection"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	Matches []AccessKeyValueMatch `json:"matches"`
}

// +kubebuilder:validation:XValidation:rule="has(self.access) != has(self.collections)",message="set exactly one of access (cluster-wide) or collections (per collection)"
// +kubebuilder:validation:XValidation:rule="!has(self.renewBefore) || has(self.ttl)",message="renewBefore needs ttl"
// +kubebuilder:validation:XValidation:rule="self.clusterRef == oldSelf.clusterRef",message="clusterRef is immutable"
// +kubebuilder:validation:XValidation:rule="(has(self.secretName) ? self.secretName : \x27\x27) == (has(oldSelf.secretName) ? oldSelf.secretName : \x27\x27)",message="secretName is immutable"
type QdrantAccessKeySpec struct {
	ClusterRef ClusterRef `json:"clusterRef"`
	// Cluster-wide mode - r reads everything, m manages everything
	// +optional
	// +kubebuilder:validation:Enum=r;m
	Access string `json:"access,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=name
	Collections []AccessKeyCollection `json:"collections,omitempty"`
	// Value of the JWT sub claim (a user or service identifier)
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Subject string `json:"subject,omitempty"`
	// Token lifetime such as 720h; omitted tokens never expire
	// +optional
	// +kubebuilder:validation:Pattern=`^([0-9]+[hms])+$`
	TTL string `json:"ttl,omitempty"`
	// How long before expiry a fresh token is written (default a third of ttl)
	// +optional
	// +kubebuilder:validation:Pattern=`^([0-9]+[hms])+$`
	RenewBefore string `json:"renewBefore,omitempty"`
	// Token is valid only while a point matching every key/value exists in the collection
	// +optional
	ValueExists *AccessKeyValueExists `json:"valueExists,omitempty"`
	// Secret receiving the token (defaults to metadata.name); keys token and url
	// +optional
	// +kubebuilder:validation:MaxLength=253
	SecretName string `json:"secretName,omitempty"`
}

type AccessKeySecretRef struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	Key string `json:"key,omitempty"`
}

type QdrantAccessKeyStatus struct {
	// +optional
	Phase AccessKeyPhase `json:"phase,omitempty"`
	// +optional
	SecretRef *AccessKeySecretRef `json:"secretRef,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	IssuedAt string `json:"issuedAt,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	ExpiresAt string `json:"expiresAt,omitempty"`
	// +optional
	// +kubebuilder:validation:Format=date-time
	RenewAt string `json:"renewAt,omitempty"`
	// Fingerprint of the cluster API key the token was signed with
	// +optional
	KeyFingerprint string `json:"keyFingerprint,omitempty"`
	// +optional
	Error string `json:"error,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qak
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterRef.name`
// +kubebuilder:printcolumn:name="Access",type=string,JSONPath=`.spec.access`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Secret",type=string,JSONPath=`.status.secretRef.name`
// +kubebuilder:printcolumn:name="Expires",type=string,JSONPath=`.status.expiresAt`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantAccessKey struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantAccessKeySpec `json:"spec"`
	// +optional
	Status QdrantAccessKeyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantAccessKeyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantAccessKey `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantAccessKey{}, &QdrantAccessKeyList{})
}

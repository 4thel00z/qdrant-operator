package v1alpha1

import (
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// +kubebuilder:validation:XValidation:rule="self.replicas == 1 || !has(self.cluster) || self.cluster.enabled",message="replicas > 1 requires cluster.enabled"
// +kubebuilder:validation:XValidation:rule="(has(self.persistence) && has(self.persistence.size) ? quantity(self.persistence.size) : quantity('10Gi')).compareTo(has(oldSelf.persistence) && has(oldSelf.persistence.size) ? quantity(oldSelf.persistence.size) : quantity('10Gi')) >= 0",message="persistence.size can grow but not shrink"
// +kubebuilder:validation:XValidation:rule="(has(self.snapshotPersistence) && has(self.snapshotPersistence.size) ? quantity(self.snapshotPersistence.size) : quantity('10Gi')).compareTo(has(oldSelf.snapshotPersistence) && has(oldSelf.snapshotPersistence.size) ? quantity(oldSelf.snapshotPersistence.size) : quantity('10Gi')) >= 0",message="snapshotPersistence.size can grow but not shrink"
// +kubebuilder:validation:XValidation:rule="(has(self.persistence) && has(self.persistence.storageClassName) ? self.persistence.storageClassName : \x27\x27) == (has(oldSelf.persistence) && has(oldSelf.persistence.storageClassName) ? oldSelf.persistence.storageClassName : \x27\x27)",message="persistence.storageClassName is immutable"
type QdrantClusterSpec struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	Replicas *int32 `json:"replicas,omitempty"`
	// Qdrant version, also the qdrant-helm chart version (e.g. v1.16.3)
	// +kubebuilder:validation:Pattern=`^v?[0-9]+\.[0-9]+\.[0-9]+$`
	Version string `json:"version"`
	// +optional
	Image *ClusterImage `json:"image,omitempty"`
	// +optional
	Resources *ClusterResources `json:"resources,omitempty"`
	// +optional
	Persistence *ClusterPersistence `json:"persistence,omitempty"`
	// +optional
	SnapshotPersistence *ClusterSnapshotPersistence `json:"snapshotPersistence,omitempty"`
	// +optional
	Cluster *ClusterMode `json:"cluster,omitempty"`
	// +optional
	Service *ClusterService `json:"service,omitempty"`
	// +optional
	APIKey *ClusterAPIKey `json:"apiKey,omitempty"`
	// +optional
	ReadOnlyAPIKey *ClusterReadOnlyAPIKey `json:"readOnlyApiKey,omitempty"`
	// +optional
	TLS *ClusterTLS `json:"tls,omitempty"`
	// +optional
	Metrics *ClusterMetrics `json:"metrics,omitempty"`
	// Annotations on the Qdrant pods (e.g. scrape hints for a metrics collector)
	// +optional
	PodAnnotations map[string]string `json:"podAnnotations,omitempty"`
	// +optional
	PodLabels map[string]string `json:"podLabels,omitempty"`
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	Tolerations []ClusterToleration `json:"tolerations,omitempty"`
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Affinity *apiextensionsv1.JSON `json:"affinity,omitempty"`
	// PodDisruptionBudget of the pods; enabled by default when replicas > 1 with maxUnavailable 1
	// +optional
	PodDisruptionBudget *ClusterPodDisruptionBudget `json:"podDisruptionBudget,omitempty"`
	// Spread of pods over nodes (on by default when replicas > 1) and zones (opt in)
	// +optional
	TopologySpread *ClusterTopologySpread `json:"topologySpread,omitempty"`
	// Seconds a pod must be Ready before the rolling update moves to the next one
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=0
	MinReadySeconds *int32 `json:"minReadySeconds,omitempty"`
	// Opt-in peer management when the replica count changes or pods are rebuilt
	// +optional
	Scaling *ClusterScaling `json:"scaling,omitempty"`
	// How pods are restarted when the pod template changes
	// +optional
	Rollout *ClusterRollout `json:"rollout,omitempty"`
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Config *apiextensionsv1.JSON `json:"config,omitempty"`
}

type ClusterImage struct {
	// +optional
	// +kubebuilder:default="docker.io/qdrant/qdrant"
	Repository string `json:"repository,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=Always;IfNotPresent;Never
	// +kubebuilder:default=IfNotPresent
	PullPolicy string `json:"pullPolicy,omitempty"`
}

type ClusterResources struct {
	// +optional
	Requests *ClusterResourceList `json:"requests,omitempty"`
	// +optional
	Limits *ClusterResourceList `json:"limits,omitempty"`
}

type ClusterResourceList struct {
	// +optional
	CPU string `json:"cpu,omitempty"`
	// +optional
	Memory string `json:"memory,omitempty"`
}

type ClusterPersistence struct {
	// Volume size per pod; increases are applied to the PVCs and the StatefulSet is recreated, decreases are rejected
	// +optional
	// +kubebuilder:validation:MaxLength=20
	// +kubebuilder:default="10Gi"
	Size string `json:"size,omitempty"`
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
	// +optional
	// +kubebuilder:default={"ReadWriteOnce"}
	AccessModes []string `json:"accessModes,omitempty"`
	// Delete removes the cluster's PersistentVolumeClaims (data and snapshots) when the QdrantCluster is deleted; Retain keeps them
	// +optional
	// +kubebuilder:validation:Enum=Retain;Delete
	// +kubebuilder:default=Retain
	ReclaimPolicy string `json:"reclaimPolicy,omitempty"`
}

type ClusterSnapshotPersistence struct {
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`
	// Snapshot volume size per pod; size it for the largest snapshot of one node (a full copy of its shards)
	// +optional
	// +kubebuilder:validation:MaxLength=20
	// +kubebuilder:default="10Gi"
	Size string `json:"size,omitempty"`
	// +optional
	StorageClassName string `json:"storageClassName,omitempty"`
}

type ClusterMode struct {
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	P2P *ClusterP2P `json:"p2p,omitempty"`
}

type ClusterP2P struct {
	// +optional
	// +kubebuilder:default=6335
	Port *int32 `json:"port,omitempty"`
	// +optional
	// +kubebuilder:default=false
	EnableTLS *bool `json:"enableTls,omitempty"`
}

type ClusterService struct {
	// +optional
	// +kubebuilder:validation:Enum=ClusterIP;LoadBalancer;NodePort
	// +kubebuilder:default=ClusterIP
	Type string `json:"type,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.secretRef) && self.autoGenerate)",message="set either secretRef or autoGenerate, not both"
// +kubebuilder:validation:XValidation:rule="!self.jwtRbac || has(self.secretRef) || self.autoGenerate",message="jwtRbac needs an API key (secretRef or autoGenerate) to sign tokens with"
type ClusterAPIKey struct {
	// Enable JWT role-based access (service.jwt_rbac); QdrantAccessKey resources need it
	// +optional
	// +kubebuilder:default=false
	JWTRbac *bool `json:"jwtRbac,omitempty"`
	// +optional
	SecretRef *ClusterRequiredSecretKeyRef `json:"secretRef,omitempty"`
	// +optional
	// +kubebuilder:default=false
	AutoGenerate *bool `json:"autoGenerate,omitempty"`
}

type ClusterRequiredSecretKeyRef struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.secretRef) && self.autoGenerate)",message="set either secretRef or autoGenerate, not both"
type ClusterReadOnlyAPIKey struct {
	// +optional
	SecretRef *ClusterSecretKeyRef `json:"secretRef,omitempty"`
	// +optional
	// +kubebuilder:default=false
	AutoGenerate *bool `json:"autoGenerate,omitempty"`
}

type ClusterSecretKeyRef struct {
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	Key string `json:"key,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!self.enabled || (has(self.secretRef) && has(self.secretRef.name))",message="tls.enabled requires tls.secretRef.name (a kubernetes.io/tls secret)"
type ClusterTLS struct {
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	SecretRef *ClusterSecretName `json:"secretRef,omitempty"`
	// Key in the TLS Secret that holds the CA certificate. Qdrant verifies its peers with it when cluster.p2p.enableTls is on, and the operator verifies Qdrant's HTTPS with it.
	// +optional
	// +kubebuilder:default=ca.crt
	CAKey string `json:"caKey,omitempty"`
}

type ClusterSecretName struct {
	// +optional
	Name string `json:"name,omitempty"`
}

type ClusterMetrics struct {
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	ServiceMonitor *ClusterServiceMonitor `json:"serviceMonitor,omitempty"`
}

type ClusterServiceMonitor struct {
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	// +kubebuilder:default="30s"
	Interval string `json:"interval,omitempty"`
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

type ClusterToleration struct {
	// +optional
	Key string `json:"key,omitempty"`
	// +optional
	Operator string `json:"operator,omitempty"`
	// +optional
	Value string `json:"value,omitempty"`
	// +optional
	Effect string `json:"effect,omitempty"`
	// +optional
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="!(has(self.maxUnavailable) && has(self.minAvailable))",message="set either maxUnavailable or minAvailable, not both"
type ClusterPodDisruptionBudget struct {
	// Unset means on when replicas > 1
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`
}

type ClusterTopologySpread struct {
	// +optional
	Hostname *ClusterSpreadLevel `json:"hostname,omitempty"`
	// +optional
	Zone *ClusterZoneSpreadLevel `json:"zone,omitempty"`
	// Replaces the generated constraints; labelSelector defaults to the cluster's pods; an empty list disables spreading
	// +optional
	// +kubebuilder:validation:items:Type=object
	// +kubebuilder:validation:items:XPreserveUnknownFields
	Constraints []apiextensionsv1.JSON `json:"constraints,omitempty"`
}

type ClusterSpreadLevel struct {
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	MaxSkew *int32 `json:"maxSkew,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=ScheduleAnyway;DoNotSchedule
	// +kubebuilder:default=ScheduleAnyway
	WhenUnsatisfiable string `json:"whenUnsatisfiable,omitempty"`
}

type ClusterZoneSpreadLevel struct {
	// +optional
	// +kubebuilder:default=false
	Enabled *bool `json:"enabled,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:default=1
	MaxSkew *int32 `json:"maxSkew,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=ScheduleAnyway;DoNotSchedule
	// +kubebuilder:default=ScheduleAnyway
	WhenUnsatisfiable string `json:"whenUnsatisfiable,omitempty"`
}

type ClusterScaling struct {
	// Before removing pods, move their shard replicas to the remaining peers and remove the peers from consensus
	// +optional
	// +kubebuilder:default=false
	DrainOnScaleDown *bool `json:"drainOnScaleDown,omitempty"`
	// Re-replicate shards off peers whose pod was rebuilt on an empty volume, then remove those peers
	// +optional
	// +kubebuilder:default=false
	RemoveDeadPeers *bool `json:"removeDeadPeers,omitempty"`
	// Keep every collection at its replication factor and its replicas spread evenly over the pods, e.g. after a scale-up, a restore or a peer repair; one shard operation per collection at a time
	// +optional
	// +kubebuilder:default=false
	Rebalance *bool `json:"rebalance,omitempty"`
}

type ClusterRollout struct {
	// ShardHealth has the operator lower the StatefulSet partition one pod at a time, each only once every pod is Ready, every shard replica is Active, no transfer runs and every pod is a peer. Readiness is the plain StatefulSet RollingUpdate with partition 0
	// +optional
	// +kubebuilder:validation:Enum=ShardHealth;Readiness
	// +kubebuilder:default=ShardHealth
	Strategy string `json:"strategy,omitempty"`
}

type QdrantClusterStatus struct {
	// +optional
	// +kubebuilder:validation:Enum=Pending;Running;Failed;Upgrading;Terminating
	Phase string `json:"phase,omitempty"`
	// +optional
	Replicas int32 `json:"replicas,omitempty"`
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
	// +optional
	UpdatedReplicas int32 `json:"updatedReplicas,omitempty"`
	// +optional
	CurrentRevision string `json:"currentRevision,omitempty"`
	// +optional
	UpdateRevision string `json:"updateRevision,omitempty"`
	// Digest of the chart version and values last applied; an unchanged digest skips the helm upgrade
	// +optional
	AppliedHash string `json:"appliedHash,omitempty"`
	// +optional
	Version string `json:"version,omitempty"`
	// +optional
	HelmRelease string `json:"helmRelease,omitempty"`
	// +optional
	Endpoint string `json:"endpoint,omitempty"`
	// +optional
	Conditions []Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=qc
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type QdrantCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QdrantClusterSpec `json:"spec"`
	// +optional
	Status QdrantClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type QdrantClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []QdrantCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&QdrantCluster{}, &QdrantClusterList{})
}

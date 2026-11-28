// Package translate converts the qdrant.io API types into domain values and back.
package translate

import (
	"encoding/json"
	"fmt"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func ClusterSpecFromAPI(name string, namespace string, spec qdrantv1alpha1.QdrantClusterSpec) (domain.ClusterSpec, error) {
	cluster := domain.DefaultClusterSpec(name, namespace, spec.Version)
	if spec.Replicas != nil {
		cluster.Replicas = int(*spec.Replicas)
	}
	if spec.MinReadySeconds != nil {
		cluster.MinReadySeconds = int(*spec.MinReadySeconds)
	}
	applyImage(&cluster.Image, spec.Image)
	applyResources(&cluster.Resources, spec.Resources)
	applyPersistence(&cluster.Persistence, spec.Persistence)
	applySnapshotPersistence(&cluster.SnapshotPersistence, spec.SnapshotPersistence)
	applyDistributed(&cluster.Distributed, spec.Cluster)
	applyService(&cluster.Service, spec.Service)
	applyAPIKey(&cluster.APIKey, spec.APIKey, namespace)
	applyReadOnlyAPIKey(&cluster.ReadOnlyAPIKey, spec.ReadOnlyAPIKey, namespace)
	applyTLS(&cluster.TLS, spec.TLS)
	applyMetrics(&cluster.Metrics, spec.Metrics)
	applyScaling(&cluster.Scaling, spec.Scaling)
	applyRollout(&cluster.Rollout, spec.Rollout)
	applyDisruptionBudget(&cluster.DisruptionBudget, spec.PodDisruptionBudget)
	if spec.PodAnnotations != nil {
		cluster.PodMetadata.Annotations = spec.PodAnnotations
	}
	if spec.PodLabels != nil {
		cluster.PodMetadata.Labels = spec.PodLabels
	}
	if spec.NodeSelector != nil {
		cluster.Scheduling.NodeSelector = spec.NodeSelector
	}
	tolerations, err := TolerationsFromAPI(spec.Tolerations)
	if err != nil {
		return domain.ClusterSpec{}, err
	}
	cluster.Scheduling.Tolerations = tolerations
	affinity, err := ObjectFromAPI(spec.Affinity)
	if err != nil {
		return domain.ClusterSpec{}, fmt.Errorf("affinity: %w", err)
	}
	cluster.Scheduling.Affinity = affinity
	config, err := ObjectFromAPI(spec.Config)
	if err != nil {
		return domain.ClusterSpec{}, fmt.Errorf("config: %w", err)
	}
	cluster.Config = config
	if err := applyTopologySpread(&cluster.TopologySpread, spec.TopologySpread); err != nil {
		return domain.ClusterSpec{}, err
	}
	return cluster, nil
}

// ObjectFromAPI decodes a free-form JSON object; unset gives an empty object.
func ObjectFromAPI(value *apiextensionsv1.JSON) (domain.JSON, error) {
	object := domain.JSON{}
	if value == nil || len(value.Raw) == 0 {
		return object, nil
	}
	if err := json.Unmarshal(value.Raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return domain.JSON{}, nil
	}
	return object, nil
}

func TolerationsFromAPI(tolerations []qdrantv1alpha1.ClusterToleration) ([]domain.JSON, error) {
	result := make([]domain.JSON, 0, len(tolerations))
	for _, toleration := range tolerations {
		raw, err := json.Marshal(toleration)
		if err != nil {
			return nil, fmt.Errorf("toleration: %w", err)
		}
		object := domain.JSON{}
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, fmt.Errorf("toleration: %w", err)
		}
		result = append(result, object)
	}
	return result, nil
}

func IntOrStringValue(value *intstr.IntOrString) any {
	if value == nil {
		return nil
	}
	if value.Type == intstr.String {
		return value.StrVal
	}
	return int(value.IntVal)
}

func applyImage(image *domain.ImageSpec, spec *qdrantv1alpha1.ClusterImage) {
	if spec == nil {
		return
	}
	image.Repository = stringOr(spec.Repository, image.Repository)
	image.PullPolicy = stringOr(spec.PullPolicy, image.PullPolicy)
}

func applyResources(resources *domain.Resources, spec *qdrantv1alpha1.ClusterResources) {
	if spec == nil {
		return
	}
	resources.Requests = resourceRequirements(spec.Requests)
	resources.Limits = resourceRequirements(spec.Limits)
}

func resourceRequirements(spec *qdrantv1alpha1.ClusterResourceList) domain.ResourceRequirements {
	if spec == nil {
		return domain.ResourceRequirements{}
	}
	return domain.ResourceRequirements{CPU: spec.CPU, Memory: spec.Memory}
}

func applyPersistence(persistence *domain.PersistenceSpec, spec *qdrantv1alpha1.ClusterPersistence) {
	if spec == nil {
		return
	}
	persistence.Size = stringOr(spec.Size, persistence.Size)
	persistence.StorageClass = spec.StorageClassName
	if len(spec.AccessModes) > 0 {
		persistence.AccessModes = spec.AccessModes
	}
	persistence.ReclaimPolicy = domain.ReclaimPolicy(stringOr(spec.ReclaimPolicy, string(persistence.ReclaimPolicy)))
}

func applySnapshotPersistence(persistence *domain.SnapshotPersistenceSpec, spec *qdrantv1alpha1.ClusterSnapshotPersistence) {
	if spec == nil {
		return
	}
	persistence.Enabled = boolOr(spec.Enabled, persistence.Enabled)
	persistence.Size = stringOr(spec.Size, persistence.Size)
	persistence.StorageClass = spec.StorageClassName
}

func applyDistributed(distributed *domain.DistributedSpec, spec *qdrantv1alpha1.ClusterMode) {
	if spec == nil {
		return
	}
	distributed.Enabled = boolOr(spec.Enabled, distributed.Enabled)
	if spec.P2P == nil {
		return
	}
	if spec.P2P.Port != nil {
		distributed.P2PPort = int(*spec.P2P.Port)
	}
	distributed.P2PTLS = boolOr(spec.P2P.EnableTLS, distributed.P2PTLS)
}

func applyService(service *domain.ServiceSpec, spec *qdrantv1alpha1.ClusterService) {
	if spec == nil {
		return
	}
	service.Type = stringOr(spec.Type, service.Type)
	if spec.Annotations != nil {
		service.Annotations = spec.Annotations
	}
}

func applyAPIKey(key *domain.APIKeySpec, spec *qdrantv1alpha1.ClusterAPIKey, namespace string) {
	if spec == nil {
		return
	}
	key.JWTRbac = boolOr(spec.JWTRbac, key.JWTRbac)
	key.AutoGenerate = boolOr(spec.AutoGenerate, key.AutoGenerate)
	if spec.SecretRef == nil {
		return
	}
	key.SecretRef = &domain.SecretRef{Name: spec.SecretRef.Name, Key: spec.SecretRef.Key, Namespace: namespace}
}

func applyReadOnlyAPIKey(key *domain.APIKeySpec, spec *qdrantv1alpha1.ClusterReadOnlyAPIKey, namespace string) {
	if spec == nil {
		return
	}
	key.AutoGenerate = boolOr(spec.AutoGenerate, key.AutoGenerate)
	if spec.SecretRef == nil {
		return
	}
	key.SecretRef = &domain.SecretRef{Name: spec.SecretRef.Name, Key: spec.SecretRef.Key, Namespace: namespace}
}

func applyTLS(tls *domain.TLSSpec, spec *qdrantv1alpha1.ClusterTLS) {
	if spec == nil {
		return
	}
	tls.Enabled = boolOr(spec.Enabled, tls.Enabled)
	tls.CAKey = stringOr(spec.CAKey, tls.CAKey)
	if spec.SecretRef == nil {
		return
	}
	tls.SecretName = spec.SecretRef.Name
}

func applyMetrics(metrics *domain.MetricsSpec, spec *qdrantv1alpha1.ClusterMetrics) {
	if spec == nil {
		return
	}
	metrics.Enabled = boolOr(spec.Enabled, metrics.Enabled)
	if spec.ServiceMonitor == nil {
		return
	}
	metrics.ServiceMonitor.Enabled = boolOr(spec.ServiceMonitor.Enabled, metrics.ServiceMonitor.Enabled)
	metrics.ServiceMonitor.Interval = stringOr(spec.ServiceMonitor.Interval, metrics.ServiceMonitor.Interval)
	if spec.ServiceMonitor.Labels != nil {
		metrics.ServiceMonitor.Labels = spec.ServiceMonitor.Labels
	}
}

func applyScaling(scaling *domain.ScalingSpec, spec *qdrantv1alpha1.ClusterScaling) {
	if spec == nil {
		return
	}
	scaling.DrainOnScaleDown = boolOr(spec.DrainOnScaleDown, scaling.DrainOnScaleDown)
	scaling.RemoveDeadPeers = boolOr(spec.RemoveDeadPeers, scaling.RemoveDeadPeers)
	scaling.Rebalance = boolOr(spec.Rebalance, scaling.Rebalance)
}

func applyRollout(rollout *domain.RolloutStrategy, spec *qdrantv1alpha1.ClusterRollout) {
	if spec == nil || spec.Strategy == "" {
		return
	}
	*rollout = domain.RolloutStrategy(spec.Strategy)
}

func applyDisruptionBudget(budget *domain.DisruptionBudgetSpec, spec *qdrantv1alpha1.ClusterPodDisruptionBudget) {
	if spec == nil {
		return
	}
	budget.Enabled = spec.Enabled
	budget.MaxUnavailable = IntOrStringValue(spec.MaxUnavailable)
	budget.MinAvailable = IntOrStringValue(spec.MinAvailable)
}

func applyTopologySpread(spread *domain.TopologySpreadSpec, spec *qdrantv1alpha1.ClusterTopologySpread) error {
	if spec == nil {
		return nil
	}
	if spec.Hostname != nil {
		applySpreadLevel(&spread.Hostname, spec.Hostname.Enabled, spec.Hostname.MaxSkew, spec.Hostname.WhenUnsatisfiable)
	}
	if spec.Zone != nil {
		applySpreadLevel(&spread.Zone, spec.Zone.Enabled, spec.Zone.MaxSkew, spec.Zone.WhenUnsatisfiable)
	}
	if spec.Constraints == nil {
		return nil
	}
	spread.CustomConstraints = true
	spread.Constraints = make([]domain.JSON, 0, len(spec.Constraints))
	for _, constraint := range spec.Constraints {
		object, err := ObjectFromAPI(&constraint)
		if err != nil {
			return fmt.Errorf("topology spread constraint: %w", err)
		}
		spread.Constraints = append(spread.Constraints, object)
	}
	return nil
}

func applySpreadLevel(rule *domain.TopologyRule, enabled *bool, maxSkew *int32, whenUnsatisfiable string) {
	rule.Enabled = boolOr(enabled, rule.Enabled)
	if maxSkew != nil {
		rule.MaxSkew = int(*maxSkew)
	}
	rule.WhenUnsatisfiable = stringOr(whenUnsatisfiable, rule.WhenUnsatisfiable)
}

func stringOr(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func boolOr(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

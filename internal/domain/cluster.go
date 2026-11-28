package domain

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type ReclaimPolicy string

const (
	ReclaimRetain ReclaimPolicy = "Retain"
	ReclaimDelete ReclaimPolicy = "Delete"
)

type RolloutStrategy string

const (
	RolloutReadiness   RolloutStrategy = "Readiness"
	RolloutShardHealth RolloutStrategy = "ShardHealth"
)

type ResourceRequirements struct {
	CPU    string
	Memory string
}

func (r ResourceRequirements) HelmValues() JSON {
	values := JSON{}
	if r.CPU != "" {
		values["cpu"] = r.CPU
	}
	if r.Memory != "" {
		values["memory"] = r.Memory
	}
	return values
}

type Resources struct {
	Requests ResourceRequirements
	Limits   ResourceRequirements
}

func (r Resources) HelmValues() JSON {
	values := JSON{}
	if requests := r.Requests.HelmValues(); len(requests) > 0 {
		values["requests"] = requests
	}
	if limits := r.Limits.HelmValues(); len(limits) > 0 {
		values["limits"] = limits
	}
	return values
}

type ImageSpec struct {
	Repository string
	PullPolicy string
}

type PersistenceSpec struct {
	Size          string
	StorageClass  string
	AccessModes   []string
	ReclaimPolicy ReclaimPolicy
}

func (p PersistenceSpec) SizeBytes() (int64, error) {
	return ParseQuantity(p.Size)
}

func (p PersistenceSpec) HelmValues() JSON {
	values := JSON{"size": p.Size, "accessModes": stringsToAny(p.AccessModes)}
	if p.StorageClass != "" {
		values["storageClassName"] = p.StorageClass
	}
	return values
}

type SnapshotPersistenceSpec struct {
	Enabled      bool
	Size         string
	StorageClass string
}

func (p SnapshotPersistenceSpec) SizeBytes() (int64, error) {
	return ParseQuantity(p.Size)
}

func (p SnapshotPersistenceSpec) HelmValues() JSON {
	values := JSON{"enabled": p.Enabled, "size": p.Size}
	if p.StorageClass != "" {
		values["storageClassName"] = p.StorageClass
	}
	return values
}

type APIKeySpec struct {
	SecretRef    *SecretRef
	AutoGenerate bool
	JWTRbac      bool
}

func (a APIKeySpec) Generated() bool {
	return a.AutoGenerate && a.SecretRef == nil
}

// HelmValue is the user's secret, the operator-generated one, or false. Never true: qdrant-helm then
// renders its random key twice on install and the first upgrade rolls every pod.
func (a APIKeySpec) HelmValue(generated SecretRef) any {
	if a.SecretRef != nil {
		return a.SecretRef.HelmValueFrom()
	}
	if a.AutoGenerate {
		return generated.HelmValueFrom()
	}
	return false
}

type TLSSpec struct {
	Enabled    bool
	SecretName string
	CAKey      string
}

func (t TLSSpec) CASecretRef(namespace string) *SecretRef {
	if !t.Enabled || t.SecretName == "" {
		return nil
	}
	return &SecretRef{Name: t.SecretName, Key: t.CAKey, Namespace: namespace}
}

type ServiceSpec struct {
	Type        string
	Annotations map[string]string
}

func (s ServiceSpec) HelmValues() JSON {
	return JSON{"type": s.Type, "annotations": stringMapToAny(s.Annotations)}
}

type ServiceMonitorSpec struct {
	Enabled  bool
	Interval string
	Labels   map[string]string
}

type MetricsSpec struct {
	Enabled        bool
	ServiceMonitor ServiceMonitorSpec
}

func (m MetricsSpec) HelmValues() JSON {
	return JSON{"serviceMonitor": JSON{
		"enabled":          m.Enabled && m.ServiceMonitor.Enabled,
		"scrapeInterval":   m.ServiceMonitor.Interval,
		"additionalLabels": stringMapToAny(m.ServiceMonitor.Labels),
	}}
}

type PodMetadataSpec struct {
	Annotations map[string]string
	Labels      map[string]string
}

type SchedulingSpec struct {
	NodeSelector map[string]string
	Tolerations  []JSON
	Affinity     JSON
}

type DistributedSpec struct {
	Enabled bool
	P2PPort int
	P2PTLS  bool
}

// DisruptionBudgetSpec leaves Enabled nil to mean on when there is more than one replica.
// MaxUnavailable and MinAvailable hold an int or a percentage string, or nil when unset.
type DisruptionBudgetSpec struct {
	Enabled        *bool
	MaxUnavailable any
	MinAvailable   any
}

func (d DisruptionBudgetSpec) HelmValues(replicas int) JSON {
	enabled := replicas > 1
	if d.Enabled != nil {
		enabled = *d.Enabled
	}
	if d.MinAvailable != nil {
		return JSON{"enabled": enabled, "minAvailable": d.MinAvailable, "maxUnavailable": nil}
	}
	maxUnavailable := d.MaxUnavailable
	if maxUnavailable == nil {
		maxUnavailable = 1
	}
	return JSON{"enabled": enabled, "maxUnavailable": maxUnavailable}
}

type TopologyRule struct {
	Enabled           bool
	MaxSkew           int
	WhenUnsatisfiable string
}

func (r TopologyRule) Constraint(topologyKey string, selector map[string]string) JSON {
	return JSON{
		"maxSkew":           r.MaxSkew,
		"topologyKey":       topologyKey,
		"whenUnsatisfiable": r.WhenUnsatisfiable,
		"labelSelector":     JSON{"matchLabels": stringMapToAny(selector)},
	}
}

// TopologySpreadSpec spreads pods across nodes (on by default) and zones (opt in). With
// CustomConstraints set, Constraints replace the generated rules, even when empty.
type TopologySpreadSpec struct {
	Hostname          TopologyRule
	Zone              TopologyRule
	CustomConstraints bool
	Constraints       []JSON
}

func (t TopologySpreadSpec) HelmValues(replicas int, selector map[string]string) []any {
	if t.CustomConstraints {
		constraints := make([]any, 0, len(t.Constraints))
		for _, constraint := range t.Constraints {
			merged := JSON{"labelSelector": JSON{"matchLabels": stringMapToAny(selector)}}
			maps.Copy(merged, constraint)
			constraints = append(constraints, merged)
		}
		return constraints
	}
	constraints := []any{}
	if replicas < 2 {
		return constraints
	}
	if t.Hostname.Enabled {
		constraints = append(constraints, t.Hostname.Constraint(HostnameTopologyKey, selector))
	}
	if t.Zone.Enabled {
		constraints = append(constraints, t.Zone.Constraint(ZoneTopologyKey, selector))
	}
	return constraints
}

// ScalingSpec is opt-in peer management; with everything off the operator only changes the replica count.
type ScalingSpec struct {
	DrainOnScaleDown bool
	RemoveDeadPeers  bool
	Rebalance        bool
}

type ClusterSpec struct {
	Name                string
	Namespace           string
	Version             string
	Replicas            int
	Image               ImageSpec
	Resources           Resources
	Persistence         PersistenceSpec
	SnapshotPersistence SnapshotPersistenceSpec
	Distributed         DistributedSpec
	Service             ServiceSpec
	APIKey              APIKeySpec
	ReadOnlyAPIKey      APIKeySpec
	TLS                 TLSSpec
	Metrics             MetricsSpec
	Scheduling          SchedulingSpec
	PodMetadata         PodMetadataSpec
	DisruptionBudget    DisruptionBudgetSpec
	TopologySpread      TopologySpreadSpec
	Scaling             ScalingSpec
	Rollout             RolloutStrategy
	MinReadySeconds     int
	Config              JSON
}

// DefaultClusterSpec carries the defaults of the QdrantCluster CRD.
func DefaultClusterSpec(name string, namespace string, version string) ClusterSpec {
	return ClusterSpec{
		Name:                name,
		Namespace:           namespace,
		Version:             version,
		Replicas:            1,
		Image:               ImageSpec{Repository: "docker.io/qdrant/qdrant", PullPolicy: "IfNotPresent"},
		Persistence:         PersistenceSpec{Size: "10Gi", AccessModes: []string{"ReadWriteOnce"}, ReclaimPolicy: ReclaimRetain},
		SnapshotPersistence: SnapshotPersistenceSpec{Size: "10Gi"},
		Distributed:         DistributedSpec{Enabled: true, P2PPort: 6335},
		Service:             ServiceSpec{Type: "ClusterIP", Annotations: map[string]string{}},
		TLS:                 TLSSpec{CAKey: "ca.crt"},
		Metrics:             MetricsSpec{ServiceMonitor: ServiceMonitorSpec{Interval: "30s", Labels: map[string]string{}}},
		Scheduling:          SchedulingSpec{NodeSelector: map[string]string{}, Tolerations: []JSON{}, Affinity: JSON{}},
		PodMetadata:         PodMetadataSpec{Annotations: map[string]string{}, Labels: map[string]string{}},
		TopologySpread: TopologySpreadSpec{
			Hostname: TopologyRule{Enabled: true, MaxSkew: 1, WhenUnsatisfiable: "ScheduleAnyway"},
			Zone:     TopologyRule{MaxSkew: 1, WhenUnsatisfiable: "ScheduleAnyway"},
		},
		Rollout: RolloutShardHealth,
		Config:  JSON{},
	}
}

func (c ClusterSpec) ReleaseName() string {
	return ReleaseName(c.Name)
}

func (c ClusterSpec) GeneratedKeysSecret() string {
	return c.ReleaseName() + "-generated-keys"
}

func (c ClusterSpec) ChartKeysSecret() string {
	return c.ReleaseName() + "-apikey"
}

func (c ClusterSpec) GeneratedKeyRef(key string) SecretRef {
	return SecretRef{Name: c.GeneratedKeysSecret(), Key: key, Namespace: c.Namespace}
}

// GeneratedKeys lists the Secret keys the operator generates instead of letting the chart do it.
func (c ClusterSpec) GeneratedKeys() []string {
	keys := []string{}
	if c.APIKey.Generated() {
		keys = append(keys, AutoGeneratedAPIKeyKey)
	}
	if c.ReadOnlyAPIKey.Generated() {
		keys = append(keys, AutoGeneratedReadOnlyAPIKeyKey)
	}
	return keys
}

// APIKeyRef is where the operator reads the admin key from: the user's Secret or the chart's.
func (c ClusterSpec) APIKeyRef() *SecretRef {
	if c.APIKey.SecretRef != nil {
		return c.APIKey.SecretRef
	}
	if !c.APIKey.AutoGenerate {
		return nil
	}
	return &SecretRef{Name: c.ChartKeysSecret(), Key: AutoGeneratedAPIKeyKey, Namespace: c.Namespace}
}

// ClaimSizes is the requested size per volume claim template of the StatefulSet.
func (c ClusterSpec) ClaimSizes() map[string]string {
	sizes := map[string]string{StorageClaim: c.Persistence.Size}
	if !c.SnapshotPersistence.Enabled {
		return sizes
	}
	sizes[SnapshotClaim] = c.SnapshotPersistence.Size
	return sizes
}

func (c ClusterSpec) VolumeSelector() string {
	return "app.kubernetes.io/instance=" + c.ReleaseName()
}

// DepartedClaims returns the claims `<template>-<release>-<ordinal>` whose ordinal is at or beyond the replica count.
func (c ClusterSpec) DepartedClaims(names []string) []string {
	pattern := regexp.MustCompile(`^.+-` + regexp.QuoteMeta(c.ReleaseName()) + `-([0-9]+)$`)
	departed := []string{}
	for _, name := range names {
		match := pattern.FindStringSubmatch(name)
		if match == nil {
			continue
		}
		ordinal, err := strconv.Atoi(match[1])
		if err != nil || ordinal < c.Replicas {
			continue
		}
		departed = append(departed, name)
	}
	return departed
}

// PodSelector is the label set the qdrant-helm chart puts on the pods of this release.
func (c ClusterSpec) PodSelector() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":     ChartName,
		"app.kubernetes.io/instance": c.ReleaseName(),
	}
}

func (c ClusterSpec) PodLabelSelector() string {
	selector := c.PodSelector()
	keys := slices.Sorted(maps.Keys(selector))
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+selector[key])
	}
	return strings.Join(pairs, ",")
}

func (c ClusterSpec) Scheme() string {
	if c.TLS.Enabled {
		return "https"
	}
	return "http"
}

func (c ClusterSpec) ServiceURL() string {
	return fmt.Sprintf("%s://%s.%s.svc.%s:%d", c.Scheme(), c.ReleaseName(), c.Namespace, ClusterDomain, QdrantHTTPPort)
}

// NodeURLs has one URL per StatefulSet pod, through the chart's headless service.
func (c ClusterSpec) NodeURLs() []string {
	headless := fmt.Sprintf("%s-headless.%s.svc.%s", c.ReleaseName(), c.Namespace, ClusterDomain)
	urls := make([]string, 0, c.Replicas)
	for ordinal := range c.Replicas {
		urls = append(urls, fmt.Sprintf("%s://%s-%d.%s:%d", c.Scheme(), c.ReleaseName(), ordinal, headless, QdrantHTTPPort))
	}
	return urls
}

func (c ClusterSpec) HelmValues() JSON {
	service := JSON{"enable_tls": c.TLS.Enabled}
	if c.APIKey.JWTRbac {
		service["jwt_rbac"] = true
	}
	config := MergeJSON(
		JSON{
			"cluster": JSON{
				"enabled": c.Distributed.Enabled,
				"p2p":     JSON{"port": c.Distributed.P2PPort, "enable_tls": c.Distributed.P2PTLS},
			},
			"service": service,
		},
		c.TLSConfig(),
		c.Config,
	)
	values := JSON{
		"replicaCount":              c.Replicas,
		"image":                     JSON{"repository": c.Image.Repository, "pullPolicy": c.Image.PullPolicy},
		"resources":                 c.Resources.HelmValues(),
		"persistence":               c.Persistence.HelmValues(),
		"snapshotPersistence":       c.SnapshotPersistence.HelmValues(),
		"service":                   c.Service.HelmValues(),
		"apiKey":                    c.APIKey.HelmValue(c.GeneratedKeyRef(AutoGeneratedAPIKeyKey)),
		"readOnlyApiKey":            c.ReadOnlyAPIKey.HelmValue(c.GeneratedKeyRef(AutoGeneratedReadOnlyAPIKeyKey)),
		"metrics":                   c.Metrics.HelmValues(),
		"nodeSelector":              stringMapToAny(c.Scheduling.NodeSelector),
		"tolerations":               jsonListToAny(c.Scheduling.Tolerations),
		"affinity":                  maps.Clone(c.Scheduling.Affinity),
		"podAnnotations":            stringMapToAny(c.PodMetadata.Annotations),
		"podLabels":                 stringMapToAny(c.PodMetadata.Labels),
		"podDisruptionBudget":       c.DisruptionBudget.HelmValues(c.Replicas),
		"topologySpreadConstraints": c.TopologySpread.HelmValues(c.Replicas, c.PodSelector()),
		"minReadySeconds":           c.MinReadySeconds,
		"config":                    config,
	}
	maps.Copy(values, c.TLSVolumes())
	return values
}

func (c ClusterSpec) TLSConfig() JSON {
	if !c.TLS.Enabled {
		return JSON{}
	}
	files := JSON{"cert": TLSMountPath + "/tls.crt", "key": TLSMountPath + "/tls.key"}
	if !c.Distributed.P2PTLS {
		return JSON{"tls": files}
	}
	files["ca_cert"] = TLSMountPath + "/" + c.TLS.CAKey
	return JSON{"tls": files}
}

func (c ClusterSpec) TLSVolumes() JSON {
	if !c.TLS.Enabled || c.TLS.SecretName == "" {
		return JSON{}
	}
	return JSON{
		"additionalVolumes":      []any{JSON{"name": "tls", "secret": JSON{"secretName": c.TLS.SecretName}}},
		"additionalVolumeMounts": []any{JSON{"name": "tls", "mountPath": TLSMountPath, "readOnly": true}},
	}
}

func stringMapToAny(values map[string]string) JSON {
	result := make(JSON, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func stringsToAny(values []string) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

func jsonListToAny(values []JSON) []any {
	result := make([]any, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}

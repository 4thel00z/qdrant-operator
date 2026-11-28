package fakes

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

type StatusPatch struct {
	Ref    domain.ResourceRef
	Status domain.JSON
}

type ClaimResize struct {
	Name string
	Size string
}

// Kubernetes is an in-memory ports.Kubernetes. Maps are keyed by "namespace/name"; tests set them
// up before use and read them afterwards.
type Kubernetes struct {
	mu              sync.Mutex
	Secrets         map[string]map[string]string
	SecretOwners    map[string]domain.OwnerReference
	Resources       map[string]domain.JSON
	StatefulSets    map[string]domain.StatefulSetStatus
	ClaimTemplates  map[string]map[string]string
	VolumeClaims    map[string]map[string]string
	ClaimDetails    map[string]domain.VolumeClaim
	CreatedClaims   map[string]domain.JSON
	Pods            map[string][]domain.PodState
	PodLogs         map[string]string
	VolumeSnapshots map[string]domain.JSON
	SnapshotReady   bool
	SnapshotErrors  map[string]string
	VolumeZones     map[string]domain.Zone
	ZoneNodes       map[domain.Zone]string
	StatusPatches   []StatusPatch
	Deleted         []domain.ResourceRef
	Resized         []ClaimResize
	Orphaned        []string
	Partitions      []int
	Clock           time.Time
}

var _ ports.Kubernetes = (*Kubernetes)(nil)

func NewKubernetes() *Kubernetes {
	return &Kubernetes{
		Secrets:         map[string]map[string]string{},
		SecretOwners:    map[string]domain.OwnerReference{},
		Resources:       map[string]domain.JSON{},
		StatefulSets:    map[string]domain.StatefulSetStatus{},
		ClaimTemplates:  map[string]map[string]string{},
		VolumeClaims:    map[string]map[string]string{},
		ClaimDetails:    map[string]domain.VolumeClaim{},
		CreatedClaims:   map[string]domain.JSON{},
		Pods:            map[string][]domain.PodState{},
		PodLogs:         map[string]string{},
		VolumeSnapshots: map[string]domain.JSON{},
		SnapshotReady:   true,
		SnapshotErrors:  map[string]string{},
		VolumeZones:     map[string]domain.Zone{},
		ZoneNodes:       map[domain.Zone]string{},
		Clock:           time.Date(2026, 9, 23, 2, 30, 0, 0, time.UTC),
	}
}

func objectKey(namespace, name string) string {
	return namespace + "/" + name
}

func resourceKey(plural, namespace, name string) string {
	return plural + "/" + namespace + "/" + name
}

func PluralOf(kind string) string {
	return strings.ToLower(kind) + "s"
}

func metadataOf(body domain.JSON) domain.JSON {
	metadata, ok := body["metadata"].(domain.JSON)
	if !ok {
		metadata = domain.JSON{}
		body["metadata"] = metadata
	}
	return metadata
}

func labelsOf(body domain.JSON) domain.JSON {
	labels, _ := metadataOf(body)["labels"].(domain.JSON)
	return labels
}

func matchesSelector(labels domain.JSON, selector string) bool {
	if selector == "" {
		return true
	}
	for _, term := range strings.Split(selector, ",") {
		key, value, _ := strings.Cut(term, "=")
		if labels[key] != value {
			return false
		}
	}
	return true
}

// PutResource stores a custom resource body, filling uid and creationTimestamp like the API server.
func (k *Kubernetes) PutResource(body domain.JSON) domain.JSON {
	metadata := metadataOf(body)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	kind, _ := body["kind"].(string)
	if _, ok := metadata["uid"]; !ok {
		metadata["uid"] = "uid-" + name
	}
	if _, ok := metadata["creationTimestamp"]; !ok {
		metadata["creationTimestamp"] = domain.FormatTime(k.Clock)
	}
	k.Resources[resourceKey(PluralOf(kind), namespace, name)] = body
	return body
}

func (k *Kubernetes) SecretValue(ctx context.Context, ref domain.SecretRef) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	data, ok := k.Secrets[objectKey(ref.Namespace, ref.Name)]
	if !ok {
		return "", fmt.Errorf("%s/%s:%s: %w", ref.Namespace, ref.Name, ref.Key, ports.ErrSecretKeyNotFound)
	}
	value, ok := data[ref.Key]
	if !ok {
		return "", fmt.Errorf("%s/%s:%s: %w", ref.Namespace, ref.Name, ref.Key, ports.ErrSecretKeyNotFound)
	}
	return value, nil
}

func (k *Kubernetes) ApplySecret(ctx context.Context, name, namespace string, data map[string]string, owner domain.OwnerReference) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := objectKey(namespace, name)
	current, owned := k.SecretOwners[key]
	_, exists := k.Secrets[key]
	if exists && (!owned || current.UID != owner.UID) {
		return fmt.Errorf("Secret %s: %w", key, ports.ErrSecretNotOwned)
	}
	k.Secrets[key] = maps.Clone(data)
	k.SecretOwners[key] = owner
	return nil
}

func (k *Kubernetes) CustomResource(ctx context.Context, ref domain.ResourceRef) (domain.JSON, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.Resources[resourceKey(ref.Kind.Plural, ref.Namespace, ref.Name)], nil
}

func (k *Kubernetes) ListCustomResources(ctx context.Context, kind domain.ResourceKind, namespace, labelSelector string) ([]domain.JSON, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	prefix := kind.Plural + "/" + namespace + "/"
	bodies := []domain.JSON{}
	for _, key := range slices.Sorted(maps.Keys(k.Resources)) {
		body := k.Resources[key]
		if !strings.HasPrefix(key, prefix) || !matchesSelector(labelsOf(body), labelSelector) {
			continue
		}
		bodies = append(bodies, body)
	}
	return bodies, nil
}

func (k *Kubernetes) CreateCustomResource(ctx context.Context, body domain.JSON) (domain.JSON, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.PutResource(body), nil
}

func (k *Kubernetes) DeleteCustomResource(ctx context.Context, ref domain.ResourceRef) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.Deleted = append(k.Deleted, ref)
	delete(k.Resources, resourceKey(ref.Kind.Plural, ref.Namespace, ref.Name))
	return nil
}

func (k *Kubernetes) PatchStatus(ctx context.Context, ref domain.ResourceRef, status domain.JSON) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.StatusPatches = append(k.StatusPatches, StatusPatch{Ref: ref, Status: status})
	body, ok := k.Resources[resourceKey(ref.Kind.Plural, ref.Namespace, ref.Name)]
	if !ok {
		return nil
	}
	current, _ := body["status"].(domain.JSON)
	merged := maps.Clone(current)
	if merged == nil {
		merged = domain.JSON{}
	}
	for key, value := range status {
		if value == nil {
			delete(merged, key)
			continue
		}
		merged[key] = value
	}
	body["status"] = merged
	return nil
}

func (k *Kubernetes) StatefulSetStatus(ctx context.Context, name, namespace string) (*domain.StatefulSetStatus, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	status, ok := k.StatefulSets[objectKey(namespace, name)]
	if !ok {
		return nil, nil
	}
	return &status, nil
}

func (k *Kubernetes) SetStatefulSetPartition(ctx context.Context, name, namespace string, partition int) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := objectKey(namespace, name)
	status, ok := k.StatefulSets[key]
	if !ok {
		return fmt.Errorf("StatefulSet %s not found", key)
	}
	status.Partition = partition
	k.StatefulSets[key] = status
	k.Partitions = append(k.Partitions, partition)
	return nil
}

func (k *Kubernetes) OrphanStatefulSet(ctx context.Context, name, namespace string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	key := objectKey(namespace, name)
	delete(k.ClaimTemplates, key)
	delete(k.StatefulSets, key)
	k.Orphaned = append(k.Orphaned, key)
	return nil
}

func (k *Kubernetes) StatefulSetClaims(ctx context.Context, name, namespace string) (map[string]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return maps.Clone(k.ClaimTemplates[objectKey(namespace, name)]), nil
}

func (k *Kubernetes) ListPodStates(ctx context.Context, namespace, labelSelector string) ([]domain.PodState, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.Pods[namespace]), nil
}

func (k *Kubernetes) CrashLog(ctx context.Context, name, namespace string, tailLines int) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.PodLogs[objectKey(namespace, name)], nil
}

func (k *Kubernetes) ListVolumeClaims(ctx context.Context, namespace, labelSelector string) ([]domain.VolumeClaim, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	sizes := k.VolumeClaims[namespace]
	claims := make([]domain.VolumeClaim, 0, len(sizes))
	for _, name := range slices.Sorted(maps.Keys(sizes)) {
		claim, ok := k.ClaimDetails[name]
		if !ok {
			claim = domain.DefaultVolumeClaim(name, sizes[name])
		}
		claims = append(claims, claim)
	}
	return claims, nil
}

func (k *Kubernetes) CreateVolumeClaim(ctx context.Context, body domain.JSON) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	metadata := metadataOf(body)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	k.CreatedClaims[objectKey(namespace, name)] = body
	if k.VolumeClaims[namespace] == nil {
		k.VolumeClaims[namespace] = map[string]string{}
	}
	k.VolumeClaims[namespace][name] = requestedStorage(body)
	return nil
}

func requestedStorage(body domain.JSON) string {
	spec, _ := body["spec"].(domain.JSON)
	resources, _ := spec["resources"].(domain.JSON)
	requests, _ := resources["requests"].(domain.JSON)
	storage, _ := requests["storage"].(string)
	return storage
}

func (k *Kubernetes) ResizeVolumeClaim(ctx context.Context, name, namespace, size string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.VolumeClaims[namespace] == nil {
		return fmt.Errorf("PersistentVolumeClaim %s not found", objectKey(namespace, name))
	}
	k.VolumeClaims[namespace][name] = size
	k.Resized = append(k.Resized, ClaimResize{Name: name, Size: size})
	return nil
}

func (k *Kubernetes) DeleteVolumeClaim(ctx context.Context, name, namespace string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.VolumeClaims[namespace], name)
	return nil
}

func (k *Kubernetes) VolumeZone(ctx context.Context, volumeName string) (*domain.Zone, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	zone, ok := k.VolumeZones[volumeName]
	if !ok {
		return nil, nil
	}
	return &zone, nil
}

func (k *Kubernetes) NodeInZone(ctx context.Context, zone domain.Zone) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.ZoneNodes[zone], nil
}

func (k *Kubernetes) CreateVolumeSnapshot(ctx context.Context, body domain.JSON) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	metadata := metadataOf(body)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	key := objectKey(namespace, name)
	if _, ok := k.VolumeSnapshots[key]; ok {
		return nil
	}
	k.VolumeSnapshots[key] = body
	return nil
}

func (k *Kubernetes) VolumeSnapshot(ctx context.Context, name, namespace string) (*domain.VolumeSnapshotState, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.VolumeSnapshots[objectKey(namespace, name)]; !ok {
		return nil, nil
	}
	return &domain.VolumeSnapshotState{Name: name, Ready: k.SnapshotReady, Error: k.SnapshotErrors[name]}, nil
}

func (k *Kubernetes) ListVolumeSnapshots(ctx context.Context, namespace, labelSelector string) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	names := []string{}
	for _, key := range slices.Sorted(maps.Keys(k.VolumeSnapshots)) {
		snapshotNamespace, name, _ := strings.Cut(key, "/")
		if snapshotNamespace != namespace || !matchesSelector(labelsOf(k.VolumeSnapshots[key]), labelSelector) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

func (k *Kubernetes) DeleteVolumeSnapshot(ctx context.Context, name, namespace string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.VolumeSnapshots, objectKey(namespace, name))
	return nil
}

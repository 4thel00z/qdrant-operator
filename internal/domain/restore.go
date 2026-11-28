package domain

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

type RestorePhase string

const (
	RestorePending     RestorePhase = "Pending"
	RestoreDownloading RestorePhase = "Downloading"
	RestoreRestoring   RestorePhase = "Restoring"
	RestoreIndexing    RestorePhase = "Indexing"
	RestoreCompleted   RestorePhase = "Completed"
	RestoreFailed      RestorePhase = "Failed"
)

type RestorePriority string

const (
	PrioritySnapshot RestorePriority = "snapshot"
	PriorityReplica  RestorePriority = "replica"
	PriorityNoSync   RestorePriority = "no_sync"
)

const (
	SelectedNodeAnnotation = "volume.kubernetes.io/selected-node"
	DefaultIndexingTimeout = time.Hour
)

type BackupRef struct {
	Name      string
	Namespace string
}

func (r BackupRef) ResourceRef() ResourceRef {
	return ResourceRef{Kind: Backups, Name: r.Name, Namespace: r.Namespace}
}

type RestoreSpec struct {
	Name              string
	Namespace         string
	TargetClusterRef  ClusterRef
	BackupRef         *BackupRef
	SourceS3          *S3StorageSpec
	Collections       []string
	CollectionMapping map[string]string
	Priority          RestorePriority
	WaitForIndexing   bool
	IndexingTimeout   time.Duration
}

func DefaultRestoreSpec(name string, namespace string, target ClusterRef) RestoreSpec {
	return RestoreSpec{
		Name:              name,
		Namespace:         namespace,
		TargetClusterRef:  target,
		Collections:       []string{},
		CollectionMapping: map[string]string{},
		Priority:          PrioritySnapshot,
		WaitForIndexing:   true,
		IndexingTimeout:   DefaultIndexingTimeout,
	}
}

func (r RestoreSpec) TargetName(collection string) string {
	if target, ok := r.CollectionMapping[collection]; ok {
		return target
	}
	return collection
}

func (r RestoreSpec) SelectCollections(available []string) ([]string, error) {
	return SelectCollections(r.Collections, available, "backup")
}

// SelectCollections returns the requested collections, or every available one; a request for a missing collection is an error.
func SelectCollections(requested []string, available []string, source string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string{}, available...), nil
	}
	missing := []string{}
	for _, name := range requested {
		if !slices.Contains(available, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("Collections not present in %s: %s", source, strings.Join(missing, ", "))
	}
	return append([]string{}, requested...), nil
}

// IndexingTimeoutMessage explains a collection that stayed yellow, naming optimizer settings that were restored as disabled.
func IndexingTimeoutMessage(spec RestoreSpec, collection string, info JSON) string {
	status := "unknown"
	if value, ok := info["status"]; ok && value != nil {
		status = fmt.Sprint(value)
	}
	message := fmt.Sprintf(
		"Collection %s did not turn green within %ss (status %s)",
		collection, strconv.FormatFloat(spec.IndexingTimeout.Seconds(), 'g', 6, 64), status,
	)
	config, _ := info["config"].(map[string]any)
	optimizers, _ := config["optimizer_config"].(map[string]any)
	disabled := []string{}
	for _, key := range []string{"indexing_threshold", "max_optimization_threads"} {
		if optimizerDisabled(optimizers[key]) {
			disabled = append(disabled, "optimizer_config."+key)
		}
	}
	if len(disabled) == 0 {
		return message
	}
	return fmt.Sprintf("%s; indexing is disabled in the restored config (%s is 0)", message, strings.Join(disabled, ", "))
}

func optimizerDisabled(value any) bool {
	switch number := value.(type) {
	case int:
		return number == 0
	case int64:
		return number == 0
	case float64:
		return number == 0
	}
	return false
}

// RestoreClaimBody is the PVC of a pod's data volume, filled from its VolumeSnapshot and named so the StatefulSet adopts it.
// A node in the snapshot's zone is pre-selected so the volume is provisioned where the snapshot lives;
// the scheduler would otherwise pick any zone and drivers such as Cinder refuse.
func RestoreClaimBody(target ClusterRef, record VolumeSnapshotRecord, snapshotNamespace string, node string) JSON {
	metadata := JSON{
		"name":      dataVolumeClaimName(target.Name, record.NodeIndex),
		"namespace": snapshotNamespace,
		"labels": JSON{
			"app":                        ChartName,
			"app.kubernetes.io/name":     ChartName,
			"app.kubernetes.io/instance": ReleaseName(target.Name),
		},
	}
	if node != "" {
		metadata["annotations"] = JSON{SelectedNodeAnnotation: node}
	}
	spec := JSON{
		"accessModes": append([]string{}, record.AccessModes...),
		"resources":   JSON{"requests": JSON{"storage": record.Size}},
		"dataSource": JSON{
			"apiGroup": SnapshotAPIGroup,
			"kind":     "VolumeSnapshot",
			"name":     record.SnapshotName,
		},
	}
	if record.StorageClass != "" {
		spec["storageClassName"] = record.StorageClass
	}
	return JSON{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": metadata, "spec": spec}
}

// PlanVolumeRestore returns the PVC bodies that bring a volumeSnapshot backup back, or an error naming the broken rule.
// The raft state and peer ids live on the volume, so the target must be a fresh cluster of the source's name and size
// in the source's namespace: VolumeSnapshots are namespaced and the pod and claim names derive from the cluster name.
func PlanVolumeRestore(
	spec RestoreSpec,
	source BackupSpec,
	records []VolumeSnapshotRecord,
	targetReplicas *int,
	deployed bool,
	existingClaims []string,
	nodes map[int]string,
) ([]JSON, error) {
	target := spec.TargetClusterRef
	if len(spec.Collections) > 0 || len(spec.CollectionMapping) > 0 {
		return nil, fmt.Errorf("volumeSnapshot backups restore whole volumes; drop collections and collectionMapping")
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("QdrantBackup %s recorded no volume snapshots", source.Name)
	}
	if target.Namespace != source.ClusterRef.Namespace {
		return nil, fmt.Errorf("VolumeSnapshots live in namespace %s; target cluster is in %s", source.ClusterRef.Namespace, target.Namespace)
	}
	if target.Name != source.ClusterRef.Name {
		return nil, fmt.Errorf(
			"Target cluster must be named %s like the source: pod and claim names carry the cluster name and the volumes hold its peer ids",
			source.ClusterRef.Name,
		)
	}
	if targetReplicas != nil && *targetReplicas != len(records) {
		return nil, fmt.Errorf("Backup holds %d volumes but target cluster has %d replicas; sizes must match", len(records), *targetReplicas)
	}
	if deployed {
		return nil, fmt.Errorf(
			"Cluster %s is already deployed; restore into a new cluster, after deleting the old one and its volume claims",
			target.Name,
		)
	}
	clashing := []string{}
	for _, record := range records {
		claim := dataVolumeClaimName(target.Name, record.NodeIndex)
		if slices.Contains(existingClaims, claim) && !slices.Contains(clashing, claim) {
			clashing = append(clashing, claim)
		}
	}
	if len(clashing) > 0 {
		slices.Sort(clashing)
		return nil, fmt.Errorf("Volume claims already exist: %s", strings.Join(clashing, ", "))
	}
	claims := make([]JSON, 0, len(records))
	for _, record := range records {
		claims = append(claims, RestoreClaimBody(target, record, source.ClusterRef.Namespace, nodes[record.NodeIndex]))
	}
	return claims, nil
}

type RestoredCollection struct {
	Name         string
	Status       RestorePhase
	OriginalName string
	Size         string
	PointsCount  int64
	Error        string
}

type RestoreProgress struct {
	CollectionsTotal     int
	CollectionsCompleted int
	CurrentCollection    string
}

// Percentage rounds half to even, so 12.5 becomes 12.
func (p RestoreProgress) Percentage() int {
	if p.CollectionsTotal == 0 {
		return 0
	}
	return int(math.RoundToEven(100 * float64(p.CollectionsCompleted) / float64(p.CollectionsTotal)))
}

type RestoreStatus struct {
	Phase               RestorePhase
	StartTime           time.Time
	CompletionTime      time.Time
	SourceBackup        string
	RestoredCollections []RestoredCollection
	Progress            RestoreProgress
	Error               string
	Conditions          []Condition
}

package usecases

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

const (
	DefaultVolumeSnapshotReadyTimeout = time.Hour
	DefaultVolumeSnapshotPollInterval = 2 * time.Second
)

// ExecuteVolumeSnapshotBackup backs a cluster up with one CSI VolumeSnapshot per data volume.
// The snapshots are crash-consistent: Qdrant offers no flush call, so each volume is captured as it stands
// and Qdrant replays its write-ahead log when a restore starts from it.
// A snapshot that fails or is not ready in time is reported in the returned status, not as an error.
type ExecuteVolumeSnapshotBackup struct {
	Kubernetes   ports.Kubernetes
	Clock        Clock
	ReadyTimeout time.Duration
	PollInterval time.Duration
}

func NewExecuteVolumeSnapshotBackup(kubernetes ports.Kubernetes, clock Clock) ExecuteVolumeSnapshotBackup {
	return ExecuteVolumeSnapshotBackup{
		Kubernetes:   kubernetes,
		Clock:        clock,
		ReadyTimeout: DefaultVolumeSnapshotReadyTimeout,
		PollInterval: DefaultVolumeSnapshotPollInterval,
	}
}

func (e ExecuteVolumeSnapshotBackup) Execute(ctx context.Context, spec domain.BackupSpec, ref domain.ResourceRef, previous *domain.BackupStatus) (domain.BackupStatus, error) {
	startTime := backupStartTime(previous, e.Clock)
	if spec.VolumeSnapshotClass == "" {
		return domain.BackupStatus{}, fmt.Errorf("volumeSnapshotClassName is required when method is %s", domain.BackupMethodVolumeSnapshot)
	}
	connection, err := ResolveCluster{Kubernetes: e.Kubernetes}.Execute(ctx, spec.ClusterRef)
	if err != nil {
		return domain.BackupStatus{}, err
	}
	if err := patchBackupStatus(ctx, e.Kubernetes, ref, domain.BackupStatus{Phase: domain.BackupInProgress, StartTime: startTime}); err != nil {
		return domain.BackupStatus{}, err
	}
	namespace := spec.ClusterRef.Namespace
	deleter := DeleteVolumeSnapshots{Kubernetes: e.Kubernetes}
	if _, err := deleter.Execute(ctx, spec); err != nil {
		return domain.BackupStatus{}, err
	}
	listed, err := e.Kubernetes.ListVolumeClaims(ctx, namespace, "app.kubernetes.io/instance="+domain.ReleaseName(spec.ClusterRef.Name))
	if err != nil {
		return domain.BackupStatus{}, fmt.Errorf("list volume claims in %s: %w", namespace, err)
	}
	claims := map[string]domain.VolumeClaim{}
	for _, claim := range listed {
		claims[claim.Name] = claim
	}
	records := make([]domain.VolumeSnapshotRecord, 0, len(connection.Nodes))
	for index := range connection.Nodes {
		record, err := e.record(ctx, spec, index, claims)
		if err != nil {
			return domain.BackupStatus{}, err
		}
		records = append(records, record)
	}
	if err := e.snapshotAll(ctx, spec, records); err != nil {
		if ctx.Err() != nil {
			return domain.BackupStatus{}, ctx.Err()
		}
		if _, cleanupErr := deleter.Execute(ctx, spec); cleanupErr != nil {
			return domain.BackupStatus{}, fmt.Errorf("clean up after %q: %w", err.Error(), cleanupErr)
		}
		return e.failed(startTime, err.Error()), nil
	}
	var total int64
	for _, record := range records {
		size, err := domain.ParseQuantity(record.Size)
		if err != nil {
			return domain.BackupStatus{}, fmt.Errorf("volume snapshot %s: %w", record.SnapshotName, err)
		}
		total += size
	}
	completionTime := backupNow(e.Clock)
	result := domain.BackupStatus{
		Phase:           domain.BackupCompleted,
		StartTime:       startTime,
		CompletionTime:  completionTime,
		TotalSize:       domain.FormatSize(total),
		VolumeSnapshots: records,
		Conditions: []domain.Condition{
			backupCondition(true, completionTime, "BackupCompleted", "BackupFailed", fmt.Sprintf("Snapshotted %d volumes", len(records))),
		},
	}
	result.ExpiresAt, _ = spec.ExpiresAt(completionTime)
	return result, nil
}

func (e ExecuteVolumeSnapshotBackup) record(ctx context.Context, spec domain.BackupSpec, index int, claims map[string]domain.VolumeClaim) (domain.VolumeSnapshotRecord, error) {
	name := domain.DataClaimName(spec.ClusterRef.Name, index)
	claim, ok := claims[name]
	if !ok {
		return domain.VolumeSnapshotRecord{}, fmt.Errorf("volume claim %s/%s not found", spec.ClusterRef.Namespace, name)
	}
	record := domain.VolumeSnapshotRecord{
		NodeIndex:    index,
		ClaimName:    name,
		SnapshotName: spec.VolumeSnapshotName(index),
		Size:         claim.Size,
		StorageClass: claim.StorageClass,
		AccessModes:  claim.AccessModes,
	}
	if claim.VolumeName == "" {
		return record, nil
	}
	zone, err := e.Kubernetes.VolumeZone(ctx, claim.VolumeName)
	if err != nil {
		return domain.VolumeSnapshotRecord{}, fmt.Errorf("zone of volume %s: %w", claim.VolumeName, err)
	}
	if zone != nil {
		record.Zone = &domain.VolumeZone{Label: zone.Label, Name: zone.Name}
	}
	return record, nil
}

func (e ExecuteVolumeSnapshotBackup) snapshotAll(ctx context.Context, spec domain.BackupSpec, records []domain.VolumeSnapshotRecord) error {
	names := make([]string, 0, len(records))
	for index := range records {
		if err := e.Kubernetes.CreateVolumeSnapshot(ctx, spec.VolumeSnapshotBody(index)); err != nil {
			return fmt.Errorf("create VolumeSnapshot %s: %w", spec.VolumeSnapshotName(index), err)
		}
		names = append(names, records[index].SnapshotName)
	}
	return e.awaitReady(ctx, spec, names)
}

func (e ExecuteVolumeSnapshotBackup) awaitReady(ctx context.Context, spec domain.BackupSpec, names []string) error {
	namespace := spec.ClusterRef.Namespace
	deadline := time.Now().Add(e.ReadyTimeout)
	pending := slices.Clone(names)
	for len(pending) > 0 {
		states := make([]*domain.VolumeSnapshotState, 0, len(pending))
		for _, name := range pending {
			state, err := e.Kubernetes.VolumeSnapshot(ctx, name, namespace)
			if err != nil {
				return fmt.Errorf("read VolumeSnapshot %s: %w", name, err)
			}
			states = append(states, state)
		}
		waiting := []string{}
		for index, name := range pending {
			state := states[index]
			if state != nil && state.Error != "" {
				return fmt.Errorf("VolumeSnapshot %s failed: %s", name, state.Error)
			}
			if state == nil || !state.Ready {
				waiting = append(waiting, name)
			}
		}
		pending = waiting
		if len(pending) == 0 {
			return nil
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf(
				"VolumeSnapshots not readyToUse within %ss: %s",
				strconv.FormatFloat(e.ReadyTimeout.Seconds(), 'g', -1, 64), strings.Join(pending, ", "),
			)
		}
		if err := backupSleep(ctx, e.PollInterval); err != nil {
			return err
		}
	}
	return nil
}

func (e ExecuteVolumeSnapshotBackup) failed(startTime time.Time, message string) domain.BackupStatus {
	completionTime := backupNow(e.Clock)
	return domain.BackupStatus{
		Phase:          domain.BackupFailed,
		StartTime:      startTime,
		CompletionTime: completionTime,
		Error:          message,
		Conditions:     []domain.Condition{backupCondition(false, completionTime, "BackupCompleted", "BackupFailed", message)},
	}
}

func backupSleep(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// DeleteVolumeSnapshots deletes the VolumeSnapshots labelled with the backup's name and returns how many there were.
type DeleteVolumeSnapshots struct {
	Kubernetes ports.Kubernetes
}

func (d DeleteVolumeSnapshots) Execute(ctx context.Context, spec domain.BackupSpec) (int, error) {
	namespace := spec.ClusterRef.Namespace
	names, err := d.Kubernetes.ListVolumeSnapshots(ctx, namespace, spec.SnapshotSelector())
	if err != nil {
		return 0, fmt.Errorf("list VolumeSnapshots of backup %s: %w", spec.Name, err)
	}
	for _, name := range names {
		if err := d.Kubernetes.DeleteVolumeSnapshot(ctx, name, namespace); err != nil {
			return 0, fmt.Errorf("delete VolumeSnapshot %s: %w", name, err)
		}
	}
	return len(names), nil
}

// SourceBackup is a completed QdrantBackup a restore reads from.
type SourceBackup struct {
	Spec   domain.BackupSpec
	Status domain.BackupStatus
}

// RestoreVolumeSnapshots pre-creates the data PVCs of a new cluster from the VolumeSnapshots of a backup.
// The qdrant-helm chart gives its volumeClaimTemplates no dataSource, but a StatefulSet adopts existing
// PVCs of the names it would create, so the Helm install then starts from the data.
// A target that cannot be restored into is reported in the returned status, not as an error.
type RestoreVolumeSnapshots struct {
	Kubernetes ports.Kubernetes
	Clock      Clock
}

func (r RestoreVolumeSnapshots) Execute(ctx context.Context, spec domain.RestoreSpec, source SourceBackup) (domain.RestoreStatus, error) {
	startTime := backupNow(r.Clock)
	target := spec.TargetClusterRef
	body, err := r.Kubernetes.CustomResource(ctx, target.ResourceRef())
	if err != nil {
		return domain.RestoreStatus{}, err
	}
	var replicas *int
	if body != nil {
		cluster, err := ClusterSpecFromBody(body)
		if err != nil {
			return domain.RestoreStatus{}, err
		}
		replicas = &cluster.Replicas
	}
	nodes := map[int]string{}
	unplaceable := []string{}
	for _, record := range source.Status.VolumeSnapshots {
		if record.Zone == nil {
			continue
		}
		node, err := r.Kubernetes.NodeInZone(ctx, domain.Zone{Label: record.Zone.Label, Name: record.Zone.Name})
		if err != nil {
			return domain.RestoreStatus{}, fmt.Errorf("find a node in zone %s: %w", record.Zone.Name, err)
		}
		if node == "" {
			if !slices.Contains(unplaceable, record.Zone.Name) {
				unplaceable = append(unplaceable, record.Zone.Name)
			}
			continue
		}
		nodes[record.NodeIndex] = node
	}
	if len(unplaceable) > 0 {
		slices.Sort(unplaceable)
		message := fmt.Sprintf("No Ready node in zone %s, where the snapshots live", strings.Join(unplaceable, ", "))
		return r.status(source, startTime, message, true), nil
	}
	statefulSet, err := r.Kubernetes.StatefulSetStatus(ctx, domain.ReleaseName(target.Name), target.Namespace)
	if err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("read StatefulSet of %s: %w", target.Name, err)
	}
	existing, err := r.Kubernetes.ListVolumeClaims(ctx, target.Namespace, "app.kubernetes.io/instance="+domain.ReleaseName(target.Name))
	if err != nil {
		return domain.RestoreStatus{}, fmt.Errorf("list volume claims in %s: %w", target.Namespace, err)
	}
	existingNames := make([]string, 0, len(existing))
	for _, claim := range existing {
		existingNames = append(existingNames, claim.Name)
	}
	claims, err := domain.PlanVolumeRestore(spec, source.Spec, source.Status.VolumeSnapshots, replicas, statefulSet != nil, existingNames, nodes)
	if err != nil {
		return r.status(source, startTime, err.Error(), true), nil
	}
	for _, claim := range claims {
		if err := r.Kubernetes.CreateVolumeClaim(ctx, claim); err != nil {
			return domain.RestoreStatus{}, fmt.Errorf("create volume claim from snapshot: %w", err)
		}
	}
	message := fmt.Sprintf(
		"Created %d volume claims from VolumeSnapshots; create QdrantCluster %s with %d replicas to start from them",
		len(claims), target.Name, len(claims),
	)
	return r.status(source, startTime, message, false), nil
}

func (r RestoreVolumeSnapshots) status(source SourceBackup, startTime time.Time, message string, failed bool) domain.RestoreStatus {
	completionTime := backupNow(r.Clock)
	status := domain.RestoreStatus{
		Phase:          domain.RestoreCompleted,
		StartTime:      startTime,
		CompletionTime: completionTime,
		SourceBackup:   source.Spec.Name,
		Conditions:     []domain.Condition{backupCondition(true, completionTime, "VolumesRestored", "IncompatibleTarget", message)},
	}
	if !failed {
		return status
	}
	status.Phase = domain.RestoreFailed
	status.Error = message
	status.Conditions = []domain.Condition{backupCondition(false, completionTime, "VolumesRestored", "IncompatibleTarget", message)}
	return status
}

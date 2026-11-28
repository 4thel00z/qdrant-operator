package domain

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"
)

type BackupPhase string

const (
	BackupPending    BackupPhase = "Pending"
	BackupInProgress BackupPhase = "InProgress"
	BackupCompleted  BackupPhase = "Completed"
	BackupFailed     BackupPhase = "Failed"
)

func (p BackupPhase) Finished() bool {
	return p == BackupCompleted || p == BackupFailed
}

type BackupMethod string

const (
	BackupMethodS3             BackupMethod = "s3"
	BackupMethodVolumeSnapshot BackupMethod = "volumeSnapshot"
)

const (
	DefaultAccessKeyIDKey     = "AWS_ACCESS_KEY_ID"
	DefaultSecretAccessKeyKey = "AWS_SECRET_ACCESS_KEY"
	DefaultS3Region           = "us-east-1"
	DefaultAccessMode         = "ReadWriteOnce"
)

// CredentialsSecretRef names the Secret holding both halves of an S3 credential pair.
type CredentialsSecretRef struct {
	Name               string
	Namespace          string
	AccessKeyIDKey     string
	SecretAccessKeyKey string
}

func DefaultCredentialsSecretRef(name string, namespace string) CredentialsSecretRef {
	return CredentialsSecretRef{
		Name:               name,
		Namespace:          namespace,
		AccessKeyIDKey:     DefaultAccessKeyIDKey,
		SecretAccessKeyKey: DefaultSecretAccessKeyKey,
	}
}

func (c CredentialsSecretRef) JSON() JSON {
	return JSON{"name": c.Name, "accessKeyIdKey": c.AccessKeyIDKey, "secretAccessKeyKey": c.SecretAccessKeyKey}
}

func (c CredentialsSecretRef) AccessKeyRef() SecretRef {
	return SecretRef{Name: c.Name, Key: c.AccessKeyIDKey, Namespace: c.Namespace}
}

func (c CredentialsSecretRef) SecretKeyRef() SecretRef {
	return SecretRef{Name: c.Name, Key: c.SecretAccessKeyKey, Namespace: c.Namespace}
}

type S3Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
}

type S3StorageSpec struct {
	Bucket               string
	CredentialsSecretRef CredentialsSecretRef
	Prefix               string
	Region               string
	Endpoint             string
	ForcePathStyle       bool
}

func DefaultS3StorageSpec(bucket string, credentials CredentialsSecretRef) S3StorageSpec {
	return S3StorageSpec{Bucket: bucket, CredentialsSecretRef: credentials, Region: DefaultS3Region}
}

func (s S3StorageSpec) JSON() JSON {
	spec := JSON{
		"bucket":               s.Bucket,
		"prefix":               s.Prefix,
		"region":               s.Region,
		"forcePathStyle":       s.ForcePathStyle,
		"credentialsSecretRef": s.CredentialsSecretRef.JSON(),
	}
	if s.Endpoint != "" {
		spec["endpoint"] = s.Endpoint
	}
	return spec
}

func (s S3StorageSpec) URI(parts ...string) string {
	return "s3://" + JoinKey(append([]string{s.Bucket, s.Prefix}, parts...)...)
}

type BackupSpec struct {
	Name                string
	Namespace           string
	ClusterRef          ClusterRef
	Storage             *S3StorageSpec
	Collections         []string
	RetentionDays       int
	Method              BackupMethod
	VolumeSnapshotClass string
}

func DefaultBackupSpec(name string, namespace string, cluster ClusterRef) BackupSpec {
	return BackupSpec{Name: name, Namespace: namespace, ClusterRef: cluster, Collections: []string{}, Method: BackupMethodS3}
}

func (b BackupSpec) S3Storage() (S3StorageSpec, error) {
	if b.Storage == nil {
		return S3StorageSpec{}, fmt.Errorf("QdrantBackup %s has no S3 storage (method %s)", b.Name, b.Method)
	}
	return *b.Storage, nil
}

func (b BackupSpec) Location() (string, error) {
	if b.Method == BackupMethodVolumeSnapshot {
		return fmt.Sprintf("VolumeSnapshots labelled %s=%s", BackupLabel, b.Name), nil
	}
	storage, err := b.S3Storage()
	if err != nil {
		return "", err
	}
	return storage.URI(b.Name), nil
}

func (b BackupSpec) SnapshotSelector() string {
	return fmt.Sprintf("%s=%s", BackupLabel, b.Name)
}

func (b BackupSpec) VolumeSnapshotName(ordinal int) string {
	return fmt.Sprintf("%s-node-%d", b.Name, ordinal)
}

func (b BackupSpec) VolumeSnapshotBody(ordinal int) JSON {
	var class any
	if b.VolumeSnapshotClass != "" {
		class = b.VolumeSnapshotClass
	}
	return JSON{
		"apiVersion": SnapshotAPIGroup + "/" + SnapshotAPIVersion,
		"kind":       "VolumeSnapshot",
		"metadata": JSON{
			"name":      b.VolumeSnapshotName(ordinal),
			"namespace": b.ClusterRef.Namespace,
			"labels":    JSON{BackupLabel: b.Name},
		},
		"spec": JSON{
			"volumeSnapshotClassName": class,
			"source":                  JSON{"persistentVolumeClaimName": dataVolumeClaimName(b.ClusterRef.Name, ordinal)},
		},
	}
}

func (b BackupSpec) RootKey() (string, error) {
	storage, err := b.S3Storage()
	if err != nil {
		return "", err
	}
	return JoinKey(storage.Prefix, b.Name), nil
}

func (b BackupSpec) SnapshotKey(collection string, nodeIndex int, snapshotName string) (string, error) {
	root, err := b.RootKey()
	if err != nil {
		return "", err
	}
	return JoinKey(root, collection, fmt.Sprintf("node-%d", nodeIndex), snapshotName), nil
}

// ExpiresAt reports when the backup lapses; false when no retention is set.
func (b BackupSpec) ExpiresAt(completion time.Time) (time.Time, bool) {
	if b.RetentionDays == 0 {
		return time.Time{}, false
	}
	return completion.Add(time.Duration(b.RetentionDays) * 24 * time.Hour), true
}

func dataVolumeClaimName(clusterName string, ordinal int) string {
	return fmt.Sprintf("%s-%s-%d", StorageClaim, ReleaseName(clusterName), ordinal)
}

func clusterRefJSON(ref ClusterRef) JSON {
	return JSON{"name": ref.Name, "namespace": ref.Namespace}
}

// SnapshotRecord is one stored snapshot: the node it came from and where it lives in the bucket.
type SnapshotRecord struct {
	NodeIndex    int
	Key          string
	SnapshotName string
	SizeBytes    int64
	Checksum     string
}

func (s SnapshotRecord) JSON() JSON {
	record := JSON{"nodeIndex": s.NodeIndex, "key": s.Key, "snapshotName": s.SnapshotName, "sizeBytes": s.SizeBytes}
	if s.Checksum != "" {
		record["checksum"] = s.Checksum
	}
	return record
}

type VolumeZone struct {
	Label string
	Name  string
}

// VolumeSnapshotRecord is one VolumeSnapshot of a pod's data claim, with what a restore needs to recreate the claim.
type VolumeSnapshotRecord struct {
	NodeIndex    int
	ClaimName    string
	SnapshotName string
	Size         string
	StorageClass string
	AccessModes  []string
	Zone         *VolumeZone
}

type CollectionBackup struct {
	Name      string
	Snapshots []SnapshotRecord
}

func (c CollectionBackup) JSON() JSON {
	snapshots := make([]JSON, 0, len(c.Snapshots))
	for _, snapshot := range c.Snapshots {
		snapshots = append(snapshots, snapshot.JSON())
	}
	return JSON{"name": c.Name, "snapshots": snapshots}
}

func (c CollectionBackup) SizeBytes() int64 {
	return sumSnapshotBytes(c.Snapshots)
}

func sumSnapshotBytes(snapshots []SnapshotRecord) int64 {
	total := int64(0)
	for _, snapshot := range snapshots {
		total += snapshot.SizeBytes
	}
	return total
}

// BackupManifest indexes a completed backup and is stored next to the snapshots so restores need no guessing.
type BackupManifest struct {
	BackupName  string
	Cluster     ClusterRef
	NodeCount   int
	CreatedAt   time.Time
	Collections []CollectionBackup
}

func (m BackupManifest) JSON() JSON {
	collections := make([]JSON, 0, len(m.Collections))
	for _, collection := range m.Collections {
		collections = append(collections, collection.JSON())
	}
	return JSON{
		"backupName":  m.BackupName,
		"cluster":     clusterRefJSON(m.Cluster),
		"nodeCount":   m.NodeCount,
		"createdAt":   FormatTime(m.CreatedAt),
		"collections": collections,
	}
}

func (m BackupManifest) Collection(name string) (CollectionBackup, bool) {
	for _, collection := range m.Collections {
		if collection.Name == name {
			return collection, true
		}
	}
	return CollectionBackup{}, false
}

func (m BackupManifest) CollectionNames() []string {
	names := make([]string, 0, len(m.Collections))
	for _, collection := range m.Collections {
		names = append(names, collection.Name)
	}
	return names
}

func (m BackupManifest) SizeBytes() int64 {
	total := int64(0)
	for _, collection := range m.Collections {
		total += collection.SizeBytes()
	}
	return total
}

type manifestWire struct {
	BackupName string `json:"backupName"`
	Cluster    struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"cluster"`
	NodeCount   int    `json:"nodeCount"`
	CreatedAt   string `json:"createdAt"`
	Collections []struct {
		Name      string `json:"name"`
		Snapshots []struct {
			NodeIndex    int    `json:"nodeIndex"`
			Key          string `json:"key"`
			SnapshotName string `json:"snapshotName"`
			SizeBytes    int64  `json:"sizeBytes"`
			Checksum     string `json:"checksum"`
		} `json:"snapshots"`
	} `json:"collections"`
}

// ParseManifest reads a manifest.json document.
func ParseManifest(raw []byte) (BackupManifest, error) {
	wire := manifestWire{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return BackupManifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if wire.BackupName == "" {
		return BackupManifest{}, fmt.Errorf("manifest has no backupName")
	}
	createdAt, err := ParseTime(wire.CreatedAt)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("manifest createdAt: %w", err)
	}
	manifest := BackupManifest{
		BackupName:  wire.BackupName,
		Cluster:     ClusterRef{Name: wire.Cluster.Name, Namespace: wire.Cluster.Namespace},
		NodeCount:   wire.NodeCount,
		CreatedAt:   createdAt,
		Collections: make([]CollectionBackup, 0, len(wire.Collections)),
	}
	for _, collection := range wire.Collections {
		backup := CollectionBackup{Name: collection.Name, Snapshots: make([]SnapshotRecord, 0, len(collection.Snapshots))}
		for _, snapshot := range collection.Snapshots {
			backup.Snapshots = append(backup.Snapshots, SnapshotRecord{
				NodeIndex:    snapshot.NodeIndex,
				Key:          snapshot.Key,
				SnapshotName: snapshot.SnapshotName,
				SizeBytes:    snapshot.SizeBytes,
				Checksum:     snapshot.Checksum,
			})
		}
		manifest.Collections = append(manifest.Collections, backup)
	}
	return manifest, nil
}

type CollectionBackupStatus struct {
	Name      string
	Status    BackupPhase
	Snapshots []SnapshotRecord
	Error     string
}

func (c CollectionBackupStatus) Size() string {
	return FormatSize(sumSnapshotBytes(c.Snapshots))
}

// InProgressSnapshot is the node snapshot a backup is streaming right now.
type InProgressSnapshot struct {
	NodeIndex  int
	Collection string
	Snapshot   string
}

// BackupStatus carries no per-collection state back from the API: the snapshot records live in the manifest.
type BackupStatus struct {
	Phase              BackupPhase
	StartTime          time.Time
	CompletionTime     time.Time
	ExpiresAt          time.Time
	S3Path             string
	TotalSize          string
	Collections        []CollectionBackupStatus
	Error              string
	Conditions         []Condition
	InProgressSnapshot *InProgressSnapshot
	VolumeSnapshots    []VolumeSnapshotRecord
}

func (s BackupStatus) Finished() bool {
	return s.Phase.Finished()
}

type RetentionPolicy struct {
	KeepLast    *int
	KeepDaily   *int
	KeepWeekly  *int
	KeepMonthly *int
}

func (p RetentionPolicy) Configured() bool {
	return p.KeepLast != nil || p.KeepDaily != nil || p.KeepWeekly != nil || p.KeepMonthly != nil
}

// Expired returns the backups no bucket of the policy keeps, newest first.
// Buckets count completed backups only. Of the failed ones only the newest is kept, and only while no completed backup is newer.
func (p RetentionPolicy) Expired(backups []BackupRecord) []BackupRecord {
	expired := []BackupRecord{}
	if !p.Configured() {
		return expired
	}
	ordered := slices.Clone(backups)
	sort.SliceStable(ordered, func(i int, j int) bool { return ordered[i].CreationTime.After(ordered[j].CreationTime) })
	completed := []BackupRecord{}
	for _, backup := range ordered {
		if backup.Phase == BackupCompleted {
			completed = append(completed, backup)
		}
	}
	keep := map[string]bool{}
	for _, backup := range completed[:min(max(countOrZero(p.KeepLast), 0), len(completed))] {
		keep[backup.Name] = true
	}
	for _, bucket := range []struct {
		count  *int
		bucket func(time.Time) string
	}{
		{p.KeepDaily, dayBucket},
		{p.KeepWeekly, weekBucket},
		{p.KeepMonthly, monthBucket},
	} {
		for name := range NewestPerBucket(completed, countOrZero(bucket.count), bucket.bucket) {
			keep[name] = true
		}
	}
	if len(ordered) > 0 && ordered[0].Phase != BackupCompleted {
		keep[ordered[0].Name] = true
	}
	for _, backup := range ordered {
		if !keep[backup.Name] {
			expired = append(expired, backup)
		}
	}
	return expired
}

func countOrZero(count *int) int {
	if count == nil {
		return 0
	}
	return *count
}

func dayBucket(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func weekBucket(t time.Time) string {
	year, week := t.UTC().ISOWeek()
	return fmt.Sprintf("%04d-W%02d", year, week)
}

func monthBucket(t time.Time) string {
	return t.UTC().Format("2006-01")
}

// NewestPerBucket names the newest backup of each of the first count buckets; ordered must be newest first.
func NewestPerBucket(ordered []BackupRecord, count int, bucket func(time.Time) string) map[string]bool {
	names := map[string]bool{}
	if count <= 0 {
		return names
	}
	seen := map[string]bool{}
	winners := []string{}
	for _, backup := range ordered {
		key := bucket(backup.CreationTime)
		if seen[key] {
			continue
		}
		seen[key] = true
		winners = append(winners, backup.Name)
	}
	for _, name := range winners[:min(count, len(winners))] {
		names[name] = true
	}
	return names
}

// BackupRecord is what a schedule needs to know about one of its QdrantBackups.
type BackupRecord struct {
	Name           string
	CreationTime   time.Time
	Phase          BackupPhase
	CompletionTime time.Time
	TotalSize      string
}

func (r BackupRecord) Finished() bool {
	return r.Phase.Finished()
}

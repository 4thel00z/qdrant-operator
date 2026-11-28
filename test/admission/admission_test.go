package admission_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/yaml"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
)

var database = qdrantv1alpha1.ClusterRef{Name: "db"}

func (w *world) cluster(change func(*qdrantv1alpha1.QdrantClusterSpec)) *qdrantv1alpha1.QdrantCluster {
	object := &qdrantv1alpha1.QdrantCluster{
		ObjectMeta: w.meta(),
		Spec:       qdrantv1alpha1.QdrantClusterSpec{Version: "v1.16.3", Replicas: ptr(int32(3))},
	}
	change(&object.Spec)
	return object
}

func (w *world) collection(change func(*qdrantv1alpha1.QdrantCollectionSpec)) *qdrantv1alpha1.QdrantCollection {
	object := &qdrantv1alpha1.QdrantCollection{
		ObjectMeta: w.meta(),
		Spec: qdrantv1alpha1.QdrantCollectionSpec{
			ClusterRef: database,
			Vectors:    []qdrantv1alpha1.DenseVector{{Size: 4, Distance: "Cosine"}},
		},
	}
	change(&object.Spec)
	return object
}

func (w *world) accessKey(change func(*qdrantv1alpha1.QdrantAccessKeySpec)) *qdrantv1alpha1.QdrantAccessKey {
	object := &qdrantv1alpha1.QdrantAccessKey{
		ObjectMeta: w.meta(),
		Spec:       qdrantv1alpha1.QdrantAccessKeySpec{ClusterRef: database, Access: "r"},
	}
	change(&object.Spec)
	return object
}

func (w *world) restore(change func(*qdrantv1alpha1.QdrantRestoreSpec)) *qdrantv1alpha1.QdrantRestore {
	object := &qdrantv1alpha1.QdrantRestore{
		ObjectMeta: w.meta(),
		Spec: qdrantv1alpha1.QdrantRestoreSpec{
			BackupRef:        &qdrantv1alpha1.RestoreBackupRef{Name: "nightly"},
			TargetClusterRef: database,
		},
	}
	change(&object.Spec)
	return object
}

func (w *world) migration(change func(*qdrantv1alpha1.QdrantMigrationSpec)) *qdrantv1alpha1.QdrantMigration {
	object := &qdrantv1alpha1.QdrantMigration{
		ObjectMeta: w.meta(),
		Spec: qdrantv1alpha1.QdrantMigrationSpec{
			Source:           qdrantv1alpha1.MigrationSource{ClusterRef: &qdrantv1alpha1.ClusterRef{Name: "old"}},
			TargetClusterRef: database,
		},
	}
	change(&object.Spec)
	return object
}

func (w *world) backup(change func(*qdrantv1alpha1.QdrantBackupSpec)) *qdrantv1alpha1.QdrantBackup {
	object := &qdrantv1alpha1.QdrantBackup{
		ObjectMeta: w.meta(),
		Spec: qdrantv1alpha1.QdrantBackupSpec{
			ClusterRef: database,
			Method:     "s3",
			Storage:    &qdrantv1alpha1.BackupStorage{S3: backupBucket()},
		},
	}
	change(&object.Spec)
	return object
}

func (w *world) schedule(change func(*qdrantv1alpha1.QdrantBackupScheduleSpec)) *qdrantv1alpha1.QdrantBackupSchedule {
	object := &qdrantv1alpha1.QdrantBackupSchedule{
		ObjectMeta: w.meta(),
		Spec: qdrantv1alpha1.QdrantBackupScheduleSpec{
			Schedule:   "0 2 * * *",
			ClusterRef: database,
			Method:     "s3",
			Storage:    &qdrantv1alpha1.ScheduleStorage{S3: scheduleBucket()},
		},
	}
	change(&object.Spec)
	return object
}

func backupBucket() qdrantv1alpha1.BackupS3Storage {
	return qdrantv1alpha1.BackupS3Storage{
		Bucket:               "backups",
		CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3"},
	}
}

func scheduleBucket() qdrantv1alpha1.ScheduleS3Storage {
	return qdrantv1alpha1.ScheduleS3Storage{
		Bucket:               "backups",
		CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3"},
	}
}

func restoreSource() *qdrantv1alpha1.RestoreSource {
	return &qdrantv1alpha1.RestoreSource{S3: &qdrantv1alpha1.RestoreS3Source{
		Bucket:               "backups",
		Path:                 "nightly/db",
		CredentialsSecretRef: qdrantv1alpha1.S3CredentialsSecretRef{Name: "s3"},
	}}
}

type rejection struct {
	name string
	want string
	run  func(w *world) error
}

type acceptance struct {
	name string
	run  func(w *world) error
}

var rejections = []rejection{
	{"restore without a source", "exactly one of backupRef or source", func(w *world) error {
		return w.dryCreate(w.restore(func(s *qdrantv1alpha1.QdrantRestoreSpec) { s.BackupRef = nil }))
	}},
	{"multi replica single node cluster", "requires cluster.enabled", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.Cluster = &qdrantv1alpha1.ClusterMode{Enabled: ptr(false)}
		}))
	}},
	{"cluster persistence shrinks", "persistence.size can grow but not shrink", func(w *world) error {
		base := w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.Persistence = &qdrantv1alpha1.ClusterPersistence{Size: "20Gi"}
		})
		return dryUpdate(w, base, func(o *qdrantv1alpha1.QdrantCluster) { o.Spec.Persistence.Size = "10Gi" })
	}},
	{"cluster snapshot persistence shrinks", "snapshotPersistence.size can grow but not shrink", func(w *world) error {
		base := w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.SnapshotPersistence = &qdrantv1alpha1.ClusterSnapshotPersistence{Size: "20Gi"}
		})
		return dryUpdate(w, base, func(o *qdrantv1alpha1.QdrantCluster) { o.Spec.SnapshotPersistence.Size = "10Gi" })
	}},
	{"cluster storage class changes", "persistence.storageClassName is immutable", func(w *world) error {
		base := w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.Persistence = &qdrantv1alpha1.ClusterPersistence{StorageClassName: "fast"}
		})
		return dryUpdate(w, base, func(o *qdrantv1alpha1.QdrantCluster) { o.Spec.Persistence.StorageClassName = "slow" })
	}},
	{"cluster api key with secretRef and autoGenerate", "set either secretRef or autoGenerate, not both", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.APIKey = &qdrantv1alpha1.ClusterAPIKey{
				SecretRef:    &qdrantv1alpha1.ClusterRequiredSecretKeyRef{Name: "key", Key: "api-key"},
				AutoGenerate: ptr(true),
			}
		}))
	}},
	{"cluster jwtRbac without an api key", "jwtRbac needs an API key", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.APIKey = &qdrantv1alpha1.ClusterAPIKey{JWTRbac: ptr(true)}
		}))
	}},
	{"cluster read-only api key with secretRef and autoGenerate", "set either secretRef or autoGenerate, not both", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.ReadOnlyAPIKey = &qdrantv1alpha1.ClusterReadOnlyAPIKey{
				SecretRef:    &qdrantv1alpha1.ClusterSecretKeyRef{Name: "key", Key: "api-key"},
				AutoGenerate: ptr(true),
			}
		}))
	}},
	{"cluster tls enabled without a secret", "tls.enabled requires tls.secretRef.name", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.TLS = &qdrantv1alpha1.ClusterTLS{Enabled: ptr(true)}
		}))
	}},
	{"cluster disruption budget with both bounds", "set either maxUnavailable or minAvailable, not both", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.PodDisruptionBudget = &qdrantv1alpha1.ClusterPodDisruptionBudget{
				MaxUnavailable: ptr(intstr.FromInt32(1)),
				MinAvailable:   ptr(intstr.FromInt32(2)),
			}
		}))
	}},
	{"collection without vectors", "declare at least one of vectors or sparseVectors", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) { s.Vectors = nil }))
	}},
	{"collection with an unnamed vector among several", "an unnamed vector must be the only vector", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) {
			s.Vectors = []qdrantv1alpha1.DenseVector{
				{Name: "text", Size: 4, Distance: "Cosine"},
				{Size: 4, Distance: "Dot"},
			}
		}))
	}},
	{"collection changes its cluster", "clusterRef is immutable", func(w *world) error {
		return dryUpdate(w, w.collection(func(*qdrantv1alpha1.QdrantCollectionSpec) {}), func(o *qdrantv1alpha1.QdrantCollection) {
			o.Spec.ClusterRef.Name = "other"
		})
	}},
	{"collection changes its name", "collectionName is immutable", func(w *world) error {
		return dryUpdate(w, w.collection(func(*qdrantv1alpha1.QdrantCollectionSpec) {}), func(o *qdrantv1alpha1.QdrantCollection) {
			o.Spec.CollectionName = "renamed"
		})
	}},
	{"collection shard keys without custom sharding", "shardKeys need shardingMethod: custom", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) {
			s.ShardKeys = []qdrantv1alpha1.ShardKey{{Key: intstr.FromString("tenant-a")}}
		}))
	}},
	{"access key with neither access nor collections", "set exactly one of access", func(w *world) error {
		return w.dryCreate(w.accessKey(func(s *qdrantv1alpha1.QdrantAccessKeySpec) { s.Access = "" }))
	}},
	{"access key with both access and collections", "set exactly one of access", func(w *world) error {
		return w.dryCreate(w.accessKey(func(s *qdrantv1alpha1.QdrantAccessKeySpec) {
			s.Collections = []qdrantv1alpha1.AccessKeyCollection{{Name: "docs", Access: "r"}}
		}))
	}},
	{"access key renewBefore without ttl", "renewBefore needs ttl", func(w *world) error {
		return w.dryCreate(w.accessKey(func(s *qdrantv1alpha1.QdrantAccessKeySpec) { s.RenewBefore = "1h" }))
	}},
	{"access key changes its cluster", "clusterRef is immutable", func(w *world) error {
		return dryUpdate(w, w.accessKey(func(*qdrantv1alpha1.QdrantAccessKeySpec) {}), func(o *qdrantv1alpha1.QdrantAccessKey) {
			o.Spec.ClusterRef.Name = "other"
		})
	}},
	{"access key changes its secret name", "secretName is immutable", func(w *world) error {
		return dryUpdate(w, w.accessKey(func(*qdrantv1alpha1.QdrantAccessKeySpec) {}), func(o *qdrantv1alpha1.QdrantAccessKey) {
			o.Spec.SecretName = "renamed"
		})
	}},
	{"restore with backupRef and source", "exactly one of backupRef or source", func(w *world) error {
		return w.dryCreate(w.restore(func(s *qdrantv1alpha1.QdrantRestoreSpec) { s.Source = restoreSource() }))
	}},
	{"restore spec changes", "spec is immutable; create a new QdrantRestore instead", func(w *world) error {
		return dryUpdate(w, w.restore(func(*qdrantv1alpha1.QdrantRestoreSpec) {}), func(o *qdrantv1alpha1.QdrantRestore) {
			o.Spec.Priority = "replica"
		})
	}},
	{"migration without a source", "source needs exactly one of clusterRef or endpoint", func(w *world) error {
		return w.dryCreate(w.migration(func(s *qdrantv1alpha1.QdrantMigrationSpec) { s.Source = qdrantv1alpha1.MigrationSource{} }))
	}},
	{"migration with two sources", "source needs exactly one of clusterRef or endpoint", func(w *world) error {
		return w.dryCreate(w.migration(func(s *qdrantv1alpha1.QdrantMigrationSpec) {
			s.Source.Endpoint = &qdrantv1alpha1.MigrationEndpoint{URL: "https://qdrant.example.com:6333"}
		}))
	}},
	{"migration spec changes", "spec is immutable; create a new QdrantMigration instead", func(w *world) error {
		return dryUpdate(w, w.migration(func(*qdrantv1alpha1.QdrantMigrationSpec) {}), func(o *qdrantv1alpha1.QdrantMigration) {
			o.Spec.Collections = []string{"docs"}
		})
	}},
	{"migration with shardKey and shardKeyField", "target.shardKey and target.shardKeyField are mutually exclusive", func(w *world) error {
		return w.dryCreate(w.migration(func(s *qdrantv1alpha1.QdrantMigrationSpec) {
			s.Target = &qdrantv1alpha1.MigrationTarget{ShardKey: ptr(intstr.FromString("tenant-a")), ShardKeyField: "tenant"}
		}))
	}},
	{"backup to s3 without storage", "storage.s3 is required when method is s3", func(w *world) error {
		return w.dryCreate(w.backup(func(s *qdrantv1alpha1.QdrantBackupSpec) { s.Storage = nil }))
	}},
	{"backup with the default method and no storage", "storage.s3 is required when method is s3", func(w *world) error {
		return w.dryCreate(w.backup(func(s *qdrantv1alpha1.QdrantBackupSpec) {
			s.Method = ""
			s.Storage = nil
		}))
	}},
	{"backup volume snapshot without a class", "volumeSnapshotClassName is required when method is volumeSnapshot", func(w *world) error {
		return w.dryCreate(w.backup(func(s *qdrantv1alpha1.QdrantBackupSpec) { s.Method = "volumeSnapshot" }))
	}},
	{"backup spec changes", "spec is immutable; create a new QdrantBackup instead", func(w *world) error {
		return dryUpdate(w, w.backup(func(*qdrantv1alpha1.QdrantBackupSpec) {}), func(o *qdrantv1alpha1.QdrantBackup) {
			o.Spec.RetentionDays = ptr(int32(7))
		})
	}},
	{"schedule to s3 without storage", "storage.s3 is required when method is s3", func(w *world) error {
		return w.dryCreate(w.schedule(func(s *qdrantv1alpha1.QdrantBackupScheduleSpec) { s.Storage = nil }))
	}},
	{"schedule volume snapshot without a class", "volumeSnapshotClassName is required when method is volumeSnapshot", func(w *world) error {
		return w.dryCreate(w.schedule(func(s *qdrantv1alpha1.QdrantBackupScheduleSpec) { s.Method = "volumeSnapshot" }))
	}},
}

var acceptances = []acceptance{
	{"minimal cluster", func(w *world) error {
		return w.dryCreate(w.cluster(func(*qdrantv1alpha1.QdrantClusterSpec) {}))
	}},
	{"single replica cluster without cluster mode", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.Replicas = ptr(int32(1))
			s.Cluster = &qdrantv1alpha1.ClusterMode{Enabled: ptr(false)}
		}))
	}},
	{"fully configured cluster", func(w *world) error {
		return w.dryCreate(w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.APIKey = &qdrantv1alpha1.ClusterAPIKey{JWTRbac: ptr(true), AutoGenerate: ptr(true)}
			s.ReadOnlyAPIKey = &qdrantv1alpha1.ClusterReadOnlyAPIKey{SecretRef: &qdrantv1alpha1.ClusterSecretKeyRef{Name: "ro", Key: "api-key"}}
			s.TLS = &qdrantv1alpha1.ClusterTLS{Enabled: ptr(true), SecretRef: &qdrantv1alpha1.ClusterSecretName{Name: "tls"}}
			s.PodDisruptionBudget = &qdrantv1alpha1.ClusterPodDisruptionBudget{MaxUnavailable: ptr(intstr.FromInt32(1))}
			s.Persistence = &qdrantv1alpha1.ClusterPersistence{Size: "20Gi", StorageClassName: "fast"}
			s.SnapshotPersistence = &qdrantv1alpha1.ClusterSnapshotPersistence{Size: "20Gi"}
		}))
	}},
	{"cluster persistence grows", func(w *world) error {
		base := w.cluster(func(s *qdrantv1alpha1.QdrantClusterSpec) {
			s.Persistence = &qdrantv1alpha1.ClusterPersistence{Size: "10Gi"}
		})
		return dryUpdate(w, base, func(o *qdrantv1alpha1.QdrantCluster) { o.Spec.Persistence.Size = "20Gi" })
	}},
	{"dense collection", func(w *world) error {
		return w.dryCreate(w.collection(func(*qdrantv1alpha1.QdrantCollectionSpec) {}))
	}},
	{"collection with named vectors", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) {
			s.Vectors = []qdrantv1alpha1.DenseVector{
				{Name: "text", Size: 4, Distance: "Cosine"},
				{Name: "image", Size: 8, Distance: "Dot"},
			}
		}))
	}},
	{"sparse only collection", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) {
			s.Vectors = nil
			s.SparseVectors = []qdrantv1alpha1.SparseVector{{Name: "words"}}
		}))
	}},
	{"collection with custom sharding", func(w *world) error {
		return w.dryCreate(w.collection(func(s *qdrantv1alpha1.QdrantCollectionSpec) {
			s.ShardingMethod = "custom"
			s.ShardKeys = []qdrantv1alpha1.ShardKey{{Key: intstr.FromString("tenant-a")}}
		}))
	}},
	{"cluster wide access key", func(w *world) error {
		return w.dryCreate(w.accessKey(func(*qdrantv1alpha1.QdrantAccessKeySpec) {}))
	}},
	{"per collection access key with renewal", func(w *world) error {
		return w.dryCreate(w.accessKey(func(s *qdrantv1alpha1.QdrantAccessKeySpec) {
			s.Access = ""
			s.Collections = []qdrantv1alpha1.AccessKeyCollection{{Name: "docs", Access: "rw"}}
			s.TTL = "24h"
			s.RenewBefore = "1h"
		}))
	}},
	{"restore from a backup", func(w *world) error {
		return w.dryCreate(w.restore(func(*qdrantv1alpha1.QdrantRestoreSpec) {}))
	}},
	{"restore from a source", func(w *world) error {
		return w.dryCreate(w.restore(func(s *qdrantv1alpha1.QdrantRestoreSpec) {
			s.BackupRef = nil
			s.Source = restoreSource()
		}))
	}},
	{"migration from a cluster", func(w *world) error {
		return w.dryCreate(w.migration(func(*qdrantv1alpha1.QdrantMigrationSpec) {}))
	}},
	{"migration from an endpoint with a shard key", func(w *world) error {
		return w.dryCreate(w.migration(func(s *qdrantv1alpha1.QdrantMigrationSpec) {
			s.Source = qdrantv1alpha1.MigrationSource{Endpoint: &qdrantv1alpha1.MigrationEndpoint{URL: "https://qdrant.example.com:6333"}}
			s.Target = &qdrantv1alpha1.MigrationTarget{ShardKey: ptr(intstr.FromString("tenant-a"))}
		}))
	}},
	{"backup to s3", func(w *world) error {
		return w.dryCreate(w.backup(func(*qdrantv1alpha1.QdrantBackupSpec) {}))
	}},
	{"backup as a volume snapshot", func(w *world) error {
		return w.dryCreate(w.backup(func(s *qdrantv1alpha1.QdrantBackupSpec) {
			s.Method = "volumeSnapshot"
			s.Storage = nil
			s.VolumeSnapshotClassName = "csi"
		}))
	}},
	{"schedule to s3", func(w *world) error {
		return w.dryCreate(w.schedule(func(*qdrantv1alpha1.QdrantBackupScheduleSpec) {}))
	}},
	{"schedule as a volume snapshot", func(w *world) error {
		return w.dryCreate(w.schedule(func(s *qdrantv1alpha1.QdrantBackupScheduleSpec) {
			s.Method = "volumeSnapshot"
			s.Storage = nil
			s.VolumeSnapshotClassName = "csi"
		}))
	}},
}

func TestAdmissionRejects(t *testing.T) {
	w := startWorld(t)
	for _, tc := range rejections {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(w)
			if !isInvalid(err) {
				t.Fatalf("want an Invalid error, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestAdmissionAccepts(t *testing.T) {
	w := startWorld(t)
	for _, tc := range acceptances {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(w); err != nil {
				t.Fatalf("want the object admitted, got %v", err)
			}
		})
	}
}

func celMessages(t *testing.T, schema *apiextensionsv1.JSONSchemaProps) []string {
	t.Helper()
	if schema == nil {
		return nil
	}
	var messages []string
	for _, rule := range schema.XValidations {
		messages = append(messages, rule.Message)
	}
	for name := range schema.Properties {
		child := schema.Properties[name]
		messages = append(messages, celMessages(t, &child)...)
	}
	if schema.Items != nil {
		messages = append(messages, celMessages(t, schema.Items.Schema)...)
	}
	if schema.AdditionalProperties != nil {
		messages = append(messages, celMessages(t, schema.AdditionalProperties.Schema)...)
	}
	return messages
}

func TestEveryCELRuleIsRejectedByACase(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "manifests", "crds", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no CRDs found: %v", err)
	}
	covered := map[string]int{}
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		for _, version := range crd.Spec.Versions {
			for _, message := range celMessages(t, version.Schema.OpenAPIV3Schema) {
				if message == "" {
					t.Errorf("%s has a rule without a message", filepath.Base(path))
					continue
				}
				index := slices.IndexFunc(rejections, func(r rejection) bool { return strings.Contains(message, r.want) })
				if index < 0 {
					t.Errorf("%s: no rejection case for %q", filepath.Base(path), message)
					continue
				}
				covered[message]++
			}
		}
	}
	if len(covered) == 0 {
		t.Fatal("found no x-kubernetes-validations rules")
	}
}

package translate_test

import (
	"encoding/json"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type backupGolden struct {
	Specs []struct {
		Name       string          `json:"name"`
		Spec       json.RawMessage `json:"spec"`
		Completion string          `json:"completion"`
		Outputs    map[string]any  `json:"outputs"`
	} `json:"specs"`
	Manifests []struct {
		Name     string          `json:"name"`
		Manifest json.RawMessage `json:"manifest"`
		Outputs  map[string]any  `json:"outputs"`
	} `json:"manifests"`
	Statuses []struct {
		Name        string                  `json:"name"`
		Status      json.RawMessage         `json:"status"`
		Collections []goldenCollectionState `json:"collections"`
		Outputs     map[string]any          `json:"outputs"`
	} `json:"statuses"`
	Records []struct {
		Name    string          `json:"name"`
		Backup  json.RawMessage `json:"backup"`
		Outputs map[string]any  `json:"outputs"`
	} `json:"records"`
	Retention []struct {
		Name    string               `json:"name"`
		Policy  json.RawMessage      `json:"policy"`
		Backups []goldenBackupRecord `json:"backups"`
		Outputs map[string]any       `json:"outputs"`
	} `json:"retention"`
}

type goldenCollectionState struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	Snapshots []struct {
		NodeIndex    int    `json:"nodeIndex"`
		Key          string `json:"key"`
		SnapshotName string `json:"snapshotName"`
		SizeBytes    int64  `json:"sizeBytes"`
	} `json:"snapshots"`
}

type goldenBackupRecord struct {
	Name           string `json:"name"`
	CreationTime   string `json:"creationTime"`
	CompletionTime string `json:"completionTime"`
	Phase          string `json:"phase"`
	Size           string `json:"size"`
}

func TestBackupSpecOutputsMatchPythonGolden(t *testing.T) {
	golden := backupGolden{}
	readJSON(t, "testdata/backup_spec_golden.json", &golden)
	for _, testCase := range golden.Specs {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantBackupSpec{}
			decodeStrict(t, testCase.Spec, &api)
			spec, err := translate.BackupSpecFromAPI("demo", "tenant-a", api)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			completion := mustTime(t, testCase.Completion)
			compareJSON(t, testCase.Outputs, backupSpecOutputs(spec, completion))
		})
	}
}

func backupSpecOutputs(spec domain.BackupSpec, completion time.Time) map[string]any {
	var accessKeyRef, secretKeyRef any
	if spec.Storage != nil {
		accessKeyRef = secretRefJSON(spec.Storage.CredentialsSecretRef.AccessKeyRef())
		secretKeyRef = secretRefJSON(spec.Storage.CredentialsSecretRef.SecretKeyRef())
	}
	var expiresAt any
	if expires, ok := spec.ExpiresAt(completion); ok {
		expiresAt = domain.FormatTime(expires)
	}
	bodies := []any{spec.VolumeSnapshotBody(0), spec.VolumeSnapshotBody(1)}
	return map[string]any{
		"cluster_ref":       map[string]any{"name": spec.ClusterRef.Name, "namespace": spec.ClusterRef.Namespace},
		"collections":       spec.Collections,
		"method":            string(spec.Method),
		"s3_storage":        outcome(func() (any, error) { return storageJSON(spec) }),
		"access_key_ref":    accessKeyRef,
		"secret_key_ref":    secretKeyRef,
		"location":          outcome(func() (any, error) { return spec.Location() }),
		"snapshot_selector": spec.SnapshotSelector(),
		"volume_snapshot_names": []string{
			spec.VolumeSnapshotName(0), spec.VolumeSnapshotName(1), spec.VolumeSnapshotName(2),
		},
		"volume_snapshot_bodies": bodies,
		"root_key":               outcome(func() (any, error) { return spec.RootKey() }),
		"snapshot_key":           outcome(func() (any, error) { return spec.SnapshotKey("coll", 1, "s.snapshot") }),
		"expires_at":             expiresAt,
	}
}

func storageJSON(spec domain.BackupSpec) (any, error) {
	storage, err := spec.S3Storage()
	if err != nil {
		return nil, err
	}
	return storage.JSON(), nil
}

func secretRefJSON(ref domain.SecretRef) map[string]any {
	return map[string]any{"name": ref.Name, "key": ref.Key, "namespace": ref.Namespace}
}

func outcome(compute func() (any, error)) map[string]any {
	value, err := compute()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{"value": value}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := domain.ParseTime(value)
	if err != nil {
		t.Fatalf("parse time %q: %v", value, err)
	}
	return parsed
}

func TestBackupManifestsMatchPythonGolden(t *testing.T) {
	golden := backupGolden{}
	readJSON(t, "testdata/backup_spec_golden.json", &golden)
	for _, testCase := range golden.Manifests {
		t.Run(testCase.Name, func(t *testing.T) {
			manifest, err := domain.ParseManifest(testCase.Manifest)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			first := "none"
			if names := manifest.CollectionNames(); len(names) > 0 {
				first = names[0]
			}
			var firstCollection any
			if found, ok := manifest.Collection(first); ok {
				firstCollection = found.JSON()
			}
			_, missing := manifest.Collection("zzz")
			sizes := map[string]any{}
			for _, collection := range manifest.Collections {
				sizes[collection.Name] = collection.SizeBytes()
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"json":               manifest.JSON(),
				"collection_names":   manifest.CollectionNames(),
				"size_bytes":         manifest.SizeBytes(),
				"collection_sizes":   sizes,
				"first_collection":   firstCollection,
				"missing_collection": missing,
			})
			encoded, err := json.Marshal(manifest.JSON())
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			again, err := domain.ParseManifest(encoded)
			if err != nil {
				t.Fatalf("parse own output: %v", err)
			}
			compareJSON(t, map[string]any{"json": roundTrip(t, manifest.JSON())}, map[string]any{"json": again.JSON()})
		})
	}
}

func TestBackupStatusMatchesPythonGolden(t *testing.T) {
	golden := backupGolden{}
	readJSON(t, "testdata/backup_spec_golden.json", &golden)
	for _, testCase := range golden.Statuses {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantBackupStatus{}
			decodeStrict(t, testCase.Status, &api)
			status, err := translate.BackupStatusFromAPI(api)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			for _, collection := range testCase.Collections {
				status.Collections = append(status.Collections, collectionStatus(collection))
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"finished": status.Finished(),
				"api":      pruneEmptyValues(roundTrip(t, translate.BackupStatusToAPI(status))),
			})
		})
	}
}

func collectionStatus(data goldenCollectionState) domain.CollectionBackupStatus {
	collection := domain.CollectionBackupStatus{Name: data.Name, Status: domain.BackupPhase(data.Status), Error: data.Error}
	for _, snapshot := range data.Snapshots {
		collection.Snapshots = append(collection.Snapshots, domain.SnapshotRecord{
			NodeIndex: snapshot.NodeIndex, Key: snapshot.Key, SnapshotName: snapshot.SnapshotName, SizeBytes: snapshot.SizeBytes,
		})
	}
	return collection
}

func TestBackupRecordsMatchPythonGolden(t *testing.T) {
	golden := backupGolden{}
	readJSON(t, "testdata/backup_spec_golden.json", &golden)
	for _, testCase := range golden.Records {
		t.Run(testCase.Name, func(t *testing.T) {
			backup := qdrantv1alpha1.QdrantBackup{}
			decodeStrict(t, testCase.Backup, &backup)
			record, err := translate.BackupRecordFromAPI(backup)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"finished": record.Finished(),
				"record":   pruneEmptyValues(roundTrip(t, translate.RecentBackupToAPI(record))),
			})
		})
	}
}

func TestRetentionMatchesPythonGolden(t *testing.T) {
	golden := backupGolden{}
	readJSON(t, "testdata/backup_spec_golden.json", &golden)
	for _, testCase := range golden.Retention {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.RetentionPolicy{}
			decodeStrict(t, testCase.Policy, &api)
			policy := translate.RetentionPolicyFromAPI(&api)
			records := []domain.BackupRecord{}
			for _, backup := range testCase.Backups {
				records = append(records, domain.BackupRecord{
					Name:         backup.Name,
					CreationTime: mustTime(t, backup.CreationTime),
					Phase:        domain.BackupPhase(backup.Phase),
				})
			}
			names := []string{}
			for _, expired := range policy.Expired(records) {
				names = append(names, expired.Name)
			}
			compareJSON(t, testCase.Outputs, map[string]any{"configured": policy.Configured(), "expired": names})
		})
	}
}

func roundTrip(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return decoded
}

// pruneEmptyValues drops what Python's null, empty and zero values would leave out of a Go omitempty field.
func pruneEmptyValues(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		pruned := map[string]any{}
		for key, item := range typed {
			item = pruneEmptyValues(item)
			if emptyValue(item) {
				continue
			}
			pruned[key] = item
		}
		return pruned
	case []any:
		pruned := make([]any, 0, len(typed))
		for _, item := range typed {
			pruned = append(pruned, pruneEmptyValues(item))
		}
		return pruned
	}
	return value
}

func emptyValue(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case float64:
		return typed == 0
	case []any:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	}
	return false
}

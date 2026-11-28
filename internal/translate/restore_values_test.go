package translate_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type restoreGolden struct {
	Available []string `json:"available"`
	Specs     []struct {
		Name    string          `json:"name"`
		Spec    json.RawMessage `json:"spec"`
		Outputs map[string]any  `json:"outputs"`
	} `json:"specs"`
	Selections []struct {
		Name      string         `json:"name"`
		Requested []string       `json:"requested"`
		Available []string       `json:"available"`
		Source    string         `json:"source"`
		Outputs   map[string]any `json:"outputs"`
	} `json:"selections"`
	Timeouts []struct {
		Name       string         `json:"name"`
		Timeout    int32          `json:"timeout"`
		Collection string         `json:"collection"`
		Info       domain.JSON    `json:"info"`
		Outputs    map[string]any `json:"outputs"`
	} `json:"timeouts"`
	VolumePlans []struct {
		Name           string                                `json:"name"`
		Restore        json.RawMessage                       `json:"restore"`
		Source         json.RawMessage                       `json:"source"`
		Records        []qdrantv1alpha1.BackupVolumeSnapshot `json:"records"`
		TargetReplicas *int                                  `json:"target_replicas"`
		Deployed       bool                                  `json:"deployed"`
		ExistingClaims []string                              `json:"existing_claims"`
		Nodes          map[string]string                     `json:"nodes"`
		Outputs        map[string]any                        `json:"outputs"`
	} `json:"volume_plans"`
	Statuses []struct {
		Name    string          `json:"name"`
		Status  json.RawMessage `json:"status"`
		Outputs map[string]any  `json:"outputs"`
	} `json:"statuses"`
}

func TestRestoreSpecOutputsMatchPythonGolden(t *testing.T) {
	golden := restoreGolden{}
	readJSON(t, "testdata/restore_values_golden.json", &golden)
	for _, testCase := range golden.Specs {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantRestoreSpec{}
			decodeStrict(t, testCase.Spec, &api)
			spec, err := translate.RestoreSpecFromAPI("restore", "ns", api)
			if err != nil {
				compareJSON(t, testCase.Outputs, map[string]any{"error": err.Error()})
				return
			}
			var backupRef any
			if spec.BackupRef != nil {
				ref := spec.BackupRef.ResourceRef()
				backupRef = map[string]any{"name": ref.Name, "namespace": ref.Namespace, "plural": ref.Kind.Plural}
			}
			var sourceS3, sourceURI any
			if spec.SourceS3 != nil {
				sourceS3 = spec.SourceS3.JSON()
				sourceURI = spec.SourceS3.URI()
			}
			targetNames := map[string]any{}
			for _, name := range golden.Available {
				targetNames[name] = spec.TargetName(name)
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"target_cluster_ref":       map[string]any{"name": spec.TargetClusterRef.Name, "namespace": spec.TargetClusterRef.Namespace},
				"target_names":             targetNames,
				"selection":                outcome(func() (any, error) { return spec.SelectCollections(golden.Available) }),
				"priority":                 string(spec.Priority),
				"wait_for_indexing":        spec.WaitForIndexing,
				"indexing_timeout_seconds": spec.IndexingTimeout.Seconds(),
				"backup_ref":               backupRef,
				"source_s3":                sourceS3,
				"source_uri":               sourceURI,
			})
		})
	}
}

func TestSelectCollectionsMatchesPythonGolden(t *testing.T) {
	golden := restoreGolden{}
	readJSON(t, "testdata/restore_values_golden.json", &golden)
	for _, testCase := range golden.Selections {
		t.Run(testCase.Name, func(t *testing.T) {
			got := outcome(func() (any, error) {
				return domain.SelectCollections(testCase.Requested, testCase.Available, testCase.Source)
			})
			compareJSON(t, testCase.Outputs, got)
		})
	}
}

func TestIndexingTimeoutMessageMatchesPythonGolden(t *testing.T) {
	golden := restoreGolden{}
	readJSON(t, "testdata/restore_values_golden.json", &golden)
	for _, testCase := range golden.Timeouts {
		t.Run(testCase.Name, func(t *testing.T) {
			spec := domain.DefaultRestoreSpec("restore", "ns", domain.ClusterRef{Name: "db", Namespace: "ns"})
			spec.IndexingTimeout = time.Duration(testCase.Timeout) * time.Second
			compareJSON(t, testCase.Outputs, map[string]any{"message": domain.IndexingTimeoutMessage(spec, testCase.Collection, testCase.Info)})
		})
	}
}

func TestVolumeRestorePlansMatchPythonGolden(t *testing.T) {
	golden := restoreGolden{}
	readJSON(t, "testdata/restore_values_golden.json", &golden)
	for _, testCase := range golden.VolumePlans {
		t.Run(testCase.Name, func(t *testing.T) {
			restoreAPI := qdrantv1alpha1.QdrantRestoreSpec{}
			decodeStrict(t, testCase.Restore, &restoreAPI)
			restore, err := translate.RestoreSpecFromAPI("restore", "ns", restoreAPI)
			if err != nil {
				t.Fatalf("restore: %v", err)
			}
			sourceAPI := qdrantv1alpha1.QdrantBackupSpec{}
			decodeStrict(t, testCase.Source, &sourceAPI)
			source, err := translate.BackupSpecFromAPI("nightly", "ns", sourceAPI)
			if err != nil {
				t.Fatalf("source: %v", err)
			}
			records := []domain.VolumeSnapshotRecord{}
			for _, record := range testCase.Records {
				records = append(records, translate.VolumeSnapshotRecordFromAPI(record))
			}
			nodes := map[int]string{}
			for index, node := range testCase.Nodes {
				ordinal, err := strconv.Atoi(index)
				if err != nil {
					t.Fatalf("node index %q: %v", index, err)
				}
				nodes[ordinal] = node
			}
			got := outcome(func() (any, error) {
				return domain.PlanVolumeRestore(restore, source, records, testCase.TargetReplicas, testCase.Deployed, testCase.ExistingClaims, nodes)
			})
			compareJSON(t, testCase.Outputs, got)
		})
	}
}

func TestRestoreStatusMatchesPythonGolden(t *testing.T) {
	golden := restoreGolden{}
	readJSON(t, "testdata/restore_values_golden.json", &golden)
	for _, testCase := range golden.Statuses {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantRestoreStatus{}
			decodeStrict(t, testCase.Status, &api)
			status, err := translate.RestoreStatusFromAPI(api)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"percentage": status.Progress.Percentage(),
				"api":        pruneEmptyValues(roundTrip(t, translate.RestoreStatusToAPI(status))),
			})
		})
	}
}

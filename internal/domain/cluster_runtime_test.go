package domain_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

type runtimePod struct {
	Ordinal  int    `json:"ordinal"`
	Revision string `json:"revision"`
	Ready    bool   `json:"ready"`
}

type runtimePodState struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
	Ready    bool   `json:"ready"`
}

type runtimeWait struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type runtimeSnapshotOutputs struct {
	Behind          []int        `json:"behind"`
	HeldAt          int          `json:"held_at"`
	Wait            *runtimeWait `json:"wait"`
	NextReadiness   *int         `json:"next_readiness"`
	NextShardHealth *int         `json:"next_shard_health"`
}

type runtimeSnapshotCase struct {
	Name           string                 `json:"name"`
	Partition      int                    `json:"partition"`
	Replicas       int                    `json:"replicas"`
	UpdateRevision string                 `json:"update_revision"`
	Pods           []runtimePod           `json:"pods"`
	Problems       []string               `json:"problems"`
	Outputs        runtimeSnapshotOutputs `json:"outputs"`
}

type runtimeVolumeOut struct {
	Grow     [][]string `json:"grow"`
	Recreate bool       `json:"recreate"`
}

type runtimeGolden struct {
	Snapshots    []runtimeSnapshotCase `json:"snapshots"`
	PodRevisions []struct {
		Name     string            `json:"name"`
		Release  string            `json:"release"`
		Replicas int               `json:"replicas"`
		Pods     []runtimePodState `json:"pods"`
		Out      []runtimePod      `json:"out"`
	} `json:"pod_revisions"`
	DescribeProblems []struct {
		Name     string   `json:"name"`
		Problems []string `json:"problems"`
		Out      string   `json:"out"`
	} `json:"describe_problems"`
	HoldPartition []struct {
		Strategy string `json:"strategy"`
		Replicas int    `json:"replicas"`
		Out      int    `json:"out"`
	} `json:"hold_partition"`
	ReleasedPodsSettled []struct {
		Partition      int          `json:"partition"`
		UpdateRevision string       `json:"update_revision"`
		Pods           []runtimePod `json:"pods"`
		Out            bool         `json:"out"`
	} `json:"released_pods_settled"`
	StatefulSetRolling []struct {
		CurrentRevision string `json:"current_revision"`
		UpdateRevision  string `json:"update_revision"`
		Out             bool   `json:"out"`
	} `json:"statefulset_rolling"`
	VolumePlans []struct {
		Name    string            `json:"name"`
		Desired map[string]string `json:"desired"`
		Live    map[string]string `json:"live"`
		Out     *runtimeVolumeOut `json:"out"`
		Error   bool              `json:"error"`
	} `json:"volume_plans"`
	DataClaimNames []struct {
		Cluster string `json:"cluster"`
		Ordinal int    `json:"ordinal"`
		Out     string `json:"out"`
	} `json:"data_claim_names"`
	Zones []struct {
		Zone  string `json:"zone"`
		Label string `json:"label"`
		Out   *struct {
			Label string `json:"label"`
			Name  string `json:"name"`
		} `json:"out"`
	} `json:"zones"`
}

func loadRuntimeGolden(t *testing.T) runtimeGolden {
	t.Helper()
	raw, err := os.ReadFile("testdata/cluster_runtime_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	golden := runtimeGolden{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&golden); err != nil {
		t.Fatalf("decode golden: %v", err)
	}
	return golden
}

func podRevisions(pods []runtimePod) []domain.PodRevision {
	revisions := []domain.PodRevision{}
	for _, pod := range pods {
		revisions = append(revisions, domain.PodRevision{Ordinal: pod.Ordinal, Revision: pod.Revision, Ready: pod.Ready})
	}
	return revisions
}

func runtimePods(pods []domain.PodRevision) []runtimePod {
	result := []runtimePod{}
	for _, pod := range pods {
		result = append(result, runtimePod{Ordinal: pod.Ordinal, Revision: pod.Revision, Ready: pod.Ready})
	}
	return result
}

func optionalInt(value int, ok bool) *int {
	if !ok {
		return nil
	}
	return &value
}

func TestRolloutSnapshotsMatchPythonGolden(t *testing.T) {
	golden := loadRuntimeGolden(t)
	if len(golden.Snapshots) == 0 {
		t.Fatal("golden has no snapshot cases")
	}
	for _, testCase := range golden.Snapshots {
		snapshot := domain.RolloutSnapshot{
			Partition:      testCase.Partition,
			Replicas:       testCase.Replicas,
			UpdateRevision: testCase.UpdateRevision,
			Pods:           podRevisions(testCase.Pods),
			Health:         domain.ClusterHealth{Problems: testCase.Problems},
		}
		var wait *runtimeWait
		if found, waiting := snapshot.Wait(); waiting {
			wait = &runtimeWait{Reason: found.Reason, Message: found.Message}
		}
		next, nextOK := domain.NextPartition(domain.RolloutReadiness, snapshot)
		shardNext, shardOK := domain.NextPartition(domain.RolloutShardHealth, snapshot)
		behind := snapshot.Behind()
		if len(behind) == 0 {
			behind = []int{}
		}
		got := runtimeSnapshotOutputs{
			Behind:          behind,
			HeldAt:          snapshot.HeldAt(),
			Wait:            wait,
			NextReadiness:   optionalInt(next, nextOK),
			NextShardHealth: optionalInt(shardNext, shardOK),
		}
		if !reflect.DeepEqual(got, testCase.Outputs) {
			t.Errorf("%s: got %+v want %+v", testCase.Name, got, testCase.Outputs)
		}
	}
}

func TestPodRevisionsMatchPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).PodRevisions {
		pods := []domain.PodState{}
		for _, pod := range testCase.Pods {
			pods = append(pods, domain.PodState{Name: pod.Name, Revision: pod.Revision, Ready: pod.Ready})
		}
		got := runtimePods(domain.PodRevisions(testCase.Release, testCase.Replicas, pods))
		if !reflect.DeepEqual(got, testCase.Out) {
			t.Errorf("%s: got %+v want %+v", testCase.Name, got, testCase.Out)
		}
	}
}

func TestDescribeProblemsMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).DescribeProblems {
		if got := domain.DescribeProblems(testCase.Problems); got != testCase.Out {
			t.Errorf("%s: got %q want %q", testCase.Name, got, testCase.Out)
		}
	}
}

func TestHoldPartitionMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).HoldPartition {
		if got := domain.HoldPartition(domain.RolloutStrategy(testCase.Strategy), testCase.Replicas); got != testCase.Out {
			t.Errorf("%s/%d: got %d want %d", testCase.Strategy, testCase.Replicas, got, testCase.Out)
		}
	}
}

func TestReleasedPodsSettledMatchesPythonGolden(t *testing.T) {
	golden := loadRuntimeGolden(t)
	if len(golden.ReleasedPodsSettled) == 0 {
		t.Fatal("golden has no settled cases")
	}
	for _, testCase := range golden.ReleasedPodsSettled {
		got := domain.ReleasedPodsSettled(testCase.Partition, testCase.UpdateRevision, podRevisions(testCase.Pods))
		if got != testCase.Out {
			t.Errorf("%+v: got %v", testCase, got)
		}
	}
}

func TestStatefulSetRollingMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).StatefulSetRolling {
		status := domain.StatefulSetStatus{CurrentRevision: testCase.CurrentRevision, UpdateRevision: testCase.UpdateRevision}
		if got := status.Rolling(); got != testCase.Out {
			t.Errorf("%q/%q: got %v", testCase.CurrentRevision, testCase.UpdateRevision, got)
		}
	}
}

func TestPlanVolumesMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).VolumePlans {
		plan, err := domain.PlanVolumes(testCase.Desired, testCase.Live)
		if testCase.Error {
			if err == nil {
				t.Errorf("%s: want error", testCase.Name)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", testCase.Name, err)
			continue
		}
		grow := [][]string{}
		for _, growth := range plan.Grow {
			grow = append(grow, []string{growth.Name, growth.Size})
		}
		got := runtimeVolumeOut{Grow: grow, Recreate: plan.Recreate}
		if !reflect.DeepEqual(got, *testCase.Out) {
			t.Errorf("%s: got %+v want %+v", testCase.Name, got, *testCase.Out)
		}
	}
}

func TestPlanVolumesNamesShrunkClaims(t *testing.T) {
	_, err := domain.PlanVolumes(map[string]string{"qdrant-storage": "5Gi"}, map[string]string{"qdrant-storage": "10Gi"})
	if err == nil || !strings.Contains(err.Error(), "qdrant-storage") {
		t.Fatalf("got %v", err)
	}
}

func TestDataClaimNameMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).DataClaimNames {
		if got := domain.DataClaimName(testCase.Cluster, testCase.Ordinal); got != testCase.Out {
			t.Errorf("%s/%d: got %q want %q", testCase.Cluster, testCase.Ordinal, got, testCase.Out)
		}
	}
}

func TestNewZoneMatchesPythonGolden(t *testing.T) {
	for _, testCase := range loadRuntimeGolden(t).Zones {
		zone, ok := domain.NewZone(testCase.Zone, testCase.Label)
		if ok != (testCase.Out != nil) {
			t.Errorf("%+v: got ok=%v", testCase, ok)
			continue
		}
		if ok && (zone.Label != testCase.Out.Label || zone.Name != testCase.Out.Name) {
			t.Errorf("%+v: got %+v", testCase, zone)
		}
	}
}

func TestClusterHealthIsHealthyWithoutProblems(t *testing.T) {
	if !(domain.ClusterHealth{}).Healthy() || (domain.ClusterHealth{Problems: []string{"x"}}).Healthy() {
		t.Fatal("healthy must mean no problems")
	}
}

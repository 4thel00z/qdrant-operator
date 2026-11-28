package domain_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func TestClusterPhaseSettled(t *testing.T) {
	cases := map[domain.ClusterPhase]bool{
		domain.PhasePending:     false,
		domain.PhaseUpgrading:   false,
		domain.PhaseRunning:     true,
		domain.PhaseFailed:      true,
		domain.PhaseTerminating: true,
	}
	for phase, want := range cases {
		if got := phase.Settled(); got != want {
			t.Errorf("%s: want %v, got %v", phase, want, got)
		}
	}
}

func TestParseQuantity(t *testing.T) {
	cases := map[string]int64{"10Gi": 10 << 30, "500M": 500_000_000, "1.5Ki": 1536, "0.1": 1, "42": 42}
	for value, want := range cases {
		got, err := domain.ParseQuantity(value)
		if err != nil {
			t.Fatalf("%s: %v", value, err)
		}
		if got != want {
			t.Errorf("%s: want %d, got %d", value, want, got)
		}
	}
	if _, err := domain.ParseQuantity("ten gigs"); err == nil {
		t.Error("expected an error for a malformed quantity")
	}
}

func TestRetryDelayDoublesUpToTheCap(t *testing.T) {
	if got := domain.RetryDelay(0); got != 30*time.Second {
		t.Errorf("first retry: %s", got)
	}
	if got := domain.RetryDelay(2); got != 2*time.Minute {
		t.Errorf("third retry: %s", got)
	}
	if got := domain.RetryDelay(50); got != 10*time.Minute {
		t.Errorf("capped retry: %s", got)
	}
}

func TestMergeJSONMergesNestedMaps(t *testing.T) {
	merged := domain.MergeJSON(
		domain.JSON{"service": domain.JSON{"enable_tls": false, "port": 6333}},
		domain.JSON{"service": domain.JSON{"enable_tls": true}, "log_level": "INFO"},
	)
	want := domain.JSON{"service": domain.JSON{"enable_tls": true, "port": 6333}, "log_level": "INFO"}
	if !reflect.DeepEqual(merged, want) {
		t.Errorf("merged: %v", merged)
	}
}

func TestDepartedClaimsAreAtOrBeyondReplicas(t *testing.T) {
	cluster := domain.DefaultClusterSpec("demo", "search", "v1.15.0")
	cluster.Replicas = 2
	names := []string{
		"qdrant-storage-qdrant-demo-0",
		"qdrant-storage-qdrant-demo-1",
		"qdrant-storage-qdrant-demo-2",
		"qdrant-snapshots-qdrant-demo-3",
		"qdrant-storage-qdrant-other-5",
	}
	want := []string{"qdrant-storage-qdrant-demo-2", "qdrant-snapshots-qdrant-demo-3"}
	if got := cluster.DepartedClaims(names); !reflect.DeepEqual(got, want) {
		t.Errorf("departed: %v", got)
	}
}

func TestNodeURLsUseTheHeadlessService(t *testing.T) {
	cluster := domain.DefaultClusterSpec("demo", "search", "v1.15.0")
	cluster.Replicas = 2
	cluster.TLS.Enabled = true
	want := []string{
		"https://qdrant-demo-0.qdrant-demo-headless.search.svc.cluster.local:6333",
		"https://qdrant-demo-1.qdrant-demo-headless.search.svc.cluster.local:6333",
	}
	if got := cluster.NodeURLs(); !reflect.DeepEqual(got, want) {
		t.Errorf("node urls: %v", got)
	}
	if got := cluster.ServiceURL(); got != "https://qdrant-demo.search.svc.cluster.local:6333" {
		t.Errorf("service url: %s", got)
	}
}

func TestAPIKeyRefPrefersTheUserSecret(t *testing.T) {
	cluster := domain.DefaultClusterSpec("demo", "search", "v1.15.0")
	if ref := cluster.APIKeyRef(); ref != nil {
		t.Errorf("no key configured, got %v", ref)
	}
	cluster.APIKey.AutoGenerate = true
	if ref := cluster.APIKeyRef(); ref == nil || ref.Name != "qdrant-demo-apikey" {
		t.Errorf("generated ref: %v", ref)
	}
	if keys := cluster.GeneratedKeys(); !reflect.DeepEqual(keys, []string{"api-key"}) {
		t.Errorf("generated keys: %v", keys)
	}
	cluster.APIKey.SecretRef = &domain.SecretRef{Name: "mine", Key: "token", Namespace: "search"}
	if ref := cluster.APIKeyRef(); ref == nil || ref.Name != "mine" {
		t.Errorf("user ref: %v", ref)
	}
	if keys := cluster.GeneratedKeys(); len(keys) != 0 {
		t.Errorf("user secret should generate nothing: %v", keys)
	}
}

func TestSetConditionKeepsTransitionTimeWhenStatusIsUnchanged(t *testing.T) {
	first := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	later := first.Add(time.Hour)
	conditions := []domain.Condition{
		{Type: "Ready", Status: domain.ConditionTrue, LastTransitionTime: first},
		{Type: "Degraded", Status: domain.ConditionFalse, LastTransitionTime: first},
	}
	kept := domain.SetCondition(conditions, domain.Condition{Type: "Ready", Status: domain.ConditionTrue, LastTransitionTime: later, Reason: "Again"})
	ready := domain.FindCondition(kept, "Ready")
	if ready == nil || !ready.LastTransitionTime.Equal(first) || ready.Reason != "Again" {
		t.Errorf("unchanged status: %+v", ready)
	}
	flipped := domain.SetCondition(conditions, domain.Condition{Type: "Ready", Status: domain.ConditionFalse, LastTransitionTime: later})
	if ready := domain.FindCondition(flipped, "Ready"); ready == nil || !ready.LastTransitionTime.Equal(later) {
		t.Errorf("changed status: %+v", ready)
	}
	if len(flipped) != 2 {
		t.Errorf("conditions: %+v", flipped)
	}
}

func TestValuesDigestIgnoresKeyOrder(t *testing.T) {
	first, err := domain.ValuesDigest("1.15.0", domain.JSON{"a": 1, "b": domain.JSON{"c": true, "d": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := domain.ValuesDigest("1.15.0", domain.JSON{"b": domain.JSON{"d": "x", "c": true}, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Error("digests differ for equal values")
	}
	other, err := domain.ValuesDigest("1.16.0", domain.JSON{"a": 1, "b": domain.JSON{"c": true, "d": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if other == first {
		t.Error("chart version is not part of the digest")
	}
}

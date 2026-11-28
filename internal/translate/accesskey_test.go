package translate_test

import (
	"encoding/json"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type accessKeyCases struct {
	Durations    []string              `json:"durations"`
	Fingerprints []string              `json:"fingerprints"`
	Specs        []accessKeySpecCase   `json:"specs"`
	Statuses     []accessKeyStatusCase `json:"statuses"`
}

type accessKeySpecCase struct {
	Name     string          `json:"name"`
	IssuedAt string          `json:"issued_at"`
	Spec     json.RawMessage `json:"spec"`
}

type accessKeyStatusCase struct {
	Name   string          `json:"name"`
	Status json.RawMessage `json:"status"`
	Checks []struct {
		Generation    int64  `json:"generation"`
		Fingerprint   string `json:"fingerprint"`
		SecretPresent bool   `json:"secret_present"`
		Now           string `json:"now"`
	} `json:"checks"`
}

type accessKeyGolden struct {
	Durations    []map[string]any `json:"durations"`
	Fingerprints []string         `json:"fingerprints"`
	Specs        []map[string]any `json:"specs"`
	Statuses     []map[string]any `json:"statuses"`
}

func loadAccessKey(t *testing.T) (accessKeyCases, accessKeyGolden) {
	t.Helper()
	cases := accessKeyCases{}
	golden := accessKeyGolden{}
	readJSON(t, "testdata/accesskey_cases.json", &cases)
	readJSON(t, "testdata/accesskey_golden.json", &golden)
	if len(cases.Durations) != len(golden.Durations) || len(cases.Fingerprints) != len(golden.Fingerprints) ||
		len(cases.Specs) != len(golden.Specs) || len(cases.Statuses) != len(golden.Statuses) {
		t.Fatal("accesskey golden data is stale; regenerate it with hack/golden/accesskey_values.py")
	}
	return cases, golden
}

func TestParseDurationMatchesPythonGolden(t *testing.T) {
	cases, golden := loadAccessKey(t)
	for index, input := range cases.Durations {
		t.Run(input, func(t *testing.T) {
			duration, err := domain.ParseDuration(input)
			got := map[string]any{"seconds": nil, "error": err != nil}
			if err == nil {
				got["seconds"] = duration.Seconds()
			}
			compareJSON(t, golden.Durations[index], got)
		})
	}
}

func TestKeyFingerprintMatchesPythonGolden(t *testing.T) {
	cases, golden := loadAccessKey(t)
	for index, input := range cases.Fingerprints {
		if got := domain.KeyFingerprint(input); got != golden.Fingerprints[index] {
			t.Errorf("fingerprint of %q = %s, want %s", input, got, golden.Fingerprints[index])
		}
	}
}

func TestAccessKeySpecOutputsMatchPythonGolden(t *testing.T) {
	cases, golden := loadAccessKey(t)
	for index, testCase := range cases.Specs {
		t.Run(testCase.Name, func(t *testing.T) {
			spec := qdrantv1alpha1.QdrantAccessKeySpec{}
			decodeStrict(t, testCase.Spec, &spec)
			key, err := translate.AccessKeySpecFromAPI("app-token", "tenant-a", spec)
			if err != nil {
				compareJSON(t, golden.Specs[index], map[string]any{"error": true})
				return
			}
			issuedAt, err := domain.ParseTime(testCase.IssuedAt)
			if err != nil {
				t.Fatalf("issued_at: %v", err)
			}
			compareJSON(t, golden.Specs[index], accessKeyOutputs(key, issuedAt))
		})
	}
}

func accessKeyOutputs(key domain.AccessKeySpec, issuedAt time.Time) map[string]any {
	return map[string]any{
		"error":                false,
		"cluster_ref":          map[string]any{"name": key.ClusterRef.Name, "namespace": key.ClusterRef.Namespace},
		"secret_name":          key.SecretName,
		"access_claim":         key.AccessClaim(),
		"claims":               key.Claims(issuedAt),
		"expires_at_micros":    microsOrNil(key.ExpiresAt(issuedAt)),
		"renew_at_micros":      microsOrNil(key.RenewAt(issuedAt)),
		"ttl_seconds":          secondsOrNil(key.TTL),
		"renew_before_seconds": secondsOrNil(key.RenewBefore),
		"token_secret_ref": map[string]any{
			"name": key.TokenSecretRef().Name, "key": key.TokenSecretRef().Key, "namespace": key.TokenSecretRef().Namespace,
		},
	}
}

func microsOrNil(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UnixMicro()
}

func secondsOrNil(value time.Duration) any {
	if value == 0 {
		return nil
	}
	return value.Seconds()
}

func TestAccessKeyStatusOutputsMatchPythonGolden(t *testing.T) {
	cases, golden := loadAccessKey(t)
	for index, testCase := range cases.Statuses {
		t.Run(testCase.Name, func(t *testing.T) {
			apiStatus := qdrantv1alpha1.QdrantAccessKeyStatus{}
			decodeStrict(t, testCase.Status, &apiStatus)
			status, err := translate.AccessKeyStatusFromAPI("tenant-a", apiStatus)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			current := []any{}
			for _, check := range testCase.Checks {
				now, err := domain.ParseTime(check.Now)
				if err != nil {
					t.Fatalf("now: %v", err)
				}
				current = append(current, status.TokenCurrent(check.Generation, check.Fingerprint, check.SecretPresent, now))
			}
			var secretRef any
			if status.SecretRef != nil {
				secretRef = map[string]any{"name": status.SecretRef.Name, "key": status.SecretRef.Key, "namespace": status.SecretRef.Namespace}
			}
			compareJSON(t, golden.Statuses[index], map[string]any{
				"api":           translate.AccessKeyStatusToAPI(status),
				"secret_ref":    secretRef,
				"token_current": current,
			})
		})
	}
}

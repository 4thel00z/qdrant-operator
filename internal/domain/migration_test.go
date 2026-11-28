package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func TestParseDurationRejectsOverflow(t *testing.T) {
	for _, value := range []string{"99999999999999999999s", "9223372036854775807h", "2562047h2562047h"} {
		if _, err := domain.ParseDuration(value); err == nil {
			t.Errorf("%s: want an out of range error", value)
		}
	}
}

func TestAccessKeyClaimsOmitWhatIsUnset(t *testing.T) {
	spec := domain.DefaultAccessKeySpec("token", "ns", domain.ClusterRef{Name: "db", Namespace: "ns"})
	spec.GlobalAccess = "m"
	claims := spec.Claims(time.Date(2026, 9, 24, 20, 0, 0, 0, time.UTC))
	if len(claims) != 1 || claims["access"] != "m" {
		t.Errorf("claims = %v, want only access", claims)
	}
	if err := spec.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
	spec.Collections = []domain.CollectionAccess{{Name: "docs", Access: "r"}}
	if err := spec.Validate(); err == nil {
		t.Error("want an error when both access forms are set")
	}
}

func TestMigrationProgressRoundsHalfToEven(t *testing.T) {
	cases := []struct {
		copied, total int64
		want          int
	}{{1, 8, 12}, {3, 8, 38}, {25, 10, 100}, {0, 10, 0}}
	for _, testCase := range cases {
		progress := domain.MigrationProgress{PointsTotal: testCase.total, PointsCopied: testCase.copied}
		if got := progress.Percentage(); got != testCase.want {
			t.Errorf("%d of %d = %d, want %d", testCase.copied, testCase.total, got, testCase.want)
		}
	}
}

func TestMigrationSourceDescriptionIsEmptyWithoutASource(t *testing.T) {
	if got := (domain.MigrationSource{}).Description(); got != "" {
		t.Errorf("description = %q", got)
	}
}

func TestRouteNamesThePointAndFieldWhenThePayloadCannotRoute(t *testing.T) {
	overrides := domain.TargetOverrides{ShardKeyField: "tenant"}
	point := domain.Point{ID: domain.TextPointID("abc"), Payload: json.RawMessage(`{"tenant": 1.5}`)}
	_, err := overrides.Route(point)
	if err == nil || !strings.Contains(err.Error(), "abc") || !strings.Contains(err.Error(), "'tenant'") {
		t.Errorf("error = %v, want it to name the point and the field", err)
	}
}

func TestMigrationStatusResumePoint(t *testing.T) {
	status := domain.MigrationStatus{Collections: []domain.CollectionMigration{{Name: "docs", Offset: domain.NumberPointID(41)}}}
	found, ok := status.ResumePoint("docs")
	if !ok || found.Offset != domain.NumberPointID(41) {
		t.Errorf("resume point = %+v, %v", found, ok)
	}
	if _, ok := status.ResumePoint("ghost"); ok {
		t.Error("ghost has no resume point")
	}
}

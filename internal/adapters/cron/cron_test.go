package cron_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/adapters/cron"
)

type goldenSlot struct {
	At    string `json:"at"`
	Error string `json:"error"`
}

type goldenCase struct {
	Expression string     `json:"expression"`
	Now        string     `json:"now"`
	Prev       goldenSlot `json:"prev"`
	Next       goldenSlot `json:"next"`
}

func TestMatchesCroniter(t *testing.T) {
	raw, err := os.ReadFile("testdata/golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		now, err := time.Parse(time.RFC3339Nano, c.Now)
		if err != nil {
			t.Fatal(err)
		}
		schedule, parseErr := cron.Parse(c.Expression)
		if c.Prev.Error != "" && parseErr != nil {
			continue
		}
		if parseErr != nil {
			t.Errorf("%q: Python accepts, Go rejects: %v", c.Expression, parseErr)
			continue
		}
		check(t, "previous", c.Expression, now, c.Prev, schedule.Previous)
		check(t, "next", c.Expression, now, c.Next, schedule.Next)
	}
}

func check(t *testing.T, direction string, expression string, now time.Time, want goldenSlot, got func(time.Time) (time.Time, error)) {
	t.Helper()
	slot, err := got(now)
	if want.Error != "" {
		if err == nil {
			t.Errorf("%s %q from %s: Python fails with %s, Go returns %s", direction, expression, now.Format(time.RFC3339Nano), want.Error, slot.Format(time.RFC3339))
		}
		return
	}
	if err != nil {
		t.Errorf("%s %q from %s: %v", direction, expression, now.Format(time.RFC3339Nano), err)
		return
	}
	wantAt, err := time.Parse(time.RFC3339Nano, want.At)
	if err != nil {
		t.Fatal(err)
	}
	if !slot.Equal(wantAt) {
		t.Errorf("%s %q from %s: got %s, croniter %s", direction, expression, now.Format(time.RFC3339Nano), slot.Format(time.RFC3339), wantAt.Format(time.RFC3339))
	}
}

func TestParseRejects(t *testing.T) {
	for _, expression := range []string{"", "* * * *", "* * * * * *", "0 0 * * * 2030", "@always", "@every5m", "60 * * * *", "a b c d e", "*/0 * * * *"} {
		if _, err := cron.Parse(expression); err == nil {
			t.Errorf("%q accepted", expression)
		}
	}
}

func TestResultsAreUTC(t *testing.T) {
	schedule, err := cron.Parse("0 12 * * *")
	if err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("plus2", 2*3600)
	slot, err := schedule.Next(time.Date(2024, 1, 1, 13, 0, 0, 0, zone))
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC); !slot.Equal(want) || slot.Location() != time.UTC {
		t.Fatalf("got %s", slot)
	}
}

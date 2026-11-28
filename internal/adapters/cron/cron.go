// Package cron implements domain.CronSchedule on gronx.
//
// It accepts what the QdrantBackupSchedule CRD admits and croniter parsed in the Python operator:
// five fields, or one of the croniter aliases. All slots are UTC.
package cron

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/adhocore/gronx"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

var aliases = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Schedule is a validated cron expression. When day-of-month and day-of-week are both restricted,
// croniter fires on either (Vixie cron), also for star-step fields such as "*/10" where gronx would require both;
// so that case is held as two expressions whose slots are merged.
type Schedule struct {
	expressions []string
}

var _ domain.CronSchedule = Schedule{}

func unrestricted(field string) bool {
	return field == "*" || field == "?"
}

func Parse(expression string) (Schedule, error) {
	fields := strings.Fields(expression)
	if len(fields) == 1 && strings.HasPrefix(fields[0], "@") {
		expanded, ok := aliases[strings.ToLower(fields[0])]
		if !ok {
			return Schedule{}, fmt.Errorf("cron expression %q: unknown alias", expression)
		}
		return Parse(expanded)
	}
	if len(fields) != 5 {
		return Schedule{}, fmt.Errorf("cron expression %q: want 5 fields, got %d", expression, len(fields))
	}
	if !gronx.IsValid(strings.Join(fields, " ")) {
		return Schedule{}, fmt.Errorf("cron expression %q: invalid syntax", expression)
	}
	if unrestricted(fields[2]) || unrestricted(fields[4]) {
		return Schedule{expressions: []string{strings.Join(fields, " ")}}, nil
	}
	byDay := slices.Clone(fields)
	byDay[4] = "*"
	byWeekday := slices.Clone(fields)
	byWeekday[2] = "*"
	return Schedule{expressions: []string{strings.Join(byDay, " "), strings.Join(byWeekday, " ")}}, nil
}

// Previous is the latest slot strictly before after.
func (s Schedule) Previous(after time.Time) (time.Time, error) {
	instant := after.UTC()
	whole := instant.Truncate(time.Second)
	return s.merge("previous", instant, func(expression string) (time.Time, error) {
		return gronx.PrevTickBefore(expression, whole, !whole.Equal(instant))
	}, time.Time.After)
}

// Next is the earliest slot strictly after after.
func (s Schedule) Next(after time.Time) (time.Time, error) {
	instant := after.UTC()
	return s.merge("next", instant, func(expression string) (time.Time, error) {
		return gronx.NextTickAfter(expression, instant.Truncate(time.Second), false)
	}, time.Time.Before)
}

func (s Schedule) merge(direction string, instant time.Time, tick func(string) (time.Time, error), closer func(time.Time, time.Time) bool) (time.Time, error) {
	var best time.Time
	var failure error
	for _, expression := range s.expressions {
		slot, err := tick(expression)
		if err != nil {
			failure = fmt.Errorf("%s slot of %q from %s: %w", direction, expression, instant.Format(time.RFC3339Nano), err)
			continue
		}
		if best.IsZero() || closer(slot.UTC(), best) {
			best = slot.UTC()
		}
	}
	if best.IsZero() {
		return time.Time{}, failure
	}
	return best, nil
}

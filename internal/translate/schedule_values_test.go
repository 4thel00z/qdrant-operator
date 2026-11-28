package translate_test

import (
	"encoding/json"
	"testing"
	"time"

	qdrantv1alpha1 "github.com/4thel00z/qdrant-operator/api/v1alpha1"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/translate"
)

type scheduleGolden struct {
	Owner struct {
		APIVersion string `json:"apiVersion"`
		Kind       string `json:"kind"`
		Name       string `json:"name"`
		UID        string `json:"uid"`
	} `json:"owner"`
	Specs []struct {
		Name      string          `json:"name"`
		Spec      json.RawMessage `json:"spec"`
		Now       string          `json:"now"`
		Baselines []*string       `json:"baselines"`
		BackupAt  string          `json:"backup_at"`
		Outputs   map[string]any  `json:"outputs"`
	} `json:"specs"`
	Statuses []struct {
		Name    string               `json:"name"`
		Status  json.RawMessage      `json:"status"`
		Recent  []goldenBackupRecord `json:"recent"`
		Outputs map[string]any       `json:"outputs"`
	} `json:"statuses"`
}

// slotsCron answers with the slots Python's croniter computed for the case.
type slotsCron struct {
	previous time.Time
	next     time.Time
}

func (c slotsCron) Previous(time.Time) (time.Time, error) { return c.previous, nil }

func (c slotsCron) Next(time.Time) (time.Time, error) { return c.next, nil }

func TestScheduleOutputsMatchPythonGolden(t *testing.T) {
	golden := scheduleGolden{}
	readJSON(t, "testdata/schedule_values_golden.json", &golden)
	owner := domain.OwnerReference{APIVersion: golden.Owner.APIVersion, Kind: golden.Owner.Kind, Name: golden.Owner.Name, UID: golden.Owner.UID}
	for _, testCase := range golden.Specs {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantBackupScheduleSpec{}
			decodeStrict(t, testCase.Spec, &api)
			spec, err := translate.ScheduleSpecFromAPI("demo", "tenant-a", api)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			cron := slotsCron{
				previous: mustTime(t, testCase.Outputs["previous_slot"].(string)),
				next:     mustTime(t, testCase.Outputs["next_slot"].(string)),
			}
			now := mustTime(t, testCase.Now)
			previous, err := spec.PreviousSlot(cron, now)
			if err != nil {
				t.Fatalf("previous: %v", err)
			}
			next, err := spec.NextSlot(cron, now)
			if err != nil {
				t.Fatalf("next: %v", err)
			}
			due := []any{}
			for _, baseline := range testCase.Baselines {
				var base time.Time
				if baseline != nil {
					base = mustTime(t, *baseline)
				}
				slot, ok, err := spec.Due(cron, now, base)
				if err != nil {
					t.Fatalf("due: %v", err)
				}
				if !ok {
					due = append(due, nil)
					continue
				}
				due = append(due, domain.FormatTime(slot))
			}
			backupName := spec.BackupName(mustTime(t, testCase.BackupAt))
			var deadline any
			if spec.StartingDeadline != nil {
				deadline = int64(spec.StartingDeadline.Seconds())
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"label_selector":               spec.LabelSelector(),
				"backup_name":                  backupName,
				"previous_slot":                domain.FormatTime(previous),
				"next_slot":                    domain.FormatTime(next),
				"due":                          due,
				"backup_resource":              spec.ToBackupResource(backupName, owner),
				"owner_reference":              owner.JSON(),
				"suspend":                      spec.Suspend,
				"concurrency_policy":           string(spec.ConcurrencyPolicy),
				"starting_deadline_seconds":    deadline,
				"delete_backups_with_schedule": spec.DeleteBackupsWithSchedule,
				"retention_configured":         spec.RetentionPolicy.Configured(),
				"method":                       string(spec.Method),
				"collections":                  spec.Collections,
			})
		})
	}
}

func TestScheduleStatusMatchesPythonGolden(t *testing.T) {
	golden := scheduleGolden{}
	readJSON(t, "testdata/schedule_values_golden.json", &golden)
	for _, testCase := range golden.Statuses {
		t.Run(testCase.Name, func(t *testing.T) {
			api := qdrantv1alpha1.QdrantBackupScheduleStatus{}
			decodeStrict(t, testCase.Status, &api)
			status, err := translate.ScheduleStatusFromAPI(api)
			if err != nil {
				t.Fatalf("translate: %v", err)
			}
			for _, recent := range testCase.Recent {
				status.RecentBackups = append(status.RecentBackups, domain.BackupRecord{
					Name:           recent.Name,
					CreationTime:   mustTime(t, recent.CreationTime),
					Phase:          domain.BackupPhase(recent.Phase),
					CompletionTime: mustTime(t, recent.CompletionTime),
					TotalSize:      recent.Size,
				})
			}
			compareJSON(t, testCase.Outputs, map[string]any{
				"api": pruneEmptyValues(roundTrip(t, translate.ScheduleStatusToAPI(status))),
			})
		})
	}
}

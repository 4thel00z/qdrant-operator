package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const failedPhase = "Failed"

// WriteStatus replaces the whole status subresource with status, so a field the new status leaves
// out is removed rather than kept. It needs no resourceVersion, so the status patches use cases
// make while they run cannot make it conflict. Conditions without a transition time get now.
// A phase change is also recorded as an event on the object.
func (d Dependencies) WriteStatus(ctx context.Context, object client.Object, status any) error {
	body, err := usecases.EncodeObject(status)
	if err != nil {
		return err
	}
	StampConditions(body, d.Clock())
	before := PhaseOf(object)
	patch, err := json.Marshal([]domain.JSON{{"op": "add", "path": "/status", "value": body}})
	if err != nil {
		return fmt.Errorf("encode status patch: %w", err)
	}
	if err := client.IgnoreNotFound(d.Client.Status().Patch(ctx, object, client.RawPatch(types.JSONPatchType, patch))); err != nil {
		return fmt.Errorf("write status of %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	d.announce(object, before, body)
	return nil
}

func (d Dependencies) announce(object client.Object, before string, after domain.JSON) {
	phase, _ := after["phase"].(string)
	if phase == "" || phase == before {
		return
	}
	d.Recorder.Eventf(object, nil, EventType(phase), phase, "Reconcile", "%s", EventNote(before, after))
}

func EventType(phase string) string {
	if phase == failedPhase {
		return corev1.EventTypeWarning
	}
	return corev1.EventTypeNormal
}

// EventNote prefers the status error or message and falls back to the phase transition.
func EventNote(before string, after domain.JSON) string {
	for _, key := range []string{"error", "message"} {
		if text, _ := after[key].(string); text != "" {
			return text
		}
	}
	if before == "" {
		return fmt.Sprintf("phase is %s", after["phase"])
	}
	return fmt.Sprintf("phase changed from %s to %s", before, after["phase"])
}

func PhaseOf(object client.Object) string {
	encoded, err := usecases.EncodeObject(object)
	if err != nil {
		return ""
	}
	status, _ := encoded["status"].(map[string]any)
	phase, _ := status["phase"].(string)
	return phase
}

func StampConditions(body domain.JSON, now time.Time) {
	conditions, ok := body["conditions"].([]any)
	if !ok {
		return
	}
	for _, entry := range conditions {
		condition, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if stamp, _ := condition["lastTransitionTime"].(string); stamp != "" {
			continue
		}
		condition["lastTransitionTime"] = domain.FormatTime(now)
	}
}

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

// WriteStatus replaces the whole status subresource with status, so a field the new status leaves
// out is removed rather than kept. It needs no resourceVersion, so the status patches use cases
// make while they run cannot make it conflict. Conditions without a transition time get now.
func WriteStatus(ctx context.Context, writer client.Client, object client.Object, status any, now time.Time) error {
	body, err := usecases.EncodeObject(status)
	if err != nil {
		return err
	}
	StampConditions(body, now)
	patch, err := json.Marshal([]domain.JSON{{"op": "add", "path": "/status", "value": body}})
	if err != nil {
		return fmt.Errorf("encode status patch: %w", err)
	}
	if err := client.IgnoreNotFound(writer.Status().Patch(ctx, object, client.RawPatch(types.JSONPatchType, patch))); err != nil {
		return fmt.Errorf("write status of %s/%s: %w", object.GetNamespace(), object.GetName(), err)
	}
	return nil
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

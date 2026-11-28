package kubernetes

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func statefulSetKey(name, namespace string) client.ObjectKey {
	return client.ObjectKey{Namespace: namespace, Name: name}
}

func (a *Adapter) StatefulSetStatus(ctx context.Context, name, namespace string) (*domain.StatefulSetStatus, error) {
	statefulSet := &appsv1.StatefulSet{}
	err := a.Reader.Get(ctx, statefulSetKey(name, namespace), statefulSet)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get statefulset %s/%s: %w", namespace, name, err)
	}
	return &domain.StatefulSetStatus{
		Replicas:        int(statefulSet.Status.Replicas),
		ReadyReplicas:   int(statefulSet.Status.ReadyReplicas),
		UpdatedReplicas: int(statefulSet.Status.UpdatedReplicas),
		CurrentRevision: statefulSet.Status.CurrentRevision,
		UpdateRevision:  statefulSet.Status.UpdateRevision,
		Partition:       partition(statefulSet),
	}, nil
}

func partition(statefulSet *appsv1.StatefulSet) int {
	rolling := statefulSet.Spec.UpdateStrategy.RollingUpdate
	if rolling == nil || rolling.Partition == nil {
		return 0
	}
	return int(*rolling.Partition)
}

func (a *Adapter) SetStatefulSetPartition(ctx context.Context, name, namespace string, partition int) error {
	patch, err := mergePatch(domain.JSON{"spec": domain.JSON{"updateStrategy": domain.JSON{"rollingUpdate": domain.JSON{"partition": partition}}}})
	if err != nil {
		return err
	}
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := a.Writer.Patch(ctx, statefulSet, patch); err != nil {
		return fmt.Errorf("set partition of statefulset %s/%s: %w", namespace, name, err)
	}
	return nil
}

// OrphanStatefulSet returns once the StatefulSet is gone, which needs the garbage collector to
// remove the orphan finalizer; it fails after OrphanTimeout.
func (a *Adapter) OrphanStatefulSet(ctx context.Context, name, namespace string) error {
	statefulSet := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}
	if err := ignoreNotFound(a.Writer.Delete(ctx, statefulSet, client.PropagationPolicy(metav1.DeletePropagationOrphan))); err != nil {
		return fmt.Errorf("orphan-delete statefulset %s/%s: %w", namespace, name, err)
	}
	deadline := time.Now().Add(a.OrphanTimeout)
	for {
		err := a.Reader.Get(ctx, statefulSetKey(name, namespace), &appsv1.StatefulSet{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("wait for statefulset %s/%s: %w", namespace, name, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("statefulset %s/%s still present after %s", namespace, name, a.OrphanTimeout)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for statefulset %s/%s: %w", namespace, name, ctx.Err())
		case <-time.After(a.OrphanPoll):
		}
	}
}

// StatefulSetClaims returns nil when the StatefulSet is absent.
func (a *Adapter) StatefulSetClaims(ctx context.Context, name, namespace string) (map[string]string, error) {
	statefulSet := &appsv1.StatefulSet{}
	err := a.Reader.Get(ctx, statefulSetKey(name, namespace), statefulSet)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get statefulset %s/%s: %w", namespace, name, err)
	}
	claims := map[string]string{}
	for _, template := range statefulSet.Spec.VolumeClaimTemplates {
		claims[template.Name] = requestedSize(template.Spec.Resources.Requests)
	}
	return claims, nil
}

func requestedSize(requests corev1.ResourceList) string {
	size, ok := requests[corev1.ResourceStorage]
	if !ok {
		return ""
	}
	return size.String()
}

func (a *Adapter) ListPodStates(ctx context.Context, namespace, labelSelector string) ([]domain.PodState, error) {
	options, err := listOptions(namespace, labelSelector)
	if err != nil {
		return nil, err
	}
	pods := &corev1.PodList{}
	if err := a.Reader.List(ctx, pods, options...); err != nil {
		return nil, fmt.Errorf("list pods in %s: %w", namespace, err)
	}
	states := make([]domain.PodState, 0, len(pods.Items))
	for _, pod := range pods.Items {
		states = append(states, podState(pod))
	}
	return states, nil
}

func podState(pod corev1.Pod) domain.PodState {
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			ready = true
		}
	}
	return domain.PodState{
		Name:     pod.Name,
		Revision: pod.Labels[revisionLabel],
		Ready:    ready && pod.DeletionTimestamp == nil,
	}
}

// CrashLog joins the current and the previous container log; an absent previous run contributes nothing.
func (a *Adapter) CrashLog(ctx context.Context, name, namespace string, tailLines int) (string, error) {
	current, err := a.containerLog(ctx, name, namespace, tailLines, false)
	if err != nil {
		return "", err
	}
	previous, err := a.containerLog(ctx, name, namespace, tailLines, true)
	if err != nil {
		return "", err
	}
	return combineLogs(current, previous), nil
}

func combineLogs(current, previous string) string {
	return strings.Join([]string{current, previous}, "\n")
}

func (a *Adapter) containerLog(ctx context.Context, name, namespace string, tailLines int, previous bool) (string, error) {
	lines := int64(tailLines)
	stream, err := a.Logs.CoreV1().Pods(namespace).GetLogs(name, &corev1.PodLogOptions{Previous: previous, TailLines: &lines}).Stream(ctx)
	if apierrors.IsNotFound(err) || apierrors.IsBadRequest(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("stream log of pod %s/%s: %w", namespace, name, err)
	}
	defer stream.Close()
	content, err := io.ReadAll(stream)
	if err != nil {
		return "", fmt.Errorf("read log of pod %s/%s: %w", namespace, name, err)
	}
	return string(content), nil
}

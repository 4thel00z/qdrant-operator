package usecases_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/usecases"
)

const (
	clusterNS      = "tenant-a"
	clusterRelease = "qdrant-db"
	clusterStsKey  = clusterNS + "/" + clusterRelease
)

func clusterClock() time.Time {
	return time.Date(2026, 9, 23, 2, 30, 0, 0, time.UTC)
}

func clusterPending() domain.ClusterStatus {
	return domain.ClusterStatus{Phase: domain.PhasePending}
}

func clusterSpec(t *testing.T, replicas int, extra domain.JSON) domain.ClusterSpec {
	t.Helper()
	spec := domain.JSON{"version": "1.16.3", "replicas": replicas}
	for key, value := range extra {
		spec[key] = value
	}
	body := domain.JSON{
		"apiVersion": domain.APIVersion,
		"kind":       "QdrantCluster",
		"metadata":   domain.JSON{"name": "db", "namespace": clusterNS},
		"spec":       spec,
	}
	parsed, err := usecases.ClusterSpecFromBody(body)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func clusterKubernetes() *fakes.Kubernetes {
	kubernetes := fakes.NewKubernetes()
	kubernetes.PutResource(domain.JSON{
		"kind":     "QdrantCluster",
		"metadata": domain.JSON{"name": "db", "namespace": clusterNS},
		"spec":     domain.JSON{"version": "1.16.3"},
	})
	return kubernetes
}

func clusterPodURL(ordinal int) string {
	return fmt.Sprintf("http://%s-%d.%s-headless.%s.svc.cluster.local:6333", clusterRelease, ordinal, clusterRelease, clusterNS)
}

func clusterPeerURI(ordinal int) string {
	return fmt.Sprintf("http://%s-%d.%s-headless.%s.svc.cluster.local:6335/", clusterRelease, ordinal, clusterRelease, clusterNS)
}

func claimSet(pods int) map[string]string {
	claims := map[string]string{}
	for _, template := range []string{"qdrant-storage", "qdrant-snapshots"} {
		for ordinal := range pods {
			claims[fmt.Sprintf("%s-%s-%d", template, clusterRelease, ordinal)] = "10Gi"
		}
	}
	return claims
}

type conditionState struct {
	Status domain.ConditionStatus
	Reason string
}

func conditionsOf(status domain.ClusterStatus) map[string]conditionState {
	states := map[string]conditionState{}
	for _, condition := range status.Conditions {
		states[condition.Type] = conditionState{Status: condition.Status, Reason: condition.Reason}
	}
	return states
}

func conditionMessage(status domain.ClusterStatus, conditionType string) string {
	condition := domain.FindCondition(status.Conditions, conditionType)
	if condition == nil {
		return ""
	}
	return condition.Message
}

func peeredQdrant(pods int) *fakes.Qdrant {
	qdrant := fakes.NewQdrant()
	qdrant.AddCollection(clusterPodURL(0), "docs", 0, nil)
	qdrant.ReplicationFactors["docs"] = 1
	for shard := range pods {
		qdrant.Replicas["docs"] = append(qdrant.Replicas["docs"], domain.Replica{ShardID: shard, PeerID: uint64(100 + shard), State: domain.ShardActive})
	}
	for ordinal := range pods {
		qdrant.Peers[uint64(100+ordinal)] = clusterPeerURI(ordinal)
		qdrant.NodePeerIDs[clusterPodURL(ordinal)] = uint64(100 + ordinal)
	}
	return qdrant
}

// rebuiltPodQdrant has pod 1 back on an empty volume: new peer 201 at the URI of dead peer 101.
func rebuiltPodQdrant() *fakes.Qdrant {
	qdrant := peeredQdrant(3)
	qdrant.ReplicationFactors["docs"] = 2
	qdrant.Replicas["docs"] = []domain.Replica{
		{ShardID: 0, PeerID: 100, State: domain.ShardActive},
		{ShardID: 0, PeerID: 101, State: "Dead"},
		{ShardID: 1, PeerID: 101, State: "Dead"},
		{ShardID: 1, PeerID: 102, State: domain.ShardActive},
	}
	qdrant.Peers[201] = clusterPeerURI(1)
	qdrant.NodePeerIDs[clusterPodURL(1)] = 201
	return qdrant
}

// crashLoopingPodQdrant has pod 1 crash-looping: Qdrant refuses to join at dead peer 101's URI.
func crashLoopingPodQdrant() *fakes.Qdrant {
	qdrant := rebuiltPodQdrant()
	delete(qdrant.Peers, 201)
	delete(qdrant.NodePeerIDs, clusterPodURL(1))
	return qdrant
}

func settledStatefulSet(kubernetes *fakes.Kubernetes, replicas int) {
	kubernetes.StatefulSets[clusterStsKey] = domain.StatefulSetStatus{Replicas: replicas, ReadyReplicas: replicas, Partition: replicas}
}

func clusterOperationKinds(operations []domain.ShardOperation) []domain.ShardOperationKind {
	kinds := []domain.ShardOperationKind{}
	for _, operation := range operations {
		kinds = append(kinds, operation.Kind)
	}
	return kinds
}

func clusterAssertEqual[T any](t *testing.T, got T, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

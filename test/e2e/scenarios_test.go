//go:build e2e

package e2e_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const (
	scenarioCluster    = "TestClusterBecomesRunning"
	scenarioCollection = "TestCollectionIsCreatedWithItsPayloadIndex"
	scenarioBackup     = "TestBackupStreamsSnapshotsAndManifestIntoTheBucket"
)

func TestClusterBecomesRunning(t *testing.T) {
	scenario(t)
	apply(t, customResource(clusters, clusterName, object{
		"version":     qdrantVersion,
		"replicas":    int64(1),
		"resources":   object{"requests": object{"cpu": "100m", "memory": "256Mi"}},
		"persistence": object{"size": "1Gi"},
		"apiKey": object{
			"secretRef": object{"name": apiKeySecret, "key": "api-key"},
			"jwtRbac":   true,
		},
	}))
	status := waitForPhase(t, clusters, clusterName, "Running")
	if status["readyReplicas"] != int64(1) {
		t.Fatalf("readyReplicas = %v, want 1", status["readyReplicas"])
	}
}

func TestCollectionIsCreatedWithItsPayloadIndex(t *testing.T) {
	scenario(t, scenarioCluster)
	apply(t, customResource(collections, collectionName, object{
		"clusterRef":     object{"name": clusterName},
		"vectors":        []any{object{"size": int64(4), "distance": "Dot"}},
		"payloadIndexes": []any{object{"field": "group", "type": "keyword"}},
		"deletionPolicy": "Delete",
	}))
	waitForPhase(t, collections, collectionName, "Ready")
	schema := qdrant(t).collectionPayloadSchema(t, collectionName)
	if _, ok := schema["group"]; !ok {
		t.Fatalf("payload_schema has no group index: %v", schema)
	}
}

func TestAccessKeyTokenIsAcceptedByQdrant(t *testing.T) {
	scenario(t, scenarioCluster, scenarioCollection)
	apply(t, customResource(accessKeys, accessKeyName, object{
		"clusterRef": object{"name": clusterName},
		"access":     "r",
		"ttl":        "1h",
	}))
	waitForPhase(t, accessKeys, accessKeyName, "Ready")
	token := secretValue(t, accessKeyName, tokenSecretKey)
	got := qdrant(t).withKey(token).listCollections(t)
	if !reflect.DeepEqual(got, []string{collectionName}) {
		t.Fatalf("collections = %v, want [%s]", got, collectionName)
	}
}

func TestBackupStreamsSnapshotsAndManifestIntoTheBucket(t *testing.T) {
	scenario(t, scenarioCluster, scenarioCollection)
	points := []object{}
	for i := 1; i <= pointCount; i++ {
		points = append(points, object{
			"id":      i,
			"vector":  []float64{float64(i), 1.0, 0.5, 0.25},
			"payload": object{"group": fmt.Sprintf("g%d", i%4)},
		})
	}
	qdrant(t).upsertPoints(t, collectionName, points)
	storage := openBucket(t)
	storage.create(t, bucketName)

	apply(t, customResource(backups, backupName, object{
		"clusterRef": object{"name": clusterName},
		"storage": object{"s3": object{
			"bucket":               bucketName,
			"prefix":               bucketPrefix,
			"endpoint":             s3Endpoint,
			"forcePathStyle":       true,
			"credentialsSecretRef": object{"name": s3Secret},
		}},
	}))
	waitForPhase(t, backups, backupName, "Completed")

	names, nodeCount := storage.manifest(t, bucketName, joinKey(bucketPrefix, backupName, manifestKey))
	if !reflect.DeepEqual(names, []string{collectionName}) {
		t.Fatalf("manifest collections = %v, want [%s]", names, collectionName)
	}
	if nodeCount != 1 {
		t.Fatalf("manifest nodeCount = %d, want 1", nodeCount)
	}
}

func TestRestoreRecoversThePointsIntoANewCollection(t *testing.T) {
	scenario(t, scenarioCluster, scenarioBackup)
	apply(t, customResource(restores, restoreName, object{
		"backupRef":         object{"name": backupName},
		"targetClusterRef":  object{"name": clusterName},
		"collectionMapping": object{collectionName: restoredName},
	}))
	waitForPhase(t, restores, restoreName, "Completed")
	if got := qdrant(t).countPoints(t, restoredName); got != pointCount {
		t.Fatalf("restored points = %d, want %d", got, pointCount)
	}
}

func TestDeletingTheBackupRemovesItsObjects(t *testing.T) {
	scenario(t, scenarioBackup)
	deleteAndWait(t, backups, backupName)
	prefix := fmt.Sprintf("%s/%s/", bucketPrefix, backupName)
	if got := openBucket(t).countObjects(t, bucketName, prefix); got != 0 {
		t.Fatalf("%d objects remain under %s", got, prefix)
	}
}

func TestDeletingTheCollectionDropsIt(t *testing.T) {
	scenario(t, scenarioCluster, scenarioCollection)
	deleteAndWait(t, collections, collectionName)
	if qdrant(t).collectionExists(t, collectionName) {
		t.Fatalf("collection %s still exists", collectionName)
	}
}

func TestDeletingTheClusterUninstallsItsRelease(t *testing.T) {
	scenario(t, scenarioCluster)
	deleteAndWait(t, accessKeys, accessKeyName)
	deleteAndWait(t, restores, restoreName)
	deleteAndWait(t, clusters, clusterName)
	if got := releaseSecretCount(t, "qdrant-"+clusterName); got != 0 {
		t.Fatalf("helm release qdrant-%s still has %d release secrets", clusterName, got)
	}
}

func TestOperatorNeverRestarted(t *testing.T) {
	scenario(t)
	output := kubectl(t, "get", "pods", "-n", operatorNamespace, "-l", operatorSelector, "-o", "json")
	var pods struct {
		Items []struct {
			Status struct {
				ContainerStatuses []struct {
					RestartCount int `json:"restartCount"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &pods); err != nil {
		t.Fatalf("decode pods: %v", err)
	}
	restarts := []int{}
	for _, pod := range pods.Items {
		for _, status := range pod.Status.ContainerStatuses {
			restarts = append(restarts, status.RestartCount)
		}
	}
	if !reflect.DeepEqual(restarts, []int{0}) {
		t.Fatalf("restart counts = %v, want [0]", restarts)
	}
}

func TestOperatorLoggedNoPermanentFailures(t *testing.T) {
	scenario(t)
	logs := kubectl(t, "logs", "-n", operatorNamespace, "-l", operatorSelector, "--tail=-1")
	failures := []string{}
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "failed permanently") {
			failures = append(failures, line)
		}
	}
	if len(failures) > 0 {
		t.Fatalf("operator logged permanent failures: %v", failures)
	}
}

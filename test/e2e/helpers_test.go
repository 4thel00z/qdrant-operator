//go:build e2e

package e2e_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const apiVersion = "qdrant.io/v1alpha1"

type object = map[string]any

type resourceKind struct {
	kind   string
	plural string
}

var (
	clusters    = resourceKind{"QdrantCluster", "qdrantclusters"}
	backups     = resourceKind{"QdrantBackup", "qdrantbackups"}
	restores    = resourceKind{"QdrantRestore", "qdrantrestores"}
	collections = resourceKind{"QdrantCollection", "qdrantcollections"}
	accessKeys  = resourceKind{"QdrantAccessKey", "qdrantaccesskeys"}
)

var kube client.Client

var (
	passedMutex sync.Mutex
	passed      = map[string]bool{}
)

func initClient() error {
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	kube, err = client.New(cfg, client.Options{})
	if err != nil {
		return fmt.Errorf("create kubernetes client: %w", err)
	}
	return nil
}

func scenario(t *testing.T, prerequisites ...string) {
	t.Helper()
	passedMutex.Lock()
	defer passedMutex.Unlock()
	for _, name := range prerequisites {
		if !passed[name] {
			t.Fatalf("prerequisite scenario %s did not pass", name)
		}
	}
	t.Cleanup(func() {
		passedMutex.Lock()
		defer passedMutex.Unlock()
		passed[t.Name()] = !t.Failed() && !t.Skipped()
	})
}

func kubectlRun(args ...string) (string, error) {
	command := exec.Command("kubectl", args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

func kubectl(t *testing.T, args ...string) string {
	t.Helper()
	output, err := kubectlRun(args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func toUnstructured(item object) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: item}
}

func createAll(ctx context.Context, items []object) error {
	for _, item := range items {
		target := toUnstructured(item)
		target.SetNamespace(namespace)
		if err := kube.Create(ctx, target); err != nil {
			return fmt.Errorf("create %s %s: %w", target.GetKind(), target.GetName(), err)
		}
	}
	return nil
}

func apply(t *testing.T, items ...object) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := createAll(ctx, items); err != nil {
		t.Fatal(err)
	}
}

func customResource(kind resourceKind, name string, spec object) object {
	return object{
		"apiVersion": apiVersion,
		"kind":       kind.kind,
		"metadata":   object{"name": name},
		"spec":       spec,
	}
}

func secretObject(name string, data map[string]string) object {
	return object{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   object{"name": name},
		"stringData": toAnyMap(data),
	}
}

func toAnyMap(values map[string]string) object {
	converted := object{}
	for key, value := range values {
		converted[key] = value
	}
	return converted
}

func s3Server() []object {
	labels := object{"app": s3Service}
	container := object{
		"name":  s3Service,
		"image": s3Image,
		"env": []any{
			object{"name": "RUSTFS_ACCESS_KEY", "value": s3AccessKey},
			object{"name": "RUSTFS_SECRET_KEY", "value": s3SecretKey},
			object{"name": "RUSTFS_OBS_LOG_DIRECTORY", "value": ""},
		},
		"ports":          []any{object{"containerPort": int64(s3Port)}},
		"readinessProbe": object{"tcpSocket": object{"port": int64(s3Port)}, "periodSeconds": int64(2)},
		"volumeMounts":   []any{object{"name": "data", "mountPath": "/data"}},
	}
	deployment := object{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   object{"name": s3Service},
		"spec": object{
			"selector": object{"matchLabels": labels},
			"template": object{
				"metadata": object{"labels": labels},
				"spec": object{
					"containers": []any{container},
					"volumes":    []any{object{"name": "data", "emptyDir": object{}}},
				},
			},
		},
	}
	service := object{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   object{"name": s3Service},
		"spec":       object{"selector": labels, "ports": []any{object{"port": int64(s3Port)}}},
	}
	return []object{deployment, service}
}

func fetch(ctx context.Context, kind resourceKind, name string) (*unstructured.Unstructured, error) {
	found := &unstructured.Unstructured{}
	found.SetAPIVersion(apiVersion)
	found.SetKind(kind.kind)
	err := kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, found)
	if errors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get %s %s: %w", kind.kind, name, err)
	}
	return found, nil
}

func secretValue(t *testing.T, name, key string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	found := &unstructured.Unstructured{}
	found.SetAPIVersion("v1")
	found.SetKind("Secret")
	if err := kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, found); err != nil {
		t.Fatalf("get secret %s: %v", name, err)
	}
	encoded, ok, err := unstructured.NestedString(found.Object, "data", key)
	if err != nil || !ok {
		t.Fatalf("secret %s has no key %s", name, key)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode secret %s key %s: %v", name, key, err)
	}
	return string(decoded)
}

func waitForPhase(t *testing.T, kind resourceKind, name, phase string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	status := map[string]any{}
	for time.Now().Before(deadline) {
		found, err := fetch(context.Background(), kind, name)
		if err != nil {
			t.Fatal(err)
		}
		status = statusOf(found)
		current, _ := status["phase"].(string)
		if current == phase {
			return status
		}
		if current == failedPhase {
			t.Fatalf("%s %s failed: %v", kind.kind, name, status["error"])
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("%s %s never reached %s; last status %v", kind.kind, name, phase, status)
	return nil
}

func statusOf(found *unstructured.Unstructured) map[string]any {
	if found == nil {
		return map[string]any{}
	}
	status, _, _ := unstructured.NestedMap(found.Object, "status")
	if status == nil {
		return map[string]any{}
	}
	return status
}

func deleteAndWait(t *testing.T, kind resourceKind, name string) {
	t.Helper()
	target := &unstructured.Unstructured{}
	target.SetAPIVersion(apiVersion)
	target.SetKind(kind.kind)
	target.SetNamespace(namespace)
	target.SetName(name)
	if err := kube.Delete(context.Background(), target); err != nil {
		t.Fatalf("delete %s %s: %v", kind.kind, name, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found, err := fetch(context.Background(), kind, name)
		if err != nil {
			t.Fatal(err)
		}
		if found == nil {
			return
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("%s %s still exists; its finalizer never finished", kind.kind, name)
}

func portForward(t *testing.T, service string, localPort, remotePort int) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, "kubectl", "port-forward", "-n", namespace,
		"svc/"+service, fmt.Sprintf("%d:%d", localPort, remotePort))
	stdout, err := command.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("port-forward to %s: %v", service, err)
	}
	if err := command.Start(); err != nil {
		cancel()
		t.Fatalf("port-forward to %s: %v", service, err)
	}
	t.Cleanup(func() {
		cancel()
		command.Wait()
	})
	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- line
		io.Copy(io.Discard, stdout)
	}()
	select {
	case line := <-lines:
		if !strings.HasPrefix(line, "Forwarding from") {
			t.Fatalf("port-forward to %s failed: %q", service, line)
		}
	case <-time.After(forwardTimeout):
		t.Fatalf("port-forward to %s timed out", service)
	}
	return fmt.Sprintf("127.0.0.1:%d", localPort)
}

type qdrantNode struct {
	address string
	apiKey  string
}

func qdrant(t *testing.T) qdrantNode {
	t.Helper()
	return qdrantNode{address: portForward(t, "qdrant-"+clusterName, qdrantLocalPort, 6333), apiKey: apiKey}
}

func (node qdrantNode) withKey(key string) qdrantNode {
	return qdrantNode{address: node.address, apiKey: key}
}

func (node qdrantNode) call(t *testing.T, method, path string, body any) json.RawMessage {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s %s: %v", method, path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, "http://"+node.address+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	request.Header.Set("api-key", node.apiKey)
	request.Header.Set("Content-Type", "application/json")
	httpClient := http.Client{Timeout: time.Minute}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		t.Fatalf("%s %s: status %d: %s", method, path, response.StatusCode, payload)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("decode %s %s: %v", method, path, err)
	}
	return envelope.Result
}

func (node qdrantNode) collectionPayloadSchema(t *testing.T, name string) map[string]json.RawMessage {
	t.Helper()
	var info struct {
		PayloadSchema map[string]json.RawMessage `json:"payload_schema"`
	}
	if err := json.Unmarshal(node.call(t, http.MethodGet, "/collections/"+name, nil), &info); err != nil {
		t.Fatalf("decode collection info: %v", err)
	}
	return info.PayloadSchema
}

func (node qdrantNode) listCollections(t *testing.T) []string {
	t.Helper()
	var result struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
	}
	if err := json.Unmarshal(node.call(t, http.MethodGet, "/collections", nil), &result); err != nil {
		t.Fatalf("decode collections: %v", err)
	}
	names := []string{}
	for _, collection := range result.Collections {
		names = append(names, collection.Name)
	}
	return names
}

func (node qdrantNode) upsertPoints(t *testing.T, collection string, points []object) {
	t.Helper()
	node.call(t, http.MethodPut, "/collections/"+collection+"/points?wait=true", object{"points": points})
}

func (node qdrantNode) countPoints(t *testing.T, collection string) int {
	t.Helper()
	var result struct {
		Count int `json:"count"`
	}
	raw := node.call(t, http.MethodPost, "/collections/"+collection+"/points/count", object{"exact": false})
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return result.Count
}

func (node qdrantNode) collectionExists(t *testing.T, collection string) bool {
	t.Helper()
	var result struct {
		Exists bool `json:"exists"`
	}
	if err := json.Unmarshal(node.call(t, http.MethodGet, "/collections/"+collection+"/exists", nil), &result); err != nil {
		t.Fatalf("decode exists: %v", err)
	}
	return result.Exists
}

func joinKey(parts ...string) string {
	segments := []string{}
	for _, part := range parts {
		for _, segment := range strings.Split(part, "/") {
			if segment != "" {
				segments = append(segments, segment)
			}
		}
	}
	return strings.Join(segments, "/")
}

func releaseSecretCount(t *testing.T, release string) int {
	t.Helper()
	listing := &unstructured.UnstructuredList{}
	listing.SetAPIVersion("v1")
	listing.SetKind("SecretList")
	selector := client.MatchingLabels{"owner": "helm", "name": release}
	if err := kube.List(context.Background(), listing, client.InNamespace(namespace), selector); err != nil {
		t.Fatalf("list helm release secrets: %v", err)
	}
	return len(listing.Items)
}

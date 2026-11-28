package qdrant_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/adapters/qdrant"
	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
	"github.com/4thel00z/qdrant-operator/internal/settings"
)

const apiKey = "s3cret"

type seen struct {
	Method string
	Path   string
	Query  string
	APIKey string
	Type   string
	Body   string
}

type fixture struct {
	t      *testing.T
	server *httptest.Server
	client *qdrant.Client
	node   domain.QdrantNode
	mu     sync.Mutex
	calls  []seen
}

type reply struct {
	status int
	body   string
}

func newFixture(t *testing.T, replies ...reply) *fixture {
	t.Helper()
	f := &fixture{t: t}
	next := 0
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		f.mu.Lock()
		f.calls = append(f.calls, seen{Method: r.Method, Path: r.URL.EscapedPath(), Query: r.URL.RawQuery, APIKey: r.Header.Get("api-key"), Type: r.Header.Get("Content-Type"), Body: string(body)})
		answer := reply{status: http.StatusOK, body: `{"result":true,"status":"ok","time":0.001}`}
		if next < len(replies) {
			answer = replies[next]
		}
		next++
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answer.status)
		if _, err := io.WriteString(w, answer.body); err != nil {
			t.Errorf("write reply: %v", err)
		}
	}))
	t.Cleanup(f.server.Close)
	f.client = newClient(t, 5*time.Second, 5*time.Second)
	f.node = domain.QdrantNode{URL: f.server.URL, APIKey: apiKey}
	return f
}

func newClient(t *testing.T, timeout time.Duration, snapshotTimeout time.Duration) *qdrant.Client {
	t.Helper()
	client, err := qdrant.New(settings.Qdrant{Timeout: timeout, SnapshotTimeout: snapshotTimeout})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func ok(result string) reply {
	return reply{status: http.StatusOK, body: `{"result":` + result + `,"status":"ok","time":0.002}`}
}

func failure(status int, message string) reply {
	return reply{status: status, body: `{"status":{"error":"` + message + `"},"time":0.0}`}
}

func (f *fixture) only() seen {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		f.t.Fatalf("want 1 request, got %d: %+v", len(f.calls), f.calls)
	}
	return f.calls[0]
}

func (f *fixture) expect(index int, method string, path string, query string, body string) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if index >= len(f.calls) {
		f.t.Fatalf("request %d missing, got %d", index, len(f.calls))
	}
	got := f.calls[index]
	if got.Method != method || got.Path != path || got.Query != query {
		f.t.Fatalf("request %d: want %s %s?%s got %s %s?%s", index, method, path, query, got.Method, got.Path, got.Query)
	}
	if got.APIKey != apiKey {
		f.t.Fatalf("request %d: api-key %q", index, got.APIKey)
	}
	if got.Body != body {
		f.t.Fatalf("request %d: body want %s got %s", index, body, got.Body)
	}
	if body != "" && got.Type != "application/json" {
		f.t.Fatalf("request %d: content type %q", index, got.Type)
	}
}

func (f *fixture) expectOnly(method string, path string, query string, body string) {
	f.t.Helper()
	f.only()
	f.expect(0, method, path, query, body)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRejectsNonPositiveTimeouts(t *testing.T) {
	if _, err := qdrant.New(settings.Qdrant{Timeout: 0, SnapshotTimeout: time.Hour}); err == nil {
		t.Fatal("want an error for a zero timeout")
	}
	if _, err := qdrant.New(settings.Qdrant{Timeout: time.Second, SnapshotTimeout: -1}); err == nil {
		t.Fatal("want an error for a negative snapshot timeout")
	}
}

func TestListCollections(t *testing.T) {
	f := newFixture(t, ok(`{"collections":[{"name":"docs"},{"name":"images"}]}`))
	names, err := f.client.ListCollections(context.Background(), f.node)
	must(t, err)
	if !slices.Equal(names, []string{"docs", "images"}) {
		t.Fatalf("names %v", names)
	}
	f.expectOnly("GET", "/collections", "", "")
}

func TestBaseURLWithTrailingSlashAndNoAPIKey(t *testing.T) {
	f := newFixture(t, ok(`{"collections":[]}`))
	f.node = domain.QdrantNode{URL: f.server.URL + "/"}
	_, err := f.client.ListCollections(context.Background(), f.node)
	must(t, err)
	got := f.only()
	if got.Path != "/collections" || got.APIKey != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestCreateSnapshot(t *testing.T) {
	f := newFixture(t, ok(`{"name":"docs-123.snapshot","creation_time":"2022-08-04T10:49:10","size":4096,"checksum":"abc"}`))
	snapshot, err := f.client.CreateSnapshot(context.Background(), f.node, "docs")
	must(t, err)
	want := domain.Snapshot{Name: "docs-123.snapshot", Collection: "docs", SizeBytes: 4096, Checksum: "abc"}
	if snapshot != want {
		t.Fatalf("want %+v got %+v", want, snapshot)
	}
	f.expectOnly("POST", "/collections/docs/snapshots", "wait=true", "")
}

func TestCreateSnapshotWithoutChecksum(t *testing.T) {
	f := newFixture(t, ok(`{"name":"docs-1.snapshot","size":10}`))
	snapshot, err := f.client.CreateSnapshot(context.Background(), f.node, "docs")
	must(t, err)
	if snapshot.Checksum != "" || snapshot.SizeBytes != 10 {
		t.Fatalf("got %+v", snapshot)
	}
}

func TestStreamSnapshot(t *testing.T) {
	f := newFixture(t, reply{status: http.StatusOK, body: "snapshot-bytes"})
	stream, err := f.client.StreamSnapshot(context.Background(), f.node, "docs", "docs-1.snapshot")
	must(t, err)
	payload, err := io.ReadAll(stream)
	must(t, err)
	must(t, stream.Close())
	if string(payload) != "snapshot-bytes" {
		t.Fatalf("payload %q", payload)
	}
	f.expectOnly("GET", "/collections/docs/snapshots/docs-1.snapshot", "", "")
}

func TestStreamSnapshotMapsAnErrorReply(t *testing.T) {
	f := newFixture(t, failure(http.StatusNotFound, "Not found: snapshot docs-1.snapshot"))
	_, err := f.client.StreamSnapshot(context.Background(), f.node, "docs", "docs-1.snapshot")
	var reply *ports.QdrantError
	if !errors.As(err, &reply) || reply.Status != 404 || reply.Message != "Not found: snapshot docs-1.snapshot" {
		t.Fatalf("err %v", err)
	}
	if !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want ErrNotFound: %v", err)
	}
}

func TestListSnapshots(t *testing.T) {
	f := newFixture(t, ok(`[{"name":"a.snapshot","creation_time":"2022-08-04T10:49:10","size":10},{"name":"b.snapshot","creation_time":"2026-01-02T03:04:05.5Z","size":20},{"name":"c.snapshot","size":30}]`))
	snapshots, err := f.client.ListSnapshots(context.Background(), f.node, "docs")
	must(t, err)
	want := []domain.SnapshotInfo{
		{Name: "a.snapshot", CreatedAt: time.Date(2022, 8, 4, 10, 49, 10, 0, time.UTC), SizeBytes: 10},
		{Name: "b.snapshot", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 500000000, time.UTC), SizeBytes: 20},
		{Name: "c.snapshot", SizeBytes: 30},
	}
	if len(snapshots) != len(want) {
		t.Fatalf("got %+v", snapshots)
	}
	for index := range want {
		if snapshots[index].Name != want[index].Name || !snapshots[index].CreatedAt.Equal(want[index].CreatedAt) || snapshots[index].SizeBytes != want[index].SizeBytes {
			t.Fatalf("snapshot %d: want %+v got %+v", index, want[index], snapshots[index])
		}
	}
	f.expectOnly("GET", "/collections/docs/snapshots", "", "")
}

func TestListSnapshotsOfUnknownCollectionIsEmpty(t *testing.T) {
	f := newFixture(t, failure(http.StatusNotFound, "Not found: Collection `docs` doesn't exist!"))
	snapshots, err := f.client.ListSnapshots(context.Background(), f.node, "docs")
	must(t, err)
	if snapshots == nil || len(snapshots) != 0 {
		t.Fatalf("got %#v", snapshots)
	}
}

func TestListSnapshotsRejectsAnUnreadableTime(t *testing.T) {
	f := newFixture(t, ok(`[{"name":"a.snapshot","creation_time":"yesterday","size":1}]`))
	if _, err := f.client.ListSnapshots(context.Background(), f.node, "docs"); err == nil {
		t.Fatal("want an error")
	}
}

func TestDeleteSnapshot(t *testing.T) {
	f := newFixture(t, ok(`true`))
	must(t, f.client.DeleteSnapshot(context.Background(), f.node, "docs", "a.snapshot"))
	f.expectOnly("DELETE", "/collections/docs/snapshots/a.snapshot", "wait=true", "")
}

func TestDeleteSnapshotAlreadyGone(t *testing.T) {
	f := newFixture(t, failure(http.StatusNotFound, "Not found"))
	must(t, f.client.DeleteSnapshot(context.Background(), f.node, "docs", "a.snapshot"))
}

func TestDeleteSnapshotReportsOtherFailures(t *testing.T) {
	f := newFixture(t, failure(http.StatusInternalServerError, "disk failure"))
	err := f.client.DeleteSnapshot(context.Background(), f.node, "docs", "a.snapshot")
	var reply *ports.QdrantError
	if !errors.As(err, &reply) || reply.Status != 500 || reply.Message != "disk failure" {
		t.Fatalf("err %v", err)
	}
}

func TestRecoverSnapshot(t *testing.T) {
	f := newFixture(t, ok(`true`), ok(`true`))
	must(t, f.client.RecoverSnapshot(context.Background(), f.node, "docs", "http://minio/docs.snapshot", domain.PriorityNoSync, "abc"))
	must(t, f.client.RecoverSnapshot(context.Background(), f.node, "docs", "http://minio/docs.snapshot", domain.PrioritySnapshot, ""))
	f.expect(0, "PUT", "/collections/docs/snapshots/recover", "wait=true", `{"checksum":"abc","location":"http://minio/docs.snapshot","priority":"no_sync"}`)
	f.expect(1, "PUT", "/collections/docs/snapshots/recover", "wait=true", `{"location":"http://minio/docs.snapshot","priority":"snapshot"}`)
}

func TestCollectionInfoKeepsLargeNumbers(t *testing.T) {
	f := newFixture(t, ok(`{"status":"green","points_count":9007199254740993,"config":{"params":{"replication_factor":2}}}`))
	info, err := f.client.CollectionInfo(context.Background(), f.node, "docs")
	must(t, err)
	if info["status"] != "green" {
		t.Fatalf("status %v", info["status"])
	}
	if count, isNumber := info["points_count"].(json.Number); !isNumber || count.String() != "9007199254740993" {
		t.Fatalf("points_count %#v", info["points_count"])
	}
	params, isObject := info["config"].(domain.JSON)["params"].(domain.JSON)
	if !isObject || params["replication_factor"] == nil {
		t.Fatalf("config %#v", info["config"])
	}
	f.expectOnly("GET", "/collections/docs", "", "")
}

func TestReady(t *testing.T) {
	f := newFixture(t, reply{status: http.StatusOK, body: "all shards are ready"}, reply{status: http.StatusServiceUnavailable, body: "some shards are not ready"})
	if !f.client.Ready(context.Background(), f.node) {
		t.Fatal("want ready")
	}
	if f.client.Ready(context.Background(), f.node) {
		t.Fatal("want not ready on 503")
	}
	f.expect(0, "GET", "/readyz", "", "")
	f.server.Close()
	if f.client.Ready(context.Background(), f.node) {
		t.Fatal("want not ready when unreachable")
	}
}

func TestCollectionExists(t *testing.T) {
	f := newFixture(t, ok(`{"exists":true}`), ok(`{"exists":false}`))
	exists, err := f.client.CollectionExists(context.Background(), f.node, "docs")
	must(t, err)
	missing, err := f.client.CollectionExists(context.Background(), f.node, "other")
	must(t, err)
	if !exists || missing {
		t.Fatalf("exists %v missing %v", exists, missing)
	}
	f.expect(0, "GET", "/collections/docs/exists", "", "")
}

func TestCreateAndUpdateCollection(t *testing.T) {
	f := newFixture(t)
	body := domain.JSON{"vectors": domain.JSON{"size": 4, "distance": "Cosine"}}
	must(t, f.client.CreateCollection(context.Background(), f.node, "docs", body))
	must(t, f.client.UpdateCollection(context.Background(), f.node, "docs", domain.JSON{"hnsw_config": domain.JSON{"m": 32}}))
	f.expect(0, "PUT", "/collections/docs", "", `{"vectors":{"distance":"Cosine","size":4}}`)
	f.expect(1, "PATCH", "/collections/docs", "", `{"hnsw_config":{"m":32}}`)
}

func TestCreateCollectionReportsTheQdrantError(t *testing.T) {
	f := newFixture(t, failure(http.StatusConflict, "Collection `docs` already exists!"))
	err := f.client.CreateCollection(context.Background(), f.node, "docs", domain.JSON{})
	var reply *ports.QdrantError
	if !errors.As(err, &reply) || reply.Status != 409 || reply.Message != "Collection `docs` already exists!" {
		t.Fatalf("err %v", err)
	}
	if errors.Is(err, ports.ErrNotFound) || errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("a 409 is neither: %v", err)
	}
}

func TestErrorMessageFallsBackToTheBody(t *testing.T) {
	f := newFixture(t, reply{status: http.StatusBadGateway, body: "upstream gone\n"})
	err := f.client.CreateCollection(context.Background(), f.node, "docs", domain.JSON{})
	var reply *ports.QdrantError
	if !errors.As(err, &reply) || reply.Message != "upstream gone" {
		t.Fatalf("err %v", err)
	}
}

func TestDeleteCollection(t *testing.T) {
	f := newFixture(t, ok(`true`), failure(http.StatusNotFound, "Not found"), failure(http.StatusInternalServerError, "boom"))
	must(t, f.client.DeleteCollection(context.Background(), f.node, "docs"))
	must(t, f.client.DeleteCollection(context.Background(), f.node, "docs"))
	if err := f.client.DeleteCollection(context.Background(), f.node, "docs"); err == nil {
		t.Fatal("want an error for a 500")
	}
	f.expect(0, "DELETE", "/collections/docs", "", "")
}

func TestListAndUpdateAliases(t *testing.T) {
	f := newFixture(t, ok(`{"aliases":[{"alias_name":"live","collection_name":"docs-v2"},{"alias_name":"old","collection_name":"docs-v1"}]}`), ok(`true`))
	aliases, err := f.client.ListAliases(context.Background(), f.node)
	must(t, err)
	if len(aliases) != 2 || aliases["live"] != "docs-v2" || aliases["old"] != "docs-v1" {
		t.Fatalf("aliases %v", aliases)
	}
	actions := []domain.JSON{
		{"delete_alias": domain.JSON{"alias_name": "live"}},
		{"create_alias": domain.JSON{"alias_name": "live", "collection_name": "docs-v3"}},
	}
	must(t, f.client.UpdateAliases(context.Background(), f.node, actions))
	f.expect(0, "GET", "/aliases", "", "")
	f.expect(1, "POST", "/collections/aliases", "", `{"actions":[{"delete_alias":{"alias_name":"live"}},{"create_alias":{"alias_name":"live","collection_name":"docs-v3"}}]}`)
}

func TestPayloadIndexes(t *testing.T) {
	f := newFixture(t)
	must(t, f.client.CreatePayloadIndex(context.Background(), f.node, "docs", "tag", "keyword"))
	must(t, f.client.CreatePayloadIndex(context.Background(), f.node, "docs", "body", domain.JSON{"type": "text", "tokenizer": "word"}))
	must(t, f.client.DeletePayloadIndex(context.Background(), f.node, "docs", "tag"))
	f.expect(0, "PUT", "/collections/docs/index", "wait=true", `{"field_name":"tag","field_schema":"keyword"}`)
	f.expect(1, "PUT", "/collections/docs/index", "wait=true", `{"field_name":"body","field_schema":{"tokenizer":"word","type":"text"}}`)
	f.expect(2, "DELETE", "/collections/docs/index/tag", "wait=true", "")
}

func TestShardKeys(t *testing.T) {
	f := newFixture(t, ok(`{"shard_keys":[{"key":"eu"},{"key":18446744073709551615}]}`), ok(`{"shard_keys":null}`), ok(`true`))
	keys, err := f.client.ListShardKeys(context.Background(), f.node, "docs")
	must(t, err)
	if len(keys) != 2 || keys[0] != "eu" {
		t.Fatalf("keys %#v", keys)
	}
	if number, isNumber := keys[1].(json.Number); !isNumber || number.String() != "18446744073709551615" {
		t.Fatalf("key %#v", keys[1])
	}
	none, err := f.client.ListShardKeys(context.Background(), f.node, "docs")
	must(t, err)
	if none == nil || len(none) != 0 {
		t.Fatalf("none %#v", none)
	}
	must(t, f.client.CreateShardKey(context.Background(), f.node, "docs", domain.JSON{"shard_key": "us", "shards_number": 2}))
	f.expect(0, "GET", "/collections/docs/shards", "", "")
	f.expect(2, "PUT", "/collections/docs/shards", "", `{"shard_key":"us","shards_number":2}`)
}

func TestCountPoints(t *testing.T) {
	f := newFixture(t, ok(`{"count":42}`), ok(`{"count":7}`))
	approximate, err := f.client.CountPoints(context.Background(), f.node, "docs", false)
	must(t, err)
	exact, err := f.client.CountPoints(context.Background(), f.node, "docs", true)
	must(t, err)
	if approximate != 42 || exact != 7 {
		t.Fatalf("counts %d %d", approximate, exact)
	}
	f.expect(0, "POST", "/collections/docs/points/count", "", `{"exact":false}`)
	f.expect(1, "POST", "/collections/docs/points/count", "", `{"exact":true}`)
}

func TestScrollPointsKeepsLargeIDsAndPaginates(t *testing.T) {
	page := `{"points":[` +
		`{"id":18446744073709551615,"payload":{"n":9007199254740993,"f":1.5},"vector":[0.25,0.5]},` +
		`{"id":9007199254740993,"payload":{},"vector":{"dense":[1.0],"sparse":{"indices":[1],"values":[2.0]}},"shard_key":"eu"},` +
		`{"id":"550e8400-e29b-41d4-a716-446655440000","payload":null}` +
		`],"next_page_offset":9007199254740995}`
	f := newFixture(t, ok(page), ok(`{"points":[],"next_page_offset":null}`))
	points, next, err := f.client.ScrollPoints(context.Background(), f.node, "docs", domain.PointID{}, 3)
	must(t, err)
	if len(points) != 3 {
		t.Fatalf("points %+v", points)
	}
	if points[0].ID.String() != "18446744073709551615" || !points[0].ID.IsNumber() {
		t.Fatalf("id %v", points[0].ID)
	}
	if points[1].ID != domain.NumberPointID(9007199254740993) {
		t.Fatalf("id %v", points[1].ID)
	}
	if points[2].ID != domain.TextPointID("550e8400-e29b-41d4-a716-446655440000") || points[2].Payload != nil {
		t.Fatalf("point %+v", points[2])
	}
	if string(points[0].Payload) != `{"n":9007199254740993,"f":1.5}` || string(points[0].Vector) != `[0.25,0.5]` {
		t.Fatalf("raw bodies %s %s", points[0].Payload, points[0].Vector)
	}
	if string(points[1].ShardKey) != `"eu"` {
		t.Fatalf("shard key %s", points[1].ShardKey)
	}
	if next != domain.NumberPointID(9007199254740995) {
		t.Fatalf("next %v", next)
	}
	_, last, err := f.client.ScrollPoints(context.Background(), f.node, "docs", next, 3)
	must(t, err)
	if !last.IsZero() {
		t.Fatalf("last %v", last)
	}
	f.expect(0, "POST", "/collections/docs/points/scroll", "", `{"limit":3,"with_payload":true,"with_vector":true}`)
	f.expect(1, "POST", "/collections/docs/points/scroll", "", `{"limit":3,"offset":9007199254740995,"with_payload":true,"with_vector":true}`)
}

func TestScrollPointsSendsAUUIDOffset(t *testing.T) {
	f := newFixture(t, ok(`{"points":[],"next_page_offset":"550e8400-e29b-41d4-a716-446655440000"}`))
	_, next, err := f.client.ScrollPoints(context.Background(), f.node, "docs", domain.TextPointID("11111111-1111-1111-1111-111111111111"), 10)
	must(t, err)
	if next != domain.TextPointID("550e8400-e29b-41d4-a716-446655440000") {
		t.Fatalf("next %v", next)
	}
	f.expectOnly("POST", "/collections/docs/points/scroll", "", `{"limit":10,"offset":"11111111-1111-1111-1111-111111111111","with_payload":true,"with_vector":true}`)
}

func TestUpsertPointsKeepsLargeIDsUnchanged(t *testing.T) {
	f := newFixture(t, ok(`{"operation_id":1,"status":"completed"}`), ok(`{"operation_id":2,"status":"completed"}`), ok(`{"operation_id":3,"status":"completed"}`))
	points := []domain.Point{
		{ID: domain.NumberPointID(18446744073709551615), Vector: json.RawMessage(`[0.25,0.5]`), Payload: json.RawMessage(`{"n":9007199254740993}`)},
		{ID: domain.TextPointID("550e8400-e29b-41d4-a716-446655440000"), Vector: json.RawMessage(`{"dense":[1.0]}`)},
	}
	must(t, f.client.UpsertPoints(context.Background(), f.node, "docs", points, domain.PointShardKey{}))
	must(t, f.client.UpsertPoints(context.Background(), f.node, "docs", points[:1], domain.TextShardKey("eu")))
	must(t, f.client.UpsertPoints(context.Background(), f.node, "docs", points[:1], domain.NumberShardKey(18446744073709551615)))
	first := `{"id":18446744073709551615,"vector":[0.25,0.5],"payload":{"n":9007199254740993}}`
	f.expect(0, "PUT", "/collections/docs/points", "wait=true", `{"points":[`+first+`,{"id":"550e8400-e29b-41d4-a716-446655440000","vector":{"dense":[1.0]}}]}`)
	f.expect(1, "PUT", "/collections/docs/points", "wait=true", `{"points":[`+first+`],"shard_key":"eu"}`)
	f.expect(2, "PUT", "/collections/docs/points", "wait=true", `{"points":[`+first+`],"shard_key":18446744073709551615}`)
}

func TestDeletePointsKeepsLargeIDsUnchanged(t *testing.T) {
	f := newFixture(t)
	ids := []domain.PointID{domain.NumberPointID(18446744073709551615), domain.NumberPointID(3), domain.TextPointID("550e8400-e29b-41d4-a716-446655440000")}
	must(t, f.client.DeletePoints(context.Background(), f.node, "docs", ids))
	f.expectOnly("POST", "/collections/docs/points/delete", "wait=true", `{"points":[18446744073709551615,3,"550e8400-e29b-41d4-a716-446655440000"]}`)
}

func TestClusterTopology(t *testing.T) {
	f := newFixture(t, ok(`{"status":"enabled","peer_id":663769463945476,"peers":{"18446744073709551615":{"uri":"http://qdrant-1.qdrant-headless:6335/"},"663769463945476":{"uri":"http://qdrant-0.qdrant-headless:6335/"}},"raft_info":{"term":1}}`),
		ok(`{"status":"disabled"}`))
	topology, err := f.client.ClusterTopology(context.Background(), f.node)
	must(t, err)
	want := domain.ClusterTopology{PeerID: 663769463945476, Peers: []domain.Peer{
		{PeerID: 663769463945476, URI: "http://qdrant-0.qdrant-headless:6335/"},
		{PeerID: 18446744073709551615, URI: "http://qdrant-1.qdrant-headless:6335/"},
	}}
	if topology.PeerID != want.PeerID || !slices.Equal(topology.Peers, want.Peers) {
		t.Fatalf("want %+v got %+v", want, topology)
	}
	single, err := f.client.ClusterTopology(context.Background(), f.node)
	must(t, err)
	if single.PeerID != 0 || len(single.Peers) != 0 {
		t.Fatalf("single %+v", single)
	}
	f.expect(0, "GET", "/cluster", "", "")
}

func TestCollectionShards(t *testing.T) {
	cluster := `{"peer_id":10,"shard_count":3,"local_shards":[{"shard_id":0,"points_count":5,"state":"Active"}],` +
		`"remote_shards":[{"shard_id":1,"peer_id":18446744073709551615,"state":"Active"},{"shard_id":0,"peer_id":12,"state":"Dead"}],` +
		`"shard_transfers":[{"shard_id":2,"from":10,"to":12,"sync":false}],"resharding_operations":[{"direction":"up"}]}`
	f := newFixture(t, ok(cluster), ok(`{"status":"green","config":{"params":{"replication_factor":2}}}`),
		ok(`{"peer_id":10,"shard_count":0}`), ok(`{"config":{"params":{"replication_factor":null}}}`))
	shards, err := f.client.CollectionShards(context.Background(), f.node, "docs")
	must(t, err)
	want := domain.CollectionShards{Collection: "docs", ReplicationFactor: 2, Transfers: 2, Replicas: []domain.Replica{
		{ShardID: 0, PeerID: 10, State: "Active"},
		{ShardID: 1, PeerID: 18446744073709551615, State: "Active"},
		{ShardID: 0, PeerID: 12, State: "Dead"},
	}}
	if shards.Collection != want.Collection || shards.ReplicationFactor != 2 || shards.Transfers != 2 || !slices.Equal(shards.Replicas, want.Replicas) {
		t.Fatalf("want %+v got %+v", want, shards)
	}
	empty, err := f.client.CollectionShards(context.Background(), f.node, "docs")
	must(t, err)
	if len(empty.Replicas) != 0 || empty.ReplicationFactor != 1 || empty.Transfers != 0 {
		t.Fatalf("empty %+v", empty)
	}
	f.expect(0, "GET", "/collections/docs/cluster", "", "")
	f.expect(1, "GET", "/collections/docs", "", "")
}

func TestApplyShardOperation(t *testing.T) {
	f := newFixture(t)
	target := uint64(18446744073709551615)
	operations := []domain.ShardOperation{
		{Kind: domain.ShardMove, Collection: "docs", ShardID: 3, PeerID: 10, TargetPeerID: &target},
		{Kind: domain.ShardReplicate, Collection: "docs", ShardID: 3, PeerID: 10, TargetPeerID: &target},
		{Kind: domain.ShardDrop, Collection: "docs", ShardID: 3, PeerID: 10},
	}
	for _, operation := range operations {
		must(t, f.client.ApplyShardOperation(context.Background(), f.node, operation))
	}
	f.expect(0, "POST", "/collections/docs/cluster", "wait=true", `{"move_shard":{"from_peer_id":10,"shard_id":3,"to_peer_id":18446744073709551615}}`)
	f.expect(1, "POST", "/collections/docs/cluster", "wait=true", `{"replicate_shard":{"from_peer_id":10,"shard_id":3,"to_peer_id":18446744073709551615}}`)
	f.expect(2, "POST", "/collections/docs/cluster", "wait=true", `{"drop_replica":{"peer_id":10,"shard_id":3}}`)
}

func TestRemovePeer(t *testing.T) {
	f := newFixture(t, ok(`true`), failure(http.StatusBadRequest, "Cannot remove peer 11 as there are shards on it"))
	must(t, f.client.RemovePeer(context.Background(), f.node, 663769463945476, true))
	err := f.client.RemovePeer(context.Background(), f.node, 11, false)
	var reply *ports.QdrantError
	if !errors.As(err, &reply) || reply.Status != 400 {
		t.Fatalf("err %v", err)
	}
	f.expect(0, "DELETE", "/cluster/peer/663769463945476", "force=true", "")
	f.expect(1, "DELETE", "/cluster/peer/11", "force=false", "")
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	f := newFixture(t, ok(`{"exists":true}`))
	_, err := f.client.CollectionExists(context.Background(), f.node, "a/b c")
	must(t, err)
	f.expectOnly("GET", "/collections/a%2Fb%20c/exists", "", "")
}

func TestUnreachableNode(t *testing.T) {
	f := newFixture(t)
	f.server.Close()
	_, err := f.client.ListCollections(context.Background(), f.node)
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("err %v", err)
	}
}

func TestCancelledContextIsNotUnreachable(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.client.ListCollections(ctx, f.node)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("err %v", err)
	}
}

func TestSnapshotTimeoutOutlastsTheRequestTimeout(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(300 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"result":{"name":"a.snapshot","size":1}}`); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer slow.Close()
	client := newClient(t, 50*time.Millisecond, 5*time.Second)
	node := domain.QdrantNode{URL: slow.URL}
	if _, err := client.ListCollections(context.Background(), node); !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("a plain call past its timeout: %v", err)
	}
	snapshot, err := client.CreateSnapshot(context.Background(), node, "docs")
	must(t, err)
	if snapshot.Name != "a.snapshot" {
		t.Fatalf("snapshot %+v", snapshot)
	}
}

func TestTLSNodeIsVerifiedAgainstTheClusterCA(t *testing.T) {
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if _, err := io.WriteString(w, `{"result":{"collections":[{"name":"docs"}]}}`); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer secure.Close()
	client := newClient(t, 5*time.Second, 5*time.Second)
	authority := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secure.Certificate().Raw}))

	trusted, err := client.ListCollections(context.Background(), domain.QdrantNode{URL: secure.URL, CACert: authority})
	must(t, err)
	if !slices.Equal(trusted, []string{"docs"}) {
		t.Fatalf("names %v", trusted)
	}
	if _, err := client.ListCollections(context.Background(), domain.QdrantNode{URL: secure.URL}); !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("want an untrusted certificate to fail: %v", err)
	}
	if _, err := client.ListCollections(context.Background(), domain.QdrantNode{URL: secure.URL, CACert: "not a certificate"}); err == nil {
		t.Fatal("want an error for a CA that holds no certificate")
	}
}

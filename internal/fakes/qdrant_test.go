package fakes_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/fakes"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

var node = domain.QdrantNode{URL: "http://qdrant:6333"}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func point(id domain.PointID) domain.Point {
	return domain.Point{ID: id, Vector: json.RawMessage(`[1.0]`)}
}

func TestCollectionLifecycleAndConfig(t *testing.T) {
	ctx := context.Background()
	q := fakes.NewQdrant()
	must(t, q.CreateCollection(ctx, node, "docs", domain.JSON{"vectors": domain.JSON{"size": 4, "distance": "Cosine"}, "replication_factor": 2}))
	if err := q.CreateCollection(ctx, node, "docs", domain.JSON{}); err == nil {
		t.Fatal("want a conflict")
	}
	exists, err := q.CollectionExists(ctx, node, "docs")
	must(t, err)
	if !exists {
		t.Fatal("want docs to exist")
	}
	must(t, q.UpdateCollection(ctx, node, "docs", domain.JSON{"hnsw_config": domain.JSON{"m": 32}, "optimizers_config": domain.JSON{"max_optimization_threads": "auto"}}))
	info, err := q.CollectionInfo(ctx, node, "docs")
	must(t, err)
	config := info["config"].(domain.JSON)
	if config["hnsw_config"].(domain.JSON)["m"] != 32 || config["optimizer_config"].(domain.JSON)["max_optimization_threads"] != nil {
		t.Fatalf("config %v", config)
	}
	if config["params"].(domain.JSON)["replication_factor"] != 2 {
		t.Fatalf("params %v", config["params"])
	}
	must(t, q.UpdateAliases(ctx, node, []domain.JSON{{"create_alias": domain.JSON{"alias_name": "live", "collection_name": "docs"}}}))
	must(t, q.CreatePayloadIndex(ctx, node, "docs", "tag", "keyword"))
	must(t, q.DeleteCollection(ctx, node, "docs"))
	aliases, err := q.ListAliases(ctx, node)
	must(t, err)
	if len(aliases) != 0 {
		t.Fatalf("aliases %v", aliases)
	}
	if _, err := q.CollectionInfo(ctx, node, "docs"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
}

func TestScrollPagesInScrollOrderAndAppliesLateWaves(t *testing.T) {
	ctx := context.Background()
	q := fakes.NewQdrant()
	q.AddCollection(node.URL, "docs", 0, nil)
	big := domain.NumberPointID(18446744073709551615)
	q.AddPoints(node.URL, "docs", []domain.Point{point(domain.TextPointID("b")), point(big), point(domain.NumberPointID(2))})
	key := fakes.PointsKey{NodeURL: node.URL, Collection: "docs"}
	q.LatePoints[key] = [][]domain.Point{{point(domain.NumberPointID(1))}}

	page, next, err := q.ScrollPoints(ctx, node, "docs", domain.PointID{}, 2)
	must(t, err)
	if len(page) != 2 || page[0].ID != domain.NumberPointID(2) || page[1].ID != big || next != domain.TextPointID("b") {
		t.Fatalf("page %+v next %v", page, next)
	}
	page, next, err = q.ScrollPoints(ctx, node, "docs", next, 2)
	must(t, err)
	if len(page) != 1 || !next.IsZero() {
		t.Fatalf("last page %+v next %v", page, next)
	}
	if count, err := q.CountPoints(ctx, node, "docs", true); err != nil || count != 4 {
		t.Fatalf("count %d %v", count, err)
	}
	must(t, q.DeletePoints(ctx, node, "docs", []domain.PointID{big}))
	if stored := q.StoredPoints(node.URL, "docs"); len(stored) != 3 {
		t.Fatalf("stored %+v", stored)
	}
}

func TestUpsertTagsShardKeyAndGoesUnreachable(t *testing.T) {
	ctx := context.Background()
	q := fakes.NewQdrant()
	q.AddCollection(node.URL, "docs", 0, nil)
	limit := 1
	q.UnreachableAfterUpserts = &limit
	must(t, q.UpsertPoints(ctx, node, "docs", []domain.Point{point(domain.NumberPointID(1))}, domain.TextShardKey("eu")))
	stored := q.StoredPoints(node.URL, "docs")
	if len(stored) != 1 || string(stored[0].ShardKey) != `"eu"` {
		t.Fatalf("stored %+v", stored)
	}
	err := q.UpsertPoints(ctx, node, "docs", nil, domain.PointShardKey{})
	if !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("err %v", err)
	}
}

func TestSnapshotsStreamAndDelete(t *testing.T) {
	ctx := context.Background()
	q := fakes.NewQdrant()
	q.AddCollection(node.URL, "docs", 0, nil)
	q.AddSnapshot(node.URL, "docs", "old.snapshot", time.Unix(0, 0).UTC(), nil)
	created, err := q.CreateSnapshot(ctx, node, "docs")
	must(t, err)
	listed, err := q.ListSnapshots(ctx, node, "docs")
	must(t, err)
	if len(listed) != 2 || listed[0].Name != created.Name || listed[1].Name != "old.snapshot" {
		t.Fatalf("listed %+v created %+v", listed, created)
	}
	stream, err := q.StreamSnapshot(ctx, node, "docs", created.Name)
	must(t, err)
	payload, err := io.ReadAll(stream)
	must(t, err)
	if int64(len(payload)) != created.SizeBytes {
		t.Fatalf("payload %d size %d", len(payload), created.SizeBytes)
	}
	must(t, q.DeleteSnapshot(ctx, node, "docs", "old.snapshot"))
	must(t, q.DeleteSnapshot(ctx, node, "docs", "old.snapshot"))
	q.FailingCollections["bad"] = true
	if _, err := q.CreateSnapshot(ctx, node, "bad"); err == nil {
		t.Fatal("want a refused snapshot")
	}
	must(t, q.RecoverSnapshot(ctx, node, "restored", "http://s3/x", domain.PriorityReplica, ""))
	if len(q.Recovered) != 1 {
		t.Fatalf("recovered %+v", q.Recovered)
	}
	if exists, err := q.CollectionExists(ctx, node, "restored"); err != nil || !exists {
		t.Fatalf("restored exists %v %v", exists, err)
	}
}

func TestShardOperationsAndPeerRemoval(t *testing.T) {
	ctx := context.Background()
	q := fakes.NewQdrant()
	q.NodePeerIDs[node.URL] = 10
	q.Peers[10] = "http://qdrant-0:6335/"
	q.Peers[11] = "http://qdrant-1:6335/"
	q.Replicas["docs"] = []domain.Replica{{ShardID: 0, PeerID: 10, State: "Active"}, {ShardID: 1, PeerID: 11, State: "Active"}}
	topology, err := q.ClusterTopology(ctx, node)
	must(t, err)
	if topology.PeerID != 10 || len(topology.Peers) != 2 || topology.Peers[0].PeerID != 10 {
		t.Fatalf("topology %+v", topology)
	}
	if _, err := q.ClusterTopology(ctx, domain.QdrantNode{URL: "http://down:6333"}); !errors.Is(err, ports.ErrUnreachable) {
		t.Fatalf("err %v", err)
	}

	target := uint64(10)
	move := domain.ShardOperation{Kind: domain.ShardMove, Collection: "docs", ShardID: 1, PeerID: 11, TargetPeerID: &target}
	q.SlowTransfers = true
	must(t, q.ApplyShardOperation(ctx, node, move))
	shards, err := q.CollectionShards(ctx, node, "docs")
	must(t, err)
	if shards.Transfers != 1 || shards.ReplicationFactor != 1 || len(shards.Replicas) != 2 {
		t.Fatalf("pending %+v", shards)
	}
	must(t, q.Settle())
	shards, err = q.CollectionShards(ctx, node, "docs")
	must(t, err)
	if shards.Transfers != 0 || !slices.Equal(shards.Replicas, []domain.Replica{{ShardID: 0, PeerID: 10, State: "Active"}, {ShardID: 1, PeerID: 10, State: "Active"}}) {
		t.Fatalf("settled %+v", shards)
	}

	q.Replicas["docs"] = append(q.Replicas["docs"], domain.Replica{ShardID: 2, PeerID: 11, State: "Active"})
	if err := q.RemovePeer(ctx, node, 11, false); err == nil {
		t.Fatal("want a refusal while the peer holds shards")
	}
	must(t, q.RemovePeer(ctx, node, 11, true))
	if len(q.Replicas["docs"]) != 2 || len(q.Peers) != 1 || len(q.RemovedPeers) != 1 {
		t.Fatalf("after removal %+v %v %+v", q.Replicas, q.Peers, q.RemovedPeers)
	}
	missing := domain.ShardOperation{Kind: domain.ShardDrop, Collection: "docs", ShardID: 9, PeerID: 10}
	if err := q.ApplyShardOperation(ctx, node, missing); err == nil {
		t.Fatal("want an error for a replica that does not exist")
	}
}

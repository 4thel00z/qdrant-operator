package qdrant

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func (c *Client) ClusterTopology(ctx context.Context, node domain.QdrantNode) (domain.ClusterTopology, error) {
	result, err := call[struct {
		PeerID uint64 `json:"peer_id"`
		Peers  map[string]struct {
			URI string `json:"uri"`
		} `json:"peers"`
	}](ctx, c.plain(node, http.MethodGet, route("cluster")))
	if err != nil {
		return domain.ClusterTopology{}, err
	}
	peers := make([]domain.Peer, 0, len(result.Peers))
	for key, info := range result.Peers {
		peerID, err := strconv.ParseUint(key, 10, 64)
		if err != nil {
			return domain.ClusterTopology{}, fmt.Errorf("qdrant at %s reports peer id %q: %w", node.URL, key, err)
		}
		peers = append(peers, domain.Peer{PeerID: peerID, URI: info.URI})
	}
	slices.SortFunc(peers, func(a domain.Peer, b domain.Peer) int {
		switch {
		case a.PeerID < b.PeerID:
			return -1
		case a.PeerID > b.PeerID:
			return 1
		}
		return 0
	})
	return domain.ClusterTopology{PeerID: result.PeerID, Peers: peers}, nil
}

func (c *Client) CollectionShards(ctx context.Context, node domain.QdrantNode, collection string) (domain.CollectionShards, error) {
	cluster, err := call[struct {
		PeerID      uint64 `json:"peer_id"`
		LocalShards []struct {
			ShardID int    `json:"shard_id"`
			State   string `json:"state"`
		} `json:"local_shards"`
		RemoteShards []struct {
			ShardID int    `json:"shard_id"`
			PeerID  uint64 `json:"peer_id"`
			State   string `json:"state"`
		} `json:"remote_shards"`
		ShardTransfers       []struct{} `json:"shard_transfers"`
		ReshardingOperations []struct{} `json:"resharding_operations"`
	}](ctx, c.plain(node, http.MethodGet, route("collections", collection, "cluster")))
	if err != nil {
		return domain.CollectionShards{}, err
	}
	info, err := call[struct {
		Config struct {
			Params struct {
				ReplicationFactor *int `json:"replication_factor"`
			} `json:"params"`
		} `json:"config"`
	}](ctx, c.plain(node, http.MethodGet, route("collections", collection)))
	if err != nil {
		return domain.CollectionShards{}, err
	}
	replicas := make([]domain.Replica, 0, len(cluster.LocalShards)+len(cluster.RemoteShards))
	for _, shard := range cluster.LocalShards {
		replicas = append(replicas, domain.Replica{ShardID: shard.ShardID, PeerID: cluster.PeerID, State: shard.State})
	}
	for _, shard := range cluster.RemoteShards {
		replicas = append(replicas, domain.Replica{ShardID: shard.ShardID, PeerID: shard.PeerID, State: shard.State})
	}
	replicationFactor := 1
	if info.Config.Params.ReplicationFactor != nil {
		replicationFactor = *info.Config.Params.ReplicationFactor
	}
	return domain.CollectionShards{
		Collection:        collection,
		Replicas:          replicas,
		ReplicationFactor: replicationFactor,
		Transfers:         len(cluster.ShardTransfers) + len(cluster.ReshardingOperations),
	}, nil
}

func (c *Client) ApplyShardOperation(ctx context.Context, node domain.QdrantNode, operation domain.ShardOperation) error {
	r := c.plain(node, http.MethodPost, route("collections", operation.Collection, "cluster"))
	r.query = waiting()
	r.body = operation.Body()
	return c.execute(ctx, r)
}

func (c *Client) RemovePeer(ctx context.Context, node domain.QdrantNode, peerID uint64, force bool) error {
	r := c.plain(node, http.MethodDelete, route("cluster", "peer", strconv.FormatUint(peerID, 10)))
	r.query = map[string][]string{"force": {strconv.FormatBool(force)}}
	return c.execute(ctx, r)
}

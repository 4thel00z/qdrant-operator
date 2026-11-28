package qdrant

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

func (c *Client) ListCollections(ctx context.Context, node domain.QdrantNode) ([]string, error) {
	result, err := call[struct {
		Collections []struct {
			Name string `json:"name"`
		} `json:"collections"`
	}](ctx, c.plain(node, http.MethodGet, route("collections")))
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(result.Collections))
	for _, collection := range result.Collections {
		names = append(names, collection.Name)
	}
	return names, nil
}

func (c *Client) CollectionInfo(ctx context.Context, node domain.QdrantNode, collection string) (domain.JSON, error) {
	return call[domain.JSON](ctx, c.plain(node, http.MethodGet, route("collections", collection)))
}

func (c *Client) CollectionExists(ctx context.Context, node domain.QdrantNode, collection string) (bool, error) {
	result, err := call[struct {
		Exists bool `json:"exists"`
	}](ctx, c.plain(node, http.MethodGet, route("collections", collection, "exists")))
	return result.Exists, err
}

func (c *Client) CreateCollection(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	r := c.slow(node, http.MethodPut, route("collections", collection))
	r.body = body
	return c.execute(ctx, r)
}

func (c *Client) UpdateCollection(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	r := c.slow(node, http.MethodPatch, route("collections", collection))
	r.body = body
	return c.execute(ctx, r)
}

func (c *Client) DeleteCollection(ctx context.Context, node domain.QdrantNode, collection string) error {
	err := c.execute(ctx, c.slow(node, http.MethodDelete, route("collections", collection)))
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) ListAliases(ctx context.Context, node domain.QdrantNode) (map[string]string, error) {
	result, err := call[struct {
		Aliases []struct {
			AliasName      string `json:"alias_name"`
			CollectionName string `json:"collection_name"`
		} `json:"aliases"`
	}](ctx, c.plain(node, http.MethodGet, route("aliases")))
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]string, len(result.Aliases))
	for _, alias := range result.Aliases {
		aliases[alias.AliasName] = alias.CollectionName
	}
	return aliases, nil
}

func (c *Client) UpdateAliases(ctx context.Context, node domain.QdrantNode, actions []domain.JSON) error {
	r := c.plain(node, http.MethodPost, route("collections", "aliases"))
	r.body = domain.JSON{"actions": actions}
	return c.execute(ctx, r)
}

func (c *Client) CreatePayloadIndex(ctx context.Context, node domain.QdrantNode, collection string, fieldName string, fieldSchema any) error {
	r := c.slow(node, http.MethodPut, route("collections", collection, "index"))
	r.query = waiting()
	r.body = domain.JSON{"field_name": fieldName, "field_schema": fieldSchema}
	return c.execute(ctx, r)
}

func (c *Client) DeletePayloadIndex(ctx context.Context, node domain.QdrantNode, collection string, fieldName string) error {
	r := c.slow(node, http.MethodDelete, route("collections", collection, "index", fieldName))
	r.query = waiting()
	return c.execute(ctx, r)
}

func (c *Client) ListShardKeys(ctx context.Context, node domain.QdrantNode, collection string) ([]any, error) {
	result, err := call[struct {
		ShardKeys []struct {
			Key any `json:"key"`
		} `json:"shard_keys"`
	}](ctx, c.plain(node, http.MethodGet, route("collections", collection, "shards")))
	if err != nil {
		return nil, err
	}
	keys := make([]any, 0, len(result.ShardKeys))
	for _, entry := range result.ShardKeys {
		keys = append(keys, entry.Key)
	}
	return keys, nil
}

func (c *Client) CreateShardKey(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error {
	r := c.slow(node, http.MethodPut, route("collections", collection, "shards"))
	r.body = body
	return c.execute(ctx, r)
}

func (c *Client) CountPoints(ctx context.Context, node domain.QdrantNode, collection string, exact bool) (int64, error) {
	r := c.plain(node, http.MethodPost, route("collections", collection, "points", "count"))
	r.body = domain.JSON{"exact": exact}
	result, err := call[struct {
		Count int64 `json:"count"`
	}](ctx, r)
	if err != nil {
		return 0, fmt.Errorf("count points of %s: %w", collection, err)
	}
	return result.Count, nil
}

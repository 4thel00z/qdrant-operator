package qdrant

import (
	"context"
	"net/http"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

func (c *Client) ScrollPoints(ctx context.Context, node domain.QdrantNode, collection string, offset domain.PointID, limit int) ([]domain.Point, domain.PointID, error) {
	body := domain.JSON{"limit": limit, "with_payload": true, "with_vector": true}
	if !offset.IsZero() {
		body["offset"] = offset
	}
	r := c.slow(node, http.MethodPost, route("collections", collection, "points", "scroll"))
	r.body = body
	result, err := call[struct {
		Points         []domain.Point `json:"points"`
		NextPageOffset domain.PointID `json:"next_page_offset"`
	}](ctx, r)
	if err != nil {
		return nil, domain.PointID{}, err
	}
	return result.Points, result.NextPageOffset, nil
}

func (c *Client) UpsertPoints(ctx context.Context, node domain.QdrantNode, collection string, points []domain.Point, shardKey domain.PointShardKey) error {
	body := domain.JSON{"points": points}
	if shardKey.IsSet() {
		body["shard_key"] = shardKey
	}
	r := c.slow(node, http.MethodPut, route("collections", collection, "points"))
	r.query = waiting()
	r.body = body
	return c.execute(ctx, r)
}

func (c *Client) DeletePoints(ctx context.Context, node domain.QdrantNode, collection string, ids []domain.PointID) error {
	r := c.slow(node, http.MethodPost, route("collections", collection, "points", "delete"))
	r.query = waiting()
	r.body = domain.JSON{"points": ids}
	return c.execute(ctx, r)
}

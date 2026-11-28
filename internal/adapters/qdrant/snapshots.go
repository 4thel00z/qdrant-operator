package qdrant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/4thel00z/qdrant-operator/internal/domain"
	"github.com/4thel00z/qdrant-operator/internal/ports"
)

type snapshotDescription struct {
	Name         string `json:"name"`
	CreationTime string `json:"creation_time"`
	Size         int64  `json:"size"`
	Checksum     string `json:"checksum"`
}

func (c *Client) CreateSnapshot(ctx context.Context, node domain.QdrantNode, collection string) (domain.Snapshot, error) {
	r := c.slow(node, http.MethodPost, route("collections", collection, "snapshots"))
	r.query = waiting()
	result, err := call[snapshotDescription](ctx, r)
	if err != nil {
		return domain.Snapshot{}, err
	}
	return domain.Snapshot{Name: result.Name, Collection: collection, SizeBytes: result.Size, Checksum: result.Checksum}, nil
}

func (c *Client) StreamSnapshot(ctx context.Context, node domain.QdrantNode, collection string, snapshotName string) (io.ReadCloser, error) {
	r := c.slow(node, http.MethodGet, route("collections", collection, "snapshots", snapshotName))
	response, cancel, err := send(ctx, r)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		defer cancel()
		defer response.Body.Close()
		return nil, failed(r, replyError(response))
	}
	return cancelOnClose{ReadCloser: response.Body, cancel: cancel}, nil
}

func (c *Client) ListSnapshots(ctx context.Context, node domain.QdrantNode, collection string) ([]domain.SnapshotInfo, error) {
	items, err := call[[]snapshotDescription](ctx, c.plain(node, http.MethodGet, route("collections", collection, "snapshots")))
	if errors.Is(err, ports.ErrNotFound) {
		return []domain.SnapshotInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	snapshots := make([]domain.SnapshotInfo, 0, len(items))
	for _, item := range items {
		created, err := snapshotTime(item.CreationTime)
		if err != nil {
			return nil, fmt.Errorf("snapshot %s of %s: %w", item.Name, collection, err)
		}
		snapshots = append(snapshots, domain.SnapshotInfo{Name: item.Name, CreatedAt: created, SizeBytes: item.Size})
	}
	return snapshots, nil
}

// snapshotTime reads Qdrant's creation_time, which carries no zone and means UTC.
func snapshotTime(value string) (time.Time, error) {
	zoned, err := domain.ParseTime(value)
	if err == nil {
		return zoned, nil
	}
	naive, naiveErr := time.ParseInLocation("2006-01-02T15:04:05.999999999", value, time.UTC)
	if naiveErr != nil {
		return time.Time{}, fmt.Errorf("creation_time %q: %w", value, err)
	}
	return naive, nil
}

func (c *Client) DeleteSnapshot(ctx context.Context, node domain.QdrantNode, collection string, snapshotName string) error {
	r := c.plain(node, http.MethodDelete, route("collections", collection, "snapshots", snapshotName))
	r.query = waiting()
	err := c.execute(ctx, r)
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	return err
}

func (c *Client) RecoverSnapshot(ctx context.Context, node domain.QdrantNode, collection string, location string, priority domain.RestorePriority, checksum string) error {
	body := domain.JSON{"location": location, "priority": string(priority)}
	if checksum != "" {
		body["checksum"] = checksum
	}
	r := c.slow(node, http.MethodPut, route("collections", collection, "snapshots", "recover"))
	r.query = waiting()
	r.body = body
	return c.execute(ctx, r)
}

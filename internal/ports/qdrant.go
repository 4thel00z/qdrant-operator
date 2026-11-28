package ports

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/4thel00z/qdrant-operator/internal/domain"
)

// ErrUnreachable marks a node that did not answer: connection failures and timeouts, never a Qdrant error reply.
var ErrUnreachable = errors.New("qdrant is unreachable")

// ErrNotFound marks a 404 reply.
var ErrNotFound = errors.New("qdrant resource not found")

// QdrantError is an error reply from a Qdrant node: its HTTP status and the message of {"status":{"error":...}}.
type QdrantError struct {
	Status  int
	Message string
}

func (e *QdrantError) Error() string {
	return fmt.Sprintf("qdrant replied %d: %s", e.Status, e.Message)
}

func (e *QdrantError) Is(target error) bool {
	return target == ErrNotFound && e.Status == http.StatusNotFound
}

// Qdrant is the REST API of one Qdrant node. Point IDs and shard keys never pass through float64.
type Qdrant interface {
	ListCollections(ctx context.Context, node domain.QdrantNode) ([]string, error)

	CreateSnapshot(ctx context.Context, node domain.QdrantNode, collection string) (domain.Snapshot, error)

	// StreamSnapshot returns the snapshot bytes; closing the reader ends the download.
	StreamSnapshot(ctx context.Context, node domain.QdrantNode, collection string, snapshotName string) (io.ReadCloser, error)

	// ListSnapshots is empty when the collection is unknown.
	ListSnapshots(ctx context.Context, node domain.QdrantNode, collection string) ([]domain.SnapshotInfo, error)

	// DeleteSnapshot treats a snapshot that is already gone as done.
	DeleteSnapshot(ctx context.Context, node domain.QdrantNode, collection string, snapshotName string) error

	// RecoverSnapshot asks the node to fetch a snapshot from a URL and recover the collection from it; checksum may be empty.
	RecoverSnapshot(ctx context.Context, node domain.QdrantNode, collection string, location string, priority domain.RestorePriority, checksum string) error

	CollectionInfo(ctx context.Context, node domain.QdrantNode, collection string) (domain.JSON, error)

	Ready(ctx context.Context, node domain.QdrantNode) bool

	CollectionExists(ctx context.Context, node domain.QdrantNode, collection string) (bool, error)

	CreateCollection(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error

	UpdateCollection(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error

	// DeleteCollection treats a collection that is already gone as done.
	DeleteCollection(ctx context.Context, node domain.QdrantNode, collection string) error

	// ListAliases maps every alias on the cluster to the collection it points at.
	ListAliases(ctx context.Context, node domain.QdrantNode) (map[string]string, error)

	UpdateAliases(ctx context.Context, node domain.QdrantNode, actions []domain.JSON) error

	// CreatePayloadIndex takes a field type name or a schema object.
	CreatePayloadIndex(ctx context.Context, node domain.QdrantNode, collection string, fieldName string, fieldSchema any) error

	DeletePayloadIndex(ctx context.Context, node domain.QdrantNode, collection string, fieldName string) error

	// ListShardKeys returns custom shard keys as strings or json.Number; empty for auto sharding.
	ListShardKeys(ctx context.Context, node domain.QdrantNode, collection string) ([]any, error)

	CreateShardKey(ctx context.Context, node domain.QdrantNode, collection string, body domain.JSON) error

	// CountPoints is approximate unless exact is set.
	CountPoints(ctx context.Context, node domain.QdrantNode, collection string, exact bool) (int64, error)

	// ScrollPoints returns one page with payload and vectors and the next offset, which is the zero PointID on the last page.
	ScrollPoints(ctx context.Context, node domain.QdrantNode, collection string, offset domain.PointID, limit int) ([]domain.Point, domain.PointID, error)

	UpsertPoints(ctx context.Context, node domain.QdrantNode, collection string, points []domain.Point, shardKey domain.PointShardKey) error

	DeletePoints(ctx context.Context, node domain.QdrantNode, collection string, ids []domain.PointID) error

	// ClusterTopology is the node's own peer id and every peer it knows, sorted by peer id.
	ClusterTopology(ctx context.Context, node domain.QdrantNode) (domain.ClusterTopology, error)

	CollectionShards(ctx context.Context, node domain.QdrantNode, collection string) (domain.CollectionShards, error)

	ApplyShardOperation(ctx context.Context, node domain.QdrantNode, operation domain.ShardOperation) error

	// RemovePeer refuses a peer that still has shards unless force is set.
	RemovePeer(ctx context.Context, node domain.QdrantNode, peerID uint64, force bool) error
}

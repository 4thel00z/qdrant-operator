"""Port interfaces (typing.Protocol) between use cases and external systems."""

from collections.abc import AsyncIterator
from collections.abc import Mapping
from typing import Any
from typing import Protocol

from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import PodState
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestorePriority
from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec
from qdrant_operator.domain import SecretRef
from qdrant_operator.domain import Snapshot
from qdrant_operator.domain import SnapshotInfo
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import VolumeClaim
from qdrant_operator.domain import VolumeSnapshotState
from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import ShardOperation


class HelmPort(Protocol):
    async def apply(
        self,
        release_name: str,
        namespace: str,
        chart_version: str,
        values: Mapping[str, Any],
        statefulset: str,
        partition: int,
    ) -> None:
        """Install or upgrade a release to the given chart version and values.

        The rendered StatefulSet `statefulset` carries updateStrategy partition `partition`.

        Raises RuntimeError with helm's error output when the install or upgrade fails.
        """
        ...

    async def uninstall(self, release_name: str, namespace: str) -> None: ...

    async def release_exists(self, release_name: str, namespace: str) -> bool: ...


class QdrantPort(Protocol):
    async def list_collections(self, node: QdrantNode) -> list[str]: ...

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot: ...

    def stream_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> AsyncIterator[bytes]: ...

    async def list_snapshots(self, node: QdrantNode, collection: str) -> list[SnapshotInfo]:
        """Snapshots the node holds for a collection; empty when the collection is unknown."""
        ...

    async def delete_snapshot(self, node: QdrantNode, collection: str, snapshot_name: str) -> None:
        """Delete a snapshot; one that is already gone is not an error."""
        ...

    async def recover_snapshot(
        self,
        node: QdrantNode,
        collection: str,
        location: str,
        priority: RestorePriority,
        checksum: str | None,
    ) -> None:
        """Ask the node to fetch a snapshot from a URL and recover the collection from it."""
        ...

    async def collection_info(self, node: QdrantNode, collection: str) -> JsonDict: ...

    async def ready(self, node: QdrantNode) -> bool: ...

    async def collection_exists(self, node: QdrantNode, collection: str) -> bool: ...

    async def create_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        """PUT /collections/{collection} with a Qdrant CreateCollection body."""
        ...

    async def update_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        """PATCH /collections/{collection} with a Qdrant UpdateCollection body."""
        ...

    async def delete_collection(self, node: QdrantNode, collection: str) -> None: ...

    async def list_aliases(self, node: QdrantNode) -> dict[str, str]:
        """Every alias on the cluster, mapped to the collection it points at."""
        ...

    async def update_aliases(self, node: QdrantNode, actions: list[JsonDict]) -> None:
        """Apply create_alias/delete_alias actions atomically."""
        ...

    async def create_payload_index(
        self, node: QdrantNode, collection: str, field_name: str, field_schema: str | JsonDict
    ) -> None: ...

    async def delete_payload_index(
        self, node: QdrantNode, collection: str, field_name: str
    ) -> None: ...

    async def list_shard_keys(self, node: QdrantNode, collection: str) -> list[Any]:
        """Custom shard keys of a collection (empty for auto sharding)."""
        ...

    async def create_shard_key(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        """PUT /collections/{collection}/shards with a Qdrant CreateShardingKey body."""
        ...

    async def count_points(self, node: QdrantNode, collection: str, exact: bool = False) -> int:
        """Point count; approximate unless exact is set."""
        ...

    async def scroll_points(
        self, node: QdrantNode, collection: str, offset: Any, limit: int
    ) -> tuple[list[JsonDict], Any]:
        """One page of points with payload and vectors; returns (points, next offset or None)."""
        ...

    async def upsert_points(
        self, node: QdrantNode, collection: str, points: list[JsonDict], shard_key: Any = None
    ) -> None: ...

    async def cluster_topology(self, node: QdrantNode) -> ClusterTopology:
        """GET /cluster: the node's own peer id and every peer it knows."""
        ...

    async def collection_shards(self, node: QdrantNode, collection: str) -> CollectionShards:
        """Shard replicas, running transfers and replication factor of one collection."""
        ...

    async def apply_shard_operation(self, node: QdrantNode, operation: ShardOperation) -> None:
        """POST /collections/{c}/cluster with a move_shard, replicate_shard or drop_replica."""
        ...

    async def remove_peer(self, node: QdrantNode, peer_id: int, force: bool) -> None:
        """DELETE /cluster/peer/{id}; without force Qdrant refuses a peer that still has shards."""
        ...


class StoragePort(Protocol):
    async def upload_stream(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        chunks: AsyncIterator[bytes],
    ) -> int:
        """Stream chunks into an object. Returns the number of bytes written."""
        ...

    async def put_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str, data: bytes
    ) -> None: ...

    async def get_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str
    ) -> bytes: ...

    async def delete_prefix(
        self, storage: S3StorageSpec, credentials: S3Credentials, prefix: str
    ) -> int:
        """Delete every object under a prefix. Returns how many were deleted."""
        ...

    async def abort_uploads(
        self, storage: S3StorageSpec, credentials: S3Credentials, prefix: str
    ) -> int:
        """Abort unfinished multipart uploads under a prefix. Returns how many were aborted."""
        ...

    async def presigned_get_url(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        expires_seconds: int,
    ) -> str: ...


class KubernetesPort(Protocol):
    async def get_secret_value(self, secret_ref: SecretRef) -> str: ...

    async def apply_secret(
        self, name: str, namespace: str, data: Mapping[str, str], owner: JsonDict
    ) -> None:
        """Create or replace an Opaque Secret owned by the given resource (ownerReferences).

        Raises PermissionError when a Secret of that name exists without this owner.
        """
        ...

    async def get_custom_resource(self, ref: ResourceRef) -> JsonDict | None: ...

    async def list_custom_resources(
        self, kind: ResourceKind, namespace: str, label_selector: str | None = None
    ) -> list[JsonDict]: ...

    async def create_custom_resource(self, body: JsonDict) -> JsonDict: ...

    async def delete_custom_resource(self, ref: ResourceRef) -> None: ...

    async def patch_status(self, ref: ResourceRef, status: JsonDict) -> None: ...

    async def get_statefulset_status(
        self, name: str, namespace: str
    ) -> StatefulSetStatus | None: ...

    async def set_statefulset_partition(self, name: str, namespace: str, partition: int) -> None:
        """Patch updateStrategy.rollingUpdate.partition of the StatefulSet."""
        ...

    async def list_pod_states(self, namespace: str, label_selector: str) -> list[PodState]:
        """Pods with their controller-revision-hash label and Ready condition."""
        ...

    async def get_statefulset_claims(self, name: str, namespace: str) -> dict[str, str] | None:
        """Requested storage per volume claim template, None when the StatefulSet is absent."""
        ...

    async def list_volume_claims(self, namespace: str, label_selector: str) -> list[VolumeClaim]:
        """PersistentVolumeClaims with the size each one requests."""
        ...

    async def resize_volume_claim(self, name: str, namespace: str, size: str) -> None: ...

    async def orphan_statefulset(self, name: str, namespace: str) -> None:
        """Delete the StatefulSet but leave its pods and volumes running."""
        ...

    async def delete_volume_claim(self, name: str, namespace: str) -> None: ...

    async def create_volume_claim(self, body: JsonDict) -> None:
        """Create a PersistentVolumeClaim; one that already exists is left as it is."""
        ...

    async def create_volume_snapshot(self, body: JsonDict) -> None:
        """Create a snapshot.storage.k8s.io/v1 VolumeSnapshot; one that already exists is kept."""
        ...

    async def get_volume_snapshot(self, name: str, namespace: str) -> VolumeSnapshotState | None:
        """Readiness and error of a VolumeSnapshot, None when it is absent."""
        ...

    async def list_volume_snapshots(self, namespace: str, label_selector: str) -> list[str]:
        """Names of the VolumeSnapshots matching the selector."""
        ...

    async def delete_volume_snapshot(self, name: str, namespace: str) -> None:
        """Delete a VolumeSnapshot; one that is already gone is not an error."""
        ...

    async def crash_log(self, name: str, namespace: str, tail_lines: int) -> str:
        """Last lines of the pod's current and previous container runs; empty when there are none.

        A crash-looping container is terminated between restarts, so its error is in the current
        log, while the previous run's log may already be gone.
        """
        ...


class TokenPort(Protocol):
    def sign(self, claims: JsonDict, secret: str) -> str:
        """Encode claims as an HS256 JWT signed with the cluster's API key."""
        ...

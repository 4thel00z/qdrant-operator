"""Port interfaces (typing.Protocol) between use cases and external systems."""

from collections.abc import AsyncIterator
from collections.abc import Mapping
from typing import Any
from typing import Protocol

from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestorePriority
from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec
from qdrant_operator.domain import SecretRef
from qdrant_operator.domain import Snapshot
from qdrant_operator.domain import StatefulSetStatus


class HelmPort(Protocol):
    async def apply(
        self,
        release_name: str,
        namespace: str,
        chart_version: str,
        values: Mapping[str, Any],
    ) -> None:
        """Install or upgrade a release to the given chart version and values."""
        ...

    async def uninstall(self, release_name: str, namespace: str) -> None: ...

    async def release_exists(self, release_name: str, namespace: str) -> bool: ...


class QdrantPort(Protocol):
    async def list_collections(self, node: QdrantNode) -> list[str]: ...

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot: ...

    def stream_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> AsyncIterator[bytes]: ...

    async def delete_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> None: ...

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

    async def presigned_get_url(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        expires_seconds: int,
    ) -> str: ...


class KubernetesPort(Protocol):
    async def get_secret_value(self, secret_ref: SecretRef) -> str: ...

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

"""In-memory implementations of the ports, used by the unit tests instead of mocks."""

from collections.abc import AsyncIterator
from collections.abc import Mapping
from dataclasses import dataclass
from dataclasses import field
from datetime import UTC
from datetime import datetime
from typing import Any

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
from qdrant_operator.domain import format_time


@dataclass
class FakeHelm:
    releases: dict[tuple[str, str], JsonDict] = field(
        default_factory=dict[tuple[str, str], JsonDict]
    )
    apply_calls: int = 0

    async def apply(
        self, release_name: str, namespace: str, chart_version: str, values: Mapping[str, Any]
    ) -> None:
        self.apply_calls += 1
        self.releases[(namespace, release_name)] = {
            "chartVersion": chart_version,
            "values": dict(values),
        }

    async def uninstall(self, release_name: str, namespace: str) -> None:
        del self.releases[(namespace, release_name)]

    async def release_exists(self, release_name: str, namespace: str) -> bool:
        return (namespace, release_name) in self.releases


def snapshot_payload(node_url: str, collection: str) -> bytes:
    return f"{node_url}:{collection}\n".encode() * 200


@dataclass
class FakeQdrant:
    collections: dict[str, dict[str, JsonDict]] = field(
        default_factory=dict[str, dict[str, JsonDict]]
    )
    snapshots: dict[tuple[str, str, str], bytes] = field(
        default_factory=dict[tuple[str, str, str], bytes]
    )
    recovered: list[tuple[str, str, str, RestorePriority, str | None]] = field(
        default_factory=list[tuple[str, str, str, RestorePriority, str | None]]
    )
    failing_collections: set[str] = field(default_factory=set[str])
    aliases: dict[str, str] = field(default_factory=dict[str, str])
    created: int = 0

    def resolve(self, node: QdrantNode) -> str:
        """Map a Service URL onto the node that would answer it."""
        return self.aliases.get(node.url, node.url)

    def add_collection(self, node_url: str, collection: str, points: int = 0) -> None:
        self.collections.setdefault(node_url, {})[collection] = {
            "status": "green",
            "points_count": points,
        }

    async def list_collections(self, node: QdrantNode) -> list[str]:
        return sorted(self.collections.get(self.resolve(node), {}))

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot:
        if collection in self.failing_collections:
            raise RuntimeError(f"snapshot of {collection} refused")
        self.created += 1
        name = f"{collection}-{self.created}.snapshot"
        payload = snapshot_payload(node.url, collection)
        self.snapshots[(node.url, collection, name)] = payload
        return Snapshot(name, collection, len(payload), checksum=f"sha256:{self.created}")

    async def stream_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> AsyncIterator[bytes]:
        payload = self.snapshots[(node.url, collection, snapshot_name)]
        for start in range(0, len(payload), 64):
            yield payload[start : start + 64]

    async def delete_snapshot(self, node: QdrantNode, collection: str, snapshot_name: str) -> None:
        del self.snapshots[(node.url, collection, snapshot_name)]

    async def recover_snapshot(
        self,
        node: QdrantNode,
        collection: str,
        location: str,
        priority: RestorePriority,
        checksum: str | None,
    ) -> None:
        self.recovered.append((node.url, collection, location, priority, checksum))
        self.add_collection(node.url, collection, points=42)

    async def collection_info(self, node: QdrantNode, collection: str) -> JsonDict:
        return self.collections[self.resolve(node)][collection]

    async def ready(self, node: QdrantNode) -> bool:
        return True


@dataclass
class FakeStorage:
    objects: dict[tuple[str, str], bytes] = field(default_factory=dict[tuple[str, str], bytes])

    async def upload_stream(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        chunks: AsyncIterator[bytes],
    ) -> int:
        data = b"".join([chunk async for chunk in chunks])
        self.objects[(storage.bucket, key)] = data
        return len(data)

    async def put_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str, data: bytes
    ) -> None:
        self.objects[(storage.bucket, key)] = data

    async def get_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str
    ) -> bytes:
        return self.objects[(storage.bucket, key)]

    async def delete_prefix(
        self, storage: S3StorageSpec, credentials: S3Credentials, prefix: str
    ) -> int:
        doomed = [
            k for k in self.objects if k[0] == storage.bucket and k[1].startswith(f"{prefix}/")
        ]
        for key in doomed:
            del self.objects[key]
        return len(doomed)

    async def presigned_get_url(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        expires_seconds: int,
    ) -> str:
        return f"https://presigned.test/{storage.bucket}/{key}?expires={expires_seconds}"

    def keys(self, bucket: str) -> list[str]:
        return sorted(key for b, key in self.objects if b == bucket)


@dataclass
class FakeKubernetes:
    secrets: dict[tuple[str, str], dict[str, str]] = field(
        default_factory=dict[tuple[str, str], dict[str, str]]
    )
    resources: dict[tuple[str, str, str], JsonDict] = field(
        default_factory=dict[tuple[str, str, str], JsonDict]
    )
    statefulsets: dict[tuple[str, str], StatefulSetStatus] = field(
        default_factory=dict[tuple[str, str], StatefulSetStatus]
    )
    status_patches: list[tuple[ResourceRef, JsonDict]] = field(
        default_factory=list[tuple[ResourceRef, JsonDict]]
    )
    deleted: list[ResourceRef] = field(default_factory=list[ResourceRef])
    clock: datetime = datetime(2026, 9, 23, 2, 30, tzinfo=UTC)

    def put_resource(self, body: JsonDict) -> JsonDict:
        metadata = body["metadata"]
        key = (plural_of(body["kind"]), metadata["namespace"], metadata["name"])
        metadata.setdefault("uid", f"uid-{metadata['name']}")
        metadata.setdefault("creationTimestamp", format_time(self.clock))
        self.resources[key] = body
        return body

    async def get_secret_value(self, secret_ref: SecretRef) -> str:
        data = self.secrets.get((secret_ref.namespace, secret_ref.name))
        if data is None or secret_ref.key not in data:
            raise KeyError(f"{secret_ref.namespace}/{secret_ref.name}:{secret_ref.key}")
        return data[secret_ref.key]

    async def get_custom_resource(self, ref: ResourceRef) -> JsonDict | None:
        return self.resources.get((ref.kind.plural, ref.namespace, ref.name))

    async def list_custom_resources(
        self, kind: ResourceKind, namespace: str, label_selector: str | None = None
    ) -> list[JsonDict]:
        wanted = dict([label_selector.split("=", 1)]) if label_selector else {}
        return [
            body
            for (plural, ns, _), body in self.resources.items()
            if plural == kind.plural
            and ns == namespace
            and all(body["metadata"].get("labels", {}).get(k) == v for k, v in wanted.items())
        ]

    async def create_custom_resource(self, body: JsonDict) -> JsonDict:
        return self.put_resource(body)

    async def delete_custom_resource(self, ref: ResourceRef) -> None:
        self.deleted.append(ref)
        self.resources.pop((ref.kind.plural, ref.namespace, ref.name), None)

    async def patch_status(self, ref: ResourceRef, status: JsonDict) -> None:
        self.status_patches.append((ref, status))
        body = self.resources.get((ref.kind.plural, ref.namespace, ref.name))
        if body is not None:
            body["status"] = {**body.get("status", {}), **status}

    async def get_statefulset_status(self, name: str, namespace: str) -> StatefulSetStatus | None:
        return self.statefulsets.get((namespace, name))


def plural_of(kind: str) -> str:
    return f"{kind.lower()}s"

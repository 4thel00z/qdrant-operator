"""In-memory implementations of the ports, used by the unit tests instead of mocks."""

from collections.abc import AsyncIterator
from collections.abc import Mapping
from dataclasses import dataclass
from dataclasses import field
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from typing import Any

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
from qdrant_operator.domain import format_time
from qdrant_operator.domain import merge_dicts
from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import Peer
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardOperation
from qdrant_operator.peers import ShardOperationKind


@dataclass
class FakeHelm:
    releases: dict[tuple[str, str], JsonDict] = field(
        default_factory=dict[tuple[str, str], JsonDict]
    )
    apply_calls: int = 0
    apply_error: str | None = None

    async def apply(
        self,
        release_name: str,
        namespace: str,
        chart_version: str,
        values: Mapping[str, Any],
        statefulset: str,
        partition: int,
    ) -> None:
        self.apply_calls += 1
        if self.apply_error:
            raise RuntimeError(self.apply_error)
        self.releases[(namespace, release_name)] = {
            "chartVersion": chart_version,
            "values": dict(values),
            "statefulset": statefulset,
            "partition": partition,
        }

    async def uninstall(self, release_name: str, namespace: str) -> None:
        del self.releases[(namespace, release_name)]

    async def release_exists(self, release_name: str, namespace: str) -> bool:
        return (namespace, release_name) in self.releases


def snapshot_payload(node_url: str, collection: str) -> bytes:
    return f"{node_url}:{collection}\n".encode() * 200


DEFAULT_HNSW: JsonDict = {
    "m": 16,
    "ef_construct": 100,
    "full_scan_threshold": 10000,
    "max_indexing_threads": 0,
    "on_disk": False,
    "payload_m": None,
}
DEFAULT_OPTIMIZERS: JsonDict = {
    "deleted_threshold": 0.2,
    "vacuum_min_vector_number": 1000,
    "default_segment_number": 0,
    "max_segment_size": None,
    "memmap_threshold": None,
    "indexing_threshold": 10000,
    "flush_interval_sec": 5,
    "max_optimization_threads": None,
}


def config_from_create(body: Mapping[str, Any]) -> JsonDict:
    """What GET /collections/{c} reports after PUT with this body, defaults filled like Qdrant."""
    return {
        "params": {
            "vectors": body.get("vectors", {}),
            "shard_number": body.get("shard_number", 1),
            "sharding_method": body.get("sharding_method"),
            "replication_factor": body.get("replication_factor", 1),
            "write_consistency_factor": body.get("write_consistency_factor", 1),
            "read_fan_out_factor": body.get("read_fan_out_factor"),
            "on_disk_payload": body.get("on_disk_payload", True),
            "sparse_vectors": body.get("sparse_vectors"),
        },
        "hnsw_config": merge_dicts(DEFAULT_HNSW, body.get("hnsw_config", {})),
        "optimizer_config": merge_dicts(DEFAULT_OPTIMIZERS, body.get("optimizers_config", {})),
        "wal_config": {"wal_capacity_mb": 32, "wal_segments_ahead": 0},
        "quantization_config": body.get("quantization_config"),
        "strict_mode_config": body.get("strict_mode_config"),
        "metadata": body.get("metadata"),
    }


def quantization_after(config: Mapping[str, Any], body: Mapping[str, Any]) -> Any:
    if "quantization_config" not in body:
        return config.get("quantization_config")
    return None if body["quantization_config"] == "Disabled" else body["quantization_config"]


def apply_update(config: JsonDict, body: Mapping[str, Any]) -> JsonDict:
    """Fold a PATCH /collections/{c} body into a reported config the way Qdrant does."""
    params: JsonDict = dict(config["params"])
    vectors = params.get("vectors", {})
    for name, diff in body.get("vectors", {}).items():
        if name == "":
            vectors = merge_dicts(vectors, diff)
            continue
        vectors = {**vectors, name: merge_dicts(vectors.get(name, {}), diff)}
    params["vectors"] = vectors
    if "sparse_vectors" in body:
        params["sparse_vectors"] = merge_dicts(
            params.get("sparse_vectors") or {}, body["sparse_vectors"]
        )
    params.update(body.get("params", {}))
    return {
        **config,
        "params": params,
        "hnsw_config": merge_dicts(config["hnsw_config"], body.get("hnsw_config", {})),
        "optimizer_config": merge_dicts(
            config["optimizer_config"],
            {
                key: None if value == "auto" else value
                for key, value in body.get("optimizers_config", {}).items()
            },
        ),
        "quantization_config": quantization_after(config, body),
        "strict_mode_config": merge_dicts(
            config.get("strict_mode_config") or {}, body.get("strict_mode_config", {})
        )
        or None,
        "metadata": merge_dicts(config.get("metadata") or {}, body.get("metadata", {})) or None,
    }


@dataclass
class FakeQdrant:
    collections: dict[str, dict[str, JsonDict]] = field(
        default_factory=dict[str, dict[str, JsonDict]]
    )
    snapshots: dict[tuple[str, str, str], bytes] = field(
        default_factory=dict[tuple[str, str, str], bytes]
    )
    snapshot_times: dict[tuple[str, str, str], datetime] = field(
        default_factory=dict[tuple[str, str, str], datetime]
    )
    recovered: list[tuple[str, str, str, RestorePriority, str | None]] = field(
        default_factory=list[tuple[str, str, str, RestorePriority, str | None]]
    )
    failing_collections: set[str] = field(default_factory=set[str])
    routes: dict[str, str] = field(default_factory=dict[str, str])
    aliases: dict[str, str] = field(default_factory=dict[str, str])
    updates: list[tuple[str, JsonDict]] = field(default_factory=list[tuple[str, JsonDict]])
    index_changes: list[tuple[str, str, str]] = field(default_factory=list[tuple[str, str, str]])
    alias_changes: list[list[JsonDict]] = field(default_factory=list[list[JsonDict]])
    points: dict[tuple[str, str], dict[Any, JsonDict]] = field(
        default_factory=dict[tuple[str, str], dict[Any, JsonDict]]
    )
    late_points: dict[tuple[str, str], list[list[JsonDict]]] = field(
        default_factory=dict[tuple[str, str], list[list[JsonDict]]]
    )
    upserts: list[tuple[str, Any, int]] = field(default_factory=list[tuple[str, Any, int]])
    shard_key_bodies: list[tuple[str, JsonDict]] = field(default_factory=list[tuple[str, JsonDict]])
    peers: dict[int, str] = field(default_factory=dict[int, str])
    node_peer_ids: dict[str, int] = field(default_factory=dict[str, int])
    replicas: dict[str, list[Replica]] = field(default_factory=dict[str, list[Replica]])
    replication_factors: dict[str, int] = field(default_factory=dict[str, int])
    slow_transfers: bool = False
    unreachable_after_upserts: int | None = None
    pending: list[ShardOperation] = field(default_factory=list[ShardOperation])
    shard_operations: list[ShardOperation] = field(default_factory=list[ShardOperation])
    removed_peers: list[tuple[int, bool]] = field(default_factory=list[tuple[int, bool]])
    created: int = 0

    def resolve(self, node: QdrantNode) -> str:
        """Map a Service URL onto the node that would answer it."""
        return self.routes.get(node.url, node.url)

    def add_collection(
        self, node_url: str, collection: str, points: int = 0, body: JsonDict | None = None
    ) -> None:
        self.collections.setdefault(node_url, {})[collection] = {
            "status": "green",
            "points_count": points,
            "indexed_vectors_count": points,
            "segments_count": 1,
            "config": config_from_create(body or {"vectors": {"size": 4, "distance": "Cosine"}}),
            "payload_schema": {},
        }

    def entry(self, node: QdrantNode, collection: str) -> JsonDict:
        return self.collections[self.resolve(node)][collection]

    async def list_collections(self, node: QdrantNode) -> list[str]:
        return sorted(self.collections.get(self.resolve(node), {}))

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot:
        if collection in self.failing_collections:
            raise RuntimeError(f"snapshot of {collection} refused")
        self.created += 1
        name = f"{collection}-{self.created}.snapshot"
        payload = snapshot_payload(node.url, collection)
        self.add_snapshot(node.url, collection, name, datetime.now(UTC), payload)
        return Snapshot(name, collection, len(payload), checksum=f"sha256:{self.created}")

    async def stream_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> AsyncIterator[bytes]:
        payload = self.snapshots[(node.url, collection, snapshot_name)]
        for start in range(0, len(payload), 64):
            yield payload[start : start + 64]

    def add_snapshot(
        self,
        node_url: str,
        collection: str,
        name: str,
        created_at: datetime,
        payload: bytes = b"leftover",
    ) -> None:
        self.snapshots[(node_url, collection, name)] = payload
        self.snapshot_times[(node_url, collection, name)] = created_at

    async def list_snapshots(self, node: QdrantNode, collection: str) -> list[SnapshotInfo]:
        return [
            SnapshotInfo(name, self.snapshot_times[(url, coll, name)], len(payload))
            for (url, coll, name), payload in self.snapshots.items()
            if url == node.url and coll == collection
        ]

    async def delete_snapshot(self, node: QdrantNode, collection: str, snapshot_name: str) -> None:
        self.snapshots.pop((node.url, collection, snapshot_name), None)
        self.snapshot_times.pop((node.url, collection, snapshot_name), None)

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
        return self.entry(node, collection)

    async def ready(self, node: QdrantNode) -> bool:
        return True

    def add_points(self, node_url: str, collection: str, points: list[JsonDict]) -> None:
        store = self.points.setdefault((node_url, collection), {})
        for point in points:
            store[point["id"]] = point
        self.collections[node_url][collection]["points_count"] = len(store)

    def stored_points(self, node_url: str, collection: str) -> list[JsonDict]:
        store = self.points.get((node_url, collection), {})
        return [store[point_id] for point_id in sorted(store)]

    async def count_points(self, node: QdrantNode, collection: str, exact: bool = False) -> int:
        return len(self.points.get((self.resolve(node), collection), {}))

    async def scroll_points(
        self, node: QdrantNode, collection: str, offset: Any, limit: int
    ) -> tuple[list[JsonDict], Any]:
        store = self.points.get((self.resolve(node), collection), {})
        ids = sorted(store)
        start = ids.index(offset) if offset is not None else 0
        page = ids[start : start + limit]
        following = ids[start + limit : start + limit + 1]
        result = [store[i] for i in page]
        if following:
            return result, following[0]
        waves = self.late_points.get((self.resolve(node), collection), [])
        if waves:
            self.add_points(self.resolve(node), collection, waves.pop(0))
        return result, None

    async def upsert_points(
        self, node: QdrantNode, collection: str, points: list[JsonDict], shard_key: Any = None
    ) -> None:
        if (
            self.unreachable_after_upserts is not None
            and len(self.upserts) >= self.unreachable_after_upserts
        ):
            raise ConnectionError(f"Qdrant at {node.url} is unreachable")
        self.upserts.append((collection, shard_key, len(points)))
        self.add_points(
            self.resolve(node),
            collection,
            [{**p, **({"shard_key": shard_key} if shard_key is not None else {})} for p in points],
        )

    async def collection_exists(self, node: QdrantNode, collection: str) -> bool:
        return collection in self.collections.get(self.resolve(node), {})

    async def create_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        if await self.collection_exists(node, collection):
            raise RuntimeError(f"collection {collection} already exists")
        self.add_collection(self.resolve(node), collection, body=body)

    async def update_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        entry = self.entry(node, collection)
        entry["config"] = apply_update(entry["config"], body)
        self.updates.append((collection, body))

    async def delete_collection(self, node: QdrantNode, collection: str) -> None:
        self.collections.get(self.resolve(node), {}).pop(collection, None)
        self.aliases = {a: c for a, c in self.aliases.items() if c != collection}

    async def list_aliases(self, node: QdrantNode) -> dict[str, str]:
        return dict(self.aliases)

    async def update_aliases(self, node: QdrantNode, actions: list[JsonDict]) -> None:
        self.alias_changes.append(actions)
        for action in actions:
            if "create_alias" in action:
                create = action["create_alias"]
                self.aliases[create["alias_name"]] = create["collection_name"]
                continue
            self.aliases.pop(action["delete_alias"]["alias_name"], None)

    async def create_payload_index(
        self, node: QdrantNode, collection: str, field_name: str, field_schema: str | JsonDict
    ) -> None:
        schema: JsonDict = (
            {"type": field_schema} if isinstance(field_schema, str) else dict(field_schema)
        )
        self.entry(node, collection)["payload_schema"][field_name] = {
            "data_type": schema["type"],
            "params": schema if len(schema) > 1 else None,
            "points": 0,
        }
        self.index_changes.append(("create", collection, field_name))

    async def delete_payload_index(
        self, node: QdrantNode, collection: str, field_name: str
    ) -> None:
        self.entry(node, collection)["payload_schema"].pop(field_name, None)
        self.index_changes.append(("delete", collection, field_name))

    async def list_shard_keys(self, node: QdrantNode, collection: str) -> list[Any]:
        return list(self.entry(node, collection).setdefault("shard_keys", []))

    async def create_shard_key(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        keys: list[Any] = self.entry(node, collection).setdefault("shard_keys", [])
        if body["shard_key"] in keys:
            raise RuntimeError(f"shard key {body['shard_key']} already exists")
        keys.append(body["shard_key"])
        self.shard_key_bodies.append((collection, body))

    async def cluster_topology(self, node: QdrantNode) -> ClusterTopology:
        own = self.node_peer_ids.get(self.resolve(node))
        if own is None:
            raise ConnectionError(f"Qdrant at {node.url} is unreachable")
        return ClusterTopology(
            own, tuple(Peer(pid, uri) for pid, uri in sorted(self.peers.items()))
        )

    async def collection_shards(self, node: QdrantNode, collection: str) -> CollectionShards:
        return CollectionShards(
            collection=collection,
            replicas=tuple(self.replicas.get(collection, [])),
            replication_factor=self.replication_factors.get(collection, 1),
            transfers=len([op for op in self.pending if op.collection == collection]),
        )

    async def apply_shard_operation(self, node: QdrantNode, operation: ShardOperation) -> None:
        self.shard_operations.append(operation)
        if self.slow_transfers and operation.kind is not ShardOperationKind.DROP:
            self.pending.append(operation)
            return
        self.execute(operation)

    def execute(self, operation: ShardOperation) -> None:
        replicas = self.replicas[operation.collection]
        source = next(
            r
            for r in replicas
            if r.shard_id == operation.shard_id and r.peer_id == operation.peer_id
        )
        if operation.kind is ShardOperationKind.DROP:
            replicas.remove(source)
            return
        if operation.target_peer_id is None:
            raise ValueError("transfer without a target peer")
        if operation.kind is ShardOperationKind.MOVE:
            replicas.remove(source)
        replicas.append(Replica(operation.shard_id, operation.target_peer_id, "Active"))

    def settle(self) -> None:
        """Finish every transfer that slow_transfers held back."""
        for operation in self.pending:
            self.execute(operation)
        self.pending.clear()

    async def remove_peer(self, node: QdrantNode, peer_id: int, force: bool) -> None:
        holding = [r for rs in self.replicas.values() for r in rs if r.peer_id == peer_id]
        if holding and not force:
            raise RuntimeError(f"peer {peer_id} still holds {len(holding)} shard replicas")
        for collection, replicas in self.replicas.items():
            self.replicas[collection] = [r for r in replicas if r.peer_id != peer_id]
        self.peers.pop(peer_id, None)
        self.removed_peers.append((peer_id, force))


@dataclass
class FakeStorage:
    objects: dict[tuple[str, str], bytes] = field(default_factory=dict[tuple[str, str], bytes])
    uploads: set[tuple[str, str]] = field(default_factory=set[tuple[str, str]])

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

    async def abort_uploads(
        self, storage: S3StorageSpec, credentials: S3Credentials, prefix: str
    ) -> int:
        doomed = {
            k for k in self.uploads if k[0] == storage.bucket and k[1].startswith(f"{prefix}/")
        }
        self.uploads -= doomed
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
    claim_templates: dict[tuple[str, str], dict[str, str]] = field(
        default_factory=dict[tuple[str, str], dict[str, str]]
    )
    volume_claims: dict[str, dict[str, str]] = field(default_factory=dict[str, dict[str, str]])
    resized: list[tuple[str, str]] = field(default_factory=list[tuple[str, str]])
    orphaned: list[tuple[str, str]] = field(default_factory=list[tuple[str, str]])
    pods: dict[str, list[PodState]] = field(default_factory=dict[str, list[PodState]])
    partitions: list[int] = field(default_factory=list[int])
    pod_logs: dict[tuple[str, str], str] = field(default_factory=dict[tuple[str, str], str])
    deleted: list[ResourceRef] = field(default_factory=list[ResourceRef])
    secret_owners: dict[tuple[str, str], JsonDict] = field(
        default_factory=dict[tuple[str, str], JsonDict]
    )
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

    async def apply_secret(
        self, name: str, namespace: str, data: Mapping[str, str], owner: JsonDict
    ) -> None:
        key = (namespace, name)
        current_owner = self.secret_owners.get(key)
        if key in self.secrets and (not current_owner or current_owner["uid"] != owner["uid"]):
            raise PermissionError(
                f"Secret {namespace}/{name} exists and is not owned by this resource"
            )
        self.secrets[key] = dict(data)
        self.secret_owners[key] = owner

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

    async def set_statefulset_partition(self, name: str, namespace: str, partition: int) -> None:
        self.statefulsets[(namespace, name)] = replace(
            self.statefulsets[(namespace, name)], partition=partition
        )
        self.partitions.append(partition)

    async def list_pod_states(self, namespace: str, label_selector: str) -> list[PodState]:
        return list(self.pods.get(namespace, []))

    async def get_statefulset_claims(self, name: str, namespace: str) -> dict[str, str] | None:
        claims = self.claim_templates.get((namespace, name))
        return None if claims is None else dict(claims)

    async def list_volume_claims(self, namespace: str, label_selector: str) -> list[VolumeClaim]:
        return [VolumeClaim(n, size) for n, size in self.volume_claims.get(namespace, {}).items()]

    async def resize_volume_claim(self, name: str, namespace: str, size: str) -> None:
        self.volume_claims[namespace][name] = size
        self.resized.append((name, size))

    async def delete_volume_claim(self, name: str, namespace: str) -> None:
        self.volume_claims.get(namespace, {}).pop(name, None)

    async def crash_log(self, name: str, namespace: str, tail_lines: int) -> str:
        return self.pod_logs.get((namespace, name), "")

    async def orphan_statefulset(self, name: str, namespace: str) -> None:
        self.claim_templates.pop((namespace, name), None)
        self.statefulsets.pop((namespace, name), None)
        self.orphaned.append((namespace, name))


def plural_of(kind: str) -> str:
    return f"{kind.lower()}s"

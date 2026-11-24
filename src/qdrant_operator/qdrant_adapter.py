"""Qdrant REST API adapter. Stateless: every call opens its own client against the given node."""

import ssl
from collections.abc import AsyncGenerator
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from datetime import UTC
from datetime import datetime
from typing import Any

import httpx
from loguru import logger

from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import RestorePriority
from qdrant_operator.domain import Snapshot
from qdrant_operator.domain import SnapshotInfo
from qdrant_operator.domain import parse_time
from qdrant_operator.peers import ClusterTopology
from qdrant_operator.peers import CollectionShards
from qdrant_operator.peers import Peer
from qdrant_operator.peers import Replica
from qdrant_operator.peers import ShardOperation
from qdrant_operator.settings import QdrantSettings

STREAM_CHUNK_BYTES = 1024 * 1024
HTTP_NOT_FOUND = 404


def utc(value: datetime | None) -> datetime | None:
    if not value:
        return None
    return value if value.tzinfo else value.replace(tzinfo=UTC)


@dataclass
class QdrantAdapter:
    settings: QdrantSettings

    @asynccontextmanager
    async def client(self, node: QdrantNode, timeout: float) -> AsyncGenerator[httpx.AsyncClient]:
        try:
            async with httpx.AsyncClient(
                base_url=node.url,
                headers={"api-key": node.api_key} if node.api_key else {},
                timeout=timeout,
                verify=self.ssl_context(node),
            ) as client:
                yield client
        except httpx.TransportError as error:
            raise ConnectionError(f"Qdrant at {node.url} is unreachable: {error}") from error

    @staticmethod
    def ssl_context(node: QdrantNode) -> ssl.SSLContext | bool:
        if not node.ca_cert:
            return True
        context = ssl.create_default_context()
        context.load_verify_locations(cadata=node.ca_cert)
        return context

    async def list_collections(self, node: QdrantNode) -> list[str]:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(client.get("/collections"))
            return [c["name"] for c in result["collections"]]

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            result = await self.result(
                client.post(f"/collections/{collection}/snapshots", params={"wait": "true"})
            )
        logger.info(
            "snapshot created", node=node.url, collection=collection, snapshot=result["name"]
        )
        return Snapshot(
            name=result["name"],
            collection=collection,
            size_bytes=result.get("size", 0),
            checksum=result.get("checksum"),
        )

    async def stream_snapshot(
        self, node: QdrantNode, collection: str, snapshot_name: str
    ) -> AsyncIterator[bytes]:
        path = f"/collections/{collection}/snapshots/{snapshot_name}"
        async with (
            self.client(node, self.settings.snapshot_timeout_seconds) as client,
            client.stream("GET", path) as response,
        ):
            response.raise_for_status()
            async for chunk in response.aiter_bytes(STREAM_CHUNK_BYTES):
                yield chunk

    async def list_snapshots(self, node: QdrantNode, collection: str) -> list[SnapshotInfo]:
        async with self.client(node, self.settings.timeout_seconds) as client:
            response = await client.get(f"/collections/{collection}/snapshots")
        if response.status_code == HTTP_NOT_FOUND:
            return []
        response.raise_for_status()
        return [
            SnapshotInfo(
                name=item["name"],
                created_at=utc(parse_time(item.get("creation_time"))),
                size_bytes=item.get("size", 0),
            )
            for item in response.json()["result"]
        ]

    async def delete_snapshot(self, node: QdrantNode, collection: str, snapshot_name: str) -> None:
        async with self.client(node, self.settings.timeout_seconds) as client:
            response = await client.delete(
                f"/collections/{collection}/snapshots/{snapshot_name}",
                params={"wait": "true"},
            )
        if response.status_code == HTTP_NOT_FOUND:
            return
        response.raise_for_status()
        logger.info(
            "snapshot deleted", node=node.url, collection=collection, snapshot=snapshot_name
        )

    async def recover_snapshot(
        self,
        node: QdrantNode,
        collection: str,
        location: str,
        priority: RestorePriority,
        checksum: str | None,
    ) -> None:
        body: JsonDict = {"location": location, "priority": priority.value}
        if checksum:
            body["checksum"] = checksum
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(
                client.put(
                    f"/collections/{collection}/snapshots/recover",
                    params={"wait": "true"},
                    json=body,
                )
            )
        logger.info("snapshot recovered", node=node.url, collection=collection)

    async def collection_info(self, node: QdrantNode, collection: str) -> JsonDict:
        async with self.client(node, self.settings.timeout_seconds) as client:
            return await self.result(client.get(f"/collections/{collection}"))

    async def ready(self, node: QdrantNode) -> bool:
        try:
            async with self.client(node, 5.0) as client:
                response = await client.get("/readyz")
                return response.status_code == 200
        except httpx.HTTPError:
            return False

    async def collection_exists(self, node: QdrantNode, collection: str) -> bool:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(client.get(f"/collections/{collection}/exists"))
            return bool(result["exists"])

    async def create_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(client.put(f"/collections/{collection}", json=body))
        logger.info("collection created", node=node.url, collection=collection)

    async def update_collection(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(client.patch(f"/collections/{collection}", json=body))
        logger.info("collection updated", node=node.url, collection=collection, body=body)

    async def delete_collection(self, node: QdrantNode, collection: str) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            response = await client.delete(f"/collections/{collection}")
            if response.status_code != 404:
                response.raise_for_status()
        logger.info("collection deleted", node=node.url, collection=collection)

    async def list_aliases(self, node: QdrantNode) -> dict[str, str]:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(client.get("/aliases"))
            return {a["alias_name"]: a["collection_name"] for a in result["aliases"]}

    async def update_aliases(self, node: QdrantNode, actions: list[JsonDict]) -> None:
        async with self.client(node, self.settings.timeout_seconds) as client:
            await self.result(client.post("/collections/aliases", json={"actions": actions}))
        logger.info("aliases updated", node=node.url, actions=actions)

    async def create_payload_index(
        self, node: QdrantNode, collection: str, field_name: str, field_schema: str | JsonDict
    ) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(
                client.put(
                    f"/collections/{collection}/index",
                    params={"wait": "true"},
                    json={"field_name": field_name, "field_schema": field_schema},
                )
            )
        logger.info("payload index created", collection=collection, field=field_name)

    async def delete_payload_index(
        self, node: QdrantNode, collection: str, field_name: str
    ) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(
                client.delete(
                    f"/collections/{collection}/index/{field_name}", params={"wait": "true"}
                )
            )
        logger.info("payload index deleted", collection=collection, field=field_name)

    async def list_shard_keys(self, node: QdrantNode, collection: str) -> list[Any]:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(client.get(f"/collections/{collection}/shards"))
            entries: list[JsonDict] = result.get("shard_keys") or []
            return [entry["key"] for entry in entries]

    async def create_shard_key(self, node: QdrantNode, collection: str, body: JsonDict) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(client.put(f"/collections/{collection}/shards", json=body))
        logger.info("shard key created", collection=collection, shard_key=body["shard_key"])

    async def count_points(self, node: QdrantNode, collection: str, exact: bool = False) -> int:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(
                client.post(f"/collections/{collection}/points/count", json={"exact": exact})
            )
            return int(result["count"])

    async def scroll_points(
        self, node: QdrantNode, collection: str, offset: Any, limit: int
    ) -> tuple[list[JsonDict], Any]:
        body: JsonDict = {"limit": limit, "with_payload": True, "with_vector": True}
        if offset is not None:
            body["offset"] = offset
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            result = await self.result(
                client.post(f"/collections/{collection}/points/scroll", json=body)
            )
            return list(result["points"]), result.get("next_page_offset")

    async def upsert_points(
        self, node: QdrantNode, collection: str, points: list[JsonDict], shard_key: Any = None
    ) -> None:
        body: JsonDict = {"points": points}
        if shard_key is not None:
            body["shard_key"] = shard_key
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(
                client.put(f"/collections/{collection}/points", params={"wait": "true"}, json=body)
            )

    async def delete_points(self, node: QdrantNode, collection: str, ids: list[Any]) -> None:
        async with self.client(node, self.settings.snapshot_timeout_seconds) as client:
            await self.result(
                client.post(
                    f"/collections/{collection}/points/delete",
                    params={"wait": "true"},
                    json={"points": ids},
                )
            )

    @staticmethod
    async def result(request: Any) -> JsonDict:
        response: httpx.Response = await request
        response.raise_for_status()
        return response.json()["result"]

    async def cluster_topology(self, node: QdrantNode) -> ClusterTopology:
        async with self.client(node, self.settings.timeout_seconds) as client:
            result = await self.result(client.get("/cluster"))
        peers = result.get("peers", {})
        return ClusterTopology(
            peer_id=int(result.get("peer_id", 0)),
            peers=tuple(Peer(int(peer_id), info["uri"]) for peer_id, info in peers.items()),
        )

    async def collection_shards(self, node: QdrantNode, collection: str) -> CollectionShards:
        async with self.client(node, self.settings.timeout_seconds) as client:
            cluster = await self.result(client.get(f"/collections/{collection}/cluster"))
            info = await self.result(client.get(f"/collections/{collection}"))
        own = int(cluster["peer_id"])
        local = [Replica(s["shard_id"], own, s["state"]) for s in cluster.get("local_shards", [])]
        remote = [
            Replica(s["shard_id"], int(s["peer_id"]), s["state"])
            for s in cluster.get("remote_shards", [])
        ]
        transfers = len(cluster.get("shard_transfers", [])) + len(
            cluster.get("resharding_operations", [])
        )
        return CollectionShards(
            collection=collection,
            replicas=tuple(local + remote),
            replication_factor=info["config"]["params"].get("replication_factor", 1),
            transfers=transfers,
        )

    async def apply_shard_operation(self, node: QdrantNode, operation: ShardOperation) -> None:
        async with self.client(node, self.settings.timeout_seconds) as client:
            await self.result(
                client.post(
                    f"/collections/{operation.collection}/cluster",
                    params={"wait": "true"},
                    json=operation.to_body(),
                )
            )
        logger.info("shard operation applied", node=node.url, operation=operation.describe())

    async def remove_peer(self, node: QdrantNode, peer_id: int, force: bool) -> None:
        async with self.client(node, self.settings.timeout_seconds) as client:
            await self.result(
                client.delete(f"/cluster/peer/{peer_id}", params={"force": str(force).lower()})
            )
        logger.info("peer removed", node=node.url, peer_id=peer_id, force=force)

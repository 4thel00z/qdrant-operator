"""Qdrant REST API adapter. Stateless: every call opens its own client against the given node."""

import ssl
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any

import httpx
from loguru import logger

from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import RestorePriority
from qdrant_operator.domain import Snapshot

STREAM_CHUNK_BYTES = 1024 * 1024


@dataclass
class QdrantAdapter:
    timeout_seconds: float = 30.0
    snapshot_timeout_seconds: float = 3600.0

    @asynccontextmanager
    async def client(self, node: QdrantNode, timeout: float) -> AsyncIterator[httpx.AsyncClient]:
        async with httpx.AsyncClient(
            base_url=node.url,
            headers={"api-key": node.api_key} if node.api_key else {},
            timeout=timeout,
            verify=self.ssl_context(node),
        ) as client:
            yield client

    @staticmethod
    def ssl_context(node: QdrantNode) -> ssl.SSLContext | bool:
        if not node.ca_cert:
            return True
        context = ssl.create_default_context()
        context.load_verify_locations(cadata=node.ca_cert)
        return context

    async def list_collections(self, node: QdrantNode) -> list[str]:
        async with self.client(node, self.timeout_seconds) as client:
            result = await self.result(client.get("/collections"))
            return [c["name"] for c in result["collections"]]

    async def create_snapshot(self, node: QdrantNode, collection: str) -> Snapshot:
        async with self.client(node, self.snapshot_timeout_seconds) as client:
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
            self.client(node, self.snapshot_timeout_seconds) as client,
            client.stream("GET", path) as response,
        ):
            response.raise_for_status()
            async for chunk in response.aiter_bytes(STREAM_CHUNK_BYTES):
                yield chunk

    async def delete_snapshot(self, node: QdrantNode, collection: str, snapshot_name: str) -> None:
        async with self.client(node, self.timeout_seconds) as client:
            await self.result(
                client.delete(
                    f"/collections/{collection}/snapshots/{snapshot_name}",
                    params={"wait": "true"},
                )
            )
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
        async with self.client(node, self.snapshot_timeout_seconds) as client:
            await self.result(
                client.put(
                    f"/collections/{collection}/snapshots/recover",
                    params={"wait": "true"},
                    json=body,
                )
            )
        logger.info("snapshot recovered", node=node.url, collection=collection)

    async def collection_info(self, node: QdrantNode, collection: str) -> JsonDict:
        async with self.client(node, self.timeout_seconds) as client:
            return await self.result(client.get(f"/collections/{collection}"))

    async def ready(self, node: QdrantNode) -> bool:
        try:
            async with self.client(node, 5.0) as client:
                response = await client.get("/readyz")
                return response.status_code == 200
        except httpx.HTTPError:
            return False

    @staticmethod
    async def result(request: Any) -> JsonDict:
        response: httpx.Response = await request
        response.raise_for_status()
        return response.json()["result"]

"""Qdrant REST API adapter."""

from dataclasses import dataclass
from datetime import UTC
from datetime import datetime
from pathlib import Path

import httpx
import structlog

from qdrant_operator.domain import Snapshot

log = structlog.get_logger()


@dataclass
class QdrantAdapter:
    """Adapter for Qdrant REST API operations."""

    endpoint: str
    api_key: str | None = None
    timeout: float = 30.0

    async def list_collections(self) -> list[str]:
        """List all collection names."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=self.timeout,
        ) as client:
            response = await client.get("/collections")
            response.raise_for_status()
            data = response.json()
            return [c["name"] for c in data["result"]["collections"]]

    async def create_snapshot(self, collection: str) -> Snapshot:
        """Create a snapshot of a collection."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=300.0,
        ) as client:
            response = await client.post(f"/collections/{collection}/snapshots")
            response.raise_for_status()
            data = response.json()["result"]

            await log.ainfo("snapshot_created", collection=collection, snapshot=data["name"])

            return Snapshot(
                name=data["name"],
                collection=collection,
                size_bytes=data.get("size", 0),
                created_at=datetime.now(UTC),
            )

    async def list_snapshots(self, collection: str) -> list[Snapshot]:
        """List snapshots for a collection."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=self.timeout,
        ) as client:
            response = await client.get(f"/collections/{collection}/snapshots")
            response.raise_for_status()
            data = response.json()["result"]

            return [
                Snapshot(
                    name=s["name"],
                    collection=collection,
                    size_bytes=s.get("size", 0),
                    created_at=(
                        datetime.fromisoformat(s["creation_time"])
                        if s.get("creation_time")
                        else datetime.now(UTC)
                    ),
                )
                for s in data
            ]

    async def delete_snapshot(self, collection: str, snapshot_name: str) -> None:
        """Delete a snapshot."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=self.timeout,
        ) as client:
            response = await client.delete(
                f"/collections/{collection}/snapshots/{snapshot_name}"
            )
            response.raise_for_status()
            await log.ainfo("snapshot_deleted", collection=collection, snapshot=snapshot_name)

    async def download_snapshot(
        self,
        collection: str,
        snapshot_name: str,
        destination: str,
    ) -> str:
        """Download a snapshot to local path."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=600.0,
        ) as client:
            async with client.stream(
                "GET",
                f"/collections/{collection}/snapshots/{snapshot_name}",
            ) as response:
                response.raise_for_status()
                path = Path(destination)
                path.parent.mkdir(parents=True, exist_ok=True)

                with path.open("wb") as f:
                    async for chunk in response.aiter_bytes(chunk_size=8192):
                        f.write(chunk)

        await log.ainfo(
            "snapshot_downloaded",
            collection=collection,
            snapshot=snapshot_name,
            destination=destination,
        )
        return destination

    async def recover_from_snapshot(self, collection: str, snapshot_path: str) -> None:
        """Recover a collection from a snapshot file."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=600.0,
        ) as client:
            with Path(snapshot_path).open("rb") as f:
                response = await client.post(
                    f"/collections/{collection}/snapshots/upload",
                    content=f.read(),
                    headers={"Content-Type": "application/octet-stream"},
                )
            response.raise_for_status()
            await log.ainfo("snapshot_recovered", collection=collection, snapshot=snapshot_path)

    async def get_collection_info(self, collection: str) -> dict:
        """Get collection info including point count and status."""
        async with httpx.AsyncClient(
            base_url=self.endpoint,
            headers=self.headers(),
            timeout=self.timeout,
        ) as client:
            response = await client.get(f"/collections/{collection}")
            response.raise_for_status()
            return response.json()["result"]

    async def health_check(self) -> bool:
        """Check if Qdrant is healthy and ready."""
        try:
            async with httpx.AsyncClient(
                base_url=self.endpoint,
                timeout=5.0,
            ) as client:
                response = await client.get("/healthz")
                return response.status_code == 200
        except httpx.RequestError:
            return False

    def headers(self) -> dict[str, str]:
        """Build request headers."""
        if self.api_key:
            return {"api-key": self.api_key}
        return {}
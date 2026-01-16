"""S3-compatible object storage adapter."""

from dataclasses import dataclass
from pathlib import Path

import aioboto3
import structlog

from qdrant_operator.domain import S3StorageSpec

log = structlog.get_logger()


@dataclass
class S3Adapter:
    """Adapter for S3-compatible object storage operations."""

    async def upload_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        local_path: str,
        remote_key: str,
    ) -> str:
        """Upload a file to storage."""
        session = aioboto3.Session(
            aws_access_key_id=credentials[0],
            aws_secret_access_key=credentials[1],
            region_name=storage.region,
        )

        async with session.client("s3", **self.client_config(storage)) as s3:
            await s3.upload_file(local_path, storage.bucket, remote_key)

        full_path = f"s3://{storage.bucket}/{remote_key}"
        await log.ainfo("s3_upload_complete", path=full_path)
        return full_path

    async def download_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
        local_path: str,
    ) -> str:
        """Download a file from storage."""
        session = aioboto3.Session(
            aws_access_key_id=credentials[0],
            aws_secret_access_key=credentials[1],
            region_name=storage.region,
        )

        Path(local_path).parent.mkdir(parents=True, exist_ok=True)

        async with session.client("s3", **self.client_config(storage)) as s3:
            await s3.download_file(storage.bucket, remote_key, local_path)

        await log.ainfo("s3_download_complete", key=remote_key, local=local_path)
        return local_path

    async def delete_file(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
    ) -> None:
        """Delete a file from storage."""
        session = aioboto3.Session(
            aws_access_key_id=credentials[0],
            aws_secret_access_key=credentials[1],
            region_name=storage.region,
        )

        async with session.client("s3", **self.client_config(storage)) as s3:
            await s3.delete_object(Bucket=storage.bucket, Key=remote_key)

        await log.ainfo("s3_delete_complete", key=remote_key)

    async def list_files(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        prefix: str,
    ) -> list[str]:
        """List files under a prefix."""
        session = aioboto3.Session(
            aws_access_key_id=credentials[0],
            aws_secret_access_key=credentials[1],
            region_name=storage.region,
        )

        files: list[str] = []

        async with session.client("s3", **self.client_config(storage)) as s3:
            paginator = s3.get_paginator("list_objects_v2")
            async for page in paginator.paginate(Bucket=storage.bucket, Prefix=prefix):
                for obj in page.get("Contents", []):
                    files.append(obj["Key"])

        await log.ainfo("s3_list_complete", prefix=prefix, count=len(files))
        return files

    async def file_exists(
        self,
        storage: S3StorageSpec,
        credentials: tuple[str, str],
        remote_key: str,
    ) -> bool:
        """Check if a file exists in storage."""
        session = aioboto3.Session(
            aws_access_key_id=credentials[0],
            aws_secret_access_key=credentials[1],
            region_name=storage.region,
        )

        async with session.client("s3", **self.client_config(storage)) as s3:
            try:
                await s3.head_object(Bucket=storage.bucket, Key=remote_key)
                return True
            except Exception:
                return False

    def client_config(self, storage: S3StorageSpec) -> dict:
        """Build boto3 client configuration."""
        config: dict = {}
        if storage.endpoint:
            config["endpoint_url"] = storage.endpoint
        if storage.force_path_style:
            config["config"] = {"s3": {"addressing_style": "path"}}
        return config
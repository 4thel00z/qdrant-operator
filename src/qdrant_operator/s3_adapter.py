"""S3-compatible object storage adapter built on aioboto3."""

from collections.abc import AsyncGenerator
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any

import aioboto3
from botocore.config import Config
from loguru import logger

from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec

DELETE_BATCH_SIZE = 1000


@dataclass
class ChunkReader:
    """Adapts an async byte iterator to the async `read(n)` aioboto3 expects."""

    chunks: AsyncIterator[bytes]
    buffer: bytearray
    exhausted: bool = False
    total: int = 0

    async def read(self, size: int) -> bytes:
        while len(self.buffer) < size and not self.exhausted:
            chunk = await anext(self.chunks, None)
            if chunk is None:
                self.exhausted = True
                break
            self.buffer.extend(chunk)
        data = bytes(self.buffer[:size])
        del self.buffer[:size]
        self.total += len(data)
        return data


@dataclass
class S3Adapter:
    @asynccontextmanager
    async def client(
        self, storage: S3StorageSpec, credentials: S3Credentials
    ) -> AsyncGenerator[Any]:
        session: Any = aioboto3.Session(
            aws_access_key_id=credentials.access_key_id,
            aws_secret_access_key=credentials.secret_access_key,
            region_name=storage.region,
        )
        addressing = {"addressing_style": "path"} if storage.force_path_style else {}
        async with session.client(
            "s3",
            endpoint_url=storage.endpoint,
            config=Config(s3=addressing),
        ) as client:
            yield client

    async def upload_stream(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        chunks: AsyncIterator[bytes],
    ) -> int:
        reader = ChunkReader(chunks, bytearray())
        async with self.client(storage, credentials) as s3:
            await s3.upload_fileobj(reader, storage.bucket, key)
        logger.info("s3 upload complete", uri=storage.uri(key), bytes=reader.total)
        return reader.total

    async def put_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str, data: bytes
    ) -> None:
        async with self.client(storage, credentials) as s3:
            await s3.put_object(Bucket=storage.bucket, Key=key, Body=data)

    async def get_object(
        self, storage: S3StorageSpec, credentials: S3Credentials, key: str
    ) -> bytes:
        async with self.client(storage, credentials) as s3:
            response = await s3.get_object(Bucket=storage.bucket, Key=key)
            async with response["Body"] as body:
                return await body.read()

    async def delete_prefix(
        self, storage: S3StorageSpec, credentials: S3Credentials, prefix: str
    ) -> int:
        deleted = 0
        async with self.client(storage, credentials) as s3:
            paginator = s3.get_paginator("list_objects_v2")
            async for page in paginator.paginate(Bucket=storage.bucket, Prefix=f"{prefix}/"):
                keys = [{"Key": item["Key"]} for item in page.get("Contents", [])]
                for start in range(0, len(keys), DELETE_BATCH_SIZE):
                    batch = keys[start : start + DELETE_BATCH_SIZE]
                    await s3.delete_objects(Bucket=storage.bucket, Delete={"Objects": batch})
                    deleted += len(batch)
        logger.info("s3 prefix deleted", uri=storage.uri(prefix), objects=deleted)
        return deleted

    async def presigned_get_url(
        self,
        storage: S3StorageSpec,
        credentials: S3Credentials,
        key: str,
        expires_seconds: int,
    ) -> str:
        async with self.client(storage, credentials) as s3:
            return await s3.generate_presigned_url(
                "get_object",
                Params={"Bucket": storage.bucket, "Key": key},
                ExpiresIn=expires_seconds,
            )

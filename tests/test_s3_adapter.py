from collections.abc import AsyncIterator

from qdrant_operator.s3_adapter import UPLOAD_PART_BYTES
from qdrant_operator.s3_adapter import ChunkReader

CHUNK = 1024 * 1024


async def chunks(count: int) -> AsyncIterator[bytes]:
    for _ in range(count):
        yield b"x" * CHUNK


async def test_chunk_reader_buffers_at_most_one_part_plus_one_chunk() -> None:
    reader = ChunkReader(chunks(40), bytearray())
    peak = 0
    read = 0

    while True:
        part = await reader.read(UPLOAD_PART_BYTES)
        peak = max(peak, len(reader.buffer))
        read += len(part)
        if not part:
            break

    assert read == 40 * CHUNK == reader.total
    assert peak < CHUNK

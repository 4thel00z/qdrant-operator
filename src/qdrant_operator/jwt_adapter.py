"""JWT adapter: signs Qdrant RBAC tokens (HS256, the algorithm Qdrant's parser accepts)."""

# pyright: reportUnknownMemberType=false

import warnings
from dataclasses import dataclass

from jwt import PyJWT
from jwt.warnings import InsecureKeyLengthWarning
from loguru import logger

from qdrant_operator.domain import JsonDict

ALGORITHM = "HS256"
RECOMMENDED_KEY_BYTES = 32


@dataclass
class JwtAdapter:
    def sign(self, claims: JsonDict, secret: str) -> str:
        if len(secret.encode()) < RECOMMENDED_KEY_BYTES:
            logger.warning(
                "cluster API key is shorter than the HS256 recommendation; "
                "use at least 32 random bytes",
                key_bytes=len(secret.encode()),
            )
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", InsecureKeyLengthWarning)
            return PyJWT().encode(claims, secret, algorithm=ALGORITHM)

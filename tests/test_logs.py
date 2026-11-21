import logging

import kopf
from loguru import logger

from qdrant_operator.logs import InterceptHandler
from qdrant_operator.logs import configure_logging


def test_configure_logging_replaces_the_handler_kopf_installs() -> None:
    root = logging.getLogger()
    saved = list(root.handlers)
    try:
        kopf.configure()
        assert len(root.handlers) >= 1

        configure_logging("INFO")

        assert [type(handler) for handler in root.handlers] == [InterceptHandler]
    finally:
        root.handlers[:] = saved
        logger.remove()


def test_configure_logging_is_idempotent() -> None:
    root = logging.getLogger()
    saved = list(root.handlers)
    try:
        configure_logging("INFO")
        configure_logging("INFO")

        assert len(root.handlers) == 1
    finally:
        root.handlers[:] = saved
        logger.remove()

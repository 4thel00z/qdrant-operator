"""Routes stdlib logging (kopf, httpx) into loguru so every line is emitted once."""

import logging
import sys

from loguru import logger

from qdrant_operator.settings import LogFormat
from qdrant_operator.settings import LoggingSettings

LEVELS = frozenset({"TRACE", "DEBUG", "INFO", "SUCCESS", "WARNING", "ERROR", "CRITICAL"})


class InterceptHandler(logging.Handler):
    def emit(self, record: logging.LogRecord) -> None:
        level = (
            logger.level(record.levelname).name if record.levelname in LEVELS else record.levelno
        )
        logger.opt(depth=6, exception=record.exc_info).log(level, record.getMessage())


def configure_logging(settings: LoggingSettings) -> None:
    """Replace every root handler, including the one kopf installs, with the loguru bridge."""
    logger.remove()
    logger.add(sys.stderr, level=settings.level, serialize=settings.format == LogFormat.JSON)
    logging.basicConfig(handlers=[InterceptHandler()], level=0, force=True)

"""Operator entry point: routes stdlib logging (kopf) into loguru and registers the handlers."""

import logging
import os
import sys

from kopf import cli
from loguru import logger

import qdrant_operator.handlers as handlers

LIVENESS_URL = "http://0.0.0.0:8080/healthz"


class InterceptHandler(logging.Handler):
    def emit(self, record: logging.LogRecord) -> None:
        level = (
            logger.level(record.levelname).name if record.levelname in LEVELS else record.levelno
        )
        logger.opt(depth=6, exception=record.exc_info).log(level, record.getMessage())


LEVELS = frozenset({"TRACE", "DEBUG", "INFO", "SUCCESS", "WARNING", "ERROR", "CRITICAL"})


def configure_logging(level: str) -> None:
    logger.remove()
    logger.add(sys.stderr, level=level, serialize=os.environ.get("LOG_FORMAT", "json") == "json")
    logging.basicConfig(handlers=[InterceptHandler()], level=0, force=True)


DEFAULT_ARGS = ("--all-namespaces", f"--liveness={LIVENESS_URL}")


def run(argv: list[str] | None = None) -> None:
    """Start the operator; extra arguments are passed to `kopf run` and override the defaults."""
    configure_logging(os.environ.get("LOG_LEVEL", "INFO"))
    extra = sys.argv[1:] if argv is None else argv
    cli.main(
        args=["run", "-m", handlers.__name__, *DEFAULT_ARGS, *extra],
        prog_name="qdrant-operator",
    )


configure_logging(os.environ.get("LOG_LEVEL", "INFO"))

if __name__ == "__main__":
    run()

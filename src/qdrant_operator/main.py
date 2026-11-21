"""Operator entry point: starts kopf with the handlers registered."""

import os
import sys

from kopf import cli

import qdrant_operator.handlers as handlers
from qdrant_operator.logs import configure_logging

LIVENESS_URL = "http://0.0.0.0:8080/healthz"
DEFAULT_ARGS = ("--all-namespaces", f"--liveness={LIVENESS_URL}")


def run(argv: list[str] | None = None) -> None:
    """Start the operator; extra arguments are passed to `kopf run` and override the defaults."""
    configure_logging(os.environ.get("LOG_LEVEL", "INFO"))
    extra = sys.argv[1:] if argv is None else argv
    cli.main(
        args=["run", "-m", handlers.__name__, *DEFAULT_ARGS, *extra],
        prog_name="qdrant-operator",
    )


if __name__ == "__main__":
    run()

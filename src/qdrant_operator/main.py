"""Operator entry point: reads settings once, builds the container and runs kopf with it as memo."""

import random

import kopf
import uvloop

from qdrant_operator.container import Container
from qdrant_operator.ha import peering_priority
from qdrant_operator.handlers import operator_settings
from qdrant_operator.kubernetes_adapter import load_configuration
from qdrant_operator.logs import configure_logging
from qdrant_operator.settings import Settings


async def serve(settings: Settings) -> None:
    container = Container.from_settings(settings, await load_configuration(settings.kubernetes))
    if settings.metrics.port:
        container.metrics.serve(settings.metrics.port)
    peering = settings.kopf.peering_name
    await kopf.operator(
        settings=operator_settings(settings.kopf),
        memo=container,
        clusterwide=not settings.kopf.namespaces,
        namespaces=settings.kopf.namespaces,
        liveness_endpoint=settings.kopf.liveness_endpoint,
        peering_name=peering or None,
        priority=peering_priority(settings.kopf.priority, random.SystemRandom()) if peering else 0,
    )


def run() -> None:
    settings = Settings()
    configure_logging(settings.logging)
    uvloop.run(serve(settings))


if __name__ == "__main__":
    run()

"""Replica coordination: kopf peering arguments and settings read from the environment."""

import random
from collections.abc import Mapping

import kopf

PEERING_NAME_ENV = "PEERING_NAME"
PEERING_PRIORITY_ENV = "PEERING_PRIORITY"
PEERING_LIFETIME_ENV = "PEERING_LIFETIME"
WATCH_NAMESPACES_ENV = "WATCH_NAMESPACES"
MAX_PRIORITY = 2**31 - 1


def peering_priority(environ: Mapping[str, str], rng: random.Random) -> int:
    """The configured priority, else a random one so that no two pods tie."""
    configured = environ.get(PEERING_PRIORITY_ENV, "")
    if configured:
        return int(configured)
    return rng.randint(1, MAX_PRIORITY)


def peering_args(environ: Mapping[str, str], rng: random.Random) -> list[str]:
    """`kopf run` arguments that join the named peering with this pod's priority."""
    name = environ.get(PEERING_NAME_ENV, "")
    if not name:
        return []
    return ["--peering", name, "--priority", str(peering_priority(environ, rng))]


def apply_peering_lifetime(settings: kopf.OperatorSettings, environ: Mapping[str, str]) -> None:
    configured = environ.get(PEERING_LIFETIME_ENV, "")
    if not configured:
        return
    settings.peering.lifetime = int(configured)


def namespace_args(environ: Mapping[str, str]) -> list[str]:
    """`--namespace` per entry of WATCH_NAMESPACES, else `--all-namespaces`."""
    names = [name.strip() for name in environ.get(WATCH_NAMESPACES_ENV, "").split(",")]
    watched = [name for name in names if name]
    if not watched:
        return ["--all-namespaces"]
    return [argument for name in watched for argument in ("--namespace", name)]

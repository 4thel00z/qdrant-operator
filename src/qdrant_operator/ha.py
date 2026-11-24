"""Replica coordination: the kopf peering priority of this pod."""

import random

MAX_PRIORITY = 2**31 - 1


def peering_priority(configured: int, rng: random.Random) -> int:
    """The configured priority, else a random one so that no two pods tie."""
    if configured:
        return configured
    return rng.randint(1, MAX_PRIORITY)

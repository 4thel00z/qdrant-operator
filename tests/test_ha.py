import random

from qdrant_operator.ha import MAX_PRIORITY
from qdrant_operator.ha import peering_priority


def test_priority_differs_per_pod() -> None:
    priorities = {peering_priority(0, random.Random(seed)) for seed in range(50)}

    assert len(priorities) == 50
    assert all(1 <= priority <= MAX_PRIORITY for priority in priorities)


def test_configured_priority_wins() -> None:
    assert peering_priority(42, random.Random(0)) == 42

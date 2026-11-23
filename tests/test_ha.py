import random

import kopf

from qdrant_operator.ha import MAX_PRIORITY
from qdrant_operator.ha import apply_peering_lifetime
from qdrant_operator.ha import namespace_args
from qdrant_operator.ha import peering_args
from qdrant_operator.ha import peering_priority


def test_no_peering_name_adds_no_arguments() -> None:
    assert peering_args({}, random.Random(0)) == []
    assert peering_args({"PEERING_PRIORITY": "5"}, random.Random(0)) == []


def test_peering_name_joins_the_peering_with_a_priority() -> None:
    args = peering_args({"PEERING_NAME": "operator"}, random.Random(0))

    assert args[:3] == ["--peering", "operator", "--priority"]
    assert 1 <= int(args[3]) <= MAX_PRIORITY


def test_priority_differs_per_pod() -> None:
    priorities = {peering_priority({}, random.Random(seed)) for seed in range(50)}

    assert len(priorities) == 50


def test_configured_priority_wins() -> None:
    assert peering_priority({"PEERING_PRIORITY": "42"}, random.Random(0)) == 42


def test_lifetime_is_applied_to_settings() -> None:
    settings = kopf.OperatorSettings()

    apply_peering_lifetime(settings, {"PEERING_LIFETIME": "20"})

    assert settings.peering.lifetime == 20


def test_lifetime_keeps_the_kopf_default_when_unset() -> None:
    settings = kopf.OperatorSettings()
    default = settings.peering.lifetime

    apply_peering_lifetime(settings, {})

    assert settings.peering.lifetime == default


def test_all_namespaces_by_default() -> None:
    assert namespace_args({}) == ["--all-namespaces"]
    assert namespace_args({"WATCH_NAMESPACES": " , "}) == ["--all-namespaces"]


def test_watched_namespaces_become_namespace_arguments() -> None:
    args = namespace_args({"WATCH_NAMESPACES": "a, b"})

    assert args == ["--namespace", "a", "--namespace", "b"]

import pytest

from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import VolumePlan
from qdrant_operator.domain import parse_quantity
from qdrant_operator.domain import plan_volumes
from qdrant_operator.domain import retry_delay
from qdrant_operator.domain import values_digest

META = {"name": "db", "namespace": "ns"}
SELECTOR = {"app.kubernetes.io/name": "qdrant", "app.kubernetes.io/instance": "qdrant-db"}


def spec(replicas: int = 3, **extra: object) -> ClusterSpec:
    return ClusterSpec.from_dict({"version": "1.16.3", "replicas": replicas, **extra}, META)


def test_pod_disruption_budget_defaults_on_for_replicated_clusters() -> None:
    assert spec(3).to_helm_values()["podDisruptionBudget"] == {
        "enabled": True,
        "maxUnavailable": 1,
    }
    assert spec(1).to_helm_values()["podDisruptionBudget"] == {
        "enabled": False,
        "maxUnavailable": 1,
    }


def test_pod_disruption_budget_can_be_forced_and_tuned() -> None:
    forced = spec(1, podDisruptionBudget={"enabled": True, "maxUnavailable": "25%"})
    off = spec(3, podDisruptionBudget={"enabled": False})
    min_available = spec(3, podDisruptionBudget={"minAvailable": 2})

    assert forced.to_helm_values()["podDisruptionBudget"] == {
        "enabled": True,
        "maxUnavailable": "25%",
    }
    assert off.to_helm_values()["podDisruptionBudget"]["enabled"] is False
    assert min_available.to_helm_values()["podDisruptionBudget"] == {
        "enabled": True,
        "minAvailable": 2,
        "maxUnavailable": None,
    }


def test_topology_spread_defaults_to_hostname_for_replicated_clusters() -> None:
    constraints = spec(3).to_helm_values()["topologySpreadConstraints"]

    assert constraints == [
        {
            "maxSkew": 1,
            "topologyKey": "kubernetes.io/hostname",
            "whenUnsatisfiable": "ScheduleAnyway",
            "labelSelector": {"matchLabels": SELECTOR},
        }
    ]
    assert spec(1).to_helm_values()["topologySpreadConstraints"] == []


def test_topology_spread_adds_zones_and_switches_hostname_off() -> None:
    both = spec(3, topologySpread={"zone": {"enabled": True, "maxSkew": 2}})
    zone_only = spec(
        3,
        topologySpread={
            "hostname": {"enabled": False},
            "zone": {"enabled": True, "whenUnsatisfiable": "DoNotSchedule"},
        },
    )

    keys = [c["topologyKey"] for c in both.to_helm_values()["topologySpreadConstraints"]]
    zone = zone_only.to_helm_values()["topologySpreadConstraints"]
    assert keys == ["kubernetes.io/hostname", "topology.kubernetes.io/zone"]
    assert [(c["topologyKey"], c["whenUnsatisfiable"]) for c in zone] == [
        ("topology.kubernetes.io/zone", "DoNotSchedule")
    ]


def test_explicit_topology_constraints_replace_the_generated_ones() -> None:
    explicit = spec(
        3,
        topologySpread={
            "constraints": [
                {"maxSkew": 1, "topologyKey": "rack", "whenUnsatisfiable": "DoNotSchedule"}
            ]
        },
    )
    disabled = spec(3, topologySpread={"constraints": []})

    assert explicit.to_helm_values()["topologySpreadConstraints"] == [
        {
            "maxSkew": 1,
            "topologyKey": "rack",
            "whenUnsatisfiable": "DoNotSchedule",
            "labelSelector": {"matchLabels": SELECTOR},
        }
    ]
    assert disabled.to_helm_values()["topologySpreadConstraints"] == []


def test_scaling_and_upgrade_knobs_default_to_off() -> None:
    plain = spec(3)
    tuned = spec(3, scaling={"drainOnScaleDown": True, "removeDeadPeers": True}, minReadySeconds=30)

    assert (plain.scaling.drain_on_scale_down, plain.scaling.remove_dead_peers) == (False, False)
    assert plain.to_helm_values()["minReadySeconds"] == 0
    assert (tuned.scaling.drain_on_scale_down, tuned.scaling.remove_dead_peers) == (True, True)
    assert tuned.to_helm_values()["minReadySeconds"] == 30


@pytest.mark.parametrize(
    ("text", "expected"),
    [("1Gi", 2**30), ("500M", 500 * 10**6), ("1.5Gi", 3 * 2**29), ("1024", 1024), ("2Ti", 2**41)],
)
def test_parse_quantity(text: str, expected: int) -> None:
    assert parse_quantity(text) == expected


def test_parse_quantity_rejects_garbage() -> None:
    with pytest.raises(ValueError, match="storage quantity"):
        parse_quantity("lots")


def test_claim_sizes_include_snapshots_only_when_enabled() -> None:
    assert spec(persistence={"size": "5Gi"}).claim_sizes() == {"qdrant-storage": "5Gi"}
    assert spec(snapshotPersistence={"enabled": True, "size": "50Gi"}).claim_sizes() == {
        "qdrant-storage": "10Gi",
        "qdrant-snapshots": "50Gi",
    }


def test_volume_plan_grows_and_recreates_on_larger_requests() -> None:
    plan = plan_volumes({"qdrant-storage": "40Gi"}, {"qdrant-storage": "5Gi"})

    assert plan == VolumePlan(grow=(("qdrant-storage", "40Gi"),), recreate=True)


def test_volume_plan_is_empty_when_templates_match_in_any_unit() -> None:
    assert plan_volumes({"qdrant-storage": "1024Mi"}, {"qdrant-storage": "1Gi"}) == VolumePlan()


def test_volume_plan_recreates_when_the_claim_set_changes() -> None:
    plan = plan_volumes(
        {"qdrant-storage": "10Gi", "qdrant-snapshots": "50Gi"}, {"qdrant-storage": "10Gi"}
    )

    assert plan == VolumePlan(grow=(), recreate=True)


def test_volume_plan_refuses_to_shrink() -> None:
    with pytest.raises(ValueError, match="qdrant-storage"):
        plan_volumes({"qdrant-storage": "5Gi"}, {"qdrant-storage": "10Gi"})


def test_values_digest_tracks_content_not_key_order() -> None:
    assert values_digest("1.0.0", {"a": 1, "b": 2}) == values_digest("1.0.0", {"b": 2, "a": 1})
    assert values_digest("1.0.0", {"a": 1}) != values_digest("1.0.1", {"a": 1})
    assert values_digest("1.0.0", {"a": 1}) != values_digest("1.0.0", {"a": 2})


def test_retry_delay_doubles_up_to_the_cap() -> None:
    assert [retry_delay(n) for n in (0, 1, 2, 3, 10)] == [30.0, 60.0, 120.0, 240.0, 600.0]

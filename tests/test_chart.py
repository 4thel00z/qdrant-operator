import shutil
import subprocess
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import pytest
import yaml

CHART = Path(__file__).resolve().parent.parent / "charts" / "qdrant-operator"

pytestmark = pytest.mark.skipif(not shutil.which("helm"), reason="helm is not installed")


def render(*sets: str) -> list[dict[str, Any]]:
    arguments = [argument for value in sets for argument in ("--set", value)]
    out = subprocess.run(
        ["helm", "template", "op", str(CHART), "--namespace", "ops", *arguments],
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [doc for doc in yaml.safe_load_all(out) if doc]


def of_kind(docs: Sequence[dict[str, Any]], kind: str) -> list[dict[str, Any]]:
    return [doc for doc in docs if doc["kind"] == kind]


def kopf_crds(docs: Sequence[dict[str, Any]]) -> list[str]:
    names = [doc["metadata"]["name"] for doc in of_kind(docs, "CustomResourceDefinition")]
    return [name for name in names if name.endswith("kopf.dev")]


def env(deployment: dict[str, Any]) -> dict[str, dict[str, Any]]:
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    return {item["name"]: item for item in container["env"]}


def test_default_is_one_replica_standalone() -> None:
    docs = render()
    deployment = of_kind(docs, "Deployment")[0]

    assert deployment["spec"]["replicas"] == 1
    assert deployment["spec"]["strategy"] == {"type": "Recreate"}
    assert not of_kind(docs, "ClusterKopfPeering")
    assert not of_kind(docs, "KopfPeering")
    assert not any(
        doc["kind"] == "CustomResourceDefinition" and "kopf" in doc["metadata"]["name"]
        for doc in docs
    )
    assert "PEERING_NAME" not in env(deployment)
    assert "WATCH_NAMESPACES" not in env(deployment)


def test_several_replicas_without_peering_are_refused() -> None:
    with pytest.raises(subprocess.CalledProcessError):
        render("replicaCount=2")


def test_cluster_wide_peering() -> None:
    docs = render("ha.enabled=true", "replicaCount=2", "ha.lifetime=30")
    deployment = of_kind(docs, "Deployment")[0]
    variables = env(deployment)

    assert deployment["spec"]["replicas"] == 2
    assert deployment["spec"]["strategy"]["type"] == "RollingUpdate"
    assert deployment["spec"]["strategy"]["rollingUpdate"] == {"maxSurge": 1, "maxUnavailable": 0}
    assert [
        crd["metadata"]["name"]
        for crd in of_kind(docs, "CustomResourceDefinition")
        if "kopf" in crd["metadata"]["name"]
    ] == ["clusterkopfpeerings.kopf.dev"]
    peering = of_kind(docs, "ClusterKopfPeering")
    assert [p["metadata"]["name"] for p in peering] == [variables["PEERING_NAME"]["value"]]
    assert variables["PEERING_LIFETIME"]["value"] == "30"
    assert variables["POD_ID"]["valueFrom"]["fieldRef"]["fieldPath"] == "metadata.name"
    assert "WATCH_NAMESPACES" not in variables
    assert not of_kind(docs, "KopfPeering")


def test_namespaced_peering() -> None:
    docs = render("ha.enabled=true", "replicaCount=2", "ha.namespaces={a,b}", "ha.peeringName=ops")
    deployment = of_kind(docs, "Deployment")[0]
    role = of_kind(docs, "ClusterRole")[0]

    assert env(deployment)["WATCH_NAMESPACES"]["value"] == "a,b"
    assert env(deployment)["PEERING_NAME"]["value"] == "ops"
    assert [
        (p["metadata"]["namespace"], p["metadata"]["name"]) for p in of_kind(docs, "KopfPeering")
    ] == [
        ("a", "ops"),
        ("b", "ops"),
    ]
    assert [
        crd["metadata"]["name"]
        for crd in of_kind(docs, "CustomResourceDefinition")
        if "kopf" in crd["metadata"]["name"]
    ] == ["kopfpeerings.kopf.dev"]
    assert not of_kind(docs, "ClusterKopfPeering")
    resources = [
        r for rule in role["rules"] if "kopf.dev" in rule["apiGroups"] for r in rule["resources"]
    ]
    assert "kopfpeerings" in resources

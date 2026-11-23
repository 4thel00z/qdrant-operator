import shutil
import subprocess
from collections.abc import Mapping
from pathlib import Path
from typing import Any
from typing import cast

import pytest
import yaml

CHART = Path(__file__).resolve().parent.parent / "charts" / "qdrant-operator"
Manifest = Mapping[str, Any]
Rule = Mapping[str, Any]

pytestmark = pytest.mark.skipif(not shutil.which("helm"), reason="helm is not installed")


def render(*sets: str) -> list[Manifest]:
    arguments = [argument for value in sets for argument in ("--set", value)]
    output = subprocess.run(
        ["helm", "template", "qdrant-operator", str(CHART), "--namespace", "qdrant-system"]
        + arguments,
        check=True,
        capture_output=True,
        text=True,
    ).stdout
    return [cast(Manifest, doc) for doc in yaml.safe_load_all(output) if doc]


def of_kind(manifests: list[Manifest], kind: str) -> list[Manifest]:
    return [m for m in manifests if m["kind"] == kind]


def grants(rules: list[Rule]) -> dict[str, set[str]]:
    granted: dict[str, set[str]] = {}
    for rule in rules:
        for resource in rule["resources"]:
            key = f"{rule['apiGroups'][0]}/{resource}"
            granted[key] = granted.get(key, set()) | set(rule["verbs"])
    return granted


def test_default_is_one_cluster_role_without_wildcards() -> None:
    manifests = render()
    (role,) = of_kind(manifests, "ClusterRole")
    granted = grants(role["rules"])

    assert not of_kind(manifests, "Role")
    assert not of_kind(manifests, "RoleBinding")
    assert all("*" not in verbs for verbs in granted.values())
    assert granted["apiextensions.k8s.io/customresourcedefinitions"] == {"list", "watch"}
    assert "kopf.dev/clusterkopfpeerings" in granted
    assert "kopf.dev/kopfpeerings" not in granted
    assert "/namespaces" not in granted
    assert "networking.k8s.io/ingresses" not in granted
    assert granted["/secrets"] == {"get", "list", "create", "update", "patch", "delete"}
    assert granted["qdrant.io/qdrantbackups"] == {
        "get",
        "list",
        "watch",
        "patch",
        "create",
        "delete",
    }
    assert granted["qdrant.io/qdrantclusters"] == {"get", "list", "watch", "patch"}


def env(manifests: list[Manifest]) -> dict[str, str]:
    (deployment,) = of_kind(manifests, "Deployment")
    container = deployment["spec"]["template"]["spec"]["containers"][0]
    return {e["name"]: e["value"] for e in container["env"]}


def test_default_deployment_has_no_watch_namespaces() -> None:
    assert "WATCH_NAMESPACES" not in env(render())


def test_watch_namespaces_moves_rules_into_roles() -> None:
    manifests = render("watchNamespaces={team-a,team-b}")
    (cluster_role,) = of_kind(manifests, "ClusterRole")
    default_rules = grants(cast(list[Rule], of_kind(render(), "ClusterRole")[0]["rules"]))
    roles = of_kind(manifests, "Role")
    bindings = of_kind(manifests, "RoleBinding")

    assert grants(cluster_role["rules"]) == {
        "apiextensions.k8s.io/customresourcedefinitions": {"list", "watch"}
    }
    assert sorted(r["metadata"]["namespace"] for r in roles) == ["team-a", "team-b"]
    assert sorted(b["metadata"]["namespace"] for b in bindings) == ["team-a", "team-b"]
    for role in roles:
        granted = grants(role["rules"])
        assert "kopf.dev/kopfpeerings" in granted
        assert "kopf.dev/clusterkopfpeerings" not in granted
        assert {k: v for k, v in granted.items() if "kopfpeerings" not in k} == {
            k: v
            for k, v in default_rules.items()
            if "kopfpeerings" not in k and "apiextensions" not in k
        }
    for binding in bindings:
        assert binding["roleRef"]["kind"] == "Role"
        assert binding["subjects"][0]["namespace"] == "qdrant-system"


def test_watch_namespaces_become_the_watch_namespaces_variable() -> None:
    assert env(render("watchNamespaces={team-a,team-b}"))["WATCH_NAMESPACES"] == "team-a,team-b"

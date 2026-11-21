import json
from dataclasses import dataclass
from dataclasses import field

import pytest

from qdrant_operator.helm_adapter import QDRANT_CHART_REPO
from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.helm_adapter import chart_arguments
from qdrant_operator.helm_adapter import chart_repo_from_env


@dataclass
class RecordingHelm(HelmAdapter):
    """The real adapter with the helm subprocess replaced by a recorder."""

    commands: list[tuple[str, ...]] = field(default_factory=list[tuple[str, ...]])
    inputs: list[bytes | None] = field(default_factory=list[bytes | None])

    async def run(self, *args: str, stdin: bytes | None = None) -> str:
        self.commands.append(args)
        self.inputs.append(stdin)
        if args[0] == "status":
            return json.dumps({"info": {"status": "deployed"}})
        return ""


def test_repo_defaults_to_public_qdrant_helm(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("QDRANT_CHART_REPO", raising=False)

    assert chart_repo_from_env() == QDRANT_CHART_REPO
    assert HelmAdapter().repo == QDRANT_CHART_REPO


def test_repo_comes_from_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("QDRANT_CHART_REPO", "https://charts.internal.example/qdrant")

    assert HelmAdapter().repo == "https://charts.internal.example/qdrant"


def test_empty_environment_value_falls_back_to_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("QDRANT_CHART_REPO", "")

    assert chart_repo_from_env() == QDRANT_CHART_REPO


def test_http_repository_uses_the_repo_flag() -> None:
    assert chart_arguments("qdrant", "https://mirror.example/charts") == [
        "qdrant",
        "--repo",
        "https://mirror.example/charts",
    ]


def test_oci_repository_embeds_the_chart_in_the_reference() -> None:
    assert chart_arguments("qdrant", "oci://registry.example/charts/") == [
        "oci://registry.example/charts/qdrant"
    ]


async def test_apply_passes_the_configured_repository_to_helm() -> None:
    helm = RecordingHelm(repo="https://mirror.example/charts")

    await helm.apply("qdrant-a", "ns", "1.16.3", {"replicaCount": 1})

    install = next(command for command in helm.commands if command[0] == "upgrade")
    assert install[install.index("--repo") + 1] == "https://mirror.example/charts"

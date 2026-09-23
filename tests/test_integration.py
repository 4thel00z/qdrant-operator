"""Integration tests against a real cluster. Enabled with QDRANT_OPERATOR_INTEGRATION=1.

Requires kubectl access, helm, and the CRDs applied (`make crds-install`).
"""

import asyncio
import os
import subprocess
from collections.abc import Iterator

import pytest

from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import QdrantNode
from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.kubernetes_adapter import KubernetesAdapter
from qdrant_operator.kubernetes_adapter import load_kubernetes_config
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.usecases import DeleteCluster
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import ReconcileCluster

pytestmark = [
    pytest.mark.integration,
    pytest.mark.skipif(
        not os.environ.get("QDRANT_OPERATOR_INTEGRATION"),
        reason="set QDRANT_OPERATOR_INTEGRATION=1 to run against a live cluster",
    ),
]

NAMESPACE = "qdrant-test"
CLUSTER = "test-qdrant"
LOCAL_PORT = 16333


@pytest.fixture(scope="module", autouse=True)
def namespace() -> Iterator[None]:
    subprocess.run(["kubectl", "create", "namespace", NAMESPACE], capture_output=True)
    yield
    subprocess.run(
        ["kubectl", "delete", "namespace", NAMESPACE, "--wait=false"], capture_output=True
    )


@pytest.fixture
def spec() -> ClusterSpec:
    return ClusterSpec.from_dict(
        {
            "version": "v1.16.3",
            "replicas": 1,
            "resources": {"requests": {"cpu": "100m", "memory": "256Mi"}},
            "persistence": {"size": "1Gi"},
        },
        {"name": CLUSTER, "namespace": NAMESPACE},
    )


async def test_reconcile_installs_release(spec: ClusterSpec) -> None:
    helm = HelmAdapter()
    status = await ReconcileCluster(helm).execute(spec, 1, ClusterStatus(ClusterPhase.PENDING))

    assert status.phase == ClusterPhase.PENDING
    assert await helm.release_exists(spec.release_name, NAMESPACE)


async def test_cluster_becomes_running(spec: ClusterSpec) -> None:
    await load_kubernetes_config()
    use_case = ObserveCluster(KubernetesAdapter())
    status = ClusterStatus(ClusterPhase.PENDING)
    for _ in range(60):
        status = await use_case.execute(spec, status)
        if status.phase == ClusterPhase.RUNNING:
            break
        await asyncio.sleep(5)
    assert status.phase == ClusterPhase.RUNNING


async def test_qdrant_answers_over_port_forward(spec: ClusterSpec) -> None:
    forward = subprocess.Popen(
        [
            "kubectl",
            "port-forward",
            "-n",
            NAMESPACE,
            f"svc/{spec.release_name}",
            f"{LOCAL_PORT}:6333",
        ],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        await asyncio.sleep(3)
        node = QdrantNode(f"http://localhost:{LOCAL_PORT}")
        adapter = QdrantAdapter()
        assert await adapter.ready(node)
        assert await adapter.list_collections(node) == []
    finally:
        forward.terminate()
        forward.wait()


async def test_delete_cluster_uninstalls_release(spec: ClusterSpec) -> None:
    helm = HelmAdapter()
    await DeleteCluster(helm).execute(spec)
    assert not await helm.release_exists(spec.release_name, NAMESPACE)

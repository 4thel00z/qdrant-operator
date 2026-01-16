"""Integration tests for qdrant-operator against a real Kubernetes cluster.

Requires:
- kubectl configured with cluster access
- Helm installed
- CRDs applied: kubectl apply -f manifests/crds/

Run with: uv run pytest tests/test_integration.py -v -s
"""

import asyncio
import subprocess
import time

import pytest

from qdrant_operator.domain import (
    ClusterPhase,
    ClusterSpec,
    PersistenceSpec,
    Resources,
    ResourceRequirements,
)
from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.usecases import ReconcileCluster, DeleteCluster


TEST_NAMESPACE = "qdrant-test"
TEST_CLUSTER_NAME = "test-qdrant"


@pytest.fixture(scope="module")
def event_loop():
    """Create event loop for async tests."""
    loop = asyncio.new_event_loop()
    yield loop
    loop.close()


@pytest.fixture(scope="module", autouse=True)
def setup_namespace():
    """Create test namespace before tests, clean up after."""
    subprocess.run(
        ["kubectl", "create", "namespace", TEST_NAMESPACE],
        capture_output=True,
    )
    yield
    subprocess.run(
        ["kubectl", "delete", "namespace", TEST_NAMESPACE, "--wait=false"],
        capture_output=True,
    )


@pytest.fixture
def cluster_spec() -> ClusterSpec:
    """Create a minimal ClusterSpec for testing."""
    return ClusterSpec(
        name=TEST_CLUSTER_NAME,
        namespace=TEST_NAMESPACE,
        replicas=1,
        version="1.12.4",
        resources=Resources(
            requests=ResourceRequirements(cpu="100m", memory="256Mi"),
            limits=ResourceRequirements(cpu="500m", memory="512Mi"),
        ),
        persistence=PersistenceSpec(
            size="1Gi",
            access_modes=("ReadWriteOnce",),
        ),
    )


@pytest.fixture
def helm_adapter() -> HelmAdapter:
    """Create HelmAdapter instance."""
    return HelmAdapter()


class MockKubernetesAdapter:
    """Mock Kubernetes adapter for testing."""

    async def get_secret_value(self, secret_ref) -> str:
        return "mock-secret-value"

    async def update_status(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
        status: dict,
    ) -> None:
        pass

    async def create_resource(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        body: dict,
    ) -> dict:
        return body

    async def get_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> dict | None:
        return None

    async def list_resources(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        label_selector: str | None = None,
    ) -> list[dict]:
        return []

    async def delete_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> None:
        pass

    async def get_service_endpoint(self, name: str, namespace: str, port: int = 6333) -> str:
        return f"http://{name}.{namespace}.svc.cluster.local:{port}"


@pytest.mark.integration
class TestClusterLifecycle:
    """Test QdrantCluster create/update/delete lifecycle."""

    @pytest.mark.asyncio
    async def test_create_cluster(self, cluster_spec: ClusterSpec, helm_adapter: HelmAdapter):
        """Test creating a QdrantCluster via Helm."""
        kubernetes = MockKubernetesAdapter()
        use_case = ReconcileCluster(helm=helm_adapter, kubernetes=kubernetes)

        result = await use_case.execute(cluster_spec)

        assert result.phase in (ClusterPhase.PENDING, ClusterPhase.UPGRADING)
        assert result.helm_release == f"qdrant-{TEST_CLUSTER_NAME}"
        assert result.replicas == 1

        release_status = await helm_adapter.get_release_status(
            f"qdrant-{TEST_CLUSTER_NAME}",
            TEST_NAMESPACE,
        )
        assert release_status

    @pytest.mark.asyncio
    async def test_cluster_becomes_ready(self, helm_adapter: HelmAdapter):
        """Test that the cluster eventually becomes ready."""
        release_name = f"qdrant-{TEST_CLUSTER_NAME}"

        for _ in range(60):
            result = subprocess.run(
                [
                    "kubectl", "get", "pods",
                    "-n", TEST_NAMESPACE,
                    "-l", f"app.kubernetes.io/instance={release_name}",
                    "-o", "jsonpath={.items[0].status.phase}",
                ],
                capture_output=True,
                text=True,
            )
            if result.stdout == "Running":
                break
            time.sleep(5)
        else:
            pytest.fail("Cluster pod did not become Running within timeout")

    @pytest.mark.asyncio
    async def test_qdrant_health_check(self):
        """Test Qdrant health endpoint via port-forward."""
        release_name = f"qdrant-{TEST_CLUSTER_NAME}"

        port_forward = subprocess.Popen(
            [
                "kubectl", "port-forward",
                "-n", TEST_NAMESPACE,
                f"svc/{release_name}",
                "16333:6333",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        try:
            await asyncio.sleep(3)

            adapter = QdrantAdapter(endpoint="http://localhost:16333")
            healthy = await adapter.health_check()

            assert healthy, "Qdrant health check failed"

        finally:
            port_forward.terminate()
            port_forward.wait()

    @pytest.mark.asyncio
    async def test_qdrant_list_collections(self):
        """Test listing collections on fresh Qdrant instance."""
        release_name = f"qdrant-{TEST_CLUSTER_NAME}"

        port_forward = subprocess.Popen(
            [
                "kubectl", "port-forward",
                "-n", TEST_NAMESPACE,
                f"svc/{release_name}",
                "16333:6333",
            ],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        try:
            await asyncio.sleep(3)

            adapter = QdrantAdapter(endpoint="http://localhost:16333")
            collections = await adapter.list_collections()

            assert collections == [], "Fresh Qdrant should have no collections"

        finally:
            port_forward.terminate()
            port_forward.wait()

    @pytest.mark.asyncio
    async def test_delete_cluster(self, cluster_spec: ClusterSpec, helm_adapter: HelmAdapter):
        """Test deleting a QdrantCluster."""
        use_case = DeleteCluster(helm=helm_adapter)

        await use_case.execute(cluster_spec)

        release_status = await helm_adapter.get_release_status(
            f"qdrant-{TEST_CLUSTER_NAME}",
            TEST_NAMESPACE,
        )
        assert not release_status


@pytest.mark.integration
class TestHelmAdapter:
    """Test HelmAdapter directly."""

    @pytest.mark.asyncio
    async def test_ensure_repo(self, helm_adapter: HelmAdapter):
        """Test adding Qdrant Helm repo."""
        await helm_adapter.ensure_repo()

        result = subprocess.run(
            ["helm", "repo", "list", "-o", "json"],
            capture_output=True,
            text=True,
        )
        assert "qdrant" in result.stdout

    @pytest.mark.asyncio
    async def test_get_nonexistent_release(self, helm_adapter: HelmAdapter):
        """Test getting status of non-existent release returns None."""
        result = await helm_adapter.get_release_status(
            "nonexistent-release",
            TEST_NAMESPACE,
        )
        assert not result
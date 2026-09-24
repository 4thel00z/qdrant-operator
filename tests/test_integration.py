"""Integration tests against a real cluster. Enabled with QDRANT_OPERATOR_INTEGRATION=1.

Requires kubectl access, helm, and the CRDs applied (`make crds-install`).
"""

import asyncio
import os
import subprocess
from collections.abc import AsyncGenerator
from collections.abc import Iterator
from contextlib import asynccontextmanager
from dataclasses import replace
from datetime import UTC
from datetime import datetime

import httpx
import pytest

from qdrant_operator.domain import COLLECTIONS
from qdrant_operator.domain import AccessKeySpec
from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import CollectionSpec
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import is_subset
from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.jwt_adapter import JwtAdapter
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
API_KEY_SECRET = "qdrant-test-key"
API_KEY = "integration-test-key-0123456789abcdef"


@pytest.fixture(scope="module", autouse=True)
def namespace() -> Iterator[None]:
    subprocess.run(["kubectl", "create", "namespace", NAMESPACE], capture_output=True)
    subprocess.run(
        [
            "kubectl",
            "create",
            "secret",
            "generic",
            API_KEY_SECRET,
            "-n",
            NAMESPACE,
            f"--from-literal=api-key={API_KEY}",
        ],
        capture_output=True,
    )
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
            "apiKey": {"secretRef": {"name": API_KEY_SECRET, "key": "api-key"}, "jwtRbac": True},
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


@asynccontextmanager
async def port_forward(spec: ClusterSpec) -> AsyncGenerator[QdrantNode]:
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
        yield QdrantNode(f"http://localhost:{LOCAL_PORT}", api_key=API_KEY)
    finally:
        forward.terminate()
        forward.wait()


async def test_qdrant_answers_over_port_forward(spec: ClusterSpec) -> None:
    async with port_forward(spec) as node:
        adapter = QdrantAdapter()
        assert await adapter.ready(node)
        assert await adapter.list_collections(node) == []


async def test_collection_calls_round_trip_against_real_qdrant(spec: ClusterSpec) -> None:
    """The bodies the domain derives are accepted by Qdrant and read back as the domain expects."""
    adapter = QdrantAdapter()
    collection = CollectionSpec.from_dict(
        {
            "clusterRef": {"name": CLUSTER},
            "vectors": [{"size": 2, "distance": "Dot", "onDisk": True}],
            "optimizers": {"indexing_threshold": 10000},
            "onDiskPayload": False,
            "payloadIndexes": [
                {"field": "city", "type": "keyword"},
                {"field": "body", "type": "text", "params": {"tokenizer": "word"}},
            ],
            "aliases": ["docs-live"],
        },
        {"name": "docs", "namespace": NAMESPACE},
    )
    async with port_forward(spec) as node:
        await adapter.create_collection(node, "docs", collection.create_body())
        info = await adapter.collection_info(node, "docs")
        assert is_subset(collection.immutable_config(), info["config"])
        assert is_subset(collection.mutable_config(), info["config"])

        for index in collection.payload_indexes:
            await adapter.create_payload_index(node, "docs", index.field_name, index.field_schema())
        schema = (await adapter.collection_info(node, "docs"))["payload_schema"]
        assert all(i.satisfied_by(schema[i.field_name]) for i in collection.payload_indexes)

        await adapter.update_aliases(
            node, [{"create_alias": {"collection_name": "docs", "alias_name": "docs-live"}}]
        )
        assert await adapter.list_aliases(node) == {"docs-live": "docs"}

        tuned = replace(collection, optimizers={"indexing_threshold": 5000})
        await adapter.update_collection(node, "docs", tuned.update_body(info["config"]))
        info = await adapter.collection_info(node, "docs")
        assert not is_subset(collection.mutable_config(), info["config"])
        assert is_subset(tuned.mutable_config(), info["config"])

        points = [{"id": i, "vector": [float(i), 1.0], "payload": {"n": i}} for i in range(1, 6)]
        await adapter.upsert_points(node, "docs", points)
        assert await adapter.count_points(node, "docs") == 5
        first, offset = await adapter.scroll_points(node, "docs", None, 3)
        rest, end = await adapter.scroll_points(node, "docs", offset, 3)
        assert (len(first), len(rest), end) == (3, 2, None)
        assert first[0]["payload"] == {"n": 1} and len(first[0]["vector"]) == 2

        await adapter.delete_payload_index(node, "docs", "city")
        assert "city" not in (await adapter.collection_info(node, "docs"))["payload_schema"]
        await adapter.delete_collection(node, "docs")
        assert await adapter.collection_exists(node, "docs") is False


async def test_custom_sharding_calls_round_trip_against_real_qdrant(spec: ClusterSpec) -> None:
    adapter = QdrantAdapter()
    sharded = CollectionSpec.from_dict(
        {
            "clusterRef": {"name": CLUSTER},
            "vectors": [{"size": 2, "distance": "Dot"}],
            "shardingMethod": "custom",
            "shardNumber": 1,
            "shardKeys": [{"key": "eu"}, {"key": 7}],
        },
        {"name": "regions", "namespace": NAMESPACE},
    )
    async with port_forward(spec) as node:
        await adapter.create_collection(node, "regions", sharded.create_body())
        assert await adapter.list_shard_keys(node, "regions") == []
        for shard_key in sharded.shard_keys:
            await adapter.create_shard_key(node, "regions", shard_key.to_body())
        assert sorted(map(str, await adapter.list_shard_keys(node, "regions"))) == ["7", "eu"]

        points = [{"id": i, "vector": [float(i), 1.0], "payload": {"n": i}} for i in (1, 2)]
        await adapter.upsert_points(node, "regions", points, shard_key="eu")
        await adapter.upsert_points(node, "regions", [{"id": 3, "vector": [3.0, 1.0]}], shard_key=7)
        assert await adapter.count_points(node, "regions") == 3
        page, _ = await adapter.scroll_points(node, "regions", None, 10)
        assert {p["id"]: p["shard_key"] for p in page} == {1: "eu", 2: "eu", 3: 7}
        await adapter.delete_collection(node, "regions")


async def test_jwt_signed_with_the_cluster_key_is_scoped_by_qdrant(spec: ClusterSpec) -> None:
    adapter = QdrantAdapter()
    access_key = AccessKeySpec.from_dict(
        {"clusterRef": {"name": CLUSTER}, "access": "r", "ttl": "1h"},
        {"name": "reader", "namespace": NAMESPACE},
    )
    token = JwtAdapter().sign(access_key.claims(datetime.now(UTC)), API_KEY)
    async with port_forward(spec) as node:
        reader = QdrantNode(node.url, api_key=token)
        assert await adapter.list_collections(reader) == []
        with pytest.raises(httpx.HTTPStatusError) as denied:
            await adapter.create_collection(
                reader, "forbidden", {"vectors": {"size": 2, "distance": "Dot"}}
            )
        assert denied.value.response.status_code == 403
        assert await adapter.list_collections(node) == []


async def test_delete_cluster_uninstalls_release(spec: ClusterSpec) -> None:
    helm = HelmAdapter()
    await DeleteCluster(helm).execute(spec)
    assert not await helm.release_exists(spec.release_name, NAMESPACE)


def dry_run_apply(manifest: str) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        ["kubectl", "apply", "--dry-run=server", "-n", NAMESPACE, "-f", "-"],
        input=manifest,
        capture_output=True,
        text=True,
    )


def test_admission_rejects_restore_without_a_source() -> None:
    result = dry_run_apply(
        "apiVersion: qdrant.io/v1alpha1\nkind: QdrantRestore\nmetadata:\n  name: no-source\n"
        "spec:\n  targetClusterRef:\n    name: db\n"
    )
    assert result.returncode != 0
    assert "exactly one of backupRef or source" in result.stderr


def test_admission_rejects_multi_replica_single_node_cluster() -> None:
    result = dry_run_apply(
        "apiVersion: qdrant.io/v1alpha1\nkind: QdrantCluster\nmetadata:\n  name: lonely\n"
        "spec:\n  version: v1.16.3\n  replicas: 3\n  cluster:\n    enabled: false\n"
    )
    assert result.returncode != 0
    assert "requires cluster.enabled" in result.stderr


def test_admission_accepts_a_valid_cluster() -> None:
    result = dry_run_apply(
        "apiVersion: qdrant.io/v1alpha1\nkind: QdrantCluster\nmetadata:\n  name: fine\n"
        "spec:\n  version: v1.16.3\n  replicas: 3\n"
    )
    assert result.returncode == 0, result.stderr


async def test_status_patch_reaches_a_real_custom_resource() -> None:
    """The adapter's status write must be a merge patch; the client defaults to JSON Patch."""
    subprocess.run(
        ["kubectl", "apply", "-n", NAMESPACE, "-f", "-"],
        input=(
            "apiVersion: qdrant.io/v1alpha1\nkind: QdrantCollection\nmetadata:\n"
            "  name: status-probe\nspec:\n  clusterRef:\n    name: nobody\n"
            "  vectors:\n    - {size: 2, distance: Dot}\n"
        ),
        capture_output=True,
        text=True,
        check=True,
    )
    await load_kubernetes_config()
    adapter = KubernetesAdapter()
    ref = ResourceRef(COLLECTIONS, "status-probe", NAMESPACE)

    await adapter.patch_status(ref, {"phase": "Pending", "collectionName": "probe"})
    await adapter.patch_status(ref, {"phase": "Ready"})
    body = await adapter.get_custom_resource(ref)

    assert body and body["status"] == {"phase": "Ready", "collectionName": "probe"}
    listed = await adapter.list_custom_resources(COLLECTIONS, NAMESPACE)
    assert [item["metadata"]["name"] for item in listed] == ["status-probe"]
    await adapter.delete_custom_resource(ref)
    assert await adapter.get_custom_resource(ref) is None

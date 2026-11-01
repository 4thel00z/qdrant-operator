"""End-to-end tests against the operator deployed by its chart. Enabled with QDRANT_OPERATOR_E2E=1.

Requires the chart installed in qdrant-system (`make e2e-image e2e-deploy`), kubectl and helm.
The tests run in file order and share one QdrantCluster; after a failure the namespace is kept
for inspection.
"""

import asyncio
import base64
import json
import os
import subprocess
import time
from collections.abc import AsyncGenerator
from collections.abc import Iterator
from contextlib import asynccontextmanager
from typing import Any

import pytest

from qdrant_operator.domain import ACCESS_KEYS
from qdrant_operator.domain import BACKUPS
from qdrant_operator.domain import CLUSTERS
from qdrant_operator.domain import COLLECTIONS
from qdrant_operator.domain import MANIFEST_KEY
from qdrant_operator.domain import RESTORES
from qdrant_operator.domain import TOKEN_SECRET_KEY
from qdrant_operator.domain import AccessKeyPhase
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import CollectionPhase
from qdrant_operator.domain import CredentialsSecretRef
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import S3Credentials
from qdrant_operator.domain import S3StorageSpec
from qdrant_operator.domain import join_key
from qdrant_operator.domain import release_name
from qdrant_operator.helm_adapter import HelmAdapter
from qdrant_operator.qdrant_adapter import QdrantAdapter
from qdrant_operator.s3_adapter import S3Adapter

pytestmark = [
    pytest.mark.e2e,
    pytest.mark.skipif(
        not os.environ.get("QDRANT_OPERATOR_E2E"),
        reason="set QDRANT_OPERATOR_E2E=1 to run against an operator deployed by its chart",
    ),
]

NAMESPACE = "qdrant-e2e"
OPERATOR_NAMESPACE = "qdrant-system"
OPERATOR_SELECTOR = "app.kubernetes.io/name=qdrant-operator"
CLUSTER = "store"
QDRANT_VERSION = "v1.16.3"
COLLECTION = "docs"
RESTORED_COLLECTION = "docs-restored"
ACCESS_KEY = "reader"
BACKUP = "first"
RESTORE = "docs-copy"
API_KEY_SECRET = "store-api-key"
API_KEY = "e2e-api-key-0123456789abcdef0123456789"
S3_SERVICE = "s3"
S3_IMAGE = "rustfs/rustfs:1.0.4"
S3_PORT = 9000
S3_SECRET = "s3-credentials"
S3_CREDENTIALS = S3Credentials("e2e-access-key", "e2e-secret-key-0123456789")
S3_ENDPOINT = f"http://{S3_SERVICE}.{NAMESPACE}.svc:{S3_PORT}"
BUCKET = "qdrant-backups"
PREFIX = "e2e"
QDRANT_LOCAL_PORT = 16343
S3_LOCAL_PORT = 19000
POINTS = 64
FAILED = "Failed"
TIMEOUT_SECONDS = 300.0
POLL_SECONDS = 3.0
PORT_FORWARD_TIMEOUT_SECONDS = 30.0
DELETE_ORDER = (RESTORES, ACCESS_KEYS, COLLECTIONS, BACKUPS, CLUSTERS)


def kubectl(*args: str, stdin: str | None = None) -> str:
    result = subprocess.run(["kubectl", *args], input=stdin, capture_output=True, text=True)
    if result.returncode:
        pytest.fail(f"kubectl {' '.join(args)} failed: {result.stderr}")
    return result.stdout


def apply(*manifests: JsonDict) -> None:
    items = {"apiVersion": "v1", "kind": "List", "items": list(manifests)}
    kubectl("apply", "-n", NAMESPACE, "-f", "-", stdin=json.dumps(items))


def custom_resource(kind: ResourceKind, name: str, spec: JsonDict) -> JsonDict:
    return {
        "apiVersion": f"{kind.group}/{kind.version}",
        "kind": kind.kind,
        "metadata": {"name": name},
        "spec": spec,
    }


def secret(name: str, data: dict[str, str]) -> JsonDict:
    return {"apiVersion": "v1", "kind": "Secret", "metadata": {"name": name}, "stringData": data}


def s3_server() -> list[JsonDict]:
    labels = {"app": S3_SERVICE}
    container: JsonDict = {
        "name": S3_SERVICE,
        "image": S3_IMAGE,
        "env": [
            {"name": "RUSTFS_ACCESS_KEY", "value": S3_CREDENTIALS.access_key_id},
            {"name": "RUSTFS_SECRET_KEY", "value": S3_CREDENTIALS.secret_access_key},
            {"name": "RUSTFS_OBS_LOG_DIRECTORY", "value": ""},
        ],
        "ports": [{"containerPort": S3_PORT}],
        "readinessProbe": {"tcpSocket": {"port": S3_PORT}, "periodSeconds": 2},
        "volumeMounts": [{"name": "data", "mountPath": "/data"}],
    }
    deployment: JsonDict = {
        "apiVersion": "apps/v1",
        "kind": "Deployment",
        "metadata": {"name": S3_SERVICE},
        "spec": {
            "selector": {"matchLabels": labels},
            "template": {
                "metadata": {"labels": labels},
                "spec": {
                    "containers": [container],
                    "volumes": [{"name": "data", "emptyDir": {}}],
                },
            },
        },
    }
    service: JsonDict = {
        "apiVersion": "v1",
        "kind": "Service",
        "metadata": {"name": S3_SERVICE},
        "spec": {"selector": labels, "ports": [{"port": S3_PORT}]},
    }
    return [deployment, service]


def fetch(kind: ResourceKind, name: str) -> JsonDict | None:
    output = kubectl("get", kind.plural, name, "-n", NAMESPACE, "-o", "json", "--ignore-not-found")
    if not output.strip():
        return None
    return json.loads(output)


def secret_value(name: str, key: str) -> str:
    encoded = kubectl("get", "secret", name, "-n", NAMESPACE, "-o", f"jsonpath={{.data.{key}}}")
    return base64.b64decode(encoded).decode()


async def wait_for_phase(kind: ResourceKind, name: str, phase: str) -> JsonDict:
    deadline = time.monotonic() + TIMEOUT_SECONDS
    status: JsonDict = {}
    while time.monotonic() < deadline:
        status = (fetch(kind, name) or {}).get("status", {})
        if status.get("phase") == phase:
            return status
        if status.get("phase") == FAILED:
            pytest.fail(f"{kind.kind} {name} failed: {status.get('error')}")
        await asyncio.sleep(POLL_SECONDS)
    pytest.fail(f"{kind.kind} {name} never reached {phase}; last status {status}")


async def delete(kind: ResourceKind, name: str) -> None:
    kubectl("delete", kind.plural, name, "-n", NAMESPACE, "--wait=false")
    deadline = time.monotonic() + TIMEOUT_SECONDS
    while time.monotonic() < deadline:
        if not fetch(kind, name):
            return
        await asyncio.sleep(POLL_SECONDS)
    pytest.fail(f"{kind.kind} {name} still exists; its finalizer never finished")


@asynccontextmanager
async def port_forward(service: str, local_port: int, remote_port: int) -> AsyncGenerator[str]:
    forward = await asyncio.create_subprocess_exec(
        "kubectl",
        "port-forward",
        "-n",
        NAMESPACE,
        f"svc/{service}",
        f"{local_port}:{remote_port}",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.DEVNULL,
    )
    try:
        assert forward.stdout
        line = await asyncio.wait_for(forward.stdout.readline(), PORT_FORWARD_TIMEOUT_SECONDS)
        assert line.startswith(b"Forwarding from"), f"port-forward to {service} failed"
        yield f"http://127.0.0.1:{local_port}"
    finally:
        forward.terminate()
        await forward.wait()


@asynccontextmanager
async def qdrant() -> AsyncGenerator[QdrantNode]:
    async with port_forward(release_name(CLUSTER), QDRANT_LOCAL_PORT, 6333) as url:
        yield QdrantNode(url, api_key=API_KEY)


@asynccontextmanager
async def bucket() -> AsyncGenerator[Any]:
    async with port_forward(S3_SERVICE, S3_LOCAL_PORT, S3_PORT) as url:
        storage = S3StorageSpec(
            bucket=BUCKET,
            credentials_secret_ref=CredentialsSecretRef(S3_SECRET, NAMESPACE),
            endpoint=url,
            force_path_style=True,
        )
        async with S3Adapter().client(storage, S3_CREDENTIALS) as client:
            yield client


@pytest.fixture(scope="module", autouse=True)
def namespace(request: pytest.FixtureRequest) -> Iterator[None]:
    kubectl("create", "namespace", NAMESPACE)
    apply(
        secret(API_KEY_SECRET, {"api-key": API_KEY}),
        secret(
            S3_SECRET,
            {
                "AWS_ACCESS_KEY_ID": S3_CREDENTIALS.access_key_id,
                "AWS_SECRET_ACCESS_KEY": S3_CREDENTIALS.secret_access_key,
            },
        ),
        *s3_server(),
    )
    kubectl("rollout", "status", "-n", NAMESPACE, f"deployment/{S3_SERVICE}", "--timeout=300s")
    yield
    if request.session.testsfailed:
        return
    for kind in DELETE_ORDER:
        subprocess.run(
            ["kubectl", "delete", kind.plural, "--all", "-n", NAMESPACE, "--timeout=120s"],
            capture_output=True,
        )
    subprocess.run(
        ["kubectl", "delete", "namespace", NAMESPACE, "--timeout=180s"], capture_output=True
    )


async def test_cluster_becomes_running() -> None:
    apply(
        custom_resource(
            CLUSTERS,
            CLUSTER,
            {
                "version": QDRANT_VERSION,
                "replicas": 1,
                "resources": {"requests": {"cpu": "100m", "memory": "256Mi"}},
                "persistence": {"size": "1Gi"},
                "apiKey": {
                    "secretRef": {"name": API_KEY_SECRET, "key": "api-key"},
                    "jwtRbac": True,
                },
            },
        )
    )
    status = await wait_for_phase(CLUSTERS, CLUSTER, ClusterPhase.RUNNING)
    assert status["readyReplicas"] == 1


async def test_collection_is_created_with_its_payload_index() -> None:
    apply(
        custom_resource(
            COLLECTIONS,
            COLLECTION,
            {
                "clusterRef": {"name": CLUSTER},
                "vectors": [{"size": 4, "distance": "Dot"}],
                "payloadIndexes": [{"field": "group", "type": "keyword"}],
                "deletionPolicy": "Delete",
            },
        )
    )
    await wait_for_phase(COLLECTIONS, COLLECTION, CollectionPhase.READY)
    async with qdrant() as node:
        info = await QdrantAdapter().collection_info(node, COLLECTION)
    assert "group" in info["payload_schema"]


async def test_access_key_token_is_accepted_by_qdrant() -> None:
    apply(
        custom_resource(
            ACCESS_KEYS, ACCESS_KEY, {"clusterRef": {"name": CLUSTER}, "access": "r", "ttl": "1h"}
        )
    )
    await wait_for_phase(ACCESS_KEYS, ACCESS_KEY, AccessKeyPhase.READY)
    token = secret_value(ACCESS_KEY, TOKEN_SECRET_KEY)
    async with qdrant() as node:
        reader = QdrantNode(node.url, api_key=token)
        assert await QdrantAdapter().list_collections(reader) == [COLLECTION]


async def test_backup_streams_snapshots_and_manifest_into_the_bucket() -> None:
    points = [
        {"id": i, "vector": [float(i), 1.0, 0.5, 0.25], "payload": {"group": f"g{i % 4}"}}
        for i in range(1, POINTS + 1)
    ]
    async with qdrant() as node:
        await QdrantAdapter().upsert_points(node, COLLECTION, points)
    async with bucket() as client:
        await client.create_bucket(Bucket=BUCKET)

    apply(
        custom_resource(
            BACKUPS,
            BACKUP,
            {
                "clusterRef": {"name": CLUSTER},
                "storage": {
                    "s3": {
                        "bucket": BUCKET,
                        "prefix": PREFIX,
                        "endpoint": S3_ENDPOINT,
                        "forcePathStyle": True,
                        "credentialsSecretRef": {"name": S3_SECRET},
                    }
                },
            },
        )
    )
    await wait_for_phase(BACKUPS, BACKUP, BackupPhase.COMPLETED)

    async with bucket() as client:
        key = join_key(PREFIX, BACKUP, MANIFEST_KEY)
        response = await client.get_object(Bucket=BUCKET, Key=key)
        manifest = json.loads(await response["Body"].read())
    assert [c["name"] for c in manifest["collections"]] == [COLLECTION]
    assert manifest["nodeCount"] == 1


async def test_restore_recovers_the_points_into_a_new_collection() -> None:
    apply(
        custom_resource(
            RESTORES,
            RESTORE,
            {
                "backupRef": {"name": BACKUP},
                "targetClusterRef": {"name": CLUSTER},
                "collectionMapping": {COLLECTION: RESTORED_COLLECTION},
            },
        )
    )
    await wait_for_phase(RESTORES, RESTORE, RestorePhase.COMPLETED)
    async with qdrant() as node:
        assert await QdrantAdapter().count_points(node, RESTORED_COLLECTION) == POINTS


async def test_deleting_the_backup_removes_its_objects() -> None:
    await delete(BACKUPS, BACKUP)
    async with bucket() as client:
        listing = await client.list_objects_v2(Bucket=BUCKET, Prefix=f"{PREFIX}/{BACKUP}/")
    assert listing["KeyCount"] == 0


async def test_deleting_the_collection_drops_it() -> None:
    await delete(COLLECTIONS, COLLECTION)
    async with qdrant() as node:
        assert not await QdrantAdapter().collection_exists(node, COLLECTION)


async def test_deleting_the_cluster_uninstalls_its_release() -> None:
    await delete(ACCESS_KEYS, ACCESS_KEY)
    await delete(RESTORES, RESTORE)
    await delete(CLUSTERS, CLUSTER)
    assert not await HelmAdapter().release_exists(release_name(CLUSTER), NAMESPACE)


def test_operator_never_restarted() -> None:
    pods = json.loads(
        kubectl("get", "pods", "-n", OPERATOR_NAMESPACE, "-l", OPERATOR_SELECTOR, "-o", "json")
    )["items"]
    restarts = [s["restartCount"] for p in pods for s in p["status"]["containerStatuses"]]
    assert restarts == [0]


def test_operator_logged_no_permanent_failures() -> None:
    logs = kubectl("logs", "-n", OPERATOR_NAMESPACE, "-l", OPERATOR_SELECTOR, "--tail=-1")
    assert [line for line in logs.splitlines() if "failed permanently" in line] == []

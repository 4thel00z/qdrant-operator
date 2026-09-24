import json
import warnings
from dataclasses import replace
from datetime import UTC
from datetime import datetime
from datetime import timedelta

import pytest
from jwt import PyJWT
from jwt.warnings import InsecureKeyLengthWarning

from qdrant_operator.domain import BACKUPS
from qdrant_operator.domain import CLUSTERS
from qdrant_operator.domain import MIGRATIONS
from qdrant_operator.domain import RESTORES
from qdrant_operator.domain import SCHEDULES
from qdrant_operator.domain import AccessKeyPhase
from qdrant_operator.domain import AccessKeySpec
from qdrant_operator.domain import AccessKeyStatus
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupScheduleStatus
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import BackupStatus
from qdrant_operator.domain import ClusterPhase
from qdrant_operator.domain import ClusterRef
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import ClusterStatus
from qdrant_operator.domain import CollectionPhase
from qdrant_operator.domain import CollectionSpec
from qdrant_operator.domain import CollectionStatus
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import MigrationPhase
from qdrant_operator.domain import MigrationSpec
from qdrant_operator.domain import QdrantNode
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import RestorePhase
from qdrant_operator.domain import RestorePriority
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import SchedulePhase
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import format_time
from qdrant_operator.jwt_adapter import JwtAdapter
from qdrant_operator.usecases import ClusterNotFoundError
from qdrant_operator.usecases import DeleteBackupData
from qdrant_operator.usecases import DeleteCluster
from qdrant_operator.usecases import DeleteCollection
from qdrant_operator.usecases import ExecuteBackup
from qdrant_operator.usecases import ExecuteMigration
from qdrant_operator.usecases import ExecuteRestore
from qdrant_operator.usecases import ExpireBackup
from qdrant_operator.usecases import IssueAccessKey
from qdrant_operator.usecases import ObserveCluster
from qdrant_operator.usecases import ProcessSchedule
from qdrant_operator.usecases import ReconcileCluster
from qdrant_operator.usecases import ReconcileCollection
from qdrant_operator.usecases import ResolveCluster
from qdrant_operator.usecases import SourceNotReadyError
from tests.fakes import FakeHelm
from tests.fakes import FakeKubernetes
from tests.fakes import FakeQdrant
from tests.fakes import FakeStorage
from tests.fakes import snapshot_payload

NS = "tenant-a"
NOW = datetime(2026, 9, 23, 2, 30, tzinfo=UTC)
S3 = {"bucket": "bucket", "prefix": "backups", "credentialsSecretRef": {"name": "s3-creds"}}


def cluster_body(name: str = "db", replicas: int = 2, **spec: object) -> JsonDict:
    return {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantCluster",
        "metadata": {"name": name, "namespace": NS},
        "spec": {"version": "1.16.3", "replicas": replicas, **spec},
    }


def backup_spec(name: str = "nightly", **extra: object) -> BackupSpec:
    return BackupSpec.from_dict(
        {"clusterRef": {"name": "db"}, "storage": {"s3": S3}, **extra},
        {"name": name, "namespace": NS},
    )


def node_url(index: int, cluster: str = "db") -> str:
    return f"http://qdrant-{cluster}-{index}.qdrant-{cluster}-headless.{NS}.svc.cluster.local:6333"


def service_url(cluster: str = "db") -> str:
    return f"http://qdrant-{cluster}.{NS}.svc.cluster.local:6333"


@pytest.fixture
def kubernetes() -> FakeKubernetes:
    fake = FakeKubernetes(clock=NOW)
    fake.secrets[(NS, "s3-creds")] = {"AWS_ACCESS_KEY_ID": "AK", "AWS_SECRET_ACCESS_KEY": "SK"}
    fake.secrets[(NS, "api-keys")] = {"key": "topsecret"}
    fake.put_resource(cluster_body(apiKey={"secretRef": {"name": "api-keys", "key": "key"}}))
    return fake


@pytest.fixture
def qdrant() -> FakeQdrant:
    fake = FakeQdrant()
    for index in range(2):
        fake.add_collection(node_url(index), "docs", points=10)
        fake.add_collection(node_url(index), "images", points=5)
    fake.routes[service_url()] = node_url(0)
    return fake


async def test_reconcile_applies_release_and_reports_pending_then_upgrading() -> None:
    helm = FakeHelm()
    spec = ClusterSpec.from_dict(
        {"version": "v1.16.3", "replicas": 2}, {"name": "db", "namespace": NS}
    )
    use_case = ReconcileCluster(helm)

    first = await use_case.execute(spec, 1, ClusterStatus(ClusterPhase.PENDING))
    second = await use_case.execute(spec, 2, first)

    assert helm.releases[(NS, "qdrant-db")]["chartVersion"] == "1.16.3"
    assert helm.releases[(NS, "qdrant-db")]["values"]["replicaCount"] == 2
    assert (first.phase, second.phase) == (ClusterPhase.PENDING, ClusterPhase.UPGRADING)
    assert second.observed_generation == 2
    assert second.helm_release == "qdrant-db"
    assert [c.type for c in second.conditions] == ["Progressing"]


async def test_observe_lifts_statefulset_readiness_into_status(kubernetes: FakeKubernetes) -> None:
    spec = ClusterSpec.from_dict(
        {"version": "1.16.3", "replicas": 2}, {"name": "db", "namespace": NS}
    )
    use_case = ObserveCluster(kubernetes)

    waiting = await use_case.execute(spec, ClusterStatus(ClusterPhase.UPGRADING))
    kubernetes.statefulsets[(NS, "qdrant-db")] = StatefulSetStatus(replicas=2, ready_replicas=2)
    running = await use_case.execute(spec, waiting)

    assert (waiting.phase, waiting.ready_replicas) == (ClusterPhase.UPGRADING, 0)
    assert running.phase == ClusterPhase.RUNNING
    conditions = {c.type: c.status for c in running.conditions}
    assert conditions == {"Ready": ConditionStatus.TRUE, "Progressing": ConditionStatus.FALSE}


async def test_delete_cluster_only_uninstalls_existing_release() -> None:
    helm = FakeHelm()
    spec = ClusterSpec.from_dict({"version": "1.16.3"}, {"name": "db", "namespace": NS})
    await DeleteCluster(helm).execute(spec)
    await helm.apply("qdrant-db", NS, "1.16.3", {})
    await DeleteCluster(helm).execute(spec)
    assert helm.releases == {}


async def test_resolve_cluster_reads_api_key_and_lists_every_node(
    kubernetes: FakeKubernetes,
) -> None:
    connection = await ResolveCluster(kubernetes).execute(ClusterRef("db", NS))

    assert connection.service.api_key == "topsecret"
    assert [n.url for n in connection.nodes] == [node_url(0), node_url(1)]
    with pytest.raises(ClusterNotFoundError):
        await ResolveCluster(kubernetes).execute(ClusterRef("missing", NS))


async def test_backup_streams_every_node_snapshot_and_writes_manifest(
    kubernetes: FakeKubernetes, qdrant: FakeQdrant
) -> None:
    storage = FakeStorage()
    spec = backup_spec(retentionDays=7)
    ref = ResourceRef(BACKUPS, spec.name, NS)

    status = await ExecuteBackup(qdrant, storage, kubernetes).execute(spec, ref)

    assert status.phase == BackupPhase.COMPLETED
    assert status.s3_path == "s3://bucket/backups/nightly"
    assert status.completion_time is not None
    assert status.expires_at == status.completion_time + timedelta(days=7)
    assert kubernetes.status_patches[0][1]["phase"] == "InProgress"
    keys = storage.keys("bucket")
    assert "backups/nightly/manifest.json" in keys
    assert len([k for k in keys if k.endswith(".snapshot")]) == 4
    node0_docs = next(k for k in keys if "/docs/node-0/" in k)
    assert storage.objects[("bucket", node0_docs)] == snapshot_payload(node_url(0), "docs")
    assert qdrant.snapshots == {}
    manifest = json.loads(storage.objects[("bucket", "backups/nightly/manifest.json")])
    assert manifest["nodeCount"] == 2
    assert [c["name"] for c in manifest["collections"]] == ["docs", "images"]
    assert status.to_dict()["collections"][0]["snapshots"][1]["node"] == "node-1"


async def test_backup_marks_failed_collection_and_keeps_the_rest(
    kubernetes: FakeKubernetes, qdrant: FakeQdrant
) -> None:
    qdrant.failing_collections.add("images")
    storage = FakeStorage()
    spec = backup_spec(collections=["docs", "images"])

    status = await ExecuteBackup(qdrant, storage, kubernetes).execute(
        spec, ResourceRef(BACKUPS, spec.name, NS)
    )

    assert status.phase == BackupPhase.FAILED
    assert status.error == "snapshot of images refused"
    assert [c.status for c in status.collections] == ["Completed", "Failed"]
    manifest = json.loads(storage.objects[("bucket", "backups/nightly/manifest.json")])
    assert [c["name"] for c in manifest["collections"]] == ["docs"]


async def test_delete_backup_data_removes_only_that_backup_prefix(
    kubernetes: FakeKubernetes,
) -> None:
    storage = FakeStorage()
    storage.objects[("bucket", "backups/nightly/manifest.json")] = b"{}"
    storage.objects[("bucket", "backups/nightly/docs/node-0/a.snapshot")] = b"x"
    storage.objects[("bucket", "backups/nightly-2/manifest.json")] = b"{}"

    deleted = await DeleteBackupData(storage, kubernetes).execute(backup_spec())

    assert deleted == 2
    assert storage.keys("bucket") == ["backups/nightly-2/manifest.json"]


async def test_expire_backup_deletes_resource_once_past_expiry(kubernetes: FakeKubernetes) -> None:
    ref = ResourceRef(BACKUPS, "nightly", NS)
    kubernetes.put_resource(
        {"kind": "QdrantBackup", "metadata": {"name": "nightly", "namespace": NS}}
    )
    status = BackupStatus(BackupPhase.COMPLETED, expires_at=NOW + timedelta(hours=1))

    assert not await ExpireBackup(kubernetes).execute(ref, status, NOW)
    assert await ExpireBackup(kubernetes).execute(ref, status, NOW + timedelta(hours=2))
    assert kubernetes.deleted == [ref]


async def completed_backup(kubernetes: FakeKubernetes, qdrant: FakeQdrant) -> FakeStorage:
    storage = FakeStorage()
    spec = backup_spec()
    status = await ExecuteBackup(qdrant, storage, kubernetes).execute(
        spec, ResourceRef(BACKUPS, spec.name, NS)
    )
    kubernetes.put_resource(
        {
            "apiVersion": "qdrant.io/v1alpha1",
            "kind": "QdrantBackup",
            "metadata": {"name": spec.name, "namespace": NS},
            "spec": {"clusterRef": {"name": "db"}, "storage": {"s3": S3}},
            "status": status.to_dict(),
        }
    )
    return storage


async def test_restore_from_backup_ref_recovers_each_node_from_presigned_urls(
    kubernetes: FakeKubernetes, qdrant: FakeQdrant
) -> None:
    storage = await completed_backup(kubernetes, qdrant)
    kubernetes.put_resource(cluster_body(name="target"))
    qdrant.routes[service_url("target")] = node_url(0, "target")
    spec = RestoreSpec.from_dict(
        {
            "targetClusterRef": {"name": "target"},
            "backupRef": {"name": "nightly"},
            "collections": ["docs"],
            "collectionMapping": {"docs": "docs-restored"},
            "priority": "replica",
        },
        {"name": "rs", "namespace": NS},
    )

    status = await ExecuteRestore(qdrant, storage, kubernetes, indexing_poll_seconds=0).execute(
        spec, ResourceRef(RESTORES, "rs", NS)
    )

    assert status.phase == RestorePhase.COMPLETED
    assert status.source_backup == "nightly"
    assert status.progress.percentage == 100
    restored = status.restored_collections[0]
    assert (restored.name, restored.original_name, restored.points_count) == (
        "docs-restored",
        "docs",
        42,
    )
    assert len(qdrant.recovered) == 2
    assert sorted(r[0] for r in qdrant.recovered) == [node_url(0, "target"), node_url(1, "target")]
    assert all(
        r[1] == "docs-restored" and r[3] == RestorePriority.REPLICA for r in qdrant.recovered
    )
    assert all(
        "/docs/node-" in r[2] and r[2].startswith("https://presigned.test/bucket/")
        for r in qdrant.recovered
    )
    assert all(r[4] is not None for r in qdrant.recovered)
    phases = [p[1]["phase"] for p in kubernetes.status_patches if p[0].kind == RESTORES]
    assert phases == ["Downloading", "Restoring"]


async def test_restore_refuses_unfinished_backup_and_mismatched_node_count(
    kubernetes: FakeKubernetes, qdrant: FakeQdrant
) -> None:
    storage = await completed_backup(kubernetes, qdrant)
    use_case = ExecuteRestore(qdrant, storage, kubernetes, indexing_poll_seconds=0)
    meta = {"name": "rs", "namespace": NS}

    kubernetes.put_resource(cluster_body(name="small", replicas=1))
    spec = RestoreSpec.from_dict(
        {"targetClusterRef": {"name": "small"}, "backupRef": {"name": "nightly"}}, meta
    )
    with pytest.raises(ValueError, match="node counts must match"):
        await use_case.execute(spec, ResourceRef(RESTORES, "rs", NS))

    kubernetes.resources[("qdrantbackups", NS, "nightly")]["status"]["phase"] = "InProgress"
    with pytest.raises(SourceNotReadyError):
        await use_case.execute(spec, ResourceRef(RESTORES, "rs", NS))


async def test_restore_from_direct_s3_path(kubernetes: FakeKubernetes, qdrant: FakeQdrant) -> None:
    storage = await completed_backup(kubernetes, qdrant)
    spec = RestoreSpec.from_dict(
        {
            "targetClusterRef": {"name": "db"},
            "source": {"s3": {**S3, "path": "backups/nightly"}},
            "waitForIndexing": False,
        },
        {"name": "rs", "namespace": NS},
    )

    status = await ExecuteRestore(qdrant, storage, kubernetes).execute(
        spec, ResourceRef(RESTORES, "rs", NS)
    )

    assert status.phase == RestorePhase.COMPLETED
    assert status.source_backup == "s3://bucket/backups/nightly"
    assert sorted(r.name for r in status.restored_collections) == ["docs", "images"]


def schedule_spec(**extra: object) -> BackupScheduleSpec:
    return BackupScheduleSpec.from_dict(
        {"schedule": "0 2 * * *", "clusterRef": {"name": "db"}, "storage": {"s3": S3}, **extra},
        {"name": "sched", "namespace": NS},
    )


def schedule_owner(created: datetime = NOW - timedelta(days=1)) -> JsonDict:
    return {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantBackupSchedule",
        "metadata": {
            "name": "sched",
            "namespace": NS,
            "uid": "sched-uid",
            "creationTimestamp": format_time(created),
        },
    }


def spawned_backups(kubernetes: FakeKubernetes) -> list[JsonDict]:
    return [b for (plural, _, _), b in kubernetes.resources.items() if plural == "qdrantbackups"]


async def test_schedule_fires_missed_slot_once_and_records_it(kubernetes: FakeKubernetes) -> None:
    use_case = ProcessSchedule(kubernetes)
    spec = schedule_spec()

    first = await use_case.execute(
        spec, BackupScheduleStatus(SchedulePhase.ACTIVE), schedule_owner(), NOW
    )
    second = await use_case.execute(spec, first, schedule_owner(), NOW + timedelta(minutes=1))

    backups = spawned_backups(kubernetes)
    assert len(backups) == 1
    assert backups[0]["metadata"]["name"] == "sched-20260923-020000"
    assert backups[0]["metadata"]["ownerReferences"][0]["uid"] == "sched-uid"
    assert first.last_schedule_time == datetime(2026, 9, 23, 2, 0, tzinfo=UTC)
    assert first.active_backup == "sched-20260923-020000"
    assert first.next_backup_time == datetime(2026, 9, 24, 2, 0, tzinfo=UTC)
    assert second.active_backup == "sched-20260923-020000"
    assert second.last_backup_name is None


async def test_new_schedule_does_not_fire_slots_older_than_itself(
    kubernetes: FakeKubernetes,
) -> None:
    status = await ProcessSchedule(kubernetes).execute(
        schedule_spec(), BackupScheduleStatus(SchedulePhase.ACTIVE), schedule_owner(NOW), NOW
    )

    assert spawned_backups(kubernetes) == []
    assert status.last_schedule_time is None
    assert status.next_backup_time == datetime(2026, 9, 24, 2, 0, tzinfo=UTC)


async def test_schedule_concurrency_policies(kubernetes: FakeKubernetes) -> None:
    owner = schedule_owner()
    running = {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantBackup",
        "metadata": {
            "name": "sched-old",
            "namespace": NS,
            "labels": {"qdrant.io/schedule": "sched"},
        },
        "spec": {},
        "status": {"phase": "InProgress"},
    }
    kubernetes.put_resource(json.loads(json.dumps(running)))
    status = BackupScheduleStatus(SchedulePhase.ACTIVE)

    forbid = await ProcessSchedule(kubernetes).execute(schedule_spec(), status, owner, NOW)
    assert [b["metadata"]["name"] for b in spawned_backups(kubernetes)] == ["sched-old"]
    assert forbid.last_schedule_time is None
    assert forbid.active_backup == "sched-old"

    await ProcessSchedule(kubernetes).execute(
        schedule_spec(concurrencyPolicy="Allow"), status, owner, NOW
    )
    assert len(spawned_backups(kubernetes)) == 2

    kubernetes.put_resource(json.loads(json.dumps(running)))
    replaced = await ProcessSchedule(kubernetes).execute(
        schedule_spec(concurrencyPolicy="Replace"), status, owner, NOW
    )
    names = {b["metadata"]["name"] for b in spawned_backups(kubernetes)}
    assert "sched-old" not in names
    assert replaced.active_backup == "sched-20260923-020000"


async def test_schedule_applies_retention_and_suspend(kubernetes: FakeKubernetes) -> None:
    for days_ago in range(4):
        kubernetes.put_resource(
            {
                "apiVersion": "qdrant.io/v1alpha1",
                "kind": "QdrantBackup",
                "metadata": {
                    "name": f"sched-{days_ago}",
                    "namespace": NS,
                    "labels": {"qdrant.io/schedule": "sched"},
                    "creationTimestamp": format_time(NOW - timedelta(days=days_ago, hours=1)),
                },
                "spec": {},
                "status": {
                    "phase": "Completed",
                    "completionTime": format_time(NOW - timedelta(days=days_ago)),
                    "totalSize": "1.0MB",
                },
            }
        )
    spec = schedule_spec(retentionPolicy={"keepLast": 2}, suspend=True)

    status = await ProcessSchedule(kubernetes).execute(
        spec, BackupScheduleStatus(SchedulePhase.ACTIVE), schedule_owner(), NOW
    )

    assert status.phase == SchedulePhase.SUSPENDED
    assert sorted(b["metadata"]["name"] for b in spawned_backups(kubernetes)) == [
        "sched-0",
        "sched-1",
    ]
    assert [r.name for r in kubernetes.deleted] == ["sched-2", "sched-3"]
    assert status.last_backup_name == "sched-0"
    assert status.last_backup_status == "Completed"
    assert status.next_backup_time is None
    assert [b.name for b in status.recent_backups] == ["sched-0", "sched-1"]
    assert SCHEDULES.plural == "qdrantbackupschedules" and CLUSTERS.plural == "qdrantclusters"


def collection_spec(name: str = "docs", **spec: object) -> CollectionSpec:
    return CollectionSpec.from_dict(
        {
            "clusterRef": {"name": "db"},
            "vectors": [{"size": 4, "distance": "Cosine"}],
            **spec,
        },
        {"name": name, "namespace": NS},
    )


async def test_reconcile_collection_creates_collection_indexes_and_aliases(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    spec = collection_spec(
        name="articles",
        payloadIndexes=[
            {"field": "city", "type": "keyword"},
            {"field": "body", "type": "text", "params": {"tokenizer": "word"}},
        ],
        aliases=["articles-live"],
        replicationFactor=2,
    )
    use_case = ReconcileCollection(qdrant, kubernetes)

    status = await use_case.execute(spec, 1, CollectionStatus(CollectionPhase.PENDING))

    assert status.phase == CollectionPhase.READY
    assert (status.collection_name, status.health, status.observed_generation) == (
        "articles",
        "green",
        1,
    )
    assert status.payload_indexes == ("body", "city")
    assert status.aliases == ("articles-live",)
    assert qdrant.aliases == {"articles-live": "articles"}
    config = qdrant.collections[node_url(0)]["articles"]["config"]
    assert config["params"]["replication_factor"] == 2
    assert qdrant.updates == []
    assert [c.type for c in status.conditions] == ["Ready"]


async def test_reconcile_collection_is_idempotent_and_patches_only_mutable_drift(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    spec = collection_spec(optimizers={"indexing_threshold": 5000}, aliases=["docs-live"])
    use_case = ReconcileCollection(qdrant, kubernetes)

    first = await use_case.execute(spec, 1, CollectionStatus(CollectionPhase.PENDING))
    second = await use_case.execute(spec, 1, first)
    changed = replace(spec, replication_factor=2)
    third = await use_case.execute(changed, 2, second)

    assert qdrant.updates == [("docs", {"params": {"replication_factor": 2}})]
    assert qdrant.alias_changes == [
        [{"create_alias": {"collection_name": "docs", "alias_name": "docs-live"}}]
    ]
    assert third.observed_generation == 2
    assert second.conditions[0].last_transition_time == first.conditions[0].last_transition_time


async def test_reconcile_collection_reports_immutable_mismatch_without_touching_qdrant(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    qdrant.add_collection(node_url(0), "docs", points=7)
    spec = collection_spec(vectors=[{"size": 8, "distance": "Cosine"}], aliases=["docs-live"])

    status = await ReconcileCollection(qdrant, kubernetes).execute(
        spec, 3, CollectionStatus(CollectionPhase.PENDING)
    )

    assert status.phase == CollectionPhase.DEGRADED
    assert status.points_count == 7
    assert status.conditions[0].reason == "ImmutableFieldMismatch"
    assert status.error and "re-creating" in status.error
    assert qdrant.updates == [] and qdrant.alias_changes == []


async def test_reconcile_collection_drops_only_indexes_and_aliases_it_created(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    qdrant.add_collection(node_url(0), "docs")
    node = QdrantNode(node_url(0))
    await qdrant.create_payload_index(node, "docs", "handmade", "integer")
    await qdrant.create_payload_index(node, "docs", "city", "integer")
    qdrant.aliases["foreign"] = "other"
    qdrant.index_changes.clear()
    use_case = ReconcileCollection(qdrant, kubernetes)

    first = await use_case.execute(
        collection_spec(payloadIndexes=[{"field": "city", "type": "keyword"}], aliases=["a"]),
        1,
        CollectionStatus(CollectionPhase.PENDING),
    )
    second = await use_case.execute(collection_spec(aliases=["b"]), 2, first)

    assert qdrant.index_changes == [
        ("delete", "docs", "city"),
        ("create", "docs", "city"),
        ("delete", "docs", "city"),
    ]
    assert "handmade" in qdrant.collections[node_url(0)]["docs"]["payload_schema"]
    assert qdrant.aliases == {"foreign": "other", "b": "docs"}
    assert (first.payload_indexes, second.payload_indexes) == (("city",), ())
    assert (first.aliases, second.aliases) == (("a",), ("b",))


async def test_reconcile_collection_waits_for_cluster(kubernetes: FakeKubernetes) -> None:
    qdrant = FakeQdrant()
    use_case = ReconcileCollection(qdrant, kubernetes)

    with pytest.raises(ClusterNotFoundError):
        await use_case.execute(
            replace(collection_spec(), cluster_ref=ClusterRef("ghost", NS)),
            1,
            CollectionStatus(CollectionPhase.PENDING),
        )


async def test_delete_collection_honours_deletion_policy(kubernetes: FakeKubernetes) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    qdrant.add_collection(node_url(0), "docs")
    qdrant.aliases["docs-live"] = "docs"
    use_case = DeleteCollection(qdrant, kubernetes)

    retained = await use_case.execute(collection_spec())
    dropped = await use_case.execute(collection_spec(deletionPolicy="Delete"))
    gone = await use_case.execute(
        replace(collection_spec(deletionPolicy="Delete"), cluster_ref=ClusterRef("ghost", NS))
    )

    assert (retained, dropped, gone) == (False, True, False)
    assert qdrant.collections[node_url(0)] == {} and qdrant.aliases == {}


def access_key_body(name: str = "app-token", **spec: object) -> JsonDict:
    return {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantAccessKey",
        "metadata": {"name": name, "namespace": NS, "uid": "uid-app-token", "generation": 1},
        "spec": {
            "clusterRef": {"name": "db"},
            **({} if "collections" in spec else {"access": "r"}),
            **spec,
        },
    }


def decode_token(token: str, key: str) -> JsonDict:
    """Decode like Qdrant would, minus expiry (the test clock is fixed in the past)."""
    with warnings.catch_warnings():
        warnings.simplefilter("ignore", InsecureKeyLengthWarning)
        return PyJWT().decode(  # pyright: ignore[reportUnknownMemberType]
            token, key, algorithms=["HS256"], options={"verify_exp": False}
        )


async def test_issue_access_key_writes_signed_token_and_renews_on_time(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(
        cluster_body(apiKey={"secretRef": {"name": "api-keys", "key": "key"}, "jwtRbac": True})
    )
    owner = access_key_body(collections=[{"name": "docs", "access": "rw"}], ttl="3h")
    spec = AccessKeySpec.from_dict(owner["spec"], owner["metadata"])
    use_case = IssueAccessKey(kubernetes, JwtAdapter())
    pending = AccessKeyStatus(AccessKeyPhase.PENDING)

    issued = await use_case.execute(spec, 1, pending, owner, NOW)
    unchanged = await use_case.execute(spec, 1, issued, owner, NOW + timedelta(hours=1))
    renewed = await use_case.execute(spec, 1, issued, owner, NOW + timedelta(hours=2, minutes=1))

    secret = kubernetes.secrets[(NS, "app-token")]
    assert decode_token(secret["token"], "topsecret") == {
        "exp": int((NOW + timedelta(hours=2, minutes=1) + timedelta(hours=3)).timestamp()),
        "access": [{"collection": "docs", "access": "rw"}],
    }
    assert secret["url"] == service_url()
    assert kubernetes.secret_owners[(NS, "app-token")]["uid"] == "uid-app-token"
    assert issued.phase == AccessKeyPhase.READY
    assert (issued.expires_at, issued.renew_at) == (
        NOW + timedelta(hours=3),
        NOW + timedelta(hours=2),
    )
    assert unchanged == issued
    assert renewed.issued_at == NOW + timedelta(hours=2, minutes=1)
    assert renewed.conditions[0].last_transition_time == issued.conditions[0].last_transition_time


async def test_issue_access_key_resigns_when_cluster_key_rotates_or_secret_vanishes(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(
        cluster_body(apiKey={"secretRef": {"name": "api-keys", "key": "key"}, "jwtRbac": True})
    )
    owner = access_key_body(secretName="shared-token")
    spec = AccessKeySpec.from_dict(owner["spec"], owner["metadata"])
    use_case = IssueAccessKey(kubernetes, JwtAdapter())

    first = await use_case.execute(spec, 1, AccessKeyStatus(AccessKeyPhase.PENDING), owner, NOW)
    kubernetes.secrets[(NS, "api-keys")] = {"key": "rotated"}
    second = await use_case.execute(spec, 1, first, owner, NOW)
    del kubernetes.secrets[(NS, "shared-token")]
    third = await use_case.execute(spec, 1, second, owner, NOW)

    assert first.key_fingerprint != second.key_fingerprint
    assert decode_token(kubernetes.secrets[(NS, "shared-token")]["token"], "rotated") == {
        "access": "r"
    }
    assert third.secret_ref and third.secret_ref.name == "shared-token"
    assert first.expires_at is None


async def test_issue_access_key_waits_until_the_cluster_enables_jwt_rbac(
    kubernetes: FakeKubernetes,
) -> None:
    owner = access_key_body()
    spec = AccessKeySpec.from_dict(owner["spec"], owner["metadata"])
    use_case = IssueAccessKey(kubernetes, JwtAdapter())

    blocked = await use_case.execute(spec, 1, AccessKeyStatus(AccessKeyPhase.PENDING), owner, NOW)
    kubernetes.put_resource(cluster_body(apiKey={"jwtRbac": True, "autoGenerate": True}))
    with pytest.raises(KeyError):
        await use_case.execute(spec, 1, blocked, owner, NOW)
    with pytest.raises(ClusterNotFoundError):
        await use_case.execute(
            replace(spec, cluster_ref=ClusterRef("ghost", NS)), 1, blocked, owner, NOW
        )

    assert blocked.phase == AccessKeyPhase.PENDING
    assert blocked.conditions[0].reason == "JwtRbacDisabled"
    assert (NS, "app-token") not in kubernetes.secrets


def migration_spec(**spec: object) -> MigrationSpec:
    return MigrationSpec.from_dict(
        {
            "source": {"clusterRef": {"name": "db"}},
            "targetClusterRef": {"name": "target"},
            **spec,
        },
        {"name": "move", "namespace": NS},
    )


def sample_points(count: int, shard_keys: tuple[str, ...] = ()) -> list[JsonDict]:
    return [
        {
            "id": i,
            "vector": [float(i), 0.5],
            "payload": {"n": i},
            **({"shard_key": shard_keys[i % len(shard_keys)]} if shard_keys else {}),
        }
        for i in range(1, count + 1)
    ]


async def test_migration_copies_collections_in_batches_and_creates_targets(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(cluster_body(name="target", replicas=3))
    qdrant = FakeQdrant(
        routes={service_url(): node_url(0), service_url("target"): node_url(0, "target")}
    )
    body = {"vectors": {"size": 2, "distance": "Dot"}, "shard_number": 1, "replication_factor": 1}
    qdrant.add_collection(node_url(0), "docs", body=body)
    qdrant.add_collection(node_url(0), "logs", body=body)
    qdrant.add_points(node_url(0), "docs", sample_points(7))
    qdrant.add_points(node_url(0), "logs", sample_points(2))
    qdrant.collections[node_url(0, "target")] = {}
    node = QdrantNode(node_url(0))
    await qdrant.create_payload_index(node, "docs", "n", "integer")
    spec = migration_spec(
        batchSize=3, collectionMapping={"docs": "docs_v2"}, target={"replicationFactor": 2}
    )
    ref = ResourceRef(MIGRATIONS, "move", NS)

    status = await ExecuteMigration(qdrant, kubernetes).execute(spec, ref)

    target = node_url(0, "target")
    assert status.phase == MigrationPhase.COMPLETED
    assert status.source == f"{NS}/db"
    assert [
        (c.name, c.target_name, c.points_total, c.points_copied) for c in status.collections
    ] == [
        ("docs", "docs_v2", 7, 7),
        ("logs", "logs", 2, 2),
    ]
    assert status.progress.percentage == 100
    assert [p["id"] for p in qdrant.stored_points(target, "docs_v2")] == list(range(1, 8))
    assert qdrant.stored_points(target, "docs_v2")[0]["payload"] == {"n": 1}
    assert [n for c, _, n in qdrant.upserts if c == "docs_v2"] == [3, 3, 1]
    created = qdrant.collections[target]["docs_v2"]["config"]["params"]
    assert (created["vectors"], created["replication_factor"]) == (
        {"size": 2, "distance": "Dot"},
        2,
    )
    assert "n" in qdrant.collections[target]["docs_v2"]["payload_schema"]
    phases = [p["phase"] for _, p in kubernetes.status_patches]
    assert phases[0] == "Running" and len(phases) == 3


async def test_migration_groups_upserts_by_shard_key_and_reads_external_endpoints(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(cluster_body(name="target"))
    kubernetes.secrets[(NS, "cloud")] = {"api-key": "cloud-key"}
    cloud = "https://x.cloud.qdrant.io:6333"
    qdrant = FakeQdrant(routes={service_url("target"): node_url(0, "target")})
    qdrant.add_collection(cloud, "docs", body={"vectors": {"size": 2, "distance": "Dot"}})
    qdrant.add_points(cloud, "docs", sample_points(4, shard_keys=("eu", "us")))
    qdrant.add_collection(node_url(0, "target"), "docs")
    spec = migration_spec(
        source={"endpoint": {"url": cloud, "apiKeySecretRef": {"name": "cloud", "key": "api-key"}}},
        batchSize=10,
    )

    status = await ExecuteMigration(qdrant, kubernetes).execute(
        spec, ResourceRef(MIGRATIONS, "move", NS)
    )

    assert status.phase == MigrationPhase.COMPLETED
    assert status.source == cloud
    assert sorted(qdrant.upserts) == [("docs", "eu", 2), ("docs", "us", 2)]
    assert qdrant.stored_points(node_url(0, "target"), "docs")[0]["shard_key"] == "us"


async def test_migration_reports_per_collection_failures_and_missing_sources(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(cluster_body(name="target"))
    qdrant = FakeQdrant(
        routes={service_url(): node_url(0), service_url("target"): node_url(0, "target")}
    )
    qdrant.add_collection(node_url(0), "docs")
    qdrant.add_collection(node_url(0), "logs")
    qdrant.add_points(node_url(0), "logs", sample_points(1))
    qdrant.collections[node_url(0, "target")] = {}
    use_case = ExecuteMigration(qdrant, kubernetes)
    ref = ResourceRef(MIGRATIONS, "move", NS)

    status = await use_case.execute(migration_spec(createMissing=False), ref)
    with pytest.raises(ValueError, match="not present"):
        await use_case.execute(migration_spec(collections=["ghost"]), ref)
    with pytest.raises(ClusterNotFoundError):
        await use_case.execute(migration_spec(targetClusterRef={"name": "ghost"}), ref)

    assert status.phase == MigrationPhase.FAILED
    assert all(c.status == MigrationPhase.FAILED for c in status.collections)
    assert status.error and "createMissing is false" in status.error
    assert status.conditions[0].reason == "MigrationFailed"


async def test_issue_access_key_refuses_to_overwrite_a_foreign_secret(
    kubernetes: FakeKubernetes,
) -> None:
    kubernetes.put_resource(
        cluster_body(apiKey={"secretRef": {"name": "api-keys", "key": "key"}, "jwtRbac": True})
    )
    kubernetes.secrets[(NS, "app-token")] = {"password": "keep-me"}
    owner = access_key_body()
    spec = AccessKeySpec.from_dict(owner["spec"], owner["metadata"])

    status = await IssueAccessKey(kubernetes, JwtAdapter()).execute(
        spec, 1, AccessKeyStatus(AccessKeyPhase.PENDING), owner, NOW
    )

    assert status.phase == AccessKeyPhase.PENDING
    assert status.conditions[0].reason == "SecretNotOwned"
    assert kubernetes.secrets[(NS, "app-token")] == {"password": "keep-me"}


async def test_reconcile_collection_creates_missing_shard_keys_only(
    kubernetes: FakeKubernetes,
) -> None:
    qdrant = FakeQdrant(routes={service_url(): node_url(0)})
    spec = collection_spec(
        shardingMethod="custom", shardKeys=[{"key": "eu"}, {"key": "us", "shardsNumber": 2}]
    )
    use_case = ReconcileCollection(qdrant, kubernetes)

    first = await use_case.execute(spec, 1, CollectionStatus(CollectionPhase.PENDING))
    trimmed = await use_case.execute(
        collection_spec(shardingMethod="custom", shardKeys=[{"key": "eu"}]), 2, first
    )

    assert first.shard_keys == ("eu", "us")
    assert trimmed.shard_keys == ("eu", "us")
    assert qdrant.shard_key_bodies == [
        ("docs", {"shard_key": "eu"}),
        ("docs", {"shard_key": "us", "shards_number": 2}),
    ]

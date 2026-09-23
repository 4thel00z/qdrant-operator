from datetime import UTC
from datetime import datetime
from datetime import timedelta

import pytest

from qdrant_operator.domain import BackupManifest
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupRecord
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import ClusterRef
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import CollectionBackup
from qdrant_operator.domain import Condition
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RetentionPolicy
from qdrant_operator.domain import SnapshotRecord
from qdrant_operator.domain import chart_version
from qdrant_operator.domain import format_size
from qdrant_operator.domain import join_key
from qdrant_operator.domain import merge_dicts
from qdrant_operator.domain import set_condition

NOW = datetime(2026, 9, 23, 2, 30, tzinfo=UTC)
META = {"name": "demo", "namespace": "tenant-a"}


def test_join_key_drops_empty_segments_and_stray_slashes() -> None:
    assert join_key("", "backups/", "/demo", "coll") == "backups/demo/coll"
    assert join_key("", "demo") == "demo"


def test_format_size_keeps_fractions() -> None:
    assert format_size(0) == "0.0B"
    assert format_size(1536) == "1.5KB"
    assert format_size(3 * 1024**3) == "3.0GB"


def test_chart_version_strips_v_prefix() -> None:
    assert chart_version("v1.16.3") == "1.16.3"
    assert chart_version("1.16.3") == "1.16.3"


def test_cluster_spec_maps_every_declared_field_into_helm_values() -> None:
    spec = ClusterSpec.from_dict(
        {
            "version": "1.16.3",
            "replicas": 3,
            "image": {"repository": "mirror/qdrant", "pullPolicy": "Always"},
            "resources": {"requests": {"cpu": "1"}, "limits": {"memory": "4Gi"}},
            "persistence": {"size": "50Gi", "storageClassName": "fast"},
            "snapshotPersistence": {"enabled": True, "size": "20Gi"},
            "cluster": {"enabled": True, "p2p": {"port": 7000, "enableTls": True}},
            "service": {"type": "LoadBalancer", "annotations": {"a": "b"}},
            "apiKey": {"secretRef": {"name": "keys", "key": "api"}},
            "readOnlyApiKey": {"autoGenerate": True},
            "tls": {"enabled": True, "secretRef": {"name": "certs"}},
            "metrics": {"enabled": True, "serviceMonitor": {"enabled": True, "interval": "15s"}},
            "nodeSelector": {"pool": "db"},
            "tolerations": [{"key": "db", "operator": "Exists"}],
            "affinity": {"podAntiAffinity": {}},
            "config": {"storage": {"performance": {"max_search_threads": 4}}},
        },
        META,
    )
    values = spec.to_helm_values()

    assert values["replicaCount"] == 3
    assert values["image"] == {"repository": "mirror/qdrant", "pullPolicy": "Always"}
    assert values["resources"] == {"requests": {"cpu": "1"}, "limits": {"memory": "4Gi"}}
    assert values["persistence"]["storageClassName"] == "fast"
    assert values["snapshotPersistence"] == {"enabled": True, "size": "20Gi"}
    assert values["service"] == {"type": "LoadBalancer", "annotations": {"a": "b"}}
    assert values["apiKey"] == {"valueFrom": {"secretKeyRef": {"name": "keys", "key": "api"}}}
    assert values["readOnlyApiKey"] is True
    assert values["metrics"]["serviceMonitor"]["enabled"] is True
    assert values["metrics"]["serviceMonitor"]["scrapeInterval"] == "15s"
    assert values["nodeSelector"] == {"pool": "db"}
    assert values["tolerations"] == [{"key": "db", "operator": "Exists"}]
    assert values["config"]["cluster"]["p2p"] == {"port": 7000, "enable_tls": True}
    assert values["config"]["service"]["enable_tls"] is True
    assert values["config"]["tls"]["cert"] == "/qdrant/tls/tls.crt"
    assert values["config"]["storage"]["performance"]["max_search_threads"] == 4
    assert values["additionalVolumes"][0]["secret"]["secretName"] == "certs"
    assert spec.scheme == "https"


def test_cluster_spec_defaults_and_addresses() -> None:
    spec = ClusterSpec.from_dict({"version": "v1.16.3", "replicas": 2}, META)

    assert spec.api_key.to_helm_value() is False
    assert spec.service_url() == "http://qdrant-demo.tenant-a.svc.cluster.local:6333"
    assert spec.node_urls() == (
        "http://qdrant-demo-0.qdrant-demo-headless.tenant-a.svc.cluster.local:6333",
        "http://qdrant-demo-1.qdrant-demo-headless.tenant-a.svc.cluster.local:6333",
    )


def test_auto_generated_api_key_resolves_to_chart_secret() -> None:
    spec = ClusterSpec.from_dict({"version": "1.0.0", "apiKey": {"autoGenerate": True}}, META)
    ref = spec.api_key.resolve_secret_ref(spec.name, spec.namespace)

    assert ref is not None
    assert (ref.name, ref.key, ref.namespace) == ("qdrant-demo-apikey", "api-key", "tenant-a")


def test_backup_spec_secret_refs_live_in_the_resource_namespace() -> None:
    spec = BackupSpec.from_dict(
        {
            "clusterRef": {"name": "db"},
            "storage": {
                "s3": {
                    "bucket": "b",
                    "prefix": "backups/",
                    "credentialsSecretRef": {"name": "creds", "secretAccessKeyKey": "SECRET"},
                }
            },
        },
        META,
    )
    creds = spec.storage.credentials_secret_ref

    assert spec.cluster_ref == ClusterRef("db", "tenant-a")
    assert creds.access_key_ref().namespace == "tenant-a"
    assert creds.access_key_ref().key == "AWS_ACCESS_KEY_ID"
    assert creds.secret_key_ref().key == "SECRET"
    assert spec.root_key == "backups/demo"
    assert spec.snapshot_key("coll", 1, "s.snapshot") == "backups/demo/coll/node-1/s.snapshot"
    assert spec.storage.uri(spec.name) == "s3://b/backups/demo"


def test_restore_spec_requires_a_source_and_validates_selection() -> None:
    with pytest.raises(ValueError):
        RestoreSpec.from_dict({"targetClusterRef": {"name": "db"}}, META)

    spec = RestoreSpec.from_dict(
        {
            "targetClusterRef": {"name": "db"},
            "backupRef": {"name": "bk"},
            "collections": ["a"],
            "collectionMapping": {"a": "a2"},
        },
        META,
    )
    assert spec.target_name("a") == "a2"
    assert spec.select_collections(["a", "b"]) == ("a",)
    with pytest.raises(ValueError):
        spec.select_collections(["b"])


def record(name: str, days_ago: int) -> BackupRecord:
    return BackupRecord(name, NOW - timedelta(days=days_ago), BackupPhase.COMPLETED)


def test_retention_keeps_newest_and_one_per_daily_bucket() -> None:
    backups = [record(f"b{i}", i) for i in range(6)]

    assert RetentionPolicy().expired(backups) == []
    assert [b.name for b in RetentionPolicy(keep_last=2).expired(backups)] == [
        "b2",
        "b3",
        "b4",
        "b5",
    ]
    expired = RetentionPolicy(keep_last=1, keep_daily=3).expired(backups)
    assert [b.name for b in expired] == ["b3", "b4", "b5"]


def schedule(**extra: object) -> BackupScheduleSpec:
    return BackupScheduleSpec.from_dict(
        {
            "schedule": "0 2 * * *",
            "clusterRef": {"name": "db"},
            "storage": {"s3": {"bucket": "b", "credentialsSecretRef": {"name": "c"}}},
            **extra,
        },
        META,
    )


def test_schedule_due_follows_cronjob_semantics() -> None:
    slot = datetime(2026, 9, 23, 2, 0, tzinfo=UTC)

    assert schedule().due(NOW, None) == slot
    assert schedule().due(NOW, slot) is None
    assert schedule().due(NOW, slot - timedelta(days=1)) == slot
    assert schedule(startingDeadlineSeconds=60).due(NOW, None) is None
    assert schedule(startingDeadlineSeconds=3600).due(NOW, None) == slot
    assert schedule().next_slot(NOW) == slot + timedelta(days=1)


def test_schedule_spawns_owned_labelled_backup_without_null_collections() -> None:
    owner = {
        "apiVersion": "qdrant.io/v1alpha1",
        "kind": "QdrantBackupSchedule",
        "metadata": {"name": "demo", "uid": "u1"},
    }
    body = schedule().to_backup_resource("demo-20260923-020000", owner)

    assert body["metadata"]["labels"] == {"qdrant.io/schedule": "demo"}
    assert body["metadata"]["ownerReferences"][0]["uid"] == "u1"
    assert "collections" not in body["spec"]
    assert body["spec"]["storage"]["s3"]["credentialsSecretRef"]["name"] == "c"


def test_set_condition_keeps_transition_time_while_status_is_stable() -> None:
    earlier = NOW - timedelta(hours=1)
    ready = Condition("Ready", ConditionStatus.TRUE, earlier, "A", "a")

    same = set_condition([ready], Condition("Ready", ConditionStatus.TRUE, NOW, "B", "b"))
    assert same[0].last_transition_time == earlier
    assert same[0].reason == "B"

    flipped = set_condition([ready], Condition("Ready", ConditionStatus.FALSE, NOW, "C", "c"))
    assert flipped[0].last_transition_time == NOW


def test_merge_dicts_is_deep_and_later_layers_win() -> None:
    merged = merge_dicts({"a": {"x": 1, "y": 1}, "b": 1}, {"a": {"y": 2}, "c": 3})
    assert merged == {"a": {"x": 1, "y": 2}, "b": 1, "c": 3}


def test_manifest_round_trips_through_dict() -> None:
    manifest = BackupManifest(
        backup_name="demo",
        cluster=ClusterRef("db", "tenant-a"),
        node_count=2,
        created_at=NOW,
        collections=(
            CollectionBackup(
                "a", (SnapshotRecord(0, "k0", "s0", 10, "c0"), SnapshotRecord(1, "k1", "s1", 5))
            ),
        ),
    )
    restored = BackupManifest.from_dict(manifest.to_dict())

    assert restored == manifest
    assert restored.size_bytes == 15
    assert restored.collection_names == ("a",)

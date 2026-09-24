from dataclasses import replace
from datetime import UTC
from datetime import datetime
from datetime import timedelta

import pytest

from qdrant_operator.domain import AccessKeyPhase
from qdrant_operator.domain import AccessKeySpec
from qdrant_operator.domain import AccessKeyStatus
from qdrant_operator.domain import BackupManifest
from qdrant_operator.domain import BackupPhase
from qdrant_operator.domain import BackupRecord
from qdrant_operator.domain import BackupScheduleSpec
from qdrant_operator.domain import BackupSpec
from qdrant_operator.domain import ClusterRef
from qdrant_operator.domain import ClusterSpec
from qdrant_operator.domain import CollectionBackup
from qdrant_operator.domain import CollectionMigration
from qdrant_operator.domain import CollectionSpec
from qdrant_operator.domain import Condition
from qdrant_operator.domain import ConditionStatus
from qdrant_operator.domain import DeletionPolicy
from qdrant_operator.domain import MigrationPhase
from qdrant_operator.domain import MigrationProgress
from qdrant_operator.domain import MigrationSpec
from qdrant_operator.domain import PayloadIndexSpec
from qdrant_operator.domain import RestoreSpec
from qdrant_operator.domain import RetentionPolicy
from qdrant_operator.domain import SnapshotRecord
from qdrant_operator.domain import TargetOverrides
from qdrant_operator.domain import chart_version
from qdrant_operator.domain import create_body_from_config
from qdrant_operator.domain import format_size
from qdrant_operator.domain import group_by_shard_key
from qdrant_operator.domain import is_subset
from qdrant_operator.domain import join_key
from qdrant_operator.domain import merge_dicts
from qdrant_operator.domain import parse_duration
from qdrant_operator.domain import payload_indexes_from_schema
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


def collection_spec(**spec: object) -> CollectionSpec:
    return CollectionSpec.from_dict(
        {"clusterRef": {"name": "db"}, **spec}, {"name": "docs", "namespace": "tenant-a"}
    )


def test_collection_single_unnamed_vector_becomes_bare_params() -> None:
    spec = collection_spec(vectors=[{"size": 768, "distance": "Cosine", "onDisk": True}])

    assert spec.collection_name == "docs"
    assert spec.deletion_policy == DeletionPolicy.RETAIN
    assert spec.create_body() == {"vectors": {"size": 768, "distance": "Cosine", "on_disk": True}}
    assert spec.update_body() == {"vectors": {"": {"on_disk": True}}}
    assert spec.immutable_config() == {"params": {"vectors": {"size": 768, "distance": "Cosine"}}}


def test_collection_named_vectors_and_tuning_blocks_map_to_qdrant_names() -> None:
    spec = collection_spec(
        collectionName="Docs_v2",
        vectors=[
            {"name": "text", "size": 384, "distance": "Dot", "hnsw": {"m": 32}},
            {"name": "image", "size": 512, "distance": "Euclid", "datatype": "float16"},
        ],
        sparseVectors=[{"name": "bm25", "modifier": "idf", "index": {"on_disk": True}}],
        shardNumber=6,
        replicationFactor=2,
        onDiskPayload=False,
        optimizers={"indexing_threshold": 10000},
        quantization={"scalar": {"type": "int8"}},
        wal={"wal_capacity_mb": 64},
        strictMode={"enabled": True, "max_query_limit": 100},
    )

    assert spec.collection_name == "Docs_v2"
    body = spec.create_body()
    assert body["vectors"] == {
        "text": {"size": 384, "distance": "Dot", "hnsw_config": {"m": 32}},
        "image": {"size": 512, "distance": "Euclid", "datatype": "float16"},
    }
    assert body["sparse_vectors"] == {"bm25": {"modifier": "idf", "index": {"on_disk": True}}}
    assert (body["shard_number"], body["replication_factor"], body["on_disk_payload"]) == (
        6,
        2,
        False,
    )
    assert body["optimizers_config"] == {"indexing_threshold": 10000}
    assert body["wal_config"] == {"wal_capacity_mb": 64}
    assert "wal_config" not in spec.update_body()
    assert spec.update_body()["params"] == {"replication_factor": 2, "on_disk_payload": False}
    assert spec.mutable_config()["optimizer_config"] == {"indexing_threshold": 10000}
    assert spec.mutable_config()["params"]["vectors"] == {"text": {"hnsw_config": {"m": 32}}}


def test_collection_spec_rejects_mixed_unnamed_vectors_and_empty_specs() -> None:
    with pytest.raises(ValueError, match="unnamed"):
        collection_spec(
            vectors=[{"size": 4, "distance": "Dot"}, {"name": "b", "size": 4, "distance": "Dot"}]
        )
    with pytest.raises(ValueError, match="vectors"):
        collection_spec()


def test_is_subset_ignores_undeclared_keys_and_treats_null_as_false() -> None:
    actual = {"params": {"vectors": {"size": 4, "distance": "Dot", "on_disk": None}, "x": 1}}

    assert is_subset({"params": {"vectors": {"size": 4, "on_disk": False}}}, actual)
    assert not is_subset({"params": {"vectors": {"size": 8}}}, actual)
    assert not is_subset({"params": {"missing": {"a": 1}}}, actual)
    assert not is_subset({"params": {"vectors": {"on_disk": True}}}, actual)


def test_payload_index_schema_and_satisfaction() -> None:
    plain = PayloadIndexSpec.from_dict({"field": "city", "type": "keyword"})
    text = PayloadIndexSpec.from_dict(
        {"field": "body", "type": "text", "params": {"tokenizer": "word", "lowercase": True}}
    )

    assert plain.field_schema() == "keyword"
    assert text.field_schema() == {"type": "text", "tokenizer": "word", "lowercase": True}
    assert plain.satisfied_by({"data_type": "keyword", "params": None, "points": 3})
    assert not plain.satisfied_by({"data_type": "integer", "params": None, "points": 3})
    assert text.satisfied_by(
        {"data_type": "text", "params": {"type": "text", "tokenizer": "word", "lowercase": True}}
    )
    assert not text.satisfied_by({"data_type": "text", "params": {"tokenizer": "prefix"}})


def access_key_spec(**spec: object) -> AccessKeySpec:
    return AccessKeySpec.from_dict(
        {"clusterRef": {"name": "db"}, **spec}, {"name": "app-token", "namespace": "tenant-a"}
    )


def test_parse_duration_accepts_go_style_units() -> None:
    assert parse_duration("720h") == timedelta(hours=720)
    assert parse_duration("1h30m15s") == timedelta(hours=1, minutes=30, seconds=15)
    with pytest.raises(ValueError):
        parse_duration("30d")


def test_access_key_claims_match_qdrant_parser_layout() -> None:
    issued = datetime(2026, 9, 24, 20, 0, tzinfo=UTC)
    per_collection = access_key_spec(
        collections=[{"name": "docs", "access": "rw"}, {"name": "logs", "access": "r"}],
        subject="billing-service",
        ttl="720h",
        renewBefore="24h",
        valueExists={"collection": "tenants", "matches": [{"key": "id", "value": 42}]},
    )
    cluster_wide = access_key_spec(access="r")

    assert per_collection.claims(issued) == {
        "sub": "billing-service",
        "exp": int((issued + timedelta(hours=720)).timestamp()),
        "access": [
            {"collection": "docs", "access": "rw"},
            {"collection": "logs", "access": "r"},
        ],
        "value_exists": {"collection": "tenants", "matches": [{"key": "id", "value": 42}]},
    }
    assert per_collection.renew_at(issued) == issued + timedelta(hours=696)
    assert cluster_wide.claims(issued) == {"access": "r"}
    assert cluster_wide.expires_at(issued) is None and cluster_wide.renew_at(issued) is None
    assert cluster_wide.secret_name == "app-token"
    assert access_key_spec(access="m", ttl="90h").renew_at(issued) == issued + timedelta(hours=60)


def test_access_key_spec_needs_exactly_one_access_form() -> None:
    with pytest.raises(ValueError, match="exactly one"):
        access_key_spec()
    with pytest.raises(ValueError, match="exactly one"):
        access_key_spec(access="r", collections=[{"name": "docs", "access": "r"}])


def test_access_key_status_knows_when_a_token_is_current() -> None:
    now = datetime(2026, 9, 24, 20, 0, tzinfo=UTC)
    ready = AccessKeyStatus(
        phase=AccessKeyPhase.READY,
        key_fingerprint="abc",
        observed_generation=2,
        renew_at=now + timedelta(hours=1),
    )

    assert ready.token_current(2, "abc", True, now)
    assert not ready.token_current(3, "abc", True, now)
    assert not ready.token_current(2, "rotated", True, now)
    assert not ready.token_current(2, "abc", False, now)
    assert not ready.token_current(2, "abc", True, now + timedelta(hours=2))
    assert replace(ready, renew_at=None).token_current(2, "abc", True, now + timedelta(days=9))


def test_migration_create_body_leaves_shard_layout_to_the_target_unless_overridden() -> None:
    config = {
        "params": {
            "vectors": {"size": 4, "distance": "Cosine"},
            "shard_number": 1,
            "replication_factor": 1,
            "write_consistency_factor": 1,
            "on_disk_payload": True,
            "sparse_vectors": {"bm25": {"modifier": "idf"}},
        },
        "hnsw_config": {"m": 16},
        "optimizer_config": {"indexing_threshold": 20000},
        "wal_config": {"wal_capacity_mb": 32},
        "quantization_config": None,
        "strict_mode_config": None,
    }

    plain = create_body_from_config(config, TargetOverrides())
    sized = create_body_from_config(config, TargetOverrides(shard_number=6, replication_factor=2))

    assert plain == {
        "vectors": {"size": 4, "distance": "Cosine"},
        "sparse_vectors": {"bm25": {"modifier": "idf"}},
        "on_disk_payload": True,
        "hnsw_config": {"m": 16},
        "optimizers_config": {"indexing_threshold": 20000},
        "wal_config": {"wal_capacity_mb": 32},
    }
    assert (sized["shard_number"], sized["replication_factor"]) == (6, 2)
    assert "write_consistency_factor" not in sized


def test_migration_payload_schema_and_point_grouping() -> None:
    schema = {
        "city": {"data_type": "keyword", "params": None, "points": 3},
        "body": {"data_type": "text", "params": {"type": "text", "tokenizer": "word"}},
    }
    points = [
        {"id": 1, "vector": [0.1], "payload": {"a": 1}, "shard_key": "eu"},
        {"id": 2, "vector": {"text": [0.2]}, "payload": None, "shard_key": "us"},
        {"id": 3, "vector": [0.3], "shard_key": "eu"},
        {"id": "9f2c", "vector": [0.4]},
    ]

    assert payload_indexes_from_schema(schema) == [
        ("city", "keyword"),
        ("body", {"type": "text", "tokenizer": "word"}),
    ]
    assert group_by_shard_key(points) == {
        "eu": [{"id": 1, "vector": [0.1], "payload": {"a": 1}}, {"id": 3, "vector": [0.3]}],
        "us": [{"id": 2, "vector": {"text": [0.2]}}],
        None: [{"id": "9f2c", "vector": [0.4]}],
    }


def test_migration_spec_source_forms_and_progress() -> None:
    meta = {"name": "move", "namespace": "tenant-a"}
    from_cluster = MigrationSpec.from_dict(
        {
            "source": {"clusterRef": {"name": "old"}},
            "targetClusterRef": {"name": "new", "namespace": "tenant-b"},
            "collectionMapping": {"docs": "docs_v2"},
        },
        meta,
    )
    from_endpoint = MigrationSpec.from_dict(
        {
            "source": {
                "endpoint": {
                    "url": "https://x.cloud.qdrant.io:6333/",
                    "apiKeySecretRef": {"name": "cloud", "key": "api-key"},
                }
            },
            "targetClusterRef": {"name": "new"},
            "batchSize": 500,
        },
        meta,
    )
    with pytest.raises(ValueError, match="exactly one"):
        MigrationSpec.from_dict({"source": {}, "targetClusterRef": {"name": "new"}}, meta)

    assert from_cluster.source.description == "tenant-a/old"
    assert from_cluster.target_cluster_ref.namespace == "tenant-b"
    assert (from_cluster.target_name("docs"), from_cluster.target_name("logs")) == (
        "docs_v2",
        "logs",
    )
    assert from_endpoint.source.endpoint and from_endpoint.source.endpoint.url == (
        "https://x.cloud.qdrant.io:6333"
    )
    assert from_endpoint.source.description == "https://x.cloud.qdrant.io:6333"
    assert from_endpoint.batch_size == 500

    progress = MigrationProgress.of(
        [
            CollectionMigration("a", "a", MigrationPhase.COMPLETED, 100, 100),
            CollectionMigration("b", "b", MigrationPhase.RUNNING, 300, 50),
        ]
    )
    assert progress.to_dict() == {
        "collectionsTotal": 2,
        "collectionsCompleted": 1,
        "pointsTotal": 400,
        "pointsCopied": 150,
        "percentage": 38,
    }
    assert MigrationProgress.of([]).percentage == 100

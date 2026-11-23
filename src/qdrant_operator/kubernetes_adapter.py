"""Kubernetes API adapter. Client configuration is loaded once at operator startup."""

import asyncio
import base64
from collections.abc import AsyncGenerator
from collections.abc import Mapping
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any
from typing import cast

from kubernetes_asyncio import client
from kubernetes_asyncio import config as kubernetes_config
from kubernetes_asyncio.client.exceptions import ApiException
from loguru import logger

from qdrant_operator.domain import SNAPSHOT_API_GROUP
from qdrant_operator.domain import SNAPSHOT_API_VERSION
from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import PodState
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import SecretRef
from qdrant_operator.domain import StatefulSetStatus
from qdrant_operator.domain import VolumeClaim
from qdrant_operator.domain import VolumeSnapshotState
from qdrant_operator.domain import Zone

config: Any = kubernetes_config
MERGE_PATCH = "application/merge-patch+json"
REVISION_LABEL = "controller-revision-hash"
ORPHAN_WAIT_SECONDS = 60.0
ORPHAN_POLL_SECONDS = 1.0
VOLUME_SNAPSHOT_PLURAL = "volumesnapshots"


async def load_kubernetes_config() -> None:
    """In-cluster service account when present, otherwise the local kubeconfig."""
    try:
        config.load_incluster_config()
        logger.info("kubernetes config loaded", source="in-cluster")
    except config.ConfigException:
        await config.load_kube_config()
        logger.info("kubernetes config loaded", source="kubeconfig")


@dataclass
class KubernetesAdapter:
    @asynccontextmanager
    async def api(self) -> AsyncGenerator[Any]:
        api_client: Any = client.ApiClient()
        async with api_client as api:
            yield api

    async def get_secret_value(self, secret_ref: SecretRef) -> str:
        async with self.api() as api:
            try:
                secret = await client.CoreV1Api(api).read_namespaced_secret(
                    name=secret_ref.name, namespace=secret_ref.namespace
                )
            except ApiException as error:
                if error.status != 404:
                    raise
                raise KeyError(
                    f"Secret {secret_ref.namespace}/{secret_ref.name} not found"
                ) from error
        data: dict[str, str] = secret.data or {}
        encoded = data.get(secret_ref.key)
        if not encoded:
            raise KeyError(
                f"Key {secret_ref.key} not found in secret {secret_ref.namespace}/{secret_ref.name}"
            )
        return base64.b64decode(encoded).decode()

    async def apply_secret(
        self, name: str, namespace: str, data: Mapping[str, str], owner: JsonDict
    ) -> None:
        owner_reference = client.V1OwnerReference(
            api_version=owner["apiVersion"],
            kind=owner["kind"],
            name=owner["name"],
            uid=owner["uid"],
            controller=owner.get("controller", True),
            block_owner_deletion=owner.get("blockOwnerDeletion", True),
        )
        body = client.V1Secret(
            metadata=client.V1ObjectMeta(
                name=name, namespace=namespace, owner_references=[owner_reference]
            ),
            type="Opaque",
            string_data=dict(data),
        )
        async with self.api() as api:
            core = client.CoreV1Api(api)
            try:
                await core.create_namespaced_secret(namespace=namespace, body=body)
            except ApiException as error:
                if error.status != 409:
                    raise
                existing: Any = await core.read_namespaced_secret(name=name, namespace=namespace)
                owners: list[Any] = existing.metadata.owner_references or []
                if all(ref.uid != owner["uid"] for ref in owners):
                    raise PermissionError(
                        f"Secret {namespace}/{name} exists and is not owned by this resource"
                    ) from error
                await core.replace_namespaced_secret(name=name, namespace=namespace, body=body)
        logger.info("secret applied", name=name, namespace=namespace)

    async def get_custom_resource(self, ref: ResourceRef) -> JsonDict | None:
        async with self.api() as api:
            try:
                body = await client.CustomObjectsApi(api).get_namespaced_custom_object(
                    group=ref.kind.group,
                    version=ref.kind.version,
                    namespace=ref.namespace,
                    plural=ref.kind.plural,
                    name=ref.name,
                )
            except ApiException as error:
                if error.status == 404:
                    return None
                raise
        return cast(JsonDict, body)

    async def list_custom_resources(
        self, kind: ResourceKind, namespace: str, label_selector: str | None = None
    ) -> list[JsonDict]:
        async with self.api() as api:
            result = await client.CustomObjectsApi(api).list_namespaced_custom_object(
                group=kind.group,
                version=kind.version,
                namespace=namespace,
                plural=kind.plural,
                label_selector=label_selector or "",
            )
        return cast(list[JsonDict], cast(JsonDict, result).get("items", []))

    async def create_custom_resource(self, body: JsonDict) -> JsonDict:
        kind = ResourceKind(body["kind"], f"{body['kind'].lower()}s")
        metadata = body["metadata"]
        async with self.api() as api:
            created = await client.CustomObjectsApi(api).create_namespaced_custom_object(
                group=kind.group,
                version=kind.version,
                namespace=metadata["namespace"],
                plural=kind.plural,
                body=body,
            )
        logger.info("resource created", kind=body["kind"], name=metadata["name"])
        return cast(JsonDict, created)

    async def delete_custom_resource(self, ref: ResourceRef) -> None:
        async with self.api() as api:
            try:
                await client.CustomObjectsApi(api).delete_namespaced_custom_object(
                    group=ref.kind.group,
                    version=ref.kind.version,
                    namespace=ref.namespace,
                    plural=ref.kind.plural,
                    name=ref.name,
                )
            except ApiException as error:
                if error.status != 404:
                    raise
        logger.info("resource deleted", kind=ref.kind.kind, name=ref.name, namespace=ref.namespace)

    async def patch_status(self, ref: ResourceRef, status: JsonDict) -> None:
        async with self.api() as api:
            custom_objects: Any = client.CustomObjectsApi(api)
            await custom_objects.patch_namespaced_custom_object_status(
                group=ref.kind.group,
                version=ref.kind.version,
                namespace=ref.namespace,
                plural=ref.kind.plural,
                name=ref.name,
                body={"status": status},
                _content_type=MERGE_PATCH,
            )

    async def get_statefulset_status(self, name: str, namespace: str) -> StatefulSetStatus | None:
        async with self.api() as api:
            try:
                statefulset: Any = await client.AppsV1Api(api).read_namespaced_stateful_set_status(
                    name=name, namespace=namespace
                )
            except ApiException as error:
                if error.status == 404:
                    return None
                raise
        status = statefulset.status
        return StatefulSetStatus(
            replicas=status.replicas or 0,
            ready_replicas=status.ready_replicas or 0,
            updated_replicas=status.updated_replicas or 0,
            current_revision=status.current_revision,
            update_revision=status.update_revision,
            partition=self.partition(statefulset),
        )

    @staticmethod
    def partition(statefulset: Any) -> int:
        strategy = statefulset.spec.update_strategy
        rolling = strategy.rolling_update if strategy else None
        return (rolling.partition if rolling else 0) or 0

    async def set_statefulset_partition(self, name: str, namespace: str, partition: int) -> None:
        async with self.api() as api:
            apps: Any = client.AppsV1Api(api)
            await apps.patch_namespaced_stateful_set(
                name=name,
                namespace=namespace,
                body={"spec": {"updateStrategy": {"rollingUpdate": {"partition": partition}}}},
                _content_type=MERGE_PATCH,
            )
        logger.info(
            "statefulset partition set", name=name, namespace=namespace, partition=partition
        )

    async def list_pod_states(self, namespace: str, label_selector: str) -> list[PodState]:
        async with self.api() as api:
            pods: Any = await client.CoreV1Api(api).list_namespaced_pod(
                namespace=namespace, label_selector=label_selector
            )
        items: list[Any] = pods.items
        return [self.pod_state(pod) for pod in items]

    @staticmethod
    def pod_state(pod: Any) -> PodState:
        labels: dict[str, str] = pod.metadata.labels or {}
        conditions: list[Any] = pod.status.conditions or []
        ready = any(c.type == "Ready" and c.status == "True" for c in conditions)
        return PodState(
            name=pod.metadata.name,
            revision=labels.get(REVISION_LABEL),
            ready=ready and not pod.metadata.deletion_timestamp,
        )

    async def get_statefulset_claims(self, name: str, namespace: str) -> dict[str, str] | None:
        async with self.api() as api:
            try:
                statefulset: Any = await client.AppsV1Api(api).read_namespaced_stateful_set(
                    name=name, namespace=namespace
                )
            except ApiException as error:
                if error.status == 404:
                    return None
                raise
        templates: list[Any] = statefulset.spec.volume_claim_templates or []
        return {t.metadata.name: t.spec.resources.requests["storage"] for t in templates}

    async def list_volume_claims(self, namespace: str, label_selector: str) -> list[VolumeClaim]:
        async with self.api() as api:
            claims: Any = await client.CoreV1Api(api).list_namespaced_persistent_volume_claim(
                namespace=namespace, label_selector=label_selector
            )
        return [
            VolumeClaim(
                c.metadata.name,
                c.spec.resources.requests["storage"],
                c.spec.storage_class_name,
                tuple(c.spec.access_modes or ("ReadWriteOnce",)),
                c.spec.volume_name,
            )
            for c in claims.items
        ]

    async def resize_volume_claim(self, name: str, namespace: str, size: str) -> None:
        async with self.api() as api:
            core: Any = client.CoreV1Api(api)
            await core.patch_namespaced_persistent_volume_claim(
                name=name,
                namespace=namespace,
                body={"spec": {"resources": {"requests": {"storage": size}}}},
                _content_type=MERGE_PATCH,
            )
        logger.info("volume claim resized", name=name, namespace=namespace, size=size)

    async def delete_volume_claim(self, name: str, namespace: str) -> None:
        async with self.api() as api:
            try:
                await client.CoreV1Api(api).delete_namespaced_persistent_volume_claim(
                    name=name, namespace=namespace
                )
            except ApiException as error:
                if error.status != 404:
                    raise
        logger.info("volume claim deleted", name=name, namespace=namespace)

    async def create_volume_claim(self, body: JsonDict) -> None:
        metadata = body["metadata"]
        async with self.api() as api:
            try:
                core: Any = client.CoreV1Api(api)
                await core.create_namespaced_persistent_volume_claim(
                    namespace=metadata["namespace"], body=body
                )
            except ApiException as error:
                if error.status != 409:
                    raise
        logger.info("volume claim created", name=metadata["name"], namespace=metadata["namespace"])

    async def create_volume_snapshot(self, body: JsonDict) -> None:
        metadata = body["metadata"]
        async with self.api() as api:
            try:
                await client.CustomObjectsApi(api).create_namespaced_custom_object(
                    group=SNAPSHOT_API_GROUP,
                    version=SNAPSHOT_API_VERSION,
                    namespace=metadata["namespace"],
                    plural=VOLUME_SNAPSHOT_PLURAL,
                    body=body,
                )
            except ApiException as error:
                if error.status != 409:
                    raise
        logger.info(
            "volume snapshot created", name=metadata["name"], namespace=metadata["namespace"]
        )

    async def volume_zone(self, volume_name: str) -> Zone | None:
        async with self.api() as api:
            try:
                volume: Any = await client.CoreV1Api(api).read_persistent_volume(name=volume_name)
            except ApiException as error:
                if error.status == 404:
                    return None
                raise
        affinity: Any = volume.spec.node_affinity
        terms: list[Any] = (
            affinity.required.node_selector_terms if affinity and affinity.required else []
        )
        zones = [
            Zone(label=e.key, name=e.values[0])
            for term in terms
            for e in term.match_expressions or ()
            if e.key.endswith("zone") and e.operator == "In" and len(e.values or ()) == 1
        ]
        return zones[0] if zones else None

    async def node_in_zone(self, zone: Zone) -> str | None:
        async with self.api() as api:
            nodes: Any = await client.CoreV1Api(api).list_node(
                label_selector=f"{zone.label}={zone.name}"
            )
        ready = sorted(
            n.metadata.name
            for n in nodes.items
            if not n.spec.unschedulable
            and any(c.type == "Ready" and c.status == "True" for c in n.status.conditions or ())
        )
        return ready[0] if ready else None

    async def get_volume_snapshot(self, name: str, namespace: str) -> VolumeSnapshotState | None:
        async with self.api() as api:
            try:
                body = await client.CustomObjectsApi(api).get_namespaced_custom_object(
                    group=SNAPSHOT_API_GROUP,
                    version=SNAPSHOT_API_VERSION,
                    namespace=namespace,
                    plural=VOLUME_SNAPSHOT_PLURAL,
                    name=name,
                )
            except ApiException as error:
                if error.status == 404:
                    return None
                raise
        status: JsonDict = cast(JsonDict, body).get("status") or {}
        failure: JsonDict = status.get("error") or {}
        error_message: str | None = failure.get("message")
        return VolumeSnapshotState(
            name=name, ready=bool(status.get("readyToUse")), error=error_message
        )

    async def list_volume_snapshots(self, namespace: str, label_selector: str) -> list[str]:
        async with self.api() as api:
            result = await client.CustomObjectsApi(api).list_namespaced_custom_object(
                group=SNAPSHOT_API_GROUP,
                version=SNAPSHOT_API_VERSION,
                namespace=namespace,
                plural=VOLUME_SNAPSHOT_PLURAL,
                label_selector=label_selector,
            )
        items = cast(list[JsonDict], cast(JsonDict, result).get("items", []))
        return [item["metadata"]["name"] for item in items]

    async def delete_volume_snapshot(self, name: str, namespace: str) -> None:
        async with self.api() as api:
            try:
                await client.CustomObjectsApi(api).delete_namespaced_custom_object(
                    group=SNAPSHOT_API_GROUP,
                    version=SNAPSHOT_API_VERSION,
                    namespace=namespace,
                    plural=VOLUME_SNAPSHOT_PLURAL,
                    name=name,
                )
            except ApiException as error:
                if error.status != 404:
                    raise
        logger.info("volume snapshot deleted", name=name, namespace=namespace)

    async def crash_log(self, name: str, namespace: str, tail_lines: int) -> str:
        async with self.api() as api:
            core: Any = client.CoreV1Api(api)
            runs = [
                await self.container_log(core, name, namespace, tail_lines, p)
                for p in (False, True)
            ]
        return "\n".join(runs)

    @staticmethod
    async def container_log(
        core: Any, name: str, namespace: str, tail_lines: int, previous: bool
    ) -> str:
        try:
            log: Any = await core.read_namespaced_pod_log(
                name=name, namespace=namespace, previous=previous, tail_lines=tail_lines
            )
        except ApiException as error:
            if error.status not in (400, 404):
                raise
            return ""
        return str(log)

    async def orphan_statefulset(self, name: str, namespace: str) -> None:
        async with self.api() as api:
            apps: Any = client.AppsV1Api(api)
            try:
                await apps.delete_namespaced_stateful_set(
                    name=name, namespace=namespace, propagation_policy="Orphan"
                )
            except ApiException as error:
                if error.status != 404:
                    raise
            deadline = asyncio.get_running_loop().time() + ORPHAN_WAIT_SECONDS
            while await self.statefulset_exists(apps, name, namespace):
                if asyncio.get_running_loop().time() > deadline:
                    raise TimeoutError(f"StatefulSet {namespace}/{name} still present after delete")
                await asyncio.sleep(ORPHAN_POLL_SECONDS)
        logger.info("statefulset orphan-deleted", name=name, namespace=namespace)

    @staticmethod
    async def statefulset_exists(apps: Any, name: str, namespace: str) -> bool:
        try:
            await apps.read_namespaced_stateful_set(name=name, namespace=namespace)
        except ApiException as error:
            if error.status == 404:
                return False
            raise
        return True

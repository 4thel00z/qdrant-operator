"""Kubernetes API adapter. Client configuration is loaded once at operator startup."""

import base64
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Any
from typing import cast

from kubernetes_asyncio import client
from kubernetes_asyncio import config as kubernetes_config
from kubernetes_asyncio.client.exceptions import ApiException
from loguru import logger

from qdrant_operator.domain import JsonDict
from qdrant_operator.domain import ResourceKind
from qdrant_operator.domain import ResourceRef
from qdrant_operator.domain import SecretRef
from qdrant_operator.domain import StatefulSetStatus

config: Any = kubernetes_config


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
    async def api(self) -> AsyncIterator[Any]:
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
            await client.CustomObjectsApi(api).patch_namespaced_custom_object_status(
                group=ref.kind.group,
                version=ref.kind.version,
                namespace=ref.namespace,
                plural=ref.kind.plural,
                name=ref.name,
                body={"status": status},
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
            replicas=status.replicas or 0, ready_replicas=status.ready_replicas or 0
        )

"""Kubernetes API adapter."""

import base64
from dataclasses import dataclass

import structlog
from kubernetes_asyncio import client
from kubernetes_asyncio import config

from qdrant_operator.domain import SecretRef

log = structlog.get_logger()


@dataclass
class KubernetesAdapter:
    """Adapter for Kubernetes API operations."""

    async def get_secret_value(self, secret_ref: SecretRef) -> str:
        """Get a value from a Kubernetes Secret."""
        config.load_incluster_config()
        v1 = client.CoreV1Api()

        secret = await v1.read_namespaced_secret(
            name=secret_ref.name,
            namespace=secret_ref.namespace,
        )

        encoded = secret.data.get(secret_ref.key)
        if not encoded:
            raise ValueError(f"Key {secret_ref.key} not found in secret {secret_ref.name}")

        return base64.b64decode(encoded).decode("utf-8")

    async def update_status(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
        status: dict,
    ) -> None:
        """Update the status subresource of a custom resource."""
        config.load_incluster_config()
        custom = client.CustomObjectsApi()

        await custom.patch_namespaced_custom_object_status(
            group=group,
            version=version,
            namespace=namespace,
            plural=plural,
            name=name,
            body={"status": status},
        )

        await log.ainfo("status_updated", name=name, namespace=namespace)

    async def create_resource(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        body: dict,
    ) -> dict:
        """Create a custom resource."""
        await config.load_incluster_config()
        custom = client.CustomObjectsApi()

        result = await custom.create_namespaced_custom_object(
            group=group,
            version=version,
            namespace=namespace,
            plural=plural,
            body=body,
        )

        await log.ainfo("resource_created", name=body["metadata"]["name"], namespace=namespace)
        return result

    async def get_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> dict | None:
        """Get a custom resource. Returns None if not found."""
        config.load_incluster_config()
        custom = client.CustomObjectsApi()

        try:
            return await custom.get_namespaced_custom_object(
                group=group,
                version=version,
                namespace=namespace,
                plural=plural,
                name=name,
            )
        except client.ApiException as e:
            if e.status == 404:
                return None
            raise

    async def list_resources(
        self,
        group: str,
        version: str,
        plural: str,
        namespace: str,
        label_selector: str | None = None,
    ) -> list[dict]:
        """List custom resources in a namespace."""
        config.load_incluster_config()
        custom = client.CustomObjectsApi()

        result = await custom.list_namespaced_custom_object(
            group=group,
            version=version,
            namespace=namespace,
            plural=plural,
            label_selector=label_selector or "",
        )

        return result.get("items", [])

    async def delete_resource(
        self,
        group: str,
        version: str,
        plural: str,
        name: str,
        namespace: str,
    ) -> None:
        """Delete a custom resource."""
        config.load_incluster_config()
        custom = client.CustomObjectsApi()

        await custom.delete_namespaced_custom_object(
            group=group,
            version=version,
            namespace=namespace,
            plural=plural,
            name=name,
        )

        await log.ainfo("resource_deleted", name=name, namespace=namespace)

    async def get_service_endpoint(
        self,
        name: str,
        namespace: str,
        port: int = 6333,
    ) -> str:
        """Get the internal endpoint URL for a service."""
        return f"http://{name}.{namespace}.svc.cluster.local:{port}"
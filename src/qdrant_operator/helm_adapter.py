"""Helm CLI adapter: stateless, one `helm upgrade --install` per reconcile, values over stdin."""

import asyncio
import json
from collections.abc import Mapping
from dataclasses import dataclass
from typing import Any

from loguru import logger

QDRANT_CHART = "qdrant"
QDRANT_CHART_REPO = "https://qdrant.github.io/qdrant-helm"


class HelmError(RuntimeError):
    """A helm invocation exited non-zero."""


@dataclass
class HelmAdapter:
    chart: str = QDRANT_CHART
    repo: str = QDRANT_CHART_REPO
    kubeconfig: str | None = None
    timeout_seconds: float = 600.0

    async def apply(
        self,
        release_name: str,
        namespace: str,
        chart_version: str,
        values: Mapping[str, Any],
    ) -> None:
        await self.recover_pending(release_name, namespace)
        await self.run(
            "upgrade",
            "--install",
            release_name,
            self.chart,
            "--repo",
            self.repo,
            "--version",
            chart_version,
            "--namespace",
            namespace,
            "--create-namespace",
            "--values",
            "-",
            stdin=json.dumps(values).encode(),
        )
        logger.info("helm release applied", release=release_name, namespace=namespace)

    async def uninstall(self, release_name: str, namespace: str) -> None:
        await self.run("uninstall", release_name, "--namespace", namespace, "--wait")
        logger.info("helm release uninstalled", release=release_name, namespace=namespace)

    async def release_exists(self, release_name: str, namespace: str) -> bool:
        return bool(await self.release_status(release_name, namespace))

    async def release_status(self, release_name: str, namespace: str) -> str:
        """Helm's release status (deployed, failed, pending-upgrade, ...), empty when absent."""
        try:
            output = await self.run(
                "status", release_name, "--namespace", namespace, "--output", "json"
            )
        except HelmError:
            return ""
        return str(json.loads(output).get("info", {}).get("status", ""))

    async def recover_pending(self, release_name: str, namespace: str) -> None:
        """Roll back a release another helm process left mid-operation, or every upgrade fails.

        Helm refuses to touch a release in a pending-* state ("another operation is in
        progress"); that state is what a killed operator leaves behind.
        """
        status = await self.release_status(release_name, namespace)
        if not status.startswith("pending-"):
            return
        logger.warning(
            "helm release left pending, rolling back", release=release_name, status=status
        )
        await self.run("rollback", release_name, "--namespace", namespace)

    async def run(self, *args: str, stdin: bytes | None = None) -> str:
        command = ["helm", *args, *self.kubeconfig_args()]
        logger.debug("helm command", command=" ".join(command))
        process = await asyncio.create_subprocess_exec(
            *command,
            stdin=asyncio.subprocess.PIPE if stdin is not None else None,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            stdout, stderr = await asyncio.wait_for(
                process.communicate(stdin), timeout=self.timeout_seconds
            )
        except TimeoutError:
            process.kill()
            raise HelmError(f"helm {args[0]} timed out after {self.timeout_seconds}s") from None
        if process.returncode != 0:
            raise HelmError(f"helm {args[0]} failed: {stderr.decode().strip()}")
        return stdout.decode()

    def kubeconfig_args(self) -> list[str]:
        if not self.kubeconfig:
            return []
        return ["--kubeconfig", self.kubeconfig]

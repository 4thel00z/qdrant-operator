"""Helm CLI adapter for chart operations."""

import asyncio
import json
import tempfile
from dataclasses import dataclass
from pathlib import Path

import structlog

from qdrant_operator.ports import HelmPort

log = structlog.get_logger()


@dataclass
class HelmAdapter(HelmPort):
    """Adapter for Helm CLI operations."""

    kubeconfig: str | None = None

    async def install(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
        version: str | None = None,
    ) -> str:
        """Install a Helm release."""
        await self.ensure_repo()

        cmd = [
            "helm",
            "install",
            release_name,
            chart,
            "--namespace",
            namespace,
            "--create-namespace",
            "--wait",
        ]

        if version:
            cmd.extend(["--version", version])

        cmd = await self.add_values(cmd, values)
        cmd = self.add_kubeconfig(cmd)

        await self.run_command(cmd)
        await log.ainfo("helm_install_complete", release=release_name, namespace=namespace)
        return release_name

    async def upgrade(
        self,
        release_name: str,
        namespace: str,
        chart: str,
        values: dict,
        version: str | None = None,
    ) -> str:
        """Upgrade an existing Helm release."""
        await self.ensure_repo()

        cmd = [
            "helm",
            "upgrade",
            release_name,
            chart,
            "--namespace",
            namespace,
            "--wait",
        ]

        if version:
            cmd.extend(["--version", version])

        cmd = await self.add_values(cmd, values)
        cmd = self.add_kubeconfig(cmd)

        await self.run_command(cmd)
        await log.ainfo("helm_upgrade_complete", release=release_name, namespace=namespace)
        return release_name

    async def uninstall(self, release_name: str, namespace: str) -> None:
        """Uninstall a Helm release."""
        cmd = [
            "helm",
            "uninstall",
            release_name,
            "--namespace",
            namespace,
        ]
        cmd = self.add_kubeconfig(cmd)

        await self.run_command(cmd)
        await log.ainfo("helm_uninstall_complete", release=release_name, namespace=namespace)

    async def get_release_status(
        self, release_name: str, namespace: str
    ) -> dict | None:
        """Get status of a Helm release."""
        cmd = [
            "helm",
            "status",
            release_name,
            "--namespace",
            namespace,
            "--output",
            "json",
        ]
        cmd = self.add_kubeconfig(cmd)

        try:
            stdout = await self.run_command(cmd)
            return json.loads(stdout)
        except RuntimeError:
            return None

    async def ensure_repo(self) -> None:
        """Ensure Qdrant Helm repo is added."""
        cmd = ["helm", "repo", "add", "qdrant", "https://qdrant.github.io/qdrant-helm"]
        cmd = self.add_kubeconfig(cmd)

        try:
            await self.run_command(cmd)
        except RuntimeError:
            pass

        cmd = ["helm", "repo", "update"]
        cmd = self.add_kubeconfig(cmd)
        await self.run_command(cmd)

    async def add_values(self, cmd: list[str], values: dict) -> list[str]:
        """Add values file to command."""
        with tempfile.NamedTemporaryFile(
            mode="w", suffix=".json", delete=False
        ) as f:
            json.dump(values, f)
            values_path = f.name

        return [*cmd, "--values", values_path]

    def add_kubeconfig(self, cmd: list[str]) -> list[str]:
        """Add kubeconfig to command if set."""
        if self.kubeconfig:
            return [*cmd, "--kubeconfig", self.kubeconfig]
        return cmd

    async def run_command(self, cmd: list[str]) -> str:
        """Run a command and return stdout."""
        await log.adebug("helm_command", cmd=" ".join(cmd))

        process = await asyncio.create_subprocess_exec(
            *cmd,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        stdout, stderr = await process.communicate()

        if process.returncode != 0:
            await log.aerror(
                "helm_command_failed",
                cmd=" ".join(cmd),
                returncode=process.returncode,
                stderr=stderr.decode(),
            )
            raise RuntimeError(f"Helm command failed: {stderr.decode()}")

        return stdout.decode()
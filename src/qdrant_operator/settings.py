"""Operator settings, read once in main from `QDRANT_OPERATOR_<SECTION>_*` environment variables."""

from enum import StrEnum
from typing import Annotated
from typing import ClassVar

from pydantic import BaseModel
from pydantic import ConfigDict
from pydantic import Field
from pydantic import field_validator
from pydantic_settings import BaseSettings
from pydantic_settings import NoDecode
from pydantic_settings import SettingsConfigDict


class LogFormat(StrEnum):
    JSON = "json"
    TEXT = "text"


class LoggingSettings(BaseSettings):
    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_LOGGING_", frozen=True
    )

    level: str = "INFO"
    format: LogFormat = LogFormat.JSON


class KopfSettings(BaseSettings):
    """Watched namespaces (comma-separated, empty watches all), peering and liveness.

    `priority` 0 draws a random priority per pod; `peering_lifetime` 0 keeps kopf's default.
    """

    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_KOPF_", frozen=True
    )

    namespaces: Annotated[tuple[str, ...], NoDecode] = ()
    liveness_endpoint: str = "http://0.0.0.0:8080/healthz"
    peering_name: str = ""
    priority: int = 0
    peering_lifetime: int = 0

    @field_validator("namespaces", mode="before")
    @classmethod
    def split_namespaces(cls, value: object) -> object:
        if not isinstance(value, str):
            return value
        return tuple(name.strip() for name in value.split(",") if name.strip())


class MetricsSettings(BaseSettings):
    """Port of the Prometheus endpoint; 0 disables it."""

    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_METRICS_", frozen=True
    )

    port: int = 9090


class KubernetesSettings(BaseSettings):
    """An empty kubeconfig means in-cluster config, falling back to the default kubeconfig."""

    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_KUBERNETES_", frozen=True
    )

    kubeconfig: str = ""


class HelmSettings(BaseSettings):
    """An empty post_renderer means the script on PATH, else the one beside the interpreter."""

    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_HELM_", frozen=True
    )

    chart: str = "qdrant"
    repo: str = "https://qdrant.github.io/qdrant-helm"
    timeout_seconds: float = 600.0
    post_renderer: str = ""


class QdrantSettings(BaseSettings):
    model_config: ClassVar[SettingsConfigDict] = SettingsConfigDict(
        env_prefix="QDRANT_OPERATOR_QDRANT_", frozen=True
    )

    timeout_seconds: float = 30.0
    snapshot_timeout_seconds: float = 3600.0


class Settings(BaseModel):
    model_config: ClassVar[ConfigDict] = ConfigDict(frozen=True)

    logging: LoggingSettings = Field(default_factory=LoggingSettings)
    kopf: KopfSettings = Field(default_factory=KopfSettings)
    metrics: MetricsSettings = Field(default_factory=MetricsSettings)
    kubernetes: KubernetesSettings = Field(default_factory=KubernetesSettings)
    helm: HelmSettings = Field(default_factory=HelmSettings)
    qdrant: QdrantSettings = Field(default_factory=QdrantSettings)

import kopf
import pytest

from qdrant_operator.handlers import FINALIZER
from qdrant_operator.handlers import operator_settings
from qdrant_operator.settings import KopfSettings
from qdrant_operator.settings import LogFormat
from qdrant_operator.settings import Settings


def test_defaults_without_environment() -> None:
    settings = Settings()

    assert settings.logging.format == LogFormat.JSON
    assert settings.kopf.namespaces == ()
    assert settings.metrics.port == 9090
    assert settings.kubernetes.kubeconfig == ""


def test_sections_read_their_own_prefix(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("QDRANT_OPERATOR_LOGGING_FORMAT", "text")
    monkeypatch.setenv("QDRANT_OPERATOR_METRICS_PORT", "0")
    monkeypatch.setenv("QDRANT_OPERATOR_QDRANT_TIMEOUT_SECONDS", "5")

    settings = Settings()

    assert settings.logging.format == LogFormat.TEXT
    assert settings.metrics.port == 0
    assert settings.qdrant.timeout_seconds == 5.0


def test_watched_namespaces_are_comma_separated(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("QDRANT_OPERATOR_KOPF_NAMESPACES", "a, b,")

    assert KopfSettings().namespaces == ("a", "b")


def test_blank_namespaces_watch_all(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("QDRANT_OPERATOR_KOPF_NAMESPACES", " , ")

    assert KopfSettings().namespaces == ()


def test_operator_settings_use_the_qdrant_prefix() -> None:
    operator = operator_settings(KopfSettings())

    assert operator.persistence.finalizer == FINALIZER


def test_peering_lifetime_is_applied() -> None:
    assert operator_settings(KopfSettings(peering_lifetime=20)).peering.lifetime == 20


def test_peering_lifetime_keeps_the_kopf_default_when_unset() -> None:
    default = kopf.OperatorSettings().peering.lifetime

    assert operator_settings(KopfSettings()).peering.lifetime == default

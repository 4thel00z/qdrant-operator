import io
from typing import Any

import pytest
import yaml

from qdrant_operator.postrender import main
from qdrant_operator.postrender import set_partition

STATEFULSET = """---
# Source: qdrant/templates/statefulset.yaml
apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: qdrant-db
spec:
  replicas: 3
  updateStrategy:
    type: RollingUpdate
    rollingUpdate:
      partition: 0
  template:
    spec:
      containers:
        - name: qdrant
          image: "qdrant/qdrant:v1.16.3"
"""

SERVICE = """---
# Source: qdrant/templates/service.yaml
apiVersion: v1
kind: Service
metadata:
  name: qdrant-db
spec:
  ports:
    - port: 6333   # kept as is
"""

OTHER_STATEFULSET = STATEFULSET.replace("name: qdrant-db", "name: qdrant-other")
CONFIG_MAP = "---\nkind: ConfigMap\nmetadata:\n  name: qdrant-db\ndata:\n  a: |\n    x\n"


def documents(stream: str) -> list[dict[str, Any]]:
    return [doc for doc in yaml.safe_load_all(stream) if doc]


def strategy(stream: str, name: str) -> dict[str, Any]:
    return next(
        doc["spec"]["updateStrategy"]
        for doc in documents(stream)
        if doc["kind"] == "StatefulSet" and doc["metadata"]["name"] == name
    )


def test_matching_statefulset_gets_the_partition() -> None:
    rewritten = set_partition(SERVICE + STATEFULSET, "qdrant-db", 3)

    assert strategy(rewritten, "qdrant-db") == {
        "type": "RollingUpdate",
        "rollingUpdate": {"partition": 3},
    }


def test_rest_of_the_statefulset_is_unchanged() -> None:
    rewritten = set_partition(STATEFULSET, "qdrant-db", 3)

    before = documents(STATEFULSET)[0]
    after = documents(rewritten)[0]
    before["spec"]["updateStrategy"] = after["spec"]["updateStrategy"]
    assert before == after
    assert rewritten.startswith("---\n# Source: qdrant/templates/statefulset.yaml\n")


def test_missing_update_strategy_is_added() -> None:
    stream = STATEFULSET.replace(
        "  updateStrategy:\n    type: RollingUpdate\n    rollingUpdate:\n      partition: 0\n", ""
    )

    rewritten = set_partition(stream, "qdrant-db", 2)

    assert strategy(rewritten, "qdrant-db") == {
        "type": "RollingUpdate",
        "rollingUpdate": {"partition": 2},
    }


def test_only_the_partition_line_changes() -> None:
    rewritten = set_partition(STATEFULSET, "qdrant-db", 3)

    assert rewritten == STATEFULSET.replace("partition: 0", "partition: 3")


def test_flow_style_update_strategy_is_replaced() -> None:
    stream = STATEFULSET.replace(
        "  updateStrategy:\n    type: RollingUpdate\n    rollingUpdate:\n      partition: 0\n",
        "  updateStrategy: {type: OnDelete}\n",
    )

    rewritten = set_partition(stream, "qdrant-db", 2)

    assert strategy(rewritten, "qdrant-db") == {
        "type": "RollingUpdate",
        "rollingUpdate": {"partition": 2},
    }
    assert documents(rewritten)[0]["spec"]["replicas"] == 3


def test_other_kinds_are_returned_byte_for_byte() -> None:
    stream = SERVICE + CONFIG_MAP + STATEFULSET

    rewritten = set_partition(stream, "qdrant-db", 3)

    assert rewritten.startswith(SERVICE + CONFIG_MAP)


def test_statefulset_with_another_name_is_untouched() -> None:
    stream = SERVICE + OTHER_STATEFULSET

    assert set_partition(stream, "qdrant-db", 3) == stream


def test_stream_without_a_statefulset_is_unchanged() -> None:
    stream = SERVICE + CONFIG_MAP

    assert set_partition(stream, "qdrant-db", 3) == stream


def test_empty_stream_is_unchanged() -> None:
    assert set_partition("", "qdrant-db", 3) == ""


def test_stream_without_leading_separator_is_handled() -> None:
    stream = STATEFULSET.removeprefix("---\n")

    rewritten = set_partition(stream, "qdrant-db", 3)

    assert strategy(rewritten, "qdrant-db")["rollingUpdate"] == {"partition": 3}


def test_main_filters_stdin_to_stdout(
    monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr("sys.argv", ["qdrant-operator-postrender", "qdrant-db", "3"])
    monkeypatch.setattr("sys.stdin", io.StringIO(SERVICE + STATEFULSET))

    main()

    assert capsys.readouterr().out == set_partition(SERVICE + STATEFULSET, "qdrant-db", 3)


@pytest.mark.parametrize("arguments", [[], ["qdrant-db"], ["qdrant-db", "x"], ["a", "1", "2"]])
def test_main_rejects_bad_arguments(monkeypatch: pytest.MonkeyPatch, arguments: list[str]) -> None:
    monkeypatch.setattr("sys.argv", ["qdrant-operator-postrender", *arguments])

    with pytest.raises(SystemExit) as raised:
        main()

    assert "usage" in str(raised.value)

from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def test_chart_crds_match_manifests() -> None:
    manifests = ROOT / "manifests" / "crds"
    chart = ROOT / "charts" / "qdrant-operator" / "crds"
    names = sorted(p.name for p in manifests.glob("*.yaml"))

    assert names == sorted(p.name for p in chart.glob("*.yaml"))
    for name in names:
        assert (manifests / name).read_text() == (chart / name).read_text(), name

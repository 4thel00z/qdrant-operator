from qdrant_operator.ha import namespace_args


def test_all_namespaces_by_default() -> None:
    assert namespace_args({}) == ["--all-namespaces"]
    assert namespace_args({"WATCH_NAMESPACES": " , "}) == ["--all-namespaces"]


def test_watched_namespaces_become_namespace_arguments() -> None:
    assert namespace_args({"WATCH_NAMESPACES": "a, b"}) == ["--namespace", "a", "--namespace", "b"]

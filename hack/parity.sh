#!/usr/bin/env bash
set -uo pipefail

usage="usage: hack/parity.sh [python|go ...]   (default: python go)"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

kind_cluster="${KIND_CLUSTER:-kind}"
operator_namespace="${NAMESPACE:-qdrant-system}"
release="${HELM_RELEASE:-qdrant-operator}"
test_namespace="${E2E_NAMESPACE:-qdrant-e2e}"
out_dir="${PARITY_OUT:-$root/parity-out}"
test_timeout="${PARITY_TEST_TIMEOUT:-120m}"
kinds="qdrantrestores qdrantaccesskeys qdrantcollections qdrantbackups qdrantmigrations qdrantbackupschedules qdrantclusters"

impls=("$@")
[ "${#impls[@]}" -gt 0 ] || impls=(python go)
for impl in "${impls[@]}"; do
	case "$impl" in
	python | go) ;;
	*)
		echo "$usage" >&2
		exit 2
		;;
	esac
done

mkdir -p "$out_dir"
status=0

build_image() {
	case "$1" in
	python) docker build -t qdrant-operator:e2e -f Dockerfile . ;;
	go) docker build -t qdrant-operator-go:e2e -f Dockerfile.golang . ;;
	esac
}

image_repository() {
	case "$1" in
	python) echo qdrant-operator ;;
	go) echo qdrant-operator-go ;;
	esac
}

extra_values() {
	case "$1" in
	python) echo --set operator.logFormat=text ;;
	go) ;;
	esac
}

deploy() {
	local impl="$1" repository
	repository="$(image_repository "$impl")"
	kind load docker-image "$repository:e2e" --name "$kind_cluster" || return 1
	# shellcheck disable=SC2046
	helm upgrade --install "$release" charts/qdrant-operator \
		--namespace "$operator_namespace" \
		--create-namespace \
		--set image.repository="$repository" \
		--set image.tag=e2e \
		--set image.pullPolicy=Never \
		$(extra_values "$impl") \
		--wait --timeout 5m || return 1
	kubectl rollout status deployment -n "$operator_namespace" -l "app.kubernetes.io/instance=$release" --timeout 5m
}

clean() {
	local kind name
	for kind in $kinds; do
		kubectl delete "$kind" --all -n "$test_namespace" --timeout=120s >/dev/null 2>&1
		for name in $(kubectl get "$kind" -n "$test_namespace" -o name 2>/dev/null); do
			kubectl patch "$name" -n "$test_namespace" --type=merge -p '{"metadata":{"finalizers":null}}' >/dev/null 2>&1
		done
	done
	kubectl delete namespace "$test_namespace" --ignore-not-found --timeout=180s >/dev/null 2>&1
	helm uninstall "$release" -n "$operator_namespace" --wait >/dev/null 2>&1
}

report_arguments=()
clean
for impl in "${impls[@]}"; do
	echo "== $impl: build"
	result="$out_dir/$impl.json"
	: >"$result"
	report_arguments+=("$impl=$result")
	if ! build_image "$impl"; then
		echo "$impl: image build failed" >&2
		status=1
		continue
	fi
	echo "== $impl: deploy"
	if ! deploy "$impl"; then
		echo "$impl: deploy failed" >&2
		kubectl logs -n "$operator_namespace" -l "app.kubernetes.io/instance=$release" --tail=50 >&2
		clean
		status=1
		continue
	fi
	echo "== $impl: test"
	go test -tags e2e -json -count=1 -timeout "$test_timeout" ./test/e2e/... >"$result" || true
	kubectl logs -n "$operator_namespace" -l "app.kubernetes.io/instance=$release" --tail=-1 >"$out_dir/$impl.operator.log" 2>&1
	echo "== $impl: clean"
	clean
done

go run ./hack/parityreport "${report_arguments[@]}" | tee "$out_dir/report.md"
exit "$status"

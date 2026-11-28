#!/usr/bin/env bash
set -euo pipefail

out_dir="${1:?usage: hack/gen-crds.sh <output dir>}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

go tool controller-gen object paths=./api/...
go tool controller-gen crd paths=./api/... output:crd:dir="$work_dir"

mkdir -p "$out_dir"
for file in "$work_dir"/qdrant.io_*.yaml; do
	plural="$(basename "$file" .yaml)"
	plural="${plural#qdrant.io_}"
	cp "$file" "$out_dir/${plural%s}-crd.yaml"
done

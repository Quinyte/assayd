#!/usr/bin/env bash
# e2e on a real k3d cluster — design 07's matrix, runnable locally.
# Deliberately fails loudly rather than skipping: a skipped e2e is an untested feature.
set -euo pipefail
CLUSTER="${CLUSTER:-plume-local}"

command -v k3d >/dev/null || { echo "k3d not installed"; exit 1; }
k3d cluster list | grep -q "^${CLUSTER} " || { echo "creating cluster ${CLUSTER}"; k3d cluster create "${CLUSTER}" --agents 1 --wait; }
kubectl config use-context "k3d-${CLUSTER}" >/dev/null

echo "==> applying CRDs"
kubectl apply -f config/crd

echo "==> running e2e suite"
go test ./test/e2e/... -count=1 -timeout 20m -v

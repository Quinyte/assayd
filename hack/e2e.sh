#!/usr/bin/env bash
# e2e on a real k3d cluster — design 07's matrix, runnable locally.
# Deliberately fails loudly rather than skipping: a skipped e2e is an untested feature.
set -euo pipefail
CLUSTER="${CLUSTER:-plume-local}"

command -v k3d >/dev/null || { echo "k3d not installed"; exit 1; }
k3d cluster list | grep -q "^${CLUSTER} " || { echo "creating cluster ${CLUSTER}"; k3d cluster create "${CLUSTER}" --agents 1 --wait; }
kubectl config use-context "k3d-${CLUSTER}" >/dev/null

echo "==> building and loading the operator image"
IMAGE="plume-operator:e2e"
docker build -t "${IMAGE}" -f Dockerfile .
k3d image import "${IMAGE}" -c "${CLUSTER}"

echo "==> installing the chart"
# CRDs ship in the chart's crds/ directory, so this installs them too.
helm upgrade --install plume charts/plume \
  -f charts/plume/values-local.yaml \
  --set operator.image.repository=plume-operator \
  --set operator.image.tag=e2e \
  --set operator.image.pullPolicy=Never \
  --wait --timeout 5m

echo "==> running e2e suite"
PLUME_E2E=1 go test ./test/e2e/... -count=1 -timeout 20m -v

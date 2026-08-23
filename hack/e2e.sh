#!/usr/bin/env bash
# e2e against a real cluster — design 07 §5's matrix, runnable locally.
#
# Deliberately fails loudly rather than skipping: a skipped e2e is an untested
# feature. DISTRO selects the cluster the suite runs against; k3d is created
# here, kind is expected to exist already (CI's kind-action makes it).
set -euo pipefail

DISTRO="${DISTRO:-k3d}"
CLUSTER="${CLUSTER:-plume-local}"
IMAGE="plume-operator:e2e"

case "${DISTRO}" in
k3d)
  command -v k3d >/dev/null || { echo "k3d is not installed"; exit 1; }
  if ! k3d cluster list -o json | grep -q "\"${CLUSTER}\""; then
    echo "==> creating k3d cluster ${CLUSTER}"
    k3d cluster create "${CLUSTER}" --agents 0 --wait
  fi
  kubectl config use-context "k3d-${CLUSTER}" >/dev/null
  ;;
kind)
  command -v kind >/dev/null || { echo "kind is not installed"; exit 1; }
  kubectl config use-context "kind-${CLUSTER}" >/dev/null
  ;;
*)
  echo "DISTRO must be k3d or kind, got ${DISTRO}"; exit 1 ;;
esac

echo "==> building the operator image"
docker build -t "${IMAGE}" -f Dockerfile .

echo "==> loading the image into ${DISTRO}"
case "${DISTRO}" in
k3d)  k3d image import "${IMAGE}" -c "${CLUSTER}" ;;
kind) kind load docker-image "${IMAGE}" --name "${CLUSTER}" ;;
esac

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

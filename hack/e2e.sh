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
  # Merge explicitly rather than assume `cluster create` left a context behind.
  # A cluster outlives its kubeconfig entry — restarting the Docker VM, or
  # reusing a cluster from an earlier session, leaves the cluster running with
  # no context pointing at it, and the run then fails on something unrelated to
  # what it is testing.
  echo "==> merging kubeconfig for ${CLUSTER}"
  k3d kubeconfig merge "${CLUSTER}" --kubeconfig-merge-default >/dev/null
  kubectl config use-context "k3d-${CLUSTER}" >/dev/null
  ;;
kind)
  command -v kind >/dev/null || { echo "kind is not installed"; exit 1; }
  kind export kubeconfig --name "${CLUSTER}" >/dev/null
  kubectl config use-context "kind-${CLUSTER}" >/dev/null
  ;;
*)
  echo "DISTRO must be k3d or kind, got ${DISTRO}"; exit 1 ;;
esac

# Fail here, with a useful message, rather than three steps later on something
# that looks like a product bug.
if ! kubectl cluster-info >/dev/null 2>&1; then
  echo "the ${DISTRO} cluster '${CLUSTER}' is not reachable. If the Docker VM was"
  echo "restarted, delete and recreate it:  k3d cluster delete ${CLUSTER}"
  exit 1
fi

echo "==> building the operator image"
# DOCKER_BUILD_NETWORK is an escape hatch for hosts whose container DNS returns
# an IPv6 address the bridge network cannot route — the build then fails on
# `go mod download` with "network is unreachable". Setting it to `host` sidesteps
# that. It is a WORKAROUND: the real fix is the host's Docker DNS config, since
# a build that needs the host network is a build that is not isolated.
docker build ${DOCKER_BUILD_NETWORK:+--network "${DOCKER_BUILD_NETWORK}"} \
  -t "${IMAGE}" -f Dockerfile .

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

#!/usr/bin/env bash
# Provision a throwaway cluster, install the pinned agentgateway, run the cluster
# conformance suite, tear down. Separate from CLUSTER=plume-local so `make e2e`
# is unaffected.
#
# A skipped conformance run is an unverified dependency contract, so this fails
# loudly rather than skipping when a prerequisite is missing.
set -euo pipefail

CLUSTER="${CONF_CLUSTER:-plume-conformance}"
AGW_VERSION="${AGW_VERSION:-1.4.1}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.6.0}"
KEEP="${KEEP:-0}"

for tool in k3d kubectl helm go; do
  command -v "$tool" >/dev/null || { echo "conformance needs $tool on PATH"; exit 1; }
done

cleanup() {
  if [ "$KEEP" = "1" ]; then
    echo "==> KEEP=1, leaving cluster $CLUSTER up"
  else
    k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

echo "==> creating $CLUSTER"
k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
k3d cluster create "$CLUSTER" --agents 0 --wait --timeout 180s >/dev/null
KUBECONFIG="$(k3d kubeconfig write "$CLUSTER")"
export KUBECONFIG

echo "==> Gateway API $GWAPI_VERSION"
kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null

echo "==> agentgateway $AGW_VERSION"
helm install agw-crds "oci://ghcr.io/agentgateway/charts/agentgateway-crds" --version "$AGW_VERSION" \
  -n agentgateway-system --create-namespace >/dev/null
helm install agw "oci://ghcr.io/agentgateway/charts/agentgateway" --version "$AGW_VERSION" \
  -n agentgateway-system >/dev/null
kubectl -n agentgateway-system rollout status deploy/agw-agentgateway --timeout=120s >/dev/null

echo "==> conformance"
go test -tags cluster ./test/conformance/... -count=1 -v 2>&1 | grep -E '^(=== RUN|--- (PASS|FAIL|SKIP)|ok|FAIL|    )' || true
go test -tags cluster ./test/conformance/... -count=1

#!/usr/bin/env bash
# Provision a throwaway cluster, install the pinned agentgateway, run the cluster
# conformance suite, tear down. Separate from CLUSTER=assayd-local so `make e2e`
# is unaffected.
#
# A skipped conformance run is an unverified dependency contract, so this fails
# loudly rather than skipping when a prerequisite is missing.
set -euo pipefail

CLUSTER="${CONF_CLUSTER:-assayd-conformance}"
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
# 120s was not enough for a COLD pull of cr.agentgateway.dev/controller. Measured
# on 2026-09-02: still ContainerCreating at 4 minutes on a laptop with three
# other k3d clusters running. The old failure printed only "timed out waiting
# for the condition", which says nothing about whether the dependency is broken,
# the cluster is wedged, or an image is simply large — so the diagnosis had to be
# reproduced by hand. Say what to look at.
if ! kubectl -n agentgateway-system rollout status deploy/agw-agentgateway --timeout=420s; then
  echo
  echo "agentgateway did not become ready. This is usually a slow or failing image"
  echo "pull, not a broken dependency contract. What the cluster says:"
  kubectl -n agentgateway-system get pods
  kubectl -n agentgateway-system get events --sort-by=.lastTimestamp | tail -15
  exit 1
fi

echo "==> conformance"
# ONCE, with the exit code preserved. An earlier version piped a first run
# through grep (discarding its status) and then ran the whole suite AGAIN against
# the resources and Events the first run had left behind — so a stale
# AgentGatewayNackError from run one could satisfy run two.
set -o pipefail
go test -tags cluster ./test/conformance/... -count=1 -v 2>&1 | tee /tmp/conformance.log
status=${PIPESTATUS[0]}
set +o pipefail

# A SKIP is not a PASS. `go test` exits 0 for a skipped test, so a suite whose
# load-bearing test went inconclusive would report success and the claim it
# guards would go unverified while CI stayed green. Every test here is a
# contract test against a live agentgateway: if one could not reach a verdict,
# this run proved less than it claims to have proved, and must go red.
if grep -q -- '--- SKIP' /tmp/conformance.log; then
  echo
  echo "FAIL: the cluster suite skipped a test. This gate makes claims about a"
  echo "running dataplane; a skipped test makes none of them. Skipped:"
  grep -- '--- SKIP' /tmp/conformance.log
  status=1
fi

exit "$status"

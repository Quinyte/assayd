#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
# Provision a throwaway cluster, install the pinned agentgateway, run the cluster
# conformance suite, tear down. Separate from CLUSTER=assayd-local so `make e2e`
# is unaffected.
#
# TWO phases, each on its own cluster, because the suite measures two releases:
#
#   - AGW_VERSION (1.4.1), the release design 03 is pinned to, for every test
#     but the first slice's. Two of them rest on `burst: -1`, which 1.5.0
#     refuses at admission (docs/research/agentgateway-v1.5.0-delta-2026-09.md).
#   - SLICE_AGW_VERSION (1.5.0), the release the operator's e2e runs, for the
#     first slice's cases (TestSlice*, design 03 §8.1). Each fails, never
#     skips, on a cluster whose controller is another release.
#
# A skipped conformance run is an unverified dependency contract, so this fails
# loudly rather than skipping when a prerequisite is missing.
set -euo pipefail

CLUSTER="${CONF_CLUSTER:-assayd-conformance}"
SLICE_CLUSTER="${CLUSTER}-slice"
AGW_VERSION="${AGW_VERSION:-1.4.1}"
SLICE_AGW_VERSION="${SLICE_AGW_VERSION:-1.5.0}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.6.0}"
KEEP="${KEEP:-0}"

for tool in k3d kubectl helm go; do
  command -v "$tool" >/dev/null || { echo "conformance needs $tool on PATH"; exit 1; }
done

# The run's own log, never a fixed path: two runs on one machine, or two
# worktrees, would otherwise read each other's `--- SKIP` lines.
LOG="$(mktemp "${TMPDIR:-/tmp}/conformance.XXXXXX")"

cleanup() {
  if [ "$KEEP" = "1" ]; then
    echo "==> KEEP=1, leaving clusters $CLUSTER and $SLICE_CLUSTER up"
  else
    k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true
    k3d cluster delete "$SLICE_CLUSTER" >/dev/null 2>&1 || true
  fi
  echo "==> log: $LOG"
}
trap cleanup EXIT

# provision CLUSTER VERSION: a fresh k3d cluster with Gateway API and one
# agentgateway release, and KUBECONFIG pointed at it alone.
provision() {
  local cluster="$1" version="$2"
  echo "==> creating $cluster"
  k3d cluster delete "$cluster" >/dev/null 2>&1 || true
  k3d cluster create "$cluster" --agents 0 --wait --timeout 180s \
    --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
  KUBECONFIG="$(k3d kubeconfig write "$cluster")"
  export KUBECONFIG

  echo "==> Gateway API $GWAPI_VERSION"
  kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null

  echo "==> agentgateway $version"
  helm install agw-crds "oci://ghcr.io/agentgateway/charts/agentgateway-crds" --version "$version" \
    -n agentgateway-system --create-namespace >/dev/null
  helm install agw "oci://ghcr.io/agentgateway/charts/agentgateway" --version "$version" \
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
    return 1
  fi
}

# run_suite ARGS...: the suite, ONCE, with the exit code preserved. An earlier
# version piped a first run through grep (discarding its status) and then ran the
# whole suite AGAIN against the resources and Events the first run had left
# behind — so a stale AgentGatewayNackError from run one could satisfy run two.
run_suite() {
  local status
  set +e
  go test -tags cluster ./test/conformance/... -count=1 -v -timeout 30m "$@" 2>&1 | tee -a "$LOG"
  status=${PIPESTATUS[0]}
  set -e
  return "$status"
}

status=0

echo "==> phase 1: the dependency contract, on agentgateway $AGW_VERSION"
provision "$CLUSTER" "$AGW_VERSION"
run_suite -skip '^TestSlice' || status=1
[ "$KEEP" = "1" ] || k3d cluster delete "$CLUSTER" >/dev/null 2>&1 || true

echo "==> phase 2: the first slice's cases, on agentgateway $SLICE_AGW_VERSION"
provision "$SLICE_CLUSTER" "$SLICE_AGW_VERSION"
CONF_SLICE_AGW_VERSION="$SLICE_AGW_VERSION" run_suite -run '^TestSlice' || status=1

# A SKIP is not a PASS. `go test` exits 0 for a skipped test, so a suite whose
# load-bearing test went inconclusive would report success and the claim it
# guards would go unverified while CI stayed green. Every test here is a
# contract test against a live agentgateway: if one could not reach a verdict,
# this run proved less than it claims to have proved, and must go red.
if grep -q -- '--- SKIP' "$LOG"; then
  echo
  echo "FAIL: the cluster suite skipped a test. This gate makes claims about a"
  echo "running dataplane; a skipped test makes none of them. Skipped:"
  grep -- '--- SKIP' "$LOG"
  status=1
fi

exit "$status"

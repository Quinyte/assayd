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

# Everything this run names is keyed off ONE id, as in hack/e2e.sh: the
# seconds and the pid, so two runs started in the same second on one host do
# not share a name. The seconds are taken modulo 10^6 because k3d refuses a
# cluster name over 32 characters, and the slice cluster's name is this one
# plus `-slice`: `assayd-cf-` (10) + 6 + `-` + a pid of up to 7 digits + 6 is
# 30 at most. Two runs collide only with the same pid in the same second,
# eleven days apart.
RUN_ID="$(( $(date +%s) % 1000000 ))-$$"

# Cluster naming. Until 2026-09-24 the default was a fixed name,
# `assayd-conformance`, and each phase began by DELETING the cluster of that
# name — so a second run started while a first was in phase 2 deleted the
# first's cluster under it, and each run's `k3d kubeconfig write` went to the
# same file under ~/.config/k3d. A run that is not told a name now invents one
# nobody else can be using, and deletes it again on exit. Naming one with
# CONF_CLUSTER is a deliberate choice to share, exactly as before: the
# harness still deletes and re-creates that name, so two runs that name the
# same cluster collide on purpose.
CLUSTER="${CONF_CLUSTER:-assayd-cf-${RUN_ID}}"
SLICE_CLUSTER="${CLUSTER}-slice"
AGW_VERSION="${AGW_VERSION:-1.4.1}"
SLICE_AGW_VERSION="${SLICE_AGW_VERSION:-1.5.0}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.6.0}"
KEEP="${KEEP:-0}"

# k3d refuses a cluster name over 32 characters, and a CONF_CLUSTER long
# enough to push `-slice` past it would otherwise fail only after phase 1 had
# run.
if [ "${#SLICE_CLUSTER}" -gt 32 ]; then
  echo "CONF_CLUSTER '$CLUSTER' is too long: '$SLICE_CLUSTER' exceeds k3d's 32-character limit"
  exit 1
fi

for tool in k3d kubectl helm go docker; do
  command -v "$tool" >/dev/null || { echo "conformance needs $tool on PATH"; exit 1; }
done

# The run's own log, never a fixed path, so a run never reads another run's
# `--- SKIP` lines.
LOG="$(mktemp "${TMPDIR:-/tmp}/conformance.XXXXXX")"

# THE RUN GETS ITS OWN KUBECONFIG, one file per cluster, for hack/e2e.sh's
# reason: kubectl, helm and `go test` all read $KUBECONFIG, so exporting a file
# nobody else can name is what carries the isolation to every call site.
# provision writes each phase's file and exports it; nothing is seeded from
# the environment. Until then KUBECONFIG names an empty file of this run's, so
# no tool can fall back to the shared default.
KUBECONFIG="$(mktemp "${TMPDIR:-/tmp}/assayd-conf-kubeconfig.XXXXXX")"
export KUBECONFIG
KUBECONFIGS=("$KUBECONFIG")
# KEPT pairs each cluster with its kubeconfig, for KEEP=1's message.
KEPT=()

# The image tar preload writes, global so that cleanup removes it when the run
# is interrupted mid-import: it is image-sized.
IMG_TAR=""

# CREATED lists the clusters this run has created, so cleanup deletes exactly
# those — and their docker networks. `k3d cluster delete` leaves `k3d-<name>`
# behind when another container is still attached to it, and such networks,
# leaked by the dozen, exhaust Docker's address pools (hack/e2e.sh). So every
# container still attached is disconnected first, and the network removed.
CREATED=()
delete_cluster() {
  local cluster="$1" net c
  k3d cluster delete "$cluster" >/dev/null 2>&1 || true
  net="k3d-${cluster}"
  if docker network inspect "$net" >/dev/null 2>&1; then
    for c in $(docker network inspect "$net" --format '{{range .Containers}}{{.Name}} {{end}}' 2>/dev/null); do
      docker network disconnect -f "$net" "$c" >/dev/null 2>&1 || true
    done
    docker network rm "$net" >/dev/null 2>&1 \
      || echo "WARNING: could not remove docker network $net; remove it by hand, or Docker will run out of address pools" >&2
  fi
}
cleanup() {
  rm -f "$IMG_TAR"
  if [ "$KEEP" = "1" ]; then
    echo "==> KEEP=1, leaving clusters up with their kubeconfigs: ${KEPT[*]:-none}"
  else
    for c in "${CREATED[@]+"${CREATED[@]}"}"; do
      echo "==> deleting $c, a cluster this run created"
      delete_cluster "$c"
    done
    rm -f "${KUBECONFIGS[@]}"
  fi
  echo "==> log: $LOG"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# preload CLUSTER IMAGE...: put agentgateway's two images into the node from
# the HOST, as hack/e2e.sh does, rather than let the node pull them.
# Measured on 2026-09-24 on a fresh conformance cluster: the node was still
# pulling cr.agentgateway.dev/agentgateway:v1.5.0 after ten minutes, the
# Gateway's proxy never became ready, and every case timed out at the fixture.
#
# hack/e2e.sh's reason for the single-platform save applies here too: `k3d
# image import` of a multi-platform index the host holds one platform of
# fails and still exits 0. So each image is saved for the node's platform —
# or, for an image that has no such platform (the linux/386 curl the 1.4.1
# phase uses), saved plainly — and the node is then asked whether it has it.
# Best effort: a failure warns, and the node pulls as before.
#
# Only TAGGED images: the suite's own digest-pinned images (curl, agnhost) do
# not survive this route. `docker save` of a `repo@sha256:` reference writes
# no repository tag, `k3d image import` still reports success, and the node
# then holds no image by that reference — measured on 2026-09-24 by this
# change's review. Those the node pulls itself; none of them was slow.
preload() {
  local cluster="$1"; shift
  local arch tar img err
  arch="$(docker version --format '{{.Server.Arch}}' 2>/dev/null || echo amd64)"
  IMG_TAR="$(mktemp "${TMPDIR:-/tmp}/assayd-conf-img.XXXXXX")"
  tar="$IMG_TAR"
  for img in "$@"; do
    if ! docker image inspect "$img" >/dev/null 2>&1 \
      && ! err="$(docker pull -q --platform "linux/${arch}" "$img" 2>&1)" \
      && ! err="$(docker pull -q "$img" 2>&1)"; then
      echo "WARNING: could not pull $img on the host (${err##*$'\n'}); the node will pull it" >&2
      continue
    fi
    docker save --platform "linux/${arch}" -o "$tar" "$img" >/dev/null 2>&1 \
      || docker save -o "$tar" "$img" >/dev/null 2>&1 || true
    err="$(k3d image import "$tar" -c "$cluster" 2>&1)" || true
    if ! docker exec "k3d-${cluster}-server-0" crictl inspecti "$img" >/dev/null 2>&1; then
      echo "WARNING: $img is not on the node after import ($(printf '%s' "$err" | grep -iE 'erro|fail' | tail -1)); the node will pull it" >&2
    fi
  done
  rm -f "$tar"
}

# provision CLUSTER VERSION: a fresh k3d cluster with Gateway API and one
# agentgateway release, and the run's KUBECONFIG holding it alone.
provision() {
  local cluster="$1" version="$2"
  echo "==> creating $cluster"
  delete_cluster "$cluster"
  # Recorded before the create, so a create that fails half-way is still
  # deleted on exit.
  CREATED+=("$cluster")
  k3d cluster create "$cluster" --agents 0 --wait --timeout 180s \
    --kubeconfig-update-default=false --kubeconfig-switch-context=false >/dev/null
  # Into a file of THIS run's, one per cluster, never
  # ~/.config/k3d/kubeconfig-<name>.yaml, which is keyed by the cluster name
  # alone. One per cluster so that KEEP=1 leaves a way into each.
  KUBECONFIG="$(mktemp "${TMPDIR:-/tmp}/assayd-conf-kubeconfig.XXXXXX")"
  export KUBECONFIG
  KUBECONFIGS+=("$KUBECONFIG")
  KEPT+=("$cluster=$KUBECONFIG")
  k3d kubeconfig get "$cluster" >"$KUBECONFIG"

  preload "$cluster" "cr.agentgateway.dev/controller:v${version}" \
    "cr.agentgateway.dev/agentgateway:v${version}"

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
[ "$KEEP" = "1" ] || delete_cluster "$CLUSTER"

echo "==> phase 2: the first slice's cases, on agentgateway $SLICE_AGW_VERSION"
provision "$SLICE_CLUSTER" "$SLICE_AGW_VERSION"
CONF_SLICE_AGW_VERSION="$SLICE_AGW_VERSION" run_suite -run '^TestSlice' || status=1

# A `-run` that matches nothing is green: `go test` prints "no tests to run"
# and exits 0, with no SKIP line to catch. So every TestSlice function must
# have reported a PASS, and there must be at least one.
want=$(grep -h '^func TestSlice' test/conformance/*_test.go | wc -l | tr -d ' ')
passed=$(grep -c -- '^--- PASS: TestSlice' "$LOG" || true)
if [ "$want" -lt 1 ] || [ "$passed" -ne "$want" ]; then
  echo
  echo "FAIL: phase 2 declares $want TestSlice cases and $passed of them passed."
  status=1
fi

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

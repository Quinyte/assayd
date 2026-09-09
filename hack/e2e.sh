#!/usr/bin/env bash
# e2e against a real cluster — design 07 §5's matrix, runnable locally.
#
# Deliberately fails loudly rather than skipping: a skipped e2e is an untested
# feature. DISTRO selects the cluster the suite runs against; k3d is created
# here, kind is expected to exist already (CI's kind-action makes it).
set -euo pipefail

DISTRO="${DISTRO:-k3d}"
CLUSTER="${CLUSTER:-assayd-local}"
# The tag is UNIQUE PER RUN, and that is not cosmetic.
#
# With a fixed tag and pullPolicy: Never, `helm upgrade` sees an unchanged
# Deployment spec and does not restart the pod — so the freshly built image sits
# in the cluster's image store while the OLD binary keeps running. Measured on
# 2026-09-01: the operator pod had been up for nine days, and every e2e run in
# that window tested a nine-day-old binary and reported green.
#
# A unique tag changes the pod template, so Kubernetes must roll it, and
# `--wait` then means what it appears to mean.
IMAGE_TAG="e2e-$(date +%s)"
IMAGE="assayd-operator:${IMAGE_TAG}"

# The agent image the suite deploys, and why it needs a registry at all.
#
# Until 2026-09-06 every e2e ran `registry.k8s.io/pause` — a container that
# serves nothing — so no test in this repository ever sent a request to an
# agent. Replacing it with a real responder runs into two rules the operator
# enforces on purpose: the CRD refuses an image that is not digest-pinned, and
# the rendered workload sets imagePullPolicy: Always. Together they mean a
# locally-built image cannot be `k3d image import`ed and referenced — the
# kubelet will contact a registry regardless of what is already in its store.
#
# So the suite runs its own registry. This is not scaffolding around the rules;
# it is the fixture being held to the same rules a real agent image is.
REG_NAME="assayd-e2e-registry"
REG_PORT="5111"
RESPONDER_REPO="assayd-responder"

case "${DISTRO}" in
k3d)
  command -v k3d >/dev/null || { echo "k3d is not installed"; exit 1; }
  # EXISTS is not RUNNING, and the difference is a 240s ImagePullBackOff.
  # An interrupted run left the registry container in state `Created`: k3d
  # listed it, this check passed, the cluster was wired to a mirror pointing at
  # a name docker would not resolve, and every responder test failed with
  # "no such host" while the harness reported the registry was fine.
  if ! k3d registry list -o json 2>/dev/null | grep -q "\"k3d-${REG_NAME}\""; then
    echo "==> creating registry k3d-${REG_NAME}"
    k3d registry create "${REG_NAME}" --port "${REG_PORT}" >/dev/null
  fi
  if [ "$(docker inspect -f '{{.State.Running}}' "k3d-${REG_NAME}" 2>/dev/null)" != "true" ]; then
    echo "==> registry k3d-${REG_NAME} exists but is not running; starting it"
    docker start "k3d-${REG_NAME}" >/dev/null
  fi
  # Prove it answers before anything depends on it. A registry that is "Up" but
  # not yet serving fails the same way, one race later.
  for _ in $(seq 1 20); do
    curl -sf "http://localhost:${REG_PORT}/v2/" >/dev/null 2>&1 && break
    sleep 1
  done
  if ! curl -sf "http://localhost:${REG_PORT}/v2/" >/dev/null 2>&1; then
    echo "ERROR: registry k3d-${REG_NAME} is not answering on localhost:${REG_PORT}." >&2
    echo "       The responder image cannot be pushed or pulled. Try:" >&2
    echo "         k3d registry delete k3d-${REG_NAME} && re-run" >&2
    exit 1
  fi
  if ! k3d cluster list -o json | grep -q "\"${CLUSTER}\""; then
    echo "==> creating k3d cluster ${CLUSTER}"
    k3d cluster create "${CLUSTER}" --agents 0 --wait \
      --registry-use "k3d-${REG_NAME}:${REG_PORT}"
  elif ! docker exec "k3d-${CLUSTER}-server-0" \
        cat /etc/rancher/k3s/registries.yaml 2>/dev/null \
      | grep -q "k3d-${REG_NAME}:${REG_PORT}"; then
    # Direct evidence, not an inference. k3d writes the mirror into the node's
    # registries.yaml at cluster-create time, so its presence is exactly the
    # thing that decides whether a pull by digest will resolve. A first version
    # of this check walked docker networks inside a pipeline subshell, where the
    # grep's exit status could not propagate — it reported a correctly wired
    # cluster as unwired, and a mutation run that never reached the tests then
    # read as SURVIVED.
    echo "ERROR: cluster ${CLUSTER} has no mirror for k3d-${REG_NAME}:${REG_PORT}, so the" >&2
    echo "       agent image cannot be pulled and the responder tests would not run." >&2
    echo "       The registry is wired in at cluster-create time and cannot be added here." >&2
    echo "       Recreate it:  k3d cluster delete ${CLUSTER}" >&2
    exit 1
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

# The responder needs a registry the cluster can pull from, and the registry is
# wired into a cluster at CREATE time. This script creates the k3d cluster and
# can do that; the kind cluster is created by CI's kind-action before this script
# runs, so it cannot. Rather than push to a registry that is not there — which is
# what a first version did, breaking the kind lane outright — the responder tests
# are k3d-only and say so where it is visible.
if [ "${DISTRO}" != "k3d" ]; then
  export ASSAYD_E2E_RESPONDER_SKIP="the responder needs a registry wired into the cluster at create time; ${DISTRO} clusters are created outside this script, so only the k3d lane runs them (design 02 §5)"
  echo "==> responder tests: NOT RUN on ${DISTRO} — ${ASSAYD_E2E_RESPONDER_SKIP}"
fi

if [ "${DISTRO}" = "k3d" ]; then
echo "==> building and pushing the responder (the e2e's agent image)"
# Pushed rather than imported, so the reference the Agent CR carries is a REAL
# repo digest resolved from a registry — the same path a production agent image
# takes, and the only one imagePullPolicy: Always will accept.
docker build ${DOCKER_BUILD_NETWORK:+--network "${DOCKER_BUILD_NETWORK}"} \
  -t "localhost:${REG_PORT}/${RESPONDER_REPO}:${IMAGE_TAG}" \
  -f test/responder/Dockerfile .
docker push "localhost:${REG_PORT}/${RESPONDER_REPO}:${IMAGE_TAG}" >/dev/null
RESPONDER_DIGEST="$(docker inspect \
  --format '{{index .RepoDigests 0}}' \
  "localhost:${REG_PORT}/${RESPONDER_REPO}:${IMAGE_TAG}" | cut -d@ -f2)"
if [ -z "${RESPONDER_DIGEST}" ]; then
  echo "ERROR: could not read the responder's repo digest after pushing" >&2
  exit 1
fi
# The NODE resolves the registry by its container name, not by localhost.
export ASSAYD_E2E_RESPONDER_IMAGE="k3d-${REG_NAME}:${REG_PORT}/${RESPONDER_REPO}@${RESPONDER_DIGEST}"
echo "    responder: ${ASSAYD_E2E_RESPONDER_IMAGE}"
fi

echo "==> loading the image into ${DISTRO}"
case "${DISTRO}" in
k3d)  k3d image import "${IMAGE}" -c "${CLUSTER}" ;;
kind) kind load docker-image "${IMAGE}" --name "${CLUSTER}" ;;
esac

echo "==> installing the chart"
# CRDs ship in the chart's crds/ directory, so this installs them too.
helm upgrade --install assayd charts/assayd \
  -f charts/assayd/values-local.yaml \
  --set operator.image.repository=assayd-operator \
  --set operator.image.tag="${IMAGE_TAG}" \
  --set operator.image.pullPolicy=Never \
  --wait --timeout 5m

# The suite asserts it is talking to THIS build, so a stale pod can never again
# look like a passing run.
export ASSAYD_E2E_IMAGE="${IMAGE}"

echo "==> running e2e suite"
ASSAYD_E2E=1 go test ./test/e2e/... -count=1 -timeout 20m -v

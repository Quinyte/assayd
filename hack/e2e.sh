#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Quinyte
# SPDX-License-Identifier: Apache-2.0
# e2e against a real cluster — design 07 §5's matrix, runnable locally.
#
# Deliberately fails loudly rather than skipping: a skipped e2e is an untested
# feature. DISTRO selects the cluster the suite runs against; k3d is created
# here, kind is expected to exist already (CI's kind-action makes it).
set -euo pipefail

DISTRO="${DISTRO:-k3d}"
KEEP="${KEEP:-0}"

# Everything this run names is keyed off ONE id, so two runs at once cannot
# share a cluster, an image tag or a kubeconfig by accident.
#
# The seconds alone are not enough: two runs started in the same second get the
# same IMAGE_TAG, and so the same `assayd-operator:e2e-<n>` in the same image
# store. The pid is what makes the id unique among runs on one host.
RUN_ID="$(date +%s)-$$"

# THE RUN GETS ITS OWN KUBECONFIG, and that is not hygiene — it is the
# difference between a result and a coincidence.
#
# Until 2026-09-22 this script ran `kubectl config use-context` against the
# SHARED default kubeconfig. Two runs at once — routine here, because several
# agents work in parallel — each flipped the global current-context under the
# other, so a run could build one operator image and then measure the
# deployment another run had just made. Measured by an independent reviewer:
#
#   TestTheOperatorUnderTestIsTheOneJustBuilt: the deployed operator is
#   e2e-1790099107 and this run built e2e-1790098449 — every assertion in this
#   suite is about the wrong binary
#
# and that self-check is the ONLY thing standing between a collision and a
# false green: a colliding run does not reliably fail, it can report PASS for a
# build that was never under test.
#
# So the run's kubeconfig is a file nobody else can name, exported before the
# first cluster tool runs. kubectl, helm, k3d, kind and `go test` all read
# $KUBECONFIG, so the export is what carries the isolation to every call site,
# rather than a flag that has to be repeated on each one and can be forgotten
# on one. It is deliberately NOT seeded from the environment: a caller whose
# KUBECONFIG already points at the shared default would inherit exactly the bug
# this removes.
KUBECONFIG="$(mktemp "${TMPDIR:-/tmp}/assayd-e2e-kubeconfig.XXXXXX")"
export KUBECONFIG

# The trap is armed HERE, on the line after the file exists, and before anything
# that can exit. An earlier version armed it after the DISTRO check below, so
# `DISTRO=bogus ./hack/e2e.sh` created a kubeconfig and exited past the cleanup
# that would have removed it — the one leak a run can produce without ever
# reaching a cluster. Cleanup runs on failure and on interrupt as well as on a
# clean exit: the INT and TERM traps exit, and that is what fires the EXIT trap.
# KEEP=1 is the only way to hold on to either artefact, and it says where they
# are. OWNED_CLUSTER is empty until a cluster this run named is created.
OWNED_CLUSTER=""
cleanup() {
  if [ "${KEEP}" = "1" ]; then
    echo "==> KEEP=1: kubeconfig ${KUBECONFIG} kept${OWNED_CLUSTER:+, cluster ${OWNED_CLUSTER} left up}"
    return
  fi
  rm -f "${KUBECONFIG}"
  if [ -n "${OWNED_CLUSTER}" ]; then
    echo "==> deleting ${OWNED_CLUSTER}, the cluster this run created"
    k3d cluster delete "${OWNED_CLUSTER}" >/dev/null 2>&1 || true
    # `--registry-use` attaches the SHARED registry to this cluster's network,
    # and `k3d cluster delete` leaves a network that still has a container on
    # it. So every run leaked one network, and after a few dozen Docker ran out
    # of address pools and no cluster could be created on this machine
    # ("all predefined address pools have been fully subnetted"). Detach the
    # registry from THIS run's network and remove it; the registry itself is
    # shared between runs and stays.
    local net="k3d-${OWNED_CLUSTER}"
    if docker network inspect "${net}" >/dev/null 2>&1; then
      docker network disconnect "${net}" "k3d-${REG_NAME:-assayd-e2e-registry}" >/dev/null 2>&1 || true
      # Not fatal — cleanup must not fail the run — but never silent: a
      # network this cannot remove is the leak this block exists to stop.
      docker network rm "${net}" >/dev/null 2>&1 \
        || echo "WARNING: could not remove docker network ${net}; remove it by hand, or Docker will run out of address pools" >&2
    fi
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# Cluster naming, and why the default is no longer a fixed name.
#
# A private kubeconfig isolates which cluster a run TALKS to; it does nothing
# about two runs talking to the SAME one. With the old default both runs
# helm-installed into `assayd-local`, the second overwrote the first's
# Deployment, and the first then measured the second's binary — the same
# corruption, reached without touching a kubeconfig at all.
#
# So a k3d run that is not told a cluster name invents one nobody else can be
# using, and deletes it again on exit. Naming one is a deliberate choice to
# share: the caller owns that cluster, this script never deletes it, and two
# runs that name the same cluster are colliding on purpose.
#
# kind clusters are created outside this script (CI's kind-action makes one),
# so there is nothing there for a unique name to create.
#
# That leaves kind NOT isolated locally: two concurrent DISTRO=kind runs both
# target kind-assayd-local and both `helm upgrade --install assayd` into it,
# which is the collision this script closes for k3d. In CI each job has its own
# VM, so nothing collides there. Locally, give each concurrent kind run its own
# CLUSTER, created beforehand with `kind create cluster --name`.
CLUSTER_IS_OURS=0
case "${DISTRO}" in
k3d)
  if [ -z "${CLUSTER:-}" ]; then
    CLUSTER="assayd-e2e-${RUN_ID}"
    CLUSTER_IS_OURS=1
  fi
  ;;
kind)
  CLUSTER="${CLUSTER:-assayd-local}"
  ;;
*)
  echo "DISTRO must be k3d or kind, got ${DISTRO}" >&2; exit 1 ;;
esac

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
IMAGE_TAG="e2e-${RUN_ID}"
IMAGE="assayd-operator:${IMAGE_TAG}"

echo "==> run ${RUN_ID}: distro ${DISTRO}, cluster ${CLUSTER}, image ${IMAGE}"

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
MCPSERVER_REPO="assayd-mcpserver"
# Pinned, not floated. agentgateway is the component every governance claim in
# this project rests on, and design 03 is written against a specific release.
GWAPI_VERSION="${GWAPI_VERSION:-v1.6.0}"
AGW_VERSION="${AGW_VERSION:-1.5.0}"
GATEWAY_NS="${GATEWAY_NS:-assayd-gateway}"
TOOLS_NS="${TOOLS_NS:-assayd-e2e-tools}"
# The identity the chart is given as admission.toolRouteWriters (design 07
# A6.15), and the one the MCP tests author their tool route as. A plain
# username with no RBAC of its own: the value lifts the admission refusal and
# grants nothing, so the tests grant it httproutes in the tools namespace, as
# an administrator would.
TOOL_ROUTE_WRITER="${TOOL_ROUTE_WRITER:-assayd-e2e-tool-author}"

case "${DISTRO}" in
k3d)
  command -v k3d >/dev/null || { echo "k3d is not installed"; exit 1; }
  # EXISTS is not RUNNING, and the difference is a 240s ImagePullBackOff.
  # An interrupted run left the registry container in state `Created`: k3d
  # listed it, this check passed, the cluster was wired to a mirror pointing at
  # a name docker would not resolve, and every responder test failed with
  # "no such host" while the harness reported the registry was fine.
  # `grep >/dev/null`, never `grep -q`, on a pipe under pipefail: -q exits at
  # the first match, the producer takes SIGPIPE, and the pipeline reports
  # FAILURE for a registry that exists — the script then tries to create it
  # and dies on "already exists". Plain grep reads all its input. The same
  # rule applies to every `cmd | grep` below.
  # The registry is SHARED between concurrent runs on purpose — it is keyed by a
  # fixed name and port, every image in it is tagged with the run's own RUN_ID,
  # and a second copy could not bind ${REG_PORT} anyway. Which means two runs
  # can both find it missing and both try to create it: the loser used to die on
  # "already exists" under `set -e`, turning a cosmetic race into a failed run.
  # Re-check instead of swallowing the error, so a create that failed for any
  # OTHER reason still stops the run and says so.
  if ! k3d registry list -o json 2>/dev/null | grep "\"k3d-${REG_NAME}\"" >/dev/null; then
    echo "==> creating registry k3d-${REG_NAME}"
    if ! REG_ERR="$(k3d registry create "${REG_NAME}" --port "${REG_PORT}" 2>&1)"; then
      if k3d registry list -o json 2>/dev/null | grep "\"k3d-${REG_NAME}\"" >/dev/null; then
        echo "    another run created it first; using it"
      else
        echo "ERROR: could not create registry k3d-${REG_NAME} on port ${REG_PORT}:" >&2
        echo "${REG_ERR}" >&2
        exit 1
      fi
    fi
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
  if ! k3d cluster list -o json | grep "\"${CLUSTER}\"" >/dev/null; then
    echo "==> creating k3d cluster ${CLUSTER}"
    # Only a cluster this run both NAMED and CREATED is this run's to delete; a
    # cluster the caller named is the caller's, whether it existed already or
    # this run had to make it. Ownership is claimed BEFORE the create, not
    # after: a create that fails its --wait, or is interrupted, can leave a
    # half-built cluster behind, and under an invented name nobody else can
    # find it to delete by hand. `k3d cluster delete` on a cluster that never
    # came into being is harmless, so claiming early costs nothing.
    if [ "${CLUSTER_IS_OURS}" = "1" ]; then OWNED_CLUSTER="${CLUSTER}"; fi
    # --kubeconfig-update-default=false is belt and braces beside the KUBECONFIG
    # export above: it is the flag that makes "never touch the shared file" a
    # property of the command rather than of the environment it inherited.
    # --kubeconfig-switch-context=false for the same reason — switching the
    # current context of a file two other runs are reading IS the bug.
    k3d cluster create "${CLUSTER}" --agents 0 --wait \
      --registry-use "k3d-${REG_NAME}:${REG_PORT}" \
      --kubeconfig-update-default=false --kubeconfig-switch-context=false
  elif ! docker exec "k3d-${CLUSTER}-server-0" \
        cat /etc/rancher/k3s/registries.yaml 2>/dev/null \
      | grep "k3d-${REG_NAME}:${REG_PORT}" >/dev/null; then
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
  # Write the context into THIS RUN'S kubeconfig, whether the cluster was made a
  # moment ago or already existed. `k3d kubeconfig get` prints to stdout and
  # touches no file of its own, so nothing outside ${KUBECONFIG} is written and
  # nothing outside it is read — where the old `merge --kubeconfig-merge-default`
  # plus `config use-context` pair mutated the shared default that every other
  # run and every other tool on this machine reads.
  #
  # Generating it unconditionally also keeps the property the merge was there
  # for: a cluster outlives its kubeconfig entry — restarting the Docker VM, or
  # reusing a cluster from an earlier session, leaves the cluster running with
  # no context pointing at it — and the run then fails on something unrelated to
  # what it is testing.
  echo "==> writing this run's kubeconfig for ${CLUSTER}"
  k3d kubeconfig get "${CLUSTER}" > "${KUBECONFIG}"
  ;;
kind)
  command -v kind >/dev/null || { echo "kind is not installed"; exit 1; }
  # --kubeconfig, not the default file: `kind export kubeconfig` sets the
  # current-context in whatever file it writes, and the file it writes must be
  # this run's alone.
  kind export kubeconfig --name "${CLUSTER}" --kubeconfig "${KUBECONFIG}" >/dev/null
  ;;
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
# Design 07 A5.4: "Enforcement is the CNI's, not Kubernetes'." A NetworkPolicy on
# a cluster whose CNI does not implement the API is ACCEPTED and does nothing --
# it fails green, which is the worst way for a control to fail. k3s runs a
# network-policy controller; kind's default CNI does not. So the suite is told
# which lane it is in and reports the axis unverified rather than claiming it.
#
# Measured on k3d/k3s v1.33.6 before this was written: with a deny-all ingress
# policy a cross-namespace request went from code=200 to code=000, and with an
# allow rule for one namespace that namespace got 200 while another got 000 at
# the same moment.
if [ "${DISTRO}" = "k3d" ]; then
  export ASSAYD_E2E_NETPOL_ENFORCED=1
else
  export ASSAYD_E2E_NETPOL_SKIP="NetworkPolicy enforcement is the CNI's; ${DISTRO}'s default CNI does not implement it, so a policy here is accepted and does nothing (design 07 A5.4)"
fi

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

echo "==> building and pushing the MCP tool server"
docker build ${DOCKER_BUILD_NETWORK:+--network "${DOCKER_BUILD_NETWORK}"} \
  -t "localhost:${REG_PORT}/${MCPSERVER_REPO}:${IMAGE_TAG}" \
  -f test/mcpserver/Dockerfile .
docker push "localhost:${REG_PORT}/${MCPSERVER_REPO}:${IMAGE_TAG}" >/dev/null
MCPSERVER_DIGEST="$(docker inspect \
  --format '{{index .RepoDigests 0}}' \
  "localhost:${REG_PORT}/${MCPSERVER_REPO}:${IMAGE_TAG}" | cut -d@ -f2)"
if [ -z "${MCPSERVER_DIGEST}" ]; then
  echo "ERROR: could not read the MCP server's repo digest after pushing" >&2
  exit 1
fi
export ASSAYD_E2E_MCP_IMAGE="k3d-${REG_NAME}:${REG_PORT}/${MCPSERVER_REPO}@${MCPSERVER_DIGEST}"
echo "    mcpserver: ${ASSAYD_E2E_MCP_IMAGE}"
fi

echo "==> loading the image into ${DISTRO}"
case "${DISTRO}" in
k3d)  k3d image import "${IMAGE}" -c "${CLUSTER}" ;;
kind) kind load docker-image "${IMAGE}" --name "${CLUSTER}" ;;
esac

# A REAL gateway, so that "governance becomes real at the gateway" can be
# measured rather than asserted.
#
# The chart carries no agentgateway subchart (design 07 A1), so the suite
# installs the dependency itself. ADR-0030 step 3 says to author and execute the
# exact resources FIRST and encode the mapping afterwards; the first half was
# design 07 A6 and the second is now in the operator — the serving route in the
# gateway test is EMITTED, not hand-authored. The weighted, MCP and authz
# resources are still hand-authored, because nothing emits those.
#
# This runs BEFORE the chart install, and the order is load-bearing: with
# gateway.enabled=true and no HTTPRoute kind served by the cluster the operator
# refuses to start (design 03 §3.1's broken-install row, fail-closed), so
# `helm --wait` would time out on a rollout that can never complete.
#
# The listener shape is design 03's, not invented here: `allowedRoutes.namespaces
# .from: Selector` matching `assayd.dev/run-namespace: "true"`, the label design
# 02 A42's operator stamps at run-namespace creation. A route whose namespace the
# listener does not admit is rejected, so this is the whole of the cross-namespace
# consent the run-namespace move needs.
if [ "${DISTRO}" = "k3d" ] && [ "${ASSAYD_E2E_GATEWAY:-1}" = "1" ]; then
  echo "==> installing Gateway API + agentgateway ${AGW_VERSION}"
  # Pull agentgateway's two images on the HOST and import them into the node,
  # rather than let the node pull them. Measured on 2026-09-23: a fresh k3d node
  # took 7m1s to pull the 29 MB controller image that the host pulled in ~10s,
  # so the install below (--wait --timeout 5m) failed on every fresh cluster
  # and e2e could only pass on a cluster that already had the image cached.
  # Both images are `IfNotPresent` in the chart, so an imported image is used
  # as-is. Best effort: if the host cannot pull, the node still tries, which
  # is the old behaviour.
  for agw_img in "cr.agentgateway.dev/controller:v${AGW_VERSION}" "cr.agentgateway.dev/agentgateway:v${AGW_VERSION}"; do
    if docker pull -q "${agw_img}" >/dev/null 2>&1; then
      k3d image import "${agw_img}" -c "${CLUSTER}" >/dev/null 2>&1 \
        || echo "WARNING: could not import ${agw_img} into ${CLUSTER}; the node will pull it" >&2
    else
      echo "WARNING: could not pull ${agw_img} on the host; the node will pull it" >&2
    fi
  done
  kubectl apply -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml" >/dev/null
  helm upgrade --install agentgateway-crds     oci://ghcr.io/agentgateway/charts/agentgateway-crds --version "${AGW_VERSION}"     -n agentgateway --create-namespace >/dev/null
  helm upgrade --install agentgateway     oci://ghcr.io/agentgateway/charts/agentgateway --version "${AGW_VERSION}"     -n agentgateway --wait --timeout 5m >/dev/null
  # The Gateway gets its OWN namespace, and it is NOT PodSecurity-restricted.
  #
  # Measured, not assumed: with the Gateway in assayd-system the data-plane
  # Deployment was created and its ReplicaSet could never make a pod --
  #   violates PodSecurity "restricted:latest": seccompProfile (pod or container
  #   "agentgateway" must set securityContext.seccompProfile.type ...)
  # -- because charts/assayd/templates/namespace.yaml enforces `restricted` on
  # assayd-system and agentgateway v1.5.0's generated proxy sets no
  # seccompProfile. It is the ONLY field it fails on, and AgentgatewayParameters
  # v1alpha1 cannot supply it: `.spec.deployment` overrides `metadata` only
  # (labels and annotations), with no pod securityContext anywhere in the schema.
  # So this is not a policy assayd can meet by configuring the vendor.
  #
  # Design 03 3.2 already says gateway-scoped resources live in "the gateway's
  # own namespace", so a separate namespace is that design's shape rather than a
  # workaround for this. assayd-system stays `restricted`; the vendored data
  # plane gets `baseline`, which it does satisfy.
  echo "==> creating the Gateway in its own namespace"
  # THE DELIMITER IS QUOTED, so nothing in this block is expanded by the shell.
  #
  # `<<GWEOF` expanded all of it, prose comments included, and the backticked
  # word in the `tools` listener's comment below therefore RAN on every single
  # run — `./hack/e2e.sh: line 255: http: command not found` was printed by this
  # script, not by anything it called. That one was harmless; a `$(...)` written
  # into the same comment would not have been, and a manifest whose comments
  # execute is a manifest nobody can safely annotate.
  #
  # The block needs exactly two values from the run, so they are substituted by
  # name afterwards rather than by handing the shell the whole document.
  sed -e "s|__GATEWAY_NS__|${GATEWAY_NS}|g" \
      -e "s|__TOOLS_NS__|${TOOLS_NS}|g" <<'GWEOF' | kubectl apply -f - >/dev/null
apiVersion: v1
kind: Namespace
metadata:
  name: __GATEWAY_NS__
  labels:
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/enforce-version: latest
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: assayd
  namespace: __GATEWAY_NS__
spec:
  gatewayClassName: agentgateway
  listeners:
  - name: http
    port: 8080
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            assayd.dev/run-namespace: "true"
  # A second listener for the MCP tool server, which is NOT an agent and does
  # not belong in an operator-owned run namespace. It is selected by the
  # namespace's own kubernetes.io/metadata.name rather than by an assayd.dev
  # label: those keys are reserved to the operator by A5.9's admission policy,
  # and a test that minted one would be forging the very authority that policy
  # exists to hold. Routes here are authored by TOOL_ROUTE_WRITER, the chart's
  # admission.toolRouteWriters: assayd-gateway-routes admits it on this
  # listener and refuses it on `http`, which stays the operator's (design 07
  # A6.15). Until A6.15 the tests impersonated the operator to write one,
  # because the policy reserved every route naming this Gateway to it.
  - name: tools
    port: 8081
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            kubernetes.io/metadata.name: __TOOLS_NS__
GWEOF
  kubectl create ns "${TOOLS_NS}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl -n "${GATEWAY_NS}" wait --for=condition=Programmed gateway/assayd --timeout=180s >/dev/null
  # Programmed is NOT enough. The first run of this harness reported a Programmed
  # Gateway whose Service had zero endpoints, and the test then failed on a
  # connection refused that the harness had already called healthy. Wait for the
  # data plane the Service actually routes to.
  # 600s, not 180s: a cold pull of the agentgateway image alone took over 3m.
  kubectl -n "${GATEWAY_NS}" rollout status deploy/assayd --timeout=600s >/dev/null
  export ASSAYD_E2E_GATEWAY_NS="${GATEWAY_NS}"
  export ASSAYD_E2E_GATEWAY_NAME="assayd"
  export ASSAYD_E2E_TOOLS_NS="${TOOLS_NS}"
  # The operator is told the same three things the tests derive the emitted
  # route's identity from. The suffix is the chart's own default and is passed
  # explicitly, so a change to that default breaks this line rather than
  # silently moving every hostname the suite asserts on.
  # gateway.url is where agents send their EGRESS — tool calls. The operator
  # injects it into every agent as ASSAYD_GATEWAY_URL, and it names the `tools`
  # listener, because that is the listener the MCP routes attach to.
  GW_SVC=$(kubectl -n "${GATEWAY_NS}" get svc -l gateway.networking.k8s.io/gateway-name=assayd \
    -o jsonpath='{.items[0].metadata.name}')
  if [ -z "${GW_SVC}" ]; then
    echo "no Service for Gateway ${GATEWAY_NS}/assayd; cannot tell agents where their egress goes" >&2
    exit 1
  fi
  export ASSAYD_E2E_GATEWAY_URL="http://${GW_SVC}.${GATEWAY_NS}.svc.cluster.local:8081"
  # gateway.servingUrl names the `http` listener each <agent>-serving route
  # attaches to. The operator requires it with the gateway on: it publishes a
  # new Agent's route only after an anonymous request sent there gets 401 from
  # the Agent's <agent>-auth (design 03 §3.3.3), so a wrong value here holds
  # every gateway test's Agent unpublished.
  GATEWAY_SETTINGS=(--set gateway.enabled=true --set gateway.name=assayd
    --set gateway.hostnameSuffix=assayd.internal
    --set gateway.url="${ASSAYD_E2E_GATEWAY_URL}"
    --set gateway.servingUrl="http://${GW_SVC}.${GATEWAY_NS}.svc.cluster.local:8080")
  export ASSAYD_E2E_GATEWAY_HOSTNAME_SUFFIX="assayd.internal"
  echo "    gateway: ${GATEWAY_NS}/assayd, listener admits assayd.dev/run-namespace=true"
else
  # Every gateway-dependent test SKIPS on this reason and reports its axis
  # unverified, rather than failing. A test that fails because the harness did
  # not install a dependency is reporting on the harness, not on the platform --
  # and the kind lane found exactly that when these tests were k3d-only.
  if [ "${DISTRO}" = "k3d" ]; then
    # Deliberately off, on a lane that could have had one. It reads as a
    # contradiction otherwise: the old message said "only on k3d (DISTRO=k3d)".
    export ASSAYD_E2E_GATEWAY_SKIP="no gateway on this run: ASSAYD_E2E_GATEWAY=0 turned the install off deliberately, so the gateway path is UNVERIFIED here rather than broken. gateway.enabled is false, which is the tier TestTheDeclaredUngovernedTierEmitsNoRoute measures"
  else
    export ASSAYD_E2E_GATEWAY_SKIP="no gateway on this lane: hack/e2e.sh installs Gateway API and agentgateway only on k3d (DISTRO=${DISTRO}), so the gateway path is UNVERIFIED here rather than broken"
  fi
  echo "==> gateway tests: NOT RUN on ${DISTRO} — ${ASSAYD_E2E_GATEWAY_SKIP}"
  # gateway.enabled stays FALSE, which is what P1 ships. That is not a hole in
  # this run: it is the row design 03 §3.1 calls the declared-ungoverned tier,
  # and TestTheDeclaredUngovernedTierEmitsNoRoute measures it here.
  GATEWAY_SETTINGS=()
fi

echo "==> applying this build's CRDs"
# `helm upgrade` never updates a chart's crds/, so a reused cluster keeps the
# Agent CRD it was first installed with, and the suite would test this build's
# operator against an older schema. Measured on 2026-09-12: the CRD predated
# `status.auth`, the API server pruned every write of it, and the operator's
# `-auth` transaction could not keep its own record. Applied server-side, so the
# field manager can take over fields an earlier `helm install` set.
kubectl apply --server-side --force-conflicts -f charts/assayd/crds/ >/dev/null

echo "==> installing the chart"
# CRDs ship in the chart's crds/ directory, so a FIRST install creates them.
# gateway.namespace is set here and NOT only with the gateway block above,
# because it is what assayd-gateway-routes compares a route's parentRef against.
# Render it wrong and the reservation matches nothing and admits any author,
# silently. It is set on every distro so the rendered policy names the namespace
# the Gateway would occupy, rather than defaulting to the operator's.
# admission.toolRouteWriters is set on every distro too, because the policy it
# widens renders on every install.
helm upgrade --install assayd charts/assayd \
  -f charts/assayd/values-local.yaml \
  --set operator.image.repository=assayd-operator \
  --set operator.image.tag="${IMAGE_TAG}" \
  --set operator.image.pullPolicy=Never \
  --set gateway.namespace="${GATEWAY_NS}" \
  --set "admission.toolRouteWriters.users={${TOOL_ROUTE_WRITER}}" \
  ${GATEWAY_SETTINGS[@]+"${GATEWAY_SETTINGS[@]}"} \
  --wait --timeout 5m

# The suite asserts it is talking to THIS build, so a stale pod can never again
# look like a passing run.
export ASSAYD_E2E_IMAGE="${IMAGE}"
export ASSAYD_E2E_TOOL_ROUTE_WRITER="${TOOL_ROUTE_WRITER}"

echo "==> running e2e suite"
ASSAYD_E2E=1 go test ./test/e2e/... -count=1 -timeout 20m -v ${E2E_RUN:+-run "${E2E_RUN}"}

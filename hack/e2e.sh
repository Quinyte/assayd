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
MCPSERVER_REPO="assayd-mcpserver"
# Pinned, not floated. agentgateway is the component every governance claim in
# this project rests on, and design 03 is written against a specific release.
GWAPI_VERSION="${GWAPI_VERSION:-v1.6.0}"
AGW_VERSION="${AGW_VERSION:-1.5.0}"
GATEWAY_NS="${GATEWAY_NS:-assayd-gateway}"
TOOLS_NS="${TOOLS_NS:-assayd-e2e-tools}"

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
  if ! k3d registry list -o json 2>/dev/null | grep "\"k3d-${REG_NAME}\"" >/dev/null; then
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
  if ! k3d cluster list -o json | grep "\"${CLUSTER}\"" >/dev/null; then
    echo "==> creating k3d cluster ${CLUSTER}"
    k3d cluster create "${CLUSTER}" --agents 0 --wait \
      --registry-use "k3d-${REG_NAME}:${REG_PORT}"
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
  kubectl apply -f - >/dev/null <<GWEOF
apiVersion: v1
kind: Namespace
metadata:
  name: ${GATEWAY_NS}
  labels:
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/enforce-version: latest
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: assayd
  namespace: ${GATEWAY_NS}
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
  # exists to hold. Routes here are still authored by the operator identity --
  # assayd-gateway-routes reserves every route naming this Gateway, whichever
  # listener it attaches to.
  - name: tools
    port: 8081
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            kubernetes.io/metadata.name: ${TOOLS_NS}
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
  GATEWAY_SETTINGS=(--set gateway.enabled=true --set gateway.name=assayd
    --set gateway.hostnameSuffix=assayd.internal
    --set gateway.url="${ASSAYD_E2E_GATEWAY_URL}")
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

echo "==> installing the chart"
# CRDs ship in the chart's crds/ directory, so this installs them too.
# gateway.namespace is set here and NOT only with the gateway block above,
# because it is what assayd-gateway-routes compares a route's parentRef against.
# Render it wrong and the reservation matches nothing and admits any author,
# silently. It is set on every distro so the rendered policy names the namespace
# the Gateway would occupy, rather than defaulting to the operator's.
helm upgrade --install assayd charts/assayd \
  -f charts/assayd/values-local.yaml \
  --set operator.image.repository=assayd-operator \
  --set operator.image.tag="${IMAGE_TAG}" \
  --set operator.image.pullPolicy=Never \
  --set gateway.namespace="${GATEWAY_NS}" \
  ${GATEWAY_SETTINGS[@]+"${GATEWAY_SETTINGS[@]}"} \
  --wait --timeout 5m

# The suite asserts it is talking to THIS build, so a stale pod can never again
# look like a passing run.
export ASSAYD_E2E_IMAGE="${IMAGE}"

echo "==> running e2e suite"
ASSAYD_E2E=1 go test ./test/e2e/... -count=1 -timeout 20m -v ${E2E_RUN:+-run "${E2E_RUN}"}

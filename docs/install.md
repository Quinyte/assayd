# Installing assayd with a gateway

This page takes a fresh cluster to the point where one agent completes one A2A task through the gateway, and a caller without the right key is refused. Every step comes from `hack/e2e.sh` or the e2e suite in `test/e2e/`. Where a step here goes beyond what they do, it says so.

**What you get is authentication, not governance.** With the gateway on, the operator emits each Agent's route and, for an API-key Agent, one `AgentgatewayPolicy` that checks the caller's key and group. It emits nothing else. No budget, rate limit or tool filter is enforced for any agent, and no NetworkPolicy is created, so an agent Pod can still be reached directly, around the gateway. `README.md` (*What is NOT true today*) and `SECURITY.md` list the rest.

## Tested versions

These are the versions the e2e suite runs against. They are what has been measured, not a support guarantee.

| Component | Version | Source |
|---|---|---|
| Gateway API CRDs | `v1.6.0`, standard channel | `hack/e2e.sh` (`GWAPI_VERSION`) |
| agentgateway (CRDs and controller) | `1.5.0` | `hack/e2e.sh` (`AGW_VERSION`) |
| assayd chart | `0.3.0`, `oci://ghcr.io/quinyte/charts/assayd` | `docs/supply-chain.md` |
| Kubernetes | k3d (k3s). The chart requires `>=1.30.0` | `charts/assayd/Chart.yaml` |

The e2e runs its gateway tests on k3d only. The kind lane skips them and reports the gateway path as unverified.

## 1. Before you install the chart

The chart prints its notes only after a successful install, and three things must exist before that install can succeed. With `gateway.enabled: true` and the Gateway API or agentgateway CRDs absent, **the operator refuses to start** and its last log line names the missing kind. `helm install --wait` then times out. And the chart renders a Role into the Gateway's namespace, so `helm install` fails if that namespace is missing.

### 1.1 Gateway API CRDs

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.6.0/standard-install.yaml
```

### 1.2 agentgateway: CRDs and controller

Both charts, into the `agentgateway` namespace. The CRDs alone are not enough: the controller is what programs the Gateway and runs its data plane.

```bash
helm upgrade --install agentgateway-crds oci://ghcr.io/agentgateway/charts/agentgateway-crds \
  --version 1.5.0 -n agentgateway --create-namespace
helm upgrade --install agentgateway oci://ghcr.io/agentgateway/charts/agentgateway \
  --version 1.5.0 -n agentgateway --wait --timeout 5m
```

### 1.3 The Gateway's namespace, at PodSecurity `baseline`

The Gateway gets its own namespace. It **cannot** share `assayd-system`, which the chart labels PodSecurity `restricted`. agentgateway 1.5.0's generated proxy sets no `seccompProfile`, and `AgentgatewayParameters` offers no field to add one, so its Pods are refused under `restricted`. They do satisfy `baseline`.

### 1.4 The Gateway

Four things about it are fixed by the operator, not by your preference:

- **The name** is the chart's `gateway.name`, `assayd` by default. The operator reads the Gateway by that name.
- **The listener is named `http`.** Every emitted route attaches to the listener named `http`, and there is no value to change it. Name it anything else and every route reports `NoMatchingParent` and every request gets `404`. The operator does not read route status, so an `auth: none` Agent still says `Ready`. An API-key Agent's anonymous probe cannot get its `401`, so its route stays unpublished.
- **The listener admits the run namespaces.** The operator puts each Agent's route in an operator-owned run namespace (section 3), labelled `assayd.dev/run-namespace: "true"`. A listener that does not admit them rejects every route, and every request gets `404` with no other signal.
- **The Gateway admits no ListenerSets.** Leave `spec.allowedListeners` unset. The API server defaults it to `from: None`. Set it to `Same`, `All` or any `Selector` and no new Agent is published and no lock is recorded: each held Agent reports `PolicyApplyIncomplete`, reason `GatewayAuthPolicy`, and pages until you change it.

```bash
kubectl apply -f - <<'EOF'
apiVersion: v1
kind: Namespace
metadata:
  name: assayd-gateway
  labels:
    pod-security.kubernetes.io/enforce: baseline
    pod-security.kubernetes.io/enforce-version: latest
---
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: assayd
  namespace: assayd-gateway
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
EOF
```

Wait for the data plane, not only for the Gateway. The harness once saw a `Programmed` Gateway whose Service had no endpoints. A cold pull of the agentgateway image took over three minutes.

```bash
kubectl -n assayd-gateway wait --for=condition=Programmed gateway/assayd --timeout=180s
kubectl -n assayd-gateway rollout status deploy/assayd --timeout=600s
```

**Attach no `AgentgatewayPolicy` of your own to this Gateway.** A policy in the Gateway's namespace that targets it and could answer an anonymous request by itself (authentication, authorization, `extAuth`, `directResponse` and more) holds every new Agent the same way, under `GatewayAuthPolicy`. The chart's install notes give the full rule.

### 1.5 The Gateway's Service, and the two URLs

agentgateway generates a Service for the Gateway. Find it by the Gateway API label:

```bash
GW_SVC=$(kubectl -n assayd-gateway get svc \
  -l gateway.networking.k8s.io/gateway-name=assayd \
  -o jsonpath='{.items[0].metadata.name}')
echo "$GW_SVC"
```

The chart takes two URLs, and they name different listeners:

| Value | What it is | Required | For this page |
|---|---|---|---|
| `gateway.servingUrl` | The `http` listener each Agent's route attaches to. Before publishing a new Agent's route, the operator sends an anonymous request here, with the Agent's `Host` header, and requires a `401`. | Yes, with the gateway on. The chart refuses to render without it, and the operator refuses to start. | `http://$GW_SVC.assayd-gateway.svc.cluster.local:8080` |
| `gateway.url` | Where agents send their outbound tool calls. The operator injects it into every agent as `ASSAYD_GATEWAY_URL`. | No. Empty injects nothing. | Leave it unset (see below). |

`gateway.servingUrl` must be an absolute `http(s)` URL naming only a host and port. The operator checks that shape and nothing else. A URL that reaches no listener leaves every new Agent unpublished, with `PolicyApplyIncomplete`, reason `AuthEnforcementUnverified`, four minutes after it was created. A URL that reaches something else that answers `401` on its own, such as a proxy, can pass for the Agent's policy, and nothing detects it.

`gateway.url` must name a listener that carries tool routes. The harness adds a second listener, `tools` on port `8081`, for that. How to author the tool route behind it is not documented yet, so this page leaves `gateway.url` unset. The A2A task below does not need it.

## 2. Install the chart

Install the published, signed chart. It pins the operator image by digest; `docs/supply-chain.md` shows how to verify both.

```bash
helm install assayd oci://ghcr.io/quinyte/charts/assayd --version 0.3.0 \
  --set gateway.enabled=true \
  --set gateway.name=assayd \
  --set gateway.namespace=assayd-gateway \
  --set gateway.servingUrl="http://$GW_SVC.assayd-gateway.svc.cluster.local:8080" \
  --wait --timeout 5m
```

- **Install the release into the default namespace, as above.** The chart creates `assayd-system` itself and runs the operator there. The harness installs the release into `default` too.
- **`gateway.namespace` is load-bearing.** An admission policy reserves route authorship by comparing a route's `parentRef` namespace to this value. Point it at the wrong namespace and that reservation matches nothing, so any identity can attach a route to the Gateway.
- **On a single-node cluster**, add `--set profile=local`. It forces one operator replica. The default `prod` profile runs two, with a disruption budget. The harness installs with the chart's `values-local.yaml`, which sets it.
- The first install creates the Agent CRD from the chart's `crds/` directory. Upgrades do not (section 4).

Check that the operator is running:

```bash
kubectl -n assayd-system get pods
```

## 3. API keys

An Agent with no `expose` block, or with `spec.expose.a2a.auth: apikey`, is served under API keys. Callers send a key as `Authorization: Bearer <key>`. The operator's policy admits exactly one group: the name of the **Agent's own namespace**. No field names another group.

**The keys are yours to write.** The operator writes the policy and no key set, so until you add one, every caller gets `401`. A key set is a `ConfigMap` that:

- is in the Agent's **run namespace**, not in the Agent's own namespace. A key set in another namespace was measured not to admit, at agentgateway 1.5.0;
- carries the label `assayd.dev/api-keys: "true"`. That label is a constant, with no chart value to change it;
- is written by an identity in `admission.apiKeyWriters`. By default that is the `system:masters` group. Admission refuses anyone else, and the refusal names the `assayd-api-keys` policy.

**Each entry is one key.** The entry name is an identifier of your choosing. Do not use the key itself: a `ConfigMap` is not confidential. The value is a JSON object:

```json
{"keyHash":"sha256:<hex>","metadata":{"group":"<agent-namespace>"}}
```

`<hex>` is the lowercase hex SHA-256 of the key's bytes. agentgateway refuses a raw `key` in a `ConfigMap`, so only the hash is stored.

**The run namespace** is `assayd-run-<agent-namespace>`. When that would exceed 63 characters, the operator truncates the namespace and appends `-` and 16 hex characters of its SHA-256. The run namespace appears when the first Agent in a namespace is reconciled. `status` does not record it, so to read it back, select on the label the operator puts on it:

```bash
kubectl get ns -l assayd.dev/agent-namespace=<agent-namespace>
```

**Who counts as a key writer.** Check your groups with `kubectl auth whoami`. A `cluster-admin` binding alone is not enough. kubeadm, and so kind, puts its admin kubeconfig in `kubeadm:cluster-admins`, not `system:masters`. To add an identity, set `admission.apiKeyWriters.users` or `.groups`. Each identity you add can mint a key for any Agent's group.

What the key set costs you:

- **A key is a shared bearer secret.** Whoever holds it is the principal. Nothing binds it to a workload or a user.
- **Nothing expires or rotates a key.** Rotating one means editing every `ConfigMap` that holds it; revoking one means deleting its entry.
- **agentgateway reads the key set live.** Adding an entry admits a caller at once, with no revision and no gate. The e2e allows up to two minutes for a new key to take effect.
- **Do not put one key in two `ConfigMap`s.** agentgateway calls that behaviour undefined. At 1.5.0 it was measured moving a key between the two groups without an error.

## 4. Upgrading: apply the CRDs yourself

**`helm upgrade` never updates a chart's `crds/` directory.** A cluster keeps the Agent CRD it was first installed with. Apply the new chart's CRDs before every upgrade:

```bash
helm pull oci://ghcr.io/quinyte/charts/assayd --version <version> --untar --untardir assayd-chart
kubectl apply --server-side --force-conflicts -f assayd-chart/assayd/crds/
helm upgrade assayd oci://ghcr.io/quinyte/charts/assayd --version <version> \
  <the same --set flags as your install> --wait --timeout 5m
```

`--server-side --force-conflicts` lets the apply take over fields the first `helm install` set. It is the command the harness runs before each install (`hack/e2e.sh`). An upgrade from one published chart version to another is not exercised by any test.

**If you skip it**, an Agent CRD older than `status.auth` drops that field without an error. The operator notices the record did not come back. An Agent it tries to publish then stops with `PolicyApplyIncomplete=True` and `Ready=False`, reason `AuthRecordNotKept`, and nothing is written to the gateway for it until the CRDs are applied. A CRD from before the API-key slice also still defaults `auth` to `oauth` and refuses `apikey`.

## 5. Walkthrough: one A2A task through the gateway

This follows `TestTheGatewayRefusesADisallowedPrincipal`, which measures the same three outcomes on k3d: `200` for a key in the Agent's group, `403` for a valid key in another group, `401` for no key.

### 5.1 An agent image

Nothing publishes an agent image. Build the e2e's own agent, `test/responder`, from a checkout of this repository. It serves an A2A card and answers A2A `SendMessage` by echoing the text back. It is a test fixture, and `docs/agent-contract.md` says what any container must do instead.

The image must be pushed to a registry your nodes can pull from, and referenced by digest. The Agent CRD refuses a reference without `@sha256:`, and the operator sets `imagePullPolicy: Always`, so a locally imported image does not work. On k3d, the harness creates a registry and wires it in with `k3d cluster create --registry-use`, which is only possible at cluster creation.

```bash
REPO=<registry-your-nodes-can-pull-from>/assayd-responder
docker build -t "$REPO:demo" -f test/responder/Dockerfile .
docker push "$REPO:demo"
IMAGE="$REPO@$(docker inspect --format '{{index .RepoDigests 0}}' "$REPO:demo" | cut -d@ -f2)"
echo "$IMAGE"
```

The reference must be lowercase and match `<registry>[:port]/<repo>[:tag]@sha256:<64 hex>`.

### 5.2 Create the Agent

The card's `name` must equal the Agent's name. The responder takes its name from `AGENT_NAME`, so set both to `hello`. With no `expose` block, the Agent is served under API keys.

```bash
kubectl create namespace demo
kubectl apply -f - <<EOF
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata:
  name: hello
  namespace: demo
spec:
  runtime:
    image: $IMAGE
    env:
    - name: AGENT_NAME
      value: hello
EOF
```

### 5.3 Wait for it to be served

The operator writes the route with no backend first. It writes `hello-auth`, sends an anonymous request through `gateway.servingUrl`, and publishes the route only once that request gets `401`. `status.auth.mode` is absent until then, and reads `apikey` once the route is served:

```bash
kubectl -n demo wait agent/hello --for=jsonpath='{.status.auth.mode}'=apikey --timeout=10m
kubectl -n demo get agent hello -o jsonpath='{.status.auth}{"\n"}{.status.conditions}{"\n"}'
```

While it is not yet served, `Ready` is `False`, reason `AuthEnforcementPending`. Once it is, expect:

| Condition | Status | Reason | Why |
|---|---|---|---|
| `GovernanceSkipped` | `False` | `AuthVerifiedOnOneReplica` | One probe proves one gateway replica, and no replica count is declared. |
| `Registered` | `True` | `CardValidated` | The operator fetched and validated the card. |
| `CardUnsigned` | `True` | `NoSigningConfigured` | Nothing verifies card signatures yet. This is true for every Agent. |

### 5.4 Write two keys

One key in the Agent's group, `demo`, and one valid key in another group. Run this as a key writer (section 3).

```bash
RUN_NS=$(kubectl get ns -l assayd.dev/agent-namespace=demo -o jsonpath='{.items[0].metadata.name}')
PERMITTED=$(openssl rand -hex 32)
OTHER=$(openssl rand -hex 32)
# sha256sum on Linux; on macOS use: shasum -a 256
sha() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }

kubectl apply -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: demo-api-keys
  namespace: $RUN_NS
  labels:
    assayd.dev/api-keys: "true"
data:
  demo-client: '{"keyHash":"sha256:$(sha "$PERMITTED")","metadata":{"group":"demo"}}'
  other-client: '{"keyHash":"sha256:$(sha "$OTHER")","metadata":{"group":"other"}}'
EOF
```

`RUN_NS` is `assayd-run-demo`.

### 5.5 Send the task, three ways

The route matches on the `Host` header, `<agent>.<agent-namespace>.<gateway.hostnameSuffix>`. With the chart's default suffix that is `hello.demo.assayd.internal`. It is a routing key, not a DNS name: nothing creates a record for it.

The requests go from a Pod inside the cluster, as the e2e sends them. Reaching the Gateway from outside the cluster, by `kubectl port-forward` or a LoadBalancer, is not something any test here does.

```bash
GW_URL="http://$GW_SVC.assayd-gateway.svc.cluster.local:8080"
BODY='{"message":{"messageId":"demo-1","role":"ROLE_USER","parts":[{"text":"hello"}]}}'
a2a() {  # prints the response body, then the status code
  kubectl -n demo run "a2a-$RANDOM" --rm -i --restart=Never --quiet \
    --image=curlimages/curl:8.11.1 --command -- \
    curl -sS -w '\n%{http_code}\n' -X POST "$GW_URL/message:send" \
      -H 'Host: hello.demo.assayd.internal' \
      -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' \
      "$@" -d "$BODY"
}

a2a                                          # no key
a2a -H "Authorization: Bearer $PERMITTED"    # a key in group demo
a2a -H "Authorization: Bearer $OTHER"        # a valid key in group other
```

| Request | Expect | What answered |
|---|---|---|
| No key | `401` | The policy's API-key authentication. |
| Key in group `demo` | `200`, and a task in `TASK_STATE_COMPLETED` whose artifact reads `echo: hello` | The agent. |
| Valid key in group `other` | `403` | The policy's authorization rule. The key authenticated, and its group is not admitted. |

A `200` looks like this:

```json
{"task":{"id":"task-…","contextId":"ctx-…","status":{"state":"TASK_STATE_COMPLETED"},"artifacts":[{"artifactId":"artifact-…","name":"echo","parts":[{"text":"echo: hello"}]}],"metadata":{"agent":"hello","gateway":""}}}
```

The permitted key can get `401` for a short while after you write the key set, because agentgateway reads it live. The e2e retries for up to two minutes. Leave out `A2A-Version: 1.0` and the responder itself answers `400`, since it speaks A2A 1.0 only.

This shortcut passes each key on the `kubectl run` command line, so it is readable in the Pod spec by anyone who can read Pods in `demo`. Do not do that with a key you intend to keep.

## When it does not work

| Symptom | Cause |
|---|---|
| The operator Pod exits at start, and its last log line names a kind | The Gateway API or agentgateway CRDs are missing (sections 1.1, 1.2). |
| `helm install` fails naming the Gateway's namespace | The namespace does not exist yet (section 1.3). |
| The chart refuses to render: `gateway.servingUrl is empty` | Set `gateway.servingUrl` (section 1.5). |
| Every request gets `404`; an `auth: none` Agent says `Ready`, an API-key Agent stays unpublished | The listener is not named `http`, or it does not admit `assayd.dev/run-namespace: "true"` (section 1.4). |
| `PolicyApplyIncomplete`, reason `AuthEnforcementUnverified` | `gateway.servingUrl` reaches no listener. |
| `PolicyApplyIncomplete`, reason `GatewayAuthPolicy` | The Gateway admits ListenerSets, or a policy of yours targets it (section 1.4). |
| `PolicyApplyIncomplete`, reason `AuthRecordNotKept` | The Agent CRD predates `status.auth`: apply the chart's CRDs (section 4). |
| The key set is refused, naming `assayd-api-keys` | You are not in `admission.apiKeyWriters` (section 3). |
| `Registered=False`, reason `CardNameMismatch` | The card's `name` is not the Agent's name. `docs/agent-contract.md` lists the other card reasons. |
| Every keyed request gets `401` | No key set in the run namespace, the wrong label, or the wrong hash. Hash the key's bytes with no trailing newline. |

## Not covered here

- **Routing an agent's tool calls to an MCP server** through the gateway. The e2e does it with a route it authors by hand. How a user should author that route is waiting on a decision, and this page will not guess it.
- **Anything beyond authentication.** No budget, rate limit or tool filter is compiled, and no NetworkPolicy is created. The `401` and `403` above hold on the gateway path only; a Pod that can reach the agent's Service directly bypasses them.

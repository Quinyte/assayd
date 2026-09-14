# Installing assayd with a gateway

This page takes a fresh cluster to the point where one agent completes one A2A task through the gateway, and a caller without the right key is refused. Every step comes from `hack/e2e.sh` or the e2e suite in `test/e2e/`. Where a step here goes beyond what they do, it says so.

**What you get is authentication, not governance.** With the gateway on, the operator emits each Agent's route and, for an API-key Agent, one `AgentgatewayPolicy` that checks the caller's key and group. It emits nothing else. No budget, rate limit or tool filter is enforced for any agent, and no NetworkPolicy is created, so an agent Pod can still be reached directly, around the gateway. `README.md` (*What is NOT true today*) and `SECURITY.md` list the rest.

## Tested versions

These are the versions the e2e suite runs against. They are what has been measured, not a support guarantee.

| Component | Version | Source |
|---|---|---|
| Gateway API CRDs | `v1.6.0`, standard channel | `hack/e2e.sh` (`GWAPI_VERSION`) |
| agentgateway (CRDs and controller) | `1.5.0` | `hack/e2e.sh` (`AGW_VERSION`) |
| assayd chart | `0.3.0`, `oci://ghcr.io/quinyte/charts/assayd`. Section 6 needs this repository's chart, which is ahead of `0.3.0` (section 6.2). | `docs/supply-chain.md` |
| Kubernetes | k3d (k3s). The chart requires `>=1.30.0` | `charts/assayd/Chart.yaml` |

The e2e runs its gateway tests on k3d only. The kind lane skips them and reports the gateway path as unverified.

## 1. Before you install the chart

The chart prints its notes only after a successful install, and three things must exist before that install can succeed. With `gateway.enabled: true` and the Gateway API or agentgateway CRDs absent, **the operator refuses to start** and its last log line names the missing kind. `helm install --wait` then times out. And the chart renders a Role into the Gateway's namespace, so `helm install` fails if that namespace is missing.

### 1.0 A cluster, with a registry it can pull from

Section 5 deploys an agent image you build, and the operator pulls it by digest from a registry at every Pod start. So the cluster needs a registry its nodes can pull from. On k3d, that registry is wired in when the cluster is created and cannot be added afterwards. This is what the harness does:

```bash
k3d registry create assayd-registry --port 5111
k3d cluster create assayd --agents 0 --wait --registry-use k3d-assayd-registry:5111
```

k3d names the registry `k3d-assayd-registry`. The host pushes to it as `localhost:5111`, and the cluster's nodes pull from it as `k3d-assayd-registry:5111`. Section 5.1 uses both names.

On any other cluster, use a registry its nodes can already pull from.

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
- **The listener is named `http`.** Every emitted route attaches to the listener named `http`, and there is no value to change it. Name it anything else and every route reports `NoMatchingParent: sectionName "http" not found`, and anonymous requests get `404`.
- **The listener admits the run namespaces.** The operator puts each Agent's route in an operator-owned run namespace (section 3), labelled `assayd.dev/run-namespace: "true"`. A listener whose selector matches none of them rejects every route with `NotAllowedByListeners`.
- **The Gateway admits no ListenerSets.** Leave `spec.allowedListeners` unset. The API server defaults it to `from: None`. Set it to `Same`, `All` or any `Selector` and no new Agent is published and no lock is recorded: each held Agent reports `PolicyApplyIncomplete`, reason `GatewayAuthPolicy`, and pages until you change it.

**A wrong listener is reported for a new Agent, and not for a served one.** The operator reads a route's `Accepted` and `ResolvedRefs` status while it brings the route up, and not afterwards. This was measured on k3d with agentgateway 1.5.0, during review of this page, with the listener renamed to `web`:

| Agent | What it reports |
|---|---|
| New, `auth: none` | `Ready=True` for about four minutes. Then `Ready=False` and `PolicyApplyIncomplete`, reason `AuthEnforcementUnverified`, naming `NoMatchingParent: sectionName "http" not found`. |
| New, API-key | Stops at stage `Converging` from the first pass: `Ready=False`, reason `AuthEnforcementPending`, naming `NoMatchingParent`. `AuthEnforcementUnverified` at four minutes. |
| Already served | `Ready=True`, indefinitely, while its route is `Accepted=False`. **Nothing reports it.** |

With a listener selector that matches nothing, a new Agent of either mode reports `Ready=False` naming `NotAllowedByListeners`, and an Agent already served again stays silent. Once the Gateway was corrected, every held Agent recovered to `Ready` within 90 seconds, without being recreated.

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

`gateway.url` must name a listener that carries tool routes. The harness adds a second listener, `tools` on port `8081`, for that. Section 6 adds it and sets `gateway.url`. The A2A task in section 5 does not need it, so leave it unset until then.

## 2. Install the chart

Install the published, signed chart. It pins the operator image by digest; `docs/supply-chain.md` shows how to verify both.

```bash
helm install assayd oci://ghcr.io/quinyte/charts/assayd --version 0.3.0 \
  --set profile=local \
  --set gateway.enabled=true \
  --set gateway.name=assayd \
  --set gateway.namespace=assayd-gateway \
  --set gateway.servingUrl="http://$GW_SVC.assayd-gateway.svc.cluster.local:8080" \
  --wait --timeout 5m
```

- **Install the release into the default namespace, as above.** The chart creates `assayd-system` itself and runs the operator there. The harness installs the release into `default` too.
- **`gateway.namespace` is load-bearing.** An admission policy reserves route authorship by comparing a route's `parentRef` namespace to this value. Point it at the wrong namespace and that reservation matches nothing, so any identity can attach a route to the Gateway.
- **`--set profile=local` is what lets `--wait` finish.** It runs one operator replica. The default `prod` profile runs two, and only one of them is ever `Ready`. The operator is leader-elected, and its readiness check means "this process is reconciling", which passes only for the replica holding the lease. The other is a standby, never `Ready` by design, so that an operator that cannot get its lease never reports itself healthy. Under `prod`, `helm install --wait` therefore never completes: it was measured stopping at `Available: 1/2` with `context deadline exceeded`, and the release ends `failed`. `kubectl rollout status` on the operator Deployment never completes either. The cluster's size makes no difference. The `prod` anti-affinity is preferred, not required, so both replicas do fit on one node. The harness installs with the chart's `values-local.yaml`, which sets `profile: local`.
- **To run the `prod` profile**, leave out `--set profile=local` and `--wait`, and wait for one `Ready` operator Pod instead. `kubectl wait pod -l …` does not do this, because it waits for every matching Pod, the standby included.

  ```bash
  until kubectl -n assayd-system get pods \
      -l app.kubernetes.io/component=agent-operator,app.kubernetes.io/instance=assayd \
      -o jsonpath='{range .items[*]}{.status.conditions[?(@.type=="Ready")].status}{"\n"}{end}' \
      | grep -qx True; do
    sleep 5
  done
  ```
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
  <the same --set flags as your install, profile included> --wait --timeout 5m
```

Keep `--wait` only under `profile=local`. Under `prod` it never completes, for the reason in section 2: drop it, and wait for one `Ready` operator Pod as shown there.

`--server-side --force-conflicts` lets the apply take over fields the first `helm install` set. It is the command the harness runs before each install (`hack/e2e.sh`). An upgrade from one published chart version to another is not exercised by any test.

**If you skip it**, an Agent CRD older than `status.auth` drops that field without an error. The operator notices the record did not come back. An Agent it tries to publish then stops with `PolicyApplyIncomplete=True` and `Ready=False`, reason `AuthRecordNotKept`, and nothing is written to the gateway for it until the CRDs are applied. A CRD from before the API-key slice also still defaults `auth` to `oauth` and refuses `apikey`.

## 5. Walkthrough: one A2A task through the gateway

This follows `TestTheGatewayRefusesADisallowedPrincipal`, which measures the same three outcomes on k3d: `200` for a key in the Agent's group, `403` for a valid key in another group, `401` for no key.

### 5.1 An agent image

Nothing publishes an agent image. Build the e2e's own agent, `test/responder`, from a checkout of this repository. It serves an A2A card and answers A2A `SendMessage` by echoing the text back. It is a test fixture, and `docs/agent-contract.md` says what any container must do instead.

The image must be in a registry your nodes can pull from, referenced by digest. The Agent CRD refuses a reference without `@sha256:`. The operator sets `imagePullPolicy: Always`, so an image imported straight into the nodes is not used.

On k3d, with the registry from section 1.0, the host and the nodes know the registry by different names. So push to one repository and reference the other:

```bash
PUSH_REPO=localhost:5111/assayd-responder            # the host's name for the registry
PULL_REPO=k3d-assayd-registry:5111/assayd-responder  # the nodes' name for it
docker build -t "$PUSH_REPO:demo" -f test/responder/Dockerfile .
DIGEST=$(docker push "$PUSH_REPO:demo" | awk '/digest: sha256:/ {print $3}')
if [ -z "$DIGEST" ]; then
  echo "the push failed or printed no digest: stop here" >&2
else
  IMAGE="$PULL_REPO@$DIGEST"
  echo "$IMAGE"
fi
```

- **Do not push to `k3d-assayd-registry:5111` from the host.** That name resolves only inside the cluster's network, and the push fails with `no such host`.
- **Take the digest from `docker push`, not from `docker inspect`.** With Docker's containerd image store, `{{index .RepoDigests 0}}` can name a local index digest the registry does not serve, and the Pod's pull then gets `404`. The harness reads `RepoDigests`, which works with Docker's classic image store.
- **Stop if `IMAGE` is empty.** An Agent with an empty image is refused by the CRD.

On any other cluster, push to a name your nodes can pull from, and use the digest the registry reports: the `digest:` line `docker push` prints, or `docker buildx imagetools inspect <image>`. The reference must be lowercase and match `<registry>[:port]/<repo>[:tag]@sha256:<64 hex>`.

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
kubectl -n demo wait agent/hello --for=condition=Registered --timeout=2m
kubectl -n demo get agent hello -o jsonpath='{.status.auth}{"\n"}{.status.conditions}{"\n"}'
```

The second wait is needed. The card is fetched separately, once the Pod is available, and retried every 15 seconds. The auth wait can return first, while `Registered` is still `False`, reason `CardUnreachable`. In review it matched the table below about 30 seconds later.

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

## 6. An MCP tool through the gateway

This follows `TestAnAgentCallsAnMCPToolThroughTheGateway` and `TestAnAgentCompletesATaskByCallingAToolThroughTheGateway` (`test/e2e/mcp_test.go`), which measure it on k3d. An agent calls a tool on an MCP server, through the gateway, and an allowlist at the gateway decides which tools it may call.

**Every resource in this section is yours to write.** Nothing in assayd emits a tool route, an `AgentgatewayBackend` or a tool allowlist. And what the gateway does here is route and filter, not authenticate:

- **The tool call is unauthenticated.** No identity is attached to an agent's outbound traffic, because design 06 has no implementation.
- **Nothing makes the gateway the agent's only way out.** `ASSAYD_GATEWAY_URL` is an address, not a restriction, and no egress NetworkPolicy is created.
- **Nothing checks where a tool route sends traffic.** An `AgentgatewayBackend` can name any address, an Agent's Service included, and a tool route in a run namespace can name an Agent's revision Service directly (section 6.2, design 07 A6.15).

### 6.1 A `tools` listener

Re-apply the Gateway from section 1.4 with a second listener, and create the namespace the tool server will run in:

```bash
kubectl create namespace demo-tools
kubectl apply -f - <<'EOF'
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
  - name: tools
    port: 8081
    protocol: HTTP
    allowedRoutes:
      namespaces:
        from: Selector
        selector:
          matchLabels:
            kubernetes.io/metadata.name: demo-tools
EOF
```

- **`tools` has its own port**, as in the harness. See section 6.4 for why the port matters.
- **Select the tool namespace by `kubernetes.io/metadata.name`, or a label of your own, never an `assayd.dev/*` label.** Admission reserves those to the operator, and the refusal names `assayd-namespace-labels`.
- **Never let a tool listener's `allowedRoutes` select a run namespace**, by `from: All` or by a selector that matches one. A route in a run namespace, on a tool listener that admits it, can name an Agent's revision Service directly and reach the Agent with no `<agent>-auth` (section 6.2).
- **Do not set `spec.allowedListeners`**, here or anywhere else on this Gateway. A ListenerSet can add a listener on the serving port with the hostname `*.<gateway.hostnameSuffix>`, and a route attached through a ListenerSet names the ListenerSet, not the Gateway, so `assayd-gateway-routes` does not see it. The review of this recipe measured an identity in no list admitted with a `parentRef` of kind `ListenerSet`. Section 1.4 gives the other reason: no new Agent is published while the Gateway admits ListenerSets.

### 6.2 Point agents at it, and name who may publish tools

Two chart values:

| Value | Set it to | What it does |
|---|---|---|
| `gateway.url` | `http://$GW_SVC.assayd-gateway.svc.cluster.local:8081` | The operator injects it into every agent as `ASSAYD_GATEWAY_URL`. |
| `admission.toolRouteWriters` | `users` and `groups` who publish tools | Lets them attach a route to the Gateway on any listener but `http` (design 07 A6.15). Empty by default, which admits no one but the operators: the operator and `admission.extraOperators`. Never list a group every identity carries, such as `system:authenticated` or `system:serviceaccounts`: any identity could then attach a tool route on any listener but `http`, limited only by the hostname rule (section 6.4) and by RBAC. |

**`admission.toolRouteWriters` is not in chart `0.3.0`.** It is in this repository's chart, which is what the harness installs, and will be in the next release. Helm ignores a value a chart does not declare, without an error, so on `0.3.0` the setting does nothing and every tool route is refused. Upgrade from a checkout:

```bash
kubectl apply --server-side --force-conflicts -f charts/assayd/crds/
helm upgrade assayd charts/assayd \
  --set profile=local \
  --set gateway.enabled=true \
  --set gateway.name=assayd \
  --set gateway.namespace=assayd-gateway \
  --set gateway.servingUrl="http://$GW_SVC.assayd-gateway.svc.cluster.local:8080" \
  --set gateway.url="http://$GW_SVC.assayd-gateway.svc.cluster.local:8081" \
  --set 'admission.toolRouteWriters.users={tool-publisher}' \
  --set operator.image.digest=sha256:30449f7ea1348ec393158997439bb2a6fddc78cb0fbf149b04614251add8643d \
  --wait --timeout 5m
```

**Keep the `operator.image.digest` line.** The published chart pins the operator image by digest, and a checkout's chart does not: it names the tag `0.1.0`, which is not published. Without the digest, the upgrade removes the running operator first and replaces it with an image that cannot be pulled, and `--wait` fails. The digest is the published `0.3.0` operator (`docs/supply-chain.md`), and **it predates two operator changes in this checkout**: #38, which sends the card fetch and the anonymous auth probe only to the revision's Service (`internal/controller/card.go`, `authprobe.go`), and the startup refusal of a `gateway.hostnameSuffix` that is not a lowercase DNS name (design 07 A6.15). So this upgrade runs a `0.3.0` operator, without either change, under this checkout's chart. No published release carries them yet; publishing one is a release decision, not a step in this recipe.

**No test takes this path.** The harness installs from a checkout, as here, with an operator image it builds itself. An upgrade from the published chart to a checkout is not exercised.

**`admission.toolRouteWriters` grants no RBAC.** It only lifts the admission refusal. Grant each identity the routes it writes, where it writes them, as the e2e grants its own (`routeWriterClient` in `test/e2e/toolroute_test.go`):

```bash
kubectl apply -f - <<'EOF'
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata: {name: tool-publisher, namespace: demo-tools}
rules:
- apiGroups: [gateway.networking.k8s.io]
  resources: [httproutes]
  verbs: [get, list, watch, create, update, patch, delete]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: tool-publisher, namespace: demo-tools}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: Role, name: tool-publisher}
subjects:
- {apiGroup: rbac.authorization.k8s.io, kind: User, name: tool-publisher}
EOF
```

**Never grant a tool route writer route RBAC in a run namespace** (`assayd-run-<agent-namespace>`). Admission checks a tool route's listener and hostnames, not its namespace or its `backendRefs`. So a tool route writer who can write routes in a run namespace, where a tool listener admits that namespace, can create a route with a harmless hostname whose `backendRefs` name an Agent's revision Service there. No Backend and no `ReferenceGrant` is needed, and the Agent is reached with no `<agent>-auth` in front of it (design 07 A6.15).

`patch` is for `kubectl apply`, which patches a route that already exists. The e2e creates its route and needs no `patch`. A new RoleBinding can take a few seconds to take effect, and until it does the write is refused by RBAC, not by admission; the e2e waits for it with a `SelfSubjectAccessReview`.

The e2e writes the server, the backend and the allowlist below as its cluster administrator. RBAC for a tool team to write `agentgatewaybackends` and `agentgatewaypolicies` is yours to grant. An `AgentgatewayPolicy` is reserved to the operator only in run namespaces (design 03 §6), and `demo-tools` is not one.

**Grant `agentgatewaybackends` as carefully as route RBAC in a run namespace.** An `AgentgatewayBackend` can name a static host (`spec.static.host` in agentgateway 1.5.0's CRD), and nothing checks that the host is not an Agent's revision Service. So a tool team that can write a backend and a tool route can publish an Agent on the tool listener with no `<agent>-auth` in front of it. Grant it only to identities you would trust with direct access to every Agent. This is read off the CRD schema, not measured (design 07 A6.15).

### 6.3 The MCP server, with `appProtocol` on its Service

Build and push `test/mcpserver`, the e2e's two-tool MCP server, as section 5.1 builds the responder: `-f test/mcpserver/Dockerfile`, into a repository such as `assayd-mcpserver`, and set `MCP_IMAGE` to the digest reference. It serves Streamable HTTP on port `8080` with two tools, `echo_text` and `delete_everything`.

```bash
kubectl apply -f - <<EOF
apiVersion: apps/v1
kind: Deployment
metadata: {name: mcpserver, namespace: demo-tools}
spec:
  replicas: 1
  selector: {matchLabels: {app: mcpserver}}
  template:
    metadata: {labels: {app: mcpserver}}
    spec:
      containers:
      - name: mcpserver
        image: $MCP_IMAGE
        ports: [{containerPort: 8080}]
        readinessProbe: {httpGet: {path: /healthz, port: 8080}}
---
apiVersion: v1
kind: Service
metadata: {name: mcpserver, namespace: demo-tools, labels: {app: mcpserver}}
spec:
  selector: {app: mcpserver}
  ports:
  - port: 8080
    targetPort: 8080
    protocol: TCP
    appProtocol: agentgateway.dev/mcp
EOF
```

**`appProtocol: agentgateway.dev/mcp` is required, and nothing reports it missing.** Without it the backend below reaches `Accepted=True`, the route resolves, and every request gets `503 mcp: no backends configured` (design 07 A6.8). The CRD schema does not describe it.

### 6.4 The backend, and the route written as a tool route writer

```bash
kubectl apply -f - <<'EOF'
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayBackend
metadata: {name: mcp-tools, namespace: demo-tools}
spec:
  mcp:
    sessionRouting: Stateless
    targets:
    - name: fixture
      selector:
        services:
          matchLabels: {app: mcpserver}
EOF

kubectl --as tool-publisher apply -f - <<'EOF'
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata: {name: mcp-tools, namespace: demo-tools}
spec:
  parentRefs:
  - name: assayd
    namespace: assayd-gateway
    sectionName: tools
  hostnames: [mcp.demo-tools.example]
  rules:
  - backendRefs:
    - group: agentgateway.dev
      kind: AgentgatewayBackend
      name: mcp-tools
EOF
```

`kubectl --as` impersonates the identity you named in `admission.toolRouteWriters`. A cluster administrator may do that. The identity itself would apply the route with its own credentials.

- **`sessionRouting: Stateless`** pins no MCP session to a Pod. agentgateway defaults to `Stateful`, and assayd has not decided which an MCP backend should be (`applyMCPBackend` in `test/e2e/mcp_test.go`).
- **The route must name `sectionName`.** A `parentRef` to the Gateway with no `sectionName` attaches to every listener, `http` included, and is refused, with or without a `port`. So is `sectionName: http`, and an update that moves a route onto `http` or removes its `sectionName`.
- **The route must list `hostnames`, and none may be an Agent's.** The serving hosts are `<agent>.<agent-namespace>.<gateway.hostnameSuffix>`. The suffix itself, a name under it, and a wildcard at or above it, such as `*.internal`, are refused, and so is a route with no hostnames. A request goes to the listener its port and host match, and only then to that listener's routes. So a tool listener that shared the serving port, with a hostname matching an Agent's, could hand that Agent's traffic to a tool route.

Each refusal names `assayd-gateway-routes` and says which rule refused it. `TestTheInstalledRouteReservationKeepsToolRouteWritersOffTheServingListener` measures three of them on k3d, and `test/envtest/admission_toolroutes_test.go` measures every case.

### 6.5 Check the path

```bash
TOOLS_URL="http://$GW_SVC.assayd-gateway.svc.cluster.local:8081/mcp"
kubectl -n demo-tools run "mcp-$RANDOM" --rm -i --restart=Never --quiet \
  --image=curlimages/curl:8.11.1 --command -- \
  curl -sS -X POST "$TOOLS_URL" -H 'Host: mcp.demo-tools.example' \
    -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
    -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"check","version":"0"}}}'
```

Expect a Server-Sent Events stream whose `data:` line carries a result naming `serverInfo.name` `assayd-e2e-mcpserver`. agentgateway answers a successful MCP exchange as SSE even when the server answered plain JSON, and answers its own errors as plain JSON. A `503 mcp: no backends configured` is section 6.3's `appProtocol`, or a backend that has not yet resolved a target: the e2e retries for up to two minutes.

### 6.6 The allowlist

```bash
kubectl apply -f - <<'EOF'
apiVersion: agentgateway.dev/v1alpha1
kind: AgentgatewayPolicy
metadata: {name: mcp-allowlist, namespace: demo-tools}
spec:
  targetRefs:
  - {group: agentgateway.dev, kind: AgentgatewayBackend, name: mcp-tools}
  backend:
    mcp:
      authorization:
        action: Allow
        policy:
          matchExpressions:
          - 'mcp.tool.name == "echo_text"'
EOF
```

This is design 03's `toolAllowlist`, written by hand. Two behaviours were measured:

- **A tool it does not allow is removed from `tools/list`**, so an agent never learns it exists.
- **A call to it fails with the JSON-RPC error `Unknown tool: delete_everything`**, not "forbidden", and nothing in the answer says an allowlist refused it. When an agent cannot call a tool the server offers, check the allowlist first.

`Accepted=True` on the policy is not enforcement. The e2e waits until the refused call is refused, for up to two minutes.

### 6.7 An agent that calls the tool

An agent reaches the tool at `<ASSAYD_GATEWAY_URL>/mcp`, with the tool route's hostname as its `Host` header. The operator injects the URL and nothing else, so the agent must be told the hostname some other way. The responder reads it from `MCP_TOOL_HOST`, a name of the fixture's, not of assayd's. `docs/agent-contract.md` gives the request shapes.

The e2e creates its Agent after the chart has `gateway.url`, and checks the variable on the Deployment the operator rendered. Whether an Agent created before the upgrade receives it is not tested, so create a new one:

```bash
kubectl apply -f - <<EOF
apiVersion: assayd.dev/v1alpha1
kind: Agent
metadata: {name: toolcaller, namespace: demo}
spec:
  runtime:
    image: $IMAGE
    env:
    - {name: AGENT_NAME, value: toolcaller}
    - {name: MCP_TOOL_HOST, value: mcp.demo-tools.example}
EOF
kubectl -n demo wait agent/toolcaller --for=jsonpath='{.status.auth.mode}'=apikey --timeout=10m
```

It is in namespace `demo`, so section 5.4's `$PERMITTED` key admits it. The responder's `call_tool` skill runs when the message's `metadata.tool` names a tool. It sends the message text as the tool's `text` argument, and the tool's answer becomes the task's artifact:

```bash
tool() {  # tool name, then text
  kubectl -n demo run "tool-$RANDOM" --rm -i --restart=Never --quiet \
    --image=curlimages/curl:8.11.1 --command -- \
    curl -sS -X POST "$GW_URL/message:send" \
      -H 'Host: toolcaller.demo.assayd.internal' \
      -H 'Content-Type: application/json' -H 'A2A-Version: 1.0' \
      -H "Authorization: Bearer $PERMITTED" \
      -d "{\"message\":{\"messageId\":\"demo-$RANDOM\",\"role\":\"ROLE_USER\",\"parts\":[{\"text\":\"$2\"}],\"metadata\":{\"tool\":\"$1\"}}}"
}

tool echo_text "from the agent"
tool delete_everything x
```

| Task | Expect |
|---|---|
| `echo_text` | `TASK_STATE_COMPLETED`, with the artifact `tool echo_text: echo: from the agent` |
| `delete_everything`, under section 6.6's allowlist | `TASK_STATE_FAILED`, with a status message carrying `Unknown tool: delete_everything` |
| `delete_everything`, before the allowlist | `TASK_STATE_COMPLETED`, with the artifact `tool delete_everything: deleted nothing, as promised` |

The last row needs no allowlist in place, so it cannot be seen after section 6.6 as written. To see it, delete the allowlist (`kubectl -n demo-tools delete agentgatewaypolicy mcp-allowlist`) and allow up to two minutes. The e2e asks for it before it applies the allowlist, which is what makes the refusal the gateway's: the MCP server answers `delete_everything` normally when a call reaches it. The first task can fail while the backend resolves its target; the e2e retries for up to two minutes.

## When it does not work

| Symptom | Cause |
|---|---|
| The operator Pod exits at start, and its last log line names a kind | The Gateway API or agentgateway CRDs are missing (sections 1.1, 1.2). |
| `helm install` fails naming the Gateway's namespace | The namespace does not exist yet (section 1.3). |
| The chart refuses to render: `gateway.servingUrl is empty` | Set `gateway.servingUrl` (section 1.5). |
| `helm install --wait` times out, the operator Deployment shows `Available: 1/2`, and the release is `failed` | The default `prod` profile runs two operator replicas, and only the lease holder is ever `Ready`: the standby is not `Ready` by design. The operator is working. Uninstall, then install again with `--set profile=local`, or under `prod` without `--wait` (section 2). |
| Anonymous requests get `404`, and a new Agent names `NoMatchingParent` or `NotAllowedByListeners` | The listener is not named `http`, or its selector does not admit `assayd.dev/run-namespace: "true"` (section 1.4). An Agent served before the Gateway changed still says `Ready`: nothing reports it. |
| `PolicyApplyIncomplete`, reason `AuthEnforcementUnverified` | `gateway.servingUrl` reaches no listener. |
| `PolicyApplyIncomplete`, reason `GatewayAuthPolicy` | The Gateway admits ListenerSets, or a policy of yours targets it (section 1.4). |
| `PolicyApplyIncomplete`, reason `AuthRecordNotKept` | The Agent CRD predates `status.auth`: apply the chart's CRDs (section 4). |
| The key set is refused, naming `assayd-api-keys` | You are not in `admission.apiKeyWriters` (section 3). |
| `Registered=False`, reason `CardNameMismatch` | The card's `name` is not the Agent's name. `docs/agent-contract.md` lists the other card reasons. |
| Every keyed request gets `401` | No key set in the run namespace, the wrong label, or the wrong hash. Hash the key's bytes with no trailing newline. |
| A tool route is refused, naming `assayd-gateway-routes` | The writer is not in `admission.toolRouteWriters`, the route names `http` or no `sectionName`, or a hostname is missing or an Agent's. The message says which (section 6.4). On chart `0.3.0`, the value does not exist (section 6.2). |
| Every tool request gets `503 mcp: no backends configured` | The MCP Service's port has no `appProtocol: agentgateway.dev/mcp` (section 6.3). |
| A tool task fails with `ASSAYD_GATEWAY_URL is not set` | `gateway.url` is unset, or the Agent predates it (section 6.7). |
| A tool task fails with `Unknown tool: <name>` | The allowlist does not admit that tool (section 6.6). |

## Not covered here

- **Anything beyond authentication.** No budget, rate limit or tool filter is compiled, and no NetworkPolicy is created. The `401` and `403` above hold on the gateway path only; a Pod that can reach the agent's Service directly bypasses them.

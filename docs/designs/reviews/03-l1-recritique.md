# Re-critique: ADR-0034 Amendment 5 and design 03 A75, v2 — L1, "detect and hold" for Gateway-level auth

- **Date**: 2026-09-13
- **Subject**: the v2 draft of ADR-0034 Amendment 5 and design 03 A75, answering `reviews/03-l1-critique.md` (REVISE: 0 BLOCKER, 7 MAJOR, 6 MINOR). Read against `main` at 2c1be63, which carries design 03 through A74.
- **Read**: design 03 §3.1 (the `GovernanceSkipped` clearing rule, 03:254), §3.2 (03:300-330), §3.3.1's aggregation (03:503-520), §3.3.3 (03:620-735), §5 (03:1228-1245) and A74; ADR-0034 and Amendments 1-4; `internal/controller/authtxn.go`, `authforeign.go`, `httproute.go`; `charts/assayd/files/operator-rules.yaml`, `charts/assayd/templates/rbac.yaml`, `admission.yaml`, `operator.yaml`; `hack/e2e.sh`'s Gateway.
- **Checked against, not read about**:
  - `sigs.k8s.io/gateway-api` v1.6.0 (`go.mod`): `apis/v1/gateway_types.go:291-360`, `apis/v1/listenerset_types.go:32-60`, and `config/crd/standard/gateway.networking.k8s.io_gateways.yaml:140-169`.
  - The vendored agentgateway CRD chart, `test/conformance/testdata/agentgateway-crds-1.4.1.tgz`, and the 1.5.0 CRD chart (`oci://ghcr.io/agentgateway/charts/agentgateway-crds`, `appVersion: 1.5.0`). For `agentgatewaypolicies`, the `spec`, `traffic`, `backend`, `frontend` and `strategy` schemas and every CEL rule on `spec` were compared at both versions.
  - agentgateway's published documentation (agentgateway.dev), for `transformation`, ListenerSet and ext auth. That is documentation, not a measurement, and it is cited as such.

Verdict: REVISE — 0 BLOCKER, 4 MAJOR, 7 MINOR.

v2 answers the first critique well. The ListenerSet rule is now one field on one object. The RBAC is named. The field rule is inverted. The check runs after the `401`. The reason order is stated. Re-creations hold, and item 7's override claim is scoped. Replacing the ListenerSet listing with `spec.allowedListeners` is correct and fails closed (question (a) below).

**The defect is one sentence v2 added: "Fields outside `spec.traffic` do not count."** Two fields outside `traffic` undo the rule:
- **`backend.extAuth` on the Gateway** can refuse a `Lock`'s probe with `401` (MAJOR 1).
- **`strategy.inheritance: Override`** makes a Gateway-level policy authoritative over `<agent>-auth` (MAJOR 2).

**The harmless list is also never written down.** The one candidate list on record calls `transformation` harmless. agentgateway documents setting `:status` to `401` with `transformation` (MAJOR 3).

**The ADR records as the human's two costs the human has not been shown** (MAJOR 4).

---

## The three questions asked

**(a) Is the `allowedListeners` gate correct and fail-closed? When it admits none, can a ListenerSet still capture the Agent's traffic?**

**By the specification, no.** Gateway API v1.6.0 says a ListenerSet is attached only when three things hold, the first being "The ListenerSet is selected by the Gateway's AllowedListeners field". It also says "By default, Gateways do not allow ListenerSet attachment" (`listenerset_types.go:35-37`, `:56-59`). A ListenerSet the Gateway does not select is not attached, so its listeners are not programmed and cannot receive the probe.

So v2's gate removes the first critique's MAJOR 1 attack. The attack needed a ListenerSet in a tenant namespace, and that ListenerSet now has no effect and is not read.

The gate is fail-closed in both directions:
- **Admitting ListenerSets holds,** whether or not any ListenerSet exists.
- **An unreadable Gateway holds.**

**What it rests on:** agentgateway honouring `allowedListeners`. agentgateway does implement ListenerSet. Its own documentation sets `allowedListeners.namespaces.from: All` to attach one (agentgateway.dev, "Create a Gateway with ListenerSet support"). So the admitted case is real, and v2 is right to hold on it. That a ListenerSet the Gateway does not admit stays unprogrammed on 1.5.0 is conformance-tested upstream and unmeasured here. v2 should state it as an assumption (MINOR 1).

**One capture path is not a ListenerSet:** another listener on the Gateway itself, on the serving port (MINOR 2).

**(b) The field, exactly.** `Gateway.spec.allowedListeners` is `*AllowedListeners`, optional, with no default of its own (`gateway_types.go:291-295`).
- `AllowedListeners.namespaces` is `*ListenerNamespaces`, defaulted to `{from: None}` (`:328-335`).
- `ListenerNamespaces.from` is `*FromNamespaces`: enum `All | Selector | Same | None`, defaulted to `None` (`:338-352`). `selector` is a `LabelSelector`, read only when `from: Selector`.
- The standard-channel CRD carries the same defaults (`gateway.networking.k8s.io_gateways.yaml:140-169`).

So "admits none" has three stored forms, and all mean the same:
- `allowedListeners` absent;
- `allowedListeners: {}`, which the API server defaults to `namespaces: {from: None}`;
- an explicit `from: None`.

The harness Gateway sets none of them (`hack/e2e.sh:261-291`), so it admits none. v2's "admits any namespace" is imprecise in one case, `from: Selector` with a selector that matches no namespace (MINOR 1).

**(c) Is "hold everything when ListenerSets are admitted" acceptable under L1, and is it recorded clearly enough?** It is an acceptable consequence:
- It is L1's own logic applied to a source the slice cannot rule out.
- It is loud.
- Reading ListenerSets later lifts it without reversing anything.

**It is not recorded clearly, and it is not yet the human's** (MAJOR 4):
- The ADR says "if the assayd Gateway admits any ListenerSet". That reads as "if a ListenerSet is attached". The rule actually fires on the Gateway's setting alone, with no ListenerSet and no policy anywhere.
- It says "the chart's Gateway admits none by default". The chart ships no Gateway.
- It calls this "part of the same decision". The human decided L1 before this extension was written.

---

## Closure of the first critique's findings

| Finding | Status | Note |
|---|---|---|
| MAJOR 1: ListenerSet "attached" vs parentRef; any namespace can trigger | **Closed**, by a different mechanism than suggested, judged sound | ListenerSets are not read. Admitting any holds. Only whoever can edit the Gateway, or write a policy in its namespace, can trigger the hold. The ADR's wording is MAJOR 4 |
| MAJOR 2: RBAC unstated; the shipped chart holds everything | **Closed** | `get` on `gateways`, as a Role in the Gateway's namespace, beside the existing `-gateway-events` Role (`rbac.yaml:45-70`). The ClusterRole's `list` on `agentgatewaypolicies` covers that namespace (`operator-rules.yaml`). Not-served, NotFound and Forbidden all hold, which is fail-closed and right. Design 07's record of A70's grants is not in the edit list (MINOR 6) |
| MAJOR 3: `directResponse` and `extProc` missed; pin only 1.4.1 | **Partly closed** | Both are named not harmless. The rule is inverted and pins both versions. But the harmless list is not enumerated (MAJOR 3), and "fields outside `spec.traffic` do not count" is false (MAJORs 1, 2) |
| MAJOR 4: when the check runs | **Closed**; residue understated | The check now runs after the attributing `401`, in the same pass. The residue drops the first critique's lingering-enforcement case and states an unmeasured bound (MINOR 3) |
| MAJOR 5: reason order, Ready, clearing | **Mostly closed** | Order, deadline, Ready and clearing are stated. Gaps: Ready's reason beside `ForeignTrafficPolicy`; the missing-policy deadline reason; J2's pre-deadline `PolicyApplyIncomplete` with `Ready` kept; a clearing check that item 4 never runs (MINORs 4, 5) |
| MAJOR 6: re-creations of served Agents | **Closed in the rule**; the ADR's attribution is MAJOR 4 | Item 5 holds them, and the ADR names the outage and the page. It records them as accepted by the human, who was asked to confirm and has no recorded answer |
| MAJOR 7: override rests on an unmeasured `authorization` merge | **Closed in scope**; the case is under-specified; `Override` missed | Item 7 scopes the measured claim to case 7's shape and owes the merge case. The case does not put the group-G key in the route's key source (MINOR 7). `strategy.inheritance: Override` inverts the measured property (MAJOR 2) |
| MINOR 1: `sectionName` of another listener | **Closed** for listeners on another port | A same-port sibling listener is not covered (MINOR 2) |
| MINOR 2: scale note | **Closed** | It cites A72's N² note. A served Agent's report still costs one list and one Gateway `get` per resync, which "reuses the same list" understates. Style only |
| MINOR 3: ADR scope wider than the design | **Closed** | "neither `Create` nor any `Lock`" |
| MINOR 4: vacuous `GovernanceSkipped` for `Create` | **Closed** | "`GovernanceSkipped` is unchanged". §3.1's count of six reasons holds |
| MINOR 5: tests name no mutations, and miss cases | **Partly closed** | The table names a mutation per case. Missing cases are in MINOR 7 |
| MINOR 6: stale text outside design 03 | **Closed** | Both are listed. The AGENTS.md and CLAUDE.md paragraphs are byte-identical at 2c1be63, so "must stay byte-identical" holds |

---

## New findings

### MAJOR 1 — "Fields outside `spec.traffic` do not count" misses `backend.extAuth`, which can refuse a `Lock`'s probe with `401` from the Gateway level

**The rule.** A75 item 2(b)'s last bullet says "Fields outside `spec.traffic` do not count." Nothing in v2 gives a reason.

**What the CRD allows.** At both 1.4.1 and 1.5.0:
- A `backend` policy may target a `Gateway`. The `backend` description says so, and adds: "Targeting a higher level resource, like `Gateway`, is just a way to easily apply a policy to a group of backends."
- No CEL rule on `spec` restricts `backend`'s target kinds, except `backend.mcp` and `backend.ai` on a `Service`.
- `backend.extAuth` is "External authentication configuration for requests sent to this backend".
- agentgateway documents its external auth as "allowing the request to proceed to the backend or denying it with a specific status code and message" (agentgateway.dev, "BYO ext auth service").

**The failure.** Take a Gateway-level policy in the Gateway's namespace carrying only `backend.extAuth`, whose server refuses requests with no credential with `401`. Then:
- **`Create` is safe.** A prepared route has no `backendRefs`, no backend is selected, and the probe gets `500`.
- **Every `Lock` is not.** J2, K2 and the missing-policy `Lock` all run on a route whose one `backendRef` is the revision's Service. The probe is forwarded towards that backend, and `backend.extAuth` answers `401`.
- `ProbingAfter`'s digest branch attributes it, because `status.cards[]` records a digest for the serving revision (03:699). The operator's own card fetch goes to the Service directly and never meets the policy.
- v2's check lists the policy, finds no `traffic`, and does not count it.
- `Served` is recorded under `apikey` whether or not `<agent>-auth` took. **That is the false `Served` L1 exists to close,** reached through the one field class v2 exempted.

**Fix.**
- Replace the sentence with a classification over the whole `spec`, under the same allowlist rule as `traffic`:
  - `backend.extAuth` is not harmless.
  - Every other `backend` key (`ai`, `auth`, `health`, `http`, `mcp`, `tcp`, `tls`, `transformation`, `tunnel`) is classified with its reason. Doubtful ones are not harmless.
  - `frontend` can be classified harmless with a stated reason. Its keys are `accessLog`, `connect`, `http`, `metrics`, `networkAuthorization`, `proxyProtocol`, `tcp`, `tls` and `tracing`. `networkAuthorization` refuses at L4, before any HTTP status exists.
- Extend the unit pin to the `backend` and `frontend` key sets of both CRDs.
- Add an envtest case: a Gateway-level `backend.extAuth`-only policy holds a J2 `Lock`. Its mutation is to skip `backend`.

### MAJOR 2 — `strategy.inheritance: Override` is outside `spec.traffic`, inverts the override item 7 relies on, and v2 never mentions it

**What the CRD says.** Both CRDs carry `spec.strategy.inheritance`, enum `Default | Override`, valid only on `traffic` policies (the CEL rule "strategy.inheritance may only be set on traffic policies"). Its description: "When set to `Override`, this policy blocks traffic policies at more-specific attachment points from being included in the effective policy. This is useful when a gateway-level policy must remain authoritative for all routes below it."

A74's case 7 set no `strategy`, so it measured `Default`. Three things follow.

1. **Item 7 is false for an `Override` policy.** Take a Gateway-level policy with `apiKeyAuthentication`, `authorization` and `strategy.inheritance: Override`.
   - It counts under v2, because of its `traffic` fields, so it holds new transactions.
   - On an Agent already `Served`, item 7 says it "does not un-serve it" and is "reported on `GovernanceSkipped`'s message". `GovernanceSkipped` stays `False`, reason `Governed`.
   - By the CRD's own text, the Gateway's authentication and authorization are then the route's effective policy. `<agent>-auth` is excluded. The route admits whatever the Gateway's key set and rules admit, which may be every group on the Gateway.
   - `Governed` is then false, which breaks AGENTS.md rule 8. Item 7's "the route's `apiKeyAuthentication` replaces the Gateway's" is true only under `Default`, and v2 does not say so, which breaks rule 7.
2. **A harmless-fields-only `Override` policy does not count at all.** An example is `timeouts` plus `Override`. The description says it blocks more-specific **policies** from the effective policy, not just the fields it sets. So it would remove `<agent>-auth` from every Agent's route. Whether the exclusion is per policy or per field is unmeasured. Under the per-policy reading:
   - every served Agent is open, reads `Governed`, and nothing reports it;
   - `Create` stalls on `500`, and its deadline message says "`500`: the policy has not taken", which is not the cause (rule 8);
   - a J2 `Lock` stalls on `200`.
3. **The owed conformance case cannot find this.** It sets no `strategy`.

**Fix.**
- A Gateway-level `traffic` policy with `strategy.inheritance: Override` counts, whatever its `traffic` fields are.
- The unit pin covers `strategy`'s keys.
- Item 7 says the measured override holds only under `Default`, and names `Override` as a case where a served Agent's report must not read `Governed` without a measurement.
- The owed conformance case gains an `Override` variant: a Gateway-level API-key policy with `Override`, beside `<agent>-auth`, and a key only the route's key set holds.
  - A `401` means the Gateway's authentication is authoritative. A served Agent under such a policy then cannot honestly read `Governed`.
  - What it should read instead is a decision for the human, as item 7 already says for the merge.
- Add an envtest case: a `timeouts`-only `Override` policy holds. Its mutation is to ignore `strategy`.

### MAJOR 3 — the harmless list is not written into the design, and the only candidate list on record is wrong about `transformation`

**What v2 says.** A75 item 2(b) says the list "is built by classifying every `traffic` field", and names only what is not harmless: `directResponse`, `extProc`, and the authentication and authorization fields. The classification of the rest is left to whoever writes the unit test. That classification is the security content of the rule. The test pins whatever its author chose, so a wrong choice is pinned, not caught.

**The candidate list on record is wrong.** The first critique's candidates included `transformation`, "its schema sets no status". agentgateway documents the opposite, with an example that answers `401`:

```yaml
traffic:
  transformation:
    response:
      set:
      - name: ":status"
        value: 'request.uri.contains("foo=bar") ? 401 : 403'
```

The source is agentgateway.dev, "Change the response status on a route", for 1.2.x and 1.3.x. The 1.5.0 schema has the same `response.set[].name/value` shape.

On a Gateway, with a condition keyed on a missing credential, this is a hand-rolled authentication:
- On a prepared route, a response transformation that applies to the gateway's own `500` would answer `Create`'s probe with `401`.
- On a served route it answers a `Lock`'s probe.

An implementer who takes the candidate list builds exactly the false `Served` L1 closes. `headerModifiers.response.set` has the same shape. Whether it refuses `:status` is not documented.

**Fix.** Enumerate both lists in A75, with the evidence for each field.
- **Not harmless**, at least: `apiKeyAuthentication`, `authorization`, `basicAuthentication`, `directResponse`, `extAuth`, `extProc`, `jwtAuthentication`, and **`transformation`**.
- **`headerModifiers`**: not harmless, unless a measurement or source shows it cannot set `:status`.
- **Harmless**, each with its reason: `buffer` (`413`/`5xx`), `cors` (answers preflight only, and the probe is a `GET`), `csrf` (safe methods are allowed), `delay`, `phase`, `retry`, `timeouts` (`504`), `hostRewrite` (see MINOR 3 for the backend-induced case), and `rateLimit`, whose denial status should be cited and not assumed. Its global `failureMode` defaults to `FailClosed`, "denies the request".
- Then the unit test pins the design, not the implementer's reading.

### MAJOR 4 — the ADR records as the human's two costs the human has not been shown, and states one of them unclearly and falsely

Amendment 5's "What it costs, and the human accepted it" lists every re-creation of a served Agent. That cost came from the first critique's MAJOR 6, whose fix was "State it in the ADR's cost **for the human to confirm**." Its "ListenerSets" bullet says "This is part of the same decision". That rule is v2's own, written after the human decided L1.

Neither v2 nor anything this review can see records the human confirming either cost. ADR-0034 has made this exact correction once already: Amendment 1 re-attributed two clauses of D2 "to design 03's author". An ADR states what the human decided, and this would state more.

The ListenerSet bullet also misleads the reader it is written for:
- **"If the assayd Gateway admits any ListenerSet"** reads as "if a ListenerSet is attached". A75 item 3 fires on `allowedListeners` alone, with no ListenerSet and no policy anywhere. ListenerSets exist for shared Gateways, so an install that shares its Gateway through ListenerSets can publish no new Agent at all. That is the sentence the human needs to see.
- **"The chart's Gateway admits none by default"** is false. The chart ships no Gateway (AGENTS.md, and design 03 §3.2: "the Gateway object itself still arrives with the agentgateway subchart, which the chart does not carry"). What is true is that Gateway API defaults to none. That breaks AGENTS.md rule 7.
- **The cost list does not say every held new Agent pages.** A75 item 6 raises `PolicyApplyIncomplete` before the deadline, and design 10 pages on it after 5 minutes.

**Fix.**
- Present the re-creation outage and the `allowedListeners` hold to the human as consequences of L1 for confirmation, with one line each. Record the answer, dated.
- Until then, attribute both to design 03's author, as Amendment 1 did.
- Reword the bullet: "whenever `allowedListeners.namespaces.from` is anything but `None`, whether or not any ListenerSet or policy exists". Say that Gateway API's default admits none, and that the chart ships no Gateway. Say that each held Agent pages after 5 minutes.

### MINOR 1 — "admits any namespace" is not a predicate, and its reliance on agentgateway is unstated

`from: Selector` with a selector that matches no namespace admits no ListenerSet. Deciding that would take a namespace list the rule does not grant. State the predicate as data: holds unless `allowedListeners` is absent, or `namespaces` is absent, or `from` is `None`. A nil at any level reads as the CRD default, `None`. `Same`, `All` and every `Selector` hold, whatever the selector matches.

Then record, as an assumption and not a measurement, that a ListenerSet the Gateway does not admit is not programmed by agentgateway 1.5.0. An optional `make conformance-cluster` case would measure it: a non-admitted ListenerSet in another namespace, with a `PreRouting` API-key policy and a listener on the serving port and the Agent's hostname, and the prepared route still answering `500`.

### MINOR 2 — a same-port sibling listener on the Gateway, and `group: ""`, are outside the target rule

**The sibling listener.** Item 2(a) excludes a policy whose `sectionName` names another listener. It gives `tools` as the example, which is on port 8081 (`hack/e2e.sh:283-284`). v2's own ListenerSet reasoning, "a listener can capture an Agent's traffic", applies equally to the Gateway's own listeners. Suppose a second listener on port 8080 has a hostname more specific than `http`'s that matches the Agent's host. Then:
- it wins listener selection;
- a `PreRouting` API-key policy scoped to it answers the probe with `401` (`PreRouting` may target a Gateway listener);
- that policy does not count.

The residue is small. `<agent>-serving` is bound to `http`, so such a listener leaves the Agent unreachable rather than open. But `Served` would still be recorded falsely. Either count a `sectionName` naming any Gateway listener on the serving listener's port whose hostname could match the Agent's, or state the residue. The Gateway is already read, so the first costs nothing.

**The empty group.** The shipped `isHTTPRouteRef` accepts `group: ""` (`authforeign.go:138-141`). Item 2(a) says `gateway.networking.k8s.io` only. Count `""` too, in the fail-closed direction.

### MINOR 3 — the residue is understated, and states a bound nothing measured

Item 4 says the residue "needs a policy that lives less than one probe round trip". The first critique named a second residue: a policy deleted before the probe that the proxy still enforces. A74 timed a write's enforcement, about 14 ms, never a removal. So an administrator who deletes a long-standing Gateway policy can have the next probe's `401` credited, if `<agent>-auth` has not taken. No short-lived policy is needed. "Less than one probe round trip" is a bound nothing enforces (AGENTS.md rule 7).

State both residues:
- a policy written and deleted between the probe and the check;
- a policy deleted before the check whose removal the proxy has not applied, a window whose length is unmeasured.

Also record the criterion's edge. The list classifies what the **gateway** can answer. A field that makes the **backend** refuse the anonymous probe, for example a request header or host rewrite reaching an Agent that checks it, falls to §3.3.3's existing "backend that began refusing its card path" residue.

### MINOR 4 — `GatewayAuthPolicy` "clears when nothing counts any more", but item 4 checks only after an attributable `401`

A `Create` held while `<agent>-auth` never took shows the gap. It is NACK'd, and the proxy keeps the old configuration. After the Gateway policy is removed, every probe gets `500`. Item 4's read never runs, so the reason never clears, and the deadline message names a policy that no longer exists (rule 8).

**Fix.** Run the Gateway read and the list on every `ProbingAfter` pass, for reporting and clearing. Keep the credit gated on the read made after the `401`.

### MINOR 5 — three condition cases are still left to the implementer

- **Ready beside `ForeignTrafficPolicy`.** Item 6 gives a never-served `Create` `Ready=False`, reason `GatewayAuthPolicy`. The order puts `ForeignTrafficPolicy` first, and the shipped `reportForeign` sets the withhold reason only when none is set (`authtxn.go:330-332`). Say that `Ready`'s reason follows the same order.
- **The missing-policy deadline.** The deadline bullet names only `AuthEnforcementUnverified` and `AuthLockUnverified`. The missing-policy `Lock`'s deadline reason is `AuthPolicyMissing` (03:716). It is first in the order anyway. Say so.
- **J2 and K2 before the deadline.** Item 6 raises `PolicyApplyIncomplete=GatewayAuthPolicy` for them before the deadline, and says `Ready` is withheld "only at the deadline, as A72 specifies". That is the first `PolicyApplyIncomplete=True` beside `Ready=True`. §3.3.1's aggregation table (03:505-509) makes a failing serving-required route `Ready=False`. Design 10 then pages, after 5 minutes, an Agent that reads Ready. Choose one: withhold `Ready` while held, or raise `PolicyApplyIncomplete` only at the deadline. Then edit the sentences in MINOR 6.

### MINOR 6 — sentences outside the twelve listed edits that v2 contradicts

- **03:254, §3.1's A63 exception:** "For any other Agent, nothing carries it until the `Create`'s deadline raises `PolicyApplyIncomplete`". Under item 6, `GatewayAuthPolicy` carries it before the deadline.
- **03:718, after the deadline table:** "Before the deadline it carries only `GovernanceSkipped=AuthLockPending`, which never pages", and "A stalled J2 `Lock` therefore pages about 9 minutes after the edit". A held J2 `Lock` pages at about 5 minutes, depending on MINOR 5's choice.
- **03:505-509, §3.3.1's aggregation table,** if MINOR 5 keeps `Ready=True` beside `PolicyApplyIncomplete`.
- **Design 07's record of A70's grants** (`docs/designs/07-*.md`, at 07:551) gains the `gateways` Role. The first critique's MAJOR 2 owed it, and v2's item 10 omits it.
- **The Gateway-namespace list's failure mode.** `foreignTrafficPolicies` maps `NotFound` to "no policies" (`authforeign.go:52-54`). A75 should say that its own list does not copy that, and that a list error holds, like an unreadable Gateway.

### MINOR 7 — the owed tests

**The merge case.** Item 7's case says "a key in G tries the route". Under case 7's measured behaviour the route's `apiKeyAuthentication` replaces the Gateway's. So a key held only in the Gateway's key set gets `401` under both merge and override, which is the flaw the first critique found in case 7 itself. The G key must be in the **route's** key source: a `ConfigMap` in the run namespace carrying the chart's selector label, with `metadata.group` G. Only then do `403` and `200` separate override from merge.

Record also that agentgateway's documentation describes the merge two ways:
- the CRD says "all rules are merged";
- the route-delegation page says "all defined rules must be satisfied", which is an AND.

So the case may measure a third outcome. That is a further reason it is owed, not an argument against it.

**Missing envtest cases, with mutations:**
- `ForeignTrafficPolicy` outranks `GatewayAuthPolicy` (swap them);
- the deadline reason replaces `GatewayAuthPolicy` and the message names it (drop the name);
- a `targetSelectors` entry of kind `Gateway` matching the Gateway's labels (ignore selectors);
- a K2 `Lock` holds (skip K2);
- a failed Gateway-namespace list holds (map the error to "none");
- clearing after removal on a `500` (MINOR 4);
- `backend.extAuth` (MAJOR 1);
- `Override` (MAJOR 2);
- a `transformation`-only policy holds (MAJOR 3).

**The unit pin** covers `backend`, `frontend` and `strategy` as well as `traffic`, at both versions.

---

## Verified sound

- **`allowedListeners`**, its shape, and its default of none, at Gateway API v1.6.0 (question (b)). ListenerSet attachment requires the Gateway's selection (`listenerset_types.go:35-37, 56-59`). So not reading ListenerSets, and holding when any is admitted, is correct and fails closed (question (a)).
- **Who can trigger the hold now.** Only an identity that can edit the Gateway, or write an `AgentgatewayPolicy` in the Gateway's namespace. `assayd-gateway-policies` does not reserve that namespace, but it is not a tenant's namespace. The first critique's tenant-driven, fleet-wide hold is gone.
- **Namespace locality.** In both 1.4.1 and 1.5.0, a `targetRefs` item carries `group`, `kind`, `name`, `port` and `sectionName`, and a `targetSelectors` item carries `group`, `kind`, `matchLabels`, `port` and `sectionName`. Neither carries a `namespace`. So listing only the Gateway's namespace is complete for policies targeting the Gateway. A run-namespace policy is reserved to the operator, and route-level ones are §3.2's.
- **`port` needs no rule.** CEL refuses `port` on any target of a policy carrying `traffic` or `backend`.
- **The `traffic` key sets.** 1.4.1 and 1.5.0 carry the same 18: `apiKeyAuthentication`, `authorization`, `basicAuthentication`, `buffer`, `cors`, `csrf`, `delay`, `directResponse`, `extAuth`, `extProc`, `headerModifiers`, `hostRewrite`, `jwtAuthentication`, `phase`, `rateLimit`, `retry`, `timeouts` and `transformation`.
  - `directResponse` sets `status`, directly and under `conditional[].policy.status` keyed on CEL. It is correctly not harmless.
  - `extProc` hands the request to an external processor, with a `failureMode`. It is correctly not harmless.
  - `PreRouting` accepts only `extAuth`, `authorization`, `transformation`, `extProc`, the three authentications, and `cors`. Under a correct list, every one but `cors` counts.
- **The `spec` and `backend`/`frontend` key sets** are also identical across the two versions. So a per-version pin of the whole `spec` is cheap.
- **The RBAC.** The operator already takes `--gateway-name` and `--gateway-namespace` (`operator.yaml:79-80`). So the new Role's namespace is known, and it sits beside the existing `-gateway-events` Role. No ListenerSet grant is needed. An unreadable Gateway holds, and since the operator refuses to start without `HTTPRoute` served, from the same API group, "not served" does not arise in practice.
- **The check's timing.** It runs after the attributing `401`, in the same pass. `beforeObserved` is gated the same way.
- **Where the hold sits.** `Create` stays prepared, and a `Lock`'s route stays as the `Lock` keeps it. This is consistent with §3.3.3's "safe because the route was already open". `ProbingAfter`'s requeue lifts it without a watch.
- **Re-creations hold, as the rule, and the ADR names the outage and the page.** The attribution is MAJOR 4.
- **`GovernanceSkipped` is unchanged,** so §3.1's count of six reasons stands. `GatewayAuthPolicy` is a reason on an existing condition type, so design 02's closed type set is untouched.
- **Item 7's measured claim is accurately scoped to case 7's shape,** except for `strategy` (MAJOR 2).
- **Rejecting L2 and L3** remains coherent.

---

## The smallest set of changes to approvable

1. **Replace "Fields outside `spec.traffic` do not count"** with a classification over the whole `spec`. `backend.extAuth` counts. A `traffic` policy with `strategy.inheritance: Override` counts whatever else it sets. `frontend` is harmless, with its reason. Pin `traffic`, `backend`, `frontend` and `strategy` at 1.4.1 and 1.5.0 (MAJORs 1, 2).
2. **Enumerate the harmless list in A75, with evidence per field.** `transformation`, and `headerModifiers` until shown otherwise, are not harmless (MAJOR 3).
3. **Scope item 7 to `inheritance: Default`.** Name `Override` as unmeasured on served Agents. Add an `Override` variant to the owed conformance case, and put its group-G key in the route's key source (MAJOR 2, MINOR 7).
4. **Put the re-creation outage and the `allowedListeners` hold to the human for confirmation,** and record the answer. Until then, attribute both to design 03's author. Reword the ListenerSet bullet: it fires on the setting alone, give the exact predicate, say the chart ships no Gateway, and say that held Agents page (MAJOR 4, MINOR 1).
5. **The MINORs:**
   - the same-port listener and `group: ""` (MINOR 2);
   - both residues (MINOR 3);
   - a per-pass read for clearing (MINOR 4);
   - the three condition cases (MINOR 5);
   - the unlisted sentences, design 07's grant record, and the list's failure mode (MINOR 6);
   - the tests (MINOR 7).

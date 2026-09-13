# Critique: ADR-0034 Amendment 5 and design 03 A75 — L1, "detect and hold" for Gateway-level auth

- **Date**: 2026-09-13
- **Subject**: the uncommitted draft of ADR-0034 Amendment 5 and design 03 A75. It is read against PR #34 (`slice-conformance-cluster`), which holds design 03 through A74.
- **Read**: design 03 §3.1 (the rule for clearing `GovernanceSkipped`), §3.2, §3.3.1's aggregation rule, §3.3.3, §5, §8.1, and A70–A74; ADR-0034 and Amendments 1–4; design 02's condition vocabulary; `internal/controller/authtxn.go`, `authforeign.go`, `gatewaywiring.go`; `charts/assayd/files/operator-rules.yaml`, `charts/assayd/templates/rbac.yaml`, `charts/assayd/templates/admission.yaml`; `test/conformance/slice_keys_cluster_test.go` (case 7); `test/envtest/suite_test.go`.
- **Checked against, not read about**:
  - The vendored agentgateway CRD (`test/conformance/testdata/agentgateway-crds-1.4.1.tgz`, `agentgateway.dev_agentgatewaypolicies.yaml`).
  - The 1.5.0 CRD chart. That is the release the e2e and the slice's conformance cases run. It was pulled from `oci://ghcr.io/agentgateway/charts/agentgateway-crds` and diffed against 1.4.1.
  - `sigs.k8s.io/gateway-api` v1.6.0 (`go.mod`): `apis/v1/listenerset_types.go`, `apis/v1/shared_types.go`, and `config/crd/standard/`.

Verdict: REVISE — 0 BLOCKER, 7 MAJOR, 6 MINOR.

The decision is sound. So is the place the hold sits: `ProbingAfter`, after the write, where the probe's own requeue lifts it. The draft's rule is wrong in three places:

- **Who can trigger it.** Any namespace can trip it, not only the Gateway's.
- **What the operator can read.** On the shipped RBAC it trips on every install.
- **What it detects.** Two fields that can answer `401` are missing.

It also leaves an implementer to guess three things: which reason wins, what order the list and the probe run in, and what it costs a served Agent. And it rests the "already served" case on an override that A74 did not measure for `authorization`.

---

## Findings

### MAJOR 1 — "attached" means parentRef-naming in A75 and acceptance in the ADR, and parentRef-naming hands any namespace a fleet-wide hold

A75 item 1 lists policies "in the namespace of every `ListenerSet` whose `parentRef` names the assayd Gateway". The ADR says "a `ListenerSet` attached to it". In Gateway API v1.6.0 these are different sets:

- **A ListenerSet is attached only when the Gateway's `spec.allowedListeners` selects it.** The default admits none (`apis/v1/listenerset_types.go:35-37`, `gateway_types.go:291-292`).
- **Any namespace can write the `parentRef`.** `ParentGatewayReference` carries an optional `namespace` (`apis/v1/shared_types.go:1034`). So a ListenerSet in any namespace can name the assayd Gateway, whether or not the Gateway admits it.
- **Nothing reserves the policy.** `assayd-gateway-policies` reserves `AgentgatewayPolicy` writes in run namespaces only (`charts/assayd/templates/admission.yaml:151-158`).

**The attack.** A tenant who can create a `ListenerSet` and an `AgentgatewayPolicy` in their own namespace does both. Under A75 as written, that holds every new Agent in the cluster unpublished. It also holds every J2/K2 `Lock` and every re-creation (MAJOR 6). The ListenerSet it uses has no effect on the Gateway at all.

**Even a truly attached ListenerSet's policy does not sit "above the route".** The Gateway API says: "Policies that attach to a ListenerSet apply to all listeners defined in that resource. Policies do not impact listeners in the parent Gateway" (`listenerset_types.go:43-44`). A route reaches a ListenerSet's listeners only by naming the ListenerSet as its `parentRef` (`:39`). `<agent>-serving` names the Gateway, listener `http` (§3.2). So such a policy can reach the probe only in one way: a ListenerSet listener captures the probe's request, on the serving listener's port, with a hostname that matches the Agent's host. The ADR's "attached above the route" states a topology the spec says does not exist.

**Fix.**
- Read the assayd Gateway's `spec.allowedListeners`. If it admits no namespaces, which is the default, list no ListenerSets.
- Otherwise, list only in the namespaces it admits (`Same`, `Selector`, `All`).
- Count a ListenerSet only if it names the Gateway, and has a listener on the serving listener's port whose hostname is unset or matches the Agent's hostname.
- If the hold is to stay deliberately wider than that, say so in the ADR. Its "What it costs" must then name who can trigger the hold. Today it names only "an install that keeps such a policy on purpose".

### MAJOR 2 — the RBAC the rule needs is not stated, and on the shipped chart the draft's own failure rule holds every Agent

The chart's ClusterRole (`charts/assayd/files/operator-rules.yaml`, rendered by `charts/assayd/templates/rbac.yaml:3-14`) grants no `listenersets` and no `gateways`. The operator reads no `Gateway` object today: nothing in `internal/` or `cmd/` does. Meanwhile ListenerSet is served everywhere this project runs:

- **Gateway API v1.6.0.** ListenerSet is `gateway.networking.k8s.io/v1`, in the **standard** channel, `served: true` and `storage: true` (`config/crd/standard/gateway.networking.k8s.io_listenersets.yaml:10,32,771-772`).
- **The e2e and conformance.** Both install `standard-install.yaml` (`hack/e2e.sh:228`, `hack/conformance-cluster.sh:61`).
- **envtest.** It loads `config/crd/standard` (`test/envtest/suite_test.go:56`).

A75 item 1 says: "If it is served but the operator may not list it, the operator counts that as an authenticating policy that cannot be ruled out, and holds". So an implementation that follows A75 and grants nothing holds every new Agent, on every cluster. Every gateway e2e then dies at the route wait. MAJOR 1's fix needs a `gateways` read, and so does `targetSelectors` of kind `Gateway`, whose labels live on the Gateway object.

**Fix.** State the grants, as §3.2's RBAC paragraph does for every other kind:
- `get` on `gateways`. It can be a Role in the Gateway's namespace, with `resourceNames`.
- `list` on `listenersets`: cluster-wide, or per the admitted namespaces once MAJOR 1 is applied.
- Nothing new for `agentgatewaypolicies`. The ClusterRole already grants `list`, so the Gateway's namespace and any ListenerSet namespace are already covered.

Also state how "not served" differs from "forbidden": no kind match, or `NotFound`, against `Forbidden`. The shipped `foreignTrafficPolicies` maps `NotFound` to "none" (`internal/controller/authforeign.go:52-54`). Then design 07's RBAC table is owed the same change. The fail-closed direction itself is right (Verified sound). Without the grant it is simply always on.

### MAJOR 3 — the "authenticating" field list misses two fields that can answer `401`, and its own rationale covers them

The five named fields exist, with those names, in both 1.4.1 and 1.5.0. The two releases' `traffic` key sets are identical: 18 keys, none added or removed. But two other fields can refuse an anonymous `GET` with `401` from the Gateway level:

- **`traffic.directResponse`** returns any `status` from 200 to 599. It has a `conditional` form keyed on CEL (`directResponse.conditional[].condition`, `.policy.status`). So a condition on a missing credential, answering `401`, is a hand-rolled authentication the rule does not see.
- **`traffic.extProc`** sends each request to an external processor. That processor can end the request with its own response, and `failureMode` defaults toward closed. It is a common way to do authentication.

A75's reason for listing all five is "an unmeasured field that refuses anonymously would fool the probe the same way. It errs toward holding." That sentence covers these two, and the list does not. Either one reproduces exactly the false `Served` that L1 exists to close.

**The pin is also weak.** It reads the list "from the vendored CRD", which is 1.4.1. The gateway the slice runs is 1.5.0.

**Fix.**
- Invert the rule. A Gateway- or ListenerSet-level `traffic` policy holds unless every field it sets is in a named set that cannot produce a `401`. Candidates: `cors`, `csrf`, `headerModifiers`, `hostRewrite`, `retry`, `timeouts`, `buffer`, `delay`, `rateLimit` (which answers `429`), `transformation` (its schema sets no status), and `phase`.
- Pin every `traffic` field of **both** CRDs as classified, in both directions. Then a field that a later release adds fails the test and is not silently treated as harmless.

Keeping `authorization` alone on the holding side is defensible. What an authorization-only policy answers an anonymous request is unmeasured.

**Q2, for the record.** A non-`401` refusal cannot pass either probe. `Create` waits for `401` alone. `Lock`'s `ProbingAfter` accepts only an attributed `401`. So a Gateway-level `403` or `429` stalls a transaction to its deadline, whose message names the last answer. It never records `Served` falsely. It matters only for how honest the hold's message is.

### MAJOR 4 — when "no policy stands" is evaluated is unspecified, and the shipped pattern is the wrong order

A75 item 2 says a `401` "is credited only when no Gateway-level authenticating policy stands", and never says when that is checked. Look at the shipped analogue, `gatewayStep`. It lists foreign policies **before** `authStep` probes (`internal/controller/authtxn.go:386-392`). An implementer who copies it for the Gateway-level list credits any `401` produced by a policy written between the list and the probe. The window is one pass, which includes a probe of up to 5 s. A74 measured a policy write enforcing within about 14 ms. So a policy landing inside that window is enforcing before the probe arrives.

**Fix.** Require the check to run **after** the attributing `401`, in the same pass, and to credit the `401` only if that list finds none. Then state the residue:
- a policy written and deleted between the probe and the list;
- a deleted policy that the proxy still enforces for a moment. Removal latency is unmeasured, and A74 timed only a write.

§8.1 then owes an envtest case in which the policy appears between the list and the stubbed `401`. Its mutation is to list before the probe.

### MAJOR 5 — which reason `PolicyApplyIncomplete` carries is unspecified in three of the draft's own cases

A75 item 2 raises `PolicyApplyIncomplete` "at once with reason `GatewayAuthPolicy`". That collides with three existing rules:

- **The missing-policy `Lock`** already raises `PolicyApplyIncomplete=AuthPolicyMissing` **at entry** (design 03 §3.3.3; the deadline table at 03:716; §5 at 03:1238). §3.3.1 says one condition instance carries every failing path (03:520). The shipped code keeps the first reason and appends the rest (`authtxn.go:319-326`). So which reason does the Agent show: `AuthPolicyMissing` or `GatewayAuthPolicy`?
- **The deadline.** Item 2's last bullet says "the deadline outcome still applies: `AuthEnforcementUnverified` or `AuthLockUnverified`". Does the reason flip away from `GatewayAuthPolicy` at the deadline, and so hide the cause's name? It also gets the missing-policy row wrong: that row's deadline reason is `AuthPolicyMissing`, not `AuthLockUnverified` (03:716).
- **`ForeignTrafficPolicy` standing too.** Its report appends when the condition is already set (`authtxn.go:319-326`).

Two things are left unsaid as well:

- **`Ready`'s reason.** Before its deadline, `Create` withholds `Ready` with `AuthEnforcementPending` (`authtxn.go:1110`). Does that change?
- **When `GatewayAuthPolicy` clears.** The abandonment rule lists what clears on abandonment (03:724). `GatewayAuthPolicy` is not in it. An implementation that derives conditions from stored state gets this right by accident, and one that does not leaves a `Ready=False` that pages on an Agent with no transaction.

The owed test "`Create` holds with `GatewayAuthPolicy`" will fix whichever reading its author happened to pick.

**Fix.** State an order. One that fits §3.3.1:
- `GatewayAuthPolicy` is the reason while the policy stands, before and after the deadline. At the deadline the message appends the stage and the last answer.
- On a missing-policy `Lock`, `AuthPolicyMissing` stays the reason, because an open route is the more urgent fact, and `GatewayAuthPolicy` is appended.
- `ForeignTrafficPolicy` is appended per §3.3.1.
- Say what `Ready`'s reason is.
- Say that the reason clears when the policy goes, at `Served`, and on abandonment, and add it to 03:724's list.

### MAJOR 6 — the hold reaches Agents that are already served, through their re-creations, and the ADR's cost does not say so

A75 item 2 holds "`Create` and every `Lock`". Two of those transactions are re-creations for Agents that are already served:

- **A route re-create goes through `Create`.** A served `apikey` Agent whose `<agent>-serving` is deleted, for example by an Argo or Helm prune, is re-created through `Create`, prepared, and published only after the `401` (03:732; §5 at 03:1239). Under a Gateway-level policy it stays prepared, answering `500`, and **off the air** for as long as the policy stands.
- **A missing policy goes through `Lock`.** A served Agent whose `<agent>-auth` is deleted holds at `AuthPolicyMissing`, with `Ready=False`, and pages after 5 minutes. Its policy is re-created at once, so the only thing held is the record. The page still fires for every such Agent.

Against that:

- **The ADR's cost is narrower.** "What it costs, accepted" names "every new Agent unpublished, and every J2 or K2 lock unproved". The human accepted that stated cost, not an outage of a served Agent.
- **A75 item 3 reads as the opposite.** It says "An Agent already `Served` is not un-served when such a policy appears later ... It is reported ... not held." An implementer cannot tell whether the route re-create is exempt.

Both options are real:

- **Keep holding re-creations.** This is consistent: publishing a re-created route under the Gateway's `401` is exactly the false proof. It costs an extended outage.
- **Exempt them.** This reopens the hole for the re-created route.

**Fix.** Recommend the first. State it in the ADR's cost for the human to confirm, and rewrite item 3 as "not un-served, except that a re-creation of its route or its policy holds like any other `Create` or `Lock`".

### MAJOR 7 — item 3 rests a served Agent's safety on an override A74 did not measure for `authorization`, and the CRD documents a merge

A75 item 3 says that on a served Agent's route "`<agent>-auth` overrides the Gateway's (A74 case 7)". It concludes that such an Agent is "reported on `GovernanceSkipped`'s message, not held", so it still reads `Governed`. The vendored CRD's own description of `traffic.authorization` says: "If multiple authorization rules are applied across different policies, at the same or different attachment points, all rules are merged."

Case 7 cannot tell merge from override for `authorization` (`test/conformance/slice_keys_cluster_test.go:287` onward):

- **The Gateway's rule** is an `Allow` for `gwgroup`, whose key the route's key set does not hold. Once `<agent>-auth` attaches, that key fails **authentication** with `401`, under either semantics.
- **The rogue key's group** is in neither `Allow` rule, so it gets `403` under either semantics.
- **So the measured result**, "overridden, not merged", is a result about authentication.

`Allow` rules merged across levels are an OR. Suppose a Gateway-level policy's `Allow` matches a group whose keys the route's key set holds. It would then admit that group on a served Agent's route, while `GovernanceSkipped` says `Governed`. The same applies to `phase: PreRouting` policies, which run before route selection. Whether a route-level policy overrides one of those is also unmeasured. Under AGENTS.md rule 7, item 3 states a property nothing has measured and the CRD's own documentation contradicts.

**Fix.**
- Scope item 3's override to case 7's shape.
- Owe a `make conformance-cluster` case: a Gateway-level, authorization-only `Allow` admitting the rogue key's group, beside `<agent>-auth`. A rogue key that gets `200` means merge.
- Until that case exists, do not give the override as the reason a served Agent is safe. If it measures a merge, a served Agent under such a policy cannot honestly read `Governed`, and that is a decision for the human, beyond L1.

### MINOR 1 — a `sectionName` naming another listener is held on, with a message that is false

A Gateway `targetRef` may carry a `sectionName`. The CRD forbids one only for some `frontend` fields, and none of those are `traffic` fields. A `traffic` policy scoped to the `tools` listener does not reach `http`. That is the listener `<agent>-serving` attaches to, and where the probe goes (§3.3.3). A75 ignores `sectionName`, so it holds on such a policy, and its message says a `401` on this route "cannot be attributed". That message is false, which breaks AGENTS.md rule 8. `tools` is the egress listener. When agent egress is authenticated there, every Agent holds.

**Fix.** A `sectionName` counts only if it names the serving listener. The operator knows it, because it writes the listener into the route's `parentRef` (`internal/controller/httproute.go:149`). The same applies to a ListenerSet's listeners (MAJOR 1).

### MINOR 2 — the scale note undercounts, and cites a note that is not in §3.2

A75 item 4 says "One more live, paged list per reconcile". The actual reads are:

- one ListenerSet list, cluster-wide unless MAJOR 1 narrows it;
- one policy list in the Gateway's namespace;
- one policy list per distinct ListenerSet namespace;
- a Gateway `get`, under MAJOR 1 and MAJOR 2.

None of them depends on the Agent. So they can be read once per pass for every Agent, and the note should say so. "§3.2's N² note" does not exist in §3.2. The N×N arithmetic is in A72 (03:1389-1395).

### MINOR 3 — the ADR's scope is wider than the design's

The ADR says "no `-auth` transaction credits a `401` or records `Served`". A75 specifies `Create` and every `Lock`. `Narrow` is out of the slice. Its differential needs a kept key served the card, which is not fooled by an overriding Gateway-level authentication. It is not obviously covered by an `authorization` merge, though (MAJOR 7).

**Fix.** Say "`Create` and every `Lock`", or specify `Narrow`.

### MINOR 4 — `GovernanceSkipped` "keeps its in-flight reason" is vacuous for `Create`

A `Create`'s Agent has no published route, so `GovernanceSkipped` is vacuously `False` (03:254). The draft's bullet is true, but it reads as if a `True` reason were kept.

**Fix.** Say "`False` for a `Create`, `AuthLockPending` for a J2/K2 `Lock`, `AuthPolicyMissing` for a missing policy".

This also confirms that A75 adds no `GovernanceSkipped` reason, so §3.1's count of six stays correct.

### MINOR 5 — the owed tests do not name their mutations and miss the cases these findings add

Item 5 ends "Each needs its mutation". §8.1's convention names each mutation (03:1277 onward). The missing cases are:

- the list is forbidden, so the Agent holds;
- a ListenerSet in another namespace, or one the Gateway does not admit (MAJOR 1);
- a `sectionName` naming another listener (MINOR 1);
- the policy appearing between the list and the probe (MAJOR 4);
- a route re-create and a missing-policy `Lock` under a Gateway-level policy (MAJOR 6);
- reason precedence against `AuthPolicyMissing` and `ForeignTrafficPolicy` (MAJOR 5);
- the field-classification pin over both CRDs (MAJOR 3);
- the `make conformance-cluster` merge measurement (MAJOR 7).

### MINOR 6 — text outside design 03 goes stale with this

- **`internal/controller/authforeign.go:31-33`** says "whether it merges with the route's is unmeasured". A74 measured it for one shape.
- **AGENTS.md's paragraph on the compiler's slice** says the gap "is open".

Both stay true until A75 is implemented. They are owed by the change that implements it.

---

## Sentences in design 03 that A75 must also edit (Q7)

Each contradicts A75 as drafted:

1. **03:321, §3.2.** "Detection covers route-level targets only … So such a policy is still not detected, and is not claimed harmless." The last sentence, on shapes left unmeasured, stays, with MAJOR 7's scope added.
2. **03:598, §3.3.3's opening.** "on a Gateway with no Gateway- or ListenerSet-level auth policy: under one, `Lock`'s card-digest rule cannot tell …"
3. **03:624, `Create`'s probe.** "That holds on a Gateway with no … (`reviews/03-a56-critique.md` MINOR 5, open)", and "Making either a gate stays open, and needs a decision this design has not taken."
4. **03:699, `ProbingAfter`.** "This is open, with `Create`'s (above), and the product fix is a design decision this amendment does not take."
5. **03:704, "What it proves".** "only on a Gateway with no Gateway- or ListenerSet-level auth policy (above)", and "It cannot tell the Agent's `-auth` from a Gateway- or ListenerSet-level `traffic` policy, which §3.2's detection does not see (… MINOR 5, open)."
6. **03:712-716, the deadline table.** Each row's message gains the Gateway-level policy, and the missing-policy row keeps `AuthPolicyMissing` (MAJOR 5).
7. **03:724, abandonment's "What status returns to".** Its list of what clears gains `GatewayAuthPolicy`.
8. **03:732, the route re-create.** "published only after the anonymous `401`" gains the hold (MAJOR 6).
9. **§3.2's RBAC paragraph and watches bullet.** The new grants (MAJOR 2). The bullet should also record that no watch is added, because `ProbingAfter`'s requeue re-lists (Verified sound).
10. **§5 rows 03:1234, 1235, 1238, 1239 and 1240, plus a new row for the hold.** 1240's "for route-level targets" stays true, but it should point at the new row.
11. **§8.1: 03:1277's case list, and the `make conformance-cluster` bullet at 03:1298.** Case 7 is described there as fooling the probe, and the new cases belong beside it.
12. **03:3, the Status line.** A75 changes the approved slice's `Create` and `Lock`. The ADR amendment authorizes that, and the line should record it.

---

## Verified sound

- **The namespace scoping.** A policy in another namespace cannot target the Gateway, so A75 is right to look only in the Gateway's namespace and in each ListenerSet's own. In both 1.4.1 and 1.5.0, a `targetRefs` or `targetSelectors` entry has `group`, `kind`, `name`, `port` and `sectionName`, and no `namespace`.
- **What a `traffic` policy may target.** It may target `Gateway` and `ListenerSet`, by CRD rule, at both versions. `phase: PreRouting` may target only those two. So the two kinds A75 names are the only attachments above the route, and nothing else needs detecting.
- **The ListenerSet API.** It exists at the pinned Gateway API: `gateway.networking.k8s.io/v1`, standard channel, served.
- **Where the hold sits.** Holding at `ProbingAfter`, after the write and convergence, means that removing the policy lifts the hold at the next probe. `ProbingAfter` already requeues every 5 s, then every `CardRetryInterval`. So the hold needs no watch on Gateway-namespace policies, which the filtered cache does not hold (`gatewaywiring.go:201-214`).
- **The J2/K2 route.** Keeping it open while held is consistent with §3.3.3's "safe because the route was already open".
- **The NACK hold and `ForeignTrafficPolicy`'s hold.** Both are independent of this one and compose with it. A NACK returns to `Converging`, and the foreign hold also sits at `ProbingAfter`. Only their reasons need ordering (MAJOR 5).
- **`beforeObserved`.** Requiring "no policy stands" on the transition branch too is right. A `200` before proves only that nothing refused anonymous callers then.
- **Holding when the list is forbidden** is the right, fail-closed direction, once RBAC makes it the exception (MAJOR 2).
- **`GatewayAuthPolicy`.** Design 02 (`02:117`) closes condition **types**, not reasons. A reason on `PolicyApplyIncomplete` adds no type. Design 10's page keys on that type and a duration (03:716 text), so it pages with no design 10 edit.
- **L2 and L3.** The reasons given for rejecting them are coherent.

---

## The smallest set of changes to approvable

1. **Scope ListenerSets to the attachment the ADR names.** Read the Gateway's `allowedListeners`, list none by default, and count only ListenerSets the Gateway admits, with a listener that can capture the serving port and the Agent's host. Scope Gateway `sectionName` to the serving listener. Make the ADR's wording match (MAJOR 1, MINOR 1).
2. **Name the RBAC.** `get` on `gateways`, `list` on `listenersets`, and how "not served" differs from "forbidden" (MAJOR 2).
3. **Invert the field rule to a harmless allowlist,** and pin every `traffic` field of 1.4.1 and 1.5.0 as classified (MAJOR 3).
4. **Check "no policy stands" after the attributing `401`,** in the same pass, and state the residue (MAJOR 4).
5. **State the reason order,** `Ready`'s reason, and when `GatewayAuthPolicy` clears, including on abandonment (MAJOR 5).
6. **Say that re-creations hold.** Put the outage and the pages in the ADR's cost for the human to confirm, and rewrite item 3's "not held" (MAJOR 6).
7. **Scope item 3's override to case 7's shape,** and owe the authorization-merge measurement (MAJOR 7).
8. **Apply the twelve edits above.**

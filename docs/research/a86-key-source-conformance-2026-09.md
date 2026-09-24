# A86's key-source shapes, the listener rename, and §3.4.3's ordering, measured

**Measured 2026-09-24**, on throwaway k3d clusters with Gateway API v1.6.0 and
agentgateway 1.5.0 — the release `hack/e2e.sh` and phase 2 of
`hack/conformance-cluster.sh` install — by five cluster cases now in
`test/conformance/slice_keysource_cluster_test.go`, which run in phase 2 of
`make conformance-cluster`. **No operator runs in them**: every `<agent>-auth`
is `compiler.AuthPolicy`'s output, and what is measured is agentgateway, not
assayd. Design 03 A88 records what the measurements do to the design.

## Why it was run

Design 03 owed three `make conformance-cluster` measurements that each decide a
design sentence somebody had derived rather than measured:

1. **A86's three shapes** (§11 A86, "Three shapes nobody has measured"), and
   **§8.1 owed item 4's case**: what agentgateway reports, and what requests
   get, when a served `<agent>-auth`'s key source holds no key, holds only
   entries agentgateway rejects, or holds a key only under `binaryData`. A86
   built `ApiKeySourceEmpty` on "an empty key set takes every key to `401`",
   which was derived; §9 D6 listed "every entry rejected, if 1.5.0 reports that
   `Valid`" as a silent residue; and A86 counted a `binaryData` entry as
   PRESENT while saying "the owed conformance row decides it".
2. **§8.1 case 19 (f)'s owed measurement** (A81): the report agentgateway
   writes on a served `<agent>-auth` when the Gateway's serving listener is
   renamed. envtest writes that report itself (`summarisePolicy`), and the
   only cluster reading of it was by hand
   (`a80-served-route-walkthrough-2026-09.md`).
3. **§3.4.3's experiment**: whether an authentication-rejected request
   consumes a local rate limit's quota.

## The fixture

The key-source cases run in a run namespace of their own, `conf-keysrc`,
because the key source is namespace-wide — every `<agent>-auth` there selects
every ConfigMap labelled `assayd.dev/api-keys: "true"` — and the rest of the
slice cases share `conf-slice`'s key set. A Gateway `conf-keysrc` in
`conf-slice-gw`, shaped like `hack/e2e.sh`'s (listener `http` on 8080, admitting
routes from that namespace), an `agnhost` backend there, and the slice suite's
`curl` Pod. The Agent's route is published on the backend, and its policy is
applied and seen refusing an anonymous request with `401` before anything is
changed. The key ConfigMap is rewritten in place by server-side apply under one
field manager, and read back, so its content is exactly the shape measured.

The no-key and every-entry-rejected shapes are entered from a WORKING key
source and left back to one, so the admitted key's move between `200` and `401`
is the evidence that the data plane has the new key source. The `binaryData`
case's evidence is a canary under `data` in the same ConfigMap instead.

## 1. A key source that holds no key — measured

| form | admitted key | another group's key (was `403`) | anonymous | policy, current generation | route |
|---|---|---|---|---|---|
| a labelled ConfigMap with no entry | `200` → **`401`** within 233 ms of the write starting | `401` | `401` | real Gateway ancestor, `Accepted=True`/`Valid`, `Attached=True` | `Accepted=True`, `ResolvedRefs=True` |
| no labelled ConfigMap at all (§8.1 item 4) | `200` → **`401`** within 178 ms of the delete starting | `401` | `401` | the same converged tuple | the same |

**A86's derived sentence is measured**: an empty key set, and a missing one,
take every key to `401`, and nothing on agentgateway's side reports it — the
policy reads the whole converged tuple. So `ApiKeySourceEmpty` is the only report
of this outage there is, as A86 argued. Writing the keys back admits the key
again within about 220 ms. Each figure is timed from before the write, so it is
an upper bound on propagation that includes the write and its read-back.

## 2. Every entry rejected — measured

A key ConfigMap holding ONLY two entries in A82 row 10's rejected shape
(`{"key": "<plaintext>"}`, a raw key where a ConfigMap-sourced entry must carry
`keyHash`), written over a working one:

```
policy, generation unchanged (1), real Gateway ancestor:
  Accepted=True  reason=PartiallyValid
    "configMap conf-ks-keys contains invalid key conf-rejected-a: keys sourced
     from a ConfigMap must use keyHash, not a raw key, since ConfigMaps are not
     confidential
     configMap conf-ks-keys contains invalid key conf-rejected-b: …"
  Attached=True  reason=Attached  "Attached to all targets"
route: Accepted=True, ResolvedRefs=True
admitted key 200 → 401; anonymous 401;
the rejected entries' own plaintext keys 401
```

**1.5.0 does NOT report it `Valid`.** It reports `PartiallyValid` — the same
report A82 measured for ONE rejected entry beside valid ones — even though no
entry is valid and every key is refused. The message names every rejected
entry. So §9 D6's residue "every entry rejected, if 1.5.0 reports that `Valid`"
is **not real on 1.5.0**: the operator's policy half raises
`AuthPolicyNotAttached` on this report, with A83's partly-valid lead. Restoring a
valid entry returns the policy to `Valid` and admits the key.

What it does not show: A83's partly-valid lead says whether a credential is
still required "is not established either way". Here it is established — no
credential works at all — so the lead's hedge is true but under-states the
outage, and `ApiKeySourceEmpty` does not fire, because `countKeySource` counts
the two entries as present. That is the design's stated bound (entries are
counted, not parsed), now measured in its worst case.

## 3. A key held only under `binaryData` — measured, and it is NOT read

| key source | canary (valid key, group no policy admits, under `data`) | admitted key under `binaryData` | policy |
|---|---|---|---|
| canary under `data`, admitted key under `binaryData` | **`403`** — authenticated, so the ConfigMap is loaded | **`401`**, held 5 s | converged, `Valid`/`Attached` |
| admitted key under `binaryData`, nothing under `data` | `401` (gone) | **`401`**, held 5 s | converged, `Valid`/`Attached` |
| the same entry moved to `data` | — | **`200`** | converged |

**agentgateway 1.5.0 ignores `binaryData`.** A valid entry held there
authenticates nothing, is not reported as rejected, and leaves the policy
`Valid`; the same bytes under `data` admit the key within 226 ms of the write
starting. So a key source
whose only entries are under `binaryData` is a complete, silent authentication
outage at the gateway — and the operator does not report it either, because A86
counts a `binaryData` entry as PRESENT (§5's row, A86's "Settled" bullet, case
20 (c)'s second half). A86 anticipated exactly this ("keys held where
agentgateway does not read them are silent until measured") and deferred the
counting rule to this row. Design 03 A88 records it for a decision. **Nothing
in the operator is changed.**

## 4. The listener rename — the policy half's real report

A Gateway of the case's own, a served Agent on it (route published, policy
converged, anonymous `401`), then `spec.listeners[0].name` `http` →
`http-renamed`:

```
BEFORE  policy generation 1, one ancestor:
  {group=gateway.networking.k8s.io kind=Gateway name=conf-slice-rename namespace=conf-slice-gw}
    Accepted=True Valid gen=1 · Attached=True Attached gen=1

RENAMED policy generation 1 (unchanged), ONE ancestor — the real one is gone:
  {group=agentgateway.dev kind=Gateway name=StatusSummary}   (no namespace)
  controllerName agentgateway.dev/agentgateway
    Accepted=True  reason=Valid    gen=1  "Policy accepted"
    Attached=False reason=Pending  gen=1  "Policy is not attached: HTTPRoute
                                           conf-slice/<agent>-serving is not
                                           attached to any Gateway"
  route generation 1: Accepted=False NoMatchingParent
    "sectionName \"http\" not found" gen=1 · ResolvedRefs=True gen=1
  every request 404, anonymous and keyed alike

RESTORED the real ancestor, converged; anonymous 401, admitted key 200
```

What this pins against the operator's reading:

- `policyReport` short-circuits on `{group: agentgateway.dev, name:
  StatusSummary}` and reads this as broken, clause unattached — the
  `AuthPolicyNotAttached` report fires on what agentgateway actually writes, and
  `test/conformance/ancestor.go`'s transcription agrees. Both conditions are at
  the policy's current generation, so the short-circuit's ungated read is of a
  current report here.
- **Every request gets `404`**: nothing reaches the agent and there is no
  unauthenticated path, which is what A81's route-first order says of this
  pass. The policy half's "may be answering with no credential required" is
  hedged by A81 on this pass because the route is not read accepted; the
  measurement shows the hedge resolves to "no".
- **envtest's fixture differs in two fields the operator does not read**:
  `summarisePolicy` writes the synthetic ancestor with kind `StatusSummary` and
  reason `NotAttached`; 1.5.0 writes kind **`Gateway`** and reason **`Pending`**.
  `policyReport` reads neither, so no judgement changes; A82's
  `TestSliceAnUnattachedAuthPolicyLeavesAnAcceptedRouteOpen` already asserts the
  same `Pending` on the accepted-route shape.

## 5. §3.4.3: authentication before the rate limiter — measured

One `traffic` policy carrying authentication and `rateLimit.local: [{requests:
1, unit: Hours}]` on a published route; three anonymous requests, then two
authenticated, then one anonymous:

| authentication | three anonymous | first authenticated | second authenticated | anonymous after |
|---|---|---|---|---|
| API keys (`compiler.AuthPolicy`'s output plus the limit) | `401, 401, 401` | **`200`** | **`429`** | `401` |
| JWT (`jwtAuthentication`, `mode: Strict`, inline JWKS), §3.4.3's own shape | `401, 401, 401` | **`200`** | **`429`** | `401` |

§3.4.3's table reads `401, 401, 401` as "auth precedes the limiter, and rejected
requests do not consume quota". The two columns after it are the control the
table lacked: the one request of quota is still there after three refusals
(`200`), and the limiter does enforce (`429`). An unauthenticated request after
the quota is spent still gets `401`, so authentication answers first even then.
**On 1.5.0, for these two shapes, the denial-of-wallet path in §3.4.3 is not
real.**

## What it does not show

- One release (1.5.0), one gateway replica, one proxy. Local rate limits are
  per proxy by the CRD's own description; two replicas are not measured.
- `binaryData`: one entry shape, the valid JSON a `data` entry carries. Whether
  some other encoding under `binaryData` is read is not tried; the CRD and the
  controller's message say nothing about `binaryData` either way.
- Every entry rejected: one rejected shape, the raw `key`. Other rejections
  (an unparseable value, an empty value) are not tried.
- The rate-limit order: one limiter (`local`), with authentication and the
  limit in ONE policy on the route. A limit in a separate policy, a Gateway-level
  limit, a `global` limiter, and the ordering of `-guard` (§3.4.3's second
  bullet) are not measured.
- §3.2's owed sentence — that the operator emits exactly one `traffic` policy
  across a promotion and detects a second as `ForeignTrafficPolicy` — needs an
  operator, and this suite runs none; its gateway half is
  `TestSliceOnePerAgentPolicyCoversAPromotion` since A74, and the rest stays
  envtest's.

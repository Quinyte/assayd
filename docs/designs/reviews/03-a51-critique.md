# Design 03 A51 — independent adversarial critique

- **Scope**: design 03 amendment **A51** (`docs/designs/03-policy-compiler.md:737`, plus the body edits at `:3`, `:81`, `:106`, `:353`, `:355`, `:364`, `:444`), the note it plants in `docs/designs/11-connector-crd.md:99`, `ADR-0014` Amendment 1, and the four cross-design contradiction fixes in commit `af1f1c9` (designs 20, 03, 23, `architecture.md`). Includes the one uncommitted follow-up edit to `:81` and `:737`.
- **Read cold** against designs 01, 02, 04, 11, 13, 20, 23, 26, `docs/architecture.md`, `docs/research/agentgateway-v1.4.1-2026-08.md`, `docs/research/agentgateway-otlp-attributes-2026-09.md`, `charts/assayd/templates/admission.yaml`, `internal/controller/`.
- **Verdict: REVISE — 3 BLOCKER, 7 MAJOR, 3 MINOR.**

**A51's central diagnosis is correct, verified, and worth having.** `git show HEAD~1:docs/designs/03-policy-compiler.md` line 106 read *"the resolved tool server's advertised tool set (design 11 §4) — **not** an `AgentSpec` field"*. Checked against the producing document: the field is declared at `11:30` as `toolAllowlist: [read_claim, search_claims]     # narrows what the server offers`, inside §3 "CRD schema"; the cited anchor `11:49` says *"design 03 emits `AgentgatewayBackend` and tool-filter policy from `toolAllowlist`"*. `grep -io 'advertis[a-z]*' docs/designs/11-connector-crd.md` returns exactly one hit — line 99, text this same commit wrote. `tools/list` appears nowhere in design 11. The premise was false in both halves and five amendments rested on it. That finding stands.

What does not stand is A51's own execution of the rule it invokes. A51 opened design 11 and did not open design 01/13 (the second producer in the same sentence), design 26 (the storage argument's other half), or its own §3.3.1 (six sections away in the document it is amending) — and every blocker below is a claim that fails against one of those.

---

## BLOCKER findings

### BLOCKER 1 — A51 writes the ungated tool surface that §3.3.1 exists to forbid

**Files:** `docs/designs/03-policy-compiler.md:106` · `:229` · `:273` · `docs/designs/11-connector-crd.md:99`

A51's new provenance row states the undeclared case as an outcome:

> **Undeclared is the separate case**: the field is optional, it exists to narrow "what the server offers", so with no allowlist **the emitted filter is the server's own surface** — and *that* has no producer contract (`03:106`)

And plants the same conclusion in design 11:

> **`toolAllowlist` is optional, and omitting it means no tool filter is emitted at all** — the agent gets whatever the server offers. That is the one genuinely uncontracted surface in the tool facet and §5 should name it as a failure mode (`11:99`)

Both are refused by §3.3.1, in the same document, untouched by A51. The mandatory table:

> | Tool (MCP) route | `-auth` (as `traffic.jwtAuthentication.mcp`), **`-toolfilter`** (as `backend.mcp.authorization`, `action: Allow`) | **A tool route without its filter grants the server's entire tool surface** — and because the filter also prunes `tools/list`, its absence makes every tool *visible* as well as callable (§3.4.2) | (`03:273`)

and the rule that governs it:

> A mandatory concern whose input is absent is a **compile error** (`PolicyCompileFailed`, naming the missing input), **never an omitted policy**. (`03:229`)

`-toolfilter` is mandatory on the tool route. `toolAllowlist` is its input. An undeclared allowlist is therefore a mandatory concern with an absent input, which §3.3.1 makes `PolicyCompileFailed` and the route **withheld** — and `03:273`'s rationale column is verbatim the outcome A51 now records as accepted ("grants the server's entire tool surface"). `03:133` names this case explicitly: *"A route class that **is** emitted, with one mandatory concern's input absent, is the compile error of §3.3.1. This is the dangerous case: a route exists and a guarantee does not."*

The mechanism confirms the severity. `docs/research/agentgateway-v1.4.1-2026-08.md:472-478`: the filter is an `Authorization` policy, *"allow-list, default-deny"*, and *"This filters `tools/list` as well as `tools/call` — so a filtered tool is invisible, not just unreachable."* No `Allow` rules means no policy, which means no default-deny — every tool on the connector's MCP server callable and enumerable by the agent.

**Concrete failure:** a platform team publishes a `Connector` for a claims system whose MCP image exposes `read_claim`, `search_claims` and `void_claim`. They omit `toolAllowlist` — a schema-optional field with a comment saying it *narrows*, so omitting it reads as "no narrowing needed". An Agent binds `tools: [{name: claims-system}]`. Under §3.3.1 the route is withheld and the Agent reports `PolicyCompileFailed`. Under A51's new text the route ships and the agent can call `void_claim`. Two rules in one design, opposite answers, and the difference is a capability.

**Fix:** delete the "Undeclared is the separate case" clause from `03:106` and state the rule §3.3.1 already implies: **an undeclared `toolAllowlist` is a missing mandatory input; the tool route is withheld with `PolicyCompileFailed` naming the connector and the field.** Correct `11:99` to ask design 11 for the *converse* — that a tool facet intended for agent binding must declare `toolAllowlist`, and §5 gains the row "tool facet with no `toolAllowlist` ⇒ no agent can bind it; design 03 withholds the route", not "the agent gets whatever the server offers". If instead the design intends the permissive reading, it must first remove `-toolfilter` from `03:273`'s mandatory set and delete its static security case (`03:291`) — which is a security decision needing its own amendment and an ADR, not a clause in a provenance table.

### BLOCKER 2 — the undeclared rule requires the compiler to read the server's surface, which A51 says it never does

**Files:** `docs/designs/03-policy-compiler.md:106` · `:355` · `:737` · `docs/designs/11-connector-crd.md:99`

A51's own correction of A37 is unambiguous:

> **this compiler never reads a server's surface, only the Connector CR** (design 11 §4) (`03:355`)

Yet the provenance row A51 wrote four sections earlier says that with no allowlist *"the emitted filter **is** the server's own surface"* (`03:106`). To emit a filter whose content is the server's surface, the compiler must know that surface. It cannot: there is no `tools/list`, no discovery, no catalogue anywhere in design 11 — A51 says so itself at `03:737`. The sentence describes a mechanism the same amendment proves does not exist.

The commit then states the rule three incompatible ways across two documents:

| Where | Undeclared `toolAllowlist` ⇒ |
|---|---|
| `03:106` (body, **authoritative** per `03:3`) | a filter **is emitted**, equal to the server's surface |
| `11:99` (same commit) | **no filter is emitted at all**; agent gets whatever the server offers |
| `03:273` + `03:229` (untouched) | **route withheld**, `PolicyCompileFailed` |

`03:3` says *"the body is authoritative"*, so the version that governs is the one that is mechanically impossible.

**Fix:** as BLOCKER 1. Whichever rule survives, it must be written once and the other two occurrences deleted, not softened. This design's own status line already indicts itself for this shape — *"One rule is currently stated in five places … with no two agreeing"* (`03:3`) — and A51 has added a sixth.

### BLOCKER 3 — A51 concludes "inline, in one form" and leaves the two-form split standing six lines below it

**Files:** `docs/designs/03-policy-compiler.md:81` · `:87` · `:89-93` · `:95` · `:717`

The size-bound row, rewritten by A51 (including the uncommitted follow-up):

> **A51 narrows this.** The premise that `resolvedInputs` has no bound assayd can impose was checked against design 11 and does not survive … **So the sealed value is stored inline, in one form.** (`03:81`)

Six lines later, untouched, opening on the exact premise A51 falsified:

> **Where the sealed value lives, and why it is not always the same place (A47).** **The sealed value has no bound assayd may impose — a tool catalogue is design 11's to size** — so status is the wrong home for the large case. But the object that would replace it is not always reachable … the deciding property is **mode, not size** (`03:87`)

followed by a three-row table specifying `by reference` above 4 KiB (`03:91`), and a paragraph specifying the replacement `ConfigMap`, its name, its labels, its immutability, its 1 MiB residual bound, and an "**Owed to design 02**: extend that name-shape authority to cover `-inputs`" (`03:95`). None of it was deleted. `03:87` contains verbatim the sentence A51 says "does not survive".

§8 then tests the removed machinery:

> **A47's over-bound case**: a resolved catalogue past the **object bound** produces the compile error naming the producer and the measured size (`03:717`)

There is no object and no object bound after A51. That envtest case is now written against a mechanism the body deleted, which is precisely the shape AGENTS.md rule 1 is about — except here the test cannot even be run to fail.

**Concrete failure:** an implementer reads §3.1 top-to-bottom. Line 81 says write it inline. Line 91 says write it by reference above 4 KiB. Line 95 tells them to create a `ConfigMap` and asks design 02 for a name-shape extension to collect it. Line 717 tells them to write a test for a bound that exists in one of those readings. They will implement one and the reviewer will read the other.

**Fix:** if A51's conclusion stands, delete `03:87`, the table at `03:89-93`, and `03:95` outright; delete the "Owed to design 02" name-shape request, which now asks for collection of an object nothing creates; and rewrite `03:717`'s A47 case to the inline bound A51 actually leaves (see MAJOR 4 — there is not currently one). If the two forms are to be kept, `03:81` must be withdrawn. A51 cannot assert one form in the row above the table that specifies three cases.

---

## MAJOR findings

### MAJOR 1 — "two of those three are somebody's spec on another CR" was never checked against the second producer

**Files:** `docs/designs/03-policy-compiler.md:353` · `:107` · `:737` · `docs/designs/01-knowledgegraph-provider-contract.md` · `docs/designs/13-graphiti-adapter.md`

A51 rewrites A25 with:

> §3.1's provenance table lists inputs that are **not Agent spec**: a Connector CR's `toolAllowlist` (11), a resolved KG endpoint (01/13), and `status.llmFallbackActive` … **Two of those three are somebody's spec on another CR (A51)** (`03:353`)

The second of the two is `knowledge[].endpoint`, sourced at `03:107` from *"the resolved `KnowledgeGraph` CR (design 01/13)"*. `grep -n "KnowledgeGraph\|kind: Knowledge" docs/designs/01-*.md docs/designs/13-*.md docs/designs/02-*.md` returns exactly one hit: design 01's own title. **There is no `KnowledgeGraph` CR defined anywhere in the corpus.** Design 01 refers to a "KG CR" carrying conditions and status (`01:84`, `01:137`, `01:151`) and never gives it a schema. What design 01 does say about the endpoint is that it is derived, not declared: *"The Agent CR binding `{name, version}` compiles to a route to that exact endpoint"* (`01:45`), and design 13 offers both a managed adapter and a *"BYO endpoint mode"* (`13:15`).

So A51 asserted the spec-ness of an input on a CR that no design defines, in the amendment whose entire thesis is that the previous author asserted something about a producing document without opening it. The correction may be right — but it is unverified, and by this project's rule an unverified cross-design claim is the defect, not the conclusion.

**Fix:** either check design 01/13 and state what `knowledge[].endpoint` actually is (my read: derived by the provider from `{name, version}`, hence *neither* spec nor another CR's field — a third category), or reduce the claim to the one producer A51 verified: *"`tools[].toolAllowlist` is somebody's spec on another CR. `knowledge[].endpoint`'s provenance is unverified — design 01/13 define no `KnowledgeGraph` CR, which `03:107` cites; that citation is owed a target."*

### MAJOR 2 — `status.llmFallbackActive` is a third category, and A25 is *not* misstated for it

**Files:** `docs/designs/03-policy-compiler.md:111` · `:353` · `:737`

A51's headline on A25: *"'not every producer can mint' is true of this design and **misstated in general**: a Connector author can be held to a review; what they cannot do is mint a revision of *this* Agent"* (`03:737`). The body applies that rescue to all three inputs collectively — *"so 'mint a revision' is wrong for **them**"* (`03:353`).

It cannot apply to the third. `03:111` states it plainly:

> | `llm.fallbackActive` | `status.llmFallbackActive`, controller-set by design 20 §3 — **status, not spec**, so it mints no revision by design | yes |

This is status on the Agent's **own** CR, written by a controller during an incident. There is no author, no CR to edit, and no review to hold anybody to. A25's original claim — that not every producer can mint a revision — is true of it without qualification and needs no narrowing. A51 counted "two of three" correctly and then wrote the rescue over all three.

**Fix:** split the sentence at `03:353`. Say: two of the three are another CR's spec, whose author can be held to a review but cannot mint a revision of *this* Agent; the third is this Agent's own status, deliberately mint-free (`03:111`), and for it A25's statement is exactly right. Then say which of the three cells of §3.3.3's table each lands in, because they are no longer one case.

### MAJOR 3 — "the producer can be edited under review" is a bound nothing enforces

**Files:** `docs/designs/03-policy-compiler.md:353` · `:355` · `:737` · `charts/assayd/templates/admission.yaml` · `docs/designs/11-connector-crd.md:58`

A51 softens A25 on the strength of a review that does not exist: *"the producer **can be edited under review**"* (`03:353`). I looked for it.

- The repository contains exactly two admission policies (`charts/assayd/templates/admission.yaml`): `assayd-namespace-labels`, which reserves the `assayd.dev/*` namespace labels to the operator SAs, and `assayd-gateway-routes`, which restricts HTTPRoute authorship on the assayd Gateway. Neither matches `Connector`.
- `grep -rniI "ValidatingAdmission|MutatingWebhook|admissionregistration" charts/ config/ internal/ api/ cmd/` returns only those two policies and their RBAC. There is no webhook anywhere.
- Design 11's only admission clause is signed-image: *"Catalog image unsigned | Admission rejects (same bar as agents)"* (`11:58`). It says nothing about who may edit `spec.tool.toolAllowlist`, and design 11 §11 (`11:92`) records that even the *name-uniqueness* rule three designs assume "is enforced at admission" has no enforcement in this repository.
- Design 11 defines no revision, gate or approval on a Connector spec edit. Design 02's revision gate keys on `AgentSpec`.

So a Connector spec edit is an ordinary `kubectl apply` by anyone with RBAC edit on `connectors` in the namespace. A51's own A37 paragraph says why that matters — *"a spec edit on a CR the Agent's revision gate does not cover still publishes a capability R1 was never evaluated with"* (`03:737`) — and then the A25 paragraph rests the mitigation on the review it just said does not cover it. AGENTS.md rule 7: *"Never state a bound the code does not enforce."*

The operational hole is still closed, by A37's freeze, not by any review. Say that.

**Fix:** replace *"the producer can be edited under review"* at `03:353` with what is true: *"the producer is an ordinary namespaced CR edit with no gate, no revision and no admission check — verified against design 11 and `charts/assayd/templates/admission.yaml`, which carries two policies, neither touching `Connector`. Nothing catches the edit; what protects R1 is A37's freeze alone."* Record on design 11 as owed: whether a `toolAllowlist` widening should require anything at all.

### MAJOR 4 — collapsing to inline deletes the only stated bound, and the seal is a one-shot transition

**Files:** `docs/designs/03-policy-compiler.md:79` · `:81` · `:91-93` · `:717` · `docs/designs/11-connector-crd.md:99`

Under A47 every case had a stated bound and a stated failure: by-reference cases bounded by the ConfigMap's 1 MiB (`03:95`), inline cases bounded at 4 KiB with *"a value over 4 KiB is a compile error naming the binding"* (`03:92`). A51 collapses to inline and states no bound at all — `03:81` says the bound is a `maxItems` **owed to design 11**, and concedes *"Neither bound is enforced today"*. Design 11 records the request at `11:99` and does not have the field yet; design 11 is P2 and its CRD does not exist in this repository.

So after A51 the only defence against an oversized `resolvedInputs` is a schema constraint in an unwritten CRD of an unimplemented design. Meanwhile the seal writes into Agent **status**, once per retained revision, and the retained set is *"`revisionHistoryLimit + 2 + holds`, at least four at the default"* (`03:81`).

**Concrete failure:** a connector to a large system-of-record declares 3,000 tool names. Four retained revisions × ~60 KB of names ≈ 240 KB of `resolvedInputs` in one Agent's status, on top of four `behaviourProjection`s. Push it further — a generated allowlist, several bound connectors — and the status update exceeds etcd's object limit and is rejected. `03:79` makes that unrecoverable: *"The seal is a **transition, evaluated once** … Nothing seals before that instant and **nothing seals after it**."* The revision goes active, the seal write fails, and there is no second attempt. The Agent serves with no sealed operand, and `03:380` says an unsealed active binding withholds the capability — permanently, with no condition naming a size. NFR-8 says a degraded path is never silent; this one is silent about the actual cause.

**Fix:** state a assayd-side bound on the inline form that does not depend on design 11 shipping — the 4 KiB threshold A51 is deleting is already the right number — with the compile error `03:92` already specifies, and add the §5 row. Then keep design 11's `maxItems` as the *producer-side* bound so the number is the producer's rather than assayd's, which is A51's stated intent. A bound owed to an unimplemented design is not a bound.

### MAJOR 5 — A51 consolidates onto a status write without opening design 26, which forbids that write in hard mode

**Files:** `docs/designs/03-policy-compiler.md:78` · `:81` · `:93` · `docs/designs/26-tenant-cr.md:56` · `:116`

A47's reachability argument was about **reads**: *"status is on the CR and is readable in both, an object is not"* (`03:93`). A51 keeps the read-side conclusion and drops the row. But the seal is a **write**: *"`resolvedInputs` and `sealedBindings` are **written once, in the seal's status update**"* (`03:78`).

Design 26 §4 gives the host-side compiler read only:

> | **policy compiler** (gateway config) | **host-side** — it writes gateway CRs on the host cluster … and *reads* tenant CRs through the vCluster kubeconfig. **It holds no tenant-workload write access** (`26:56`)

and A1 forbids the specific write outright:

> It is deliberately **not** a write to the Agent's status: design 02 A37 makes `status.activeRevisionDigest` the collision guard's authority *because* only the agent-operator may write it, and **a compiler holding `agents/status` `patch` in every hard-mode vCluster would hand a compromised compiler the power to mark an ungated spec as the active revision** (`26:116`)

So in hard mode the compiler resolves the value and cannot write it, and the tenant's in-vCluster agent-operator can write it and does not have it. No channel exists. The row A51 renders obsolete (`03:93`) was the one place §3.1 tied this to the owed contract — *"design 03 A45 already owes the hard-mode producer contract and this belongs to it"* — and A51's replacement carries no such link.

To be fair to A51: on the narrow question the task poses, **its conclusion is right**. Killing the size motivation does leave inline viable in *both* reachability cases, because reachability was an argument *against* by-reference, never for it; `26:56` and `26:109` confirm the compiler is host-side with vCluster read, and an external Agent has no run namespace. A51 did not quietly claim reachability it lacked. The defect is the adjacent one it did not check.

**Fix:** restore the hard-mode dependency in A51's replacement text: *"inline in one form; in hard mode the write itself is owed to A45's producer contract, since design 26 A1 denies the compiler `agents/status` patch (`26:116`) and the in-vCluster agent-operator that may write it does not hold the resolution."* Or state that the seal rides design 02's agent-operator status update in every mode, and say so at `03:78`, which today names no writer.

### MAJOR 6 — §11's A50 still carries the "no producer" claim design 04 A5 says was wrong: the fifth twin

**Files:** `docs/designs/03-policy-compiler.md:738` · `:444` · `docs/designs/20-drift-controllers.md:81` · `docs/designs/04-receipt-tap.md:111`

This commit's stated purpose includes fixing *"Four self-contradictions from the previous two commits, where a correction landed in one place and not its twin"* — and design 20 A7 was rewritten for exactly this. Design 04 A5 names both consumers:

> **design 03 A50 and design 20 A7 both concluded there was "no producer" and withdrew witnesses on that basis. That conclusion was wrong** (`04:111`)

Design 20 A7 was corrected (`20:81`). Design 03 §5's withdrawal row was corrected (`03:444`). Design 03 §11's A50 entry was **not**, and still reads:

> design 04 A4 adds the field and names **no** agentgateway OTLP attribute, no assayd-owned stamping mechanism and no grammar for it … so the check had no input … A40's canary attributes by the full endpoint identity in the receipt, which is `hop.endpoint` — **the other field with no producer** — so **`llmFallbackActive` cannot legitimately go true on any cluster today**, one replica or many (`03:738`)

Measured: `04:119` gives `hop.gatewayInstance` *"four, all default"* producers; `04:115-117` give three of `hop.endpoint`'s four parts as default. A50 as written is false in the producing document's own words, in the design under review, in a commit whose thesis is that this exact failure mode keeps recurring.

The "§11 is provenance only" defence at `03:3` does not carry: A50 is written in the present tense about the current cluster (*"cannot legitimately go true on any cluster today"*), it is the entry a reader consults for A50's reasoning, and the same commit judged the identical sentence in design 20 worth correcting.

**Fix:** apply `03:444`'s correction to `03:738`. State that the withdrawal stands on the routing argument (nothing routes a canary to a chosen replica through a Service), not on absent producers, and that the fields do exist — with the residual limits MAJOR 7 names.

### MAJOR 7 — the Azure limit is stated as one; design 04 A5 records two, and "outside that pair the canary attributes correctly" is false

**Files:** `docs/designs/03-policy-compiler.md:444` · `docs/designs/20-drift-controllers.md:81` · `docs/designs/04-receipt-tap.md:115` · `:118`

Design 20 A7's new text: *"**one real limit survives**: Azure's `deploymentName` is emitted nowhere"* (`20:81`). Design 03 §5 goes further: *"**Outside that pair the canary attributes correctly**"* (`03:444`).

Design 04 A5 records two defects on the Azure path, not one:

> | `hop.endpoint.arm` | `gen_ai.provider.name` | **default**, and it must be **mapped, not passed through**: agentgateway emits `azure`, which is not in semconv v1.41.0's closed enum … and **the CRD arm — `azureopenai` versus `azure` — is not recoverable from the export** (`04:115`)
> | `hop.endpoint` **instance fields** | — | **NOT emitted.** Azure `deploymentName` and Vertex `projectId` appear nowhere (`04:118`)

A drift fallback between an `azureopenai` arm and an `azure` arm on the same host is therefore *also* unattributable — the arm part of the tuple collapses in the export. Vertex `projectId` is a third. So "one real limit" is wrong and "outside that pair the canary attributes correctly" is affirmatively false for at least two other pairs.

**Fix:** in both places, name the limits design 04 A5 actually records — `deploymentName` not emitted, `projectId` not emitted, and the `azure`/`azureopenai` arm not recoverable — and drop the totality claim, or state it as "outside those three cases", which is what was measured.

---

## MINOR findings

### MINOR 1 — "no catalogue" is loose; design 11 has one, of a different kind

**Files:** `docs/designs/03-policy-compiler.md:737` · `docs/designs/11-connector-crd.md:10`, `:16`, `:28`, `:58`

A51: *"Design 11 contains no advertised-set concept, no `tools/list` discovery and **no catalogue**"*. Design 11 uses "catalog" seven times — *"MCP catalog content (packs)"* (`11:10`), *"Artifact (catalog images)"* as a charter primitive (`11:16`), `ghcr.io/assayd-catalog/fhir-mcp:1.2.0` (`11:28`), *"Catalog image unsigned | Admission rejects"* (`11:58`). It is an **image** catalog, not a tool catalogue, so A51's conclusion is unaffected — but the sentence as written is checkable and false, in an amendment whose authority is that it checked.

**Fix:** *"no catalogue **of tools**"*, or *"no tool-set discovery of any kind; design 11's 'catalog' is the signed pack-image catalog (`11:28`), a different thing."*

### MINOR 2 — "0027 and 0028, cited throughout" — 0027 is cited once

**Files:** `docs/designs/03-policy-compiler.md:5` · `:277`

Verified counts in the body: `ADR-0003` **0** (correctly removed), `ADR-0028` **15**, `ADR-0014` **7**, `ADR-0019` **2**, `ADR-0027` **1** — a single parenthetical in the card-discovery table cell at `03:277`. Adding 0027 to the header is right; "cited throughout" is not what one table cell is. Trivial, but this commit's whole warrant is that its statements survive a grep.

**Fix:** *"0028 cited throughout; 0027 cited once (§3.3.1's card-discovery row)."*

### MINOR 3 — the falsified vocabulary survives in four places A51 did not sweep

**Files:** `docs/designs/03-policy-compiler.md:79`, `:87`, `:95`, `:741`

A51 corrects the premise and leaves the words that carried it: *"seal its newly-arrived **catalogue** onto a route"* (`03:79`), *"a tool **catalogue** is design 11's to size"* (`03:87`), *"outlive its Agent holding a resolved **catalogue**"* (`03:95`), and §11's A47 entry still opens *"`resolvedInputs` holds a tool server's **advertised catalogue**"* and closes *"**design 11 is owed a stated catalogue bound**"* (`03:741`) — which `11:99` now explicitly refuses (*"there is no such concept here"*). `03:79` and `03:87` are body text and rank with BLOCKER 3; `03:741` is provenance and is the least of these.

**Fix:** sweep. The word appearing anywhere outside a quoted historical claim will send the next reader back to the premise A51 was written to kill.

---

## Two things A51 gets right, checked rather than assumed

**ADR-0014 Amendment 1 is fully supported by the primary-source note, at the strength claimed.** `docs/research/agentgateway-v1.4.1-2026-08.md:381-385`: *"**Codex BLOCKER 6 is confirmed: there is no generic egress-allowlist field, anywhere.** `grep -rni 'egress|allowlist' controller/api/` returns exactly **one** hit in the whole CRD API, and it is unrelated — the MCP JSON-RPC `methods` allowlist. **There is no host allowlist, no domain allowlist, no provider allowlist, and no model allowlist on `AgentgatewayBackend`.**"* The claims table repeats it as **REFUTED** at `:649`, and the note's own recommendation is the amendment's wording: *"It can only compile to *Backend construction* … ADR-0014's BAA control needs rewording accordingly"* (`:425-429`). The amendment's carve-outs are also right: PHI redaction *is* a real gateway policy, and the "still open" paragraph correctly refuses to call the BAA claim machine-checked while the entry grammar is undefined. **No finding.**

**The four cross-design contradiction fixes are removals, not relocations.** `grep -rn "consumerBudgets|per-consumer budget"` across designs 03, 23, 26, `architecture.md` and ADR-0016 returns only the deliberate tombstones (`03:38`, `03:113`, `03:740`, `26:132`, `26:144`) and `architecture.md:166`'s explicit negation. Design 23's *"rate limits per `consumerBudgets`"* became *"rate limits **when design 26 A2's tenant intent lands** — there is no per-consumer quota today"*; `architecture.md`'s external-consumer list lost the phrase it had just denied. Both are clean. Design 20 A7's rewrite agrees with design 04 A5 on the numbers (four `gatewayInstance` producers, three of four `hop.endpoint` parts — verified against `04:115-119`); it fails only on totality, which is MAJOR 7, and design 03's twin at `03:738` was missed, which is MAJOR 6.

---

## Verdict

**design 03 A51: REVISE — 3 BLOCKER, 7 MAJOR, 3 MINOR.**

A51 is the right finding, correctly diagnosed, and it should not be withdrawn. But it repeats the failure it names, one document over: it verified design 11 and then made claims about design 01/13, design 26 and its own §3.3.1 without opening them — and the worst of those, the undeclared-allowlist rule, writes an ungated tool surface into two designs at once. The status line at `03:3` already says this body needs consolidation rather than patching. The three blockers are evidence for that judgement, not against it: BLOCKER 3 exists purely because a conclusion was written into a table row while the section beneath it kept the opposite. Fix the three blockers in place — they are deletions, not new mechanism — and carry MAJOR 1, 2, 4 and 5 into the consolidation as the four cases the current text does not separate.

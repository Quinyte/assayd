# Accuracy audit: `docs/architecture.md` v1.0 vs the approved design corpus

- **Verdict**: **REVISE** — 13 findings (7 material, 6 minor)
- **Scope**: consistency/accuracy audit, not a design critique. No architectural judgements; every finding is "the document says X, the corpus says Y".
- **Audited against**: designs 01–27 (all critique-passed, with their §11/§12/§14 amendment sections), ADRs 0001–0026, `docs/designs/README.md`, `docs/research/` (12 notes).
- **Independence note**: performed by the session that ran the independent adversarial critiques of designs 03–27.

**Headline**: the ten "As designed" blocks are, with one exception, *accurate* — §04, §06 (both), §07, §08, §09, §13, §14, §15 and §17 each check out claim-by-claim against their ADRs and designs, including fine details (Wilson lower bound, `LabelsSparse` n≥200, transport-derived receipt discrimination, single-use voucher, priority bands, the ~4–5-pod hard-tenant figure). The problems are concentrated in three places: **§00's attribution**, **§10 (no block at all)**, and **stale text in older sections that the design phase corrected**.

---

## Q1 — Does §00 accurately state the corrections, and does it omit any?

### F1. MATERIAL — six of the seven rows attribute to v0.1 something v0.1 never said

`architecture.md:12-20`. The column is headed "**v0.1 said**" and the intro frames the table as "the places [the design phase] **corrected** v0.1". Checked against the v0.1 text, only **row 7** (six CRDs) is a genuine v0.1 correction. The other six corrected **design drafts**, caught by critique before anything shipped:

| Row | What v0.1 actually said | Where the correction really happened |
|---|---|---|
| 1 — adapter extracts at write time | v0.1 §13 already put extraction in the *pipeline*: "acquire → normalize → **extract (ontology-guided LLM)** → resolve" | design 13's **draft**; 13-review f1 |
| 2 — forks copy-forward episodes | v0.1 §13 Operations said only "snapshot per change (namespace-per-version)" | design 13's **draft**; 13-review f2 |
| 3 — receipts carried generated IDs | v0.1 §07 never mentions receipt identity at all | design 04's **draft** (ULID); 04-review f1 |
| 4 — chaining would run in the tap | v0.1 §06 said "hash-chained receipts" with no placement | design 27's **draft**; 27-review f1 |
| 5 — eval jobs use the gate controller's identity | v0.1 §08 says nothing about eval-job identity | design 16's **draft**; 16-review f1 |
| 6 — tenancy "adds no new isolation primitives" | that phrase is **design 26's own D1**, not a v0.1 claim | design 26's **draft**; 26-review f1/f2 |

The corrections themselves are described accurately and ADR-attributed correctly — the defect is purely the attribution. **Fix**: retitle the column ("Where the design phase started" / "The draft said") or split the table in two — *corrections to v0.1* (row 7 plus the three in F2) and *draft errors critique caught*. Worth noting the fix makes the story **stronger**, not weaker: "critique caught these before a line was written" beats "our strategy document was wrong six ways".

### F2. MATERIAL — §00 omits three corrections the design phase genuinely made to v0.1

- **(a) Scope enforcement.** v0.1 promised "**gateway-enforced** subgraph scoping" (§05 table) and "per-agent subgraph scoping **enforced at the gateway**" (§13 Operations). Design 01 §11 A1 corrected this to **gateway-injected, provider-enforced** — the gateway never parses MCP bodies — which changes the provider trust model and adds a conformance battery. §04's block states the new model, but §00 doesn't list the correction and both original phrasings **survive uncorrected** (F8).
- **(b) Approval UX.** v0.1 §14 said `requiresApproval` tools "pause as durable Workflow steps (**agent just sees a slow tool**)". Design 22 established a typed, retryable `APPROVAL_PENDING` that callers must handle, with an explicitly documented black-box-agent limitation. v1.0 silently dropped the old phrasing from §14 — correct, but it is a real user-visible correction and it is unlisted.
- **(c) Two-tier budget semantics.** ADR-0020 established that gateway budgets are a **conservative local approximation** (rate ÷ replicas, max-price) with the receipt backstop as the **exact tier** one reconcile later. This is arguably the most user-visible semantic the design phase settled, and it corrects the natural reading of v0.1 §02/§14. It appears **nowhere** in v1.0 except the one-line ADR title in §20 — not in §00, not in §14's block, not in §17.

---

## Q2 — Do the "As designed" blocks match the ADRs and designs?

### F3. MATERIAL — §10 (ModelHub) has no "As designed" block, and its table is materially stale

`architecture.md:248-257`. §10 is in the audit list but carries no block; the section is unchanged v0.1. ADR-0026 settled five things §10 should state, most importantly that **KServe runs in RawDeployment mode as a stated constraint** — "no Knative/Istio — the reason the binding respects rule 5 and ADR-0012". As written, §10 reads as though binding KServe is doctrine-neutral, which is exactly the misreading design 25 §2 was revised to prevent. Also missing: kits as the only serving source, `kit://` resolved by a **chart-shipped `ClusterStorageContainer`**, the operator writing `internal/<model>` **pricing rows** (so agent policy compilation can't fail on an unpriced internal model), the **candidate-only route** for shadow InferenceServices, and gateway-side weighting via **virtual models**.

### F4. MATERIAL — §06 asserts `workflow-actor` clients as designed, but design 06 does not record them

`architecture.md:179` vs `designs/06-identity-glue.md` §3.1, whose `ensureClient` kinds remain `app-login | external-agent | exposed-consumer | cli | agent-actor` — **no `workflow-actor`**. ADR-0025 records the decision and design 21 §5 relies on it, but the design that *owns the interface* never gained it. This is the still-open **21-review R2-a**, which was flagged "must land before ADR-0025 is recorded"; ADR-0025 was recorded anyway. The architecture is asserting a corpus fact that the corpus doesn't carry.

### F5. MATERIAL — "design phase complete / every claim backed" is asserted with three required pre-ADR residuals unlanded

`architecture.md:5,22,438`. Verified open right now:

| Residual | Status | Consequence |
|---|---|---|
| **21 R2-a** — `workflow-actor` in design 06 | **absent** | F4 |
| **27 R2-a** — design 27 §2 still reads "**chaining runs in the tap**" | **absent** | contradicts design 27's own §4.1, design 04 §11 A3, ADR-0026, *and* §00 row 4 of this document |
| **26 R3-a** — cross-cluster ownerRef lifecycle | **absent** (no `ownerRef`/finalizer/orphan text in design 26) | in hard mode, deleting a tenant Agent leaves its host-side routes and policies live — stale capability outliving its CR |

ADR-0026 even names an open review residual as its own tracking mechanism ("minimum-version pin folded into 06-review R2-a"). None of this invalidates the architecture, but "Everything else in v0.1 survived critique" (§00) and "Every claim above is backed by an ADR and a critique-passed design" (§20) currently read as *nothing is outstanding*. **Fix**: a four-line "Open items carried into implementation" note — the three residuals plus research item R1's reproducing test. §19 already does half of this.

### F6. MINOR — three small imprecisions inside otherwise-accurate blocks

- **§17** (`:404`): "workflow-operator + workflow-runtime (+2, **scaling 0→N** with trigger registrations)" — only the *runtime* scales 0→N; the operator is a standing controller (design 21 §2).
- **§15** (`:377`): the facet catalog is presented as closed at nine kinds without noting that **ADR-0026 approves a tenth (`profile`)** as a `pack/v1` contract revision — the single exception the design phase granted, and the one a reader most needs flagged.
- **§07** (`:212` vs `:216`): the bullet still says "the convention version **pinned**", corrected two paragraphs later to a **commit SHA** of an unreleased repo. One section, two answers.

---

## Q3 — Do §20's tables match `docs/decisions/` and `docs/designs/README.md`?

**Essentially yes.** Verified exactly: **26 ADRs** on disk, numbering 0001–0026 with titles matching every row of the §20 table; **27 designs**; **12 research notes**; the designs-by-phase table matches the README's phase column (trivial rounding only — design 19 is "P2–P3" in the README and appears under P2; designs 08/10 are "P1+" and appear under P1). Design 02's "eight amendments (A1–A8)" ✓ verified. "Roughly 150 findings" is a fair count (~165 across 27 first-round reviews plus r2/r3 residuals).

### F7. MINOR — the process note undercounts the blockers: there were three, not two

`architecture.md:470`. The note names the circular revision hash (02-review f1) and non-deterministic receipt IDs (04-review f1). The corpus records a **third**: **design 03-review finding 1** — non-atomic apply leaving *fail-open windows*, where a route could go live before its auth/rate-limit policy was accepted, and which produced ADR-0020's fail-closed apply ordering. It is the most security-relevant of the three and belongs in the sentence that advertises what critique caught.

---

## Q4 — Is anything in the older sections now contradicted and left uncorrected?

### F8. MATERIAL — the corrected scope phrasing survives in two places

`architecture.md:162` — "6-tool surface with **gateway-enforced** subgraph scoping" — and `:354` — "per-agent subgraph scoping **enforced at the gateway**". Both are contradicted by design 01 §11 A1 / ADR-0020, and both are contradicted **by this same document** at §04 (`:134`, "gateway-injected and provider-enforced"). This is F2(a)'s uncorrected half.

### F9. MATERIAL — §04's `KnowledgeGraph` CR example contradicts approved design 12, directly above its own As-designed block

`architecture.md:129-131`. The example shows probes inline on the **KG CR** with `query` and a per-probe `threshold`. Design 12 §10 A1 puts probes in the **ontology document**, requires **`via`** on every probe (the question text is a display label, "never the executable"), and puts the promotion threshold in the ontology as `health.pass_threshold` — which the KG CR may only *tighten*. Same example, `:127`: `ingestion: {workflowRef: policy-ingest}` predates designs 11/14, where ingestion is a **Connector facet** driving **build Jobs** (triggers: `kg push`, Connector schedule, connector events, drift remediation), not a Workflow CR reference. The stale YAML sits five lines above the block that corrects the surrounding prose.

### F10. MINOR — §05 promises two exposure projections no design specifies

`architecture.md:159-160`. **`Workflow → MCP`** ("`tools/call` starts the run; progress streamed — assayd workflows usable from Claude Code/IDEs") and **`Agent → MCP` single tool** have no design behind their *projection semantics*: design 21's triggers are event/cron/http and it explicitly defers MCP exposure to "03/23"; design 03 carries only a generic `expose.protocol: mcp` intent field; design 23 projects HTTP/SSE only (workflow POST, chat SSE, KG read). §20's "every claim above is backed by a critique-passed design" is falsified by these two rows. **Fix**: design the projections, or mark them as roadmap in the table.

### F11. MINOR — §05 still bills workflows at "0 pods"

`architecture.md:149`. True of DBOS-the-library (the point being made against Temporal), but design 21 adds **workflow-operator + workflow-runtime (+2, plus tier)** precisely because event triggers require standing workers. §17 states this correctly; §05 leaves the reader with "durable workflows cost nothing".

### F12. MINOR — §02 says "three assayd operators"

`architecture.md:41`. Four assayd-code controllers now exist: agent, workflow, model, and the enterprise **tenant-operator** (design 26 §2, counted in §17's block). "Three core operators (plus the enterprise tenant-operator)" fixes it.

### F13. MINOR — corpus fix (outside architecture.md): design 01's Graphiti appendix is superseded

`designs/01-knowledgegraph-provider-contract.md` Appendix still reads "version = backend namespace/**`group_id`** per version; `write_batch` → **Graphiti bulk add with entity resolution**". Design 13 r2 §3.1/§3.2 superseded both (key-per-version via `GRAPH.COPY`; structured upsert; **LLM-free adapter**) — these are precisely §00 rows 1 and 2. Since §04 cites the contract "as designed", the appendix needs an **A4 amendment** or the corpus half of the platform's two headline corrections still says the old thing.

---

## Verdict

**REVISE.** The technical content of v1.0 is in good shape: nine of ten As-designed blocks are accurate in detail, §20's record-keeping is exact, and the document's overall claim — that the architecture now reflects a completed, critiqued design phase — is substantially true. What needs fixing is concentrated and mostly editorial:

- **§00's table misattributes six draft-corrections to v0.1** (F1) and omits three real v0.1 corrections (F2) — the section most responsible for the document's honesty.
- **§10 never got its block** (F3) and its doctrine-critical constraint is unstated.
- **Three residuals are open while the doc reads "complete"** (F4, F5) — one of which the architecture asserts as designed.
- **Three stale passages contradict approved designs**, two of them contradicting other paragraphs of this same document (F8, F9).

None of these require design work. F1, F2, F7, F11 and F12 are wording; F3 and F6 are content to add; F8 and F9 are deletions/replacements; F4, F5 and F13 are the corpus catching up with what the ADRs already record.

ARCH v1.0: REVISE — 13 findings

---

## Second-pass audit (2026-08-22)

- **Verdict**: **REVISE** — 9 outstanding (1 material, 8 minor); **12 of 13 original findings fully fixed**, 1 half-fixed
- **Scope**: verification of the fixes, accuracy audit of the new llm-d content, and a check for inconsistencies the fixes introduced. Not a re-derivation.
- **Corpus verified this pass**: design 06 §12 A2, design 27 §2, design 26 §4 (R3-a), design 01 §11 A4, design 25 §11 A1, design 03 §3.4 (generative row), `docs/research/llm-d-2026-08.md`.

### 1 · Disposition of F1–F13

| # | Status | Evidence |
|---|---|---|
| **F1** attribution | **Fixed** | §00 is now two tables with an explicit framing line ("conflating them would flatter the process"). Checked row by row: the four in *Corrections to v0.1 itself* are genuine v0.1 text (scope, "agent just sees a slow tool", budgets, six CRDs); the six in *Draft errors that critique caught* are correctly reattributed, and the added "why it failed review" column is accurate in each case. |
| **F2** omissions | **Fixed** | All three omitted v0.1 corrections — scope enforcement, approval UX, two-tier budgets — are now rows 1–3 of the first table. |
| **F3** §10 block | **Fixed** (see N1) | §10 gained an As-designed block carrying every ADR-0026 element: RawDeployment as a *stated constraint* with the Knative/Istio reasoning and the CI assertion, kits-only serving, chart-shipped `ClusterStorageContainer`, candidate-only shadow route, `internal/<model>` pricing rows, virtual-model weighting, model metric family. |
| **F4** `workflow-actor` | **Fixed in corpus** | Design 06 §12 **A2** landed, with the right rationale ("gateway-enforced promises need gateway-visible identities, and pod-attested SVIDs are never lent"). §06's assertion is now backed. |
| **F5** open items + residuals | **Fixed, both halves** | §20 gained an *Open items carried into implementation* table (R1, re-critique of 01/02, moving pins, nothing execution-validated). All three corpus residuals landed: design 27 §2 now reads "single-writer chainer consumer downstream of the stream — §4.1, design 04 §11 A3 — not the tap"; design 26 §4 gained a **cross-cluster lifecycle** paragraph (label + `source-uid` marking, finalizer blocking removal until host cleanup confirms, orphan sweep reporting `OrphanedHostResources`) — more thorough than the finding asked; design 01 §11 **A4** supersedes the Graphiti appendix on both points. |
| **F6** three imprecisions | **Fixed** | §17 "workflow-operator (a standing controller) + workflow-runtime (which scales 0→N)"; §15 "**Exactly one has been granted**: ADR-0026 approves a tenth, `profile`"; §07 "pinned to a commit SHA … there is no version number to pin". |
| **F7** blocker count | **Fixed** | Process note names all three, describes the fail-open apply accurately, and marks it "the most security-relevant". |
| **F8** scope phrasing ×2 | **HALF-FIXED — outstanding** | §13 Operations fixed (`:368`, "injected by the gateway and enforced by the provider (design 01 A1)"). **§05's exposure table still reads "6-tool surface with gateway-enforced subgraph scoping" (`:173`)** — which now contradicts §00's own correction table, row 1, in the same document. |
| **F9** §04 example | **Fixed** | Probes/threshold moved to the ontology with an explanatory line, `ingestion.connectorRef` + schedule, `build.budget`/`probes.budget` added, endpoint "derived, not declared". Better than the finding asked. (Creates N5.) |
| **F10** MCP projections | **Fixed** | Both rows marked *Roadmap* with honest status — "the intent field exists (design 03) but the projection semantics are not yet designed; HTTP/SSE is the specified path". |
| **F11** DBOS "0 pods" | **Fixed** | §05 row now separates library cost from the `Workflow` CRD's standing workers, pointing at §17. |
| **F12** operator count | **Fixed in substance** (see N6) | §02 now names the enterprise tenant-operator. |
| **F13** design 01 appendix | **Fixed in corpus** | A4 landed, superseding both points and marking the appendix "retained only as the original sketch". |

### 2 · Accuracy of the new llm-d content

Checked §10's table rows and block against `llm-d-2026-08.md` and design 25 §11 A1. **Substantially accurate, and notably restrained** — the research note's headline performance figures (~3× output tok/s, ~2× lower TTFT) are *not* quoted, which is the number a less disciplined document would have led with.

Verified accurate: `LLMInferenceService` promoted at **KServe v0.17** and built on llm-d ✓; the capability list (KV-cache-aware routing, disaggregated prefill/decode, scale-to-zero) ✓; **GAIE `InferencePool`** as the gateway seam and **OSS in agentgateway** ✓ (ADR-0020 D1 holds — the note confirms it is not a Solo Enterprise feature); Sandbox-stage maturity with the "pin the two contracts, never llm-d internals" posture ✓ (the note's own caveat, faithfully carried). The design-03 row design 25 A1 claims **does exist** (`:83`, "Generative model serving | `InferencePool` (GAIE) emitted for LLM pools + HTTPRoute to it").

**Is "binds llm-d transitively" correct?** Yes — it is the research note's own framing ("binding KServe for LLM serving binds llm-d transitively whether the design says so or not"). See N4 for the one scoping imprecision.

#### N1. MATERIAL — §10's "Serving (classic)" row still carries the exact phrasing its own block declares stale

`architecture.md:267` reads "… **Kueue** for GPUs; **llm-d for one giant model**", four lines above a block that says "assayd binds llm-d *transitively* through KServe, **not 'only for one giant model' as v0.1 had it**". A reader cannot tell which sentence is current, and the stale clause is doubly wrong now: llm-d belongs to the *generative* row (which the fix added directly above), not the classic `InferenceService` path, which does not involve it at all. **Fix**: delete the clause from the classic row — the generative row and the block already carry llm-d correctly.

#### N2. MINOR — §17's beyond-core accounting omits the two weights design 25 A1 explicitly ledger-entered

Design 25 A1 states the chart installs KServe's **`llmisvc` component** at plus tier when generative serving is enabled, and adds **+1 endpoint-picker (EPP) per inference pool** as workload state, "the design-13 FalkorDB category, entered in the ledger". §17 itemizes that very category for the KG analogue ("Per managed knowledge graph: adapter + backend (2 workload pods)") but has no line for generative serving, and never mentions `llmisvc` or EPP. **Fix**: one clause in the beyond-core paragraph, mirroring the KG line.

#### N3. MINOR — both "moving pins" enumerations omit the fastest-moving pin in the corpus

§19's risk row and §20's new open-items row both list "agentgateway minimum, semconv SHA, Unsloth BuiltinTrainer status" — neither mentions the **`LLMInferenceService` / `InferencePool` contracts**, which the research note singles out as the one that "*will* move again before implementation" (llm-d went v0.5→v0.6 in two months, and P5 is the last phase). Since the note was landed specifically to correct a stale llm-d claim, it belongs in the list of pins expected to go stale.

#### N4. MINOR — "binds llm-d transitively" is unscoped where design 25 A1 scopes it

§10's block says "assayd binds llm-d transitively through KServe" without qualification; A1 says "binding KServe **for LLM serving** binds llm-d transitively". With the classic row present, the unscoped sentence implies all KServe use pulls in llm-d, which is not true of the predictive path. One prepositional phrase fixes it.

### 3 · New inconsistencies introduced by the fixes

#### N5. MINOR — §04's new "Jobs, not Workflow CRs" line is contradicted twice elsewhere

The F9 fix added "builds run as Jobs (design 14), **not Workflow CRs**" (`:139`). Two pre-existing passages now contradict it in the same document: §09's drift table — "trigger ingestion **Workflow** → alert" (`:252`) — and §12's connector-planes table — "Ingestion | system → KG, bulk | **Workflow steps** with native readers" (`:327`). Both are v0.1 terminology that designs 11/14 superseded; the fix made them visible rather than creating them.

#### N6. MINOR — the F12 fix broke §02's sentence

`architecture.md:51`: "**three core assayd operators** (agent, workflow, model) — plus the enterprise tenant-operator that reconcile CRs into bindings — routes, identity, gates." The inserted clause severs subject from verb, so "that reconcile" now appears to attach to the tenant-operator alone. Substance is right; the sentence needs recasting.

#### N7. MINOR — §19's semconv posture still says "pin version" after §07 was corrected

`architecture.md:439` reads "pre-stable; **pin version**, absorb renames", while the F6(c) fix made §07 explicit that "the conventions moved to an unreleased repo, so there is **no version number to pin**". Same document, two postures — the fix sharpened one and left its sibling.

#### N8. MINOR *(corpus, not architecture.md)* — design 25's CRD body is stale against its own A1

`designs/25-model-operator.md:35` still shows `serve.runtime: vllm | triton | none`, while A1 states "`serve.runtime` **splits by model kind**: generative ⇒ `LLMInferenceService`; classic/predictive ⇒ `InferenceService`". §10's table asserts the split, so the architecture is currently ahead of the design body. Same body-vs-amendment pattern the corpus residuals just closed elsewhere.

### Verdict

The revision is substantially successful: **12 of 13 findings are fully fixed**, several beyond what was asked (design 26's cross-cluster lifecycle paragraph, §04's rewritten example, §00's two-table split with its added "why it failed review" column), all four corpus residuals landed, and the new llm-d content is accurate, sourced, and commendably free of the performance-number overreach the research note invited.

What keeps this at REVISE is a *pattern*, not a hard problem: **three of the nine outstanding items are the document contradicting itself** (N1 in §10, F8's remainder in §05, N5 across §04/§09/§12), and they are the same class as findings F8 and F9 from the first pass — a corrected passage whose siblings elsewhere were not swept. Every outstanding item is a one-line deletion or recast; none requires design work or reopens a decision. The recommendation is therefore a **systematic sweep rather than nine more instance fixes**: grep the document for each superseded phrasing (`gateway-enforced`, `one giant model`, ingestion-as-`Workflow`, "pin version") and confirm each hit against the corpus, so a third pass doesn't find the fourth sibling.

ARCH v1.0 r2: REVISE — 9 outstanding

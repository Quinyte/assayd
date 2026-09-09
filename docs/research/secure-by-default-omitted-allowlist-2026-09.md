# Secure-by-default: what current practice does when a declared allowlist is OMITTED

**Status**: 2026-09-09. Research note for the unresolved design 11 §11 / design 03 §3.1 contradiction. **Re-verify by 2026-12-09** (the standards move slowly; the MCP-gateway practice does not).

**The question, as the repo poses it.** Design 11 §11 says `spec.tool.toolAllowlist` is optional and "omitting it means no tool filter is emitted at all — the agent gets whatever the server offers." Design 03 `03:102` says "**Undeclared is a compile error, not a wider filter**," withholding the route with `PolicyCompileFailed`. Design 11 records the standoff honestly: "one is a silent over-grant, the other a loud outage," and that neither document may settle it unilaterally.

**What this note adds.** External practice does not choose between those two. It rejects the framing: the convention is that a security-relevant allowlist is **not an optional field with a permissive default at all**. The over-grant is universally condemned; the loud outage is treated as a *symptom of the field having been made optional in the first place*, and the fix is applied one layer earlier, at admission. That is a third answer, and it is the one the corpus is missing.

---

## 1. The standards are unambiguous, and they are about the *default*, not the failure

| Source | What it says | Reference |
|---|---|---|
| NIST SP 800-53 r5 **SC-7(5)** | Managed interfaces "deny network communications traffic by default and allow network communications traffic by exception (i.e., deny all, permit by exception)" | [csf.tools SC-7(5)](https://csf.tools/reference/nist-sp-800-53/r5/sc/sc-7/sc-7-5/) |
| NIST SP 800-171 r2 **3.4.8** / r3 **03.04.08** | Authorized-software control is a deny-all, permit-by-exception (allowlist) policy | [csf.tools 03.04.08](https://csf.tools/reference/nist-sp-800-171/r3-0/03-04/03-04-08/) |
| NIST **"fail secure"** (SC-7 family) | On operational failure of a boundary-protection device, the system must not "enter into unsecure states where intended security properties no longer hold" | [SC-7 Boundary Protection](https://csf.tools/reference/nist-sp-800-53/r5/sc/sc-7/) |
| OWASP Developer Guide, Access Control | "Deny by default; if a request is not specifically allowed then it is denied" | [OWASP Developer Guide](https://devguide.owasp.org/en/04-design/02-web-app-checklist/07-access-controls/) |
| OWASP Authorization Cheat Sheet | Adopt a deny-by-default mentality "both during initial development and whenever new functionality or resources are exposed" | [Authorization Cheat Sheet](https://cheatsheetseries.owasp.org/cheatsheets/Authorization_Cheat_Sheet.html) |
| OWASP Top 10 **2025 A01** (Broken Access Control) | Still A01. Prevention is "deny by default, enforce server-side, centralize the rules, least privilege" | [A01:2025](https://owasp.org/Top10/2025/A01_2025-Broken_Access_Control/) |
| OWASP ASVS | Verifies that "access controls fail securely by denying access, **including when an exception occurs**" | [ASVS access-control reorg](https://github.com/OWASP/ASVS/issues/1352) |
| CISA Secure-by-Design (joint intl. guidance, **updated 2026-05-12**) | "A secure configuration should be the **default baseline**, in which products automatically enable the most important security controls" — and secure defaults belong in *product acceptance criteria* | [CISA Secure by Design](https://www.cisa.gov/securebydesign) · [2026 update](https://www.securebydesignhandbook.com/blog/2026/05/12/cisa-secure-by-design-update) |

Note the two distinct claims, because the corpus currently conflates them. ASVS/NIST "fail secure" governs the **runtime failure** case — the policy engine is down, the input cannot be resolved — and design 24's no-fail-open-knob rule already satisfies it. SC-7(5)/OWASP "deny by default" governs the **absent declaration** case, which is what design 11 and design 03 are actually arguing about, and nothing in the corpus discharges it.

**On this axis design 03 is right and design 11 is wrong.** There is no standard anywhere that endorses "omitted allowlist ⇒ grant everything." It is the textbook A01 failure.

## 2. Kubernetes has no single convention — and that is itself the finding

The tempting move is to appeal to "the Kubernetes convention." There isn't one. The platform is openly inconsistent, and each inconsistency is a known footgun:

| API | Omitted / empty means | Direction |
|---|---|---|
| `NetworkPolicy` — **no policy selects the pod** | allow all | permissive |
| `NetworkPolicy` — **policy selects pod, `ingress: []`** | deny all | restrictive |
| `NetworkPolicy` — `ingress: [{}]` (one empty rule) | allow all | permissive |
| Istio `AuthorizationPolicy` with empty `spec: {}` | **deny all** to the selected workload | restrictive |
| Istio: first ALLOW policy on a workload | flips that workload from allow-all to deny-unless-allowed | restrictive-on-adoption |
| Envoy `envoy.filters.http.rbac` — filter absent | allow (nothing enforces) | permissive |
| PodSecurityPolicy `allowedHostPaths` / `allowedFlexVolumes` empty | **all** host paths / volumes permitted | permissive |
| PSP `allowedProcMountTypes` empty | only `Default` permitted | restrictive |
| `kuberc` `credentialPluginPolicy` unset | identical to `AllowAll` | permissive |

Sources: [NetworkPolicy docs](https://kubernetes.io/docs/concepts/services-networking/network-policies/) · [Istio AuthorizationPolicy reference](https://istio.io/latest/docs/reference/config/security/authorization-policy/) · [Istio v1beta1 authz blog](https://istio.io/latest/blog/2019/v1beta1-authorization-policy/) · [kuberc](https://kubernetes.io/docs/reference/kubectl/kuberc/) · [PSP field semantics](https://registry.terraform.io/providers/hashicorp/kubernetes/latest/docs/resources/pod_security_policy).

Two things follow.

**(a) NetworkPolicy is assayd's exact shape, and it is the cautionary tale, not the model.** Its two-layer structure is precisely design 11's: *no policy attached* ⇒ the dataplane permits everything; *a policy attached with an empty list* ⇒ deny. That is the same split the repo already measured at the gateway — with no `AgentgatewayPolicy` attached, agentgateway lists and permits every tool the MCP server offers (`research/agentgateway-v1.5.0-delta-2026-09.md`, design 07 A6.8). NetworkPolicy's permissive outer layer is the single most-cited misconfiguration in Kubernetes network security, and the community answer to it is not "change the dataplane default" — it is **ship a default-deny policy and enforce its presence at admission**. That is an argument for making the declaration mandatory in assayd's control plane, not for making the gateway's absence-behaviour the user-visible contract.

**(b) Istio is the closest security-domain precedent and it goes the other way from design 11.** An `AuthorizationPolicy` with an empty spec denies; adopting any ALLOW policy flips the workload to deny-unless-allowed. A policy object in Istio never widens by omission.

### The API conventions do not rescue the permissive reading either

The Kubernetes API conventions ([sig-architecture api-conventions.md](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md)) say a **required** field "means that the writer must express an opinion about the value," and that an optional field is one "where the writer may choose to omit the field entirely." They also warn that where you must distinguish "field not set" from "field set to an empty value," you need a pointer type. Design 11's field is exactly a case where the writer must express an opinion — an author who has not thought about which tools an agent may call has not finished authoring the Connector — and the conventions' own vocabulary calls that *required*.

## 3. The third answer: make it required at admission, not permissive at compile

Practice in this space is converging on: **explicit grant, refused at write time**. The tool-plane sources are blunt about it.

- Wiz Research (July 2026) found roughly **70% of internet-exposed MCP servers return their full tool catalogue to an anonymous caller** — the over-grant is not hypothetical, it is the modal deployment. ([obot.ai MCP security](https://obot.ai/resources/learning-center/mcp-security/))
- The MCP-gateway guidance is "default to deny," with allowlists covering tool names *and versions* and parameter schemas, and gateways exposing explicit `--tools` allowlist flags. ([obot.ai](https://obot.ai/resources/learning-center/mcp-security/) · [Practical DevSecOps](https://www.practical-devsecops.com/mcp-security-best-practices/))
- Tool poisoning and the "rug pull" — a server that begins advertising a tool it did not advertise at binding time — are the named threat class. Design 03 §3.1's `Loosen` cell (a Connector author adding `wire_funds`, applying in place, with no Agent edit and no gate) is the same attack expressed through assayd's own CRD surface. Its concern is externally corroborated.

**Why "required" is better than either of the repo's two options.** Design 03's `PolicyCompileFailed` is correct in outcome but wrong in *timing*: it converts an authoring mistake into a reconcile-time outage discovered by whoever is on call, and it does so via a condition on the Agent CR rather than at the Connector where the mistake was made. A `required` field with `minItems: 1` rejects the same mistake **in `kubectl apply`, at the author's terminal, naming the field** — same security property, no outage, no cross-CR blame transfer. It also dissolves the standoff: design 11 keeps ownership of the field (it declares it), design 03 keeps its rule (an absent allowlist never compiles to a wider filter), and neither has to concede, because the case they disagree about becomes unreachable.

**The trap: `+required` alone does not close it.** Controller-tools' `+required` on a slice still admits an **empty slice** — "the Go type system allows zero values to pass the `+required` check — empty strings, zero numerics, empty slices, or empty maps are valid values," and OpenAPI validation only checks non-null presence in the payload ([Ahmet Alp Balkan, CRD generation pitfalls](https://ahmet.im/blog/crd-generation-pitfalls/)). So the rule must be `+kubebuilder:validation:Required` **plus `minItems: 1`**, or an empty list quietly re-enters as a third undefined state. That same post recommends explicit `+required`/`+optional` on every field with a package-level `Required` safety net — worth adopting repo-wide, and it pairs with the `maxItems` bound design 03 A51 already owes design 11 on this exact field.

**If "grant everything" must remain expressible, it must be a sentinel, not an absence.** The generic security guidance is that being explicit with lists future-proofs permissions "for if the `*` changes to match additional items not currently present" ([Red Hat, operator security practices](https://www.redhat.com/en/blog/kubernetes-operators-good-security-practices)). An explicit `toolAllowlist: ["*"]` — or better, a separate boolean the CRD description marks as unsafe — is auditable, greppable, and reviewable. An omission is none of those. This is the only shape in which design 11's user-visible outcome survives, and it survives only because the author typed it.

## 4. The discoverability problem is the reason this cannot be left to the gateway

Design 11's defence — that the gateway permits everything with no policy attached, so the permissive reading is "correct about the gateway" — runs straight into a named Gateway API defect. **GEP-713** records the *discoverability problem* as one of the two meaningful challenges of policy attachment: there is no clear connection between a resource and the policies affecting it, so a user reading an `HTTPRoute` cannot tell what governs it, and the absence of a policy is indistinguishable from a policy that has not been noticed. GEP-2648 requires Direct Policy CRDs to carry `gateway.networking.k8s.io/policy: direct` purely so tooling can find them, and GEP-2722 exists to build `gwctl` to work around it. ([GEP-713](https://gateway-api.sigs.k8s.io/geps/gep-713/) · [GEP-2648](https://gateway-api.sigs.k8s.io/geps/gep-2648/) · [GEP-2722](https://gateway-api.sigs.k8s.io/geps/gep-2722/))

A governance product whose central claim is that governance becomes real at the gateway cannot inherit "absent policy is invisible and permissive" as its user-visible contract. The gateway's behaviour is a *dataplane fact*; assayd's contract is a *control-plane promise*, and the promise has to be the stricter of the two or the product's thesis is untrue in its default configuration.

## 5. What this note recommends

1. **Neither design's stated outcome ships.** The field becomes **required with `minItems: 1`** on the Connector CRD (design 11 owns the declaration), enforced at admission.
2. **Design 03's rule stands and becomes unreachable in practice**: an absent `toolAllowlist` still never compiles to a wider filter. `PolicyCompileFailed` remains the backstop for any object that predates the constraint.

   **The migration cost is near zero, but only until first publish.** Ordinarily, adding a required field to a live API version is the breaking change you cannot make — the guidance is to cut a new version and carry a conversion webhook, which becomes an indefinite dependency once mixed versions sit in etcd ([CRD versioning](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/) · [Celonis, handling breaking CRD changes](https://careers.celonis.com/blog/updating-crds-through-breaking-changes)). Two facts remove that cost here: the Connector CRD is `v1alpha1`, where breaking changes are the declared contract, and the repository is **private and unpublished** — there is no installed base to migrate. **CRD validation ratcheting** softens it further even after publish: added OpenAPI validations do not re-reject stored objects that are not being changed, so existing Connectors keep serving and only an *edit* forces the author to declare. This is therefore a decision that is free today and expensive the day after the project goes public — which is the same week the licensing decisions in this quarter land.
3. **`maxItems` lands in the same change**, discharging design 03 A51's debt to design 11.
4. **If an explicit "everything" is wanted, it is a sentinel value carrying its own condition and its own audit line** — never an omission.
5. **This is an ADR, not a design amendment.** Design 11 §11 already says neither document may settle it unilaterally, and the resolution changes a CRD's required-field set — an API compatibility decision. It should supersede nothing; it should *decide* what ADR-0027 (CRD ergonomics) left open, and design 03's re-opened consolidation should be written against it rather than the reverse.

**What this note does NOT settle.** It is desk research against standards and precedent. It has not been measured: nobody has confirmed that a `minItems: 1` + `Required` marker pair actually rejects `toolAllowlist: []` on this repo's generated CRD against a real API server, and per AGENTS.md's test-layer rule that is an envtest assertion against the *generated* artifact, not a fixture. Until that runs, item 1 is a proposal with a citation, not a property.

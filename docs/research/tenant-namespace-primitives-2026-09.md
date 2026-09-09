# Namespace-boundary primitives — facts for the design 07 / 26 amendments (2026-09)

Verified 2026-09-03 against primary sources (upstream Go types, upstream source, upstream docs). **Re-verify by 2026-12-01**, or sooner if spire-controller-manager, Gateway API or Kubernetes cuts a minor.

Versions current at verification: spire-controller-manager **v0.7.0** (2026-07-27), go-spiffe **v2.8.1** (2026-06-19), Gateway API **v1.6.1** (2026-07-16; v1.5.0 was 2026-02-27), Kubernetes **v1.37.0** (2026-08-26), Go **1.27.1** docs.

## 1. spire-controller-manager `ClusterSPIFFEID`

Source of truth: `api/v1alpha1/clusterspiffeid_types.go` at v0.7.0 — https://github.com/spiffe/spire-controller-manager/blob/v0.7.0/api/v1alpha1/clusterspiffeid_types.go · docs: https://github.com/spiffe/spire-controller-manager/blob/main/docs/clusterspiffeid-crd.md

### Spec fields (exact JSON names, from the Go type)

| Field | Type | Required | Meaning (doc) |
|---|---|---|---|
| `spiffeIDTemplate` | string | **yes** | template rendering the workload's SPIFFE ID |
| `podSelector` | `*metav1.LabelSelector` | no | label selector scoping which **pods** this CR targets |
| `namespaceSelector` | `*metav1.LabelSelector` | no | label selector scoping which **namespaces** this CR targets |
| `workloadSelectorTemplates` | `[]string` | no | templates rendering **additional** SPIRE workload selectors (a `k8s:pod-uid:<uid>` selector is always added) |
| `dnsNameTemplates` | `[]string` | no | templates rendering DNS SANs |
| `autoPopulateDNSNames` | bool | no | also add DNS names derived from Services/Endpoints that select the pod |
| `ttl` / `jwtTtl` | `metav1.Duration` | no | upper bounds on X509-SVID / JWT-SVID TTL |
| `federatesWith` | `[]string` | no | trust domains the workload federates with |
| `admin` / `downstream` | bool | no | SPIRE entry flags |
| `hint` | string | no | entry hint (added v0.6.0) |
| `fallback` | bool | no | apply **only** if no non-fallback ClusterSPIFFEID matched the pod (added v0.6.0; fallback CRs are sorted last, and a pod already matched by a non-fallback CR is skipped) |
| `className` | string | no | which controller-manager instance owns this CR |

Status carries `stats.{namespacesSelected, namespacesIgnored, podsSelected, podEntryRenderFailures, entriesMasked, entriesToSet, entryFailures}`.

### Template engine and context

Templates are Go `text/template`, parsed with `template.New(name).Parse(value)` and **no `.Option(...)` and no `.Funcs(...)`** (`api/v1alpha1/clusterspiffeid_webhook.go` `ParseClusterSPIFFEIDSpec`, lines 106/138/147 at v0.7.0). Parse errors are rejected by the validating webhook. Context (`pkg/spireentry/entries.go` `templateData`): `.TrustDomain`, `.ClusterName`, `.ClusterDomain`, `.PodMeta` (`*metav1.ObjectMeta`), `.PodSpec`, `.NodeMeta`, `.NodeSpec`.

### Reading a Pod label — correct syntax

```
spiffe://{{ .TrustDomain }}/agent/{{ index .PodMeta.Labels "assayd.dev/agent-namespace" }}/{{ index .PodMeta.Labels "assayd.dev/agent" }}
```

`index` is required: a label key containing `/` or `.` cannot be written as a field access, and `{{ .PodMeta.Labels "key" }}` (a map followed by an argument) is **not** a call form text/template accepts. The upstream docs show only `.PodMeta.Namespace` / `.PodSpec.ServiceAccountName` / `.PodMeta.Name`; there is no upstream example with `index`, so the form above rests on the Go `text/template` contract: *"index — Returns the result of indexing its first argument by the following arguments. Thus "index x 1 2 3" is, in Go syntax, x[1][2][3]. Each indexed item must be a map, slice, or array."* — https://pkg.go.dev/text/template

### What happens when the label is absent

1. `index` on a map with a missing key returns the **zero value** (`""`), not an error — `text/template/funcs.go` `index()`: `if x := item.MapIndex(index); x.IsValid() { item = x } else { item = reflect.Zero(item.Type().Elem()) }`. A nil `Labels` map behaves the same. The `missingkey=error` option would not help even if set: it governs `.Key` access, not `index`, and spire-controller-manager sets no options anyway.
2. The rendered string is parsed by `spiffeid.FromString` (go-spiffe v2.8.1 `spiffeid/id.go` → `ValidatePath`). With the label as a **whole segment**, `spiffe://td/agent//reviewer` fails with `path cannot contain empty segments`; as the **last** segment, `spiffe://td/agent/team-a/` fails with `path cannot have a trailing slash` (`spiffeid/errors.go`). The SPIFFE ID spec forbids both (SPIFFE-ID.md §2.2: "MUST NOT include segments that are empty or are relative path modifiers", "MUST NOT include a trailing /").
3. `renderSPIFFEID` wraps that as `invalid SPIFFE ID: …`, `renderPodEntry` as `failed to render SPIFFE ID: …`, and the reconciler (`pkg/spireentry/reconciler.go` ~L517) does `log.Error(err, "Failed to render entry")`, increments `status.stats.podEntryRenderFailures`, and continues with the next pod. **No entry is created for that pod; it gets no SVID.** Nothing is written to the Pod, and no Kubernetes Event is emitted — the only signals are the controller log line and the counter.

**Caveat that matters for the amendment:** the fail-closed outcome is a property of the *template shape*, not of the controller. `spiffe://td/agent-{{ index .PodMeta.Labels "k" }}/x` renders `agent-` — a valid segment — and silently mints an identity. Keep every label-derived value as a whole path segment. Better still, add `podSelector.matchExpressions: [{key: assayd.dev/agent-namespace, operator: Exists}, {key: assayd.dev/agent, operator: Exists}]` so an unlabeled pod is not selected at all (no render-failure noise, same outcome: no SVID), and alert on `podEntryRenderFailures > 0` as the genuine-bug signal.

## 2. Gateway API v1 — listener `allowedRoutes.namespaces` and cross-namespace `parentRefs`

Source of truth: `apis/v1/gateway_types.go` and `apis/v1/shared_types.go` at v1.6.1 — https://github.com/kubernetes-sigs/gateway-api/blob/v1.6.1/apis/v1/gateway_types.go · https://github.com/kubernetes-sigs/gateway-api/blob/v1.6.1/apis/v1/shared_types.go · guide: https://gateway-api.sigs.k8s.io/guides/multiple-ns/ · ReferenceGrant: https://gateway-api.sigs.k8s.io/reference/api-types/referencegrant/

### Exact shape

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
spec:
  listeners:
  - name: agents
    port: 443
    protocol: HTTPS
    allowedRoutes:
      namespaces:
        from: Selector            # enum on RouteNamespaces.from: All | Selector | Same ; default Same
        selector:                 # metav1.LabelSelector; REQUIRED when from: Selector, ignored otherwise
          matchLabels:
            assayd.dev/run-namespace: "true"
      # kinds: [...]              # optional, max 8
```

`RouteNamespaces.from` is `+kubebuilder:validation:Enum=All;Selector;Same`, default `Same`; `selector` is `*metav1.LabelSelector` ("must be specified when From is set to Selector … ignored for other values"). Both are **Support: Core**. `matchExpressions` works too (the guide uses `kubernetes.io/metadata.name` with `In`). Note: the `FromNamespaces` const block at v1.6.1 also defines `None`, but it is **not** in the `RouteNamespaces.from` enum — it exists for `ListenerNamespaces` (ListenerSet); do not write `from: None` on a Gateway listener.

### ReferenceGrant is not needed for a Route's parentRef

Confirmed, and now v1 (`gateway.networking.k8s.io/v1`; moved to v1 in v1.5.0 — 1.5-CHANGELOG: "the ReferenceGrant resource is moving to v1"):

- `ParentReference.Namespace` doc (v1.6.1): *"Cross-namespace references are only valid if they are explicitly allowed by something in the namespace they are referring to. For example: Gateway has the AllowedRoutes field, and ReferenceGrant provides a generic way to enable any other kind of cross-namespace reference."*
- `ReferenceGrant` type doc (v1.6.1): *"All cross-namespace references in Gateway API (with the exception of cross-namespace Gateway-route attachment) require a ReferenceGrant."*
- The multiple-ns guide attaches an HTTPRoute via `parentRefs: [{name: shared-gateway, namespace: infra-ns}]` against a listener with `from: Selector` and never mentions ReferenceGrant.

So: **Route → Gateway** attachment across namespaces = `allowedRoutes` on the listener (Gateway owner's consent) + `parentRefs[].namespace` on the Route. **Route → backend Service** in another namespace, and **Gateway → TLS Secret** in another namespace = ReferenceGrant in the *target* namespace. This matches design 03 A44 / design 02 A45 exactly (routes and Services co-located in the run namespace precisely so no ReferenceGrant is needed; the listener admits run namespaces by label).

## 3. Pod Security Admission namespace labels

Source of truth: https://kubernetes.io/docs/concepts/security/pod-security-admission/ · https://kubernetes.io/docs/tasks/configure-pod-container/enforce-standards-namespace-labels/ · code: `staging/src/k8s.io/pod-security-admission/admission/admission.go` (`ValidateNamespace`, `EvaluatePodsInNamespace`). PSA is GA since v1.25.

### The full label set (six labels)

```
pod-security.kubernetes.io/enforce:         privileged | baseline | restricted
pod-security.kubernetes.io/enforce-version: v<MAJOR>.<MINOR> | latest      # e.g. v1.37
pod-security.kubernetes.io/audit:           privileged | baseline | restricted
pod-security.kubernetes.io/audit-version:   v<MAJOR>.<MINOR> | latest
pod-security.kubernetes.io/warn:            privileged | baseline | restricted
pod-security.kubernetes.io/warn-version:    v<MAJOR>.<MINOR> | latest
```

- `enforce`: violations reject the pod. `audit`: violations add an audit annotation to the audit-log event, pod allowed. `warn`: violations return a user-facing warning, pod allowed.
- `*-version` pins the policy to the version that shipped with that Kubernetes minor; omitted or `latest` = current. `enforce` applies only to Pod objects; `audit`/`warn` are also evaluated against workload templates (Deployments, Jobs…) so violations surface early.

### Does a label change re-evaluate running pods?

**It does not enforce against them — it only warns.** Docs (enforce-standards-namespace-labels): *"When an `enforce` policy (or version) label is added or changed, the admission plugin will test each pod in the namespace against the new policy. Violations are returned to the user as warnings."* Code: on a Namespace UPDATE, `ValidateNamespace` returns `allowedResponse()` with `Warnings = EvaluatePodsInNamespace(...)`; it early-exits (no evaluation at all) when the enforce policy is unchanged, is `privileged`, or is a relaxation, so a change to only `audit`/`warn` labels triggers nothing. The check is capped (`defaultNamespaceMaxPodsToCheck = 3000`, `defaultNamespacePodCheckTimeout = 1s`) and warns `existing pods in namespace %q violate the new PodSecurity enforce level %q`. Running pods are never evicted or mutated; the new level bites on the next pod **create** (a rollout). `kubectl label --dry-run=server --overwrite ns <ns> pod-security.kubernetes.io/enforce=restricted` previews the warnings without applying.

## 4. Creating objects in a `Terminating` namespace

Source of truth: `staging/src/k8s.io/apiserver/pkg/admission/plugin/namespace/lifecycle/admission.go` (plugin `NamespaceLifecycle`, always on) — https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/admission/plugin/namespace/lifecycle/admission.go · constant: `k8s.io/api/core/v1/types.go` L7463-7467.

- Applies to **`Create`** only, on namespaced resources, when `namespace.Status.Phase == v1.NamespaceTerminating`. Updates and deletes in a terminating namespace are **allowed** (that is how finalizers drain); non-namespaced resources and the Namespace object itself are exempt (except deleting an immortal namespace, which is refused).
- Error: `admission.NewForbidden(a, fmt.Errorf("unable to create new content in namespace %s because it is being terminated", ns))` → an `apierrors.StatusError` with **HTTP 403**, `reason: Forbidden`, message `<resource> "<name>" is forbidden: unable to create new content in namespace <ns> because it is being terminated`, and an appended cause:
  ```go
  metav1.StatusCause{Type: v1.NamespaceTerminatingCause, Message: "namespace <ns> is being terminated", Field: "metadata.namespace"}
  ```
- The constant: `NamespaceTerminatingCause metav1.CauseType = "NamespaceTerminating"` — *"returned as a defaults.cause item when a change is forbidden due to the namespace being terminated."* Phase constants: `NamespaceActive = "Active"`, `NamespaceTerminating = "Terminating"`.
- Detect it in a controller with `apierrors.HasStatusCause(err, corev1.NamespaceTerminatingCause)` (supports wrapped errors; `k8s.io/apimachinery/pkg/api/errors`). Plain `IsForbidden` is not specific enough — RBAC denials are also 403.

Consequence for design 26's delete path and design 02 A42's run namespaces: once a tenant/run namespace is `Terminating`, every SSA **create** the operator or compiler attempts there fails 403 with this cause; the reconciler must treat that cause as "stop creating, let finalizers drain", not as an error to retry.

## Contradictions with current designs (say so, per the research discipline)

1. **Design 02 §3.5 line ~320 (A59) has the wrong template syntax**: it writes `{{ .PodMeta.Labels "assayd.dev/agent-namespace" }}`. That does not parse as a map lookup in `text/template` and would be rejected by the ClusterSPIFFEID webhook (or, at best, fail at render). It must be `{{ index .PodMeta.Labels "assayd.dev/agent-namespace" }}`. Design 07 (which ships the ClusterSPIFFEID per A59's "Owed") and design 26 (per-tenant SPIRE) must carry the `index` form — and design 02 §3.5 should be corrected in the same pass so the three never disagree.
2. **A59's "label absent ⇒ fail closed" is template-shape dependent** (see §1 caveat). The amendment should state the invariant ("every label-derived value is a whole path segment") and add the `Exists` `podSelector`, rather than rely on the empty-segment rejection implicitly.
3. No contradiction found for items 2–4: design 03 A44 / design 02 A45 already state the ReferenceGrant/allowedRoutes split correctly; PSA and NamespaceTerminating are not yet described in designs 07/26, so the facts above are additive.

## Sources

- spire-controller-manager types (v0.7.0): https://github.com/spiffe/spire-controller-manager/blob/v0.7.0/api/v1alpha1/clusterspiffeid_types.go
- spire-controller-manager template parse (v0.7.0): https://github.com/spiffe/spire-controller-manager/blob/v0.7.0/api/v1alpha1/clusterspiffeid_webhook.go
- spire-controller-manager render + failure handling (v0.7.0): https://github.com/spiffe/spire-controller-manager/blob/v0.7.0/pkg/spireentry/entries.go · https://github.com/spiffe/spire-controller-manager/blob/v0.7.0/pkg/spireentry/reconciler.go
- spire-controller-manager CRD doc: https://github.com/spiffe/spire-controller-manager/blob/main/docs/clusterspiffeid-crd.md · releases: https://github.com/spiffe/spire-controller-manager/releases
- go-spiffe path validation (v2.8.1; `spiffeid/` is now at repo root): https://github.com/spiffe/go-spiffe/blob/main/spiffeid/path.go · https://github.com/spiffe/go-spiffe/blob/main/spiffeid/errors.go
- SPIFFE-ID standard §2.2: https://github.com/spiffe/spiffe/blob/main/standards/SPIFFE-ID.md
- Go text/template `index` + `missingkey`: https://pkg.go.dev/text/template · https://github.com/golang/go/blob/master/src/text/template/funcs.go
- Gateway API v1.6.1 types: https://github.com/kubernetes-sigs/gateway-api/blob/v1.6.1/apis/v1/gateway_types.go · https://github.com/kubernetes-sigs/gateway-api/blob/v1.6.1/apis/v1/shared_types.go · https://github.com/kubernetes-sigs/gateway-api/blob/v1.6.1/apis/v1/referencegrant_types.go
- Gateway API docs: https://gateway-api.sigs.k8s.io/guides/multiple-ns/ · https://gateway-api.sigs.k8s.io/reference/api-types/referencegrant/ · https://gateway-api.sigs.k8s.io/reference/api-spec/main/spec/ · 1.5 changelog: https://github.com/kubernetes-sigs/gateway-api/blob/main/CHANGELOG/1.5-CHANGELOG.md · releases: https://github.com/kubernetes-sigs/gateway-api/releases
- Kubernetes PSA: https://kubernetes.io/docs/concepts/security/pod-security-admission/ · https://kubernetes.io/docs/tasks/configure-pod-container/enforce-standards-namespace-labels/ · https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/pod-security-admission/admission/admission.go
- Kubernetes NamespaceLifecycle: https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apiserver/pkg/admission/plugin/namespace/lifecycle/admission.go · https://github.com/kubernetes/api/blob/master/core/v1/types.go · https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apimachinery/pkg/api/errors/errors.go

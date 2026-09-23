{{/*
Guard rails, evaluated at render time rather than discovered at runtime.
*/}}

{{- define "assayd.validate" -}}
{{- if not (has .Values.tier (list "core" "plus")) -}}
{{- fail (printf "tier must be core or plus, got %q" .Values.tier) -}}
{{- end -}}
{{- if eq .Values.tier "plus" -}}
{{- fail "tier: plus is not implemented yet — it adds Argo Workflows, Phoenix, OpenFGA and eval runner images, none of which are built. Rendering it as if it worked would be worse than refusing." -}}
{{- end -}}
{{- if not (has .Values.profile (list "prod" "local")) -}}
{{- fail (printf "profile must be prod or local, got %q" .Values.profile) -}}
{{- end -}}
{{- if not .Values.operator.image.tag -}}
{{- fail "operator.image.tag is required and must not float: a rollout that cannot be reproduced is not a rollout" -}}
{{- end -}}
{{- if eq .Values.operator.image.tag "latest" -}}
{{- fail "operator.image.tag: latest is refused. Pin a version, or a digest where it matters." -}}
{{- end -}}
{{- /*
Every emitted route's hostname ends in gateway.hostnameSuffix, and the HTTPRoute
CRD refuses a hostname that is not a lowercase DNS name. With gateway.enabled the
operator refuses such a suffix at startup; refused here, the install fails with
the value named instead of crash-looping. assayd-gateway-routes compares hostnames
against the same string. `--set` types a bare number or boolean, so the value is
read as its string, as operator.yaml renders it.

The suggestion is the value trimmed and lower-cased when that is valid, and the
operator's default otherwise, so it never repeats a value that would be refused.
*/ -}}
{{- $dns := "^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$" -}}
{{- $suffix := (.Values.gateway | default dict).hostnameSuffix | default "" | toString -}}
{{- if and $suffix (or (gt (len $suffix) 253) (not (regexMatch $dns $suffix))) -}}
{{- $hint := trim $suffix | trimAll ".-" | lower -}}
{{- if or (gt (len $hint) 253) (not (regexMatch $dns $hint)) -}}
{{- $hint = "assayd.internal" -}}
{{- end -}}
{{- fail (printf "gateway.hostnameSuffix %q is not a lowercase DNS name. Every emitted route's hostname ends in it, and the HTTPRoute CRD refuses one that is not lowercase; with gateway.enabled, the operator also refuses it at startup. Use lowercase letters, digits, '-' and '.', such as %q" $suffix $hint) -}}
{{- end -}}
{{- end -}}

{{- define "assayd.name" -}}assayd{{- end -}}
{{- define "assayd.operator.name" -}}assayd-agent-operator{{- end -}}

{{- define "assayd.labels" -}}
app.kubernetes.io/name: {{ include "assayd.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: assayd
assayd.dev/tier: {{ .Values.tier }}
assayd.dev/profile: {{ .Values.profile }}
{{- end -}}

{{- define "assayd.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "assayd.name" . }}
app.kubernetes.io/component: agent-operator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
The operator image reference.

A digest wins when set, and the tag is dropped rather than carried alongside it:
`repo:tag@digest` is legal, but the digest decides and the tag then reads as
though it mattered. assayd's own admission requires agent images to be
digest-pinned (CEL on spec.runtime.image), and the release workflow pins this
image to the digest it published and signed, so the platform meets that bar for
itself. It does NOT require them to be cosign-signed — this comment said it did,
which is the same false claim the CRD comment on spec.runtime.image was
corrected for. No AGENT image's signature is verified anywhere in this
repository, at admission or after it: CEL cannot, and the chart ships no policy
that does. That arrives with the Sigstore policy-controller binding design 07 A2
chose. assayd's OWN released artifacts are a different matter and are signed and
verified — the release workflow runs cosign verify against what it publishes,
which docs/supply-chain.md documents and bounds.
*/}}
{{- define "assayd.operator.image" -}}
{{- $img := .Values.operator.image -}}
{{- if $img.digest -}}
{{ $img.repository }}@{{ $img.digest }}
{{- else -}}
{{ $img.repository }}:{{ $img.tag }}
{{- end -}}
{{- end -}}

{{/*
Guard rails, evaluated at render time rather than discovered at runtime.
*/}}

{{- define "plume.validate" -}}
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
{{- end -}}

{{- define "plume.name" -}}plume{{- end -}}
{{- define "plume.operator.name" -}}plume-agent-operator{{- end -}}

{{- define "plume.labels" -}}
app.kubernetes.io/name: {{ include "plume.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: plume
plume.dev/tier: {{ .Values.tier }}
plume.dev/profile: {{ .Values.profile }}
{{- end -}}

{{- define "plume.operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "plume.name" . }}
app.kubernetes.io/component: agent-operator
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

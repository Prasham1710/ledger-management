{{/*
Expand the name of the chart.
*/}}
{{- define "ledger.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Full image reference for a service.
Usage: {{ include "ledger.image" (dict "name" "ledger-svc" "global" .Values.global) }}
*/}}
{{- define "ledger.image" -}}
{{- printf "%s/%s:%s" .global.imageRegistry .name .global.imageTag }}
{{- end }}

{{/*
Standard labels applied to every resource.
*/}}
{{- define "ledger.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/instance:   {{ .Release.Name }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end }}

{{/*
Selector labels for a named service.
*/}}
{{- define "ledger.selectorLabels" -}}
app.kubernetes.io/name: {{ .name }}
app.kubernetes.io/part-of: ledger
{{- end }}

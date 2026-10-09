{{- define "gamelog-api.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "gamelog-api.fullname" -}}
{{- if .Values.fullnameOverride }}{{ .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}{{ $name := default .Chart.Name .Values.nameOverride }}{{ if contains $name .Release.Name }}{{ .Release.Name | trunc 63 | trimSuffix "-" }}{{ else }}{{ printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}{{ end }}{{ end }}
{{- end }}

{{- define "gamelog-api.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "gamelog-api.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: go-log-api
{{- end }}

{{- define "gamelog-api.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gamelog-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "gamelog-api.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}{{ default (include "gamelog-api.fullname" .) .Values.serviceAccount.name }}
{{- else }}{{ default "default" .Values.serviceAccount.name }}{{ end }}
{{- end }}

{{- define "gamelog-api.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}

{{- define "gamelog-api.secretName" -}}
{{- if .Values.database.existingSecret }}{{ .Values.database.existingSecret }}
{{- else }}{{ include "gamelog-api.fullname" . }}{{ end }}
{{- end }}

{{- define "gamelog-api.secretKey" -}}
{{- if .Values.database.existingSecret }}{{ .Values.database.existingSecretKey }}{{ else }}database-url{{ end }}
{{- end }}

{{/* Fail early and loudly: the application's own default DSN points at localhost, which turns a
     forgotten value into a crash loop instead of a clear error. */}}
{{- define "gamelog-api.validateDatabase" -}}
{{- if and (not .Values.database.url) (not .Values.database.existingSecret) }}
{{- fail "database.url or database.existingSecret is required (the application would otherwise try localhost and crash loop)" }}
{{- end }}
{{- end }}

{{/*
Expand the name of the chart.
*/}}
{{- define "kubeJanitor.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kubeJanitor.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "kubeJanitor.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kubeJanitor.labels" -}}
helm.sh/chart: {{ include "kubeJanitor.chart" . }}
{{ include "kubeJanitor.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kubeJanitor.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kubeJanitor.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
kube-janitor arguments
*/}}
{{- define "kubeJanitor.args" -}}
{{- if .Values.kubejanitor.dryRun }}
- "--dry-run"
{{- end }}
{{- if .Values.kubejanitor.debug }}
- "--debug"
{{- end }}
{{- if eq .Values.kind "CronJob" }}
- "--once"
{{- end }}
{{- if eq .Values.kind "Deployment" }}
{{- with .Values.kubejanitor.interval }}
- "--interval"
- "{{ . }}"
{{- end }}
{{- end }}
{{- if .Values.kubejanitor.rules }}
- --rules-file=/config/rules.yaml
{{- end }}
{{- with .Values.kubejanitor.includeResources }}
- "--include-resources"
- "{{ join "," . }}"
{{- end }}
- "--exclude-resources"
{{- if .Values.kubejanitor.excludeResources }}
- "{{ join "," .Values.kubejanitor.excludeResources }}"
{{- else }}
- '""'
{{- end }}
{{- with .Values.kubejanitor.includeNamespaces }}
- "--include-namespaces"
- "{{ join "," . }}"
{{- end }}
- "--exclude-namespaces"
{{- if .Values.kubejanitor.excludeNamespaces }}
- "{{ join "," .Values.kubejanitor.excludeNamespaces }}"
{{- else }}
- '""'
{{- end }}
{{- with .Values.kubejanitor.additionalArgs }}
{{ toYaml . }}
{{- end }}
{{- end -}}

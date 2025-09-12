{{/*
Expand the name of the chart.
*/}}
{{- define "mongobouncer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "mongobouncer.fullname" -}}
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
{{- define "mongobouncer.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "mongobouncer.labels" -}}
helm.sh/chart: {{ include "mongobouncer.chart" . }}
{{ include "mongobouncer.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "mongobouncer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mongobouncer.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "mongobouncer.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "mongobouncer.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Return the proper image name
*/}}
{{- define "mongobouncer.image" -}}
{{- $registryName := .Values.image.registry -}}
{{- $repositoryName := .Values.image.repository -}}
{{- $tag := .Values.image.tag | toString -}}
{{- if .Values.global.imageRegistry }}
    {{- $registryName = .Values.global.imageRegistry -}}
{{- end -}}
{{- if $registryName }}
{{- printf "%s/%s:%s" $registryName $repositoryName $tag -}}
{{- else -}}
{{- printf "%s:%s" $repositoryName $tag -}}
{{- end -}}
{{- end }}

{{/*
Return the proper Docker Image Registry Secret Names
*/}}
{{- define "mongobouncer.imagePullSecrets" -}}
{{- if or .Values.image.pullSecrets .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- range .Values.global.imagePullSecrets }}
  - name: {{ . }}
{{- end }}
{{- range .Values.image.pullSecrets }}
  - name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create a default fully qualified config name.
*/}}
{{- define "mongobouncer.configName" -}}
{{ include "mongobouncer.fullname" . }}-config
{{- end }}

{{/*
Create a default fully qualified secret name.
*/}}
{{- define "mongobouncer.secretName" -}}
{{ include "mongobouncer.fullname" . }}-secret
{{- end }}

{{/*
Validate configuration
*/}}
{{- define "mongobouncer.validateValues" -}}
{{- if and (not .Values.app.databases) (not .Values.app.users) }}
{{- fail "At least one database or user configuration must be provided" }}
{{- end }}
{{- end }}

{{/*
Create environment variables for configuration
*/}}
{{- define "mongobouncer.envVars" -}}
{{- range .Values.extraEnvVars }}
- name: {{ .name }}
  value: {{ .value | quote }}
{{- end }}
{{- if .Values.extraEnvVarsConfigMap }}
- name: EXTRA_CONFIGMAP
  valueFrom:
    configMapKeyRef:
      name: {{ .Values.extraEnvVarsConfigMap }}
      key: config
{{- end }}
{{- if .Values.extraEnvVarsSecret }}
- name: EXTRA_SECRET
  valueFrom:
    secretKeyRef:
      name: {{ .Values.extraEnvVarsSecret }}
      key: secret
{{- end }}
{{- end }}

{{/*
Generate container ports
*/}}
{{- define "mongobouncer.containerPorts" -}}
- name: mongobouncer
  containerPort: {{ .Values.app.config.listenPort }}
  protocol: TCP
{{- if .Values.monitoring.prometheus.enabled }}
- name: metrics
  containerPort: {{ .Values.monitoring.prometheus.port }}
  protocol: TCP
{{- end }}
{{- if .Values.monitoring.healthCheck.enabled }}
- name: health
  containerPort: {{ .Values.monitoring.healthCheck.port }}
  protocol: TCP
{{- end }}
{{- end }}

{{/*
Generate service ports
*/}}
{{- define "mongobouncer.servicePorts" -}}
- name: mongobouncer
  port: {{ .Values.service.port }}
  targetPort: mongobouncer
  protocol: TCP
{{- if .Values.monitoring.prometheus.enabled }}
- name: metrics
  port: {{ .Values.monitoring.prometheus.port }}
  targetPort: metrics
  protocol: TCP
{{- end }}
{{- if .Values.monitoring.healthCheck.enabled }}
- name: health
  port: {{ .Values.monitoring.healthCheck.port }}
  targetPort: health
  protocol: TCP
{{- end }}
{{- end }}

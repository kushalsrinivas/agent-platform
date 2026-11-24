{{- define "agent-platform.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "agent-platform.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "agent-platform.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "agent-platform.labels" -}}
app.kubernetes.io/name: {{ include "agent-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "agent-platform.selectorLabels" -}}
app.kubernetes.io/name: {{ include "agent-platform.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "agent-platform.apiEnv" -}}
- name: API_DATABASE_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.apiDatabaseURL }}}}
- name: API_ORCHESTRATOR_TOKEN
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.internalAPIToken }}}}
- name: SECRETS_MASTER_KEY
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsMasterKey }}}}
- name: SECRETS_ACTIVE_KEY_ID
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsActiveKeyID }}}}
- name: SECRETS_PREVIOUS_KEYS
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsPreviousKeys }}}}
{{- end -}}

{{- define "agent-platform.orchestratorEnv" -}}
- name: ORCHESTRATOR_DATABASE_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.orchestratorDatabaseURL }}}}
- name: ORCHESTRATOR_NATS_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.natsURL }}}}
- name: ORCHESTRATOR_WORKER_TOKEN
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.workerControlToken }}}}
- name: INTERNAL_API_TOKEN
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.internalAPIToken }}}}
- name: SECRETS_MASTER_KEY
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsMasterKey }}}}
- name: SECRETS_ACTIVE_KEY_ID
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsActiveKeyID }}}}
- name: SECRETS_PREVIOUS_KEYS
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.secretsPreviousKeys }}}}
{{- end -}}

{{- define "agent-platform.workerEnv" -}}
- name: WORKER_NATS_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.natsURL }}}}
- name: WORKER_CONTROL_TOKEN
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.workerControlToken }}}}
{{- end -}}

{{- define "agent-platform.migrateEnv" -}}
- name: MIGRATE_CONTROL_DATABASE_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.migrateControlURL }}}}
- name: MIGRATE_EXECUTION_DATABASE_URL
  valueFrom: {secretKeyRef: {name: {{ .Values.secrets.existingSecret }}, key: {{ .Values.secretKeys.migrateExecutionURL }}}}
{{- end -}}

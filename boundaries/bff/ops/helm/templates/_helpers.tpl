{{/*
Имя chart (по умолчанию совпадает с именем приложения).
*/}}
{{- define "bff.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Полное имя ресурсов: <release>-<chart>, либо только release, если он уже содержит имя chart.
*/}}
{{- define "bff.fullname" -}}
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
Значение label helm.sh/chart.
*/}}
{{- define "bff.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Selector labels. Только стабильные значения: изменение остальных labels не должно
менять selector Deployment и Service.
*/}}
{{- define "bff.selectorLabels" -}}
app.kubernetes.io/name: {{ include "bff.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "bff.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Общие labels (Kubernetes recommended labels).
*/}}
{{- define "bff.labels" -}}
helm.sh/chart: {{ include "bff.chart" . }}
{{ include "bff.selectorLabels" . }}
{{- with include "bff.appVersion" . }}
app.kubernetes.io/version: {{ . | quote }}
{{- end }}
app.kubernetes.io/component: {{ .Values.component | quote }}
app.kubernetes.io/part-of: nomnom
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "bff.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "bff.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Полная ссылка на образ. Без image.tag Kubernetes использует tag "latest";
NOTES.txt предупреждает об этом.
*/}}
{{- define "bff.image" -}}
{{- if and .Values.image.repository .Values.image.tag }}
{{- printf "%s:%s" .Values.image.repository .Values.image.tag }}
{{- else }}
{{- .Values.image.repository }}
{{- end }}
{{- end }}

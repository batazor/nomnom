{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "controlplane.appVersion" -}}
{{- .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "controlplane.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "controlplane.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "controlplane.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "controlplane.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "common.names.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Полная ссылка на образ через common.images.image. Без image.tag и digest
common.images.image оставляет висящее ":", поэтому выводится только repository и
Kubernetes использует tag "latest"; NOTES.txt предупреждает об этом.
*/}}
{{- define "controlplane.image" -}}
{{- if or .Values.image.tag .Values.image.digest }}
{{- include "common.images.image" (dict "imageRoot" .Values.image "global" .Values.global "chart" .Chart) }}
{{- else }}
{{- .Values.image.repository }}
{{- end }}
{{- end }}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay
и drain timeout с запасом, иначе kubelet отправит SIGKILL посреди drain.
*/}}
{{- define "controlplane.validateShutdownBudget" -}}
{{- $budget := add .Values.shutdown.readinessDelaySeconds .Values.shutdown.drainTimeoutSeconds -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $budget) -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than shutdown.readinessDelaySeconds + shutdown.drainTimeoutSeconds (%v)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le (int .Values.shutdown.drainTimeoutSeconds) 0 -}}
{{- fail "shutdown.drainTimeoutSeconds must be positive" -}}
{{- end -}}
{{- end -}}

{{/*
OpenTelemetry (ADR 9, docs/observability.md): одинаковая идентификация traces, metrics и
JSON-логов процесса. service.name — telemetry.serviceName роли, service.instance.id — имя pod (downward API),
service.version — неизменяемый image.tag. Остальные атрибуты окружения задаются
telemetry.resourceAttributes: явная env имеет приоритет над envFrom, поэтому
OTEL_RESOURCE_ATTRIBUTES и OTEL_SERVICE_NAME в env были бы перекрыты. OTLP endpoint и
уровень логов — необязательные telemetry.otlpEndpoint и telemetry.logLevel; пустые не
задаются, и их можно передать через env/envFrom окружения.
Usage: {{ include "controlplane.otelEnv" (dict "context" $ "serviceName" "") }}
*/}}
{{- define "controlplane.otelEnv" -}}
{{- $values := .context.Values -}}
{{- $serviceName := $values.telemetry.serviceName | required "telemetry.serviceName is required" -}}
{{- $attrs := list "service.instance.id=$(POD_NAME)" (printf "service.version=%s" ($values.image.tag | required "image.tag is required")) -}}
{{- range $key, $value := $values.telemetry.resourceAttributes }}
{{- $attrs = append $attrs (printf "%s=%s" $key $value) -}}
{{- end }}
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_SERVICE_NAME
  value: {{ $serviceName | quote }}
- name: OTEL_RESOURCE_ATTRIBUTES
  value: {{ join "," $attrs | quote }}
{{- with $values.telemetry.otlpEndpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
{{- end }}
{{- with $values.telemetry.logLevel }}
- name: LOG__LEVEL
  value: {{ . | quote }}
{{- end }}
{{- end }}

{{/*
Маршруты публичного gateway (ADR 41): без них control plane не опубликует маршруты объявленных Market.
*/}}
{{- define "controlplane.validateRoutes" -}}
{{- range $key := list "namespace" "gateway" "listener" -}}
{{- if not (get $.Values.routes $key) -}}
{{- fail (printf "routes.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

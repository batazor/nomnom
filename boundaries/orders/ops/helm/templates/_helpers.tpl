{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "orders.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "orders.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "orders.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "orders.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "orders.serviceAccountName" -}}
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
{{- define "orders.image" -}}
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
{{- define "orders.validateShutdownBudget" -}}
{{- $budget := add .Values.shutdown.readinessDelaySeconds .Values.shutdown.drainTimeoutSeconds -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $budget) -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than shutdown.readinessDelaySeconds + shutdown.drainTimeoutSeconds (%v)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le (int .Values.shutdown.drainTimeoutSeconds) 0 -}}
{{- fail "shutdown.drainTimeoutSeconds must be positive" -}}
{{- end -}}
{{- if eq (int .Values.management.port) (int .Values.service.targetPort) -}}
{{- fail "management.port must differ from service.targetPort" -}}
{{- end -}}
{{- end -}}

{{/*
PostgreSQL env (ADR 8): DSN из внешнего Secret и лимиты пула процесса. Без dsnSecret.name
переменные не задаются, и процесс orders не стартует (POSTGRES__DSN обязателен).
За пулером в session mode каждое соединение пула держит server-соединение, поэтому
maxConns задаётся values каждой роли. Init container migrate передаёт "migrate": goose
держит advisory lock на одном соединении и применяет миграции на втором (пул 2).
Usage: {{ include "orders.postgresEnv" (dict "context" $ "migrate" false) }}
*/}}
{{- define "orders.postgresEnv" -}}
{{- with .context.Values.postgres }}
{{- if .dsnSecret.name }}
{{- $maxConns := ternary 2 .maxConns $.migrate -}}
{{- $minConns := ternary 0 .minConns $.migrate -}}
{{- if lt (int .maxConns) 1 -}}
{{- fail (printf "postgres.maxConns must be positive, got %v" .maxConns) -}}
{{- end -}}
{{- if or (lt (int .minConns) 0) (gt (int .minConns) 1) -}}
{{- fail (printf "postgres.minConns must be 0 or 1, got %v" .minConns) -}}
{{- end -}}
{{- if gt (int .minConns) (int .maxConns) -}}
{{- fail "postgres.minConns must not exceed postgres.maxConns" -}}
{{- end }}
- name: POSTGRES__DSN
  valueFrom:
    secretKeyRef:
      name: {{ .dsnSecret.name }}
      key: {{ .dsnSecret.key }}
- name: POSTGRES__MAX_CONNS
  value: {{ $maxConns | quote }}
- name: POSTGRES__MIN_CONNS
  value: {{ $minConns | quote }}
- name: POSTGRES__MAX_CONN_LIFETIME
  value: {{ .maxConnLifetime | quote }}
- name: POSTGRES__MAX_CONN_LIFETIME_JITTER
  value: {{ .maxConnLifetimeJitter | quote }}
- name: POSTGRES__MAX_CONN_IDLE_TIME
  value: {{ .maxConnIdleTime | quote }}
- name: POSTGRES__CONNECT_TIMEOUT
  value: {{ .connectTimeout | quote }}
- name: POSTGRES__HEALTH_CHECK_PERIOD
  value: {{ .healthCheckPeriod | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
OpenTelemetry (ADR 9, docs/observability.md): одинаковая идентификация traces, metrics и
JSON-логов процесса. service.name — telemetry.serviceName роли (init container migrate
передаёт "orders-migrate"), service.instance.id — имя pod (downward API),
service.version — неизменяемый image.tag. Остальные атрибуты окружения задаются
telemetry.resourceAttributes: явная env имеет приоритет над envFrom, поэтому
OTEL_RESOURCE_ATTRIBUTES и OTEL_SERVICE_NAME в env были бы перекрыты. OTLP endpoint и
уровень логов — необязательные telemetry.otlpEndpoint и telemetry.logLevel; пустые не
задаются, и их можно передать через env/envFrom окружения.
Usage: {{ include "orders.otelEnv" (dict "context" $ "serviceName" "") }}
*/}}
{{- define "orders.otelEnv" -}}
{{- $values := .context.Values -}}
{{- $serviceName := .serviceName | default $values.telemetry.serviceName | required "telemetry.serviceName is required" -}}
{{- $attrs := list "service.instance.id=$(POD_NAME)" (printf "service.version=%s" (default "unknown" $values.image.tag)) -}}
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
Service identity (ADR 27): процесс с gRPC API (service.enabled) проверяет token, поэтому JWKS, issuer, audience
и allowlist обязательны — иначе процесс не стартует. User assertions (ADR 32)
задаются целиком: JWKS BFF, issuer BFF и делегаты.
*/}}
{{- define "orders.validateIdentity" -}}
{{- if .Values.service.enabled -}}
{{- range $key := list "jwksConfigMap" "issuer" "audience" "serviceAccounts" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- with .Values.identity -}}
{{- if or .userJwksConfigMap .userIssuer .userDelegates -}}
{{- range $key := list "userJwksConfigMap" "userIssuer" "userDelegates" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required with the other user assertion settings" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

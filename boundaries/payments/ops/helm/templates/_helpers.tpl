{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "payments.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "payments.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "payments.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "payments.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "payments.serviceAccountName" -}}
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
{{- define "payments.image" -}}
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
{{- define "payments.validateShutdownBudget" -}}
{{- $budget := add .Values.shutdown.readinessDelaySeconds .Values.shutdown.drainTimeoutSeconds .Values.shutdown.telemetryFlushTimeoutSeconds -}}
{{- if le (int .Values.terminationGracePeriodSeconds) (int $budget) -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than shutdown.readinessDelaySeconds + shutdown.drainTimeoutSeconds + shutdown.telemetryFlushTimeoutSeconds (%v)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le (int .Values.shutdown.drainTimeoutSeconds) 0 -}}
{{- fail "shutdown.drainTimeoutSeconds must be positive" -}}
{{- end -}}
{{- if le (int .Values.shutdown.telemetryFlushTimeoutSeconds) 0 -}}
{{- fail "shutdown.telemetryFlushTimeoutSeconds must be positive" -}}
{{- end -}}
{{- if eq (int .Values.management.port) (int .Values.service.targetPort) -}}
{{- fail "management.port must differ from service.targetPort" -}}
{{- end -}}
{{- end -}}

{{/*
Service identity (ADR 27) и user assertions BFF (ADR 32): процесс проверяет token и
assertion, поэтому JWKS, issuer, audience, allowlist, JWKS BFF, issuer BFF и делегаты
обязательны — иначе процесс не стартует.
*/}}
{{- define "payments.validateIdentity" -}}
{{- range $key := list "jwksConfigMap" "issuer" "audience" "serviceAccounts" "userJwksConfigMap" "userIssuer" "userDelegates" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SpiceDB (ADR 18): endpoint, preshared key из Secret и timeout в формате <int>(ms|s|m)
обязательны — иначе процесс не стартует.
*/}}
{{- define "payments.validateSpiceDB" -}}
{{- with .Values.spicedb -}}
{{- if not .endpoint -}}
{{- fail "spicedb.endpoint is required" -}}
{{- end -}}
{{- if .endpoint -}}
{{- if not .presharedKeySecret.name -}}
{{- fail "spicedb.presharedKeySecret.name is required when spicedb.endpoint is set" -}}
{{- end -}}
{{- if not .presharedKeySecret.key -}}
{{- fail "spicedb.presharedKeySecret.key is required when spicedb.endpoint is set" -}}
{{- end -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString .timeout)) -}}
{{- fail (printf "spicedb.timeout %q must be a positive duration <int>(ms|s|m), e.g. 2s" (toString .timeout)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
PostgreSQL env (ADR 8): connection URI из внешнего Secret и лимиты пула процесса. Без
database.uriSecret.name переменные не задаются, и процесс payments не стартует
(DATABASE__URI обязателен). За пулером в session mode каждое соединение пула держит
server-соединение, поэтому maxConns задаётся values каждого release.
Usage: {{ include "payments.databaseEnv" . }}
*/}}
{{- define "payments.databaseEnv" -}}
{{- with .Values.database }}
{{- if .uriSecret.name -}}
{{- if lt (int .maxConns) 1 -}}
{{- fail (printf "database.maxConns must be positive, got %v" .maxConns) -}}
{{- end -}}
{{- if or (lt (int .minConns) 0) (gt (int .minConns) 1) -}}
{{- fail (printf "database.minConns must be 0 or 1, got %v" .minConns) -}}
{{- end -}}
{{- range $key := list "maxConnLifetime" "maxConnIdleTime" "connectTimeout" "acquireTimeout" -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString (get $.Values.database $key))) -}}
{{- fail (printf "database.%s %q must be a positive duration <int>(ms|s|m), e.g. 5s" $key (toString (get $.Values.database $key))) -}}
{{- end -}}
{{- end }}
- name: DATABASE__URI
  valueFrom:
    secretKeyRef:
      name: {{ .uriSecret.name | quote }}
      key: {{ .uriSecret.key | quote }}
- name: DATABASE__MAX_CONNS
  value: {{ .maxConns | quote }}
- name: DATABASE__MIN_CONNS
  value: {{ .minConns | quote }}
- name: DATABASE__MAX_CONN_LIFETIME
  value: {{ .maxConnLifetime | quote }}
- name: DATABASE__MAX_CONN_IDLE_TIME
  value: {{ .maxConnIdleTime | quote }}
- name: DATABASE__CONNECT_TIMEOUT
  value: {{ .connectTimeout | quote }}
- name: DATABASE__ACQUIRE_TIMEOUT
  value: {{ .acquireTimeout | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
OpenTelemetry env (ADR 9): service.instance.id — имя pod (downward API), service.version —
неизменяемый image.tag, остальные атрибуты — telemetry.resourceAttributes. Явная env
имеет приоритет над envFrom, поэтому OTEL_RESOURCE_ATTRIBUTES в env была бы перекрыта.
Usage: {{ include "payments.otelEnv" . }}
*/}}
{{- define "payments.otelEnv" -}}
{{- with .Values.telemetry }}
{{- $attrs := list "service.instance.id=$(POD_NAME)" (printf "service.version=%s" (default "latest" $.Values.image.tag)) -}}
{{- range $key, $value := .resourceAttributes }}
{{- $attrs = append $attrs (printf "%s=%s" $key (toString $value)) -}}
{{- end }}
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_RESOURCE_ATTRIBUTES
  value: {{ join "," $attrs | quote }}
{{- if .serviceName }}
- name: OTEL_SERVICE_NAME
  value: {{ .serviceName | quote }}
{{- end }}
{{- if .endpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ .endpoint | quote }}
{{- end }}
{{- end }}
{{- end }}

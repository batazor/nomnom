{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "delivery.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "delivery.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "delivery.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "delivery.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "delivery.serviceAccountName" -}}
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
{{- define "delivery.image" -}}
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
{{- define "delivery.validateShutdownBudget" -}}
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
Путь к JWKS кластера внутри контейнера (ADR 27): ConfigMap identity.jwksConfigMap
монтируется read-only каталогом /etc/nomnom/identity, ключ jwks.json становится файлом.
*/}}
{{- define "delivery.identityJwksFile" -}}
/etc/nomnom/identity/jwks.json
{{- end -}}

{{/*
Путь к JWKS BFF внутри контейнера (ADR 32): ConfigMap identity.userJwksConfigMap
монтируется read-only каталогом /etc/nomnom/user-identity.
*/}}
{{- define "delivery.identityUserJwksFile" -}}
/etc/nomnom/user-identity/jwks.json
{{- end -}}


{{/*
SpiceDB (ADR 18): при заданном endpoint preshared key обязателен и берётся только из Secret,
иначе приложение не стартует.
*/}}
{{- define "delivery.validateSpiceDb" -}}
{{- if not .Values.spicedb.presharedKeySecret.name -}}
{{- fail "spicedb.presharedKeySecret.name is required when spicedb.endpoint is set" -}}
{{- end -}}
{{- if not .Values.spicedb.presharedKeySecret.key -}}
{{- fail "spicedb.presharedKeySecret.key is required when spicedb.endpoint is set" -}}
{{- end -}}
{{- if not .Values.spicedb.timeout -}}
{{- fail "spicedb.timeout is required when spicedb.endpoint is set" -}}
{{- end -}}
{{- end -}}

{{/*
PostgreSQL (ADR 8, ADR 31): connection URI только из Secret database.uriSecret и лимиты пула
database.pool. Общий env процесса и init container миграций; dict:
root — контекст chart, maxConns — размер пула этого контейнера.
*/}}
{{- define "delivery.databaseEnv" -}}
{{- with .root.Values.database }}
{{- if .uriSecret.name }}
- name: DATABASE__URI
  valueFrom:
    secretKeyRef:
      name: {{ .uriSecret.name | quote }}
      key: {{ .uriSecret.key | quote }}
- name: DATABASE__MAX_CONNS
  value: {{ $.maxConns | quote }}
- name: DATABASE__MIN_CONNS
  value: {{ .pool.minConns | quote }}
- name: DATABASE__MAX_LIFETIME
  value: {{ .pool.maxLifetime | quote }}
- name: DATABASE__IDLE_TIMEOUT
  value: {{ .pool.idleTimeout | quote }}
- name: DATABASE__ACQUIRE_TIMEOUT
  value: {{ .pool.acquireTimeout | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
OpenTelemetry (ADR 9): OTLP endpoint из telemetry.otlpEndpoint (пусто — экспорта нет);
resource: service.instance.id — имя pod (downward API), service.version — неизменяемый
image.tag, остальные атрибуты окружения — telemetry.resourceAttributes. Явная env имеет
приоритет над envFrom, поэтому OTEL_RESOURCE_ATTRIBUTES в env была бы перекрыта.
*/}}
{{- define "delivery.otelEnv" -}}
{{- $attrs := list "service.instance.id=$(POD_NAME)" (printf "service.version=%s" (default "unknown" .Values.image.tag)) -}}
{{- range $key, $value := .Values.telemetry.resourceAttributes }}
{{- $attrs = append $attrs (printf "%s=%s" $key $value) -}}
{{- end }}
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_RESOURCE_ATTRIBUTES
  value: {{ join "," $attrs | quote }}
{{- with .Values.telemetry.otlpEndpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
{{- end }}
{{- with .Values.telemetry.serviceName }}
- name: OTEL_SERVICE_NAME
  value: {{ . | quote }}
{{- end }}
{{- end }}

{{/*
Service identity (ADR 27): процесс с gRPC API (service.enabled) проверяет token, поэтому JWKS, issuer, audience
и allowlist обязательны — иначе процесс не стартует. User assertions (ADR 32)
задаются целиком: JWKS BFF, issuer BFF и делегаты.
*/}}
{{- define "delivery.validateIdentity" -}}
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

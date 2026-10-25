{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами: digest-теги (например, image ID) длиннее.
*/}}
{{- define "restaurants.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "restaurants.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "restaurants.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "restaurants.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{/*
Имя ServiceAccount.
*/}}
{{- define "restaurants.serviceAccountName" -}}
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
{{- define "restaurants.image" -}}
{{- if or .Values.image.tag .Values.image.digest }}
{{- include "common.images.image" (dict "imageRoot" .Values.image "global" .Values.global "chart" .Chart) }}
{{- else }}
{{- .Values.image.repository }}
{{- end }}
{{- end }}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay,
drain timeout и flush telemetry с запасом, иначе kubelet отправит SIGKILL посреди drain.
*/}}
{{- define "restaurants.validateShutdownBudget" -}}
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
Service identity (ADR 27): процесс с gRPC API (service.enabled) проверяет token, поэтому JWKS, issuer, audience
и allowlist обязательны — иначе процесс не стартует. User assertions (ADR 32)
задаются целиком: JWKS BFF, issuer BFF и делегаты.
*/}}
{{- define "restaurants.validateIdentity" -}}
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

{{/*
SpiceDB (ADR 18): с endpoint приложению нужен preshared key из Secret, иначе оно
не стартует; timeout в формате приложения (500ms, 2s, 1m).
*/}}
{{- define "restaurants.validateSpiceDb" -}}
{{- with .Values.spicedb -}}
{{- if .endpoint -}}
{{- if not (and .presharedKeySecret.name .presharedKeySecret.key) -}}
{{- fail "spicedb.presharedKeySecret.name and spicedb.presharedKeySecret.key are required when spicedb.endpoint is set" -}}
{{- end -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString .timeout)) -}}
{{- fail (printf "spicedb.timeout must be a positive duration like 500ms, 2s or 1m, got %q" (toString .timeout)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
MongoDB (ADR 8): переменные подключения из Secret и лимиты connection pool процесса.
Без uriSecret.name ничего не задаётся, и процессы restaurants и restaurants-consumer не
стартуют (MONGODB__URI обязателен).
Usage: {{ include "restaurants.mongodbEnv" (dict "context" $ "pool" .Values.mongodb.pool) }}
*/}}
{{- define "restaurants.mongodbEnv" -}}
{{- $pool := .pool -}}
{{- with .context.Values.mongodb }}
{{- if .uriSecret.name }}
- name: MONGODB__URI
  valueFrom:
    secretKeyRef:
      name: {{ .uriSecret.name | quote }}
      key: {{ .uriSecret.key | quote }}
- name: MONGODB__DATABASE
  value: {{ .database | quote }}
- name: MONGODB__TIMEOUT
  value: {{ .timeout | quote }}
- name: MONGODB__MAX_POOL_SIZE
  value: {{ $pool.maxPoolSize | quote }}
- name: MONGODB__MIN_POOL_SIZE
  value: {{ $pool.minPoolSize | quote }}
- name: MONGODB__MAX_IDLE_TIME
  value: {{ $pool.maxIdleTime | quote }}
- name: MONGODB__WAIT_QUEUE_TIMEOUT
  value: {{ $pool.waitQueueTimeout | quote }}
- name: MONGODB__MAX_CONNECTING
  value: {{ $pool.maxConnecting | quote }}
{{- end }}
{{- end }}
{{- end }}

{{/*
MongoDB: имя базы, timeout и лимиты пула в формате приложения (pkg/config/connections.ts).
*/}}
{{- define "restaurants.validateMongoDb" -}}
{{- with .Values.mongodb -}}
{{- if .uriSecret.name -}}
{{- if not .uriSecret.key -}}
{{- fail "mongodb.uriSecret.key is required when mongodb.uriSecret.name is set" -}}
{{- end -}}
{{- if not (regexMatch "^[A-Za-z0-9_-]{1,63}$" (toString .database)) -}}
{{- fail (printf "mongodb.database must match ^[A-Za-z0-9_-]{1,63}$, got %q" (toString .database)) -}}
{{- end -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString .timeout)) -}}
{{- fail (printf "mongodb.timeout must be a positive duration like 500ms, 5s or 1m, got %q" (toString .timeout)) -}}
{{- end -}}
{{- $max := int .pool.maxPoolSize -}}
{{- if not (and (regexMatch "^[0-9]+$" (toString .pool.maxPoolSize)) (ge (int .pool.maxPoolSize) 1) (le (int .pool.maxPoolSize) 100)) -}}
{{- fail (printf "mongodb.pool.maxPoolSize must be an integer between 1 and 100, got %q" (toString .pool.maxPoolSize)) -}}
{{- end -}}
{{- if not (and (regexMatch "^[0-9]+$" (toString .pool.minPoolSize)) (le (int .pool.minPoolSize) $max)) -}}
{{- fail (printf "mongodb.pool.minPoolSize must be an integer between 0 and pool.maxPoolSize (%d), got %q" $max (toString .pool.minPoolSize)) -}}
{{- end -}}
{{- if not (and (regexMatch "^[0-9]+$" (toString .pool.maxConnecting)) (ge (int .pool.maxConnecting) 1) (le (int .pool.maxConnecting) $max)) -}}
{{- fail (printf "mongodb.pool.maxConnecting must be an integer between 1 and pool.maxPoolSize (%d), got %q" $max (toString .pool.maxConnecting)) -}}
{{- end -}}
{{- range $name, $value := dict "maxIdleTime" .pool.maxIdleTime "waitQueueTimeout" .pool.waitQueueTimeout -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString $value)) -}}
{{- fail (printf "mongodb.pool.%s must be a positive duration like 500ms, 5s or 5m, got %q" $name (toString $value)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
OpenTelemetry resource (ADR 9), как orders.otelEnv: service.instance.id — имя pod
(downward API), service.version — неизменяемый image.tag (или digest), остальное —
telemetry.resourceAttributes. Явная env имеет приоритет над envFrom, поэтому
OTEL_RESOURCE_ATTRIBUTES из env (ConfigMap) была бы перекрыта.
*/}}
{{- define "restaurants.otelResourceEnv" -}}
{{- $version := required "image.tag or image.digest is required" (.Values.image.tag | default .Values.image.digest) -}}
{{- $attrs := list "service.instance.id=$(POD_NAME)" (printf "service.version=%s" $version) -}}
{{- range $key, $value := .Values.telemetry.resourceAttributes }}
{{- $attrs = append $attrs (printf "%s=%s" $key (toString $value)) -}}
{{- end }}
- name: POD_NAME
  valueFrom:
    fieldRef:
      fieldPath: metadata.name
- name: OTEL_RESOURCE_ATTRIBUTES
  value: {{ join "," $attrs | quote }}
{{- end }}

{{/*
OTEL_* процесса: service.name, resource и OTLP endpoint (traces и metrics), если задан.
*/}}
{{- define "restaurants.otelEnv" -}}
- name: OTEL_SERVICE_NAME
  value: {{ .Values.telemetry.serviceName | quote }}
{{- with .Values.telemetry.endpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
{{- end }}
{{ include "restaurants.otelResourceEnv" . }}
{{- end }}

{{/*
telemetry: service.name обязателен; service.instance.id и service.version chart задаёт сам.
*/}}
{{- define "restaurants.validateTelemetry" -}}
{{- if not .Values.telemetry.serviceName -}}
{{- fail "telemetry.serviceName is required" -}}
{{- end -}}
{{- range $key := list "service.instance.id" "service.version" "service.name" "service.namespace" -}}
{{- if hasKey $.Values.telemetry.resourceAttributes $key -}}
{{- fail (printf "telemetry.resourceAttributes must not set %s: the chart and the application set it" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

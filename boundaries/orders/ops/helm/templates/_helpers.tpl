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
Секунды длительности в формате Go (500ms, 5s, 2m, 1h30m) из config.spec. Другая запись — ошибка.
Usage: {{ include "orders.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" $value) }}
*/}}
{{- define "orders.durationSeconds" -}}
{{- $value := toString .value -}}
{{- if not (regexMatch "^([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$" $value) -}}
{{- fail (printf "%s must be a Go duration such as 5s or 2m, got %q" .key $value) -}}
{{- end -}}
{{- $units := dict "ns" 0.000000001 "us" 0.000001 "µs" 0.000001 "ms" 0.001 "s" 1.0 "m" 60.0 "h" 3600.0 -}}
{{- $seconds := 0.0 -}}
{{- range regexFindAll "[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)" $value -1 -}}
{{- $number := regexFind "^[0-9]+(\\.[0-9]+)?" . -}}
{{- $seconds = addf $seconds (mulf (float64 $number) (get $units (trimPrefix $number .))) -}}
{{- end -}}
{{- $seconds -}}
{{- end -}}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay
и drain timeout config.spec.shutdown с запасом, иначе kubelet отправит SIGKILL посреди drain.
Технический порт отличается от бизнес-порта.
*/}}
{{- define "orders.validateShutdownBudget" -}}
{{- $spec := .Values.config.spec -}}
{{- $shutdown := required "config.spec.shutdown is required" $spec.shutdown -}}
{{- $readiness := include "orders.durationSeconds" (dict "key" "config.spec.shutdown.readinessDelay" "value" (required "config.spec.shutdown.readinessDelay is required" $shutdown.readinessDelay)) | float64 -}}
{{- $drain := include "orders.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" (required "config.spec.shutdown.drainTimeout is required" $shutdown.drainTimeout)) | float64 -}}
{{- $budget := addf $readiness $drain -}}
{{- if le (float64 .Values.terminationGracePeriodSeconds) $budget -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than config.spec.shutdown.readinessDelay + drainTimeout (%vs)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le $drain 0.0 -}}
{{- fail "config.spec.shutdown.drainTimeout must be positive" -}}
{{- end -}}
{{- if and .Values.service.enabled (eq (int (include "orders.managementPort" .)) (int (include "orders.serverPort" .))) -}}
{{- fail "config.spec.management.port must differ from config.spec.server.port" -}}
{{- end -}}
{{- end -}}

{{/*
Порты процесса из config.spec: бизнес-порт контейнера и технический порт metrics.
*/}}
{{- define "orders.serverPort" -}}
{{- required "config.spec.server.port is required for a release with service.enabled" (dig "server" "port" nil .Values.config.spec) | int -}}
{{- end -}}

{{- define "orders.managementPort" -}}
{{- required "config.spec.management.port is required" (dig "management" "port" nil .Values.config.spec) | int -}}
{{- end -}}

{{/*
Пул PostgreSQL роли (ADR 8): за пулером в session mode каждое соединение пула держит
server-соединение, поэтому config.spec.postgres.maxConns задаётся values каждой роли.
*/}}
{{- define "orders.validatePostgres" -}}
{{- with .Values.config.spec.postgres }}
{{- if hasKey . "maxConns" }}
{{- if lt (int .maxConns) 1 -}}
{{- fail (printf "config.spec.postgres.maxConns must be positive, got %v" .maxConns) -}}
{{- end -}}
{{- if and (hasKey . "minConns") (gt (int .minConns) (int .maxConns)) -}}
{{- fail "config.spec.postgres.minConns must not exceed config.spec.postgres.maxConns" -}}
{{- end -}}
{{- end }}
{{- if and (hasKey . "minConns") (or (lt (int .minConns) 0) (gt (int .minConns) 1)) -}}
{{- fail (printf "config.spec.postgres.minConns must be 0 or 1, got %v" .minConns) -}}
{{- end -}}
{{- end }}
{{- end -}}

{{/*
Секреты процесса (FR-764): только env из внешних Secret, в файле конфигурации их нет. DSN
PostgreSQL (POSTGRES__DSN) обязателен всем процессам; preshared key SpiceDB
(SPICEDB__PRESHARED_KEY) — когда задан config.spec.spicedb.endpoint.
*/}}
{{- define "orders.secretEnv" -}}
{{- with .Values.postgres.dsnSecret }}
{{- if .name }}
- name: POSTGRES__DSN
  valueFrom:
    secretKeyRef:
      name: {{ .name }}
      key: {{ .key }}
{{- end }}
{{- end }}
{{- if dig "spicedb" "endpoint" "" .Values.config.spec }}
{{- with .Values.spicedb.presharedKeySecret }}
- name: SPICEDB__PRESHARED_KEY
  valueFrom:
    secretKeyRef:
      name: {{ required "spicedb.presharedKeySecret.name is required when config.spec.spicedb.endpoint is set" .name }}
      key: {{ .key }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Пути, которые chart монтирует в pod; файл конфигурации получает их в spec.identity.
*/}}
{{- define "orders.identityJwksFile" -}}/etc/nomnom/identity/jwks.json{{- end -}}
{{- define "orders.identityUserJwksFile" -}}/etc/nomnom/user-identity/jwks.json{{- end -}}
{{- define "orders.identityTokenDir" -}}/var/run/secrets/nomnom/identity{{- end -}}

{{/*
Файл конфигурации процесса (FR-762): apiVersion config.nomnom.dev/v1alpha1, kind Orders,
metadata.name — роль, spec — config.spec как есть плюс ключи, которыми владеет chart: пути
смонтированных JWKS и tokens в spec.identity и service.version (image.tag) в
spec.telemetry.resourceAttributes. Такой ключ в values — ошибка.
*/}}
{{- define "orders.configFile" -}}
{{- $spec := deepCopy .Values.config.spec -}}
{{- $identity := dig "identity" (dict) $spec -}}
{{- range $key := list "jwksFile" "userJwksFile" "tokenDir" -}}
{{- if hasKey $identity $key -}}
{{- fail (printf "config.spec.identity.%s is set by the chart from the mounted volumes; remove it from values" $key) -}}
{{- end -}}
{{- end -}}
{{- if .Values.identity.jwksConfigMap -}}
{{- $_ := set $identity "jwksFile" (include "orders.identityJwksFile" .) -}}
{{- $_ := set $identity "userJwksFile" (include "orders.identityUserJwksFile" .) -}}
{{- end -}}
{{- if .Values.identity.tokenAudiences -}}
{{- $_ := set $identity "tokenDir" (include "orders.identityTokenDir" .) -}}
{{- end -}}
{{- if $identity -}}
{{- $_ := set $spec "identity" $identity -}}
{{- end -}}
{{- $attributes := dig "telemetry" "resourceAttributes" (dict) $spec -}}
{{- if hasKey $attributes "service.version" -}}
{{- fail "config.spec.telemetry.resourceAttributes.service.version is set by the chart from image.tag; remove it from values" -}}
{{- end -}}
{{- with .Values.image.tag -}}
{{- $_ := set $attributes "service.version" (toString .) -}}
{{- $telemetry := dig "telemetry" (dict) $spec -}}
{{- $_ := set $telemetry "resourceAttributes" $attributes -}}
{{- $_ := set $spec "telemetry" $telemetry -}}
{{- end -}}
apiVersion: config.nomnom.dev/v1alpha1
kind: Orders
metadata:
  name: {{ required "role is required: orders, orders-worker or orders-consumer" .Values.role }}
spec:
  {{- toYaml $spec | nindent 2 }}
{{- end -}}

{{/*
Identity: процесс с gRPC API (service.enabled) проверяет service token (ADR 27) и user assertion BFF
(ADR 32), поэтому JWKS кластера и BFF, issuer, audience, allowlist, issuer BFF и делегаты обязательны —
иначе процесс не стартует.
*/}}
{{- define "orders.validateIdentity" -}}
{{- if .Values.service.enabled -}}
{{- range $key := list "jwksConfigMap" "userJwksConfigMap" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- range $key := list "issuer" "audience" "serviceAccounts" "userIssuer" "userDelegates" -}}
{{- if not (dig "identity" $key "" $.Values.config.spec) -}}
{{- fail (printf "config.spec.identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

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
Секунды длительности Go-формата (500ms, 5s, 1m30s) для проверок chart.
Usage: {{ include "delivery.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" "20s") }}
*/}}
{{- define "delivery.durationSeconds" -}}
{{- $value := toString .value -}}
{{- if not (regexMatch "^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$" $value) -}}
{{- fail (printf "%s must be a duration such as 500ms, 5s or 2m, got %q" .key $value) -}}
{{- end -}}
{{- $scale := dict "ms" 0.001 "s" 1.0 "m" 60.0 "h" 3600.0 -}}
{{- $seconds := 0.0 -}}
{{- range $part := regexFindAll "[0-9]+(\\.[0-9]+)?(ms|s|m|h)" $value -1 -}}
{{- $number := regexFind "^[0-9]+(\\.[0-9]+)?" $part -}}
{{- $seconds = addf $seconds (mulf (float64 $number) (get $scale (trimPrefix $number $part))) -}}
{{- end -}}
{{- $seconds -}}
{{- end -}}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay
и drain timeout файла конфигурации с запасом, иначе kubelet отправит SIGKILL посреди drain.
Технический порт отличается от бизнес-порта.
*/}}
{{- define "delivery.validateShutdownBudget" -}}
{{- $spec := .Values.config.spec -}}
{{- $readinessDelay := required "config.spec.shutdown.readinessDelay is required: the chart checks the shutdown budget" (dig "shutdown" "readinessDelay" nil $spec) -}}
{{- $drainTimeout := required "config.spec.shutdown.drainTimeout is required: the chart checks the shutdown budget" (dig "shutdown" "drainTimeout" nil $spec) -}}
{{- $readiness := include "delivery.durationSeconds" (dict "key" "config.spec.shutdown.readinessDelay" "value" $readinessDelay) | float64 -}}
{{- $drain := include "delivery.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" $drainTimeout) | float64 -}}
{{- if le $drain 0.0 -}}
{{- fail "config.spec.shutdown.drainTimeout must be positive" -}}
{{- end -}}
{{- $budget := addf $readiness $drain -}}
{{- if le (float64 .Values.terminationGracePeriodSeconds) $budget -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than config.spec.shutdown.readinessDelay + config.spec.shutdown.drainTimeout (%vs)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if eq (int (include "delivery.managementPort" .)) (int .Values.service.targetPort) -}}
{{- fail "config.spec.management.port must differ from service.targetPort" -}}
{{- end -}}
{{- with dig "server" "port" nil $spec -}}
{{- if ne (int .) (int $.Values.service.targetPort) -}}
{{- fail "config.spec.server.port must equal service.targetPort" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Технический порт процесса (контейнерный порт metrics) из файла конфигурации.
*/}}
{{- define "delivery.managementPort" -}}
{{- required "config.spec.management.port is required: the chart declares the metrics port" (dig "management" "port" nil .Values.config.spec) -}}
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
Путь к файлу конфигурации процесса в контейнере (env CONFIG_FILE).
*/}}
{{- define "delivery.configFile" -}}
/etc/nomnom/config.yaml
{{- end -}}

{{/*
Файл конфигурации процесса (ADR 17): apiVersion, kind chart, metadata.name — role, spec —
config.spec как есть. Chart дописывает только то, чем владеет: пути смонтированных JWKS в
spec.identity и service.version (image.tag) в spec.telemetry.resourceAttributes; такой ключ в
values — ошибка рендера.
*/}}
{{- define "delivery.config" -}}
{{- $spec := deepCopy (required "config.spec is required" .Values.config.spec) -}}
{{- $identity := get $spec "identity" -}}
{{- if kindIs "map" $identity -}}
{{- range $key := list "jwksFile" "userJwksFile" -}}
{{- if hasKey $identity $key -}}
{{- fail (printf "config.spec.identity.%s is set by the chart from identity.jwksConfigMap and identity.userJwksConfigMap; remove it from values" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if .Values.identity.jwksConfigMap -}}
{{- if not (kindIs "map" $identity) -}}
{{- $identity = dict -}}
{{- $_ := set $spec "identity" $identity -}}
{{- end -}}
{{- $_ := set $identity "jwksFile" (include "delivery.identityJwksFile" .) -}}
{{- $_ := set $identity "userJwksFile" (include "delivery.identityUserJwksFile" .) -}}
{{- end -}}
{{- $telemetry := get $spec "telemetry" -}}
{{- if kindIs "map" $telemetry -}}
{{- $values := get $telemetry "resourceAttributes" -}}
{{- if and (kindIs "map" $values) (hasKey $values "service.version") -}}
{{- fail "config.spec.telemetry.resourceAttributes.service.version is set by the chart from image.tag; remove it from values" -}}
{{- end -}}
{{- end -}}
{{- with .Values.image.tag -}}
{{- if not (kindIs "map" $telemetry) -}}
{{- $telemetry = dict -}}
{{- $_ := set $spec "telemetry" $telemetry -}}
{{- end -}}
{{- $attributes := get $telemetry "resourceAttributes" -}}
{{- if not (kindIs "map" $attributes) -}}
{{- $attributes = dict -}}
{{- $_ := set $telemetry "resourceAttributes" $attributes -}}
{{- end -}}
{{- $_ := set $attributes "service.version" (toString .) -}}
{{- end -}}
{{- $metadata := dict "name" (required "role is required: metadata.name of the config file" .Values.role) -}}
{{- toYaml (dict "apiVersion" "config.nomnom.dev/v1alpha1" "kind" "Delivery" "metadata" $metadata "spec" $spec) -}}
{{- end -}}

{{/*
Temporal (FR-708) обязателен API (start и signals Assignment Workflow) и worker: адрес и
namespace задаёт окружение.
*/}}
{{- define "delivery.validateTemporal" -}}
{{- range $key := list "hostPort" "namespace" -}}
{{- if not (dig "temporal" $key nil $.Values.config.spec) -}}
{{- fail (printf "config.spec.temporal.%s is required (FR-708)" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
SpiceDB (ADR 18): при заданном config.spec.spicedb.endpoint preshared key обязателен и берётся
только из Secret, иначе приложение не стартует.
*/}}
{{- define "delivery.validateSpiceDb" -}}
{{- if not .Values.spicedb.presharedKeySecret.name -}}
{{- fail "spicedb.presharedKeySecret.name is required when config.spec.spicedb.endpoint is set" -}}
{{- end -}}
{{- if not .Values.spicedb.presharedKeySecret.key -}}
{{- fail "spicedb.presharedKeySecret.key is required when config.spec.spicedb.endpoint is set" -}}
{{- end -}}
{{- end -}}

{{/*
Service identity (ADR 27): процесс с gRPC API (service.enabled) проверяет token, поэтому JWKS,
issuer, audience и allowlist обязательны — иначе процесс не стартует. User assertions (ADR 32)
обязательны так же: JWKS BFF, issuer BFF и делегаты.
*/}}
{{- define "delivery.validateIdentity" -}}
{{- if .Values.service.enabled -}}
{{- range $key := list "jwksConfigMap" "userJwksConfigMap" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- range $key := list "issuer" "audience" "serviceAccounts" "userIssuer" "userDelegates" -}}
{{- if not (dig "identity" $key nil $.Values.config.spec) -}}
{{- fail (printf "config.spec.identity.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

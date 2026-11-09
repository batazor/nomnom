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
Длительность в формате приложения (500ms, 5s, 1m, 2h, 7d) в миллисекундах.
Usage: {{ include "restaurants.durationMs" (dict "key" "config.spec.shutdown.drainTimeout" "value" $value) }}
*/}}
{{- define "restaurants.durationMs" -}}
{{- $value := toString .value -}}
{{- if not (regexMatch "^[0-9]+(ms|s|m|h|d)$" $value) -}}
{{- fail (printf "%s must be a duration like 500ms, 5s or 1m, got %q" .key $value) -}}
{{- end -}}
{{- $unit := regexReplaceAll "^[0-9]+" $value "" -}}
{{- mul (regexFind "^[0-9]+" $value | int64) (get (dict "ms" 1 "s" 1000 "m" 60000 "h" 3600000 "d" 86400000) $unit) -}}
{{- end -}}

{{/*
Технический порт процесса: config.spec.management.port; chart объявляет по нему containerPort
metrics, поэтому значение обязательно в values.
*/}}
{{- define "restaurants.managementPort" -}}
{{- required "config.spec.management.port is required: the chart declares the metrics port" (dig "management" "port" nil .Values.config.spec) -}}
{{- end -}}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay,
drain timeout и flush telemetry из config.spec.shutdown с запасом, иначе kubelet отправит
SIGKILL посреди drain. Длительности обязательны в values: chart проверяет по ним бюджет.
*/}}
{{- define "restaurants.validateShutdownBudget" -}}
{{- $budget := 0 -}}
{{- $durations := dict -}}
{{- range $key := list "readinessDelay" "drainTimeout" "telemetryFlushTimeout" -}}
{{- $name := printf "config.spec.shutdown.%s" $key -}}
{{- $value := required (printf "%s is required: the chart checks the shutdown budget" $name) (dig "shutdown" $key nil $.Values.config.spec) -}}
{{- $ms := include "restaurants.durationMs" (dict "key" $name "value" $value) | int64 -}}
{{- $_ := set $durations $key $ms -}}
{{- $budget = add $budget $ms -}}
{{- end -}}
{{- if le (mul (int64 .Values.terminationGracePeriodSeconds) 1000) (int64 $budget) -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than config.spec.shutdown.readinessDelay + drainTimeout + telemetryFlushTimeout (%vms)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le (int64 $durations.drainTimeout) 0 -}}
{{- fail "config.spec.shutdown.drainTimeout must be positive" -}}
{{- end -}}
{{- if le (int64 $durations.telemetryFlushTimeout) 0 -}}
{{- fail "config.spec.shutdown.telemetryFlushTimeout must be positive" -}}
{{- end -}}
{{- if eq (int (include "restaurants.managementPort" .)) (int .Values.service.targetPort) -}}
{{- fail "config.spec.management.port must differ from service.targetPort" -}}
{{- end -}}
{{- end -}}

{{/*
Service identity (ADR 27) и user assertions BFF (ADR 32): процесс с gRPC API (service.enabled)
проверяет token и assertion, поэтому ConfigMap JWKS кластера и BFF, а также issuer, audience,
allowlist, issuer BFF и делегаты в config.spec.identity обязательны — иначе процесс не стартует.
*/}}
{{- define "restaurants.validateIdentity" -}}
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

{{/*
SpiceDB (ADR 18): с config.spec.spicedb.endpoint приложению нужен preshared key из Secret,
иначе оно не стартует; timeout в формате приложения (500ms, 2s, 1m).
*/}}
{{- define "restaurants.validateSpiceDb" -}}
{{- if dig "spicedb" "endpoint" "" .Values.config.spec -}}
{{- if not (and .Values.spicedb.presharedKeySecret.name .Values.spicedb.presharedKeySecret.key) -}}
{{- fail "spicedb.presharedKeySecret.name and spicedb.presharedKeySecret.key are required when config.spec.spicedb.endpoint is set" -}}
{{- end -}}
{{- with dig "spicedb" "timeout" nil .Values.config.spec -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString .)) -}}
{{- fail (printf "config.spec.spicedb.timeout must be a positive duration like 500ms, 2s or 1m, got %q" (toString .)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Valkey: ключ Secret и длительности в формате приложения (pkg/config/cache.ts).
*/}}
{{- define "restaurants.validateValkey" -}}
{{- if .Values.valkey.urlSecret.name -}}
{{- if not .Values.valkey.urlSecret.key -}}
{{- fail "valkey.urlSecret.key is required when valkey.urlSecret.name is set" -}}
{{- end -}}
{{- end -}}
{{- range $name, $value := dict "valkey.commandTimeout" (dig "valkey" "commandTimeout" nil .Values.config.spec) "cache.ttl" (dig "cache" "ttl" nil .Values.config.spec) -}}
{{- if and $value (not (regexMatch "^[1-9][0-9]*(ms|s|m|h)$" (toString $value))) -}}
{{- fail (printf "config.spec.%s must be a positive duration like 50ms, 5s or 5m, got %q" $name (toString $value)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
MongoDB: ключ Secret, имя базы, timeout и лимиты пула в формате приложения
(pkg/config/database.ts); проверяются заданные в config.spec.mongodb значения, остальные
берёт default процесса.
*/}}
{{- define "restaurants.validateMongoDb" -}}
{{- if and .Values.mongodb.uriSecret.name (not .Values.mongodb.uriSecret.key) -}}
{{- fail "mongodb.uriSecret.key is required when mongodb.uriSecret.name is set" -}}
{{- end -}}
{{- $mongodb := dig "mongodb" dict .Values.config.spec -}}
{{- if hasKey $mongodb "database" -}}
{{- if not (regexMatch "^[A-Za-z0-9_-]{1,63}$" (toString $mongodb.database)) -}}
{{- fail (printf "config.spec.mongodb.database must match ^[A-Za-z0-9_-]{1,63}$, got %q" (toString $mongodb.database)) -}}
{{- end -}}
{{- end -}}
{{- range $name := list "timeout" "maxIdleTime" "waitQueueTimeout" -}}
{{- if hasKey $mongodb $name -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString (get $mongodb $name))) -}}
{{- fail (printf "config.spec.mongodb.%s must be a positive duration like 500ms, 5s or 5m, got %q" $name (toString (get $mongodb $name))) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if hasKey $mongodb "maxPoolSize" -}}
{{- if not (and (regexMatch "^[0-9]+$" (toString $mongodb.maxPoolSize)) (ge (int $mongodb.maxPoolSize) 1) (le (int $mongodb.maxPoolSize) 100)) -}}
{{- fail (printf "config.spec.mongodb.maxPoolSize must be an integer between 1 and 100, got %q" (toString $mongodb.maxPoolSize)) -}}
{{- end -}}
{{- range $name, $min := dict "minPoolSize" 0 "maxConnecting" 1 -}}
{{- if hasKey $mongodb $name -}}
{{- $value := get $mongodb $name -}}
{{- if not (and (regexMatch "^[0-9]+$" (toString $value)) (ge (int $value) $min) (le (int $value) (int $mongodb.maxPoolSize))) -}}
{{- fail (printf "config.spec.mongodb.%s must be an integer between %d and maxPoolSize (%d), got %q" $name $min (int $mongodb.maxPoolSize) (toString $value)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
telemetry: service.name и service.instance.id задаёт процесс (имя процесса или
config.spec.telemetry.serviceName и POD_NAME), service.namespace — всегда nomnom,
service.version — chart из image.tag; в resourceAttributes values их не задают.
*/}}
{{- define "restaurants.validateTelemetry" -}}
{{- $attributes := dig "telemetry" "resourceAttributes" dict .Values.config.spec -}}
{{- range $key := list "service.instance.id" "service.version" "service.name" "service.namespace" -}}
{{- if hasKey $attributes $key -}}
{{- fail (printf "config.spec.telemetry.resourceAttributes must not set %s: the chart and the application set it" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Файл конфигурации процесса (ADR 17): apiVersion, kind Restaurants, metadata.name — роль
release (обязательное значение role) и spec — config.spec из values как есть. Chart дописывает
только поля, которыми владеет: пути смонтированных JWKS в spec.identity и service.version
(image.tag или digest) в spec.telemetry.resourceAttributes; такой ключ в values — ошибка.
*/}}
{{- define "restaurants.configFile" -}}
{{- $spec := deepCopy (required "config.spec is required" .Values.config.spec) -}}
{{- range $key := list "jwksFile" "userJwksFile" -}}
{{- if hasKey (dig "identity" dict $spec) $key -}}
{{- fail (printf "config.spec.identity.%s must not be set: the chart sets it from identity.jwksConfigMap and identity.userJwksConfigMap" $key) -}}
{{- end -}}
{{- end -}}
{{- $version := required "image.tag or image.digest is required" (.Values.image.tag | default .Values.image.digest) -}}
{{- $owned := dict "telemetry" (dict "resourceAttributes" (dict "service.version" (toString $version))) -}}
{{- if .Values.identity.jwksConfigMap -}}
{{- $_ := set $owned "identity" (dict "jwksFile" "/etc/nomnom/identity/jwks.json" "userJwksFile" "/etc/nomnom/user-identity/jwks.json") -}}
{{- end -}}
{{- $metadata := dict "name" (required "role is required: metadata.name of the configuration file" .Values.role) -}}
{{- toYaml (dict "apiVersion" "config.nomnom.dev/v1alpha1" "kind" "Restaurants" "metadata" $metadata "spec" (merge $spec $owned)) -}}
{{- end -}}

{{/*
Секреты процесса из Kubernetes Secret под их env-именами; в файле конфигурации полей для них
нет. Без ссылки на Secret переменная не задаётся, и процесс, которому секрет обязателен,
не стартует.
*/}}
{{- define "restaurants.secretEnv" -}}
{{- if dig "spicedb" "endpoint" "" .Values.config.spec }}
- name: SPICEDB__PRESHARED_KEY
  valueFrom:
    secretKeyRef:
      name: {{ .Values.spicedb.presharedKeySecret.name | quote }}
      key: {{ .Values.spicedb.presharedKeySecret.key | quote }}
{{- end }}
{{- with .Values.mongodb.uriSecret }}
{{- if .name }}
- name: MONGODB__URI
  valueFrom:
    secretKeyRef:
      name: {{ .name | quote }}
      key: {{ .key | quote }}
{{- end }}
{{- end }}
{{- with .Values.valkey.urlSecret }}
{{- if .name }}
- name: VALKEY__URL
  valueFrom:
    secretKeyRef:
      name: {{ .name | quote }}
      key: {{ .key | quote }}
{{- end }}
{{- end }}
{{- end }}

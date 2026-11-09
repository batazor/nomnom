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
Длительность <int>(ms|s|m) в миллисекундах; другой формат — ошибка с ключом.
Usage: {{ include "payments.durationMilliseconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" $value) }}
*/}}
{{- define "payments.durationMilliseconds" -}}
{{- $value := toString (required (printf "%s is required" .key) .value) -}}
{{- if not (regexMatch "^[0-9]+(ms|s|m)$" $value) -}}
{{- fail (printf "%s %q must be a duration <int>(ms|s|m), e.g. 5s" .key $value) -}}
{{- end -}}
{{- $amount := int (regexFind "^[0-9]+" $value) -}}
{{- if hasSuffix "ms" $value -}}
{{- $amount -}}
{{- else if hasSuffix "s" $value -}}
{{- mul $amount 1000 -}}
{{- else -}}
{{- mul $amount 60000 -}}
{{- end -}}
{{- end -}}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay,
drain timeout и flush telemetry из config.spec.shutdown с запасом, иначе kubelet отправит
SIGKILL посреди drain. Порты процесса из config.spec различны.
*/}}
{{- define "payments.validateShutdownBudget" -}}
{{- $shutdown := required "config.spec.shutdown is required" .Values.config.spec.shutdown -}}
{{- $ms := dict -}}
{{- range $key := list "readinessDelay" "drainTimeout" "telemetryFlushTimeout" -}}
{{- $_ := set $ms $key (int (include "payments.durationMilliseconds" (dict "key" (printf "config.spec.shutdown.%s" $key) "value" (get $shutdown $key)))) -}}
{{- end -}}
{{- $budget := add $ms.readinessDelay $ms.drainTimeout $ms.telemetryFlushTimeout -}}
{{- if le (mul (int .Values.terminationGracePeriodSeconds) 1000) (int $budget) -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than config.spec.shutdown.readinessDelay + drainTimeout + telemetryFlushTimeout (%vms)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le (int $ms.drainTimeout) 0 -}}
{{- fail "config.spec.shutdown.drainTimeout must be positive" -}}
{{- end -}}
{{- if le (int $ms.telemetryFlushTimeout) 0 -}}
{{- fail "config.spec.shutdown.telemetryFlushTimeout must be positive" -}}
{{- end -}}
{{- $server := int (include "payments.port" (dict "context" . "section" "server" "key" "port")) -}}
{{- $management := int (include "payments.port" (dict "context" . "section" "management" "key" "port")) -}}
{{- $webhooks := int (include "payments.port" (dict "context" . "section" "provider" "key" "webhookPort")) -}}
{{- if eq $management $server -}}
{{- fail "config.spec.management.port must differ from config.spec.server.port" -}}
{{- end -}}
{{- if or (eq $webhooks $server) (eq $webhooks $management) -}}
{{- fail "config.spec.provider.webhookPort must differ from config.spec.server.port and config.spec.management.port" -}}
{{- end -}}
{{- end -}}

{{/*
Порт процесса из config.spec: chart объявляет по нему порты контейнера и Service.
Usage: {{ include "payments.port" (dict "context" $ "section" "server" "key" "port") }}
*/}}
{{- define "payments.port" -}}
{{- $value := dig .section .key "" .context.Values.config.spec -}}
{{- required (printf "config.spec.%s.%s is required" .section .key) $value | int -}}
{{- end -}}

{{/*
Service identity (ADR 27) и user assertions BFF (ADR 32): процесс проверяет token и
assertion, поэтому JWKS, issuer, audience, allowlist, JWKS BFF, issuer BFF и делегаты
обязательны — иначе процесс не стартует.
*/}}
{{- define "payments.validateIdentity" -}}
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

{{/*
SpiceDB (ADR 18): endpoint, preshared key из Secret и timeout в формате <int>(ms|s|m)
обязательны — иначе процесс не стартует.
*/}}
{{- define "payments.validateSpiceDB" -}}
{{- if not (dig "spicedb" "endpoint" "" .Values.config.spec) -}}
{{- fail "config.spec.spicedb.endpoint is required" -}}
{{- end -}}
{{- if not .Values.spicedb.presharedKeySecret.name -}}
{{- fail "spicedb.presharedKeySecret.name is required" -}}
{{- end -}}
{{- if not .Values.spicedb.presharedKeySecret.key -}}
{{- fail "spicedb.presharedKeySecret.key is required" -}}
{{- end -}}
{{- with dig "spicedb" "timeout" "" .Values.config.spec -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString .)) -}}
{{- fail (printf "config.spec.spicedb.timeout %q must be a positive duration <int>(ms|s|m), e.g. 2s" (toString .)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Пул PostgreSQL (ADR 8) из config.spec.database: за пулером в session mode каждое
соединение пула держит server-соединение, поэтому maxConns задаётся values каждого release.
*/}}
{{- define "payments.validateDatabase" -}}
{{- $database := dig "database" dict .Values.config.spec -}}
{{- if and (hasKey $database "maxConns") (lt (int $database.maxConns) 1) -}}
{{- fail (printf "config.spec.database.maxConns must be positive, got %v" $database.maxConns) -}}
{{- end -}}
{{- if and (hasKey $database "minConns") (or (lt (int $database.minConns) 0) (gt (int $database.minConns) 1)) -}}
{{- fail (printf "config.spec.database.minConns must be 0 or 1, got %v" $database.minConns) -}}
{{- end -}}
{{- range $key := list "maxConnLifetime" "maxConnIdleTime" "connectTimeout" "acquireTimeout" -}}
{{- if hasKey $database $key -}}
{{- if not (regexMatch "^[1-9][0-9]*(ms|s|m)$" (toString (get $database $key))) -}}
{{- fail (printf "config.spec.database.%s %q must be a positive duration <int>(ms|s|m), e.g. 5s" $key (toString (get $database $key))) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Файл конфигурации процесса (ADR 17): config.spec как есть плюс ключи, которыми владеет
chart, — пути смонтированных JWKS и service.version из image.tag. Ключ chart в values —
ошибка: у значения один источник.
Usage: {{ include "payments.config" . }}
*/}}
{{- define "payments.config" -}}
{{- $spec := deepCopy .Values.config.spec -}}
{{- $identity := required "config.spec.identity is required" $spec.identity -}}
{{- range $key := list "jwksFile" "userJwksFile" -}}
{{- if hasKey $identity $key -}}
{{- fail (printf "config.spec.identity.%s is set by the chart from identity.jwksConfigMap and identity.userJwksConfigMap; remove it from values" $key) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $identity "jwksFile" "/etc/nomnom/identity/jwks.json" -}}
{{- $_ := set $identity "userJwksFile" "/etc/nomnom/user-identity/jwks.json" -}}
{{- if not (hasKey $spec "telemetry") -}}
{{- $_ := set $spec "telemetry" dict -}}
{{- end -}}
{{- if not (hasKey $spec.telemetry "resourceAttributes") -}}
{{- $_ := set $spec.telemetry "resourceAttributes" dict -}}
{{- end -}}
{{- if hasKey $spec.telemetry.resourceAttributes "service.version" -}}
{{- fail "config.spec.telemetry.resourceAttributes[\"service.version\"] is set by the chart from image.tag; remove it from values" -}}
{{- end -}}
{{- with .Values.image.tag -}}
{{- $_ := set $spec.telemetry.resourceAttributes "service.version" (toString .) -}}
{{- end -}}
apiVersion: config.nomnom.dev/v1alpha1
kind: Payments
metadata:
  name: {{ required "role is required" .Values.role }}
spec:
  {{- toYaml $spec | nindent 2 }}
{{- end -}}

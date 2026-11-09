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
Секунды длительности Go-формата (500ms, 5s, 1m30s) — для проверок chart; процесс разбирает
значение сам.
Usage: {{ include "controlplane.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" .) | float64 }}
*/}}
{{- define "controlplane.durationSeconds" -}}
{{- $value := toString .value -}}
{{- if not (regexMatch "^(0|([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))+)$" $value) -}}
{{- fail (printf "%s must be a Go duration such as 500ms, 5s or 2m, got %q" .key $value) -}}
{{- end -}}
{{- $scale := dict "ns" 0.000000001 "us" 0.000001 "µs" 0.000001 "ms" 0.001 "s" 1.0 "m" 60.0 "h" 3600.0 -}}
{{- $seconds := 0.0 -}}
{{- range $part := regexFindAll "[0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)" $value -1 -}}
{{- $number := regexFind "^[0-9]+(\\.[0-9]+)?" $part -}}
{{- $seconds = addf $seconds (mulf (float64 $number) (get $scale (trimPrefix $number $part))) -}}
{{- end -}}
{{- $seconds -}}
{{- end -}}

{{/*
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay
и drain timeout из config.spec.shutdown с запасом, иначе kubelet отправит SIGKILL посреди drain.
*/}}
{{- define "controlplane.validateShutdownBudget" -}}
{{- $shutdown := .Values.config.spec.shutdown | required "config.spec.shutdown is required: the chart checks the shutdown budget" -}}
{{- $readinessDelay := include "controlplane.durationSeconds" (dict "key" "config.spec.shutdown.readinessDelay" "value" ($shutdown.readinessDelay | required "config.spec.shutdown.readinessDelay is required")) | float64 -}}
{{- $drainTimeout := include "controlplane.durationSeconds" (dict "key" "config.spec.shutdown.drainTimeout" "value" ($shutdown.drainTimeout | required "config.spec.shutdown.drainTimeout is required")) | float64 -}}
{{- $budget := addf $readinessDelay $drainTimeout -}}
{{- if le (float64 .Values.terminationGracePeriodSeconds) $budget -}}
{{- fail (printf "terminationGracePeriodSeconds (%v) must be greater than config.spec.shutdown.readinessDelay + drainTimeout (%vs)" .Values.terminationGracePeriodSeconds $budget) -}}
{{- end -}}
{{- if le $drainTimeout 0.0 -}}
{{- fail "config.spec.shutdown.drainTimeout must be positive" -}}
{{- end -}}
{{- end -}}

{{/*
Технический порт процесса — config.spec.management.port: тот же, что слушает процесс.
*/}}
{{- define "controlplane.managementPort" -}}
{{- .Values.config.spec.management.port | required "config.spec.management.port is required" -}}
{{- end -}}

{{/*
Маршруты публичного gateway (ADR 41): без них control plane не опубликует маршруты объявленных Market.
*/}}
{{- define "controlplane.validateRoutes" -}}
{{- range $key := list "namespace" "gateway" "listener" -}}
{{- if not (get $.Values.config.spec.routes $key) -}}
{{- fail (printf "config.spec.routes.%s is required" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Файл конфигурации процесса (ADR 17, ADR 9): apiVersion, kind и metadata.name — роль из
обязательного role, spec — config.spec как есть. Chart дописывает только то, чем владеет:
service.version из неизменяемого image.tag в spec.telemetry.resourceAttributes. Если этот ключ
задан в values, рендер падает: у значения один источник. service.instance.id процесс берёт из
POD_NAME (downward API), service.name — из spec.telemetry.serviceName.
*/}}
{{- define "controlplane.configFile" -}}
{{- $values := .Values.config.spec -}}
{{- if hasKey (dig "telemetry" "resourceAttributes" (dict) $values) "service.version" -}}
{{- fail "config.spec.telemetry.resourceAttributes.service.version is set by the chart from image.tag: remove it from values" -}}
{{- end -}}
{{- $owned := dict "telemetry" (dict "resourceAttributes" (dict "service.version" (.Values.image.tag | required "image.tag is required: service.version of telemetry" | toString))) -}}
{{- $spec := mustMergeOverwrite (deepCopy $values) $owned -}}
{{- $document := dict
  "apiVersion" "config.nomnom.dev/v1alpha1"
  "kind" "ControlPlane"
  "metadata" (dict "name" (.Values.role | required "role is required: metadata.name of the process configuration file"))
  "spec" $spec
-}}
{{- toYaml $document -}}
{{- end -}}

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
PostgreSQL (ADR 8, ADR 31): URL только из Secret database.urlSecret. Общий env процесса
и init container миграций.
*/}}
{{- define "delivery.databaseEnv" -}}
{{- with .Values.database }}
{{- if .urlSecret.name }}
- name: DATABASE__URL
  valueFrom:
    secretKeyRef:
      name: {{ .urlSecret.name | quote }}
      key: {{ .urlSecret.key | quote }}
- name: DATABASE__MAX_CONNS
  value: {{ .maxConns | quote }}
{{- end }}
{{- end }}
{{- end }}

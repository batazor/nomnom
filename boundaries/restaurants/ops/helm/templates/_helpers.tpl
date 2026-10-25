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
Бюджет shutdown runtime contract: terminationGracePeriodSeconds покрывает readiness delay
и drain timeout с запасом, иначе kubelet отправит SIGKILL посреди drain.
*/}}
{{- define "restaurants.validateShutdownBudget" -}}
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
Service identity (ADR 27): с JWKS приложению нужны issuer, audience и allowlist,
иначе оно не стартует или остаётся fail closed. User assertions (ADR 32) требуют
service identity, issuer BFF и делегатов.
*/}}
{{- define "restaurants.validateIdentity" -}}
{{- with .Values.identity -}}
{{- if .jwksConfigMap -}}
{{- range $key := list "issuer" "audience" "serviceAccounts" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required when identity.jwksConfigMap is set" $key) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if .userJwksConfigMap -}}
{{- range $key := list "jwksConfigMap" "userIssuer" "userDelegates" -}}
{{- if not (get $.Values.identity $key) -}}
{{- fail (printf "identity.%s is required when identity.userJwksConfigMap is set" $key) -}}
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
MongoDB (ADR 8): переменные подключения из Secret. Без uriSecret.name ничего не
задаётся, и процессы restaurants и restaurants-migrate не стартуют (MONGODB__URI обязателен).
*/}}
{{- define "restaurants.mongodbEnv" -}}
{{- with .Values.mongodb }}
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
{{- end }}
{{- end }}
{{- end }}

{{/*
MongoDB: имя базы и timeout в формате приложения.
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
{{- end -}}
{{- end -}}
{{- end -}}

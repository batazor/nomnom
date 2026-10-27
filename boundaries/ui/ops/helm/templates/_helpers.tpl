{{/*
Имена, базовые labels и selector берутся из library chart bitnami/common
(common.names.*, common.labels.*); здесь только специфика приложения.
*/}}

{{/*
Версия приложения для label app.kubernetes.io/version: appVersion chart, иначе image.tag.
Значение label ограничено 63 символами.
*/}}
{{- define "ui.appVersion" -}}
{{- .Chart.AppVersion | default .Values.image.tag | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{/*
Labels ресурса: common.labels.standard плюс component, part-of и version.
Usage: {{ include "ui.labels" (dict "customLabels" .Values.podLabels "context" $) }}
*/}}
{{- define "ui.labels" -}}
{{- $extra := dict "app.kubernetes.io/component" (.context.Values.component | toString) "app.kubernetes.io/part-of" "nomnom" -}}
{{- with include "ui.appVersion" .context }}
{{- $_ := set $extra "app.kubernetes.io/version" . -}}
{{- end }}
{{- $custom := merge (dict) (default (dict) .customLabels) (default (dict) .context.Values.commonLabels) $extra -}}
{{- include "common.labels.standard" (dict "customLabels" $custom "context" .context) -}}
{{- end }}

{{- define "ui.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "common.names.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "ui.image" -}}
{{- if or .Values.image.tag .Values.image.digest }}
{{- include "common.images.image" (dict "imageRoot" .Values.image "global" .Values.global "chart" .Chart) }}
{{- else }}
{{- fail "image.tag or image.digest is required" }}
{{- end }}
{{- end }}

{{/*
Origin (scheme://host[:port]) адреса для connect-src.
*/}}
{{- define "ui.origin" -}}
{{- $url := urlParse . -}}
{{- printf "%s://%s" $url.scheme $url.host -}}
{{- end }}

{{/*
Окружение приложения: ключи VITE_* для window.__NOMNOM_CONFIG__ (runtime-config.ts).
Обязательные поля проверяются здесь, остальное — приложением при старте.
*/}}
{{- define "ui.runtimeConfig" -}}
{{- with .Values.config -}}
{{- $config := dict
  "VITE_PUBLIC_API_URL" (required "config.publicApiUrl is required" .publicApiUrl)
  "VITE_LOCALE" (required "config.locale is required" .locale)
  "VITE_ENABLE_MOCKS" "false"
  "VITE_DEPLOYMENT_ENVIRONMENT" (required "config.deploymentEnvironment is required" .deploymentEnvironment)
  "VITE_OIDC_ISSUER" (required "config.oidc.issuer is required (ADR 35)" .oidc.issuer)
  "VITE_OIDC_CLIENT_ID" (required "config.oidc.clientId is required" .oidc.clientId)
  "VITE_OIDC_AUDIENCE" (required "config.oidc.audience is required" .oidc.audience)
-}}
{{- with .faroUrl }}
{{- $_ := set $config "VITE_FARO_URL" . -}}
{{- $_ := set $config "VITE_APP_VERSION" (required "config.appVersion is required with config.faroUrl" $.Values.config.appVersion) -}}
{{- end }}
{{- with .oidc.demoEmailDomain }}
{{- $_ := set $config "VITE_OIDC_DEMO_EMAIL_DOMAIN" . -}}
{{- end }}
{{- with .oidc.demoPassword }}
{{- $_ := set $config "VITE_OIDC_DEMO_PASSWORD" . -}}
{{- end }}
{{- $config | toJson -}}
{{- end -}}
{{- end }}

{{/*
Origins, к которым обращается приложение: публичный API, OIDC issuer и Faro collector.
*/}}
{{- define "ui.connectOrigins" -}}
{{- $origins := list (include "ui.origin" .Values.config.publicApiUrl) (include "ui.origin" .Values.config.oidc.issuer) -}}
{{- with .Values.config.faroUrl }}
{{- $origins = append $origins (include "ui.origin" .) -}}
{{- end }}
{{- uniq $origins | toJson -}}
{{- end }}

{{/*
Content-Security-Policy origin (AGENTS.md, Edge) — та же политика, что vite.config.ts
отдаёт в vite preview: скрипты, стили и шрифты только свои, единственный inline <style>
react-aria usePress разрешён хешем, сеть — origins окружения.
*/}}
{{- define "ui.contentSecurityPolicy" -}}
{{- $connect := prepend (include "ui.connectOrigins" . | fromJsonArray) "'self'" -}}
{{- printf "default-src 'self'; script-src 'self'; style-src 'self' 'sha256-38RhXrc7EdReTKsOm23ZPOCUgniTUUcjky8QOOrQx6o='; img-src 'self' data:; font-src 'self'; connect-src %s; worker-src 'self'; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'" (join " " $connect) -}}
{{- end }}

{{/*
Connection-Allowlist (Chrome 152+): ограничивает все соединения страницы, не только
известные CSP; браузеры без поддержки заголовок игнорируют.
*/}}
{{- define "ui.connectionAllowlist" -}}
{{- $patterns := list -}}
{{- range (include "ui.connectOrigins" . | fromJsonArray) }}
{{- $patterns = append $patterns (printf "%q" (printf "%s/*" .)) -}}
{{- end }}
{{- printf "(%s response-origin)" (join " " $patterns) -}}
{{- end }}

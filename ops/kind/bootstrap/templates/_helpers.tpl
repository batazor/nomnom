{{/*
Значение ключа существующего Secret либо новое случайное (randAlphaNum).
lookup читает Secret при helm install/upgrade, поэтому повторный deploy учётные данные не
меняет. Без кластера (helm template, --dry-run=client) lookup пуст и значение всегда новое.
Аргументы: namespace, name, key, length.
*/}}
{{- define "bootstrap.secretValue" -}}
{{- $existing := lookup "v1" "Secret" .namespace .name | default dict -}}
{{- $encoded := dig "data" .key "" $existing -}}
{{- if $encoded -}}
{{- b64dec $encoded -}}
{{- else -}}
{{- randAlphaNum (int .length) -}}
{{- end -}}
{{- end -}}

{{/* Labels metadata ресурсов chart; pod templates и selectors их не получают. */}}
{{- define "bootstrap.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: nomnom-kind
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

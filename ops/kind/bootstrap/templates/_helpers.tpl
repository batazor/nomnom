{{/*
Значение ключа существующего Secret либо новое случайное (randAlphaNum).
lookup читает Secret при helm install/upgrade, поэтому повторный deploy учётные данные не
меняет. Без кластера (helm template, --dry-run=client) lookup пуст и значение новое.
Значение запоминается в .Values._secretValues на время render: один и тот же ключ, прочитанный
несколькими шаблонами (пароль PostgreSQL и cdc-credentials, ADR 37), совпадает и при первой
установке. Аргументы: root, namespace, name, key, length.
*/}}
{{- define "bootstrap.secretValue" -}}
{{- if not (hasKey .root.Values "_secretValues") -}}
{{- $_ := set .root.Values "_secretValues" dict -}}
{{- end -}}
{{- $cache := .root.Values._secretValues -}}
{{- $id := printf "%s/%s/%s" .namespace .name .key -}}
{{- if not (hasKey $cache $id) -}}
{{- $existing := lookup "v1" "Secret" .namespace .name | default dict -}}
{{- $encoded := dig "data" .key "" $existing -}}
{{- $_ := set $cache $id (ternary (b64dec $encoded) (randAlphaNum (int .length)) (ne $encoded "")) -}}
{{- end -}}
{{- get $cache $id -}}
{{- end -}}

{{/* Labels metadata ресурсов chart; pod templates и selectors их не получают. */}}
{{- define "bootstrap.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: nomnom-kind
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end -}}

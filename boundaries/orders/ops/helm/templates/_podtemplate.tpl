{{/*
Pod template of the orders process: Deployment of API and consumer,
WorkerDeployment of Temporal Worker Controller for the worker (ADR 5).
*/}}
{{- define "orders.podTemplate" -}}
metadata:
  annotations:
    # Изменение файла конфигурации перезапускает pod: процесс читает его только при старте.
    checksum/config: {{ include "orders.configFile" . | sha256sum }}
    {{- with .Values.podAnnotations }}
    {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 4 }}
    {{- end }}
  labels:
    {{- include "orders.labels" (dict "customLabels" .Values.podLabels "context" $) | nindent 4 }}
spec:
  {{- include "orders.validateShutdownBudget" . }}
  {{- include "orders.validateIdentity" . }}
  {{- include "orders.validatePostgres" . }}
  terminationGracePeriodSeconds: {{ .Values.terminationGracePeriodSeconds }}
  {{- with .Values.imagePullSecrets }}
  imagePullSecrets:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  serviceAccountName: {{ include "orders.serviceAccountName" . }}
  automountServiceAccountToken: {{ .Values.serviceAccount.automount }}
  {{- with .Values.podSecurityContext }}
  securityContext:
    {{- include "common.compatibility.renderSecurityContext" (dict "secContext" . "context" $) | nindent 4 }}
  {{- end }}
  containers:
    - name: {{ .Chart.Name }}
      image: {{ include "orders.image" . | quote }}
      imagePullPolicy: {{ .Values.image.pullPolicy }}
      {{- with .Values.command }}
      command:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.securityContext }}
      securityContext:
        {{- include "common.compatibility.renderSecurityContext" (dict "secContext" . "context" $) | nindent 8 }}
      {{- end }}
      ports:
        {{- if .Values.service.enabled }}
        - name: {{ .Values.service.portName }}
          containerPort: {{ include "orders.serverPort" . }}
          protocol: TCP
        {{- end }}
        - name: metrics
          containerPort: {{ include "orders.managementPort" . }}
          protocol: TCP
      {{- with .Values.envFrom }}
      envFrom:
        {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 8 }}
      {{- end }}
      env:
        - name: CONFIG_FILE
          value: /etc/nomnom/config.yaml
        # service.instance.id процесса (FR-768).
        - name: POD_NAME
          valueFrom:
            fieldRef:
              fieldPath: metadata.name
        {{- with include "orders.secretEnv" . | trim }}
        {{- . | nindent 8 }}
        {{- end }}
        {{- with .Values.extraEnv }}
        {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 8 }}
        {{- end }}
      {{- range $probe := list "startupProbe" "livenessProbe" "readinessProbe" }}
      {{- with get $.Values $probe }}
      {{- if .enabled }}
      {{ $probe }}:
        {{- include "common.tplvalues.render" (dict "value" (omit . "enabled") "context" $) | nindent 8 }}
      {{- end }}
      {{- end }}
      {{- end }}
      {{- with .Values.resources }}
      resources:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      volumeMounts:
        # subPath: каталог /etc/nomnom остаётся доступным для монтирования JWKS.
        - name: config
          mountPath: /etc/nomnom/config.yaml
          subPath: config.yaml
          readOnly: true
        {{- if .Values.identity.jwksConfigMap }}
        - name: identity-jwks
          mountPath: {{ include "orders.identityJwksFile" . | dir }}
          readOnly: true
        - name: identity-user-jwks
          mountPath: {{ include "orders.identityUserJwksFile" . | dir }}
          readOnly: true
        {{- end }}
        {{- if .Values.identity.tokenAudiences }}
        - name: identity-tokens
          mountPath: {{ include "orders.identityTokenDir" . }}
          readOnly: true
        {{- end }}
        {{- with .Values.volumeMounts }}
        {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 8 }}
        {{- end }}
  volumes:
    - name: config
      configMap:
        name: {{ include "common.names.fullname" . }}
    {{- with .Values.identity }}
    {{- if .jwksConfigMap }}
    - name: identity-jwks
      configMap:
        name: {{ .jwksConfigMap }}
        items:
          - key: jwks.json
            path: jwks.json
    - name: identity-user-jwks
      configMap:
        name: {{ .userJwksConfigMap }}
        items:
          - key: jwks.json
            path: jwks.json
    {{- end }}
    {{- end }}
    {{- with .Values.identity.tokenAudiences }}
    # ADR 27: one projected ServiceAccount token per target context; kubelet rotates them.
    - name: identity-tokens
      projected:
        sources:
          {{- range . }}
          - serviceAccountToken:
              audience: {{ . }}
              path: {{ . }}
              expirationSeconds: {{ $.Values.identity.tokenExpirationSeconds }}
          {{- end }}
    {{- end }}
    {{- with .Values.volumes }}
    {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 4 }}
    {{- end }}
  {{- with .Values.nodeSelector }}
  nodeSelector:
    {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 4 }}
  {{- end }}
  {{- with .Values.affinity }}
  affinity:
    {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 4 }}
  {{- end }}
  {{- with .Values.tolerations }}
  tolerations:
    {{- include "common.tplvalues.render" (dict "value" . "context" $) | nindent 4 }}
  {{- end }}
{{- end }}

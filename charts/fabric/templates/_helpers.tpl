{{- define "fabric.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: fabric-node
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: fabric
singleaxis.com/profile: {{ .Values.profile.name | quote }}
{{- end -}}

{{/* Production records must have a deployment-owned identity. */}}
{{- define "fabric.validateTenantId" -}}
{{- if and (eq .Values.profile.name "shadow-production") (not (.Values.tenant.id | toString | trim)) -}}
{{- fail "profile shadow-production requires tenant.id (set --set tenant.id=<customer-controlled-id>)" -}}
{{- end -}}
{{- end -}}

{{/*
shadow-production is a named, fail-closed recorder posture. These invariants
are checked after Helm merges overrides, so weakening one cannot silently
retain the production profile label. This is operational hardening, not a
regulatory certification and not proof that a destination persisted a batch.
*/}}
{{- define "fabric.validateShadowProduction" -}}
{{- if eq .Values.profile.name "shadow-production" -}}
{{- $required := dict
      "networkPolicy.denyDefault" true
      "otelCollector.enabled" true
      "otel-collector.fabric.guard.dropUnknownClasses" true
      "otel-collector.fabric.guard.extraAllowedFields" (dict)
      "otel-collector.fabric.guard.extraAllowedTraceFields" (list)
      "otel-collector.receiver.requireTLS" true
      "otel-collector.receiver.requireClientCertificate" true
      "otel-collector.exporter.requireEndpoint" true
      "otel-collector.exporter.requireTLS" true
      "otel-collector.exporter.requireAuth" true
      "otel-collector.exporter.requireDurableQueue" true
      "otel-collector.exporter.sendingQueue.enabled" true
      "otel-collector.exporter.sendingQueue.blockOnOverflow" true
      "otel-collector.exporter.sendingQueue.persistence.enabled" true
      "otel-collector.exporter.sendingQueue.persistence.fsync" true
      "otel-collector.exporter.retry.enabled" true
      "otel-collector.exporter.retry.maxElapsedTime" "0s"
      "otel-collector.debugExporter.enabled" false
      "otel-collector.batch.enabled" false
      "otel-collector.networkPolicy.enabled" true
      "otel-collector.networkPolicy.requireExplicitIngress" true
      "otel-collector.networkPolicy.exporterEgress.requireExplicit" true
-}}
{{- range $path, $expected := $required -}}
  {{- $cur := $.Values -}}
  {{- $missing := false -}}
  {{- range $seg := splitList "." $path -}}
    {{- if and (kindIs "map" $cur) (hasKey $cur $seg) -}}
      {{- $cur = index $cur $seg -}}
    {{- else -}}
      {{- $missing = true -}}
      {{- $cur = dict -}}
    {{- end -}}
  {{- end -}}
  {{- if $missing -}}
    {{- fail (printf "profile shadow-production requires %q but the value is missing" $path) -}}
  {{- end -}}
  {{- if not (deepEqual $cur $expected) -}}
    {{- fail (printf "profile shadow-production requires %q=%v; effective value is %v" $path $expected $cur) -}}
  {{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

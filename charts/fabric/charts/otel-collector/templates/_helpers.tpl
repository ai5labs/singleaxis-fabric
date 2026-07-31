{{/*
Expand the name of the chart.
*/}}
{{- define "otel-collector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Supports .Values.fullnameOverride.
*/}}
{{- define "otel-collector.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "otel-collector.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "otel-collector.labels" -}}
helm.sh/chart: {{ include "otel-collector.chart" . }}
{{ include "otel-collector.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: fabric
{{- end -}}

{{- define "otel-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "otel-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "otel-collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "otel-collector.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Validate fabric.sampler config: when enabled, exactly one of
hmacKey / hmacKeySecret.name must be set. When hmacKey is set
inline, it must be a 64-char hex string (the sampler validates
this at runtime; we fail-early here so the pod never starts
with a bad key).
*/}}
{{- define "otel-collector.validateSampler" -}}
{{- if .Values.fabric.sampler.enabled -}}
{{- $inline := .Values.fabric.sampler.hmacKey -}}
{{- $secret := .Values.fabric.sampler.hmacKeySecret.name -}}
{{- if and (not $inline) (not $secret) -}}
{{- fail "fabric.sampler.enabled=true requires fabric.sampler.hmacKey or fabric.sampler.hmacKeySecret.name" -}}
{{- end -}}
{{- if and $inline $secret -}}
{{- fail "fabric.sampler: set only one of hmacKey / hmacKeySecret" -}}
{{- end -}}
{{- if $inline -}}
{{- if not (regexMatch "^[0-9a-f]{64}$" $inline) -}}
{{- fail "fabric.sampler.hmacKey must be a 64-char lowercase hex string (32 bytes). Generate one with: openssl rand -hex 32. For production, prefer fabric.sampler.hmacKeySecret referencing a Kubernetes Secret." -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Validate fabric.redact config.

``fabric.redact.enabled: true`` wires the fabricredact processor to a
Unix socket at ``fabric.redact.unixSocket``. The Collector chart does
not render a Presidio sidecar — that ships in components/presidio-sidecar
as a dedicated chart (TBD). Without a socket provider this processor
will permanently error.

Fail-closed unless the operator has explicitly acknowledged one of:

  fabric.redact.existingSocketProvider: <name>
      Name of an out-of-band component (e.g. sidecar chart, DaemonSet)
      that mounts the socket into this pod.

  fabric.redact.acceptMissingProvider: true
      Escape hatch for CI / smoke renders. The Collector will boot
      but the redact processor will fail on every event — suitable
      only for template verification, never for a real install.
*/}}
{{- define "otel-collector.validateRedact" -}}
{{- if .Values.fabric.redact.enabled -}}
{{- $provider := .Values.fabric.redact.existingSocketProvider | default "" -}}
{{- $accept := .Values.fabric.redact.acceptMissingProvider | default false -}}
{{- if and (eq $provider "") (not $accept) -}}
{{- fail (printf "fabric.redact.enabled=true but no socket provider configured. Name the component that mounts %s, or accept a broken redact processor for a smoke render. From the fabric umbrella chart the value paths are prefixed with the subchart alias:\n  --set otel-collector.fabric.redact.existingSocketProvider=<name>\n  --set otel-collector.fabric.redact.acceptMissingProvider=true   (renders only; runtime errors on every event)\nInstalling this subchart directly, drop the `otel-collector.` prefix. The presidio-sidecar subchart IS bundled in the umbrella — enable it with --set presidioSidecar.enabled=true and --set presidio-sidecar.tenantKey.existingSecret=<secret>, then point existingSocketProvider at it." .Values.fabric.redact.unixSocket) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Compute the exporter list for a pipeline.

There is no OTLP endpoint that works in an L1-only OSS deploy, so
``exporter.endpoint`` has no default. The historical default
`fabric-ingest:8080` resolved to the SingleAxis commercial Telemetry
Bridge and dropped every span on the floor when that service was not
present (see CHANGELOG 0.1.3). The rule this chart enforces is:

  A rendered pipeline NEVER points at an endpoint that is not set.

  - ``exporter.endpoint`` set   -> `otlphttp/fabric` is rendered and
    listed. `debug` is added alongside it when
    ``debugExporter.enabled: true``.
  - ``exporter.endpoint`` empty -> `otlphttp/fabric` is NOT rendered
    at all. The pipeline falls back to `debug`, so spans land in the
    collector pod's stdout (`kubectl logs`) instead of vanishing, and
    NOTES.txt prints a loud post-install warning. This is a dev
    posture: stdout is visible, but it is not durable and it is not
    an audit trail.

The list is never empty, so the rendered collector config is always
valid. ``exporter.acceptUnsetEndpoint`` is no longer consulted — an
unset endpoint is now a supported (and loudly announced) state rather
than a blanket render-time error, so the escape hatch has nothing to
escape. Profiles that cannot accept a stdout-only posture set
``exporter.requireEndpoint: true`` — see validateExporter below.
*/}}
{{- define "otel-collector.validateExporter" -}}
{{- if and .Values.exporter.requireEndpoint (not .Values.exporter.endpoint) -}}
{{- fail "otel-collector.exporter.endpoint is empty and this profile sets exporter.requireEndpoint=true. The stdout debug-exporter fallback is a dev posture: pod stdout is not durable and is not an audit trail, so a profile making a retention or compliance claim must name a real backend. Set --set otel-collector.exporter.endpoint=<OTLP/HTTP url> (Datadog/Honeycomb intake, your own collector chain, or the SingleAxis commercial Telemetry Bridge ingress). To render this profile without a backend anyway, pass --set otel-collector.exporter.requireEndpoint=false and understand that spans will only reach pod stdout." -}}
{{- end -}}
{{- end -}}

{{/*
Exporter name list — see the comment block above.
*/}}
{{- define "otel-collector.exporterNames" -}}
{{- $names := list -}}
{{- if .Values.exporter.endpoint -}}
{{- $names = append $names "otlphttp/fabric" -}}
{{- end -}}
{{- if or .Values.debugExporter.enabled (not .Values.exporter.endpoint) -}}
{{- $names = append $names "debug" -}}
{{- end -}}
{{- if not $names -}}
{{- fail "otel-collector: no exporters resolved for the pipeline. This is a chart bug — the debug exporter is meant to be the unconditional fallback when exporter.endpoint is empty." -}}
{{- end -}}
{{- join ", " $names -}}
{{- end -}}

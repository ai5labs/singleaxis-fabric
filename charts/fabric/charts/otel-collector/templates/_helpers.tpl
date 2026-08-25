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
Return the directory mounted into the Collector for the redaction
socket. In sidecar mode it is derived from unixSocket; in external
volume mode the operator declares it explicitly.
*/}}
{{- define "otel-collector.redactSocketMountPath" -}}
{{- if eq .Values.fabric.redact.provider.mode "sidecar" -}}
{{- dir .Values.fabric.redact.unixSocket -}}
{{- else -}}
{{- .Values.fabric.redact.provider.externalVolume.mountPath -}}
{{- end -}}
{{- end -}}

{{/*
Validate fabric.redact config. Redaction can only be enabled when the
rendered pod has a concrete provider and socket volume. A descriptive
string and the old acceptMissingProvider escape hatch are deliberately
not accepted as proof of a provider.
*/}}
{{- define "otel-collector.validateRedact" -}}
{{- if .Values.fabric.redact.enabled -}}
{{- $mode := .Values.fabric.redact.provider.mode | default "" -}}
{{- if or .Values.fabric.redact.existingSocketProvider .Values.fabric.redact.acceptMissingProvider -}}
{{- if eq $mode "" -}}
{{- fail "fabric.redact existingSocketProvider/acceptMissingProvider cannot prove a realizable provider. Migrate to fabric.redact.provider.mode=sidecar (recommended) or externalVolume; enabled redaction never permits a missing provider." -}}
{{- end -}}
{{- end -}}
{{- if not (has $mode (list "sidecar" "externalVolume")) -}}
{{- fail "fabric.redact.enabled=true requires fabric.redact.provider.mode=sidecar or externalVolume" -}}
{{- end -}}
{{- if not (hasPrefix "/" .Values.fabric.redact.unixSocket) -}}
{{- fail "fabric.redact.unixSocket must be an absolute path" -}}
{{- end -}}
{{- $socketPath := .Values.fabric.redact.unixSocket -}}
{{- $cleanSocketPath := clean $socketPath -}}
{{- if ne $socketPath $cleanSocketPath -}}
{{- fail "fabric.redact.unixSocket must already be normalized (no duplicate separators, '.' segments, or '..' traversal)" -}}
{{- end -}}
{{- if hasSuffix "/" $socketPath -}}
{{- fail "fabric.redact.unixSocket must name a socket file, not a directory" -}}
{{- end -}}
{{- $socketDir := dir $cleanSocketPath -}}
{{- if or (eq $socketDir "/") (eq $socketDir ".") -}}
{{- fail "fabric.redact.unixSocket must be located in a non-root absolute directory; mounting '/' as the socket volume is forbidden" -}}
{{- end -}}
{{- if eq $mode "sidecar" -}}
{{- if not .Values.fabric.redact.provider.sidecar.image.repository -}}
{{- fail "fabric.redact.provider.sidecar.image.repository is required" -}}
{{- end -}}
{{- if not .Values.fabric.redact.provider.sidecar.tenantKeySecret.name -}}
{{- fail "fabric.redact provider sidecar requires provider.sidecar.tenantKeySecret.name; the redactor refuses to start without a tenant-specific HMAC key" -}}
{{- end -}}
{{- if not .Values.fabric.redact.provider.sidecar.tenantKeySecret.key -}}
{{- fail "fabric.redact provider sidecar requires provider.sidecar.tenantKeySecret.key" -}}
{{- end -}}
{{- if not (has .Values.fabric.redact.provider.sidecar.redactionMode (list "hmac" "tag")) -}}
{{- fail "fabric.redact.provider.sidecar.redactionMode must be hmac or tag" -}}
{{- end -}}
{{- else -}}
{{- $external := .Values.fabric.redact.provider.externalVolume -}}
{{- if or (not $external.mountPath) (not (hasPrefix "/" $external.mountPath)) -}}
{{- fail "fabric.redact.provider.externalVolume.mountPath must be an absolute path" -}}
{{- end -}}
{{- $cleanMountPath := clean $external.mountPath -}}
{{- if or (ne $external.mountPath $cleanMountPath) (eq $cleanMountPath "/") -}}
{{- fail "fabric.redact.provider.externalVolume.mountPath must be a normalized, non-root absolute directory" -}}
{{- end -}}
{{- if not (hasPrefix (printf "%s/" (trimSuffix "/" $cleanMountPath)) $cleanSocketPath) -}}
{{- fail "fabric.redact.unixSocket must be located underneath fabric.redact.provider.externalVolume.mountPath" -}}
{{- end -}}
{{- if empty $external.volumeSource -}}
{{- fail "fabric.redact.provider.externalVolume.volumeSource must contain a real Kubernetes VolumeSource (for example persistentVolumeClaim, csi, or an approved hostPath)" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
An enabled policy must have actual Rego mounted into the pod. The old
empty bundle path and emptyDir behavior produced either a silent no-op
or a fail-closed collector that dropped the entire audit stream.
*/}}
{{- define "otel-collector.validatePolicy" -}}
{{- if .Values.fabric.policy.enabled -}}
{{- if not .Values.fabric.policy.bundlePath -}}
{{- fail "fabric.policy.enabled=true requires fabric.policy.bundlePath" -}}
{{- end -}}
{{- if not (hasPrefix "/" .Values.fabric.policy.bundlePath) -}}
{{- fail "fabric.policy.bundlePath must be an absolute path" -}}
{{- end -}}
{{- if not .Values.fabric.policy.bundleConfigMap -}}
{{- fail "fabric.policy.enabled=true requires fabric.policy.bundleConfigMap naming an existing ConfigMap with one or more .rego files; the chart never mounts an empty policy volume" -}}
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
{{- if and .Values.exporter.requireEndpoint .Values.exporter.endpoint (not (hasPrefix "https://" .Values.exporter.endpoint)) -}}
{{- fail "otel-collector.exporter.requireEndpoint=true requires an https:// exporter.endpoint; regulated profiles cannot export audit telemetry over plaintext HTTP" -}}
{{- end -}}
{{- if and .Values.exporter.requireEndpoint .Values.exporter.insecure -}}
{{- fail "otel-collector.exporter.requireEndpoint=true requires exporter.insecure=false; regulated profiles cannot disable TLS verification" -}}
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

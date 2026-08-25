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
Validate fabric.redact config. A usable UDS has to be created inside
the Collector pod: naming an external provider cannot establish a
cross-pod Unix socket. The chart therefore accepts only its rendered,
pod-local sidecar when fabricredact is enabled.
*/}}
{{- define "otel-collector.validateRedact" -}}
{{- if .Values.fabric.redact.enabled -}}
{{- if not .Values.fabric.redact.embedded.enabled -}}
{{- fail "fabric.redact.enabled=true requires fabric.redact.embedded.enabled=true. A provider name is insufficient: Unix sockets cannot be shared across pods, so the chart must render the Presidio container and shared emptyDir in the Collector pod." -}}
{{- end -}}
{{- if not .Values.fabric.redact.embedded.tenantKeySecret.name -}}
{{- fail "fabric.redact.enabled=true requires fabric.redact.embedded.tenantKeySecret.name. Create a tenant-specific Secret and reference it; the chart will not generate or persist a redaction key." -}}
{{- end -}}
{{- if not (hasPrefix "/" .Values.fabric.redact.unixSocket) -}}
{{- fail "fabric.redact.unixSocket must be an absolute path inside the shared pod volume" -}}
{{- end -}}
{{- if not (has .Values.fabric.redact.byteHandling (list "redact_utf8" "reject" "passthrough")) -}}
{{- fail "fabric.redact.byteHandling must be one of: redact_utf8, reject, passthrough" -}}
{{- end -}}
{{- if not (has .Values.fabric.redact.embedded.redactionMode (list "hmac" "tag")) -}}
{{- fail "fabric.redact.embedded.redactionMode must be one of: hmac, tag" -}}
{{- end -}}
{{- else if .Values.fabric.redact.embedded.enabled -}}
{{- fail "fabric.redact.embedded.enabled=true requires fabric.redact.enabled=true; do not run an unused sensitive-data processor" -}}
{{- end -}}
{{- end -}}

{{/*
Validate fabric.policy bundle wiring. A non-empty bundle path is an
enforcement claim, so it must resolve to either an operator-owned
ConfigMap or the exact chart-owned reference policy version. Empty
directories are never rendered as policy sources.
*/}}
{{- define "otel-collector.validatePolicy" -}}
{{- if and .Values.fabric.policy.enabled .Values.fabric.policy.bundlePath -}}
{{- $external := .Values.fabric.policy.bundleConfigMap | default "" -}}
{{- $reference := .Values.fabric.policy.referencePolicy.enabled | default false -}}
{{- if and (not $external) (not $reference) -}}
{{- fail "fabric.policy.bundlePath is set but no policy source exists. Set fabric.policy.bundleConfigMap to an existing ConfigMap containing Rego, or explicitly enable fabric.policy.referencePolicy.enabled for the limited chart-owned baseline." -}}
{{- end -}}
{{- if and $external $reference -}}
{{- fail "fabric.policy.bundleConfigMap and fabric.policy.referencePolicy.enabled are mutually exclusive; select exactly one policy source" -}}
{{- end -}}
{{- if and $reference (ne .Values.fabric.policy.referencePolicy.version "v1") -}}
{{- fail "unsupported fabric.policy.referencePolicy.version; this chart currently ships only v1" -}}
{{- end -}}
{{- if not (hasPrefix "/" .Values.fabric.policy.bundlePath) -}}
{{- fail "fabric.policy.bundlePath must be an absolute container path" -}}
{{- end -}}
{{- else if .Values.fabric.policy.referencePolicy.enabled -}}
{{- fail "fabric.policy.referencePolicy.enabled=true requires fabric.policy.enabled=true and a non-empty fabric.policy.bundlePath" -}}
{{- end -}}
{{- end -}}

{{- define "otel-collector.referencePolicyConfigMapName" -}}
{{- printf "%s-reference-policy-%s" (include "otel-collector.fullname" .) .Values.fabric.policy.referencePolicy.version | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Validate OTLP receiver TLS. Secret names enable file-backed TLS settings; the
require* flags turn the expected posture into a fail-closed profile invariant.
*/}}
{{- define "otel-collector.validateReceiver" -}}
{{- $r := .Values.receiver -}}
{{- $server := $r.tls.serverCertificateSecret -}}
{{- $clientCA := $r.tls.clientCASecret -}}
{{- if and $r.requireTLS (not $server.name) -}}
{{- fail "receiver.requireTLS=true requires receiver.tls.serverCertificateSecret.name" -}}
{{- end -}}
{{- if and $r.requireClientCertificate (not $r.requireTLS) -}}
{{- fail "receiver.requireClientCertificate=true requires receiver.requireTLS=true" -}}
{{- end -}}
{{- if and $r.requireClientCertificate (not $clientCA.name) -}}
{{- fail "receiver.requireClientCertificate=true requires receiver.tls.clientCASecret.name" -}}
{{- end -}}
{{- if and $clientCA.name (not $server.name) -}}
{{- fail "receiver.tls.clientCASecret.name requires receiver.tls.serverCertificateSecret.name; client-certificate verification cannot run without receiver TLS" -}}
{{- end -}}
{{- if $server.name -}}
{{- if not $server.certKey -}}
{{- fail "receiver.tls.serverCertificateSecret.name requires receiver.tls.serverCertificateSecret.certKey" -}}
{{- end -}}
{{- if not $server.keyKey -}}
{{- fail "receiver.tls.serverCertificateSecret.name requires receiver.tls.serverCertificateSecret.keyKey" -}}
{{- end -}}
{{- end -}}
{{- if and $clientCA.name (not $clientCA.key) -}}
{{- fail "receiver.tls.clientCASecret.name requires receiver.tls.clientCASecret.key" -}}
{{- end -}}
{{- end -}}

{{/*
Validate explicit exporter egress. An endpoint URL cannot be translated safely
into a NetworkPolicy peer, so regulated profiles require the operator to name
both the destination peer(s) and allowed port(s).
*/}}
{{- define "otel-collector.validateNetworkPolicy" -}}
{{- $np := .Values.networkPolicy -}}
{{- $ee := $np.exporterEgress -}}
{{- $hasPeers := gt (len $ee.to) 0 -}}
{{- $hasPorts := gt (len $ee.ports) 0 -}}
{{- if and (or $hasPeers $hasPorts) (not $np.enabled) -}}
{{- fail "networkPolicy.exporterEgress is configured but networkPolicy.enabled=false; enable the policy or remove the misleading rule" -}}
{{- end -}}
{{- if ne $hasPeers $hasPorts -}}
{{- fail "networkPolicy.exporterEgress requires both non-empty to and ports lists" -}}
{{- end -}}
{{- if $ee.requireExplicit -}}
{{- if not $np.enabled -}}
{{- fail "networkPolicy.exporterEgress.requireExplicit=true requires networkPolicy.enabled=true" -}}
{{- end -}}
{{- if not .Values.exporter.endpoint -}}
{{- fail "networkPolicy.exporterEgress.requireExplicit=true requires exporter.endpoint" -}}
{{- end -}}
{{- if not $hasPeers -}}
{{- fail "networkPolicy.exporterEgress.requireExplicit=true requires an operator-supplied networkPolicy.exporterEgress.to peer" -}}
{{- end -}}
{{- if not $hasPorts -}}
{{- fail "networkPolicy.exporterEgress.requireExplicit=true requires operator-supplied networkPolicy.exporterEgress.ports" -}}
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

  - ``exporter.endpoint`` set   -> `otlp_http/fabric` is rendered and
    listed. `debug` is added alongside it when
    ``debugExporter.enabled: true``.
  - ``exporter.endpoint`` empty -> `otlp_http/fabric` is NOT rendered
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
Validate the exporter transport and delivery contract. These checks do
not promise lossless or exactly-once delivery; they prevent profiles
from rendering while contradicting their declared security/durability
posture.
*/}}
{{- define "otel-collector.validateDelivery" -}}
{{- $e := .Values.exporter -}}
{{- $p := $e.sendingQueue.persistence -}}
{{- if $e.endpoint -}}
{{- if not (regexMatch "^https?://" $e.endpoint) -}}
{{- fail "exporter.endpoint must be an absolute http:// or https:// URL" -}}
{{- end -}}
{{- end -}}
{{- if $e.requireTLS -}}
{{- if not (hasPrefix "https://" $e.endpoint) -}}
{{- fail "exporter.requireTLS=true requires an https:// exporter.endpoint" -}}
{{- end -}}
{{- if $e.insecure -}}
{{- fail "exporter.requireTLS=true requires exporter.insecure=false" -}}
{{- end -}}
{{- if $e.insecureSkipVerify -}}
{{- fail "exporter.requireTLS=true rejects exporter.insecureSkipVerify=true" -}}
{{- end -}}
{{- end -}}
{{- if $e.requireAuth -}}
{{- if not $e.auth.secret.name -}}
{{- fail "exporter.requireAuth=true requires exporter.auth.secret.name" -}}
{{- end -}}
{{- end -}}
{{- if $e.auth.secret.name -}}
{{- if not $e.auth.secret.key -}}
{{- fail "exporter.auth.secret.name requires exporter.auth.secret.key" -}}
{{- end -}}
{{- if not (regexMatch "^[!#$%&'*+.^_`|~0-9A-Za-z-]+$" $e.auth.headerName) -}}
{{- fail "exporter.auth.headerName must be a valid HTTP header token" -}}
{{- end -}}
{{- end -}}
{{- if $p.enabled -}}
{{- if not $e.endpoint -}}
{{- fail "exporter.sendingQueue.persistence.enabled=true requires exporter.endpoint; the debug exporter has no persistent queue" -}}
{{- end -}}
{{- if not $e.sendingQueue.enabled -}}
{{- fail "exporter.sendingQueue.persistence.enabled=true requires exporter.sendingQueue.enabled=true" -}}
{{- end -}}
{{- if not (hasPrefix "/" $p.directory) -}}
{{- fail "exporter.sendingQueue.persistence.directory must be an absolute container path" -}}
{{- end -}}
{{- if and $p.existingClaim (ne (int .Values.replicaCount) 1) -}}
{{- fail "exporter.sendingQueue.persistence.existingClaim can only be used with replicaCount=1; multiple Collectors must not share one file-storage database. Leave existingClaim empty for one StatefulSet PVC per replica." -}}
{{- end -}}
{{- end -}}
{{- if $e.requireDurableQueue -}}
{{- if not $e.endpoint -}}
{{- fail "exporter.requireDurableQueue=true requires exporter.endpoint" -}}
{{- end -}}
{{- if not (and $e.sendingQueue.enabled $p.enabled) -}}
{{- fail "exporter.requireDurableQueue=true requires an enabled persistent sending queue" -}}
{{- end -}}
{{- if not $e.retry.enabled -}}
{{- fail "exporter.requireDurableQueue=true requires exporter.retry.enabled=true" -}}
{{- end -}}
{{- if ne (toString $e.retry.maxElapsedTime) "0s" -}}
{{- fail "exporter.requireDurableQueue=true requires exporter.retry.maxElapsedTime=0s so transient failures are not discarded after a time limit" -}}
{{- end -}}
{{- if not $e.sendingQueue.blockOnOverflow -}}
{{- fail "exporter.requireDurableQueue=true requires exporter.sendingQueue.blockOnOverflow=true so a full queue backpressures OTLP senders instead of immediately rejecting telemetry" -}}
{{- end -}}
{{- if not $p.fsync -}}
{{- fail "exporter.requireDurableQueue=true requires exporter.sendingQueue.persistence.fsync=true to request a filesystem sync after each queue write" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Exporter name list — see the comment block above.
*/}}
{{- define "otel-collector.exporterNames" -}}
{{- $names := list -}}
{{- if .Values.exporter.endpoint -}}
{{- $names = append $names "otlp_http/fabric" -}}
{{- end -}}
{{- if or .Values.debugExporter.enabled (not .Values.exporter.endpoint) -}}
{{- $names = append $names "debug" -}}
{{- end -}}
{{- if not $names -}}
{{- fail "otel-collector: no exporters resolved for the pipeline. This is a chart bug — the debug exporter is meant to be the unconditional fallback when exporter.endpoint is empty." -}}
{{- end -}}
{{- join ", " $names -}}
{{- end -}}

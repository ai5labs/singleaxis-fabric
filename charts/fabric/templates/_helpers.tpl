{{- define "fabric.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/name: fabric
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: fabric
singleaxis.com/profile: {{ .Values.profile.name | quote }}
{{- end -}}

{{/*
Validate that ``tenant.id`` is set for any non-dev profile.

Empty tenant ID stamps every emitted span with no tenant attribution,
which silently corrupts downstream multi-tenant analysis. The
permissive-dev profile is allowed to skip this since it's intended
for evaluation on a single-user laptop.
*/}}
{{- define "fabric.validateTenantId" -}}
{{- if and (not (.Values.tenant.id | toString | trim)) (ne .Values.profile.name "permissive-dev") -}}
{{- fail (printf "tenant.id is required when profile.name=%q (set --set tenant.id=<uuid>; only the 'permissive-dev' profile may leave it blank)." .Values.profile.name) -}}
{{- end -}}
{{- end -}}

{{/*
The high-risk profile relies on the update-agent admission webhook as its
post-install drift backstop. Keep the full admission and certificate posture
as an invariant after Helm has merged operator overrides. In particular,
chart-generated private keys are unsuitable here because Helm stores rendered
Secrets in release state; regulated deployments must delegate issuance and
rotation to the operator's cert-manager issuer.

This check intentionally lives in the parent chart for the same render-order
reason as validateProfileLocks: parent failures are consistently surfaced by
helm template, install, upgrade, and lint.
*/}}
{{- define "fabric.validateHighRiskAdmission" -}}
{{- if eq .Values.profile.name "eu-ai-act-high-risk" -}}
  {{- if not .Values.updateAgent.enabled -}}
    {{- fail "profile \"eu-ai-act-high-risk\" requires updateAgent.enabled=true so admission remains a drift backstop." -}}
  {{- end -}}
  {{- if not (dig "config" "failClosed" false (index .Values "update-agent")) -}}
    {{- fail "profile \"eu-ai-act-high-risk\" requires update-agent.config.failClosed=true." -}}
  {{- end -}}
  {{- if ne (dig "webhook" "failurePolicy" "" (index .Values "update-agent") | toString) "Fail" -}}
    {{- fail "profile \"eu-ai-act-high-risk\" requires update-agent.webhook.failurePolicy=Fail." -}}
  {{- end -}}
  {{- if ne (dig "webhook" "enforceProfileLocks" "" (index .Values "update-agent") | toString) "on" -}}
    {{- fail "profile \"eu-ai-act-high-risk\" requires update-agent.webhook.enforceProfileLocks=on from the first install." -}}
  {{- end -}}
  {{- if ne (dig "tls" "mode" "" (index .Values "update-agent") | toString) "certManager" -}}
    {{- fail "profile \"eu-ai-act-high-risk\" requires update-agent.tls.mode=certManager; Helm-generated webhook private keys are not permitted." -}}
  {{- end -}}
  {{- range $field := list "name" "kind" "group" -}}
    {{- if not (dig "tls" "certManager" "issuerRef" $field "" (index $.Values "update-agent") | toString | trim) -}}
      {{- fail (printf "profile \"eu-ai-act-high-risk\" requires a non-empty update-agent.tls.certManager.issuerRef.%s." $field) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
Enforce profile.lockedFields as render-time invariants.

Helm merges profile values and user ``--set`` overrides BEFORE
rendering, so a template cannot tell a profile default from a user
override. The only workable invariant is therefore keyed on profile
identity: when a profile lists a field in ``profile.lockedFields``,
that value path MUST resolve truthy (i.e. the control stays enabled)
or the render fails — on install, upgrade, ``helm template``, and
parent-level ``helm lint`` alike.

This is what makes the eu-ai-act-high-risk lock-list real:

    otel-collector.fabric.guard.enabled
    otel-collector.fabric.guard.dropUnknownClasses
    otel-collector.fabric.guard.traceProcessingEnabled
    otel-collector.fabric.redact.enabled

A tenant who passes e.g.
``--set otel-collector.fabric.guard.enabled=false`` together with the
EU profile gets a hard render error naming the disabled control, not
a silently weakened regulated posture.

Semantics:
  - locked field resolves falsy            -> fail (control disabled)
  - locked field path missing entirely     -> fail
  - owning component toggle off            -> fail. NOTE: Helm keeps a
    disabled subchart's values merged into the parent tree (verified
    on Helm 3.19), so a missing path does NOT catch
    ``--set otelCollector.enabled=false``. For each lock we therefore
    also require the sibling camelCase ``<Toggle>.enabled`` value
    (otel-collector -> otelCollector.enabled) to stay true whenever
    that toggle key exists.
  - empty / absent lockedFields list       -> no-op (permissive-dev).

Custom profiles derived per docs/regulatory-profiles.md get the same
enforcement for whatever boolean fields they add to lockedFields.

Render-order note: invoked from templates/namespace.yaml, which is a
PARENT chart template. Parent-chart fails propagate everywhere; fails
inside SUBCHART templates are swallowed by ``helm lint`` (verified
against Helm 3.19), which is exactly why this check lives here rather
than in the otel-collector subchart.
*/}}
{{- define "fabric.validateProfileLocks" -}}
{{- if eq .Values.profile.name "eu-ai-act-high-risk" -}}
{{- $required := dict
      "networkPolicy.denyDefault" true
      "otelCollector.enabled" true
      "otel-collector.fabric.guard.enabled" true
      "otel-collector.fabric.guard.dropUnknownClasses" true
      "otel-collector.fabric.guard.traceProcessingEnabled" true
      "otel-collector.fabric.policy.enabled" true
      "otel-collector.fabric.redact.enabled" true
      "otel-collector.receiver.requireTLS" true
      "otel-collector.receiver.requireClientCertificate" true
      "otel-collector.exporter.requireEndpoint" true
      "otel-collector.exporter.requireTLS" true
      "otel-collector.exporter.requireAuth" true
      "otel-collector.exporter.requireDurableQueue" true
      "otel-collector.exporter.sendingQueue.persistence.enabled" true
      "otel-collector.networkPolicy.enabled" true
      "otel-collector.networkPolicy.exporterEgress.requireExplicit" true
      "updateAgent.enabled" true
      "update-agent.config.failClosed" true
      "update-agent.webhook.failurePolicy" "Fail"
      "update-agent.webhook.enforceProfileLocks" "on"
      "update-agent.networkPolicy.enabled" true
      "update-agent.tls.mode" "certManager"
-}}
{{- range $path, $expected := $required -}}
  {{- $segs := splitList "." $path -}}
  {{- $cur := $.Values -}}
  {{- $missing := false -}}
  {{- range $seg := $segs -}}
    {{- if and (kindIs "map" $cur) (hasKey $cur $seg) -}}
      {{- $cur = index $cur $seg -}}
    {{- else -}}
      {{- $missing = true -}}
      {{- $cur = dict -}}
    {{- end -}}
  {{- end -}}
  {{- if $missing -}}
    {{- fail (printf "high-risk profile requires %q but that value path is missing" $path) -}}
  {{- end -}}
  {{- if not (deepEqual $cur $expected) -}}
    {{- fail (printf "high-risk profile requires %q to equal %v; effective value is %v" $path $expected $cur) -}}
  {{- end -}}
{{- end -}}
{{- end -}}
{{- $locks := .Values.profile.lockedFields | default list -}}
{{- range $path := $locks -}}
  {{- $segs := splitList "." $path -}}
  {{- $cur := $.Values -}}
  {{- $missing := false -}}
  {{- range $seg := $segs -}}
    {{- if and (kindIs "map" $cur) (hasKey $cur $seg) -}}
      {{- $cur = index $cur $seg -}}
    {{- else -}}
      {{- $missing = true -}}
      {{- $cur = nil -}}
    {{- end -}}
  {{- end -}}
  {{- if or $missing (not $cur) -}}
    {{- if $missing -}}
      {{- fail (printf "profile %q locks %q but that value path does not exist under the merged values — the locked control is not active. Re-enable it or switch to a profile without this lock." $.Values.profile.name $path) -}}
    {{- else -}}
      {{- fail (printf "profile %q locks %q but it renders as false. Locked fields are enforced at render time: remove the override that disables it (--set %s=false) or switch to a less strict profile." $.Values.profile.name $path $path) -}}
    {{- end -}}
  {{- end -}}
  {{- /* Whole-component bypass: disabling the owning subchart's
         condition toggle leaves its values merged in the tree, so
         also pin the sibling <camelCase>.enabled toggle when the key
         exists (otel-collector -> otelCollector.enabled). */ -}}
  {{- $toggleKey := untitle (camelcase (index $segs 0)) -}}
  {{- if and (hasKey $.Values $toggleKey) (kindIs "map" (index $.Values $toggleKey)) (hasKey (index $.Values $toggleKey) "enabled") -}}
    {{- if not (dig "enabled" true (index $.Values $toggleKey)) -}}
      {{- fail (printf "profile %q locks %q but the owning component toggle %s.enabled renders as false, which turns the locked control off entirely. Re-enable it (--set %s.enabled=true) or switch to a less strict profile." $.Values.profile.name $path $toggleKey $toggleKey) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}

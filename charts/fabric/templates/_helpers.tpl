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

# update-agent subchart

Renders a `ValidatingAdmissionWebhook` that gates every
`create`/`update` into `fabric-system` (by default) behind a
three-step check:

1. **Signature** — Ed25519 over JCS-canonical JSON, keys pinned via
   `config.trustedKeys`.
2. **Version** — manifest's
   `fabric.singleaxis.dev/version-constraint` must admit the
   installed Fabric version.
3. **Schema** — JSON Schema for `(apiVersion, kind)` (ConfigMap +
   Secret built in).

Also renders an optional ArgoCD `AppProject` + `Application`
targeting the SingleAxis manifest channel, plus a PreSync hook Job
that runs `fabric-update-agent verify` over the channel before an
ArgoCD sync applies anything.

Component source:
[`components/update-agent`](../../../../components/update-agent).

## Minimal install (self-signed TLS)

```yaml
updateAgent:
  enabled: true
  config:
    trustedKeys:
      - id: singleaxis-release
        publicKey: "<base64 raw 32-byte Ed25519 pubkey>"
```

## Production install (cert-manager + tenant mirror)

```yaml
updateAgent:
  enabled: true
  config:
    fabricVersion: "0.1.0"
    failClosed: true
    trustedKeys:
      - id: singleaxis-release
        publicKey: "<base64 raw 32-byte Ed25519 pubkey>"
    extraTrustedKeys:
      - id: tenant-mirror
        publicKey: "<base64 raw 32-byte Ed25519 pubkey>"
  tls:
    mode: certManager
    certManager:
      issuerRef:
        name: fabric-ca-issuer
        kind: ClusterIssuer
        group: cert-manager.io
  webhook:
    watchedNamespaces:
      - fabric-system
      - fabric-policy
```

## TLS modes

| `tls.mode`    | Cert source                                  | caBundle wiring |
|---------------|----------------------------------------------|-----------------|
| `selfSigned`  | chart-rendered `Secret` (CA 1825d, leaf 365d) | copied into `ValidatingWebhookConfiguration` inline |
| `certManager` | `cert-manager.io/v1 Certificate`             | `cert-manager.io/inject-ca-from` annotation |

Use `selfSigned` for dev + airgapped tenants. Use `certManager` when
the cluster already has an issuer and you want rotation handled
externally. Swapping modes is safe on upgrade — both modes write to
the same Secret name.

In `selfSigned` mode the chart stamps the leaf's intended expiry into
the Secret (`fabric.singleaxis.dev/regenerate-after`) and regenerates
CA + leaf + caBundle together on the first `helm upgrade` after that
date. The Deployment checksum rolls the webhook pods onto the matching
leaf in the same upgrade. The server also watches projected TLS files
and exits for a Kubernetes restart when cert-manager rotates them
outside Helm. To rotate early (e.g. key compromise):

```bash
kubectl -n <ns> delete secret <release>-update-agent-tls
helm upgrade <release> <chart> ...   # reissues everything consistently
```

## Profile-lock admission backstop

Under a regulatory profile with locked fields (see
[docs/regulatory-profiles.md](../../../../docs/regulatory-profiles.md)),
the verifier can additionally deny ConfigMaps carrying the
otel-collector config naming whose collector config drops a locked
control or leaves it declared but absent from the active logs/traces
pipelines — catching direct `kubectl edit` drift that bypasses Helm.
Gated by `webhook.enforceProfileLocks: auto | on | off`; `auto`
engages when the release namespace carries the parent chart's
`singleaxis.com/profile=eu-ai-act-high-risk` label.
The shipped EU profile sets this to `on` explicitly so a first install
into a new namespace is protected before Helm can observe that label.

## Fail-closed vs fail-open

`config.failClosed: true` (default) denies any manifest in a
watched namespace that lacks both Fabric annotations. Drop to
`false` only when rolling out to a cluster with pre-existing
unsigned resources; flip back once the channel is authoritative.

## ArgoCD wiring

Off by default. Turn on with `argocd.enabled: true` to render:

- `AppProject` scoping source repos to the SingleAxis manifest
  channel and destinations to the fabric namespace.
- `Application` pointing at the manifest repo (`path: tenants/<id>`
  typical) with automated sync + self-heal.
- A PreSync hook Job (`argocd.presyncHook`) that clones the same
  repo/path/revision and runs `fabric-update-agent verify` over every
  YAML document — a deny aborts the sync before anything is applied.
  ArgoCD only honours hooks from the synced source, so the Job works
  as-is when ArgoCD manages this chart; for repo-type Applications,
  copy the rendered Job into the manifests repo.

## What it doesn't do

- No namespace admission — the webhook only gates resources *inside*
  the watched namespaces. Create the namespaces themselves via
  GitOps or the umbrella chart.
- No image-signing verification — that's a separate controller
  (e.g. Kyverno + cosign). This agent gates config/secret/manifest
  payloads, not image digests.
- No RBAC grants — the webhook runs as its own SA with zero cluster
  privileges; it doesn't read any in-cluster state.

## Security

- 2 replicas, `maxUnavailable: 0` (admission path must stay
  reachable to avoid `failurePolicy: Fail` blocking legitimate
  applies on rollout).
- non-root (`runAsUser: 1000`), read-only root filesystem,
  `capabilities.drop: [ALL]`, `seccompProfile: RuntimeDefault`.
- Serving cert + key mounted read-only from a Secret; `/tmp` is an
  `emptyDir` to preserve the read-only root.
- NetworkPolicy on by default (`networkPolicy.enabled: true`):
  ingress open on the webhook port only because the kube-apiserver's
  source IP cannot be pinned by NetworkPolicy across CNIs; egress is
  DNS-only.
- `AutomountServiceAccountToken: true` so the pod can answer the
  API server but the SA has no granted permissions.

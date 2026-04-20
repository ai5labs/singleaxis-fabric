# update-agent

Pre-apply verifier for SingleAxis Fabric's GitOps update channel
(spec [008](../../specs/008-deployment-model.md)).

Given a Kubernetes manifest delivered through ArgoCD (or Flux, or
any GitOps tool), this component answers **"should this be allowed
to apply?"** by running three checks:

1. **Signature** — Ed25519 over the canonical-JSON of the manifest,
   using a trust bundle of public keys pinned at install time.
2. **Version constraint** — the manifest's
   `fabric.singleaxis.dev/version-constraint` annotation must admit
   the installed Fabric version (PEP 440 specifier grammar).
3. **Schema** — the manifest validates against a
   `(apiVersion, kind)`-specific JSON Schema (ConfigMap + Secret out
   of the box; operator-extensible).

Two surfaces expose the same verification library:

- **`fabric-update-agent verify`** — one-shot CLI, wired up as an
  ArgoCD PreSync hook so a bad sync fails loud at sync time.
- **`fabric-update-agent serve`** — a
  [ValidatingAdmissionWebhook](https://kubernetes.io/docs/reference/access-authn-authz/extensible-admission-controllers/)
  HTTP server. K8s POSTs it every create/update against the
  `fabric-system` namespace before admission.

## Why Ed25519 over canonical JSON

Cosign-style keyless signing (sigstore + Fulcio + Rekor) would be
the fancier option; it's also a pile of transitive deps and a hard
requirement on outbound network from the webhook pod. Ed25519 with
JCS-style canonicalization gives us:

- pure-Python verification (no subprocess, no shell-out),
- a trust bundle that's a YAML file of base64 pubkeys,
- sub-millisecond verify (important for admission),
- and a canonicalization rule a tenant can re-implement in any
  language if they want to re-sign hand-edited manifests.

The signature annotation is stripped before canonicalizing, so the
signer doesn't sign their own signature.

## Trust bundle format

```yaml
fabric_version: "0.1.0"
fail_closed: true
trusted_keys:
  - id: singleaxis-release
    # Raw Ed25519 public key, base64-encoded (44 chars including
    # padding, decodes to exactly 32 bytes). NOT DER SPKI — if
    # openssl gave you something starting with "MCowBQYD..." you
    # need to strip the ASN.1 header first. See the "Generating a
    # key" section below.
    public_key: "rW8Yq7kcbIpYt8C0y/ELN0wbbMk2kl7YXgR5Qd7pGnM="
  - id: tenant-mirror
    public_key: "vXzKQpLq93bFa2wVrYEj0cJ8mIo5zQ3DXnKpRwC0y7A="
```

### Generating a key

```bash
# 1. Generate an Ed25519 keypair (OpenSSL 3+).
openssl genpkey -algorithm ed25519 -out signer.pem

# 2. Extract the raw 32-byte public key and base64-encode it.
#    The Python one-liner avoids having to parse DER by hand.
python - <<'PY'
import base64
from cryptography.hazmat.primitives.serialization import load_pem_private_key
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey
from cryptography.hazmat.primitives import serialization as s

with open("signer.pem", "rb") as f:
    key = load_pem_private_key(f.read(), password=None)
assert isinstance(key, Ed25519PrivateKey)
raw = key.public_key().public_bytes(
    encoding=s.Encoding.Raw, format=s.PublicFormat.Raw
)
print(base64.b64encode(raw).decode())
PY
```

- `fail_closed: true` (default): any manifest in a watched namespace
  without Fabric signature + version annotations is **denied**.
- `fail_closed: false`: unannotated manifests are allowed — useful
  when rolling out to a cluster with pre-existing resources.

## Manifest shape

Every Fabric-delivered manifest carries both annotations:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: fabric-policy
  annotations:
    fabric.singleaxis.dev/version-constraint: ">=0.1,<0.2"
    fabric.singleaxis.dev/signature: "singleaxis-release:<base64>"
data:
  bundle.yaml: |
    rules:
      - ...
```

The signature is computed over the JCS-canonical form of the whole
manifest with the signature annotation removed.

## CLI

```bash
# Verify one or many docs; exit 2 on deny.
fabric-update-agent verify path/to/manifest.yaml \
    --config /etc/fabric/update-agent/config.yaml

# Stdin is supported:
cat manifest.yaml | fabric-update-agent verify -
```

## Webhook server

```bash
fabric-update-agent serve \
    --host 0.0.0.0 \
    --port 8443 \
    --config /etc/fabric/update-agent/config.yaml \
    --tls-cert /etc/fabric/webhook-tls/tls.crt \
    --tls-key  /etc/fabric/webhook-tls/tls.key
```

The chart renders a `ValidatingWebhookConfiguration` pointing at
this service with `failurePolicy: Fail` (by default) scoped to
resources in the `fabric-system` namespace.

## Deployment

- Chart: [`charts/fabric/charts/update-agent`](../../charts/fabric/charts/update-agent)
- Image: `fabric/update-agent:<version>`

## Authoritative specs

- [`../../specs/008-deployment-model.md`](../../specs/008-deployment-model.md) §"The update channel (GitOps pull)"

## License

Apache-2.0.

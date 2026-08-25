# Install a pinned Fabric release

Production installations should select one released version, verify its
qualification evidence, and promote the same bytes through development,
staging, and production. Do not install `main`, a floating container tag, or
an unreviewed dependency range into a regulated environment.

This page covers the OSS artifacts. It does not imply that installing Fabric
alone satisfies a regulation or an organization's control framework.

## Supported release artifacts

| Surface | Distribution | Version authority | Integrity evidence |
| --- | --- | --- | --- |
| Python SDK | PyPI and release wheel | Python package metadata | Release qualification manifest and PyPI provenance |
| Public contracts | GitHub release `.tar.gz` | Release tag plus contract manifests | Exact-member qualification manifest and SHA-256 checksum |
| Helm deployment | GHCR OCI chart and release `.tgz` | `Chart.yaml` | Qualification manifest; keyless signature on the OCI artifact |
| Runtime images | GHCR OCI images | Release tag | Keyless signature and GitHub build provenance on the immutable digest |
| Source | GitHub release archive | Git tag | SHA-256, keyless blob signature, and GitHub provenance |

Python supports the interpreter range declared in the wheel's
`Requires-Python` metadata. The Helm chart's `kubeVersion` field is the
authoritative Kubernetes compatibility constraint. Verify both fields for the
selected release instead of relying on this page to repeat changing version
numbers.

## Before installation

1. Select an exact release such as `v1.2.3`.
2. Download `release-qualification.json` from that GitHub release.
3. Follow [Verify a Fabric release](verify-release.md).
4. Record the tag, Git commit SHA, artifact SHA-256, OCI digest, verifier
   identity, verification time, and approving change request.
5. Mirror approved artifacts into the organization's controlled registry or
   package repository when policy requires it.

For schema consumers, mirror
`singleaxis-fabric-contracts-VERSION.tar.gz` together with
`contracts-qualification.json` and `SHA256SUMS.contracts`. The archive is the
qualified distribution boundary for all contract families discovered under
`contracts/`; do not assemble a substitute archive from a source checkout and
assume it has the same digest.

## Python SDK

For an evaluation environment, download the exact wheel attached to the
GitHub release and compare its SHA-256 with `release-qualification.json`.
Install that local file without dependency resolution:

```bash
python -m venv .venv
. .venv/bin/activate
python -m pip install --no-deps ./singleaxis_fabric-1.2.3-py3-none-any.whl
python -m pip check
```

`--no-deps` is deliberate: enterprise deployments should resolve and approve
the SDK's dependency set separately, then install it from a locked internal
index. For a normal developer installation, pin the public package version:

```bash
python -m pip install "singleaxis-fabric==1.2.3"
```

Do not use an unbounded requirement in a production application.

## Helm deployment

Authenticate to the approved registry, pull one exact chart version, verify
the OCI signature, and retain the resolved digest before installation:

```bash
helm pull oci://ghcr.io/singleaxis/charts/fabric --version 1.2.3
cosign verify ghcr.io/singleaxis/charts/fabric:1.2.3 \
  --certificate-identity-regexp='^https://github.com/singleaxis/singleaxis-fabric/' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com'
helm upgrade --install fabric ./fabric-1.2.3.tgz \
  --namespace fabric-system --create-namespace \
  --values organization-reviewed-values.yaml \
  --wait --atomic
```

The release chart selects Fabric-owned images with the release version. Before
production promotion, resolve every image tag to an immutable digest, verify
its signature and provenance, and enforce those digests through the
organization's registry mirror or admission policy. The current chart values
do not expose a digest field for every component; tag-to-digest enforcement is
therefore an external deployment control rather than a chart guarantee.

## Promotion record

For each environment, retain:

- release tag and exact Git commit SHA;
- qualification manifest SHA-256;
- wheel, chart, source, and image digests used;
- signature and provenance verification output;
- reviewed configuration values and secret references;
- change approval, installer identity, and installation timestamp;
- post-install health and trace-delivery verification results;
- rollback version and rollback test evidence.

Rebuilds from source are different artifacts even when they use the same tag.
If reproducible-build equivalence has not been demonstrated, qualify the
rebuilt bytes as a separate internal release.

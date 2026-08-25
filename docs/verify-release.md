# Verify a Fabric release

Fabric's release workflow qualifies artifacts before publication and emits
`release-qualification.json`. Verification is fail-closed: absent evidence,
an unexpected workflow, a mismatched commit, a mismatched version, or an
artifact hash difference means the release is not approved for promotion.

## What the release gate proves

For the exact commit referenced by the tag, the gate requires successful
completed runs of:

- CI;
- Security;
- CodeQL;
- License compliance;
- E2E kind smoke.

The E2E workflow is manually dispatchable rather than triggered by every push
to `main`. Maintainers must dispatch it against the intended release commit
after merge and before creating the tag. A run on the pull request's synthetic
merge commit, a parent commit, or a different branch does not satisfy the
release gate.

The required workflow filenames live in
[`../scripts/release/release-policy.json`](../scripts/release/release-policy.json).
Release qualification also checks that:

- the tag has an accepted release-version form;
- Helm `version`, `appVersion`, and Fabric-owned subchart image versions agree;
- the packaged chart contains the expected release metadata;
- the Python wheel and source distribution have the expected name and version;
- required SDK files and the typed-package marker are present;
- every discovered public contract family/version has a manifest, every JSON
  contract member is pinned by SHA-256, and every manifest pin matches;
- archives contain no traversal paths, duplicate entries, links, or compiled
  Python cache files;
- the exact wheel can be installed offline without resolving dependencies;
- hashes are recorded for the artifacts that were inspected.

The publisher consumes the same qualified wheel, source distribution, chart,
TypeScript, and public-contract archives. It does not rebuild those artifacts
after qualification. Changelog validation also completes before qualification,
so absent release notes block every registry publisher.

Registry publishing is not a transaction across npm, PyPI, GHCR, and GitHub.
All publishers share the same pre-publication gate, but a registry outage can
still leave some qualified artifacts published while later jobs fail. Fabric
does not attempt cross-registry rollback and maintainers must not reuse a
published version for different bytes.

## Verify downloaded files

Download the qualification manifest and release artifacts into an empty
directory. Calculate hashes locally:

```bash
sha256sum singleaxis_fabric-*.whl singleaxis_fabric-*.tar.gz fabric-*.tgz \
  singleaxis-fabric-contracts-*.tar.gz
```

Compare each value with the corresponding `sha256` field in
`release-qualification.json`. Also confirm:

- `qualified` is `true`;
- `tag` is the release selected by the change record;
- `commit_sha` is the full SHA to which that tag resolves;
- all required workflow records contain that same SHA, `completed`, and
  `success`;
- the policy hash matches the policy at that tagged commit.

The source archive and SBOM files have a separate `SHA256SUMS` file in the
release bundle:

```bash
sha256sum --check SHA256SUMS
```

The public contract archive has independent, exact-member qualification
evidence. Verify its bytes, then compare the extracted inventory with the
ordered `archive.members` list in `contracts-qualification.json`:

```bash
sha256sum --check SHA256SUMS.contracts
tar -tzf singleaxis-fabric-contracts-1.2.3.tar.gz
```

Each member record includes its archive path, size, and SHA-256. Reject an
archive with missing, additional, reordered, linked, or path-unsafe members.

## Verify keyless signatures

Fabric release signatures use Sigstore keyless signing. Verify the certificate
issuer and constrain the identity to this repository's GitHub Actions
workflow. For the source archive:

```bash
cosign verify-blob singleaxis-fabric-1.2.3.tar.gz \
  --signature singleaxis-fabric-1.2.3.tar.gz.sig \
  --certificate singleaxis-fabric-1.2.3.tar.gz.pem \
  --certificate-identity-regexp='^https://github.com/singleaxis/singleaxis-fabric/' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com'
```

Verify OCI artifacts after resolving their digest:

```bash
cosign verify ghcr.io/singleaxis/fabric-otelcol@sha256:REPLACE_WITH_DIGEST \
  --certificate-identity-regexp='^https://github.com/singleaxis/singleaxis-fabric/' \
  --certificate-oidc-issuer='https://token.actions.githubusercontent.com'
```

Repeat that check for every enabled Fabric-owned image and for the chart OCI
artifact. Never treat a mutable `latest` tag as verification evidence.

## Verify GitHub build provenance

Where GitHub CLI attestation verification is permitted:

```bash
gh attestation verify singleaxis-fabric-1.2.3.tar.gz \
  --repo singleaxis/singleaxis-fabric
gh attestation verify \
  oci://ghcr.io/singleaxis/fabric-otelcol@sha256:REPLACE_WITH_DIGEST \
  --repo singleaxis/singleaxis-fabric
```

Archive the verifier output with the promotion record. Signature verification
establishes signer identity and artifact integrity; it does not establish that
the software is suitable for a particular regulated use case.

## Maintainer qualification command

The artifact checks run locally without a network after build dependencies are
available:

```bash
python scripts/release/qualify_release.py \
  --policy scripts/release/release-policy.json \
  --tag v1.2.3 \
  --chart-dir charts/fabric \
  --chart-package dist/fabric-1.2.3.tgz \
  --dist-dir sdk/python/dist \
  --smoke-install \
  --output dist/release-qualification.json

python scripts/release/package_contracts.py \
  --contracts-dir contracts \
  --policy scripts/release/release-policy.json \
  --version v1.2.3 \
  --output-dir dist/contracts \
  --output dist/contracts/contracts-qualification.json
```

Exact-SHA workflow evidence is queried only in the release workflow because it
requires authenticated GitHub access. The pure validation functions are
covered by offline unit tests.

## Failure response

Do not bypass a failed qualification job by publishing an artifact manually.
Correct the tagged commit, rerun every required workflow on the replacement
commit, create a new tag/version, and retain the failed run as audit evidence.

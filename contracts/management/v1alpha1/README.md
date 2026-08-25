# Fabric Management contract: `FabricDeployment` v1alpha1

`FabricDeployment` is the public, vendor-neutral desired-state envelope for one
agent deployment. It lets an operator describe identity, integration mode,
assurance posture, reference-only controls, observation posture, assurance
plan, and rollout approval in a single reviewable file.

This contract is intentionally local and declarative. It does **not** apply a
deployment, resolve a reference, fetch a secret, or require a SingleAxis
account. The v1alpha1 schema is strict: unknown fields fail validation, raw
content is not a supported content mode, and secrets, tokens, credentials, and
environment dumps cannot be expressed inline.

## Resource shape

```yaml
apiVersion: fabric.singleaxis.dev/v1alpha1
kind: FabricDeployment
metadata:
  name: payments-agent-prod
spec:
  assuranceLevel: A3
  connection:
    mode: gateway
    tenantIdFrom: tenant-identity
    workloadIdentityRef: payments-spiffe-binding-v2
  controls:
    profileRef: payments-agent-controls-v4
  observe:
    contentMode: hash-only
    relayRef: regulated-relay-eu-west
  assurance:
    planRef: payments-release-gate-v7
  rollout:
    approvalRef: change-1842
```

All `*Ref` and `*From` values are opaque identifiers. Their authorization,
integrity, type, existence, and effective configuration are controller
concerns. An operator may point them at customer-owned systems; no identifier
has product-specific resolution semantics in this contract.

## Assurance requirements

- A0 requires identity, connection, and an Observe content mode.
- A1 additionally requires `observe.relayRef`.
- A2 and A3 additionally require `controls.profileRef`,
  `assurance.planRef`, and `rollout.approvalRef`.
- A3 additionally requires `connection.workloadIdentityRef`; local validation
  proves only that the opaque reference is present, not that it is resolved,
  authorized, or bound at ingress.

These structural requirements do not certify the referenced resources or make
a compliance claim. A future Site Controller must verify signatures and
approval authority, resolve references, enforce segregation of duties, render
component configuration, reconcile rollout, publish status, and record the
effective digest. Those apply/reconcile semantics are explicitly outside
v1alpha1 and outside the current `fabricctl` implementation.

## Local operator workflow

```bash
fabricctl deployment validate deployment.yaml
fabricctl deployment digest deployment.yaml
fabricctl deployment plan deployment.yaml
```

Both commands are offline. Validation fails closed with stable diagnostic IDs.
Digest first validates, then hashes canonical UTF-8 JSON of the complete input
document. No declared field is defaulted, resolved, or excluded. This makes a
digest a precise review identity, not proof that referenced configuration was
approved or deployed.

`deployment plan` validates the same document and produces a deterministic,
offline installation/readiness plan. It names only the OSS roles selected by
the resource (connection artifact, Collector, and any selected Control, Relay,
or Assurance Runner), lists opaque references without resolving them, and
emits operator prerequisites with stable IDs. A plan is not a readiness
attestation: prerequisites have status `required`, not `verified`.

Planning does not read environment variables or secret stores, contact a
network, cluster, or platform, render Helm, verify an approval, or apply or
reconcile desired state. It does not add Governance, a Site Controller, or a
SingleAxis destination unless a future contract explicitly selects such a
component. YAML anchors and aliases are rejected; this small desired-state
contract does not need them, and rejecting them prevents alias-expansion
attacks before object composition.

The digest-pinned [`manifest.json`](manifest.json) is the contract index.
Consumers must verify it before using the schema or fixtures in qualification.

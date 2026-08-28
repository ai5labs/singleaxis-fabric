# Fabric lifecycle contracts v1

This contract family is the public boundary shared by `fabricctl`, a
customer-hosted Site Controller, GitOps automation, the SingleAxis Platform,
and approved third-party management providers. It prevents each frontend from
reimplementing installation identity, artifact pinning, approvals, receipts,
status, or pairing semantics.

The contracts separate four authorities:

1. `FabricDeployment` and `FabricInstallTarget` remain reviewed desired state
   in `contracts/management/v1alpha1`.
2. `fabricctl.image-locks/v1` resolves a released component set into immutable
   container digests after desired state is approved.
3. `fabricctl.operation-plan/v1` binds the bundle, target, cluster identity,
   exact artifacts, effects, operation, and approval posture.
4. Detached approval and receipt artifacts authorize and record a mutation;
   they never become part of deterministic desired state.

Management pairing is optional. Its public request carries only digests,
target identity, and a workload-identity reference. Device codes and grants
are ephemeral bearer material: the Go API marks them non-serializable, and no
receipt or support artifact may contain them. Management availability is never
in the agent request path.

An operation plan with `readiness: draft` has not resolved an immutable cluster
identity and cannot be applied. `readiness: mutation-ready` requires
`target.cluster_uid`. A2/A3 plans require a trusted Ed25519 approval envelope;
an interactive approval is available only to lower-assurance terminal flows.

An install receipt records what was attempted and hash-chains its own contents.
It is evidence, not proof of legal compliance. Kubernetes status can compare
the current Helm manifest digest with a verified receipt, but telemetry
delivery remains `unverified` until a destination-specific acknowledgement
adapter confirms receipt and persistence.

Runtime verification is intentionally layered. Cluster identity, workload
readiness, receipt-bound manifest state, Collector ingress acceptance, Relay
delivery, and destination persistence are independent checks. A successful
OTLP HTTP response at Collector ingress cannot promote the overall result to
`verified` when downstream acknowledgement is unavailable.

The pairing request, signed connection receipt, runtime verification, and
support manifest schemas make the optional management and operational
boundaries portable. Pairing never grants the platform authority over the
agent request path. Support generation is local, allowlisted, and has
`uploaded: false` as an invariant.

Private products consume these contracts. They do not add fields to, fork, or
silently weaken them. Proprietary policy packs, regulatory mappings, engines,
and workflows remain outside this public contract family.

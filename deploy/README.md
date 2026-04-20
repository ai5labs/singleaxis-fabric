# deploy/

Reference Infrastructure-as-Code for the non-Kubernetes
prerequisites Fabric requires (Vault, KMS, networking, workload
identity).

These are **reference** modules — tenants using their own IaC
patterns are welcome to replicate the equivalent. Fabric does not
require these modules; it requires the outcomes they produce.

## Authoritative spec

[`../specs/008-deployment-model.md`](../specs/008-deployment-model.md)

## Status

Pre-alpha — scaffold only.

## Planned layout

```
deploy/
├── terraform/
│   ├── aws/
│   │   └── eks-fabric-prerequisites/
│   ├── gcp/
│   │   └── gke-fabric-prerequisites/
│   └── azure/
│       └── aks-fabric-prerequisites/
└── crossplane/
    └── compositions/
```

## What the modules provide

- VPC endpoints / PrivateLink for LLM, object storage, secret
  manager (so egress does not traverse public internet)
- KMS key and IAM/workload-identity bindings for the `fabric-
  system` service accounts
- Either a Vault cluster deployment or Secret Manager + Workload
  Identity configuration
- Object storage bucket for backups, dry-run sink, content store
- Network ACLs permitting egress only to `ingest-<region>.singleaxis.com`

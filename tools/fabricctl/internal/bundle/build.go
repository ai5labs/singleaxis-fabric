// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
)

var (
	generatorVersionPattern = regexp.MustCompile(`^v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)
	generatorCommitPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

type resourceIdentity struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

type planOperation struct {
	Network  bool `json:"network"`
	Mutating bool `json:"mutating"`
}

type planAction struct {
	Order  int    `json:"order"`
	ID     string `json:"id"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

type planPrerequisite struct {
	Order       int    `json:"order"`
	ID          string `json:"id"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

type installationPlan struct {
	SchemaVersion         string                     `json:"schema_version"`
	Status                string                     `json:"status"`
	Readiness             string                     `json:"readiness"`
	Operation             planOperation              `json:"operation"`
	DeploymentObligations deployment.Plan            `json:"deployment_obligations"`
	Deployment            resourceIdentity           `json:"deployment"`
	Target                resourceIdentity           `json:"target"`
	Distribution          installtarget.Distribution `json:"distribution"`
	Profile               installtarget.Profile      `json:"profile"`
	Backend               installtarget.Backend      `json:"backend"`
	Actions               []planAction               `json:"actions"`
	Prerequisites         []planPrerequisite         `json:"prerequisites"`
}

type manifestArtifact struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type manifest struct {
	SchemaVersion string             `json:"schema_version"`
	SelfExclusion string             `json:"self_exclusion"`
	Generator     Generator          `json:"generator"`
	Files         []manifestArtifact `json:"files"`
	BundleDigest  string             `json:"bundle_digest"`
}

// Build validates the cross-resource binding and produces six deterministic
// artifacts. Success means internal consistency only; readiness is unverified.
func Build(deploymentResource deployment.Resource, target installtarget.Resource, generator Generator) (Bundle, error) {
	if generator.Name != "fabricctl" || !generatorVersionPattern.MatchString(generator.Version) || !generatorCommitPattern.MatchString(generator.Commit) {
		return Bundle{}, fmt.Errorf("complete released or explicit development generator identity is required")
	}
	if diagnostics := installtarget.ValidateAgainstDeployment(target, deploymentResource); len(diagnostics) != 0 {
		return Bundle{}, fmt.Errorf("install target is incompatible with deployment: %s at %s", diagnostics[0].Summary, diagnostics[0].Path)
	}

	deploymentDigest, err := deployment.DigestResource(deploymentResource)
	if err != nil {
		return Bundle{}, fmt.Errorf("digest deployment: %w", err)
	}
	targetDigest, err := installtarget.Digest(target)
	if err != nil {
		return Bundle{}, fmt.Errorf("digest install target: %w", err)
	}

	deploymentPayload, err := renderDeployment(deploymentResource)
	if err != nil {
		return Bundle{}, err
	}
	targetPayload, err := renderInstallTarget(target)
	if err != nil {
		return Bundle{}, err
	}
	valuesPayload, err := renderValues(deploymentResource, target)
	if err != nil {
		return Bundle{}, err
	}
	secretsPayload, err := renderSecretRequirements(target)
	if err != nil {
		return Bundle{}, err
	}
	planPayload, err := renderJSON(buildPlan(deploymentResource, deploymentDigest, target, targetDigest))
	if err != nil {
		return Bundle{}, fmt.Errorf("render installation plan: %w", err)
	}

	artifacts := []Artifact{
		{Path: DeploymentFileName, Payload: deploymentPayload},
		{Path: InstallTargetFileName, Payload: targetPayload},
		{Path: ValuesFileName, Payload: valuesPayload},
		{Path: SecretsRequiredFileName, Payload: secretsPayload},
		{Path: InstallationPlanFileName, Payload: planPayload},
	}
	entries := make([]manifestArtifact, 0, len(artifacts))
	for _, artifact := range artifacts {
		entries = append(entries, manifestArtifact{Path: artifact.Path, SHA256: digestHex(artifact.Payload)})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	bundleDigest, err := digestManifestEntries(entries)
	if err != nil {
		return Bundle{}, fmt.Errorf("digest bundle manifest entries: %w", err)
	}
	manifestPayload, err := renderJSON(manifest{
		SchemaVersion: "fabricctl.bundle-manifest/v1",
		SelfExclusion: ManifestFileName,
		Generator:     generator,
		Files:         entries,
		BundleDigest:  bundleDigest,
	})
	if err != nil {
		return Bundle{}, fmt.Errorf("render bundle manifest: %w", err)
	}
	artifacts = append(artifacts, Artifact{Path: ManifestFileName, Payload: manifestPayload})
	return Bundle{Artifacts: artifacts, BundleDigest: bundleDigest}, nil
}

func buildPlan(deploymentResource deployment.Resource, deploymentDigest string, target installtarget.Resource, targetDigest string) installationPlan {
	prerequisites := []planPrerequisite{
		{Order: 1, ID: "installation.prerequisite.chart_verified", Status: "required", Description: "Verify the pinned chart digest before rendering or installation"},
		{Order: 2, ID: "installation.prerequisite.profile_verified", Status: "required", Description: "Verify the selected profile bytes against the pinned digest"},
		{Order: 3, ID: "installation.prerequisite.target_identity", Status: "required", Description: "Resolve the Kubernetes context to an immutable cluster identity before any mutation"},
		{Order: 4, ID: "installation.prerequisite.authorization", Status: "required", Description: "Verify namespace and installation authorization using the deployment actor identity"},
	}
	if target.Spec.Profile.Name == installtarget.ProfilePermissiveDev {
		prerequisites = append(prerequisites, planPrerequisite{
			Order: 5, ID: "installation.prerequisite.dev_non_durable", Status: "required",
			Description: "Accept that permissive-dev writes telemetry to pod stdout and is not a durable audit trail",
		})
	} else {
		prerequisites = append(prerequisites,
			planPrerequisite{Order: 5, ID: "installation.prerequisite.secrets", Status: "required", Description: "Provision every declared Secret before installation without placing values in the bundle"},
			planPrerequisite{Order: 6, ID: "installation.prerequisite.policy", Status: "required", Description: "Provision the approved telemetry egress policy ConfigMap"},
			planPrerequisite{Order: 7, ID: "installation.prerequisite.rails", Status: "required", Description: "Provision the approved runtime rails ConfigMap"},
			planPrerequisite{Order: 8, ID: "installation.prerequisite.cert_manager", Status: "required", Description: "Verify cert-manager and the approved ClusterIssuer are available"},
			planPrerequisite{Order: 9, ID: "installation.prerequisite.egress", Status: "required", Description: "Verify that declared CIDRs and ports identify the approved telemetry destination"},
		)
	}
	return installationPlan{
		SchemaVersion:         "fabricctl.installation-plan/v1",
		Status:                "pass",
		Readiness:             "unverified",
		Operation:             planOperation{Network: false, Mutating: false},
		DeploymentObligations: deployment.BuildPlan(deploymentResource),
		Deployment:            resourceIdentity{Name: deploymentResource.Metadata.Name, Digest: deploymentDigest},
		Target:                resourceIdentity{Name: target.Metadata.Name, Digest: targetDigest},
		Distribution:          target.Spec.Distribution,
		Profile:               target.Spec.Profile,
		Backend:               target.Spec.Backend,
		Actions: []planAction{
			{Order: 1, ID: "installation.action.validate", Type: "validate", Target: "offline-bundle"},
			{Order: 2, ID: "installation.action.render", Type: "render", Target: "pinned-helm-chart"},
			{Order: 3, ID: "installation.action.verify", Type: "verify", Target: "unverified-prerequisites"},
		},
		Prerequisites: prerequisites,
	}
}

func digestBytes(payload []byte) string {
	return "sha256:" + digestHex(payload)
}

func digestHex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func digestManifestEntries(entries []manifestArtifact) (string, error) {
	ordered := append([]manifestArtifact(nil), entries...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Path < ordered[j].Path })
	var identity bytes.Buffer
	for _, entry := range ordered {
		if entry.Path == "" || len(entry.SHA256) != 64 {
			return "", fmt.Errorf("manifest entry is incomplete")
		}
		identity.WriteString(entry.Path)
		identity.WriteByte(0)
		identity.WriteString(entry.SHA256)
		identity.WriteByte('\n')
	}
	return digestBytes(identity.Bytes()), nil
}

func renderJSON(value any) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(payload, '\n'), nil
}

// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

// Package bundle builds deterministic, non-mutating Fabric installation
// bundles. It performs no network, cluster, registry, or secret-store access.
package bundle

import "fmt"

const (
	DeploymentFileName       = "singleaxis.yaml"
	InstallTargetFileName    = "install-target.yaml"
	ValuesFileName           = "fabric-values.yaml"
	SecretsRequiredFileName  = "secrets-required.yaml"
	InstallationPlanFileName = "installation-plan.json"
	ManifestFileName         = "bundle-manifest.json"
)

// Generator identifies the binary that produced a bundle. Build pipelines
// should inject immutable release identity. It deliberately contains no time.
type Generator struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Commit  string `json:"commit"`
}

// Artifact contains the exact bytes written for one allowlisted bundle path.
type Artifact struct {
	Path    string `json:"path"`
	Payload []byte `json:"-"`
}

// Bundle is ordered so writes, manifests, and reviews are reproducible.
type Bundle struct {
	Artifacts    []Artifact
	BundleDigest string
}

// Payload returns a defensive copy of one named artifact.
func (b Bundle) Payload(path string) ([]byte, error) {
	for _, artifact := range b.Artifacts {
		if artifact.Path == path {
			return append([]byte(nil), artifact.Payload...), nil
		}
	}
	return nil, fmt.Errorf("bundle artifact is not present")
}

// Paths returns the fixed artifact order.
func (b Bundle) Paths() []string {
	paths := make([]string, 0, len(b.Artifacts))
	for _, artifact := range b.Artifacts {
		paths = append(paths, artifact.Path)
	}
	return paths
}

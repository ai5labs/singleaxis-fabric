// Copyright 2026 AI5Labs Research OPC Private Limited
// SPDX-License-Identifier: Apache-2.0

package bundle

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/deployment"
	"github.com/singleaxis/singleaxis-fabric/tools/fabricctl/internal/installtarget"
	"gopkg.in/yaml.v3"
)

type yamlDeployment struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   yamlMetadata       `yaml:"metadata"`
	Spec       yamlDeploymentSpec `yaml:"spec"`
}

type yamlMetadata struct {
	Name string `yaml:"name"`
}

type yamlDeploymentSpec struct {
	AssuranceLevel string                `yaml:"assuranceLevel"`
	Connection     deployment.Connection `yaml:"connection"`
	Controls       *deployment.Controls  `yaml:"controls,omitempty"`
	Observe        deployment.Observe    `yaml:"observe"`
	Assurance      *deployment.Assurance `yaml:"assurance,omitempty"`
	Rollout        *deployment.Rollout   `yaml:"rollout,omitempty"`
}

type yamlTarget struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   yamlMetadata   `yaml:"metadata"`
	Spec       yamlTargetSpec `yaml:"spec"`
}

type yamlTargetSpec struct {
	DeploymentRef installtarget.DeploymentRef `yaml:"deploymentRef"`
	Distribution  installtarget.Distribution  `yaml:"distribution"`
	Profile       installtarget.Profile       `yaml:"profile"`
	Backend       installtarget.Backend       `yaml:"backend"`
	Bindings      *installtarget.Bindings     `yaml:"bindings,omitempty"`
}

func renderDeployment(resource deployment.Resource) ([]byte, error) {
	payload, err := yaml.Marshal(yamlDeployment{
		APIVersion: resource.APIVersion,
		Kind:       resource.Kind,
		Metadata:   yamlMetadata{Name: resource.Metadata.Name},
		Spec: yamlDeploymentSpec{
			AssuranceLevel: resource.Spec.AssuranceLevel,
			Connection:     resource.Spec.Connection,
			Controls:       resource.Spec.Controls,
			Observe:        resource.Spec.Observe,
			Assurance:      resource.Spec.Assurance,
			Rollout:        resource.Spec.Rollout,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render canonical deployment: %w", err)
	}
	return payload, nil
}

func renderInstallTarget(target installtarget.Resource) ([]byte, error) {
	payload, err := yaml.Marshal(yamlTarget{
		APIVersion: target.APIVersion,
		Kind:       target.Kind,
		Metadata:   yamlMetadata{Name: target.Metadata.Name},
		Spec: yamlTargetSpec{
			DeploymentRef: target.Spec.DeploymentRef,
			Distribution:  target.Spec.Distribution,
			Profile:       target.Spec.Profile,
			Backend:       target.Spec.Backend,
			Bindings:      target.Spec.Bindings,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("render canonical install target: %w", err)
	}
	return payload, nil
}

type valuesDocument struct {
	Tenant        valuesTenant         `yaml:"tenant"`
	Namespace     valuesNamespace      `yaml:"namespace"`
	Profile       valuesProfile        `yaml:"profile"`
	OTelCollector *valuesOTelCollector `yaml:"otel-collector,omitempty"`
	NemoSidecar   *valuesNemoSidecar   `yaml:"nemo-sidecar,omitempty"`
	UpdateAgent   *valuesUpdateAgent   `yaml:"update-agent,omitempty"`
}

type valuesTenant struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name"`
}

type valuesNamespace struct {
	Create bool   `yaml:"create"`
	Name   string `yaml:"name"`
}

type valuesProfile struct {
	Name string `yaml:"name"`
}

type valuesOTelCollector struct {
	Receiver      valuesReceiver      `yaml:"receiver"`
	Exporter      valuesExporter      `yaml:"exporter"`
	NetworkPolicy valuesNetworkPolicy `yaml:"networkPolicy"`
	Fabric        valuesFabric        `yaml:"fabric"`
}

type valuesReceiver struct {
	TLS valuesReceiverTLS `yaml:"tls"`
}
type valuesReceiverTLS struct {
	ServerCertificateSecret valuesNamedSecret `yaml:"serverCertificateSecret"`
	ClientCASecret          valuesNamedSecret `yaml:"clientCASecret"`
}
type valuesNamedSecret struct {
	Name    string `yaml:"name"`
	CertKey string `yaml:"certKey,omitempty"`
	KeyKey  string `yaml:"keyKey,omitempty"`
	Key     string `yaml:"key,omitempty"`
}
type valuesExporter struct {
	Endpoint string             `yaml:"endpoint"`
	Auth     valuesExporterAuth `yaml:"auth"`
}
type valuesExporterAuth struct {
	Secret valuesNameKey `yaml:"secret"`
}
type valuesNameKey struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}
type valuesNetworkPolicy struct {
	ExporterEgress valuesExporterEgress `yaml:"exporterEgress"`
}
type valuesExporterEgress struct {
	To    []valuesNetworkPeer  `yaml:"to"`
	Ports []installtarget.Port `yaml:"ports"`
}
type valuesNetworkPeer struct {
	IPBlock valuesIPBlock `yaml:"ipBlock"`
}
type valuesIPBlock struct {
	CIDR string `yaml:"cidr"`
}
type valuesFabric struct {
	Policy  valuesPolicy  `yaml:"policy"`
	Redact  valuesRedact  `yaml:"redact"`
	Sampler valuesSampler `yaml:"sampler"`
}
type valuesPolicy struct {
	BundleConfigMap string                `yaml:"bundleConfigMap"`
	ReferencePolicy valuesReferencePolicy `yaml:"referencePolicy"`
}
type valuesReferencePolicy struct {
	Enabled bool `yaml:"enabled"`
}
type valuesRedact struct {
	Embedded valuesEmbeddedRedact `yaml:"embedded"`
}
type valuesEmbeddedRedact struct {
	TenantKeySecret valuesNameKey `yaml:"tenantKeySecret"`
}
type valuesSampler struct {
	HMACKeySecret valuesNameKey `yaml:"hmacKeySecret"`
}
type valuesNemoSidecar struct {
	RailsConfigMap valuesRailsConfigMap `yaml:"railsConfigMap"`
}
type valuesRailsConfigMap struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
}
type valuesUpdateAgent struct {
	Config valuesUpdateConfig `yaml:"config"`
}
type valuesUpdateConfig struct {
	TrustedKeys []valuesTrustedKey `yaml:"trustedKeys"`
}
type valuesTrustedKey struct {
	ID        string `yaml:"id"`
	PublicKey string `yaml:"publicKey"`
}

func renderValues(resource deployment.Resource, target installtarget.Resource) ([]byte, error) {
	document := valuesDocument{
		Tenant:    valuesTenant{Name: resource.Metadata.Name},
		Namespace: valuesNamespace{Create: target.Spec.Backend.Helm.CreateNamespace, Name: target.Spec.Backend.Helm.Namespace},
		Profile:   valuesProfile{Name: target.Spec.Profile.Name},
	}
	if target.Spec.Profile.Name == installtarget.ProfileHighRisk {
		bindings := target.Spec.Bindings
		if bindings == nil {
			return nil, fmt.Errorf("render high-risk values: complete bindings are required")
		}
		publicKey, err := chartPublicKey(bindings.UpdateTrust.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("render high-risk values: invalid update trust public key")
		}
		peers := make([]valuesNetworkPeer, 0, len(bindings.Exporter.Egress.CIDRs))
		for _, cidr := range bindings.Exporter.Egress.CIDRs {
			peers = append(peers, valuesNetworkPeer{IPBlock: valuesIPBlock{CIDR: cidr}})
		}
		document.Tenant.ID = bindings.TenantID
		document.OTelCollector = &valuesOTelCollector{
			Receiver: valuesReceiver{TLS: valuesReceiverTLS{
				ServerCertificateSecret: valuesNamedSecret{Name: "fabric-otel-receiver-tls", CertKey: "tls.crt", KeyKey: "tls.key"},
				ClientCASecret:          valuesNamedSecret{Name: "fabric-otel-client-ca", Key: "ca.crt"},
			}},
			Exporter: valuesExporter{Endpoint: bindings.Exporter.Endpoint,
				Auth: valuesExporterAuth{Secret: valuesNameKey{Name: "fabric-otel-export-auth", Key: "authorization"}}},
			NetworkPolicy: valuesNetworkPolicy{ExporterEgress: valuesExporterEgress{To: peers, Ports: bindings.Exporter.Egress.Ports}},
			Fabric: valuesFabric{
				Policy:  valuesPolicy{BundleConfigMap: "fabric-high-risk-egress-policy", ReferencePolicy: valuesReferencePolicy{Enabled: false}},
				Redact:  valuesRedact{Embedded: valuesEmbeddedRedact{TenantKeySecret: valuesNameKey{Name: "fabric-presidio-tenant-key", Key: "tenant.key"}}},
				Sampler: valuesSampler{HMACKeySecret: valuesNameKey{Name: "fabric-otel-sampler-key", Key: "hmac_key"}},
			},
		}
		document.NemoSidecar = &valuesNemoSidecar{RailsConfigMap: valuesRailsConfigMap{Name: "fabric-high-risk-rails", MountPath: "/etc/fabric/rails"}}
		document.UpdateAgent = &valuesUpdateAgent{Config: valuesUpdateConfig{TrustedKeys: []valuesTrustedKey{{ID: bindings.UpdateTrust.KeyID, PublicKey: publicKey}}}}
	}
	payload, err := yaml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("render Helm values: %w", err)
	}
	return payload, nil
}

func chartPublicKey(value string) (string, error) {
	encoded, ok := strings.CutPrefix(value, "ed25519:")
	if !ok {
		return "", fmt.Errorf("missing ed25519 prefix")
	}
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("invalid Ed25519 key")
	}
	return base64.StdEncoding.EncodeToString(decoded), nil
}

type secretRequirementsDocument struct {
	APIVersion   string              `yaml:"apiVersion"`
	Kind         string              `yaml:"kind"`
	Metadata     yamlMetadata        `yaml:"metadata"`
	Status       string              `yaml:"status"`
	Requirements []secretRequirement `yaml:"requirements"`
}
type secretRequirement struct {
	Name      string   `yaml:"name"`
	Namespace string   `yaml:"namespace"`
	Keys      []string `yaml:"keys"`
	Purpose   string   `yaml:"purpose"`
	Consumer  string   `yaml:"consumer"`
}

func renderSecretRequirements(target installtarget.Resource) ([]byte, error) {
	requirements := make([]secretRequirement, 0)
	if target.Spec.Profile.Name == installtarget.ProfileHighRisk {
		namespace := target.Spec.Backend.Helm.Namespace
		requirements = []secretRequirement{
			{Name: "fabric-otel-receiver-tls", Namespace: namespace, Keys: []string{"tls.crt", "tls.key"}, Purpose: "otlp-receiver-server-identity", Consumer: "otel-collector"},
			{Name: "fabric-otel-client-ca", Namespace: namespace, Keys: []string{"ca.crt"}, Purpose: "otlp-client-certificate-verification", Consumer: "otel-collector"},
			{Name: "fabric-otel-export-auth", Namespace: namespace, Keys: []string{"authorization"}, Purpose: "authenticated-telemetry-export", Consumer: "otel-collector"},
			{Name: "fabric-otel-sampler-key", Namespace: namespace, Keys: []string{"hmac_key"}, Purpose: "deterministic-telemetry-sampling", Consumer: "otel-collector"},
			{Name: "fabric-presidio-tenant-key", Namespace: namespace, Keys: []string{"tenant.key"}, Purpose: "tenant-scoped-telemetry-pseudonymization", Consumer: "otel-collector/presidio"},
		}
	}
	payload, err := yaml.Marshal(secretRequirementsDocument{
		APIVersion:   installtarget.APIVersion,
		Kind:         "FabricSecretRequirements",
		Metadata:     yamlMetadata{Name: target.Metadata.Name},
		Status:       "unresolved",
		Requirements: requirements,
	})
	if err != nil {
		return nil, fmt.Errorf("render secret requirements: %w", err)
	}
	return payload, nil
}

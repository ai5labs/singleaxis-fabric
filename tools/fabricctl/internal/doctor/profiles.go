package doctor

type objectRequirement struct {
	Kind string
	Name string
	Code string
	Why  string
}

type profile struct {
	Name             string
	EndpointRequired bool
	HTTPSRequired    bool
}

var profiles = map[string]profile{
	"unprofiled": {
		Name: "unprofiled",
	},
	"permissive-dev": {
		Name: "permissive-dev",
	},
	"eu-ai-act-high-risk": {
		Name:             "eu-ai-act-high-risk",
		EndpointRequired: true,
		HTTPSRequired:    true,
	},
}

func profileByName(name string) (profile, bool) {
	p, ok := profiles[name]
	return p, ok
}

func requirementsFor(p profile, names RequirementNames) []objectRequirement {
	if p.Name != "eu-ai-act-high-risk" {
		return nil
	}
	if names.PresidioKeySecret == "" {
		names.PresidioKeySecret = "fabric-presidio-tenant-key"
	}
	if names.SamplerKeySecret == "" {
		names.SamplerKeySecret = "fabric-otel-sampler-key"
	}
	if names.PolicyConfigMap == "" {
		names.PolicyConfigMap = "fabric-high-risk-egress-policy"
	}
	if names.RailsConfigMap == "" {
		names.RailsConfigMap = "fabric-high-risk-rails"
	}
	return []objectRequirement{
		{Kind: "secret", Name: names.PresidioKeySecret, Code: "PROFILE-SECRET-PRESIDIO-001", Why: "tenant-scoped HMAC redaction key"},
		{Kind: "secret", Name: names.SamplerKeySecret, Code: "PROFILE-SECRET-SAMPLER-001", Why: "deterministic sampling HMAC key"},
		{Kind: "configmap", Name: names.PolicyConfigMap, Code: "PROFILE-CONFIGMAP-POLICY-001", Why: "customer-owned egress policy bundle"},
		{Kind: "configmap", Name: names.RailsConfigMap, Code: "PROFILE-CONFIGMAP-RAILS-001", Why: "approved versioned rails bundle"},
	}
}

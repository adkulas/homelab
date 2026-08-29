package engine

import "github.com/adkulas/homelab/internal/contractmodel"

func ProfilarrConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Declared("policy.pcdRevision", "Pinned PCD repository revision", "versions.yaml#policy.profilarrPcdRevision", "40-character Git revision", nil, "Update policy.profilarrPcdRevision and both policy fixtures in one reviewed change.", "synchronize", "verify"),
		contractmodel.Fixed("connection.radarr", "Guided Radarr connection", "http://radarr:7878", "The operator creates and tests the Environment-specific Radarr connection through Profilarr's supported UI.", "initialize", "verify"),
		contractmodel.Fixed("connection.sonarr", "Guided Sonarr connection", "http://sonarr:8989", "The operator creates and tests the Environment-specific Sonarr connection through Profilarr's supported UI.", "initialize", "verify"),
		contractmodel.Fixed("ownedPolicyDomains", "Owned policy domains", "ADR 0002", "Profilarr owns Arr quality profiles, custom formats, quality definitions, naming, upgrades, and media management.", "synchronize", "verify"),
	}
}

func ArrPolicyRevisionContract() contractmodel.Setting {
	return contractmodel.Declared("policy.pcdRevision", "Pinned Profilarr policy revision", "versions.yaml#policy.profilarrPcdRevision", "40-character Git revision", nil, "Update the pinned revision and matching service policy fixture in one reviewed change.", "synchronize", "verify")
}

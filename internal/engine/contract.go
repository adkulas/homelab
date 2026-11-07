package engine

import "github.com/adkulas/homelab/internal/contractmodel"

func ProfilarrConfigurationContract() []contractmodel.Setting {
	return []contractmodel.Setting{
		contractmodel.Declared("policy.pcdRevision", "Pinned PCD repository revision", "versions.yaml#policy.profilarrPcdRevision", "40-character Git revision", nil, "Update policy.profilarrPcdRevision and both policy fixtures in one reviewed change.", "synchronize", "verify"),
		contractmodel.Fixed("connection.radarr", "Radarr connection", "http://radarr:7878", "Fresh supported state is bootstrapped once under ADR 0009; existing state is observed through Profilarr's public API.", "initialize", "verify"),
		contractmodel.Fixed("connection.sonarr", "Sonarr connection", "http://sonarr:8989", "Fresh supported state is bootstrapped once under ADR 0009; existing state is observed through Profilarr's public API.", "initialize", "verify"),
		contractmodel.Fixed("bootstrap.compatibility", "Fresh-state compatibility", "Profilarr v2.3.1 exact digest, migrations, and schema", "ADR 0009 gates one-time initialization; partial or unsupported state requires guided recovery.", "initialize", "verify"),
		contractmodel.Fixed("policy.schedule", "Policy synchronization schedule", "daily at 04:00 in the Environment timezone", "Profilarr owns recurring Arr synchronization from the pinned PCD checkout; automatic upstream pulls are disabled.", "initialize", "synchronize", "verify"),
		contractmodel.Fixed("policy.delay", "Download delay", "PCD Default; zero minutes", "Profilarr synchronizes the upstream Default delay profile to both Arr services.", "initialize", "synchronize", "verify"),
		contractmodel.Fixed("ownedPolicyDomains", "Owned policy domains", "ADR 0002", "Profilarr owns Arr quality profiles, custom formats, quality definitions, naming, upgrades, and media management.", "synchronize", "verify"),
	}
}

func ArrPolicyRevisionContract() contractmodel.Setting {
	return contractmodel.Declared("policy.pcdRevision", "Pinned Profilarr policy revision", "versions.yaml#policy.profilarrPcdRevision", "40-character Git revision", nil, "Update the pinned revision and matching service policy fixture in one reviewed change.", "synchronize", "verify")
}

# Operable Homelab Resources

## Knowledge

- [OpenGitOps Principles](https://opengitops.dev/)
  Defines declarative, versioned desired state and continuous reconciliation. Use for: judging how close this repository is to reproducible operation without assuming a Kubernetes implementation.
- [SOPS reference](https://getsops.io/docs/reference/)
  Primary documentation for tree-aware encryption, creation rules, selective encryption, integrity protection, and key groups. Use for: designing readable encrypted YAML and recovery access.
- [Docker Compose application model](https://docs.docker.com/compose/intro/compose-application-model/)
  Explains services, networks, volumes, configs, secrets, and composition. Use for: deciding which topology belongs in the reusable stack model.
- [Docker Compose multiple-file guidance](https://docs.docker.com/compose/how-tos/multiple-compose-files/)
  Documents overrides, includes, merge behavior, and the complexity tradeoff. Use for: evaluating overlays only when the repository's typed renderer no longer provides a clearer seam.
- [NIST Risk Management Framework: Categorize](https://csrc.nist.gov/projects/risk-management/about-rmf/categorize-step)
  Frames protection in terms of the impact of losing confidentiality, integrity, or availability. Use for: classifying semi-sensitive inventory by consequence rather than by whether it resembles a password.
- [Git ignore documentation](https://git-scm.com/docs/gitignore)
  Distinguishes shared ignore policy from repository-local and user-global exclusions. Use for: generated local artifacts, not for durable configuration or secrets that must be recoverable.

## Wisdom (Communities)

- [Home Operations on Discord](https://discord.gg/home-operations)
  Practitioner community centered on operating home infrastructure. Use for: testing recovery, network segmentation, and maintainability decisions against lived experience.
- [r/selfhosted](https://www.reddit.com/r/selfhosted/)
  Broad self-hosting community. Use for: comparing onboarding friction and failure modes, while verifying technical claims against upstream documentation.

## Gaps

- A household-specific threat model has not yet been written, so exact network identifiers cannot be classified once for all future cases.
- Recovery objectives for each future stack have not yet been stated; those objectives determine what “near seamless” should mean in measurable terms.

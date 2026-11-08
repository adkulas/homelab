# Scope Media Stack credentials to Stack Instances

Application credentials belong to a Site's Stack Instance and Environment, independent
of its Host placement. Their encrypted documents live beneath the owning Site and
carry encrypted Site, instance and Environment identity. Commands require explicit
selection and reject ownership mismatches; generated credentials update only the
selected application document. Host placement changes do not rotate application
identity. Distinct instances on the same Host retain distinct Compose projects,
ports, storage roots and runtime secret directories.

NordVPN subscription service credentials belong to a separate Site-owned media
credential document that instances reference explicitly. Sharing these credentials
is intentional; application credentials remain separate. The reusable Media Stack
contains policy, not deployment inputs or credentials. This refines ADR-0004 and
ADR-0006: credentials stay separate from sensitive network inventory, while one
consuming Stack may intentionally use a Site-scoped external subscription.

Host-owned credentials were rejected because one Host may run multiple instances,
and an instance can move Hosts. Global Stack-owned credentials were rejected because
initializing one installation could reuse or overwrite another's secrets. The cost
is explicit deployment selection, ownership validation and a one-time SOPS migration.

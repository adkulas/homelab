# Sites, Hosts, and Stack Instances

This document explains how the repository can grow beyond the Media Stack while remaining understandable, portable, and safe to publish. It describes an architectural direction, not functionality that is available today. The decision is recorded in [ADR-0004](adr/0004-model-sites-with-host-assigned-stack-instances.md).

## The goal

The repository should support several opinionated, independently operable Stacks, including media, local DNS, reverse proxying, remote access, home automation, and data services. It should also describe more than one real installation, such as Home and Parents, without embedding either installation's machine details into the reusable Stack definitions.

Another operator should be able to adopt the same defaults and workflows without inheriting this household's Hosts, paths, addresses, credentials, or runtime data.

## Mental model

- A **Site** is an independently administered installation, such as Home or Parents. A Site can contain multiple Hosts.
- A **Host** is a physical machine or virtual machine. It declares identity and capabilities such as architecture, container support, stable LAN addressing, or a usable GPU.
- A **Stack** is an independently deployable domain system. It owns reusable services, policy, lifecycle operations, and its configuration contract.
- A **Stack Instance** is one installation of a Stack within a Site. It selects a Host and provides the Site- and Host-dependent inputs required by the Stack.
- A **Site Inventory** is the versioned declaration connecting those concepts.
- A **Production Environment** or **Staging Environment** remains an Environment within a Stack Instance. Environments are not Sites.

The important ownership rule is:

```text
Stack                 owns reusable behavior and supported inputs
Site Inventory        owns installation composition and placement
Host                  owns identity and capabilities
Stack Instance        owns the binding between a Stack and a Host
Environment           owns isolated operational configuration within an instance
Stack secret document owns credentials consumed by that Stack
Runtime storage       owns service state, protected by backup and restore
```

## Repository shape

The intended shape is:

```text
stacks/
  media/
  network/
  proxy/
  home-automation/
  data/

sites/
  example/
    site.yaml
    hosts/
    instances/
  home/
    site.yaml
    private.sops.yaml
    hosts/
      media-host.yaml
    instances/
      media.yaml
  parents/
    site.yaml
    private.sops.yaml
    hosts/
    instances/
```

The exact set of future Stacks is not fixed by this structure. In particular, PostgreSQL should become a Stack only when a real consumer and recovery requirement justify operating a shared database service.

Each `site.yaml` is the entry point for one Site and explicitly references its Host and Stack Instance documents. Explicit references make missing files, duplicates, and accidental additions detectable; directory scanning should not silently change a Site.

The Example Site demonstrates adoption without containing either household's private values. Home and Parents may enable different Stacks, use different Hosts, and use distinct SOPS recipients.

## Where a media data directory belongs

A path such as `/srv/media/production` is not an inherent property of a Host. It is a Host-provided path used by a particular Environment of a particular Media Stack Instance. It therefore belongs to the Stack Instance declaration:

```yaml
apiVersion: homelab/v1alpha1
kind: StackInstance
metadata:
  name: media
spec:
  stack: media
  host: media-host
  environments:
    production:
      dataRoot: /srv/media/production
      backupRoot: /mnt/backups/media/production
    staging:
      dataRoot: /srv/media/staging
      backupRoot: /mnt/backups/media/staging
```

The referenced Host declaration contains capabilities rather than Media Stack topology:

```yaml
apiVersion: homelab/v1alpha1
kind: Host
metadata:
  name: media-host
spec:
  architecture: amd64
  capabilities:
    containers: true
    intelGpu: true
```

This permits two Stack Instances on one Host to use different paths, and permits an instance to move without changing the reusable Stack. It also prevents every Host document from becoming an untyped collection of unrelated service settings.

## Configuration resolution

A Stack Instance is resolved from several deliberately distinct sources:

```text
Stack Policy and defaults
        +
Stack Instance configuration from the Site Inventory
        +
Stack-owned SOPS secrets
        +
verified Host capabilities and runtime discovery
        ↓
resolved plan for one Stack Instance and Environment
```

Resolution must be typed and validated. It is not an arbitrary YAML merge. A Stack's configuration contract states which inputs an operator may supply, which values are fixed or derived, and which Host capabilities are required. Unknown fields, missing references, incompatible capabilities, colliding paths, and conflicting ports fail before mutation.

The current Media Stack configuration remains authoritative until repository-level Site Inventory resolution is implemented. The new layer should supply the existing Media Stack inputs rather than establish a second competing source of truth.

## Public, private, secret, and runtime information

Public and portable are different properties. Values should be placed according to their meaning first and disclosure impact second.

| Information | Examples | Location |
| --- | --- | --- |
| Reusable Stack intent | services, internal topology, health rules, safe defaults | cleartext Stack declaration and code |
| Low-impact Site Inventory | semantic Host IDs, Stack assignments, capability claims | cleartext Site Inventory |
| Sensitive Site topology | exact personal device names, addresses, person-to-device mappings | small Site-specific SOPS document |
| Credentials | passwords, API keys, VPN credentials, private keys | SOPS document owned by the consuming Stack |
| Discovered facts | render device, effective numeric group, filesystem behavior | runtime observation with explicit override only when needed |
| Runtime state | databases, media, downloads, caches, logs | managed storage plus tested backup and restore |

SOPS key names remain visible. Sensitive identifiers should therefore be encrypted values referenced by stable semantic IDs rather than used as mapping keys. A Site-specific SOPS identity should not automatically decrypt another Site, and recovery access should be planned separately from day-to-day operator access.

Ignored local files are suitable for disposable outputs, not for the only copy of desired configuration or secrets needed during recovery.

## How future Stacks should interact

Stacks remain independently deployable even when they consume Site-level information from each other. The Site Inventory declares composition and placement; it does not collapse the Stacks into one Compose project.

For example, a proxy Stack can derive routes from the Site's declared service endpoints, while the local DNS Stack can derive names pointing at the proxy Host. Tailscale may provide remote reachability or Site routes. Those relationships should use explicit, typed outputs and inputs rather than reading another Stack's internal Compose files or database.

Database ownership deserves special care. A shared PostgreSQL Stack should expose a narrow provisioning and recovery contract. Individual consumers should receive isolated databases and credentials without gaining ownership of the server's global configuration. A service that works well with its embedded database need not be moved merely because PostgreSQL exists.

## Adoption and operation

The intended adoption path is:

1. Copy the Example Site to a new Site.
2. Declare the Site's Hosts and their capabilities.
3. Select Stack Instances and assign each to a Host.
4. Supply the small set of required Site- and Host-dependent inputs.
5. Create Site- and Stack-specific SOPS documents using the new operator's recipients.
6. Validate and plan the resolved Site without mutation.
7. Initialize, diagnose, apply, and verify each Stack Instance through its own lifecycle contract.
8. Prove recovery with backups and Restore Drills where the Stack supports them.

“Near seamless” operation means this path is deterministic, validated, and recoverable. It does not mean every upstream setting is exposed or every runtime byte is checked into Git.

## Deliberate limits

- A Stack Instance is initially assigned to exactly one Host. Distributed orchestration can be reconsidered when a concrete Stack requires it.
- Site Inventory does not replace Stack configuration contracts or lifecycle commands.
- Site Inventory does not contain runtime state.
- A Host does not own copies of Stack topology.
- Production and Staging do not become separate Sites.
- The first implementation should provide validation and deterministic planning before repository-wide mutation.

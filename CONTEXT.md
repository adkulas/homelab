# Homelab

This context describes the independently deployable household service systems, their installations, and the media libraries
managed by the Media Stack.

## Language

**Site**:
An independently administered homelab installation, such as Home or Parents, containing one or more Hosts and Stack Instances.
_Avoid_: Household, location, environment

**Site Inventory**:
The versioned declaration of a Site's Hosts, Stack Instances, placement, and Site-specific inputs.
_Avoid_: Machine config, deployment manifest

**Host**:
A physical machine or virtual machine that provides capabilities and runs assigned Stack Instances within a Site.
_Avoid_: Server, node, box

**Stack**:
An independently deployable domain system that defines reusable services, policy, lifecycle, and configuration contracts.
_Avoid_: App, Compose project, service group

**Stack Instance**:
One installation of a Stack within a Site, assigned to a Host and supplied with Site- and Host-dependent configuration.
_Avoid_: Deployment, host config

**Movie Library**:
The household's managed collection of movies.
_Avoid_: Film collection

**Series Library**:
The household's managed collection of episodic television series.
_Avoid_: TV shows library, television collection

**Public Torrent Source**:
A publicly accessible torrent index from which media releases may be discovered.
_Avoid_: Public tracker

**Production Environment**:
The live Media Stack that manages the household's libraries and acquisition activity.
_Avoid_: Main stack, live stack

**Staging Environment**:
An isolated but operational Media Stack used to verify deployment, configuration, acquisition, and recovery without affecting
the Production Environment.
_Avoid_: Test stack, dev stack

**Declared Configuration**:
The settings intentionally controlled by this repository and restored to their declared values during reconciliation.
_Avoid_: Managed settings, desired state

**Service Configuration Contract**:
The complete operator-visible inventory of how the Media Stack controls, sources, synchronizes, or deliberately leaves
unmanaged each service setting within its scope.
_Avoid_: Settings catalog, configuration schema

**Stack Policy**:
A service setting intentionally fixed by the Media Stack with no operator override in Declared Configuration.
_Avoid_: Hard-coded setting, default

**Externally Synchronized Policy**:
A service setting applied by a named external policy owner and verified, but not directly reconciled, by the Media Stack.
_Avoid_: Declared Configuration, manual setting

**Unmanaged Configuration**:
An upstream service setting outside the Service Configuration Contract that the Media Stack does not promise to observe,
apply, or repair.
_Avoid_: User setting, ignored setting

**Promotion**:
The deliberate application to the Production Environment of an exact change already verified in the Staging Environment.
_Avoid_: Deploy, release

**Restore Drill**:
A recovery exercise that restores a Production Environment backup into an isolated Staging Environment and verifies the
recovered state.
_Avoid_: Restore test

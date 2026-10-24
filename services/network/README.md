# Home Site network preflight

Issue #97 adds a read-only preflight for the existing home Site. Run it on each available Host before changing Host addressing, DNS, or DHCP:

```bash
python3 services/network/preflight.py --role homelab
python3 services/network/preflight.py --role raspberrypi
```

Run the matching command **on that machine** from a checkout of this repository. A laptop can use `--role recovery-client` to check its local route and help prove wired recovery access. Exit code `2` means unresolved cutover blockers; `0` means all automated checks and operator confirmations passed; `64` means invalid input. The JSON report never prints an observed IP address, MAC address, DNS server address, or network profile contents.

The check reads routes, interface address metadata, relevant listeners, resolver metadata, profile counts, and tracked configuration candidates. It does not configure interfaces, query the Bell admin API, send DHCP broadcasts, restart services, or install anything. The report shows route interfaces and metrics, and listener binding types, without addresses. A local listener is evidence to review, not proof of a port conflict. An observed IPv6 resolver is not proof of how Bell advertises it. The manual checks remain blockers until separately verified.

See [the preflight and recovery packet guide](docs/preflight.md) for the operator checklist, private evidence format, and fallback sequence. The complete installation and cutover guide is a later work item.

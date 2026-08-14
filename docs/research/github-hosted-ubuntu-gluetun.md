# GitHub-hosted Ubuntu for protected Gluetun verification

Research performed 2026-08-14 against current first-party GitHub Actions documentation and runner-image source, Docker documentation, Linux kernel documentation, and the official Gluetun documentation.

## Answer

Current standard GitHub-hosted Ubuntu runners are a viable **conditional** host for the protected disposable Gluetun and qBittorrent suite. Run it directly on an explicit `ubuntu-24.04` virtual machine, not on `ubuntu-slim` and not inside a job container. GitHub documents that standard Ubuntu jobs receive a fresh virtual machine with passwordless `sudo`; this public repository currently receives 4 CPUs, 16 GB RAM, and 14 GB SSD. The current Ubuntu 24.04 image publishes Docker Compose, Docker Client, and Docker Server as installed tools. [GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners) [Ubuntu 24.04 runner image](https://github.com/actions/runner-images/blob/main/images/ubuntu/Ubuntu2404-Readme.md)

That is enough documented host control for Docker Compose to request `NET_ADMIN` and pass a host device into Gluetun. Docker documents both `cap_add` and `devices`, and Gluetun's NordVPN example requires exactly `NET_ADMIN` plus `/dev/net/tun`. [Docker Compose service attributes](https://docs.docker.com/reference/compose-file/services/#cap_add) [Gluetun NordVPN setup](https://github.com/qdm12/gluetun-wiki/blob/main/setup/providers/nordvpn.md)

There is one important limit to the proof: neither GitHub's runner contract nor the current `actions/runner-images` Ubuntu documentation guarantees that `/dev/net/tun` exists or that the running Azure kernel exposes the TUN driver. The Linux kernel documents `/dev/net/tun`, the standard `10:200` device node, `modprobe tun`, and the need for `CAP_NET_ADMIN`, but those are Linux mechanisms rather than a GitHub-hosted-runner service guarantee. [Linux TUN/TAP documentation](https://docs.kernel.org/networking/tuntap.html)

Therefore the strategy may schedule the live suite on GitHub-hosted Ubuntu, but it must start with a non-secret capability probe and must not make hosted-runner success the sole proof of Ubuntu or WSL2 compatibility. Until an actual workflow run records a successful TUN probe and tunnel lifecycle, the conclusion is “supported enough to attempt and gate conditionally,” not “contractually guaranteed by GitHub.”

## Documented facts

### Runner and container primitives

- A standard `ubuntu-24.04` job runs on a newly provisioned virtual machine. Linux virtual machines have passwordless `sudo`. The public-repository runner size is currently 4 CPUs, 16 GB RAM, and 14 GB SSD. `ubuntu-slim` is instead an unprivileged container; GitHub explicitly says it does not support Docker-in-Docker or low-level kernel operations, so it is unsuitable here. [GitHub-hosted runners reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)
- The current Ubuntu 24.04 image manifest lists Docker Compose, Docker Client, and Docker Server. Runner images are updated weekly, and GitHub recommends a specific OS label when consumers need to avoid an automatic `-latest` migration. [Ubuntu 24.04 runner image](https://github.com/actions/runner-images/blob/main/images/ubuntu/Ubuntu2404-Readme.md) [Runner-images support and update policy](https://github.com/actions/runner-images#software-and-image-support)
- Compose can add Linux capabilities and map host devices into a service. The Linux TUN driver requires a process to open `/dev/net/tun`; creating or attaching a network device requires `CAP_NET_ADMIN`. [Docker Compose service attributes](https://docs.docker.com/reference/compose-file/services/#cap_add) [Linux TUN/TAP documentation](https://docs.kernel.org/networking/tuntap.html)
- Gluetun's official NordVPN Compose example adds only `NET_ADMIN` and `/dev/net/tun`; it does not require `privileged: true` or host networking. It accepts NordVPN OpenVPN service credentials and defaults OpenVPN to UDP. [Gluetun NordVPN setup](https://github.com/qdm12/gluetun-wiki/blob/main/setup/providers/nordvpn.md) [Gluetun OpenVPN options](https://github.com/qdm12/gluetun-wiki/blob/main/setup/options/openvpn.md)

### The live assertions are controllable

Gluetun provides the controls needed for a deterministic fault exercise:

- qBittorrent can join Gluetun's network namespace with `network_mode: "service:gluetun"`. [Gluetun container networking](https://github.com/qdm12/gluetun-wiki/blob/main/setup/connect-a-container-to-gluetun.md)
- Gluetun's authenticated control server supports reading VPN state and changing it to `stopped` or `running` with `PUT /v1/vpn/status`. This supplies a supported tunnel-interruption mechanism without stopping the firewall or altering the host. [Gluetun control server](https://github.com/qdm12/gluetun-wiki/blob/main/setup/advanced/control-server.md)
- Gluetun documents that its firewall acts as a kill switch: ordinary outbound traffic is limited to the VPN interface and selected VPN endpoint, and the firewall remains active when the tunnel is down. [Gluetun firewall behavior](https://github.com/qdm12/gluetun-wiki/blob/main/faq/firewall.md)

The hosted suite can consequently prove tunnel readiness, a VPN public IP distinct from a host-side observation, qBittorrent namespace confinement, failed fresh egress while the VPN is stopped, and recovery after the VPN is started again. These are Media Stack assertions executed through Docker and supported Gluetun interfaces; they are not merely CLI parser tests.

### Secrets can be isolated from pull requests

GitHub environments can restrict branches, require approval, and withhold environment secrets until protection rules pass. Secrets other than `GITHUB_TOKEN` are not sent to workflows triggered from forks. [Deployments and environments](https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments) [Using secrets in GitHub Actions](https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets)

Those controls do not make arbitrary checked-out code safe. GitHub warns that automatic redaction is not guaranteed, that a compromised action in the same job can access secrets and the Docker socket, and that actions should be pinned to full commit SHAs. [GitHub Actions secure-use reference](https://docs.github.com/en/actions/reference/security/secure-use)

For this suite:

- never expose NordVPN credentials to a `pull_request` or `pull_request_target` job;
- allow only a protected default-branch schedule or a manually approved run against an exact trusted commit;
- use an environment restricted to `main`, a read-only `GITHUB_TOKEN`, and only audited actions pinned to full SHAs;
- store the service username and password as two separate environment secrets, materialize owner-only temporary files, and mount them only as Gluetun Compose secrets; Gluetun supports `openvpn_user` and `openvpn_password` secret files; [Gluetun Docker secrets](https://github.com/qdm12/gluetun-wiki/blob/main/setup/advanced/docker-secrets.md)
- disable shell tracing, never put credentials on a command line, never upload raw environment dumps or unredacted Compose/container inspection, and delete plaintext in an unconditional cleanup path.

### Time, storage, and network boundaries

GitHub-hosted jobs have a six-hour platform maximum, but this suite should impose much shorter bounded readiness, interruption, recovery, and cleanup deadlines. The standard public runner has only 14 GB SSD, so the job must avoid media acquisition, large persistent fixtures, and unrelated images. GitHub states that pulls of public Docker Hub images from hosted runners are not subject to Docker Hub's rate limit. [Actions limits](https://docs.github.com/en/actions/reference/limits)

Ubuntu runners originate from changing Azure data-center address ranges; GitHub does not recommend treating those broad ranges as an allowlist and updates its published Actions ranges weekly. GitHub permits workflows to access additional networks used by an action, but its runner documentation does not promise reachability to NordVPN endpoints, a stable source address, UDP availability to a particular endpoint, or a particular NordVPN server's behavior. [GitHub-hosted runner networking](https://docs.github.com/en/actions/reference/runners/github-hosted-runners#ip-addresses)

This makes the NordVPN connection an external operational dependency. A bounded failure must report whether the prerequisite, authentication, server selection, UDP connection, tunnel health, or recovery stage failed. It must not be hidden by a blind whole-job retry.

## Required capability probe and suite shape

The protected job should use this order:

1. Record the runner OS label, image version, kernel version, Docker versions, and free disk space; record no environment values.
2. Confirm Docker and Compose are usable directly on the VM.
3. Confirm the TUN driver/device contract before loading NordVPN secrets: check `/dev/net/tun`, use the documented module/device-node setup if necessary, and prove that a pinned disposable container with only `NET_ADMIN` and the TUN device can create a TUN interface. A failure here is a stable runner-prerequisite result, not a skipped success.
4. Materialize the two protected NordVPN service credentials as secret files only after the capability probe succeeds.
5. Start pinned Gluetun and qBittorrent images under a unique Compose project name and temporary paths. Reject Production Environment identifiers or paths.
6. Prove Gluetun health, qBittorrent's shared namespace, and tunneled egress identity.
7. Stop and restart the VPN through Gluetun's authenticated control API. Prove fresh qBittorrent-namespace egress fails while stopped, never returns the host public IP, and recovers through the tunnel.
8. Capture only redacted structured evidence and bounded relevant logs, then always run project-scoped Compose cleanup including volumes and temporary secret-file cleanup.

No step may turn off Gluetun's firewall, grant `privileged: true`, use host networking, attach qBittorrent to an independent network, or broaden `FIREWALL_OUTBOUND_SUBNETS` to make the hosted runner pass.

## Failure routing and decision

- **Use GitHub-hosted Ubuntu:** run the protected live Gluetun/qBittorrent suite on explicit `ubuntu-24.04` after the capability probe has succeeded in a real workflow. Treat it as scheduled/manual evidence, not as an ordinary pull-request check.
- **Keep pull requests hermetic:** pull requests run topology validation and non-secret disposable tests only. They must not receive NordVPN credentials or depend on NordVPN/internet behavior.
- **Do not claim platform parity:** a successful hosted Ubuntu run does not prove Docker Desktop through WSL2. The agreed Local WSL2 Verification Run remains required on the owner's equipment for platform-sensitive changes and milestone completion.
- **Move rather than weaken:** if repeated runs show that the current hosted image cannot expose a working TUN device or reach NordVPN with the declared OpenVPN protocol, move the live tunnel, interruption, fail-closed, and recovery assertions to repeatable Local Ubuntu and Local WSL2 Verification Runs. Keep structural Compose and CLI acceptance coverage in GitHub Actions. Do not silently skip the live checks or weaken the VPN boundary.

The practical answer is therefore **yes, conditionally**: GitHub-hosted Ubuntu exposes the documented VM, privilege, Docker, Compose, secret, and lifecycle mechanisms the suite needs, while TUN presence and NordVPN reachability remain runtime prerequisites that GitHub does not guarantee. The first protected workflow run must turn that remaining inference into recorded evidence.

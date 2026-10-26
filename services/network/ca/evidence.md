# Management HTTPS evidence — #100

Rehearsed on Ubuntu with OpenSSL 3.3.1 and the pilot's pinned Technitium v15.6.2 image.
All CA and leaf material was disposable, stored outside Git and removed after the run.

| Requirement | Reproducible evidence | Result |
| --- | --- | --- |
| Both Host certificates and administrator trust path | CLI acceptance checks issue `homelab.home` and `raspberrypi.home` server-only leaves; verify names, issuer, expiry and key matching | Passed |
| Real Technitium certificate loading | `services/network/pilot/https_check.sh` loads encrypted PKCS#12 into both unpublished containers and verifies each named TLS connection against the public root | Passed |
| Supervised renewal | The same pilot replaces the secondary leaf and requires the exact new certificate; loopback checks also reject the old leaf when the replacement is expected | Passed |
| Failure checks | CLI rejects wrong names, unrelated roots, insufficient remaining lifetime, mismatched keys, existing output, Git destinations, symlinked output paths and public password files | Passed |
| Separate encrypted root backup | Acceptance test archives only root key/certificate, age-encrypts for a disposable recovery identity, decrypts, removes the original key and signs using the restored key; leaf verifies against the original root | Passed |
| Ciphertext leaf storage | Documented SOPS binary archive command round-tripped disposable input with a fresh age identity | Passed |
| DHCP isolation | Pilot checks all scopes disabled before and after every leaf installation; bridge has no published Host ports | Passed |

Run `go test ./tests/acceptance -run '^TestNetworkManagementCertificateCommands$'` for
CLI acceptance evidence, or run the Python suite directly as documented in the network
README. The full repository suite includes these CLI checks. The Docker rehearsal is an
explicit separate command; it is not part of the ordinary full suite.

These checks do not enroll real administrator devices or establish physical offline root
custody. Use the [custody, installation and recovery guide](../docs/management-https.md)
for those supervised actions. Live management restrictions, DNS/cluster health during
renewal and real Host installation are deployment gates. No household Host address, Bell
setting, production certificate or DHCP owner changed in this rehearsal.

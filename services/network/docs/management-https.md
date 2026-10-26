# Offline CA and Technitium management HTTPS (#100)

This procedure prepares trusted management HTTPS for `homelab.home` and `raspberrypi.home`.
It does not install the production DNS Stack Instances; #103 and the Pi deployment must
complete the live checks below before DNS Cluster join. Bell remains the gateway and DHCP
owner. Certificates terminate directly at Technitium on TCP 53443, including DNS Cluster
connections; do not put Traefik in that path.

## Ownership and prerequisites

The Site administrator is the CA custodian and approves every signing operation. Use a
separate offline computer or disconnected boot medium with Python 3 and OpenSSL 3. Keep
its clock correct. Keep the CA root private key off `homelab`, the Pi, Git, and deployment
pollers. A connected deployment Host must never receive the root directory or its backup.
The offline computer needs this checkout and the public CSRs, not Site decryption keys.

All example paths are operator-selected absolute paths **outside every Git checkout**.
Set `CA_DIR` on the offline medium, `REQUEST_DIR` in a private administrator workspace,
and `LEAF_DIR` in a fresh private workspace for each issuance. Use `umask 077`. Supply
passwords through mode-0600 files created in a password manager or secure editor, never
literal shell arguments or command history. Keep those files outside Git and remove them
after use. The CLI refuses output inside Git, existing outputs, and symlinked output
locations; it does not determine whether a machine is offline.

| Public seam | Required behavior | Source of truth | Evidence |
| --- | --- | --- | --- |
| `root`, `request`, `sign`, `bundle` | Encrypted offline root; separate per-Host keys; explicit SAN; server-only leaf; PKCS#12 with key | ADR-0008; pinned Technitium settings API | OpenSSL chain/name/purpose verification; real Technitium TLS handshake |
| `verify`, `probe` | Reject wrong name, untrusted issuer, expiry inside threshold | X.509 chain and SAN; administrator trust store | Negative checks and verified handshakes on both disposable instances |
| Offline backup procedure | Separate encrypted copy recoverable without homelab | ADR-0008 | Decrypt and validate a disposable root backup |
| Supervised renewal/recovery | Rotate one leaf at a time; explicit trust replacement after root loss | Site administrator | Replacement leaf served and checked before peer changes |

## Create the root once — offline computer

Choose `ROOT_PASSWORD_FILE` outside the CA directory; retain its password in separate
custody from both backup media. Set all path variables before running these examples.

```bash
umask 077
python3 services/network/ca/certificates.py root \
  --output "$CA_DIR" --password-file "$ROOT_PASSWORD_FILE"
openssl x509 -in "$CA_DIR/root.crt" -noout -fingerprint -sha256 -dates
```

Expected: a 10-year root with an AES-encrypted RSA private key, CA constraint and a
zero subordinate-CA path length. Record the SHA-256 root fingerprint on paper and in the
administrator password manager, independently of transfer media. Stop on any failure.
Disconnect and store the medium after signing; never copy the key into a leaf directory.
Undo an unused root by removing its untrusted public copy and private working directory;
once distributed, use the root replacement procedure below.

## Root backup and a recovery drill — offline computer

Use age with a dedicated offline recovery recipient, not the routine Site deployment
identity. Set `RECOVERY_RECIPIENT`, `ROOT_BACKUP` on separate removable storage, and
`RECOVERY_IDENTITY` from independent recovery custody. `ROOT_BACKUP` must be a new file;
never replace the only known-good backup. The root archive is separate from Technitium
backups and leaf-key SOPS documents. The recipient is public; the recovery identity is not.

```bash
(set -o pipefail; set -o noclobber
 tar -C "$CA_DIR" -cf - root.key root.crt |
   age -r "$RECOVERY_RECIPIENT" > "$ROOT_BACKUP")
mkdir -m 700 "$RESTORE_DIR"
(set -o pipefail
 age -d -i "$RECOVERY_IDENTITY" "$ROOT_BACKUP" |
   tar -x -C "$RESTORE_DIR")
openssl pkey -in "$RESTORE_DIR/root.key" -passin "file:$ROOT_PASSWORD_FILE" -check -noout
openssl verify -CAfile "$RESTORE_DIR/root.crt" "$RESTORE_DIR/root.crt"
openssl x509 -in "$RESTORE_DIR/root.crt" -noout -fingerprint -sha256
```

Compare the fingerprint with the independent record. Also sign a disposable CSR with
the restored CA using `sign` below and verify its leaf against the original root. Only
then mark the backup verified, remove the restored working copy, detach the backup
medium and store it separately from the working CA. Record date, custodian, media label,
fingerprint and drill outcome without keys/passwords. Repeat after root replacement.
Stop on any command failure; remove an incomplete new archive and retain the last verified
backup. A backup whose passphrase or age identity cannot be recovered is insufficient.

## Request, sign and package two leaves — supervised transfer

On an administrator computer with private storage, run separately for `homelab.home` and
`raspberrypi.home`. Set `NAME` to that exact Host name and `REQUEST_DIR` to a fresh path.
A request has an unencrypted leaf private key suitable for packaging; access is mode 0600
inside a mode-0700 directory. Transfer **only** `leaf.csr` to the offline signer. Check
its fingerprint out of band with the requester. No IP SAN is required: use the DNS name
in browsers even before DHCP cutover; use a temporary administrator hosts-file entry
with the privately verified Host address if DNS is not yet available.

```bash
python3 services/network/ca/certificates.py request --name "$NAME" --output "$REQUEST_DIR"
openssl req -in "$REQUEST_DIR/leaf.csr" -noout -verify
openssl req -in "$REQUEST_DIR/leaf.csr" -outform DER | openssl dgst -sha256
```

On the offline computer, verify the requested name and fingerprint, set `CSR_FILE` to
the transferred CSR and `LEAF_DIR` to a new directory. Signing deliberately supplies its
own single-name SAN and server-only extensions, rather than copying CSR extensions.

```bash
python3 services/network/ca/certificates.py sign --name "$NAME" \
  --ca "$CA_DIR" --request "$CSR_FILE" --output "$LEAF_DIR" \
  --password-file "$ROOT_PASSWORD_FILE"
python3 services/network/ca/certificates.py verify --name "$NAME" \
  --ca-cert "$CA_DIR/root.crt" --cert "$LEAF_DIR/leaf.crt"
```

Leaves last one year; signing refuses a root with less than 366 days remaining. Transfer
only the signed `leaf.crt` and public `root.crt` back, then disconnect the signer. On the
administrator computer, set `CERT_FILE` to the returned leaf and `ROOT_CERT` to the
fingerprint-checked public root. Set `PFX_PASSWORD_FILE` to a fresh strong password file
and `PFX_FILE` to a new file outside Git.

```bash
python3 services/network/ca/certificates.py bundle --key "$REQUEST_DIR/leaf.key" \
  --cert "$CERT_FILE" --ca-cert "$ROOT_CERT" --name "$NAME" \
  --password-file "$PFX_PASSWORD_FILE" --output "$PFX_FILE"
```

Encrypt the PFX and password together before persisting or transferring them. For example,
create a fresh mode-0700 `$SECRET_DIR`, copy the PFX to `$SECRET_DIR/management.pfx`
and password file to `$SECRET_DIR/pfx-password`, then use explicit approved age recipients:

```bash
(set -o pipefail; set -o noclobber
 tar -C "$SECRET_DIR" -cf - management.pfx pfx-password |
   sops --encrypt --input-type binary --output-type binary \
     --age "$LEAF_RECIPIENTS" /dev/stdin > "$LEAF_ARCHIVE")
```

`LEAF_RECIPIENTS` is the comma-separated daily and recovery public recipients selected by
the administrator; `LEAF_ARCHIVE` is a new `*.sops` file. Only that ciphertext may enter
Git. After verifying decryption to private temporary storage, remove temporary plaintext
leaf keys, PFXs and passwords. Runtime decrypted files on the selected Host remain private,
service-readable and outside Git. CA root material never enters a leaf archive.

## Install and trust — administrator device and each selected Host

First authenticate the root fingerprint out of band. Import only `root.crt` into the
administrator device's trusted roots (Ubuntu: copy it as `home-site-root.crt` under
`/usr/local/share/ca-certificates/`, then `sudo update-ca-certificates`; Windows: use
`certmgr.msc`, Current User / Trusted Root Certification Authorities; macOS: Keychain
Access / System, import and explicitly trust for SSL). Firefox may use its own certificate
authority store: import the same root under Authorities if OS trust is not used. Verify
in the browser at both Host names; there must be no exception or bypass prompt. Remove
this root from the same stores to undo trust distribution.

On each DNS Host, decrypt only that Host's leaf archive to a mode-0700 temporary directory.
Install `management.pfx` at a service-readable, private, persistent path (container path
`/etc/dns/tls/management.pfx` on homelab; a private persistent `tls/management.pfx` under
the native Pi configuration directory). The native service user must own/read the file;
use mode 0600 and a mode-0700 parent. Transfer over an authenticated encrypted channel.
Keep the previous PFX and settings privately until verification passes.

Use an SSH tunnel to loopback TCP 5380 for initial console configuration, with LAN access
to that HTTP port blocked. In Technitium Settings / Web Service, enable HTTPS, choose port
53443, disable self-signed certificate generation, set the certificate path and its password,
and enable HTTP-to-HTTPS redirect after HTTPS is verified. The matching v15.6.2 API fields
are `webServiceEnableTls`, `webServiceTlsPort`, `webServiceUseSelfSignedTlsCertificate`,
`webServiceTlsCertificatePath`, `webServiceTlsCertificatePassword`, and
`webServiceHttpToTlsRedirect`. Supply passwords in the tunneled UI or a bearer-authenticated
POST body; never URL parameters, shell arguments or Compose environment values.
Apply settings to one Host at a time. Restrict TCP 53443 to trusted LAN/VPN administrator
sources and the DNS peer; block external and untrusted sources over IPv4 and IPv6. Firewall
installation and live access checks are prerequisites in the Host deployment tickets.
See the pinned [settings API](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.2/APIDOCS.md#dns-server-settings).

On an administrator device, check each endpoint using the public root and correct name:

```bash
python3 services/network/ca/certificates.py probe --name homelab.home --ca-cert "$ROOT_CERT"
python3 services/network/ca/certificates.py probe --name raspberrypi.home --ca-cert "$ROOT_CERT"
```

Before DNS is available, add `--address "$VERIFIED_HOST_ADDRESS"`; the probe still validates
the named SAN with SNI. Expected: `OK`, verified chain/name and at least 30 days remaining.
Stop on failure. Restore the prior PFX and Web Service settings via the local SSH console;
do not bypass TLS validation. Confirm ordinary DNS answers and existing SSH/Docker services
still work before moving to the other Host. Cluster initialization must use these names
and trusted roots; never copy the pilot's `ignoreCertificateErrors=true` into deployment.

## Expiry, renewal, and loss recovery

After replacement, add `--expected-cert "$CERT_FILE"` to `probe` to confirm the exact
new leaf is being served.

Run `probe` for both endpoints weekly from an administrator machine and before protected
network changes. A nonzero exit is a stop condition; 30 days is the minimum remaining
lifetime, not an automatic-renewal trigger. Review root expiry monthly with `openssl x509
-in "$ROOT_CERT" -noout -dates -checkend 34560000` (400 days). Alert the CA custodian on
failure. Check each archived leaf with `verify` before installation. Certificate serials
and expiry dates may be recorded; never record decrypted operational addresses or keys.

At least 30 days before leaf expiry, generate a **new** key and CSR, sign offline, package,
encrypt, and replace the secondary's leaf first. Verify the browser, `probe`, DNS and cluster
health. Then replace the primary's leaf and repeat. Keep rollback copies private until
both checks pass; renewed leaves retain the same DNS SAN and root, so administrator trust
does not change. If cluster peer verification breaks after replacement, stop and restore
the previous leaf/settings; reconcile cluster certificate state through the supported
cluster procedure before retrying. No unattended signing, root mounting or renewal timer.

Lost leaf key: issue a fresh key, replace the affected leaf and remove the old material.
Treat a compromised leaf as unsafe until replaced; these procedures do not deploy a CRL
or OCSP responder. Lost root medium: recover the separately encrypted archive offline,
check its fingerprint/key and perform a disposable signing drill before issuance. Lost
passphrase or unusable backup: create a new offline root. Suspected root compromise or root
expiry: create and back up a new root, authenticate its fingerprint, install new trust on
administrator devices, reissue both leaves, rotate one Host at a time, verify both endpoints
and DNS Cluster health, then remove the old root from every administrator/peer trust store.
For compromise, isolate management while rotating and remove old trust promptly. Do not
restore a compromised root from backup. None of these operations enables DHCP.

## Repeatable evidence

`python3 -m unittest discover -s services/network/ca -p 'test_*.py'` exercises the CLI with
throwaway keys and loopback TLS. `services/network/pilot/https_check.sh` exercises issued
certificates and a renewed secondary leaf against the pinned real Technitium containers,
with no published ports or household DHCP path. Both remove disposable material on exit.
Actual root custody, administrator trust enrollment, live firewall checks and final Host
installation are supervised deployment evidence, not claims made by these rehearsals.

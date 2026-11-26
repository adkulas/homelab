# Owner remote access

Use one native Tailscale node on homelab. Existing OpenSSH keys and Traefik HTTP
URLs work over its overlay IPv4 address. Only the NVR uses subnet routing.
Perform enrollment and approvals from a trusted local console with LAN recovery access.

## Install and enroll

1. Edit the [encrypted operator inputs](../../secrets/tailscale.sops.yaml) with
   SOPS. Record the verified tailnet, owner login, and later the enrolled node
   address. Keep credentials in the password manager. Compare the encrypted
   device inventory with the live NVR network settings and its DHCP reservation
   or static assignment. Record its stable IPv4, /32 and actual media port and
   protocol. A manufacturer's default or last observed lease needs confirmation.
2. Review the effective tailnet policy including legacy ACLs, grants, shared
   nodes and automatic route approvals. Remove overlapping permissions (including
   the starter allow-all) before enrollment. Preserve unrelated policy and test
   it in the admin policy editor. Begin with access to the new node denied.
3. From the [host Ansible directory](ansible) run
   `ansible-playbook -i inventory.yml playbook.yml --tags tailscale -K`.
   Fresh check-mode runs preview the repository setup and defer package/service
   checks until installed. On the host inspect `sudo tailscale status`. If it
   already belongs to a tailnet, reconcile its identity and current settings
   before continuing. For a fresh node run
   `sudo tailscale up --accept-dns=false --ssh=false`, then follow the login URL
   and confirm the node in the intended tailnet. Capture `tailscale ip -4` and
   the owner's enrolled client IPv4 addresses privately. Verify that the host
   advertises no exit node or subnet routes.

## Grant the owner access

Substitute verified private values in the following tailnet policy fragment.
The media entry illustrates verified TCP. Use UDP or both only if required by
the device. Use the node's overlay IPv4 and the NVR's LAN IPv4 as single hosts.
Merge into the complete policy and review the resulting permissions before saving.

```json
{
  "hosts": {"home-node": "NODE_IPV4", "home-nvr": "NVR_IPV4"},
  "grants": [
    {"src": ["OWNER_LOGIN"], "dst": ["home-node"], "ip": ["tcp:22", "tcp:80", "tcp:53", "udp:53"]},
    {"src": ["OWNER_LOGIN"], "dst": ["home-nvr"], "ip": ["tcp:MEDIA_PORT"]}
  ]
}
```

In the policy editor test the owner's allowed destinations and each protocol.
Test denials to the node's management ports (TCP 53443 and 5380), SMB (TCP 445),
other LAN addresses and an ungranted NVR port. Check each other principal lacks
all four paths (SSH, HTTP, DNS, NVR). Grants are additive. Keep route approval
manual. The host keeps its existing SSH authentication. [Tailscale policy reference](https://tailscale.com/docs/reference/syntax/policy-file)

## Configure the restricted DNS resolver

1. Inventory the aliases in [the Site DNS declaration](../../network.json)
   against the [Traefik routes](../../traefik/dynamic). Baseline UDP and TCP
   answers from both LAN resolvers, cluster health, HTTP routing and the current
   firewall counters. The two Technitium HTTP aliases redirect to management
   HTTPS. The tailnet policy must deny that final connection.
2. Follow the [protected console workflow](../../../../services/network/docs/dns-cluster.md)
   for resolver settings and Apps. Pause writers, snapshot both nodes and confirm
   the deployed server and Split Horizon App versions support response translation
   on the clustered zone. Rehearse the exact change on a disposable copy. Write a
   private change sheet with commit, before/after settings, affected members,
   firewall diff, rollback and snapshot digests. Obtain operator approval, take a
   fresh paired backup and run the [backup gate](../../../../services/network/docs/backup-restore.md)
   immediately before mutation. Re-read state and renew approval if anything
   drifted. Shared zone records retain their LAN values.
3. In the primary's Split Horizon App config merge the following settings. Add
   one network mapping per verified owner client /32. Preserve any existing
   custom networks, groups and translations, checking them for overlaps. On a
   fresh installation remove sample mappings. Inspect the effective config on
   both members after applying the approved change.

   ```json
   {
     "enableAddressTranslation": true,
     "networkGroupMap": {"OWNER_CLIENT_IPV4/32": "owner-tailnet"},
     "groups": [{
       "name": "owner-tailnet",
       "enabled": true,
       "translateReverseLookups": false,
       "externalToInternalTranslation": {"HOST_LAN_IPV4": "NODE_IPV4"}
     }]
   }
   ```

   This rewrites the final host A answer for queries from the listed clients.
   Verify UDP/TCP 53 listens on the node address. Where required, add the
   specific client /32s to the primary's allowed DNS clients and existing
   persistent firewall for DNS on `tailscale0`. Keep LAN recursion and the
   existing management restrictions. Confirm effective ordering with Tailscale
   and Docker running. [Split Horizon App configuration](https://github.com/TechnitiumSoftware/DnsServer/blob/master/Apps/SplitHorizonApp/dnsApp.config)
4. In tailnet DNS add a **restricted nameserver** at the node's overlay IPv4
   for the `home` suffix. Enable Tailscale DNS on the remote client. At home,
   disconnect Tailscale or disable Tailscale DNS on that client to use the
   household resolver and get LAN answers. A connected device still using
   restricted DNS receives the overlay answer even on home Wi-Fi. Restore
   Tailscale DNS when leaving home. Keep household DHCP and global nameservers
   as configured. To roll back, remove
   that restricted entry, restore the approved previous app, resolver and
   firewall settings, then verify both LAN resolvers and cluster health. After
   validation or rollback, verify the declared-DNS plan is empty and resume
   the writers paused for the change.

## Advertise and test the NVR route

1. On the host confirm IPv4 forwarding with `sysctl net.ipv4.ip_forward`.
   If disabled, persist `net.ipv4.ip_forward=1` under `/etc/sysctl.d/` and
   load that file with `sudo sysctl -p FILE`. Verify the host can reach the
   recorded NVR port and the forwarding firewall permits the approved path.
   Retain Tailscale's default subnet SNAT. With no other advertised routes,
   run `sudo tailscale set --advertise-routes="$NVR_IPV4/32"`. Approve just
   this device route in the tailnet admin console. Enable subnet route use
   on the remote client (on Linux, `sudo tailscale set --accept-routes=true`).
2. On home Wi-Fi with Tailscale off, add the Reolink device once using
   **Manual Input → IP** with the stable LAN IP, confirmed port and its own
   Reolink credentials. Verify live view and play an identifiable recording.
   Repeat using that saved entry on cellular with Wi-Fi off and Tailscale on.
   Check fresh connections so cached video or UID/cloud access cannot mask a
   routing failure.
3. On a remote owner client verify both `dig @"$NODE_IPV4" "$APP"` and
   `dig +tcp @"$NODE_IPV4" "$APP"` end at the overlay address. Check the OS
   resolver and open each deployed media app and Homepage using its normal HTTP
   URL. Confirm the expected app/login page. For each Technitium alias verify
   the HTTP redirect and denial of its HTTPS management destination. Use the
   existing SSH key to log in as
   `adam@homelab.home` and verify host identity. Confirm blocked destinations
   from the grant checks. On LAN with Tailscale off, verify the same names
   end at the LAN address via both resolvers and open the URLs.
4. Disable approval of just the NVR /32 in the admin console. On cellular
   reconnect the saved entry and confirm fresh live view and playback fail.
   Repeat the remote SSH login and HTTP checks successfully. Reapprove that
   same /32, repeat cellular viewing/playback and leave it approved after
   success. This approval toggle is also the independent NVR off switch.
   Record date, tested client networks, policy checks and outcomes privately.

Family enrollment is deferred. Before inviting anyone, revisit browser-trusted
HTTPS and define which Traefik routes the family may use. A port 80 grant reaches
every virtual host on that listener. Grant all invited family members the family
apps and verified NVR port, with individual non-admin Reolink accounts. Reserve
SSH for the owner.

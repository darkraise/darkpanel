# M5 live demonstration checklist

The acceptance of M5 register row 4. Claude runs the demonstration with
the owner's credentials (spec 12 item 2). Each step is done through Darkpanel
(the web UI or the API), never by hand on a host, and its observation is
recorded in row 4's note: what was run, on which hosts, and what was seen.

## Prerequisites (owner checklist, spec 12 item 2)

- A test tailnet and an OAuth client with the scopes of spec 4.2a and the
  allowed tags.
- A controller, and three spare hosts: one public host for the DERP server
  (a DNS name pointing at it for Let's Encrypt, or its public IP for `ip`
  mode), and two hosts that will become tailnet nodes.
- A way to block direct UDP between the two nodes (for example a firewall
  rule on one of them dropping UDP to and from the other's address).

## Steps

1. **Tailnet management (R58, R59).** Connect the tailnet with
   `tailnet.account.connect` and the OAuth client; confirm the capability
   list shows every scope. List devices. Approve a subnet route and an
   exit node on a device. Create an auth key (`tailnet.authkey.create`).
   Edit the policy file: the diff before saving shows the change and the
   comments survive (`tailnet.policy.get`, `tailnet.policy.set`).
2. **Join at install (R60).** Create a host with the tailnet join
   (`core.host.create` with `tailnetJoin`), install the agent from the
   bundle on node A, and confirm node A appears in the device list and the
   panel's host page shows its Tailscale status. Repeat for node B.
3. **Tailscale certificate (v1 #60).** On the controller, confirm the panel
   serves the Tailscale web certificate with the MagicDNS name.
4. **Deploy DERP (R61, R62).** Run `derp.server.deploy` on the DERP host,
   with client verification left on. Confirm the firewall opened 80/tcp,
   443/tcp and 3478/udp. Record the certificate mode used, the tailscaled
   version pinned on the DERP host (its Tailscale package panel), and that
   `derp.server.status` reports `verifyClients` true.
5. **Region (R63).** Run `derp.region.set` with a region id from 900 to 999
   and `omitDefaultRegions` on, so the custom region is the only one in the
   map. Note any warning about the visibility grant.
6. **Probe (R64).** Run `derp.server.probe` from the controller and both
   nodes: TLS and STUN reachable, DebugDERPRegion diagnostics clean, and
   both nodes visible to the DERP host (the provisional `tcp:443` visibility
   grant is sufficient, spec 4.3).
7. **Relayed traffic (spec 4.3).** Block direct UDP between nodes A and B.
   Run `tailscale ping` from A to B and record that it goes via the custom
   region (`via DERP(<code>)`), not direct. The probe page shows both nodes
   observed on the region.
8. **Clean up.** `derp.region.remove`, then `derp.server.remove`; unblock
   UDP; leave or remove the test devices.

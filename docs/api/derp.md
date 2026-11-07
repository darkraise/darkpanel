# derp API

Back to the [API reference](README.md).

## derp.binary.update v1

Replace derper with the version this agent's release carries, rolling back when it is not healthy.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/derp.binary.update/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/derp.binary.update/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `version` | string | yes | The derper version to install. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | The derper version replaced. |
| `to` | string | yes | The derper version now installed. |

## derp.cert.renew v1

Replace the DERP server's self-signed certificate with a new one.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/derp.cert.renew/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/derp.cert.renew/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certName` | string | yes | The new certificate's pin. |
| `certNotAfter` | string (date-time) | yes | When the new certificate expires. |
| `priorCertName` | string | yes | The replaced certificate's pin. |

## derp.cert.rotate v1

Renew the IP certificate, then publish its pin; between the two steps clients holding the old pin cannot use the region.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.cert.rotate/v1/plan` |
| apply | `POST /api/v1/actions/derp.cert.rotate/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host running the server. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certName` | string | yes | The new certificate's pin. |
| `certNotAfter` | string (date-time) | yes | When the new certificate expires. |

## derp.node.probe v1

Probe a DERP server over TLS and STUN from this host.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/derp.node.probe/v1/read` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string | no | The address to connect to; absent resolves hostName. |
| `certName` | string | no | The certificate pin in ip mode; absent verifies the chain. |
| `hostName` | string | yes | The server's DNS name or public IP literal. |
| `regionId` | integer | no | The server's region id, 900 to 999; absent skips the region diagnostics. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `region` | object or null | yes |  |
| `stun` | object | yes | The STUN probe. |
| `stun.error` | string | yes | Why the probe failed: timeout, connection_refused, unreachable, tls_handshake_failed, certificate_not_trusted, pin_mismatch, bad_answer, no_stun_answer or failed; empty when OK. |
| `stun.latencyMs` | number | yes | The round trip in milliseconds; 0 when not OK. |
| `stun.ok` | boolean | yes | Whether the probe succeeded. |
| `tls` | object | yes | The TLS probe of /derp/probe. |
| `tls.error` | string | yes | Why the probe failed: timeout, connection_refused, unreachable, tls_handshake_failed, certificate_not_trusted, pin_mismatch, bad_answer, no_stun_answer or failed; empty when OK. |
| `tls.latencyMs` | number | yes | The round trip in milliseconds; 0 when not OK. |
| `tls.ok` | boolean | yes | Whether the probe succeeded. |

## derp.region.remove v1

Remove a region from the tailnet's DERP map.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.region.remove/v1/plan` |
| apply | `POST /api/v1/actions/derp.region.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `regionId` | integer | yes | The region id, 900 to 999. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `etag` | string | yes | The policy's ETag after the change; empty when nothing changed. |
| `warnings` | array of string | yes | Warnings of the step that computed the policy edits, such as a visibility grant that stays in the policy; empty when there are none. |

## derp.region.set v1

Publish a DERP server as a region of the tailnet's DERP map.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.region.set/v1/plan` |
| apply | `POST /api/v1/actions/derp.region.set/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `acknowledge` | string | no | The typed acknowledgement of omitting the default regions. |
| `hostId` | string | yes | Id of the host running the server. |
| `omitDefaultRegions` | boolean | no | Whether to drop Tailscale's default regions. |
| `regionCode` | string | yes | The region code. |
| `regionId` | integer | yes | The region id, 900 to 999. |
| `regionName` | string | yes | The region name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `etag` | string | yes | The policy's ETag after the change; empty when nothing changed. |
| `warnings` | array of string | yes | Warnings of the step that computed the policy edits, such as a visibility grant that stays in the policy; empty when there are none. |

## derp.server.deploy v1

Pin tailscaled, install derper and open its ports on a host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.server.deploy/v1/plan` |
| apply | `POST /api/v1/actions/derp.server.deploy/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host to run the server on. |
| `hostName` | string | yes | The server's DNS name, or its public IP literal in ip mode. |
| `mode` | string | yes | How derper gets its certificate: letsencrypt or ip. Values: letsencrypt, ip. |
| `verifyClients` | boolean or null | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certName` | string | yes | The certificate pin in ip mode; empty for letsencrypt. |
| `certNotAfter` | string (date-time) or null | yes |  |
| `derperVersion` | string | yes | The derper version installed. |

## derp.server.install v1

Install derper on this host as a systemd service run by its own system user.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/derp.server.install/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/derp.server.install/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostName` | string | yes | The server's DNS name, or its public IP literal in ip mode. |
| `mode` | string | yes | How derper gets its certificate: letsencrypt or ip. Values: letsencrypt, ip. |
| `verifyClients` | boolean or null | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certName` | string | yes | The certificate pin in ip mode; empty for letsencrypt. |
| `certNotAfter` | string (date-time) or null | yes |  |
| `derperVersion` | string | yes | The derper version installed. |

## derp.server.list v1

List the DERP servers the controller found, with their health.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/derp.server.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `availableVersion` | string | yes | The derper version this release carries. |
| `servers` | array of object | yes | The known DERP servers, by host id. |
| `servers[].certName` | string | yes | The certificate pin in ip mode; empty for letsencrypt. |
| `servers[].certNotAfter` | string (date-time) or null | yes |  |
| `servers[].checkedAt` | string (date-time) | yes | When the server was last checked. |
| `servers[].derperVersion` | string | yes | The derper version installed. |
| `servers[].error` | string | yes | Why the last check failed; empty when it succeeded. |
| `servers[].health` | string | yes | The server's health: ok, degraded or broken. |
| `servers[].host` | string | yes | The host's name. |
| `servers[].hostId` | string | yes | Id of the host running the server. |
| `servers[].hostName` | string | yes | The server's DNS name or public IP literal. |
| `servers[].mode` | string | yes | The server's certificate mode: letsencrypt or ip. |
| `servers[].reasons` | array of string | yes | One sentence per health reason; empty when ok. |
| `servers[].regionCode` | string | yes | The server's region code; empty when the region is not published. |
| `servers[].regionId` | integer | yes | The server's region id; 0 when the region is not published. |
| `servers[].unitActive` | boolean | yes | Whether derper's unit is active. |

## derp.server.probe v1

Probe a DERP server's TLS and STUN reachability from the controller and chosen agents, and show which nodes use its region.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/derp.server.probe/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `agents` | array of string | no | Ids of the hosts that probe it; absent picks the other hosts. |
| `hostId` | string | yes | Id of the host running the DERP server. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host running the DERP server. |
| `nodes` | array of object | yes | The tailnet's nodes and how they use the region. |
| `nodes[].address` | string | yes | The node's Tailscale address. |
| `nodes[].hostId` | string | yes | Id of the Darkpanel host of the node; empty when it is none. |
| `nodes[].name` | string | yes | The device name, or the host name when the tailnet is not connected. |
| `nodes[].observed` | boolean | yes | Whether a peer of the node is relayed through this region now. |
| `nodes[].preferred` | string | yes | The node's home region code; empty when unknown. |
| `nodes[].visible` | boolean | yes | Whether the DERP host's tailscaled knows the node. |
| `ready` | boolean | yes | Whether the probes back publishing the region. |
| `regionCode` | string | yes | The server's region code; empty when the region is not published. |
| `regionId` | integer | yes | The server's region id; 0 when the region is not published. |
| `server` | object | yes | The DERP host's status. |
| `server.addresses` | array of string | yes | The node's Tailscale addresses, IPv4 first. |
| `server.availableVersion` | string | yes | The derper version this agent's release carries. |
| `server.certName` | string | yes | The certificate pin in ip mode; empty for letsencrypt. |
| `server.certNotAfter` | string (date-time) or null | yes |  |
| `server.derperVersion` | string | yes | The derper version installed. |
| `server.hostName` | string | yes | The server's DNS name or public IP literal. |
| `server.installed` | boolean | yes | Whether a DERP server is installed on the host. |
| `server.linkViaTailnet` | boolean | yes | Whether the agent reaches its controller over a Tailscale address. |
| `server.mode` | string | yes | The server's certificate mode; empty when not installed. |
| `server.onTailnet` | boolean | yes | Whether tailscaled answers with BackendState Running. |
| `server.tags` | array of string | yes | The node's tags, sorted; empty when none. |
| `server.unitActive` | boolean | yes | Whether derper's unit is active. |
| `server.verifyClients` | boolean | yes | Whether derper admits only nodes of this tailnet. |
| `server.visible` | array of object | yes | One entry per looked-up address, in input order. |
| `server.visible[].address` | string | yes | The address looked up. |
| `server.visible[].node` | string | yes | The node's MagicDNS name without the trailing dot; empty when not visible. |
| `server.visible[].visible` | boolean | yes | Whether a node holds the address. |
| `sources` | array of object | yes | The probes: the controller first, then the agents in input order. |
| `sources[].error` | string | yes | Why the agent read failed; empty when it succeeded. |
| `sources[].name` | string | yes | controller, or the name of the probing host. |
| `sources[].region` | object or null | yes |  |
| `sources[].source` | string | yes | controller, or the id of the probing host. |
| `sources[].stun` | object | yes | The STUN probe. |
| `sources[].stun.error` | string | yes | Why the probe failed: timeout, connection_refused, unreachable, tls_handshake_failed, certificate_not_trusted, pin_mismatch, bad_answer, no_stun_answer or failed; empty when OK. |
| `sources[].stun.latencyMs` | number | yes | The round trip in milliseconds; 0 when not OK. |
| `sources[].stun.ok` | boolean | yes | Whether the probe succeeded. |
| `sources[].tls` | object | yes | The TLS probe of /derp/probe. |
| `sources[].tls.error` | string | yes | Why the probe failed: timeout, connection_refused, unreachable, tls_handshake_failed, certificate_not_trusted, pin_mismatch, bad_answer, no_stun_answer or failed; empty when OK. |
| `sources[].tls.latencyMs` | number | yes | The round trip in milliseconds; 0 when not OK. |
| `sources[].tls.ok` | boolean | yes | Whether the probe succeeded. |
| `warnings` | array of string | yes | Warnings about the probes. |

## derp.server.remove v1

Withdraw a DERP server's region, uninstall derper, unpin tailscaled and close the server's ports on a host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.server.remove/v1/plan` |
| apply | `POST /api/v1/actions/derp.server.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host running the server. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `removed` | array of string | yes | The effect resources removed, in order. |
| `warnings` | array of string | yes | Warnings of the step that computed the policy edits to withdraw the server's region, such as a visibility grant that stays in the policy; empty when there are none. |

## derp.server.status v1

Report the DERP server on this host and the host's place on the tailnet.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/derp.server.status/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `whois` | array of string | no | IP literals to look up on this host's tailscaled. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `addresses` | array of string | yes | The node's Tailscale addresses, IPv4 first. |
| `availableVersion` | string | yes | The derper version this agent's release carries. |
| `certName` | string | yes | The certificate pin in ip mode; empty for letsencrypt. |
| `certNotAfter` | string (date-time) or null | yes |  |
| `derperVersion` | string | yes | The derper version installed. |
| `hostName` | string | yes | The server's DNS name or public IP literal. |
| `installed` | boolean | yes | Whether a DERP server is installed on the host. |
| `linkViaTailnet` | boolean | yes | Whether the agent reaches its controller over a Tailscale address. |
| `mode` | string | yes | The server's certificate mode; empty when not installed. |
| `onTailnet` | boolean | yes | Whether tailscaled answers with BackendState Running. |
| `tags` | array of string | yes | The node's tags, sorted; empty when none. |
| `unitActive` | boolean | yes | Whether derper's unit is active. |
| `verifyClients` | boolean | yes | Whether derper admits only nodes of this tailnet. |
| `visible` | array of object | yes | One entry per looked-up address, in input order. |
| `visible[].address` | string | yes | The address looked up. |
| `visible[].node` | string | yes | The node's MagicDNS name without the trailing dot; empty when not visible. |
| `visible[].visible` | boolean | yes | Whether a node holds the address. |

## derp.server.uninstall v1

Remove the DERP server from this host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/derp.server.uninstall/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/derp.server.uninstall/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `removed` | array of string | yes | The effect resources removed, in order; empty when nothing was installed. |

## derp.server.upgrade v1

Pin tailscaled, then replace derper; between the two steps the versions differ and the region may refuse clients.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/derp.server.upgrade/v1/plan` |
| apply | `POST /api/v1/actions/derp.server.upgrade/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes | Id of the host running the server. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | The derper version replaced. |
| `to` | string | yes | The derper version now installed. |

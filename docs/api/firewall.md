# firewall API

Back to the [API reference](README.md).

## firewall.container.allow v1

Allow access to a published container port.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.container.allow/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.container.allow/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `binding` | object | yes | The published container port to allow. |
| `binding.hostAddr` | string (ip-address) | no |  |
| `binding.hostPort` | integer | yes |  |
| `binding.proto` | string | yes |  |
| `binding.selector` | string | yes |  |
| `from` | string (ip-prefix) | no | The source range allowed; omitted: every source. |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.container.deny v1

Deny a source range on an allowed container port.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.container.deny/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.container.deny/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `binding` | object | yes | The allowed published container port the deny narrows. |
| `binding.hostAddr` | string (ip-address) | no |  |
| `binding.hostPort` | integer | yes |  |
| `binding.proto` | string | yes |  |
| `binding.selector` | string | yes |  |
| `from` | string (ip-prefix) | yes | The source range denied. |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.container.remove v1

Remove a container allowance or deny.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.container.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.container.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `id` | string | yes | The container rule's id, as firewall.docker lists it. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.containers.apply v1

Change container rules in one batch and realize them.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.containers.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.containers.apply/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `ops` | array of object | yes | The operations, applied in order; empty: re-realize the stored rows against the current containers. |
| `ops[].binding` | object | no | The published container port of an allow or deny. |
| `ops[].binding.hostAddr` | string (ip-address) | no |  |
| `ops[].binding.hostPort` | integer | yes |  |
| `ops[].binding.proto` | string | yes |  |
| `ops[].binding.selector` | string | yes |  |
| `ops[].from` | string (ip-prefix) | no | The source range of an allow (omitted: every source) or a deny. |
| `ops[].id` | string | no | The container rule a remove deletes. |
| `ops[].op` | string | yes | What the operation does. Values: allow, deny, remove. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.defaults.set v1

Set UFW's default policies.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.defaults.set/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.defaults.set/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `incoming` | string | no | Default policy for incoming traffic. Values: ACCEPT, DROP, REJECT. |
| `manual` | boolean | no | Confirms the manual rules the change removes, such as the IPv6 rules a reload with IPV6=no discards (spec 2.2). |
| `outgoing` | string | no | Default policy for outgoing traffic. Values: ACCEPT, DROP, REJECT. |
| `overrides` | array of string | no |  |
| `routed` | string | no | Default policy for routed (forwarded) traffic. Values: ACCEPT, DROP, REJECT. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.docker

Read firewall.docker

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/firewall/docker` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `available` | boolean | yes |  |
| `bindings` | array of object | yes |  |
| `bindings[].allowed` | array of string | yes |  |
| `bindings[].binding` | object | yes |  |
| `bindings[].binding.hostAddr` | string (ip-address) | no |  |
| `bindings[].binding.hostPort` | integer | yes |  |
| `bindings[].binding.proto` | string | yes |  |
| `bindings[].binding.selector` | string | yes |  |
| `bindings[].containerPort` | integer | yes |  |
| `bindings[].containers` | array of string | yes |  |
| `bindings[].denied` | array of string | yes |  |
| `bindings[].id` | string | yes |  |
| `bindings[].privateReach` | array of string | no |  |
| `bindings[].proxied` | array of string | no |  |
| `dockerVariant` | string | no |  |
| `driver` | string | no |  |
| `enabled` | boolean | yes |  |
| `foreignForwards` | array of object | no |  |
| `foreignForwards[].destination` | string (ip-address) | yes |  |
| `foreignForwards[].inInterface` | string | no |  |
| `foreignForwards[].origDestination` | string (ip-address) | yes |  |
| `foreignForwards[].origPort` | integer | no |  |
| `foreignForwards[].port` | integer | no |  |
| `foreignForwards[].proto` | string | yes |  |
| `foreignRules` | array of string | no |  |
| `health` | string | yes |  |
| `issues` | array of object | no |  |
| `issues[].level` | string | yes |  |
| `issues[].reason` | string | yes |  |
| `manualRoutes` | array of string | no |  |
| `networks` | array of object | yes |  |
| `networks[].bridge` | string | yes |  |
| `networks[].id` | string | yes |  |
| `networks[].name` | string | yes |  |
| `networks[].protected` | boolean | yes |  |
| `networks[].reason` | string | no |  |
| `networks[].subnets` | array of string | yes |  |
| `rawProtection` | boolean | yes |  |
| `reason` | string | no |  |
| `rules` | array of object | yes |  |
| `rules[].binding` | object | yes |  |
| `rules[].binding.hostAddr` | string (ip-address) | no |  |
| `rules[].binding.hostPort` | integer | yes |  |
| `rules[].binding.proto` | string | yes |  |
| `rules[].binding.selector` | string | yes |  |
| `rules[].from` | string (ip-prefix) | no |  |
| `rules[].id` | string | yes |  |
| `rules[].kind` | string | yes |  |
| `settings` | object | yes |  |
| `settings.forwardExemptions` | array of object | yes |  |
| `settings.forwardExemptions[].destination` | string (ip-address) | yes |  |
| `settings.forwardExemptions[].inInterface` | string | no |  |
| `settings.forwardExemptions[].origDestination` | string (ip-address) | yes |  |
| `settings.forwardExemptions[].origPort` | integer | no |  |
| `settings.forwardExemptions[].port` | integer | no |  |
| `settings.forwardExemptions[].proto` | string | yes |  |
| `settings.privateSources` | array of string (ip-prefix) | yes |  |
| `supported` | boolean | yes |  |
| `ufwVariant` | string | no |  |
| `unsupported` | array of string | no |  |
| `version` | string | no |  |

## firewall.docker.disable v1

Disable the Docker integration.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.docker.disable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.docker.disable/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.docker.enable v1

Enable the Docker integration.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.docker.enable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.docker.enable/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.docker.repair v1

Rewrite and reload the Docker integration's block.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.docker.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.docker.repair/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `discardForeign` | boolean | no | Discards the foreign rules DOCKER-USER holds; the plan lists each. Needs manual: true. |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `manual` | boolean | no | Confirms discardForeign. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.docker.set-sources v1

Set the Docker integration's private sources and forward exemptions.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.docker.set-sources/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.docker.set-sources/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `forwardExemptions` | array of object | no | The foreign port forwards into non-Docker destinations that the block lets through; omitted: kept. |
| `forwardExemptions[].destination` | string (ip-address) | yes |  |
| `forwardExemptions[].inInterface` | string | no |  |
| `forwardExemptions[].origDestination` | string (ip-address) | yes |  |
| `forwardExemptions[].origPort` | integer | no |  |
| `forwardExemptions[].port` | integer | no |  |
| `forwardExemptions[].proto` | string | yes |  |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `overrides` | array of string | no |  |
| `privateSources` | array of string (ip-prefix) | no | The source ranges that reach every Docker network; omitted: kept. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.exposure

Read firewall.exposure

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/firewall/exposure` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `notes` | array of string | no |  |
| `rows` | array of object | yes |  |
| `rows[].addr` | string | yes |  |
| `rows[].classes` | array of object | yes |  |
| `rows[].classes[].allowed` | array of object | no |  |
| `rows[].classes[].allowed[].decidedBy` | string | yes |  |
| `rows[].classes[].allowed[].ifaces` | array of string | no |  |
| `rows[].classes[].allowed[].sources` | array of string | yes |  |
| `rows[].classes[].allowed[].srcPorts` | array of string | no |  |
| `rows[].classes[].class` | string | yes |  |
| `rows[].classes[].decidedBy` | string | no |  |
| `rows[].classes[].note` | string | no |  |
| `rows[].classes[].range` | string | no |  |
| `rows[].classes[].verdict` | string | yes |  |
| `rows[].container` | object or null | no |  |
| `rows[].family` | string | yes |  |
| `rows[].port` | integer | yes |  |
| `rows[].process` | string | no |  |
| `rows[].proto` | string | yes |  |

## firewall.logging.set v1

Set UFW's logging level.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.logging.set/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.logging.set/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `level` | string | yes | UFW's logging level. Values: off, low, medium, high, full. |
| `manual` | boolean | no | Confirms the manual rules the change removes, such as the IPv6 rules a reload with IPV6=no discards (spec 2.2). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.rule.add v1

Add a firewall rule.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.rule.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.rule.add/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `manual` | boolean | no | Confirms the manual rules the change removes, such as the IPv6 rules a reload with IPV6=no discards (spec 2.2). |
| `overrides` | array of string | no |  |
| `rule` | object | yes |  |
| `rule.action` | string | yes | What the rule does; limit only for incoming rules. Values: allow, deny, limit, reject. |
| `rule.comment` | string | no | Label after the Darkpanel tag, or a manual rule's comment. |
| `rule.direction` | string | yes | Incoming, outgoing or routed (forwarded) traffic. Values: in, out, route. |
| `rule.family` | string | yes | IP family; both only when from and to are any. Values: v4, v6, both. |
| `rule.from` | string | yes | Source: any, an address or a CIDR in canonical form. |
| `rule.fromPort` | string | no | Source port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |
| `rule.id` | string | no | Rule id: the ULID in a Darkpanel rule's tag, or manual- and 16 hex digits derived from a manual rule's tuple. |
| `rule.inIface` | string | no | Interface the packet arrives on; only for in and route rules. |
| `rule.log` | string | no | Logging; off when omitted. Values: off, log, log-all. |
| `rule.origin` | string | no | Who made the rule; darkpanel when omitted. Values: darkpanel, manual. |
| `rule.outIface` | string | no | Interface the packet leaves on; only for out and route rules. |
| `rule.proto` | string | yes | Protocol; ah, esp, gre, ipv6 and igmp take no ports, ipv6 and igmp only with family v4. Values: tcp, udp, any, ah, esp, gre, ipv6, igmp. |
| `rule.raw` | string | no | Read-only: the tuple of a manual rule the model cannot express; never accepted in input. |
| `rule.to` | string | yes | Destination: any, an address or a CIDR in canonical form. |
| `rule.toPort` | string | no | Destination port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.rule.delete v1

Delete a firewall rule.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.rule.delete/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.rule.delete/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `id` | string | yes | The rule's id, as firewall.status lists it. |
| `manual` | boolean | no | Confirms deleting a manual rule (spec 2.2). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.rules.apply v1

Add and delete a batch of firewall rules.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.rules.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.rules.apply/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `add` | array of object | no | Rules to add, each with its id and without a comment. |
| `add[].action` | string | yes | What the rule does; limit only for incoming rules. Values: allow, deny, limit, reject. |
| `add[].comment` | string | no | Label after the Darkpanel tag, or a manual rule's comment. |
| `add[].direction` | string | yes | Incoming, outgoing or routed (forwarded) traffic. Values: in, out, route. |
| `add[].family` | string | yes | IP family; both only when from and to are any. Values: v4, v6, both. |
| `add[].from` | string | yes | Source: any, an address or a CIDR in canonical form. |
| `add[].fromPort` | string | no | Source port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |
| `add[].id` | string | no | Rule id: the ULID in a Darkpanel rule's tag, or manual- and 16 hex digits derived from a manual rule's tuple. |
| `add[].inIface` | string | no | Interface the packet arrives on; only for in and route rules. |
| `add[].log` | string | no | Logging; off when omitted. Values: off, log, log-all. |
| `add[].origin` | string | no | Who made the rule; darkpanel when omitted. Values: darkpanel, manual. |
| `add[].outIface` | string | no | Interface the packet leaves on; only for out and route rules. |
| `add[].proto` | string | yes | Protocol; ah, esp, gre, ipv6 and igmp take no ports, ipv6 and igmp only with family v4. Values: tcp, udp, any, ah, esp, gre, ipv6, igmp. |
| `add[].raw` | string | no | Read-only: the tuple of a manual rule the model cannot express; never accepted in input. |
| `add[].to` | string | yes | Destination: any, an address or a CIDR in canonical form. |
| `add[].toPort` | string | no | Destination port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |
| `delete` | array of string | no | Ids of rules to delete. |
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `keepManual` | boolean | no | Leaves out an added rule that a manual rule with another comment already holds in every family it covers. |
| `label` | string | no | The comment of every added rule, after the Darkpanel tag. |
| `manual` | boolean | no | Confirms deleting manual rules (spec 2.2). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.snapshot.compare v1

Compare two firewall snapshots, or one with the host now.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/firewall.snapshot.compare/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | yes | The older snapshot's id. |
| `to` | string | no | The newer snapshot's id; omitted: the host now. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `diff` | array of object | yes |  |
| `diff[].op` | string | yes |  |
| `diff[].text` | string | yes |  |
| `files` | array of object | yes |  |
| `files[].from` | string | yes |  |
| `files[].path` | string | yes |  |
| `files[].to` | string | yes |  |
| `from` | string | yes |  |
| `to` | string | yes |  |

## firewall.snapshot.restore v1

Restore a firewall snapshot.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.snapshot.restore/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.snapshot.restore/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `id` | string | yes | The snapshot's id, as firewall.snapshots lists it. |
| `manual` | boolean | no | Confirms the manual rules the restore adds or removes (spec 2.5). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.snapshots

Read firewall.snapshots

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/firewall/snapshots` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `snapshots` | array of object | yes |  |
| `snapshots[].actor` | string | yes |  |
| `snapshots[].baseline` | boolean | yes |  |
| `snapshots[].changeId` | string | no |  |
| `snapshots[].createdAt` | string (date-time) | yes |  |
| `snapshots[].enabled` | boolean | yes |  |
| `snapshots[].id` | string | yes |  |
| `snapshots[].pinned` | boolean | yes |  |
| `snapshots[].system` | boolean | yes |  |

## firewall.state.disable v1

Disable UFW.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.state.disable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.state.disable/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `manual` | boolean | no | Confirms the manual rules the change removes, such as the IPv6 rules a start with IPV6=no discards (spec 2.2). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.state.enable v1

Enable UFW.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.state.enable/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.state.enable/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `manual` | boolean | no | Confirms the manual rules the change removes, such as the IPv6 rules a start with IPV6=no discards (spec 2.2). |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.state.repair v1

Repair the firewall: bring UFW's loaded state to its target.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/firewall.state.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/firewall.state.repair/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `guard` | object | no |  |
| `guard.agentListen` | array of string (ip-address-port) | no |  |
| `guard.agentPeers` | array of string (ip-address) | no |  |
| `guard.client` | object | no |  |
| `guard.client.addr` | string (ip-address) | yes |  |
| `guard.client.port` | integer | yes |  |
| `guard.complete` | boolean | no |  |
| `guard.panelListen` | array of string (ip-address-port) | no |  |
| `guard.via` | object | no |  |
| `guard.via.exposureId` | string | yes |  |
| `guard.via.kind` | string | yes |  |
| `guard.via.listenerAddr` | string (ip-address-port) | yes |  |
| `guard.via.proxyAddr` | string (ip-address) | yes |  |
| `guard.via.public` | array of string (ip-address-port) | yes |  |
| `manual` | boolean | no | Confirms the manual rules a restore brings back or removes (spec 2.5). |
| `outcome` | string | no | Repairing a change: restore (the default) or keep. Values: restore, keep. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `repairOutcome` | string | no |  |
| `rules` | array of string | no |  |
| `snapshot` | string | yes |  |

## firewall.status

Read firewall.status

| Operation | Request |
|---|---|
| read | `GET /api/v1/hosts/{host}/resources/firewall/status` |

Minimum scope: `read`.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `active` | boolean | yes |  |
| `configured` | object | yes |  |
| `configured.defaults` | object | yes |  |
| `configured.defaults.incoming` | string | yes |  |
| `configured.defaults.outgoing` | string | yes |  |
| `configured.defaults.routed` | string | yes |  |
| `configured.enabled` | boolean | yes |  |
| `configured.ipv6` | boolean | yes |  |
| `configured.logLevel` | string | yes |  |
| `configured.sysctlFile` | string | no |  |
| `inSync` | boolean | yes |  |
| `loaded` | object | yes |  |
| `loaded.chains` | array of object | yes |  |
| `loaded.chains[].chain` | string | yes |  |
| `loaded.chains[].family` | string | yes |  |
| `loaded.chains[].policy` | string | no |  |
| `loaded.chains[].rules` | integer | yes |  |
| `loaded.chains[].table` | string | yes |  |
| `loaded.ipv6` | boolean | yes |  |
| `loaded.sysctl` | map of string | yes |  |
| `loaded.variant` | string | yes |  |
| `outOfSync` | array of object | yes |  |
| `outOfSync[].chain` | string | yes |  |
| `outOfSync[].family` | string | yes |  |
| `outOfSync[].onlyLoaded` | array of string | no |  |
| `outOfSync[].onlyPredicted` | array of string | no |  |
| `outOfSync[].orderDiffers` | boolean | no |  |
| `outOfSync[].policyLoaded` | string | no |  |
| `outOfSync[].policyPredicted` | string | no |  |
| `outOfSync[].table` | string | yes |  |
| `rules` | array of object | yes |  |
| `rules[].action` | string | yes | What the rule does; limit only for incoming rules. Values: allow, deny, limit, reject. |
| `rules[].comment` | string | no | Label after the Darkpanel tag, or a manual rule's comment. |
| `rules[].direction` | string | yes | Incoming, outgoing or routed (forwarded) traffic. Values: in, out, route. |
| `rules[].family` | string | yes | IP family; both only when from and to are any. Values: v4, v6, both. |
| `rules[].from` | string | yes | Source: any, an address or a CIDR in canonical form. |
| `rules[].fromPort` | string | no | Source port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |
| `rules[].id` | string | no | Rule id: the ULID in a Darkpanel rule's tag, or manual- and 16 hex digits derived from a manual rule's tuple. |
| `rules[].inIface` | string | no | Interface the packet arrives on; only for in and route rules. |
| `rules[].log` | string | no | Logging; off when omitted. Values: off, log, log-all. |
| `rules[].origin` | string | no | Who made the rule; darkpanel when omitted. Values: darkpanel, manual. |
| `rules[].outIface` | string | no | Interface the packet leaves on; only for out and route rules. |
| `rules[].proto` | string | yes | Protocol; ah, esp, gre, ipv6 and igmp take no ports, ipv6 and igmp only with family v4. Values: tcp, udp, any, ah, esp, gre, ipv6, igmp. |
| `rules[].raw` | string | no | Read-only: the tuple of a manual rule the model cannot express; never accepted in input. |
| `rules[].to` | string | yes | Destination: any, an address or a CIDR in canonical form. |
| `rules[].toPort` | string | no | Destination port, range a:b or ascending comma list of at most 15 multiport slots; ranges and lists need tcp or udp. |
| `supported` | boolean | yes |  |
| `syncError` | string | no |  |
| `unsupported` | array of string | yes |  |

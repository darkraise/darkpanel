# npm API

Back to the [API reference](README.md).

## npm.accesslist.add v1

Create an IP access list in Nginx Proxy Manager.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.accesslist.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.accesslist.add/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `clients` | array of object | yes |  |
| `clients[].address` | string | yes |  |
| `clients[].directive` | string | yes | Values: allow, deny. |
| `satisfyAny` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `clients` | array of object | yes |  |
| `clients[].address` | string | yes |  |
| `clients[].directive` | string | yes | Values: allow, deny. |
| `id` | integer | yes |  |
| `name` | string | yes |  |
| `reserved` | string | yes |  |
| `satisfyAny` | boolean | yes |  |
| `unclaimed` | boolean | yes |  |

## npm.accesslist.list v1

List the access lists of Nginx Proxy Manager.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/npm.accesslist.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `items` | array of object | yes |  |
| `items[].clients` | array of object | yes |  |
| `items[].clients[].address` | string | yes |  |
| `items[].clients[].directive` | string | yes | Values: allow, deny. |
| `items[].id` | integer | yes |  |
| `items[].name` | string | yes |  |
| `items[].reserved` | string | yes |  |
| `items[].satisfyAny` | boolean | yes |  |
| `items[].unclaimed` | boolean | yes |  |

## npm.accesslist.remove v1

Delete an access list from Nginx Proxy Manager.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.accesslist.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.accesslist.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | integer | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `clients` | array of object | yes |  |
| `clients[].address` | string | yes |  |
| `clients[].directive` | string | yes | Values: allow, deny. |
| `id` | integer | yes |  |
| `name` | string | yes |  |
| `reserved` | string | yes |  |
| `satisfyAny` | boolean | yes |  |
| `unclaimed` | boolean | yes |  |

## npm.admin.expose v1

Publish Nginx Proxy Manager's admin UI over HTTPS on a hostname, with a certificate, forced SSL and HSTS.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/npm.admin.expose/v1/plan` |
| apply | `POST /api/v1/actions/npm.admin.expose/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | no |  |
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certificateId` | integer | yes |  |
| `proxyHostId` | integer | yes |  |

## npm.admin.reveal-password v1

Reveal the Nginx Proxy Manager administrator's password.

| Operation | Request |
|---|---|
| reveal | `POST /api/v1/hosts/{host}/actions/npm.admin.reveal-password/v1/reveal` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `value` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `email` | string | yes |  |
| `password` | string | yes | Secret. |

## npm.admin.unexpose v1

Remove the proxy host that publishes Nginx Proxy Manager's admin UI; its certificate stays.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/npm.admin.unexpose/v1/plan` |
| apply | `POST /api/v1/actions/npm.admin.unexpose/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostId` | string | yes |  |
| `hostname` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `hostname` | string | yes |  |

## npm.certificate.list v1

List the certificates of Nginx Proxy Manager.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/npm.certificate.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `items` | array of object | yes |  |
| `items[].domains` | array of string | yes |  |
| `items[].expiresOn` | string | yes |  |
| `items[].id` | integer | yes |  |
| `items[].name` | string | yes |  |
| `items[].reserved` | string | yes |  |
| `items[].unclaimed` | boolean | yes |  |

## npm.certificate.remove v1

Delete a certificate from Nginx Proxy Manager.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.certificate.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.certificate.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `id` | integer | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `domains` | array of string | yes |  |
| `expiresOn` | string | yes |  |
| `id` | integer | yes |  |
| `name` | string | yes |  |
| `reserved` | string | yes |  |
| `unclaimed` | boolean | yes |  |

## npm.certificate.request v1

Request a Let's Encrypt certificate through Nginx Proxy Manager.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.certificate.request/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.certificate.request/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `domains` | array of string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `domains` | array of string | yes |  |
| `expiresOn` | string | yes |  |
| `id` | integer | yes |  |
| `name` | string | yes |  |
| `reserved` | string | yes |  |
| `unclaimed` | boolean | yes |  |

## npm.expose.container v1

Attach the proxy to a container's network, request a certificate and add a proxy host that forwards to the container.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/npm.expose.container/v1/plan` |
| apply | `POST /api/v1/actions/npm.expose.container/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | no |  |
| `container` | string | yes |  |
| `domains` | array of string | yes |  |
| `hostId` | string | yes |  |
| `network` | string | yes |  |
| `port` | integer | yes |  |
| `websockets` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `certificateId` | integer | yes |  |
| `proxyHostId` | integer | yes |  |

## npm.expose.remove v1

Remove an exposed container's proxy host and its network claims.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/npm.expose.remove/v1/plan` |
| apply | `POST /api/v1/actions/npm.expose.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `domain` | string | yes |  |
| `hostId` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `domain` | string | yes |  |

## npm.instance.confirm-public v1

Record that ports 80 and 443 of this host are reachable from the internet.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.instance.confirm-public/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.instance.confirm-public/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `public` | string | yes | Values: no, confirmed. |

## npm.instance.deploy-public v1

Deploy Nginx Proxy Manager and, when the host's Docker firewall integration is on, open ports 80 and 443 for it.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/npm.instance.deploy-public/v1/plan` |
| apply | `POST /api/v1/actions/npm.instance.deploy-public/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `acmeEmail` | string | yes |  |
| `hostId` | string | yes |  |
| `integration` | boolean | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containerId` | string | yes |  |
| `portsOpened` | boolean | yes |  |

## npm.instance.deploy v1

Deploy Nginx Proxy Manager in a Darkpanel-owned container and create its administrator.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.instance.deploy/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.instance.deploy/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `acmeEmail` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `adminEmail` | string | yes |  |
| `containerId` | string | yes |  |
| `image` | string | yes |  |

## npm.instance.list v1

List the Nginx Proxy Manager instance status of every host.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/npm.instance.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `instances` | array of object | yes |  |
| `instances[].hostId` | string | yes |  |
| `instances[].hostName` | string | yes |  |
| `instances[].status` | object | yes |  |
| `instances[].status.adminEmail` | string | yes |  |
| `instances[].status.adminExposure` | object or null | yes |  |
| `instances[].status.apiReady` | boolean | yes |  |
| `instances[].status.containerId` | string | yes |  |
| `instances[].status.deployed` | boolean | yes |  |
| `instances[].status.image` | string | yes |  |
| `instances[].status.pinnedImage` | string | yes |  |
| `instances[].status.public` | string | yes | Values: no, confirmed. |
| `instances[].status.running` | boolean | yes |  |
| `instances[].status.upgradeAvailable` | boolean | yes |  |

## npm.instance.remove v1

Remove the Nginx Proxy Manager instance and its data.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.instance.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.instance.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `containerId` | string | yes |  |

## npm.instance.status v1

Show the Nginx Proxy Manager instance of this host and whether it runs and answers.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/npm.instance.status/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `adminEmail` | string | yes |  |
| `adminExposure` | object or null | yes |  |
| `apiReady` | boolean | yes |  |
| `containerId` | string | yes |  |
| `deployed` | boolean | yes |  |
| `image` | string | yes |  |
| `pinnedImage` | string | yes |  |
| `public` | string | yes | Values: no, confirmed. |
| `running` | boolean | yes |  |
| `upgradeAvailable` | boolean | yes |  |

## npm.instance.upgrade v1

Recreate the Nginx Proxy Manager container from the pinned image, archiving its volumes first.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.instance.upgrade/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.instance.upgrade/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `fromImage` | string | yes |  |
| `toImage` | string | yes |  |

## npm.network.attach v1

Attach Nginx Proxy Manager to a network.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.network.attach/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.network.attach/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `address` | string (ip-address) | no |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `target` | string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `attached` | boolean | yes |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `preexisting` | boolean | yes |  |

## npm.network.detach v1

Release an attachment claim.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.network.detach/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.network.detach/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `claimant` | string | yes |  |
| `network` | string | yes |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `attached` | boolean | yes |  |
| `claimant` | string | yes |  |
| `network` | string | yes |  |
| `preexisting` | boolean | yes |  |

## npm.proxyhost.add v1

Add a proxy host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.add/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | no |  |
| `certificateId` | integer | no |  |
| `domains` | array of string | yes |  |
| `forwardHost` | string | yes |  |
| `forwardPort` | integer | yes |  |
| `forwardScheme` | string | yes | Values: http, https. |
| `hsts` | boolean | no |  |
| `sslForced` | boolean | no |  |
| `websockets` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | yes |  |
| `certificateId` | integer | yes |  |
| `domains` | array of string | yes |  |
| `forwardHost` | string | yes |  |
| `forwardPort` | integer | yes |  |
| `forwardScheme` | string | yes |  |
| `hsts` | boolean | yes |  |
| `id` | integer | yes |  |
| `reserved` | string | yes |  |
| `sslForced` | boolean | yes |  |
| `unclaimed` | boolean | yes |  |
| `websockets` | boolean | yes |  |

## npm.proxyhost.list v1

List the proxy hosts of Nginx Proxy Manager.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `items` | array of object | yes |  |
| `items[].accessListId` | integer | yes |  |
| `items[].certificateId` | integer | yes |  |
| `items[].domains` | array of string | yes |  |
| `items[].forwardHost` | string | yes |  |
| `items[].forwardPort` | integer | yes |  |
| `items[].forwardScheme` | string | yes |  |
| `items[].hsts` | boolean | yes |  |
| `items[].id` | integer | yes |  |
| `items[].reserved` | string | yes |  |
| `items[].sslForced` | boolean | yes |  |
| `items[].unclaimed` | boolean | yes |  |
| `items[].websockets` | boolean | yes |  |

## npm.proxyhost.remove v1

Delete a proxy host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.remove/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `domain` | string | no |  |
| `id` | integer | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | yes |  |
| `certificateId` | integer | yes |  |
| `domains` | array of string | yes |  |
| `forwardHost` | string | yes |  |
| `forwardPort` | integer | yes |  |
| `forwardScheme` | string | yes |  |
| `hsts` | boolean | yes |  |
| `id` | integer | yes |  |
| `reserved` | string | yes |  |
| `sslForced` | boolean | yes |  |
| `unclaimed` | boolean | yes |  |
| `websockets` | boolean | yes |  |

## npm.proxyhost.update v1

Change the settings of a proxy host.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.update/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/npm.proxyhost.update/v1/apply` |

Minimum scope: `operate`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | no |  |
| `certificateId` | integer | no |  |
| `domains` | array of string | yes |  |
| `forwardHost` | string | yes |  |
| `forwardPort` | integer | yes |  |
| `forwardScheme` | string | yes | Values: http, https. |
| `hsts` | boolean | no |  |
| `id` | integer | yes |  |
| `sslForced` | boolean | no |  |
| `websockets` | boolean | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `accessListId` | integer | yes |  |
| `certificateId` | integer | yes |  |
| `domains` | array of string | yes |  |
| `forwardHost` | string | yes |  |
| `forwardPort` | integer | yes |  |
| `forwardScheme` | string | yes |  |
| `hsts` | boolean | yes |  |
| `id` | integer | yes |  |
| `reserved` | string | yes |  |
| `sslForced` | boolean | yes |  |
| `unclaimed` | boolean | yes |  |
| `websockets` | boolean | yes |  |

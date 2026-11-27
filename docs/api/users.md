# users API

Back to the [API reference](README.md).

## users.key.add v1

Authorize one SSH public key for an account.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.key.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.key.add/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `key` | string | yes | One public key line without options: ed25519, ecdsa, sk-ed25519, sk-ecdsa or RSA of at least 3072 bits. |
| `name` | string | yes | The account name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `fingerprint` | string | yes |  |
| `name` | string | yes |  |
| `removed` | array of string | no | The removed key lines without their options; users.key.add takes them back only for the key types and sizes it accepts. |

## users.key.list v1

List an account's authorized SSH keys: type, bits, fingerprint, comment and options.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/users.key.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | The account name. |

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | array of object | yes |  |
| `keys[].bits` | integer | yes |  |
| `keys[].comment` | string | yes |  |
| `keys[].fingerprint` | string | yes | SHA256:\<base64\>, as ssh-keygen -l prints it. |
| `keys[].options` | string | yes | The line's options as text; empty when it has none. |
| `keys[].type` | string | yes |  |

## users.key.remove v1

Remove an authorized SSH key from an account.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.key.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.key.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `fingerprint` | string | yes | The key's SHA256 fingerprint. |
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
| `name` | string | yes | The account name. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `fingerprint` | string | yes |  |
| `name` | string | yes |  |
| `removed` | array of string | no | The removed key lines without their options; users.key.add takes them back only for the key types and sizes it accepts. |

## users.password.remove v1

Remove an account's password: its hash becomes *.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.password.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.password.remove/v1/apply` |

Minimum scope: `admin`.

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
| `name` | string | yes | The account name. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.password.set v1

Set an account's password; it is never logged or returned.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.password.set/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.password.set/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | The account name. |
| `password` | string | yes | 8-256 characters with no control character. Secret. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.sshd.apply v1

Write sshd's settings to 00-darkpanel.conf and reload sshd; unconfirmed, the change reverts.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.sshd.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.sshd.apply/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `allowGroups` | array of string | no | Group patterns; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `allowUsers` | array of string | no | User patterns, USER or USER@HOST; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `clientAliveCountMax` | integer or null | no |  |
| `clientAliveInterval` | integer or null | no |  |
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
| `kbdInteractiveAuthentication` | boolean or null | no |  |
| `loginGraceTime` | integer or null | no |  |
| `maxAuthTries` | integer or null | no |  |
| `overrides` | array of string | no |  |
| `passwordAuthentication` | boolean or null | no |  |
| `permitRootLogin` | string | no | Values: yes, prohibit-password, no. |
| `x11Forwarding` | boolean or null | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `reloaded` | boolean | yes | ssh.service was active and reloaded; under socket activation new connections read the file. |
| `set` | object | yes |  |
| `set.allowGroups` | array of string | no | Group patterns; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `set.allowUsers` | array of string | no | User patterns, USER or USER@HOST; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `set.clientAliveCountMax` | integer or null | no |  |
| `set.clientAliveInterval` | integer or null | no |  |
| `set.kbdInteractiveAuthentication` | boolean or null | no |  |
| `set.loginGraceTime` | integer or null | no |  |
| `set.maxAuthTries` | integer or null | no |  |
| `set.passwordAuthentication` | boolean or null | no |  |
| `set.permitRootLogin` | string | no | Values: yes, prohibit-password, no. |
| `set.x11Forwarding` | boolean or null | no |  |

## users.sshd.get v1

Read sshd's effective settings and those Darkpanel set.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/users.sshd.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `effective` | object | yes | The values sshd -T reports. |
| `effective.allowGroups` | array of string | no |  |
| `effective.allowUsers` | array of string | no |  |
| `effective.clientAliveCountMax` | integer or null | no |  |
| `effective.clientAliveInterval` | integer or null | no |  |
| `effective.kbdInteractiveAuthentication` | boolean or null | no |  |
| `effective.loginGraceTime` | integer or null | no |  |
| `effective.maxAuthTries` | integer or null | no |  |
| `effective.passwordAuthentication` | boolean or null | no |  |
| `effective.permitRootLogin` | string | no | Values: yes, prohibit-password, forced-commands-only, no. |
| `effective.x11Forwarding` | boolean or null | no |  |
| `listsSetBy` | string | no | The file under /etc/ssh that sets AllowUsers, AllowGroups, DenyUsers or DenyGroups. |
| `matchIn` | string | no | A file holding a Match block, whose overrides sshd -T and the lockout guard do not see. |
| `readOnly` | array of string | yes | The fields Darkpanel leaves to another file: allowUsers and allowGroups when listsSetBy is set. |
| `set` | object | yes | The values 00-darkpanel.conf holds; empty when the file is absent. |
| `set.allowGroups` | array of string | no | Group patterns; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `set.allowUsers` | array of string | no | User patterns, USER or USER@HOST; managed only when no other file under /etc/ssh sets an allow or deny list. |
| `set.clientAliveCountMax` | integer or null | no |  |
| `set.clientAliveInterval` | integer or null | no |  |
| `set.kbdInteractiveAuthentication` | boolean or null | no |  |
| `set.loginGraceTime` | integer or null | no |  |
| `set.maxAuthTries` | integer or null | no |  |
| `set.passwordAuthentication` | boolean or null | no |  |
| `set.permitRootLogin` | string | no | Values: yes, prohibit-password, no. |
| `set.x11Forwarding` | boolean or null | no |  |

## users.sshd.repair v1

Rewrite 00-darkpanel.conf from the last confirmed settings, or remove it, and reload sshd.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.sshd.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.sshd.repair/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | no | The confirmed users.sshd.apply change the file was rewritten from; empty when none was confirmed and the file was removed. |
| `reloaded` | boolean | yes |  |

## users.sudo.add v1

Add an account to the sudo group.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.sudo.add/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.sudo.add/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | The account name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.sudo.remove v1

Remove an account from the sudo group.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.sudo.remove/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.sudo.remove/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | The account name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.user.create v1

Create a login account with its home and group, optionally in sudo and with a password.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.user.create/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.user.create/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `keys` | array of string | no | SSH public key lines to authorize, as users.key.add takes them. |
| `name` | string | yes | A new account name. |
| `password` | string | no | An optional password, 8-256 characters with no control character. Secret. |
| `shell` | string | no | A shell listed in /etc/shells; /bin/bash when omitted. |
| `sudo` | boolean | no | Add the account to the sudo group. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.user.delete v1

Delete an account, optionally with its home.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.user.delete/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.user.delete/v1/apply` |

Minimum scope: `admin`.

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
| `name` | string | yes | The account name. |
| `overrides` | array of string | no |  |
| `removeHome` | boolean | no | Delete the home directory and mail spool too. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.user.list v1

List root and the login accounts with UIDs 1000-59999.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/users.user.list/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `accounts` | array of object | yes |  |
| `accounts[].groups` | array of string | yes |  |
| `accounts[].home` | string | yes |  |
| `accounts[].keys` | integer | yes | The number of authorized keys. |
| `accounts[].locked` | boolean | yes | The account expired today or earlier, as users.user.lock leaves it; a ! password alone is a key-only account and not locked. |
| `accounts[].name` | string | yes |  |
| `accounts[].passwordSet` | boolean | yes | passwd -S reports a usable password (status P). |
| `accounts[].shell` | string | yes |  |
| `accounts[].sudo` | boolean | yes | A member of the sudo group. |
| `accounts[].uid` | integer | yes |  |

## users.user.lock v1

Lock an account: its expiry ends key and password sign-in.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.user.lock/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.user.lock/v1/apply` |

Minimum scope: `admin`.

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
| `name` | string | yes | The account name. |
| `overrides` | array of string | no |  |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

## users.user.unlock v1

Unlock an account, restoring the expiry it had.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/users.user.unlock/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/users.user.unlock/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes | The account name. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | yes |  |

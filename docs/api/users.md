# users API

Back to the [API reference](README.md).

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
| `name` | string | yes | The account name. |

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
| `name` | string | yes | The account name. |
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
| `name` | string | yes | The account name. |

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

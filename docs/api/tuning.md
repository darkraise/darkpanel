# tuning API

Back to the [API reference](README.md).

## tuning.sysctl.apply v1

Set kernel parameters now and in 99-zz-darkpanel.conf; unconfirmed, the change reverts.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tuning.sysctl.apply/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tuning.sysctl.apply/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `values` | map of string | yes | Value by dotted key, a port range as LOW HIGH; each is written live and to the file. A key left out leaves the file and keeps its live value; an empty object removes the file. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `set` | map of string | yes |  |
| `written` | array of string | yes | The keys whose live value changed. |

## tuning.sysctl.get v1

Read the kernel parameters Darkpanel tunes: live, set by Darkpanel, and later files.

| Operation | Request |
|---|---|
| read | `POST /api/v1/hosts/{host}/actions/tuning.sysctl.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `file` | string | yes |  |
| `keys` | array of object | yes |  |
| `keys[].choices` | array of string | no | Choice kind: the values the key takes on this host. |
| `keys[].help` | string | no |  |
| `keys[].key` | string | yes | The dotted name, as sysctl.d files write it. |
| `keys[].kind` | string | no | Empty for a key another module manages. Values: integer, choice, port-range. |
| `keys[].laterFiles` | array of string | no | Files that set the key after 99-zz-darkpanel.conf at boot, in the order they apply. |
| `keys[].live` | string | no | The kernel's value now; a port range as LOW HIGH. |
| `keys[].max` | string | no | Integer and port-range kinds: the highest value, in decimal. |
| `keys[].min` | string | no | Integer and port-range kinds: the lowest value, in decimal (fs.file-max exceeds a JavaScript number). |
| `keys[].owner` | string | no | The module that manages the key; tuning does not change it. |
| `keys[].set` | string | no | The value 99-zz-darkpanel.conf holds. |
| `keys[].unavailable` | string | no | Why the key cannot be set on this host. |

## tuning.sysctl.repair v1

Rewrite 99-zz-darkpanel.conf and the live values from the last confirmed settings.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/hosts/{host}/actions/tuning.sysctl.repair/v1/plan` |
| apply | `POST /api/v1/hosts/{host}/actions/tuning.sysctl.repair/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `from` | string | no | The confirmed tuning.sysctl.apply change the file was rewritten from; empty when none was confirmed and the file was removed. |
| `written` | array of string | yes |  |

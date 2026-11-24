# auth API

Back to the [API reference](README.md).

## auth.oidc.clear v1

Turn OIDC sign-in off; the users' links to the provider are kept.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/auth.oidc.clear/v1/plan` |
| apply | `POST /api/v1/actions/auth.oidc.clear/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `oidc` | object or null | yes |  |
| `tailscale` | object | yes | Tailscale sign-in. |
| `tailscale.enabled` | boolean | yes | Whether Tailscale sign-in is offered. |
| `tailscale.requireTotp` | boolean | yes | Whether every Continue as asks for the authenticator code, remembered node or not. |

## auth.oidc.set v1

Set up OIDC sign-in with one provider; changing the issuer keeps the users' links to the old one.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/auth.oidc.set/v1/plan` |
| apply | `POST /api/v1/actions/auth.oidc.set/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `clientId` | string | yes | The client id the provider issued to Darkpanel. |
| `clientSecret` | string | no | The client secret; write-only. Omit it to keep the stored one. Secret. |
| `displayName` | string | yes | The sign-in button's text, 1 to 32 characters. |
| `issuer` | string | yes | The provider's issuer, an https URL; discovery reads \<issuer\>/.well-known/openid-configuration. |
| `redirectBase` | string | yes | The https origin the provider redirects back to: one of the panel's HTTPS listeners or runtime listener public hosts. |
| `requireTotp` | boolean | no | Ask for the user's authenticator code after the provider; false when omitted. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `oidc` | object or null | yes |  |
| `tailscale` | object | yes | Tailscale sign-in. |
| `tailscale.enabled` | boolean | yes | Whether Tailscale sign-in is offered. |
| `tailscale.requireTotp` | boolean | yes | Whether every Continue as asks for the authenticator code, remembered node or not. |

## auth.settings.get v1

Read the sign-in method settings; the OIDC client secret is never returned.

| Operation | Request |
|---|---|
| read | `POST /api/v1/actions/auth.settings.get/v1/read` |

Minimum scope: `read`.

### Input

Sent in the `input` field of the request body.

This action takes no input fields.

### Output

Returned in the `output` field of the response.

| Field | Type | Required | Description |
|---|---|---|---|
| `oidc` | object or null | yes |  |
| `tailscale` | object | yes | Tailscale sign-in. |
| `tailscale.enabled` | boolean | yes | Whether Tailscale sign-in is offered. |
| `tailscale.requireTotp` | boolean | yes | Whether every Continue as asks for the authenticator code, remembered node or not. |

## auth.tailscale.set v1

Turn Tailscale sign-in on or off.

| Operation | Request |
|---|---|
| plan | `POST /api/v1/actions/auth.tailscale.set/v1/plan` |
| apply | `POST /api/v1/actions/auth.tailscale.set/v1/apply` |

Minimum scope: `admin`.

### Input

Sent in the `input` field of the request body.

| Field | Type | Required | Description |
|---|---|---|---|
| `enabled` | boolean | yes | Offer Tailscale sign-in on the Tailscale listener. |
| `requireTotp` | boolean | no | Ask for the user's authenticator code on every Continue as; when false, a node the user remembered with a code signs in without one. False when omitted. |

### Output

Returned in the `output` field of the change that apply returns.

| Field | Type | Required | Description |
|---|---|---|---|
| `oidc` | object or null | yes |  |
| `tailscale` | object | yes | Tailscale sign-in. |
| `tailscale.enabled` | boolean | yes | Whether Tailscale sign-in is offered. |
| `tailscale.requireTotp` | boolean | yes | Whether every Continue as asks for the authenticator code, remembered node or not. |

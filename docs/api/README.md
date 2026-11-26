# Darkpanel API reference

This reference is generated from the action schemas. `openapi.json` is the OpenAPI 3.1 document that the controller serves at `/api/v1/openapi.json`.

## Authentication

| Scheme | Type | Credential | Description |
|---|---|---|---|
| `session` | apiKey | cookie `__Host-darkpanel_session` | Session cookie; darkpanel_session on any plain-HTTP listener. |
| `token` | http | `Authorization: Bearer` header | API token dpt_\<id\>_\<secret\>. |

## Common components

### Plan

| Field | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes |  |
| `confirmation` | string or null | yes |  |
| `diff` | array of object | yes |  |
| `diff[].op` | string | yes |  |
| `diff[].text` | string | yes |  |
| `expiresAt` | string (date-time) | yes |  |
| `guards` | array of object | yes |  |
| `guards[].id` | string | yes |  |
| `guards[].message` | string | yes |  |
| `guards[].ok` | boolean | yes |  |
| `guards[].overridable` | boolean | yes |  |
| `guards[].overridden` | boolean | yes |  |
| `hostId` | string or null | yes |  |
| `inputHash` | string | yes |  |
| `planId` | string | yes |  |
| `version` | integer | yes |  |

### Change

| Field | Type | Required | Description |
|---|---|---|---|
| `action` | string | yes |  |
| `actor` | string | yes |  |
| `class` | string | yes | Values: revertible, compensable, irreversible, composite. |
| `compensatedBy` | string or null | yes |  |
| `createdAt` | string (date-time) | yes |  |
| `error` | object or null | yes |  |
| `hostId` | string or null | yes |  |
| `id` | string | yes |  |
| `output` | any | yes |  |
| `override` | any | yes |  |
| `owner` | string or null | yes |  |
| `parentChangeId` | string or null | yes |  |
| `repairOutcome` | string or null | yes |  |
| `repairedBy` | string or null | yes |  |
| `revertDeadline` | string (date-time) or null | yes |  |
| `state` | string | yes | Values: applying, pending, confirmed, reverted, completed, failed, interrupted, awaiting_confirmation, undoing, needs_repair, recovering, repaired, kept, abandoned, quarantined, releasing. |
| `updatedAt` | string (date-time) | yes |  |
| `version` | integer | yes |  |

### ApplyRequest

| Field | Type | Required | Description |
|---|---|---|---|
| `confirmNow` | boolean | no |  |
| `confirmTimeoutSeconds` | integer | no |  |
| `confirmation` | string | no |  |
| `planId` | string | yes |  |

### Problem

| Field | Type | Required | Description |
|---|---|---|---|
| `code` | string | yes |  |
| `detail` | string | yes |  |
| `errors` | array of object | no |  |
| `errors[].message` | string | yes |  |
| `errors[].path` | string | yes |  |
| `status` | integer | yes |  |
| `title` | string | yes |  |
| `type` | string | yes |  |

## Routes

| Method | Path | Authentication | Summary |
|---|---|---|---|
| POST | `/api/v1/auth/login` | none | Log in with username, password and TOTP code |
| POST | `/api/v1/auth/logout` | session or token | Log out and clear the session cookie |
| GET | `/api/v1/auth/methods` | none | Which sign-in buttons the login page shows on this origin and listener |
| GET | `/api/v1/auth/oidc/callback` | none | Finish an OIDC sign-in: a page that navigates to /?signin=\<outcome\> |
| GET | `/api/v1/auth/oidc/start` | none | Start an OIDC sign-in: redirect to the provider with a state cookie |
| GET | `/api/v1/auth/session` | session or token | Describe the calling session or token |
| POST | `/api/v1/auth/step-up` | session or token | Enter a fresh authenticator code to elevate the session for 15 minutes |
| GET | `/api/v1/auth/tailscale` | none | Which user this Tailscale node may continue as, whether the node is remembered and the user code-ready |
| POST | `/api/v1/auth/tailscale` | none | Continue as the whois match: a remembered node signs in, any other asks the code that remembers it |
| POST | `/api/v1/auth/totp` | none | Enter the authenticator code a sign-in method asks for and sign in; a Tailscale code must come from its node |
| GET | `/api/v1/changes` | session or token | List changes, newest first |
| GET | `/api/v1/changes/{id}` | session or token | Show a change |
| POST | `/api/v1/changes/{id}/confirm` | session or token | Confirm a pending change |
| GET | `/api/v1/changes/{id}/effects` | session or token | List a change's effects |
| GET | `/api/v1/changes/{id}/effects/{seq}/observe` | session or token | Observe one effect's resource now, changing nothing |
| POST | `/api/v1/changes/{id}/effects/{seq}/resolve` | session or token | Resolve an unresolved effect |
| POST | `/api/v1/changes/{id}/release` | session or token | Give up a change that needs repair |
| POST | `/api/v1/changes/{id}/repair` | session or token | Repair a change that needs repair |
| POST | `/api/v1/changes/{id}/revert` | session or token | Revert a pending change |
| GET | `/api/v1/changes/{id}/steps` | session or token | List a composite change's steps |
| POST | `/api/v1/changes/{id}/undo` | session or token | Undo a change while no later change took its locks |
| GET | `/api/v1/controller/listeners` | session or token | Listener state for the UI footer: Tailscale bound, and a message |
| POST | `/api/v1/enroll/begin` | none | Start an enrollment link's use: a new TOTP secret and its otpauth URI |
| POST | `/api/v1/enroll/finish` | none | Finish an enrollment link: set the password, confirm a code and sign in |
| GET | `/api/v1/events` | session or token | Stream the controller's change, step, host and module events (server-sent events) |
| GET | `/api/v1/hosts/{host}/modules/{module}/availability` | session or token | Which actions of a module are available on a host |
| GET | `/api/v1/hosts/{host}/streams/{module}/{name}` | session or token | Follow a module stream of a host as server-sent events |
| GET | `/api/v1/openapi.json` | session or token | This OpenAPI document |
| GET | `/healthz` | none | Health; with credentials also version, commit and schema version |

## Modules

- [core](core.md)
- [auth](auth.md)
- [hostinfo](hostinfo.md)
- [firewall](firewall.md)
- [docker](docker.md)
- [tailscale](tailscale.md)
- [tailnet](tailnet.md)
- [derp](derp.md)
- [npm](npm.md)
- [cloudflare](cloudflare.md)
- [panel](panel.md)
- [system](system.md)
- [notify](notify.md)
- [metrics](metrics.md)

# v1 wave 6a NPM spike

Checked: 2026-11-03

Evidence behind spec 6 (Nginx Proxy Manager) and the "NPM API client", "NPM image" and "NPM identities" Contracts of the
wave 6a plan. Line numbers are those of the files as fetched at the tag named. Nothing was pulled or run: every statement
below comes from NPM's source at `v2.16.0` (commit `a29eae9dc271ceabda51a6197bf1790644c99202`, the commit the image
reports as `NPM_BUILD_COMMIT=a29eae9d`), the image's registry metadata, or Docker's documentation. What the sources do
not settle is under `## Unverified`.

## Sources

Raw NPM files are `https://raw.githubusercontent.com/NginxProxyManager/nginx-proxy-manager/v2.16.0/<path>`. Every request
below answered 200 (no 404, so no tree lookup was needed for a moved path).

- Docker Hub tag list `https://hub.docker.com/v2/repositories/jc21/nginx-proxy-manager/tags?page_size=100&ordering=last_updated`: 200
- `https://auth.docker.io/token?service=registry.docker.io&scope=repository:jc21/nginx-proxy-manager:pull`: 200 (twice)
- `https://registry-1.docker.io/v2/jc21/nginx-proxy-manager/manifests/2.16.0` (index): 200
- `https://registry-1.docker.io/v2/jc21/nginx-proxy-manager/manifests/sha256:57d85bffc776c01909c7b24e127a69a72b7d8c75d387e6cb26c762978fc43e4c` (linux/amd64 manifest): 200
- `https://registry-1.docker.io/v2/jc21/nginx-proxy-manager/blobs/sha256:5c80aaf5e26c1f45a6ce9f58ad00edceabb717d68236ff5b06d6f4d1f1271742` (its config blob, through a redirect): 200
- `https://api.github.com/repos/NginxProxyManager/nginx-proxy-manager/git/trees/v2.16.0?recursive=1`: 200 (used to find paths, not for a 404)
- `https://api.github.com/repos/NginxProxyManager/nginx-proxy-manager/commits/v2.16.0` and `https://api.github.com/repos/moby/moby/commits/master` (SHA only): 200, 200
- v2.16.0 `backend/routes/main.js`, `backend/setup.js`, `backend/routes/users.js`, `backend/internal/user.js`, `backend/models/user.js`, `backend/models/auth.js`, `backend/routes/tokens.js`, `backend/internal/token.js`, `backend/schema/paths/users/post.json`, `backend/schema/components/user-object.json`, `backend/schema/paths/tokens/post.json`: 200 each
- v2.16.0 `backend/routes/nginx/proxy_hosts.js`, `backend/internal/proxy-host.js`, `backend/internal/host.js`, `backend/internal/nginx.js`, `backend/models/proxy_host.js`, `backend/schema/paths/nginx/proxy-hosts/post.json`, `backend/schema/paths/nginx/proxy-hosts/hostID/put.json`, `backend/schema/components/proxy-host-object.json`: 200 each
- v2.16.0 `backend/routes/nginx/access_lists.js`, `backend/internal/access-list.js`, `backend/schema/paths/nginx/access-lists/post.json`, `backend/schema/components/access-list-object.json`, `backend/models/access_list.js`, `backend/models/access_list_client.js`, `backend/models/access_list_auth.js`: 200 each
- v2.16.0 `backend/routes/nginx/certificates.js`, `backend/internal/certificate.js`, `backend/schema/paths/nginx/certificates/post.json`, `backend/schema/components/certificate-object.json`, `backend/models/certificate.js`, `backend/lib/certbot.js`: 200 each
- v2.16.0 `backend/lib/config.js`, `backend/lib/error.js`, `backend/lib/access.js`, `backend/lib/utils.js`, `backend/lib/helpers.js`, `backend/lib/validator/api.js`, `backend/lib/validator/index.js`, `backend/lib/express/jwt.js`, `backend/lib/express/jwt-decode.js`, `backend/lib/express/user-id-from-me.js`, `backend/lib/express/cors.js`, `backend/models/token.js`, `backend/models/now_helper.js`, `backend/db.js`, `backend/index.js`, `backend/app.js`, `backend/package.json`, `backend/schema/index.js`, `backend/schema/common.json`, `backend/schema/swagger.json`, `backend/schema/components/error-object.json`: 200 each
- v2.16.0 `backend/migrations/20180618015850_initial.js`, `20190215115310_customlocations.js`, `20190218060101_hsts.js`, `20200410143839_access_list_client.js`, `20200410143840_access_list_client_fix.js`, `20201014143841_pass_auth.js`, `20181213013211_forward_scheme.js`, `20260131163528_trust_forwarded_proto.js`: 200 each
- v2.16.0 `backend/templates/_location.conf`, `proxy_host.conf`, `default.conf`, `letsencrypt-request.conf`, `_access.conf`, `_assets.conf`, `_exploits.conf`, `_forced_ssl.conf`, `_hsts.conf`, `_hsts_map.conf`, `_header_comment.conf`, `_listen.conf`, `_certificates.conf`: 200 each
- v2.16.0 `docker/Dockerfile`, `docker/docker-compose.ci.yml`, `docker/ci.env`, `docker/rootfs/etc/letsencrypt.ini`, `docker/rootfs/etc/nginx/nginx.conf`, `docker/rootfs/etc/nginx/conf.d/production.conf.template`, `docker/rootfs/etc/nginx/conf.d/include/{proxy,force-ssl,assets,block-exploits,ip_ranges,ssl-ciphers}.conf`, `docker/rootfs/etc/s6-overlay/s6-rc.d/prepare/{20-paths,40-dynamic,45-admin-port,60-secrets}.sh`, `docker/rootfs/etc/s6-overlay/s6-rc.d/backend/run`, `docker/rootfs/usr/bin/check-health`: 200 each
- v2.16.0 `test/cypress/e2e/api/{Users,ProxyHosts,Certificates,FullCertProvision,Health,AccessListLocations}.cy.js`, `frontend/src/api/backend/createCertificate.ts`, `frontend/src/modals/HTTPCertificateModal.tsx`, `frontend/src/api/backend/models.ts`: 200 each
- For each of `v2.15.1`, `v2.15.0`, `v2.14.0`, `v2.13.7`, `v2.13.6`, `v2.13.5`, `v2.13.4`, `v2.13.3`, `v2.13.2`, `v2.13.1`, `v2.13.0`: `backend/schema/components/certificate-object.json`, `backend/schema/paths/nginx/certificates/post.json`, `backend/internal/certificate.js`, `backend/internal/token.js`, `backend/internal/user.js`, `backend/routes/users.js`, `backend/lib/error.js`, `backend/lib/access.js`: 200 each (88 requests)
- `https://docs.docker.com/engine/network/drivers/bridge/`: 200; `https://docs.docker.com/engine/network/`: 200
- moby `daemon/container_operations.go` and `daemon/network.go` on `master` (commit `aed2b781abc209a639b79d00f6962b629242aa1a`, read 2026-11-03): 200 each

## Pinned image

Pinned: jc21/nginx-proxy-manager:2.16.0@sha256:4393e642e233e5efd5adeb7f10918d4aaea71c9ed875b94190273fc8e87f9594

- Release date: tag `last_updated` 2026-09-24T09:47:40Z in the Hub tag list; the image config says `created` 2026-09-24T09:43:43Z and `NPM_BUILD_VERSION=2.16.0`.
- Digest: the registry's `docker-content-digest` for `manifests/2.16.0` equals the tag list's `digest` for `2.16.0`. The response `content-type` is `application/vnd.oci.image.index.v1+json`.
- Platforms of the index: `linux/amd64` (`sha256:57d85bffc776c01909c7b24e127a69a72b7d8c75d387e6cb26c762978fc43e4c`) and `linux/arm64` (`sha256:21554e198b039808b56690889e672bad316c4008767e648d846d99da9350afa8`), plus two `unknown/unknown` entries (build attestations, not platforms).
- Qualifying tags (form `2.<minor>.<patch>`, minor 13 or later), newest first: 2.16.0, 2.15.1, 2.15.0, 2.14.0, 2.13.7, 2.13.6, 2.13.5, 2.13.4, 2.13.3, 2.13.2, 2.13.1, 2.13.0. Skipped newer tags: none, because no qualifying tag passes the checks.
- Why no tag passes: the certificate create schema's `meta` has `additionalProperties: false` and no `letsencrypt_email` or `letsencrypt_agree` (`backend/schema/components/certificate-object.json` lines 47-83 at v2.16.0), and the same is true of all twelve tags (fetched and compared, 2026-11-03). Every other behaviour compared across the twelve tags is identical: the `nice_name` overwrite, the lower-casing of the login identity, and the unauthenticated first-user branch of `POST /api/users` are present in each of them. Following the rule, the newest tag is pinned and every failure is under `## Divergences`. The access-list `items` finding (D2) was read at v2.16.0 only.
- Image config (`Env` of the amd64 config blob): `PATH` starts with `/opt/certbot/bin`, `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`, `CURL_CA_BUNDLE=/etc/ssl/certs/ca-certificates.crt`, `NODE_ENV=production`; no `TZ`, no `LE_*`, no `REQUESTS_CA_BUNDLE`, no `DB_*`. `Entrypoint` `["/init"]`, exposed ports 80, 81 and 443, volume `/data`, no `Healthcheck`.

## Bootstrap

- `GET /api` (`backend/routes/main.js` 35-48) answers 200 `{"status": "OK", "setup": <bool>, "version": {"major", "minor", "revision"}}`. `setup` is `isSetup()` (`backend/setup.js` 11-14): true exactly when a `user` row with `is_deleted = 0` exists. So `setup` is false exactly while no user exists, and true after `POST /api/users`.
- The admin port (81) is served by nginx, not by the backend: `production.conf.template` 10-12 answers `location /api` with `302 /api/`, and `location /api/` (14-24) proxies to the backend on `127.0.0.1:3000` with `proxy_read_timeout 15m`. A client that sends `GET /api` must follow that redirect (Go's default client does for a GET) or ask for `/api/` directly; NPM's own health check asks for `/api/` (`docker/rootfs/usr/bin/check-health` line 3). Paths below `/api/` (`/api/users`, `/api/tokens`, `/api/nginx/...`) have no redirect.
- `POST /api/users` (`backend/routes/users.js` 68-98): while `setup` is false (line 74) it builds an internal `Access` (75-78), so no authentication is checked; it sets `body.is_disabled = false` (82) and makes `roles` an array containing `admin` (83-88); then validates against the request schema (91) and creates (92); success is 201 (93). After setup the same call needs a token: without one it answers 403 `Permission Denied` (see the refusal table).
- Request schema (`backend/schema/paths/users/post.json` 16-44): `additionalProperties: false`; required `name`, `nickname`, `email`; permitted `name`, `nickname`, `email`, `roles`, `is_disabled`, `auth` (a free-form object). Contracts' `CreateAdmin` body (`name`, `nickname`, `email`, `roles: ["admin"]`, `is_disabled: false`, `auth: {type: "password", secret}`) is accepted as written.
- Length limits: `name` minLength 1 (`user-object.json` 34-39), `email` minLength 3 (28-33), `nickname` none (40-44); none has a maximum and `auth.secret` has no schema limit. The `user` columns are `table.string(...)` (varchar 255, `migrations/20180618015850_initial.js` 36-38), which SQLite, the default database (`lib/config.js` 97-112), does not enforce. The secret is hashed with bcrypt, cost 13 (`models/auth.js` 15-23); bcrypt reads at most 72 bytes of input, so the 43-character password of a 32-byte base64url secret is hashed whole. No divergence.
- Email on create and login: `internalUser.create` (`internal/user.js` 26-74) stores `email` as given, and `User.$beforeInsert` (`models/user.js` 15-23) does not change it. Login lower-cases and trims the identity before the lookup (`internal/token.js` 32: `where("email", data.identity.toLowerCase().trim())`), and SQLite compares `=` case-sensitively by default (the column has no collation). Rule for the module: a mixed-case `acmeEmail` can log in only if `POST /api/users` was given the lower-cased, trimmed form. The module must send `strings.ToLower(strings.TrimSpace(acmeEmail))` as the admin's `email`; a mixed-case email stored as given can never match the lookup. NPM uses the stored user's email as the ACME contact (see `## API shapes`), so the contact is the lower-cased form too. (The update path lower-cases as well, `internal/user.js` 103.)
- `POST /api/tokens` (`routes/tokens.js` 45-54, `schema/paths/tokens/post.json` 11-31): `additionalProperties: false`; required `identity` and `secret` (minLength 1); optional `scope`, enum `["user"]`; no `expiry` is accepted from the client. Answer 200 `{"token": <JWT>, "expires": <ISO 8601>}` (`internal/token.js` 92-95). The lifetime is one day (`data.expiry = data.expiry || "1d"`, line 28; `expires` is now plus that, 78-81). A user with two-factor enabled gets `{requires_2fa, challenge_token}` instead (62-75); the admin the module creates has none.
- Refusals. Body shape (`app.js` 62-90): `{"error": {"code": <int>, "message": <string>}}`, with an extra `"message_i18n"` for some auth errors; when `DEBUG` is set, and always for paths containing `nginx/certificates` (line 74), the answer also carries a top-level `"debug"` object with a stack. A non-public error answers the message `Internal Error`. A client must read only `error.message`.

| Case | Status | `error.message` | Where |
|---|---|---|---|
| Body fails the schema (`apiValidator`, `allErrors`, `coerceTypes`) | 400 | ajv text, for example `data must have required property 'name'` | `lib/validator/api.js` 38-41, `lib/error.js` 80-87 |
| Proxy-host domain already used | 400 | `<domain> is already in use` | `internal/proxy-host.js` 43 |
| Login: unknown identity | 400 | `Invalid email or password` | `internal/token.js` 37-39, `lib/error.js` 34-42 |
| Login: wrong password | 400 | `Invalid email or password`, `message_i18n` `error.invalid-auth` | `internal/token.js` 47-50 |
| No token, on `/api/nginx/*` (including `GET /api/nginx/proxy-hosts`) and on `POST /api/users` after setup | 403 | `Permission Denied` | `lib/access.js` 45-47 and 293, `lib/error.js` 4-11 |
| Expired token | 400 | `Token has expired` | `models/token.js` 69-70 |
| Token issued before a password change | 401 | `Token was issued before the password was changed` | `lib/access.js` 97, `lib/error.js` 25-32 |
| Malformed or wrongly signed token | 500 | `Internal Error` (inferred, see `## Unverified`) | `models/token.js` 72, `app.js` 66 and 89 |
| Unknown id | 404 | `Not Found - <id>` | `lib/error.js` 13-23, `internal/proxy-host.js` 257 and 283, `internal/access-list.js` 282 and 309, `internal/certificate.js` 309 and 401 |
| Unknown path under `/api/` | 404 | `Not Found - <path>` | `routes/main.js` 75-78 |

So a missing token is 403, not 401; 401 occurs only for a token revoked by a password change. `GET /api/nginx/proxy-hosts` without a token answers 403 `Permission Denied`.

## API shapes

All three routes answer 200 on read, update and delete, 201 on create, and `DELETE` answers the JSON body `true`. Reads and the create answer carry fields Contracts does not name (`owner_user_id`, `meta`, `owner`, and others below); a decoder must ignore unknown fields. Deletes are soft (`is_deleted = 1`), so a deleted object disappears from reads at once.

Proxy hosts, `/api/nginx/proxy-hosts` (create `routes/nginx/proxy_hosts.js` 63-67, read 91-115, delete 144-149):

| Contracts field | JSON name | Type | Notes |
|---|---|---|---|
| `ID` | `id` | integer | |
| `CreatedOn`, `ModifiedOn` | `created_on`, `modified_on` | string | format below |
| `DomainNames` | `domain_names` | array of string | 1 to 100 items, unique, pattern `^[^&\| @!#%^();:/\\}{=+?<>,~`'"]+$` (`common.json`); stored and returned sorted ascending (`models/proxy_host.js` 42) |
| `ForwardScheme` | `forward_scheme` | string | enum `http`, `https` |
| `ForwardHost` | `forward_host` | string | 1 to 255 characters |
| `ForwardPort` | `forward_port` | integer | 1 to 65535 |
| `CertificateID` | `certificate_id` | integer | 0 or more, or the string `new` (NPM then issues a certificate itself; the module never sends it) |
| `AccessListID` | `access_list_id` | integer | 0 or more |
| `SSLForced`, `HSTSEnabled` | `ssl_forced`, `hsts_enabled` | boolean | |
| `AllowWebsocketUpgrade` | `allow_websocket_upgrade` | boolean | |
| `Locations` | `locations` | array of object | `id` (integer or null), `path` (string, minLength 1), `forward_scheme`, `forward_host`, `forward_port` (**integer**, 1 to 65535, via `$ref` to the host's own `forward_port`), `forward_path`, `advanced_config` (string), `access_list_id` |
| `AdvancedConfig` | `advanced_config` | string | |

- Create schema (`schema/paths/nginx/proxy-hosts/post.json`): `additionalProperties: false` (19); required `domain_names`, `forward_scheme`, `forward_host`, `forward_port` (20-25). Contracts names all four. Each location requires `forward_scheme`, `forward_host`, `forward_port`, `path` and has `additionalProperties: false` (`proxy-host-object.json` 103-104); Contracts' `Location` carries exactly those plus `advanced_config`. Update is `PUT .../<id>`, 200, `additionalProperties: false`, nothing required (`hostID/put.json`). Permitted body names include every Contracts name, and also `enabled`, `meta`, `caching_enabled`, `block_exploits`, `http2_support`, `hsts_subdomains`, `trust_forwarded_proto`.
- "None": `certificate_id: 0` and `access_list_id: 0` are valid and are the column defaults (`migrations/20180618015850_initial.js` 73-74); NPM does not reject 0.
- NPM silently sets `ssl_forced` and `http2_support` to false when `certificate_id` is 0, and `hsts_enabled` to false when `ssl_forced` is false (`internal/host.js` 17-34, called at `internal/proxy-host.js` 52). A create with `ssl_forced: true` and no certificate reads back `ssl_forced: false`.
- The create answer is the re-fetched row with `certificate`, `owner` and `access_list` expanded (`internal/proxy-host.js` 81-85). NPM writes the nginx configuration and tests it afterwards (`internal/nginx.js` 29-103); when that test fails the host is kept, `meta.nginx_online` is false and `meta.nginx_err` holds the text, and the create still answers 201. A 201 therefore does not prove the host is serving.
- Domain uniqueness is checked by a read before the insert (`internal/proxy-host.js` 35-47, `internal/host.js` 105-168), with no database constraint; two concurrent creates for one domain can both succeed.

Access lists, `/api/nginx/access-lists` (create `routes/nginx/access_lists.js` 62-66, delete 143-148):

| Contracts field | JSON name | Type | Notes |
|---|---|---|---|
| `ID` | `id` | integer | |
| `CreatedOn` | `created_on` | string | |
| `Name` | `name` | string | minLength 1, kept as given |
| `SatisfyAny` | `satisfy_any` | boolean | column default false |
| `PassAuth` | `pass_auth` | boolean | column default **true** (`migrations/20201014143841_pass_auth.js` 18) |
| `Clients` | `clients` | array of object | present only with `?expand=clients` on a read, and always on the create answer; each row has `id`, `created_on`, `modified_on`, `access_list_id`, `address`, `directive`, `meta` |
| `AccessClient.Address` | `address` | string | IPv4 with optional `/0`-`/32`, IPv6 with optional prefix, or `all` (`common.json`) |
| `AccessClient.Directive` | `directive` | string | enum `allow`, `deny` |

- Create schema (`schema/paths/nginx/access-lists/post.json`): `additionalProperties: false` (19); required `name` only; permitted `name`, `satisfy_any`, `pass_auth`, `items`, `clients`. `items` is not marked required, but `internalAccessList.create` calls `data.items.map(...)` unconditionally (`internal/access-list.js` 74) after the list row is inserted (60-68), so a body without `items` answers 500 and leaves a committed list with that name. The body must carry `"items": []` (D2).
- `?expand=clients` (`allowGraph "[owner,items,clients]"`, `internal/access-list.js` 393 and 407-408): the list and the single read add `clients`; without the query neither carries it. Every read carries `proxy_host_count`. Clients are inserted one after another in the order given (88-94), so their ids ascend in that order.
- Deleting a list sets the `access_list_id` of every host and location that used it to 0 (`internal/access-list.js` 323-343).

Certificates, `/api/nginx/certificates` (create `routes/nginx/certificates.js` 64-69, delete 227-232):

| Contracts field | JSON name | Type | Notes |
|---|---|---|---|
| `ID` | `id` | integer | |
| `CreatedOn` | `created_on` | string | |
| `NiceName` | `nice_name` | string | permitted on create; overwritten for `letsencrypt` (below) |
| `Provider` | `provider` | string | `letsencrypt` or `other` (`common.json` `ssl_provider`) |
| `DomainNames` | `domain_names` | array of string | at most 100, unique, same pattern; stored and returned sorted ascending (`models/certificate.js` 18-26 and 40) |
| `ExpiresOn` | `expires_on` | string | `YYYY-MM-DD HH:mm:ss`, wall clock of the container (`internal/certificate.js` 198) |
| `CertificateMeta.LetsencryptEmail`, `.LetsencryptAgree` | none | none | **not in the schema**, see D1 |

- Create schema (`schema/paths/nginx/certificates/post.json` 17-18): `additionalProperties: false`; required `provider` only; permitted `provider`, `nice_name`, `domain_names`, `meta`. `meta` (`certificate-object.json` 47-83): `additionalProperties: false`, permitted `certificate`, `certificate_key`, `dns_challenge`, `dns_provider`, `dns_provider_credentials`, `letsencrypt_certificate`, `propagation_seconds`, `key_type` (`rsa` or `ecdsa`). Sending `letsencrypt_email` or `letsencrypt_agree` is refused with 400 (ajv: `must NOT have additional properties`).
- A Let's Encrypt create needs `{"provider": "letsencrypt", "domain_names": [...]}` and nothing in `meta` (without `domain_names` the handler fails on `data.domain_names.join`, line 122). NPM's own web UI sends `domain_names`, `provider: "letsencrypt"` and `meta: {key_type: "ecdsa"}` (`frontend/src/modals/HTTPCertificateModal.tsx` 116-120). The ACME contact is the creating user's `email` (`internal/certificate.js` 145-150 and 174), so with the admin created from `acmeEmail` the contact is `acmeEmail`.
- Challenge: HTTP-01 through the webroot when `meta.dns_challenge` is absent or false (`internal/certificate.js` 153-167 is the DNS branch, 167-186 the HTTP branch with the certbot call at 174; certbot arguments `--authenticator webroot ... --preferred-challenges http`, 794-799).
- Blocking: `POST /api/nginx/certificates` blocks until certbot has finished. The handler awaits `requestLetsEncryptSsl` (line 174) and the row's `expires_on` update (197-198) before it answers 201; on failure the row is deleted again (212, 218) and the error is returned. The admin-port nginx allows 15 minutes (`production.conf.template` 22-23), above the plan's 10 minute `CommitWait`. A create that blocks is not a divergence.
- Delete marks the row deleted first and then revokes with certbot (`internal/certificate.js` 396-423, revoke at 420); a failed revoke answers an error after the row is already hidden.
- `created_on`: all three models set it with `now()` (`models/now_helper.js` 7-12). On SQLite, the default (`lib/config.js` 97-112), that is `datetime('now','localtime')`: the text `YYYY-MM-DD HH:MM:SS`, no zone, second precision, in the container's local time. The image sets no `TZ`, so a container deployed without one reports UTC wall time (see `## Unverified`). The module must not set `TZ`. Nothing converts the value on the way out, so the API answers that text. Other databases (MySQL, Postgres) use `NOW()` and other formats, but the deployed instance uses SQLite. The module compares at second precision, as plan Task 16 already says. Plan Task 16 and Task 18 parse `2006-01-02 15:04:05` as UTC; the fake's `CreatedOnLayout` (ISO with milliseconds and `Z`) is a simplification, not NPM's format, and no consumer depends on it.

## Identities

Certificate identity: domains

- (a) Proxy-host domains are unique. `isHostnameTaken` (`internal/host.js` 105-168) looks at proxy, redirection and dead hosts, not streams, ignoring deleted rows; the comparison is case-insensitive (`_checkHostnameRecordsTaken`, line 185). A create with a used domain is refused with 400 `<domain> is already in use` (`internal/proxy-host.js` 43), before any insert. A domain held by a redirection or dead host is refused the same way, and the module's own proxy-host list cannot see those holders.
- (b) An access list keeps the `name` it was given: the insert copies `data.name` (`internal/access-list.js` 63) and nothing rewrites it. Names are not required to be unique.
- (c) A Let's Encrypt certificate does not keep a given `nice_name`: `internalCertificate.create` sets `data.nice_name = data.domain_names.join(", ")` when `provider === "letsencrypt"` (`internal/certificate.js` 121-123). The join runs on the request's `domain_names` in the order sent, before the model sorts the stored array (`models/certificate.js` 40), so a request for `["b.example.com", "a.example.com"]` stores `nice_name` `b.example.com, a.example.com` and `domain_names` `["a.example.com", "b.example.com"]`. The module sends its domains sorted, so the name is the sorted join. The same line is present in all twelve qualifying tags, 2.13.0 to 2.16.0.
- (d) `created_on` is returned on every create: the proxy host and the access list are re-fetched whole after the insert (`internal/proxy-host.js` 81-85, `internal/access-list.js` 97-104) and the certificate answer is the patched row (`internal/certificate.js` 197-201). The format is described under `## API shapes`.

## Custom location

The template at v2.16.0 against spec 6's list, line by line (`backend/templates/_location.conf`):

| Spec 6 statement | Template | Holds |
|---|---|---|
| `proxy_set_header Host` | line 4 `proxy_set_header Host $host;` | yes |
| `X-Forwarded-Scheme` | line 5 `$scheme` | yes |
| `X-Forwarded-Proto` (`$scheme`) | line 6 `$scheme` | yes |
| `X-Forwarded-For` | line 7 `$remote_addr` | yes |
| `X-Real-IP` | line 8 `$remote_addr` | yes |
| the `proxy_pass` | line 10 `proxy_pass {{ forward_scheme }}://{{ forward_host }}:{{ forward_port }}{{ forward_path }};` | yes |
| with websockets `Upgrade`, `Connection`, `proxy_http_version 1.1` | lines 18-22: `Upgrade $http_upgrade`, `Connection $http_connection`, `proxy_http_version 1.1` | yes |
| the location's `advanced_config` is the first line inside the block | line 2, directly after `location {{ path }} {` | yes |
| `X-Darkpanel-Client` and `proxy_read_timeout` are not in the template | neither appears in `_location.conf` or the files it includes, except as in the notes below | yes |

`backend/internal/nginx.js` at v2.16.0: a host has `use_default_location = true` unless its host-level `advanced_config` matches `location /` (`advancedConfigHasDefaultLocation`, line 460; assigned 236-240), and it is set to false when any custom location has `path === "/"` (267-271). The host's `advanced_config` is rendered at server scope: `backend/templates/proxy_host.conf` line 28, after the `access_log`/`error_log` lines and before the rendered locations (line 30).

Notes (differences that do not change what Darkpanel puts in `advanced_config`):

- `_access.conf` (included at `_location.conf` line 12, and rendered with the host's access list when the location has none of its own, `nginx.js` 174-188) adds `proxy_set_header Authorization "";` (line 8) when the list has items and `pass_auth` is false, and `satisfy`, `allow`/`deny` rules and `deny all;` when the list has clients. Darkpanel's lists have no items.
- With `caching_enabled` on, `_assets.conf` includes `conf.d/include/assets.conf`, a nested location for static files with `proxy_read_timeout 45s` (line 25). The module never sends `caching_enabled`; the column default is 0.
- The http-level `proxy_read_timeout 90s` (`nginx.conf` 35) applies to the location, so the panel's `proxy_read_timeout 3600s;` is a real override. A location that defines any `proxy_set_header` does not inherit the http-level ones (`nginx.conf` 42-44, `Accept-Encoding ""` among them).
- `nginx.conf` 75-82 sets `set_real_ip_from` for the private ranges and `real_ip_header X-Real-IP`, so `$remote_addr` is replaced by a client-sent `X-Real-IP` when the peer is a private address, while `$realip_remote_addr`, which Darkpanel's line uses, stays the real peer. These directives also show the realip module is compiled into the image's nginx.
- `_forced_ssl.conf` and `_hsts.conf` are emitted only with a certificate, `ssl_forced` and `hsts_enabled`; the force-ssl include (`docker/rootfs/etc/nginx/conf.d/include/force-ssl.conf`) sets no proxy header.

Rendered block for a location `/` forwarding `http://app:8080`, certificate present, `ssl_forced` and `hsts_enabled` true, websockets off, no access list, and Darkpanel's two panel `advanced_config` lines (`proxy_set_header X-Darkpanel-Client $realip_remote_addr;` and `proxy_read_timeout 3600s;`). It is composed by hand from the template; the blank lines left by the empty includes are collapsed, and the second `advanced_config` line starts in column 0 because the template indents only the placeholder's first line:

```text
  location / {
    proxy_set_header X-Darkpanel-Client $realip_remote_addr;
proxy_read_timeout 3600s;

    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Scheme $scheme;
    proxy_set_header X-Forwarded-Proto  $scheme;
    proxy_set_header X-Forwarded-For    $remote_addr;
    proxy_set_header X-Real-IP		$remote_addr;

    proxy_pass       http://app:8080;

    # Force SSL
    set $trust_forwarded_proto "F";
    include conf.d/include/force-ssl.conf;
  # HSTS (ngx_http_headers_module is required) (63072000 seconds = 2 years)
  add_header Strict-Transport-Security $hsts_header always;
  }
```

For an ordinary exposure the first block has the single `advanced_config` line and no `proxy_read_timeout`.

## Harness settings

Set only by recreating the `darkpanel-npm` container in the harness (Task 28), never by Darkpanel's deploy: the module's `instanceCreate` carries none of these variables, and a `harness` build tag never reaches the agent.

| variable | value | effect |
|---|---|---|
| `LE_SERVER` | the ACME directory URL of the test CA, for example Pebble's `https://<host>:14000/dir` | `useLetsencryptServer()` returns it when set (`backend/lib/config.js` 263-268); `getAdditionalCertbotArgs` then adds `--server <url>` to every certbot call (`internal/certificate.js` 1295-1298), which `requestLetsEncryptSsl` (777-817), renew and revoke all use |
| `LE_STAGING` | any non-empty value | adds `--staging` only when `LE_SERVER` is unset (`internal/certificate.js` 1300-1302; `config.js` 258); not needed when `LE_SERVER` is set |
| `REQUESTS_CA_BUNDLE` | path of a PEM bundle that contains the test CA's root | certbot's ACME client uses python `requests`, which trusts this bundle instead of its own; nothing in NPM's code or image sets it, and `execFile` passes the container's environment through (`internal/certificate.js` 814, `lib/utils.js` 34-46, options `{}` for HTTP-01). NPM's own CI sets `LE_SERVER` and `REQUESTS_CA_BUNDLE=/etc/ssl/certs/NginxProxyManager.crt` together (`docker/docker-compose.ci.yml` 14-15) |
| image CA files | `/etc/ssl/certs/ca-certificates.crt` (the system bundle) | `docker/Dockerfile` 44-47 copies the CI test CA to `/etc/ssl/certs/NginxProxyManager.crt` and runs `update-ca-certificates`; the image's `SSL_CERT_FILE` and `CURL_CA_BUNDLE` point at the system bundle. A bundle for `REQUESTS_CA_BUNDLE` can be that file with Pebble's root appended, or a mounted file |
| HTTP-01 port | 80 of the NPM container | the challenge is served from a generated `server` on `listen 80` with `server_name` the requested domains (`backend/templates/letsencrypt-request.conf` 3-14) and the webroot `/data/letsencrypt-acme-challenge` (`docker/rootfs/etc/letsencrypt.ini` 3); the test CA's validator must reach the NPM container on port 80, which is a setting of the CA (not read here) |
| `TZ` | leave unset | the image sets none; `created_on` and `expires_on` are container-local wall time without a zone, and a value other than UTC would be read as UTC by the module |

`docker/rootfs/etc/letsencrypt.ini` also sets `key-type = ecdsa`, `elliptic-curve = secp384r1` and `preferred-chain = ISRG Root X1`; a test CA without that chain makes certbot warn and use its default chain.

## Docker DNS

- Docker's bridge documentation (`https://docs.docker.com/engine/network/drivers/bridge/`, read 2026-11-03): "User-defined bridges provide automatic DNS resolution between containers. Containers on the default bridge network can only access each other by IP addresses, unless you use the --link option, which is considered legacy. On a user-defined bridge network, containers can resolve each other by name or alias." The tutorial on the same page: "On the default bridge network, containers can't resolve each other by name." It also carries the note: "Automatic service discovery only resolves custom container names, not default automatically generated names."
- The networking overview (`https://docs.docker.com/engine/network/`): "Containers that attach to a custom network use Docker's embedded DNS server", at `127.0.0.11`, while containers on the default bridge network receive a copy of the host's `resolv.conf`; "a container's hostname defaults to be the container's ID".
- moby `daemon/container_operations.go` on `master` (commit `aed2b781abc209a639b79d00f6962b629242aa1a`), `buildEndpointDNSNames` (lines 676-694) builds the names registered for an endpoint from the container's name (`strings.TrimPrefix(ctr.Name, "/")`), its aliases, its short ID and its hostname, with no test of whether the name was generated. Read together with the documentation's note, the two sources disagree about a container created without `--name`.
- Container created without `--name`, reachable by its generated name on a user-defined network: not settled by sources; Task 28 exercises it.
- On the default bridge a container is not reachable by name (documentation), which is what `npm.unsupported_target` already refuses.

## Unverified

- Nothing was run. Status codes, bodies, the 500 for a body without `items` and the `created_on` text are read from source; Task 28 sees them on a running instance.
- A malformed or wrongly signed token answers 500 `Internal Error`: inferred from `models/token.js` 72 passing the `jsonwebtoken` library's error on unchanged and from `app.js` 66 and 89; the library's source was not read, so that its errors carry no `status` is an assumption.
- The container's time zone is UTC only if the base image's `/etc/localtime` is UTC. The image config has no `TZ`; the base image (`nginxproxymanager/nginx-full:certbot-node`) was not inspected.
- That certbot at `/opt/certbot/bin` honours `REQUESTS_CA_BUNDLE` was not run; NPM's own CI relies on it (`docker-compose.ci.yml` 15).
- The port the test CA's HTTP-01 validator dials is the CA's own setting and was not read.
- The rendered block under `## Custom location` was composed by hand; no Liquid engine rendered it.
- Whether a container created without `--name` resolves by its generated name: see `## Docker DNS`.

## Divergences

- D1. Certificate create meta. The plan states (Contracts "NPM API client": `CreateCertificate` with `meta.letsencrypt_email` and `meta.letsencrypt_agree: true`, `CertificateMeta{LetsencryptEmail string; LetsencryptAgree bool}`; spec 6 Management: "`npm.certificate.request` sends `meta.letsencrypt_email` = the stored `acmeEmail` and `meta.letsencrypt_agree: true`"). The pinned tag's create schema forbids both names: `meta` has `additionalProperties: false` and lists neither (`backend/schema/components/certificate-object.json` 47-83), so the call answers 400; no tag from 2.13.0 to 2.16.0 accepts them. NPM takes the ACME contact from the creating user's email (`backend/internal/certificate.js` 145-150, 174), and agreement is implicit (`--agree-tos`, 793). The body that works is `{"provider": "letsencrypt", "domain_names": [...]}` with no `meta`. Tasks that state the old value: Task 6 (types, `CreateCertificate` always sending `letsencrypt_agree`, the fake's 400 rules and their tests), Task 18 (`certificate.request` body), and spec 6 Management bullet (left as it is for the integrator, including its "the 6a spike confirms the field names" parenthesis).
- D2. Access-list create needs `items`. The plan states (Contracts `AccessListBody{Name string; SatisfyAny, PassAuth bool; Clients []AccessClient}`) a body without `items`. The pinned tag's schema does not mark `items` required, but `backend/internal/access-list.js` line 74 calls `data.items.map` on it after inserting the list (60-68): a body without `items` answers 500 and leaves the list committed. The body must send `"items": []`. Tasks that state the old value: Task 6 (the `AccessListBody` type and its JSON names, the fake and its tests) and Task 17 (`accesslist.add` builds the body).
- D3. Refusal statuses of the fake. The plan's fake (Task 6) answers a missing or wrong token on `/api/nginx/*` with 401 `Unauthorized`, a wrong password or unknown identity on `POST /api/tokens` with 401 `Invalid email or password`, a second `POST /api/users` with 403 `Setup is already complete`, an absent id with 404 `Not Found`, and an `auth.secret` outside 8 to 100 characters with 400. The pinned tag answers 403 `Permission Denied` for a missing token and for a second `POST /api/users` (`lib/access.js` 45-47 and 293), 400 `Invalid email or password` for a wrong password or identity (`internal/token.js` 37-50, `lib/error.js` 34-42), 404 `Not Found - <id>` (`lib/error.js` 13-23), and no limit on `auth.secret` (`schema/paths/users/post.json` 35-42). No Contract value or module branch depends on these: `IsClientError` is any 4xx and `IsNotFound` is 404. Tasks that state the old value: Task 6 (the fake's guard and its tests).

# v1 wave 6b Cloudflare spike

Checked: 2026-11-03

Evidence behind spec 7.2 (the Cloudflare connector) and the "Image" and "Spike note" Contracts of the wave 6b plan. Line
numbers are those of the files as fetched at the cloudflared tag `2026.10.7` (commit
`872b241f94871665c1c157adefcf90c11f0ead96`, which is also the image's `CI_GIT_COMMIT` label). Nothing was pulled or run:
every statement comes from cloudflared's source, the image's registry metadata and layer listings (read with `curl` from
the registry API, never through the Docker daemon), or the source of the libraries it names. What the sources do not
settle is under `## Unverified`.

## Sources

Raw cloudflared files are `https://raw.githubusercontent.com/cloudflare/cloudflared/2026.10.7/<path>`. Every request below
answered 200 (no 404, so no tree lookup was needed for a moved path).

- Docker Hub tag list `https://hub.docker.com/v2/repositories/cloudflare/cloudflared/tags?page_size=100&ordering=last_updated`: 200
- GitHub releases `https://api.github.com/repos/cloudflare/cloudflared/releases?per_page=30`: 200
- `https://api.github.com/repos/cloudflare/cloudflared/git/ref/tags/2026.10.7`, `.../git/tags/e7817459ae22d74127a4a648adbd1aef20ac0184` (the tag object, whose commit is `872b241f...`) and `.../git/trees/2026.10.7?recursive=1` (used to list paths, not for a 404): 200 each. The tag is the bare version.
- `https://auth.docker.io/token?service=registry.docker.io&scope=repository:cloudflare/cloudflared:pull`: 200 (three times; the token lives a few minutes)
- `https://registry-1.docker.io/v2/cloudflare/cloudflared/manifests/2026.10.7` (index): 200
- `https://registry-1.docker.io/v2/cloudflare/cloudflared/manifests/sha256:32f933aa6fe1f69c5885b93f3c92cc3f60cca8f0530868bc3bc2cc446114a385` (linux/amd64 manifest): 200
- `https://registry-1.docker.io/v2/cloudflare/cloudflared/blobs/sha256:5436e6763af2b3fb02aad5f5caebf4291e2bded1ea89b8cae774a06f55f91110` (its config blob): 307 to the registry's CDN host, then 200 (the `Authorization` header went to `registry-1.docker.io` only)
- All 18 layer blobs of the amd64 manifest, each through the same redirect: 200 each (used to read `/etc/passwd`, `/etc/group` and the file lists, see `## Image`)
- 2026.10.7 `Dockerfile`, `go.mod`, `cmd/cloudflared/main.go`, `cmd/cloudflared/linux_service.go`, `cmd/cloudflared/generic_service.go`, `cmd/cloudflared/flags/flags.go`, `cmd/cloudflared/tunnel/cmd.go`, `cmd/cloudflared/tunnel/subcommands.go`, `cmd/cloudflared/tunnel/subcommand_context.go`, `cmd/cloudflared/tunnel/configuration.go`, `cmd/cloudflared/tunnel/credential_finder.go`, `cmd/cloudflared/tunnel/filesystem.go`, `cmd/cloudflared/tunnel/signal.go`, `cmd/cloudflared/updater/update.go`, `cmd/cloudflared/cliutil/{build_info,deprecated,errors,handler,logger,management}.go`: 200 each
- 2026.10.7 `connection/{connection,control,errors,observer}.go`, `credentials/credentials.go`, `retry/backoffhandler.go`, `supervisor/{supervisor,tunnel}.go`, `tunnelrpc/pogs/{errors,registration_server}.go`, `tunnelrpc/registration_client.go`, `logger/{create,configuration}.go`: 200 each
- Libraries at the versions 2026.10.7's `go.mod` names: `google/uuid` v1.6.0 `uuid.go`, `marshal.go`; `rs/zerolog` v1.20.0 `console.go`; `urfave/cli/v2` v2.3.0 as replaced by `ipostelnik/cli` v2.3.1-0.20210324024421-b6ea8234fe3d (`go.mod` line 99) `errors.go`, `app.go`: 200 each, from `raw.githubusercontent.com`
- cloudflared `cmd/cloudflared/tunnel/subcommands.go` at `2025.4.0` and at `2024.12.2`: 200, 200 (the flag's first release, compared below)

## Image

Pinned: cloudflare/cloudflared:2026.10.7@sha256:4955524aeaa379e154023d5a027c250f7b35041a2e7e950cf13f144bc9b9c5ba

- Release date: tag `last_updated` 2026-10-30T17:22:44Z in the Hub tag list; GitHub release `published_at` 2026-10-30T17:11:03Z (not a pre-release or draft); the image config says `created` 2026-10-30T17:20:23Z.
- Digest: the registry's `docker-content-digest` for `manifests/2026.10.7` equals the tag list's `digest` for `2026.10.7`. The response `content-type` is `application/vnd.docker.distribution.manifest.list.v2+json`.
- Platforms of the index: `linux/amd64` (`sha256:32f933aa6fe1f69c5885b93f3c92cc3f60cca8f0530868bc3bc2cc446114a385`) and `linux/arm64` (`sha256:17ab7aa52b3a9805b36c81cbe353bad657fb6395d63c81e12021beaea95f03d3`); no other entry.
- Qualifying tags (form `<year>.<month>.<patch>`, 2025.4.0 or later) on the first Hub page, newest first: 2026.10.7, 2026.10.6, 2026.10.5, 2026.10.4, 2026.10.3, 2026.10.2, 2026.10.1, 2026.10.0, 2026.9.3, 2026.9.2, 2026.9.1. Every one is also a GitHub release; the first page holds no qualifying tag that is not a release. The newest passes every check below, so no newer tag was skipped.
- Image config (`config` of the amd64 config blob): `User` `65532:65532`; `Entrypoint` `["cloudflared"]`; `Cmd` `["version"]`; `Env` `PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, `SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`, `NO_AUTOUPDATE=true`; `WorkingDir` `/home/nonroot`; no exposed ports, no volumes, no `Healthcheck`. Labels include `CI_DOCKER_FROM=gcr.io/distroless/base-debian13:nonroot-amd64@sha256:91475ae1743239ff0810c39688162aaf4bb0b7a12f126b893e8cbc07ace242ee` and `IMAGE_TYPE=distroless`.
- (a) User: `config.User` is `65532:65532`. `Dockerfile` line 41 `USER 65532:65532` (the comment on lines 37-40 says the `nonroot` user maps to 65532); its base is `gcr.io/distroless/base-debian13:nonroot` at line 30. The base image's `/etc/passwd` (layer `7c12895b777b...`, 149 bytes) holds `nonroot:x:65532:65532:nonroot:/home/nonroot:/sbin/nologin`, its `/etc/group` (layer `52630fc75a18...`) holds `nonroot:x:65532:`, and `/home/nonroot` is owned 65532:65532 with mode 0700 (layer `3214acf345c0...`). Matches UID 65532.
- (b) Entrypoint: `["cloudflared"]` (`Dockerfile` line 48) with `Cmd` `["version"]` (line 49). The container command `tunnel --no-autoupdate run --token-file /run/darkpanel/token` replaces `Cmd` and is appended to the entrypoint, so the process runs `cloudflared tunnel --no-autoupdate run --token-file /run/darkpanel/token`. The binary is `/usr/local/bin/cloudflared` (`Dockerfile` line 35, found in the last layer `d8ac674fae01...`).
- (c) No shell: the 18 layers hold 1822 entries in all and none is named `sh`, `bash`, `dash`, `ash`, `busybox`, `zsh`, `ksh`, `env`, `cat` or `ls`; the only file under a `bin` directory is `usr/local/bin/cloudflared`. The base image's login shell is `/sbin/nologin`. The module depends on no other process in the image.
- `Dockerfile` lines 43-45: `ENV NO_AUTOUPDATE=true` is set "as an env var rather than an entrypoint flag, so it still applies when the entrypoint is overridden"; the module still passes `--no-autoupdate`, which is harmless (see `## Token file` (d)).

## Token file

- (a) Flag: `tunnelTokenFileFlag` is a string flag named `token-file` (constant `TunnelTokenFileFlag`, `cmd/cloudflared/tunnel/subcommands.go` line 43; definition lines 132-136) with environment variable `TUNNEL_TOKEN_FILE`. It is in the flag list of the `run` subcommand (`buildRunCommand`, line 723), so `tunnel run --token-file <path>` parses. At `2025.4.0` the same file defines it (`TunnelTokenFileFlag` line 44, read at line 761); at `2024.12.2` the file has no `token-file`, which matches the spec's Versions row (2025.4.0 is the first release with it).
- (b) Reader: `runCommand` (`subcommands.go` lines 749-799). Lines 769-780: `tokenStr := c.String(TunnelTokenFlag)`; when it is empty and `--token-file` is set, `os.ReadFile(tokenFile)` (line 774); a read error returns `cliutil.UsageError("Failed to read token file: %s", err.Error())` (line 776); otherwise `tokenStr = strings.TrimSpace(string(data))` (line 778), so a trailing newline is removed. An empty or white-space-only file leaves `tokenStr` empty, so line 782 is false and execution falls to the tunnel-name branch (lines 787-797): with no positional argument and no `tunnel:` in a configuration file it returns the usage error `"cloudflared tunnel run" requires the ID or name of the tunnel to run as the last command line argument or in the configuration file.` (line 793), exit code below. A non-empty string that does not decode returns `Provided Tunnel token is not valid.` (line 786); the decoder's own error is dropped at line 783.
- (c) Precedence: `--token` (environment `TUNNEL_TOKEN`) wins over `--token-file` (lines 769-772; the flag's usage text at line 129 says so). When a token string exists (lines 782-786) the code never reads the positional argument or `--credentials-file`: they are consulted only in the `else` branch (lines 787-797). The module's command line gives only `--token-file` and sets no environment variable (the image sets none of `TUNNEL_TOKEN`, `TUNNEL_TOKEN_FILE`, `TUNNEL_CRED_*`), so it is unambiguous. One check precedes the reader: more than one positional argument is a usage error (line 759).
- (d) `--no-autoupdate`: the flag `NoAutoUpdate = "no-autoupdate"` (`cmd/cloudflared/flags/flags.go` line 127; environment `NO_AUTOUPDATE`) is defined in `tunnelFlags` (`cmd/cloudflared/tunnel/cmd.go` lines 924-930). The `tunnel` command takes those flags (`buildTunnelCommand`, `Flags: tunnelFlags(false)`, line 222) and the root command takes them hidden (`main.go` line 85 `app.Flags = flags()`, lines 161-164 `tunnel.Flags()`). `run` does not define it itself, so `tunnel --no-autoupdate run ...` is the form that parses and `tunnel run --no-autoupdate` is not. `StartServer` reads it with `c.Bool(cfdflags.NoAutoUpdate)` (`cmd.go` line 398), and `isAutoupdateEnabled` returns `!updateDisabled && updateFreq != 0` once the platform checks pass (`cmd/cloudflared/updater/update.go` lines 271-276).
- (e) Mode and owner: the only use of the path is `os.ReadFile(tokenFile)` (`subcommands.go` line 774), a read-only open; `rg` finds no other reference to `TunnelTokenFileFlag` or `tokenFile` in the tree's `cmd/` files fetched, no write beside the file and no `chmod`. A file owned by 65532 with mode 0400, bind-mounted read-only, is readable by the process, which runs as 65532 (`## Image` (a)). A read-only bind mount is not written to.
- The token's content is not logged: the contents never pass through a flag when `--token-file` is used. `logClientOptions` (`configuration.go` lines 63-95, called at `cmd.go` line 380) logs `Settings: map[...]` with every flag's value except `secretFlags` (`credentials-contents`, `token`, `allowed-mail`, lines 41-45), which print as `*****`, and the `TUNNEL_*` environment variables except `TUNNEL_TOKEN` and `TUNNEL_CRED_CONTENTS` (lines 46 and 77-94). `token-file` is not a secret flag, so the log shows the path `/run/darkpanel/token`, never the file's text.

## Token format

Decoder: `ParseToken` (`cmd/cloudflared/tunnel/subcommands.go` lines 801-812): `base64.StdEncoding.DecodeString(tokenStr)` (padded, standard alphabet; the decoder skips `\r` and `\n`), then `json.Unmarshal` into `connection.TunnelToken` (`connection/connection.go` lines 80-86):

```go
type TunnelToken struct {
	AccountTag   string    `json:"a"`
	TunnelSecret []byte    `json:"s"`
	TunnelID     uuid.UUID `json:"t"`
	Endpoint     string    `json:"e,omitempty"`
}
```

- `a` is the account tag (string), `t` the tunnel UUID (string), `s` the tunnel secret: a Go `[]byte`, so JSON carries it as standard base64 text. `e` is an optional endpoint (omitted when empty). `TunnelToken.Encode` (lines 98-105) is the inverse: `json.Marshal` then `base64.StdEncoding.EncodeToString`.
- What the decoder rejects, by running a copy of `TunnelToken` and `ParseToken` (taken from the lines above) against `google/uuid` v1.6.0 (the version `go.mod` names): not standard base64 (`illegal base64 data at input byte N`, which includes URL-safe and unpadded base64); not JSON (`invalid character 'h' looking for beginning of value`); `t` empty (`invalid UUID length: 0`); `t` not a UUID (`invalid UUID length: 10` for the string `not-a-uuid`, `invalid UUID format` for a 36-character string with the wrong separators; `google/uuid` `Parse` lines 68-115 also accepts 32 hex digits, `urn:uuid:` and braces forms and either letter case); `s` not valid base64 (`json: cannot unmarshal string into Go struct field TunnelToken.s of type []uint8: illegal base64 data at input byte 0`).
- What it accepts: a missing `t` (the zero UUID `00000000-0000-0000-0000-000000000000`), an empty or missing `s` (a nil secret), an empty `a`, and an upper-case `t`. The decoder does not check them; the edge may.
- Whatever the decoder returns, `runCommand` shows the user only `Provided Tunnel token is not valid.` (`subcommands.go` line 786), so a test cannot read the decoder's message from the container's log.
- A Go expression that builds a well-formed token for a tunnel id (lower-case UUID) from an account tag and a secret; it was run against the copied decoder and decoded to the same account, tunnel id and secret:

  ```go
  base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"a":%q,"t":%q,"s":%q}`, account, tunnel, base64.StdEncoding.EncodeToString(secret))))
  ```

  `%q` quotes with Go syntax; for an account tag and a UUID made of ASCII letters, digits and hyphens that is also valid JSON. Standard base64 of the secret needs no escaping. This is the form Task 3's `agToken` builds; it does not differ from the plan.

## Log lines

cloudflared logs through zerolog to standard error. By default (`--output default`, environment `TUNNEL_LOG_OUTPUT` unset) the console writer is used (`logger/create.go` lines 186-196, the `config.asJSON` branch at line 187 is the JSON mode), without colour when standard error is not a terminal (line 193), and the level is `info` (`cliutil/logger.go` lines 28-29). The console writer prints `<time> <LEVEL> <message> <key>=<value>...` with the keys sorted (`rs/zerolog` v1.20.0 `console.go` `writeFields`, lines 121-141, and the `INF`/`ERR` level names at lines 325-337).

- (a) After the token is read and decoded, before the edge is contacted: `Starting tunnel tunnelID=<uuid>`. It comes from `runWithCredentials` (`cmd/cloudflared/tunnel/subcommand_context.go` line 279): `sc.log.Info().Str(LogFieldTunnelID, credentials.TunnelID.String()).Msg("Starting tunnel")`, with `LogFieldTunnelID = "tunnelID"` (`subcommands.go` line 53). `<uuid>` is the lower-case hyphenated UUID of the token's `t` member (`uuid.UUID.String`). Then `StartServer` is called (line 281), which is where the edge is first contacted. A test searches the container's log for `Starting tunnel tunnelID=<the tunnel id>`. In JSON output the same event has `"message":"Starting tunnel"` and `"tunnelID":"<uuid>"` instead. As a further positive signal the edge answered, `connection/observer.go` line 64 logs `Registered tunnel connection` (with `connIndex`, `location` and `protocol` fields) after a successful registration (`connection/control.go` line 104).
- (b) Token file unreadable: `runCommand` returns `cliutil.UsageError("Failed to read token file: %s", err.Error())` (`subcommands.go` line 776). `WithErrorHandler` (`cmd/cloudflared/cliutil/errors.go` lines 25-37) turns a usage error into `cli.Exit(msg, -1)` with `msg = "<error>\nSee 'cloudflared <command> --help'."` (line 30); `HandleExitCoder` (`ipostelnik/cli` `errors.go` lines 104-119) prints it to `ErrWriter` (standard error, `app.go` lines 124 and 174-175) and calls `OsExiter(-1)`. The text a test searches for is `Failed to read token file: open /run/darkpanel/token:` followed by the OS error (`permission denied` or `no such file or directory`); the process then exits (status 255 as a Linux process status, from `os.Exit(-1)`, which is inference from Go's semantics, not read in the sources). This message is plain text on standard error, not a zerolog line, so it has no timestamp or level.
- (b) Token does not decode: `Provided Tunnel token is not valid.` (`subcommands.go` line 786), by the same route: standard error, followed by the line `See 'cloudflared tunnel run --help'.` (`ctx.Command.FullName()` is `tunnel run`), exit through `cli.Exit(msg, -1)`.
- (b) Empty file: the `requires the ID or name of the tunnel to run` message of `## Token file` (b), by the same route.
- Failures after the token decodes go through `StartServer`: an initial connection failure logs `initial tunnel connection failed` (`supervisor/supervisor.go` line 124, level error, with an `error` field) and `Initiating shutdown` (`cmd.go` line 574); both are zerolog lines.

## Unauthorized token

Behaviour: unsettled

The source decides the branch from the edge's answer, and the answer is not in the source. For a well-formed token whose tunnel the edge does not know:

- `ServeControlStream` calls `RegisterConnection` with the token's account tag, secret and tunnel id (`connection/control.go` lines 86-92). An error other than `DuplicateConnectionError` becomes `serverRegistrationErrorFromRPC(err)` (line 100): `Permanent: false` only when the edge's answer is a `*pogs.RetryableError` (the edge sets `ShouldRetry` and `RetryAfter` in `MarshalError`, `tunnelrpc/pogs/registration_server.go` lines 302-312), and `Permanent: true` otherwise (`connection/errors.go` lines 43-54).
- `startFirstTunnel` (`supervisor/supervisor.go` lines 232-279) loops on the first connection. Line 253: it stops only when the backoff reports no retry left, and the first connection's backoff is built with `retryForever` true (line 197, `retry.NewBackoff(s.config.Retries, retry.DefaultBaseTime, true)`; `GetMaxBackoffDuration`, `retry/backoffhandler.go` lines 56-77, then always answers true), so that test never stops it. Lines 256-260: an error whose text contains `Unauthorized` is retried (`continue`), "because we hope them to be transient due to edge propagation lag on new Tunnels". Lines 261-266: otherwise a `ServerRegisterTunnelError` that is `Permanent` returns (the first connection gives up) and a non-permanent one retries. Each retry waits an exponential backoff of up to 2^`Retries` seconds (`Retries` defaults to 5, `cmd.go` lines 774-780), announced as `Retrying connection in up to <duration>` (`supervisor/tunnel.go` line 290).
- When `startFirstTunnel` returns an error, `initialize` returns it (`supervisor.go` lines 209-210), `Run` logs `initial tunnel connection failed` and returns it (lines 124-125), `StartTunnelDaemon`'s result reaches `errC` (`cmd.go` line 524), `waitToShutdown` logs `Initiating shutdown` and returns it (lines 573-574 and 605), and `WithErrorHandler` turns it into `cli.Exit(err.Error(), 1)` (`cliutil/errors.go` lines 32-34): the process exits with status 1, which the `unless-stopped` restart policy restarts.
- So the result is `restarting` if the edge's refusal is permanent and its text lacks `Unauthorized`, and `running` (retrying forever with backoff, no exit) if the text contains `Unauthorized` or the refusal is marked retryable. Which of these the edge sends for an unknown tunnel is not in cloudflared's source. Task 15 observes it.

## Unverified

- The edge's refusal of a well-formed token for an unknown tunnel: its text, whether it is marked retryable, and therefore whether the container keeps running or restarts (`## Unauthorized token`). The sources fix only the two branches.
- The exact bytes of the log lines. The formats above come from the source of cloudflared, `zerolog` v1.20.0 and the cli fork; no container was run. The harness (Task 15) checks `Starting tunnel tunnelID=<id>` and the `Failed to read token file: ...` text against a real container.
- The exit status of the process after a usage error (255) is inference from `os.Exit(-1)`; the status after a supervisor error (1) is read from `cliutil/errors.go` lines 32-34 and `HandleExitCoder`.
- Whether the edge accepts a token whose `a` is not the account that owns the tunnel, or whose `s` is not the tunnel's secret: the decoder checks neither (`## Token format`). Task 15 uses a real token from the Cloudflare API for the positive case.
- The `linux/arm64` image was not inspected beyond its place in the index; its config and layers are taken to match the amd64 image built from the same `Dockerfile`.

## Divergences

none

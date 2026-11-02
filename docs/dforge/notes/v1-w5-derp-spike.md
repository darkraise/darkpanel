# v1 wave 5 DERP spike

Checked: 2026-11-02

Evidence behind the "Recorded contract (DERP, ...)" bullet of spec 4.3. Line numbers are those of the files as fetched
at the tag named; paths under `derp/derpserver/` and the other fetched files are listed under Sources.

## Sources

Raw source files are `https://raw.githubusercontent.com/tailscale/tailscale/<tag>/<path>`.

- v1.104.1 cmd/derper/derper.go: 200
- v1.104.1 cmd/derper/cert.go: 200
- v1.104.1 cmd/derper/mesh.go: 200
- v1.104.1 cmd/derper/bootstrap_dns.go: 200
- v1.104.1 derp/derp_server.go: 404 (moved; the page was deleted and the directory listed instead)
- v1.104.1 derp/derpserver/derpserver.go: 200
- v1.104.1 derp/derpserver/handler.go: 200
- v1.104.1 tailcfg/derpmap.go: 200
- v1.104.1 net/tlsdial/tlsdial.go: 200
- v1.104.1 ipn/localapi/localapi.go: 200
- v1.104.1 ipn/ipnserver/actor.go: 200
- v1.104.1 ipn/ipnserver/server.go: 200
- v1.104.1 safesocket/unixsocket.go: 200
- v1.104.1 safesocket/safesocket.go: 200
- v1.104.1 client/local/local.go: 200
- v1.104.1 paths/paths.go: 200
- v1.104.1 go.mod: 200
- v1.82.0 ipn/localapi/localapi.go: 200
- https://api.github.com/repos/tailscale/tailscale/contents/derp?ref=v1.104.1: 200
- https://api.github.com/repos/tailscale/tailscale/contents/derp/derpserver?ref=v1.104.1: 200
- https://tailscale.com/kb/1118/custom-derp-servers: 200
- https://tailscale.com/kb/1337/policy-syntax: 200
- Go module proxy, through `go get tailscale.com/cmd/derper@v1.104.1` (`GOMODCACHE` and `GOCACHE` under `$TMPDIR`): exit status 0

`ipn/ipnserver/server.go`, `safesocket/safesocket.go`, `paths/paths.go`, `cmd/derper/mesh.go`,
`cmd/derper/bootstrap_dns.go` and the two `derp/derpserver` files are not in the task's path list; each holds a
symbol the listed files call (`actor.Permissions`, `PlatformUsesPeerCreds`, `DefaultTailscaledSocket`, `startMesh`,
`refreshBootstrapDNSLoop`, `Server.verifyClient`, `ProbeHandler`).

## Findings

### Flags

`cmd/derper/derper.go` at v1.104.1:

```text
60  addr        = flag.String("a", ":443", ...)
61  httpPort    = flag.Int("http-port", 80, "... Set to -1 to disable. ...")
62  stunPort    = flag.Int("stun-port", 3478, ...)
63  configPath  = flag.String("c", "", "config file path")
64  certMode    = flag.String("certmode", "letsencrypt", "mode for getting a cert. possible options: manual, letsencrypt, gcp")
```

- Lines 65, 66, 71, 83: `-certdir` string (`tsweb.DefaultCertDir("derper-certs")`), `-hostname` string
  (`"derp.tailscale.com"`; help: "It can be an IP address when --certmode=manual"), `-stun` bool (true),
  `-verify-clients` bool (false). Lines 84 and 85: `-verify-client-url` string (""), `-verify-client-url-fail-open`
  bool (true). Line 88: `-socket` string (""). The plan's vector passes `-stun`, which is the default and harmless.
- `cmd/derper/cert.go` 54-106: modes `letsencrypt` and `gcp` (one case), `manual`; default case returns
  `unsupport cert mode: %q`. Line 48: an empty `-certdir` is `missing required --certdir flag`. With `letsencrypt` and an
  IP `-hostname` (no `-acme-ip-certs`) it refuses: "use --certmode=manual for a self-signed cert" (lines 56-62).
- ACME on port 80: `derper.go:410` `port80mux.Handle("/", certManager.HTTPHandler(tsweb.Port80Handler{Main: mux}))`;
  `cert.go:43` documents the method as "HTTPHandler handle ACME related request, if any."; the manual manager returns the
  fallback (`cert.go:184-186`). The `autocert` implementation itself is in a module this task did not read.
- Probe: `derper.go:281-282` registers `/derp/probe` and `/derp/latency-check` on `mux`.
  `derp/derpserver/handler.go:129-136`:

  ```text
  switch r.Method {
  case "HEAD", "GET":
  	w.Header().Set("Access-Control-Allow-Origin", "*")
  default:
  	http.Error(w, "bogus probe method", http.StatusMethodNotAllowed)
  ```

  GET and HEAD write no status and no body (net/http sends 200). TLS: `derper.go:192`
  `serveTLS := tsweb.IsProd443(*addr) || *certMode == "manual"`, and `:404` the TLS handler ends in `mux.ServeHTTP`.
- Key file: `derper.go:118-128` without `-c` a non-root user gets `log.Fatalf("derper: -c <config path> not specified")`
  (root defaults to `/var/lib/derper/derper.key`). `:147-162`: `os.MkdirAll(filepath.Dir(*configPath), 0777)` and
  `atomicfile.WriteFile(*configPath, b, 0600)`, so derper creates it, mode 0600, and keeps an existing one (`:130-145`).
- Verdict: every flag of Contracts "DERP server unit" `Flags` exists under that name. Matches the plan: yes.

### Verify clients

- (a) `derp/derpserver/derpserver.go:1774-1775`:

  ```text
  if s.verifyClientsLocalTailscaled {
  	_, err := s.localClient.WhoIsNodeKey(ctx, clientKey)
  ```

  `client/local/local.go:406-407`: `WhoIsNodeKey` GETs `"/localapi/v0/whois?addr="+url.QueryEscape(key.String())`; a
  404 maps to `ErrPeerNotFound` (`:408-411`, declared `:399`). `derpserver.go:1776-1777`: that error returns
  "peer %v not authorized (not found in local tailscaled)". The local client is the `localClient local.Client` field
  (`derpserver.go:145`).
- (b) `local.go:105-109` `socket()` returns `lc.Socket` or `paths.DefaultTailscaledSocket()`; `paths/paths.go:85-86`
  returns `/var/run/tailscale/tailscaled.sock` when `/var/run` is a directory (Linux, no special distro). Flag:
  `derper.go:88` `-socket`, passed at `:196`; `derpserver.go:625-626` sets `Socket` and `UseSocketOnly = path != ""`.
- (c) `ipn/localapi/localapi.go:557-559` (`serveWhoIsWithBackend`): `if !h.PermitRead { ... "whois access denied" ...
  http.StatusForbidden }`; `status` (`:845-847`) also needs only `PermitRead`. `localapi.go:217-220`:
  "PermitWrite ... effectively means that the user is root or the admin (operator user)". `ipn/ipnserver/server.go:218`
  `lah.PermitRead, lah.PermitWrite = actor.Permissions(lb.OperatorUserID())`; `:351-352`:

  ```text
  if a.ci.IsUnixSock() {
  	return true, !a.ci.IsReadonlyConn(operatorUID, logger.Discard)
  ```

  `ipn/ipnserver/actor.go:250-251` "root is always admin"; `:254-256` "if non-root, must be operator AND able to execute
  "sudo tailscale"". Socket
  mode: `safesocket/unixsocket.go:45,69` `perm := socketPermissionsForOS()` then `os.Chmod(path, perm)`; `:85-90` returns
  0666 when `PlatformUsesPeerCreds()`, else 0600; `safesocket/safesocket.go:146-158` is true for linux when the build
  has the `HasUnixSocketIdentity` feature. A user that is neither root nor the operator therefore reads (whois needs
  read only) and cannot write.
- (d) `derpserver.go:1779-1784`: a tailscaled error other than `ErrPeerNotFound` returns
  "failed to query local tailscaled status for %v: %w"; `:1151-1152` `accept` returns "client %v rejected: %v", which
  drops the connection. `derper.go:195` only stores the flag, and `localClient` is used only at `derpserver.go:1775`
  and in `checkVerifyClientsLocalTailscaled` (`:3183-3197`, reached from `ConsistencyCheck`, `:3170`, which the
  `/debug/check` handler calls). derper therefore starts without tailscaled and refuses every non-mesh client until
  tailscaled answers.
- (e) `derpserver.go:1806-1807`: `if s.verifyClientsURLFailOpen { s.logf("admission controller unreachable; allowing
  client %v", clientKey)`, inside the block opened by `if s.verifyClientsURL != ""` (`:1789`). The flag does not touch
  the tailscaled check.
- Conclusion: non-root works (read from the source, not run). Fail-open: no flag makes `-verify-clients` fail open.

### Version coupling

- The custom-DERP page, "Run a DERP server from source": "To stay compatible with Tailscale client updates, you might
  need to update DERP servers periodically by rebuilding from source with go install tailscale.com/cmd/derper@latest."
  "(Optional) Verify client traffic ...": "run tailscaled on the same device as your DERP server, and start derper with
  the --verify-clients flag". Neither says that derper and tailscaled must, or need not, share a version.
- `derpserver.go:1779-1783`:

  ```text
  if strings.Contains(err.Error(), "invalid 'addr' parameter") {
  	// Issue 12617
  	return errors.New("tailscaled version is too old (out of sync with derper binary)")
  ```

  A runtime message for a tailscaled whose whois handler does not accept the `nodekey:` form; it names no version rule.
- A search of `derper.go`, `cert.go`, `derpserver.go` and `local.go` for "same version", "older", "newer" and
  "out of sync" found no comment about the version of tailscaled; that message is the only hit that concerns it.
- Supporting facts, not proof: `serveWhoIsWithBackend` from its first line through the 404 `"no match for IP:port"` is
  byte-identical at `v1.82.0` (`localapi.go:495-`) and `v1.104.1` (`localapi.go:556-`) (40 lines compared with `diff`);
  both gate on `PermitRead`. `v1.104.1` adds only the `svc_name` and `dst_ip` capability lookups after it.
- Outcome: unbacked.

### Certificate

- Names (`cert.go:118-120`):

  ```text
  keyname := unsafeHostnameCharacters.ReplaceAllString(hostname, "")
  crtPath := filepath.Join(certdir, keyname+".crt")
  keyPath := filepath.Join(certdir, keyname+".key")
  ```

  with `cert.go:34` `unsafeHostnameCharacters = regexp.MustCompile("[^a-zA-Z0-9-\\.]")`. An IPv4 literal is unchanged; the
  colons of an IPv6 literal are removed (`2001:db8::1` gives `2001db81`).
- Generation (`cert.go:121-129`): `tls.LoadX509KeyPair(crtPath, keyPath)`; on error, `os.IsNotExist(err) && hostnameIP !=
  nil` calls `createSelfSignedIPCert`, so it happens when the pair cannot be read because a file is missing, for an IP
  hostname only. Any other load error is returned and derper exits (`:130-132`).
- Certificate (`cert.go:194-220`): ECDSA P-256 key, 128-bit serial, `Subject: pkix.Name{CommonName: ipStr}`,
  `NotBefore: now`, `NotAfter: now.AddDate(1, 0, 0), // expires in 1 year; a bit over that is rejected by macOS etc`,
  `KeyUsage` digital signature and key encipherment, `ExtKeyUsage` server auth, `IPAddresses = []net.IP{ip}`.
- Files (`cert.go:237-243`): `os.MkdirAll(filepath.Dir(crtPath), 0700)`, `os.WriteFile(crtPath, certPEM, 0644)`,
  `os.WriteFile(keyPath, keyPEM, 0600)`; the directory need not exist beforehand.
- Start only: `NewManualCertManager` runs once from `derper.go:359`; `manualCertManager` holds `cert *tls.Certificate`
  (`cert.go:110-114`) and `getCertificate` returns copies of it (`:172-182`); the only check at load is
  `x509Cert.VerifyHostname(hostname)` (`:140`), no expiry check.
- Meta certificate: `derper.go:364` `s.ModifyTLSConfigToAddMetaCert(httpsrv.TLSConfig)`; `derpserver.go:871` appends
  `s.MetaCert()` (CN prefix `derpconst.MetaCertCommonNamePrefix`, `:825`) to the served chain.
- Pin: `cert.go:150` `CertName: fmt.Sprintf("sha256-raw:%-02x", sha256.Sum256(x509Cert.Raw))`;
  `tailcfg/derpmap.go:209-212` "if CertName starts with "sha256-raw:", then the rest of the string is a hex-encoded
  SHA256 of the cert to expect. This is used for self-signed certs."; `net/tlsdial/tlsdial.go:316-358`: skips certs whose
  CN starts with the meta prefix (`:331`), "unexpected multiple certs presented" for a second one (`:335`),
  `fmt.Sprintf("%02x", sha256.Sum256(cert.Raw)) != wantFullCertSHA256Hex` (`:337`), `cert.VerifyHostname(dialedHost)` only
  when `dialedHost != ""` (`:340-341`), `now.After(cert.NotAfter)` "cert expired" (`:346`), `now.Before(cert.NotBefore)`
  "cert not yet valid" (`:349`). The code that strips the `sha256-raw:` prefix is not in the files read.
- Start without a tailnet: `derper.go` `main` stores `-verify-clients` without calling tailscaled (`:195`); the local
  client is used only per client and by `/debug/check`; `startMesh` returns nil when `-mesh-with` is empty
  (`mesh.go:22-24`); `refreshBootstrapDNSLoop` returns when both bootstrap flags are empty
  (`bootstrap_dns.go:55-58`); the setec store is created only when `-secrets-url` is set (`derper.go:218`). The mesh PSK
  default (`defaultMeshPSKFile`, `derper.go:484-495`) reads a local file only. Starts without a tailnet: yes.
- Plan comparison: spec 4.3 `ip` bullet, Contracts "DERP install, update and renewal effects" and "DERP server unit"
  `CertFiles` say `<ip>.crt` and `<ip>.key`; IPv4 and the one-year, absent-only, read-at-start behaviour agree; IPv6 does not
  (see Divergences). Matches the plan: no.

### Region node

- `tailcfg/derpmap.go` has no `json` renames on `DERPNode` and `DERPRegion` apart from `omitempty` (lines 142-143, 159,
  169, 214, 221, 228, 233, 237, 243-255); the JSON name of a field is its Go name.
- `DERPNode` (lines 187-256): `Name` ("a unique node name (across all regions). It is not a host name. It's typically of
  the form "1b", "2a", "3b", etc."), `RegionID`, `HostName` ("required but need not be unique"), `CertName`
  (lines 204-214), `IPv4` / `IPv6` (lines 216-228: "If the string is not an IPv4 address, IPv4 is not used; the
  conventional string to disable IPv4 (and not use DNS) is "none"."), `STUNPort` ("Zero means 3478. To disable STUN on
  this node, use -1."), `STUNOnly` ("only a STUN server and not a DERP server"), `DERPPort` ("If zero, 443 is used.").
- `DERPRegion` (lines 117-184): `RegionID` ("RegionIDs in range 900-999 are reserved for end users to run their own
  DERP nodes", lines 125-130), `RegionCode` ("a short name for the region"), `RegionName` ("a long English name for the
  region"), `Nodes`.
- `DERPMap` (lines 19-37): `Regions map[DERPRegionID]*DERPRegion` keyed by region id; `OmitDefaultRegions bool` with tag
  `json:"omitDefaultRegions,omitempty"` (line 36).
- Policy file: the custom-DERP page's examples put `"Regions"` and `"OmitDefaultRegions": true` side by side inside
  `"derpMap"`, set a default region to `null` to remove it (`"Regions": { "1": null }`), use `"Name": "1"` for region 900,
  and say "There can be only one DERP server per region." The policy-syntax page's `derpMap` section is two sentences and
  lists no fields.
- Plan comparison (Contracts "DERP region edits"): every field named exists; `<id>a` follows the documented node-name form;
  `DERPPort` 443 and `STUNPort` 3478 are the defaults written out; `IPv4` or `IPv6` plus `CertName` in `ip` mode is the
  documented self-signed shape. The page spells `OmitDefaultRegions` as the plan does; the struct tag is lower-case `o`.
  Matches the plan: yes.

### Visibility grant

- Searched both pages for "grant", "visible", "visibility", "whois", "tag:" and "netmap": the custom-DERP page uses
  "visibility" only for the data DERP cannot see, and neither page says what makes a client visible to the DERP host's
  tailscaled. The policy-syntax page's grant examples are of the form `{"src": [...], "dst": ["tag:..."], "ip": ["*"]}`
  (`"ip": ["*"]` is the only `ip` value in the saved page; `tcp:` does not occur).
- Grant: `{"src": ["*"], "dst": ["<tag>"], "ip": ["tcp:443"]}`; not documented; the provisional grant stands.

### Build

- Command: Step 3's script, `GOTOOLCHAIN=local`, `GOFLAGS=-modcacherw`, `go get tailscale.com/cmd/derper@v1.104.1` then
  `CGO_ENABLED=0 GOOS=linux GOARCH=<arch> go build -trimpath -ldflags "-s -w"`; exit 0 for both.
- `ls -l`: `17662112 derper-v1.104.1-linux-amd64`, `16449696 derper-v1.104.1-linux-arm64`; both below 134217728.
- `go version -m` (amd64): `go1.27.3`, `path tailscale.com/cmd/derper`, `mod tailscale.com v1.104.1`,
  `build -trimpath=true`, `build CGO_ENABLED=0`, `build GOARCH=amd64`, `build GOOS=linux`; arm64: the same with
  `GOARCH=arm64`.
- `tailscale.com@v1.104.1`'s `go.mod` (`go.mod:3` and the module proxy's `.mod`): `go 1.27.1`, no `toolchain` line.
  This repository's `go.mod`: `go 1.27`, `toolchain go1.27.3`. The local toolchain (`go version`: `go1.27.3`) built it.
- Neither binary was run.

### Health check

The binary update's health check uses "tailscaled answers `Status` on its socket" in place of "derper's log showing that it reached tailscaled": the exec allowlist has no command that reads a journal.

### Divergences

Two entries, one difference (the file name derper gives an IPv6 address) stated in two Contracts sections; spec 4.3's `ip`
bullet and Global Constraints "Values copied from the spec" repeat `<certdir>/<ip>.crt` too.

## Divergences

- DERP server unit — CertFiles returns "<ip>.crt" and "<ip>.key" for the ip mode's address, an IPv4 or IPv6 literal → derper names the files after the hostname with every character outside [a-zA-Z0-9-.] removed, so an IPv4 literal is unchanged but an IPv6 literal loses its colons (2001:db8::1 gives 2001db81.crt and 2001db81.key) — https://raw.githubusercontent.com/tailscale/tailscale/v1.104.1/cmd/derper/cert.go
- DERP install, update and renewal effects — install waits for, and cert.renew renames, certs/<ip>.crt and certs/<ip>.key for the ip mode's address → for an IPv6 address derper writes those files without the address's colons (certs/2001db81.crt for 2001:db8::1), so those waits and RenameIn calls name files that do not exist — https://raw.githubusercontent.com/tailscale/tailscale/v1.104.1/cmd/derper/cert.go

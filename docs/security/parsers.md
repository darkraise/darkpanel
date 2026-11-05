# Parser inventory

Every parser of host-tool output, configuration files and untrusted input in the repository is listed here with the Go fuzz target that exercises it. `scripts/fuzz.sh` reads the `## Fuzzed parsers` table: `make fuzz` and `.github/workflows/fuzz.yml` run each target, and `scripts/fuzz.sh --check` fails when a row names a target that does not exist in its package or a fuzz target in `cmd/`, `internal/`, `release/` or `test/` has no row.

A cell never contains a pipe character. Several rows may name one `(Package, Target)` pair when one target covers several parsers. A target that needs a build tag lists it under Tags.

## Fuzzed parsers

| Parser | Input | Package | Target | Tags |
|---|---|---|---|---|
| `netfilter.Parse` | `*-save` output of iptables and ip6tables | `./internal/platform/netfilter` | `FuzzParse` | - |
| `netfilter.Key` | a rule parsed from `*-save` output | `./internal/platform/netfilter` | `FuzzParse` | - |
| `netfilter.ParseRestore` | `iptables-restore` input: the UFW files of one load | `./internal/platform/netfilter` | `FuzzParseRestore` | - |
| `netstat.parseEntry` | a line of `/proc/net/{tcp,udp}{,6}` | `./internal/platform/netstat` | `FuzzParseSocketLine` | - |
| `netstat.parseAddrPort` | an address and port field of `/proc/net/{tcp,udp}{,6}` | `./internal/platform/netstat` | `FuzzParseSocketLine` | - |
| `netstat.parseNetlink` | rtnetlink messages | `./internal/platform/netstat` | `FuzzParseNetlink` | - |
| `netstat.parseRoute` | an rtnetlink route message | `./internal/platform/netstat` | `FuzzParseNetlink` | - |
| `platform.scanMarkers` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.replaceBlock` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.removeBlock` | marked blocks in a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `platform.splitLines` | the text of a file Darkpanel edits | `./internal/platform` | `FuzzMarkedBlocks` | - |
| `dockerapi.(*LogStream).Next` | the multiplexed log stream of the Docker Engine API | `./internal/platform/dockerapi` | `FuzzLogStream` | - |
| `dockerapi.parseVersion` | a Docker Engine API version string | `./internal/platform/dockerapi` | `FuzzCompareVersions` | - |
| `dockerapi.compareVersions` | two Docker Engine API version strings | `./internal/platform/dockerapi` | `FuzzCompareVersions` | - |
| `tslocal.(*Prefs).UnmarshalJSON` | the preferences JSON of the Tailscale local API | `./internal/platform/tslocal` | `FuzzPrefsJSON` | - |
| `tslocal.(*Prefs).MarshalJSON` | the preferences JSON of the Tailscale local API | `./internal/platform/tslocal` | `FuzzPrefsJSON` | - |
| `compose.Parse` | `docker compose config --format json` output | `./internal/modules/docker/compose` | `FuzzParse` | - |
| `docker.containerDetailOf` | container inspect JSON of the Docker Engine API | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.envVars` | the environment of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.redactArgs` | the command line of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.redactLabels` | the labels of a container inspect document | `./internal/modules/docker` | `FuzzContainerDetail` | - |
| `docker.parseAPIVersion` | the Docker Engine API version string | `./internal/modules/docker` | `FuzzParseAPIVersion` | - |
| `logfilter.(*Filter).Push` | container log bytes | `./internal/modules/docker/logfilter` | `FuzzFilterChunking` | - |
| `logfilter.(*Filter).Flush` | container log bytes | `./internal/modules/docker/logfilter` | `FuzzFilterChunking` | - |
| `model.ParseTuple` | `### tuple ###` lines of UFW rule files | `./internal/modules/firewall/model` | `FuzzParseTuple` | - |
| `model.ParseTag` | the comment of a UFW rule | `./internal/modules/firewall/model` | `FuzzParseTag` | - |
| `conf.Read` | `ufw.conf`, `/etc/default/ufw` and `sysctl.conf` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.parseShell` | `ufw.conf` and `/etc/default/ufw` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.parseSysctl` | `sysctl.conf` | `./internal/modules/firewall/conf` | `FuzzReadFiles` | - |
| `conf.ConfiguredIPv6` | `/etc/default/ufw` | `./internal/modules/firewall/conf` | `FuzzConfiguredIPv6` | - |
| `conf.SysctlKey` | a `sysctl.conf` key | `./internal/modules/firewall/conf` | `FuzzConfiguredIPv6` | - |
| `loaded.parseNFTTables` | `nft list tables` output | `./internal/modules/firewall/loaded` | `FuzzParseNFTTable` | - |
| `loaded.parseNFTTable` | `nft list table` output | `./internal/modules/firewall/loaded` | `FuzzParseNFTTable` | - |
| `loaded.toolVariant` | `iptables --version` output | `./internal/modules/firewall/loaded` | `FuzzToolVariant` | - |
| `firewall.installedVersion` | `/var/lib/dpkg/status` | `./internal/modules/firewall` | `FuzzInstalledVersion` | - |
| `firewall.upstreamAtLeast` | a Debian package version | `./internal/modules/firewall` | `FuzzInstalledVersion` | - |
| `dockerfw.BlockOf` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `dockerfw.WithBlock` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `dockerfw.WithoutBlock` | the marked block in `after.rules` and `after6.rules` | `./internal/modules/firewall/dockerfw` | `FuzzBlock` | - |
| `predict.Edit` | `ufw` command arguments applied to the UFW rule files | `./internal/modules/firewall/predict` | `FuzzEdit` | - |
| `predict.parseVector` | the rule vector of a `ufw` command | `./internal/modules/firewall/predict` | `FuzzEdit` | - |
| `predict.ufwDefaults` | the default policies of `/etc/default/ufw` | `./internal/modules/firewall/predict` | `FuzzSetDefault` | - |
| `predict.setDefault` | the default policies of `/etc/default/ufw` | `./internal/modules/firewall/predict` | `FuzzSetDefault` | - |
| `eval.Evaluate` | a ruleset that `netfilter.Parse` accepts | `./internal/modules/firewall/eval` | `FuzzEvaluate` | - |
| `eval.Unmodelled` | a ruleset that `netfilter.Parse` accepts | `./internal/modules/firewall/eval` | `FuzzEvaluate` | - |
| `tailscale.readOSRelease` | `/etc/os-release` | `./internal/modules/tailscale` | `FuzzReadOSRelease` | - |
| `tailscale.installedVersion` | `/var/lib/dpkg/status` | `./internal/modules/tailscale` | `FuzzDpkgStatus` | - |
| `tailscale.keyringFingerprints` | an OpenPGP keyring | `./internal/modules/tailscale` | `FuzzKeyringFingerprints` | - |
| `stun.Parse` | STUN replies | `./internal/modules/tailscale/stun` | `FuzzParse` | - |
| `stun.decodeAddress` | the address attributes of STUN replies | `./internal/modules/tailscale/stun` | `FuzzParse` | - |
| `policy.Apply` | a tailnet policy file in HuJSON and the edits to patch into it | `./internal/modules/tailnet/policy` | `FuzzApply` | - |
| `policy.pointerTokens` | a JSON pointer of a policy edit | `./internal/modules/tailnet/policy` | `FuzzApply` | - |
| `policy.Canonical` | a tailnet policy file in HuJSON | `./internal/modules/tailnet/policy` | `FuzzCanonical` | - |
| `policy.Diff` | two tailnet policy files in HuJSON | `./internal/modules/tailnet/policy` | `FuzzCanonical` | - |
| `derp.parseDERPMap` | the DERP map in a tailnet policy, as HuJSON | `./internal/modules/derp` | `FuzzParseDERPMap` | - |
| `artifact.ParseManifest` | the derper manifest | `./internal/modules/derp/artifact` | `FuzzParseManifest` | - |
| `server.ParseCert` | a certificate in PEM form | `./internal/modules/derp/server` | `FuzzParseCert` | - |
| `ingress.(*Config).UnmarshalJSON` | the ingress configuration of a Cloudflare tunnel, as JSON | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `ingress.(*Rule).UnmarshalJSON` | one ingress rule of a Cloudflare tunnel, as JSON | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `ingress.Hash` | an ingress configuration of a Cloudflare tunnel | `./internal/modules/cloudflare/ingress` | `FuzzConfigJSON` | - |
| `cloudflare.serviceHost` | the service URL of a tunnel ingress rule | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `cloudflare.legacyIPv4` | the host of a tunnel service URL | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `cloudflare.serviceProblem` | the service URL of a tunnel ingress rule | `./internal/modules/cloudflare` | `FuzzServiceHost` | - |
| `proxy.CheckUpstream` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `proxy.parseNginxIPv4` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `proxy.parseInetAton` | the forward host of an NPM proxy host | `./internal/modules/npm/proxy` | `FuzzCheckUpstream` | - |
| `update.ParseVersion` | a version in the release index | `./internal/update` | `FuzzParseVersion` | - |
| `update.Compare` | two versions in the release index | `./internal/update` | `FuzzParseVersion` | - |
| `update.checksumFor` | `checksums.txt` of a release | `./internal/update` | `FuzzChecksumFor` | - |
| `update.extractBinary` | a release tarball | `./internal/update` | `FuzzExtractBinary` | - |
| `releasetool.parseVersion` | a version given to the release tooling | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.parseTag` | a release tag | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.compare` | two versions given to the release tooling | `./release/releasetool` | `FuzzParseVersion` | - |
| `releasetool.keyID` | a minisign signature file | `./release/releasetool` | `FuzzKeyID` | - |
| `transport.ParseBootstrap` | the enrollment bundle | `./internal/transport` | `FuzzParseBootstrap` | - |
| `transport.ParseToken` | an enrollment token | `./internal/transport` | `FuzzParseToken` | - |
| `harness.ParsePorts` | `nmap -oG -` output | `./test/harness` | `FuzzParsePorts` | harness |
| `harness.m3dParsePortEntry` | a port entry of `nmap -oG -` output | `./test/harness` | `FuzzParsePorts` | harness |
| `hostinfo.parseOSRelease` | `/etc/os-release` | `./internal/modules/hostinfo` | `FuzzParseOSRelease` | - |
| `hostinfo.parseCPUInfo` | `/proc/cpuinfo` | `./internal/modules/hostinfo` | `FuzzParseCPUInfo` | - |
| `hostinfo.parseMemInfo` | `/proc/meminfo` | `./internal/modules/hostinfo` | `FuzzParseMemInfo` | - |
| `hostinfo.parseLoadAvg` | `/proc/loadavg` | `./internal/modules/hostinfo` | `FuzzParseLoadAvg` | - |
| `hostinfo.parseUptime` | `/proc/uptime` | `./internal/modules/hostinfo` | `FuzzParseUptime` | - |
| `backup.formatReadHeader` | the header of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzReadHeader` | - |
| `backup.formatEncodeHeader` | the header of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzReadHeader` | - |
| `backup.frameNewReader` | the frames of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzFrames` | - |
| `backup.formatReadMembers` | the members of the frames of a `.dpbk` backup file | `./internal/controller/backup` | `FuzzFrames` | - |
| `backup.decodeJournal` | the restore journal | `./internal/controller/backup` | `FuzzDecodeJournal` | - |
| `backup.checkJournal` | the restore journal | `./internal/controller/backup` | `FuzzDecodeJournal` | - |
| `secret.(*Sealer).Open` | a sealed value from the database | `./internal/core/secret` | `FuzzOpen` | - |
| `secret.RedactJSON` | an audit or journal record in JSON | `./internal/core/secret` | `FuzzRedactJSON` | - |
| `cloud.(*Client).retryAfter` | the `Retry-After` header of a cloud API response | `./internal/controller/cloud` | `FuzzRetryAfter` | - |
| `events.ParseID` | the `Last-Event-ID` request header | `./internal/controller/events` | `FuzzParseID` | - |
| `cli.printEvents` | the server-sent event stream of the controller | `./internal/cli` | `FuzzPrintEvents` | - |
| `install.CheckAgentAddress` | the agent address flag of a unit file | `./internal/install` | `FuzzCheckAgentAddress` | - |

## Not fuzzed

| Parser | Input | Reason |
|---|---|---|
| Typed JSON decoding of provider API responses | Docker, Tailscale, Cloudflare and Nginx Proxy Manager API responses, and the JSON of the agent's mTLS peer | `encoding/json` decodes into typed structs; there is no hand-written parser |
| `systemd` state through D-Bus | unit and job state from systemd | typed D-Bus decoding, not a text grammar |
| `auth.parsePHC` | password hashes in Darkpanel's database | written only by Darkpanel |
| Firewall journal state | the firewall journal rows | written only by Darkpanel |
| `update.ReadRecord` | update transaction records under the update directory | root-owned state that only Darkpanel writes |
| `ValidateStaged` | the staged update binary and its record | root-owned state that only Darkpanel writes |
| Release index decoding (`update` source `releases.json`) | the release index of the update mirror | `encoding/json` decodes into typed structs after a size bound, and the format version is checked; there is no hand-written parser |
| Signature parsing in `update.VerifyChecksums` | the minisign signatures of `checksums.txt` from the update mirror | parsed and verified by the third-party `aead.dev/minisign`; Darkpanel's own code only compares the parsed algorithm and trusted comment |
| `backup.Read` as a whole | a `.dpbk` backup file | every input runs Argon2id at 64 MiB or more, so the header and the frames are fuzzed separately by `FuzzReadHeader` and `FuzzFrames` |
| Process ownership in `netstat` | `/proc/<pid>/fd` links | a file walk without a text grammar |
| `held` | a single value read from the host | no grammar beyond `strings.TrimSpace` |
| `lastLine` | the last line of a command's output | no grammar beyond `strings.TrimSpace` |
| `legacyTables` | a single value read from the host | no grammar beyond `strings.TrimSpace` |
| `readRPFilter` | a sysctl value | no grammar beyond `strconv.Atoi` |
| `readSysctl` | a sysctl value | no grammar beyond `strconv.Atoi` |
| `LoadPins` | `images.json` | repository-owned input |
| Command-line flags | arguments of `darkpanel` commands | parsed by cobra |

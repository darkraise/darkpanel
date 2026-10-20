# Versioning and compatibility

Darkpanel follows [Semantic Versioning 2.0.0](https://semver.org/). A release
is tagged `v<version>`, for example `v1.3.0` or `v1.3.0-beta.1`.

- **Major** releases may remove an action version or change behaviour
  incompatibly.
- **Minor** releases add features, actions and action versions, and may add
  database migrations.
- **Patch** releases fix bugs.

## Channels

A release with a prerelease part (`1.3.0-beta.1`) is published on the `beta`
channel; every other release is on `stable`. Each host follows one channel,
set in its update policy:

- `stable` hosts take only stable releases.
- `beta` hosts take every release, ordered by semantic-version precedence
  (`1.3.0-beta.1` comes before `1.3.0`), so a beta host moves on to the
  stable release when it ships. A beta host is never moved back to a lower
  stable release automatically.

## Releases and signatures

Each release publishes, under
`https://github.com/Darkraise/darkpanel/releases/download/v<version>/`:

- `darkpanel_<version>_linux_amd64.tar.gz` and
  `darkpanel_<version>_linux_arm64.tar.gz` (the binary, `LICENSE`,
  `README.md`);
- `checksums.txt`, the SHA-256 of every file above and of the SBOM and
  `install.sh`;
- `checksums.txt.<key id>.minisig`, a minisign signature of `checksums.txt`
  for each release key. Its trusted comment names the version, the channel
  and the file's purpose, so an older signed file cannot be replayed as a
  newer release;
- `darkpanel_<version>.cdx.json`, a CycloneDX software bill of materials;
- `install.sh`, the one-line installer, which embeds the trusted keys.

`install.sh` and the updater accept a release when any one signature from a
key built into them verifies. The list of releases,
`https://github.com/Darkraise/darkpanel/releases/download/index/releases.json`,
only names versions and channels; trust always comes from the signatures.

When the release key changes, releases are signed with both the old and the
new key for at least two minor releases. A host that skipped that whole
window updates through the last dual-signed release first.

## Action compatibility

Every action has a name and a version, such as `core.host.list/v1`. Once
released, an action version changes only in additive ways:

- allowed: new optional input fields, new output fields, new output enum
  values (clients accept values they do not know);
- not allowed: removing or renaming a field, changing a type, a default or a
  constraint, adding a required input field, removing an enum value, or
  changing what the action does or its class.

A breaking change ships as a new action version; the old version stays until
the next major release. Each release freezes its action schemas under
`schemas/released/v<version>/`, and the build checks the current schemas
against every released one.

## Controller and agent versions

The controller supports agents of its own minor version and of the previous
minor version, with the same major version and agent protocol 1. An agent
outside that window is shown as unsupported and accepts only update actions
until it is updated. The controller is updated first, on its own host, and
then updates agents up to its own version, one host at a time, stopping at
the first failure.

## Updates

- An update moves at most one minor version at a time. From 1.2 to 1.5 a
  host installs the newest patch of 1.3, then of 1.4, then 1.5, and each
  step must pass its health check before the next starts. A failure leaves
  the host on the last minor version that passed.
- A downgrade is refused unless forced, and a forced downgrade goes back at
  most one minor version.
- An update is refused while a change on the host still waits for
  confirmation.
- A new binary that fails its health check, or does not pass it within the
  deadline, is rolled back automatically to the previous binary within two
  minutes.

## Databases

Each service migrates its own database when it starts. Before applying a
migration it takes a backup of the database in its own directory
(`backups/<from>-<to>-<timestamp>/`, the last five kept).

Migrations follow expand, then contract: a release's migrations leave the
database usable by the previous minor version's binary, and destructive
changes ship only in a later release, once nothing reads the old shape. A
rollback therefore runs the previous binary on the migrated database without
restoring data. The backup is for disaster recovery only and is
never restored automatically.

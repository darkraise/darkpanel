# Module developer guide

This guide takes you from an empty directory to a module that the controller, the agent, the command line and the web interface all know. It assumes you write Go and React and have not seen this repository before. Every identifier it names exists in the code; the tests in `internal/docs` fail when one of them is renamed.

## What a module is

A module is a Go package under `internal/modules/<id>` whose exported value implements `core.Module`. That interface has one method, `Manifest() core.Manifest`, and the manifest is everything the module declares: its actions, resources, preflight checks, owned paths and migrations. [ADR 0002](../adr/0002-module-contract.md) (`docs/adr/0002-module-contract.md`) gives the reasons for the contract.

- Modules are compiled in. There are no runtime plugins: you add the package to the repository and rebuild.
- The id matches `^[a-z][a-z0-9]*$` and is none of the names in `core.ReservedNames`: `serve`, `agent`, `update`, `user`, `controller`, `change`, `uninstall`, `version`, `host`, `module`, `token`, `audit`, `help`, `completion` and `system`. The id becomes the first word of the module's commands, so a reserved name would clash with a command the CLI already has.
- A module runs on the controller, on the agent or on both. Each action says where it runs with `core.SideController` or `core.SideAgent`; the module's migrations and background tasks follow the same two sides.
- A module never changes another module's resources directly. It calls the owning module's actions, so the owner's locks, snapshots and audit entries apply. A composite action is how one module sequences another module's actions: it returns a list of steps, each naming a child action ([ADR 0014](../adr/0014-v1-core-extensions.md), `docs/adr/0014-v1-core-extensions.md`).

Besides `core.Module`, a module may implement three optional interfaces, each found with a type assertion on the module: `core.Uninstaller` (an agent-side uninstall step), which the uninstall code in `internal/install` finds, and `core.Streamer` (streams the controller relays as server-sent events) and `core.Background` (background tasks that run on the agent while the module is enabled), which the registry finds and checks. The rest of this guide covers `core.Uninstaller`; the other two are declared the same way.

## Create the module package

The smallest module is `hostinfo`. Read `internal/modules/hostinfo/hostinfo.go` and `internal/modules/hostinfo/hostinfo_test.go` next to this section. The package holds:

```text
internal/modules/hostinfo/
  hostinfo.go        the ID constant, Source, Module(), New() and Manifest()
  parse.go           parsers for /proc and /etc files
  hostinfo_test.go   tests over fixture file systems
  parse_test.go      parser tests
  testdata/          fixture files
```

The manifest of `hostinfo` is short:

```text
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Resources:      []core.ResourceDef{core.DefineResource(ID+".summary", m.summary)},
		Preflight:      []core.PreflightCheck{{ID: "meminfo", Run: m.checkMeminfo}},
	}
}
```

These are the fields of `core.Manifest`:

| Field | Meaning |
|---|---|
| `ID` | The module id. |
| `Version` | The module version, at least 1. |
| `Actions` | The module's actions, built with the constructors that start with `Define`, listed in the next section. |
| `Resources` | The module's resources, built with `core.DefineResource`. |
| `Settings` | A type reserved for module settings. Nothing reads it today, so leave it empty. |
| `Preflight` | The host checks that decide whether the module is available. |
| `OwnedPaths` | The files, blocks, directories, users, units, sysctl keys and service directories the module creates. |
| `Migrations` | The SQL migrations, one `fs.FS` for each side that has tables. |
| `DefaultEnabled` | Whether a new host has the module enabled. |
| `ExternalUnits` | The systemd units the module drives but does not own, with the operations it may run on each. |

`hostinfo` reads the host through a small `Source` struct: a file system rooted at `/`, a hostname function, a `statfs` function and a version function. `Module()` fills it from the real host, and the tests fill it with fixtures (`fstest.MapFS`). Because the module never opens `/proc` directly, its tests never touch the machine they run on. Give your module the same seam for everything it reads or runs.

## Declare actions

An action is a typed, versioned operation. Its name is `<module>.<noun>.<verb>`: three words in lower case, the first the module id, and the verb may contain hyphens (`npm.admin.reveal-password`). `core.ActionSpec` carries the declaration:

| Field | Meaning |
|---|---|
| `Name`, `Summary`, `Version` | The name, a one-line summary and the version, at least 1. |
| `Class` | The change class. The constructor sets it, except for `core.DefineMutating` and `core.DefineTask`, where you set `compensable` or `irreversible`. |
| `Side` | `core.SideController` or `core.SideAgent`. |
| `BreakGlass` | The controller's local socket also serves the action. Controller-side only. |
| `LockKeys` | The locks the action takes. |
| `CompensatedBy`, `ForwardOnly`, `Converges` | How a compensable action is undone, and whether running it again converges. |
| `RepairedBy` | The repair action of a revertible action. |
| `ApplyTimeout`, `LongRunning` | One time budget for apply and verify (0 means 30 seconds), and whether apply answers once the intent is stored. |
| `Unwind` | A composite's failure direction, `core.UnwindReverse` or `core.UnwindForward`. |
| `Guards` | The safety checks the plan may report. The input type then embeds `core.Guarded`. |
| `Available` | The action's own availability check; the error text is the reason. |
| `MinScope` | Raises the scope the class and side require, never lowers it. |
| `Internal`, `Reveal` | Set by `core.DefineInternalRead` and `core.DefineReveal`. |

Pick the constructor that matches what the action does. Each takes the spec and a handler:

| Constructor | Handler | Class it sets | Notes |
|---|---|---|---|
| `core.DefineRead` | `core.Reader`: `Read` | read | Reads take no locks. |
| `core.DefineMutating` | `core.Mutator`: `Plan`, `Apply`, `Verify` | the one you set: compensable or irreversible | |
| `core.DefineRevertible` | `core.Reverter`: the `core.Mutator` methods plus `Snapshot` and `Revert` | revertible | Agent side, with at least one lock key and a `RepairedBy` action. |
| `core.DefineComposite` | `core.Composer`: `Steps` and `Output` | composite | Controller side. You also choose `Unwind`; the lock key `composite:<module>` is set for you. |
| `core.DefineTask` | `core.TaskMutator`: `Plan`, `Task`, `Verify` | the one you set: compensable or irreversible | `Task` runs in a transient systemd unit. Agent side, with lock keys and an `ApplyTimeout`; the constructor sets `LongRunning`. |
| `core.DefineReveal` | a function returning `core.Revealed` | read | Returns one stored secret once. `MinScope` must be admin. |
| `core.DefineInternalRead` | a function returning `core.Revealed` | read | Only the controller dispatches it. The API and the CLI do not expose it. |

Three rules apply to every handler that changes a host:

- A plan never changes the host. It computes the diff, the guards and the state hash that apply later checks.
- `Verify` only reads the host. It runs under the change's deadline, and at startup recovery it can run with an empty output, so it must check the host from the input alone.
- A handler that also implements `core.Observer`, `core.Undoer` or `core.Recoverer` gets the matching lifecycle handler. A compensable action declares exactly one of an undo handler (`core.Undoer`), `CompensatedBy` and `ForwardOnly`; `ForwardOnly` also requires `Converges`.

Input and output are Go structs that encode to JSON. The `jsonschema` tags shape the published schema, a field tagged `secret:"true"` is a secret (redacted everywhere and never given a flag), and a pointer field of an output carries `jsonschema:"nullable"` because it encodes `null` (`TestPointerOutputsAreNullable` in `internal/modules/registry_test.go` fails otherwise).

The scope a caller needs follows from the class and side: read for a read action, operate for a mutation that runs on the agent, admin for every other mutation. `MinScope` can only raise that.

### Worked example

`npm.instance.status` in `internal/modules/npm/instance_status.go` is a small read action. Its declaration:

```text
func (n nm) instanceStatus() core.ActionDef {
	return core.DefineRead[StatusInput, InstanceStatus](core.ActionSpec{
		Name:    "npm.instance.status",
		Summary: "Show the Nginx Proxy Manager instance of this host and whether it runs and answers.",
		Version: 1,
		Side:    core.SideAgent,
	}, statusReader{n: n})
}
```

Its pieces live in these places:

- The handler, `statusReader.Read`, is in the same file. The module lists the action in `internal/modules/npm/actions.go`, which its `Manifest()` returns.
- The input type `StatusInput` (an empty struct) and the output type `InstanceStatus` are in `internal/modules/npm/wire.go`. `InstanceStatus.AdminExposure` is a pointer, so it carries `jsonschema:"nullable"`.
- The schema snapshot is `schemas/npm.instance.status/v1.json`. It holds the name, version, class, side, and the input and output JSON Schemas.

## Declare resources

A resource is a read without input, read on a host's agent. `core.DefineResource` makes `<module>.<resource>` (two lower-case words, the first the module id) at schema version 1:

```text
core.DefineResource(ID+".summary", m.summary)
```

The function you pass takes the context and the `core.Env` and returns the output value. The CLI command of a resource is `darkpanel <module> <resource> get`, so `hostinfo.summary` is `darkpanel hostinfo summary get`. A resource has no plan, no change and no audit entry for a read; if the data needs input, declare a read action instead.

## Preflight, owned paths and migrations

**Preflight.** Each `core.PreflightCheck` has an id and a `Run` function that returns an error when the host cannot run the module. A failed check marks the module unavailable on that host and the error text is the reason the operator sees; the rest of the system keeps running. `hostinfo` checks that `/proc/meminfo` is readable.

**Owned paths.** Everything a module creates on a host is declared as a `core.OwnedPath`, so that uninstall removes it ([ADR 0009](../adr/0009-host-execution-safety.md) states the rule). The `Kind` is one of these:

| Kind | Declares | Extra fields |
|---|---|---|
| `core.OwnedFile` | A file | |
| `core.OwnedBlock` | Marked blocks in a shared file | `BlockIDs`, optional `CommentPrefix` (default `#`) |
| `core.OwnedDir` | A directory | |
| `core.OwnedUser` | A system user; `Path` is the user name | |
| `core.OwnedUnit` | A systemd unit file | A module other than the core must use `/etc/systemd/system/darkpanel-<name>.service` |
| `core.OwnedRestore` | A regular file a snapshot may rewrite or remove | `Path` is absolute and clean |
| `core.OwnedManagedDir` | A root-owned directory of flat module files | `Path` is absolute and clean; optional `Pattern` for file names |
| `core.OwnedSysctl` | A kernel parameter; `Path` is the key relative to `/proc/sys` | `Pattern` is required; a key has at most one `<iface>` segment |
| `core.OwnedServiceDir` | A directory owned by the module's service user | `User` names an owned user of the module; optional `Subdirs` and `Pattern` |

The registry refuses `Pattern` on a kind other than sysctl, managed-dir and service-dir, `User` and `Subdirs` on a kind other than service-dir, a `Pattern` that does not compile, and a path or block that two modules both declare. Two modules can share a file when they declare marked blocks with different ids.

**Docker objects.** Containers, networks and volumes a module creates carry the label `darkpanel.module=<id>`; the key is `core.LabelModule`.

**Uninstall.** A module that must do more than remove its paths implements `core.Uninstaller`: `UninstallOptions` lists the options an operator can set (`--module-option`), and `Uninstall` runs before uninstall removes the module's blocks and files. It can run again after a partial uninstall, so it must succeed on a host where it already ran.

**Migrations.** By convention, a module with tables keeps its SQL migrations in `internal/modules/<id>/migrations/`, in one directory for each side that has tables: `agent` and `controller`. The registry checks only that the keys of `Migrations` are `core.SideController` and `core.SideAgent`; the directory layout is not checked. The migration runner refuses a file whose name does not match `NNNN_<name>.sql` (four digits, then lower-case letters, digits and underscores) when it applies the module's migrations, and numbers are unique and start at 1. Embed each directory with `//go:embed` and return it from `Manifest()` as an `fs.FS` for its side in `Migrations`. `internal/modules/derp/store.go` shows both sides and `internal/modules/derp/derp.go` shows the manifest entry. A migration that fails, or whose file changed after it was applied, marks the module unavailable. Add a new file for each change to the schema and never edit one that has shipped.

## Register the module

Adding a module is one line in `modules.All` in `internal/modules/modules.go`: add the call that returns your module to the list. `registry.Build` in `internal/registry/registry.go` builds the one registry that the controller, the agent, the CLI and the schema generator share: the core module first, then `modules.All()`. `core.NewRegistry` validates every rule listed in the last section when the process starts, and returns all violations at once. A module that breaks a rule stops the build of the registry, so the process does not start, and `go test ./internal/modules/` fails with the same errors.

## Get the CLI for free

The CLI is generated from the registry. `cli.CommandPath` turns the action name `<module>.<noun>.<verb>` into the command `darkpanel <module> <noun> <verb>`, and a resource into `darkpanel <module> <resource> get`. The core module drops its own segment, so `core.user.create` is `darkpanel user create`.

- Flags are generated from the input schema, in kebab case: a field `expiresInDays` is the flag `--expires-in-days`. Scalars and arrays of scalars get a flag; a nested object comes from `--input`.
- The CLI defines its own flags, and no input field may map to one of them: `url`, `socket`, `dev`, `help`, `json`, `host`, `local`, `input`, `yes`, `confirmation`, `confirm` and `override`. Generation fails when a field would. Two fields that map to the same flag also fail.
- A secret field has no flag. The CLI prompts for it on a terminal, or reads one line per secret from standard input, and a secret field must be a string.
- An action whose highest version is `Internal` has no command.

`TestEveryActionHasACLICommand` in `internal/modules/registry_test.go` proves that every action that is not internal, and every resource, has a command in the CLI built from the real registry.

Most modules need no more. A module that needs a command the generator cannot produce writes it by hand in `internal/cli/<module>.go` and adds an entry to the `moduleCommands` map in `internal/cli/modules.go`. A hand-written command may not take a name or alias that a generated command of the same group already has; that fails when the CLI is built.

## Add the web pages

The web interface keeps one folder for each module, `web/src/modules/<id>/`, and one entry in `moduleRegistry` in `web/src/modules/registry.ts`. The smallest example is `web/src/modules/hostinfo/`: a `routes.tsx` that default-exports the module's pages and a summary page component. The interfaces are in `web/src/modules/types.ts`.

A `ModuleEntry` has these fields:

| Field | Meaning |
|---|---|
| `id` | The module id. |
| `label` | The name shown in the interface. |
| `icon` | A `lucide-react` icon. |
| `routes` | A lazily loaded component for the host pages, mounted at `/hosts/:hostId/<id>/`. Leave it out for a module that has only a global page. |
| `global` | A `GlobalPage` for a page that belongs to no host. |
| `containerActions` | Buttons on the docker module's container page. |
| `hostCreate` | Extra inputs for the "Add host" dialog. |

The supporting interfaces carry this:

- `ModuleRouteProps` is what the host pages receive: `hostId`.
- `ContainerRef` describes one container: `hostId`, `containerId`, `name`, an optional `composeService` and its `ports`.
- `ContainerAction` is one button: an `id` such as `firewall.allow`, a `label` and a lazily loaded `render` component that receives the `container` and an `onDone` callback.
- `HostCreateExtension` has an `id` and a `render` component that receives the current `value` and an `onChange` callback; the value is merged into the input of `core.host.create`.
- `GlobalPage` has a `path` (mounted at `/<path>/*`), a `label` and the lazily loaded `routes`.

The registry shows a module's tab on a host only when the host has the module enabled, and shows the reason when the module is unavailable.

Every action that changes a host goes through the change dialog, `web/src/components/change-dialog.tsx`. The dialog plans the action, shows the diff and the guards, applies, and handles the confirm and revert deadline. Pass it the action name and version as the `target` and the input as `input`; `web/src/modules/panel/panel-change.tsx` is a short example. Do not call the apply route directly from a page.

Run `make lint-web test-web` before you commit. It runs ESLint, the TypeScript type check and the Vitest suite.

## Schemas and the API reference

Every action and resource has a committed schema snapshot under `schemas/`. After you add an action, or change the input or output type of one, run:

```sh
make schemas
```

and commit the result. `TestSnapshotsCurrent` in `internal/modules/modules_test.go` fails while a snapshot is missing, out of date, or belongs to nothing in the registry. The "Schemas" section of [CONTRIBUTING.md](../../CONTRIBUTING.md) says the same.

Once a version has shipped, its schema may only grow. The compatibility rules of [ADR 0002](../adr/0002-module-contract.md) allow these changes to a published version: new optional input fields, new output fields, and new output enum values. These need a new action version, with the old one kept until the next major release: removing or renaming a field, changing a type, a default or a constraint, adding a required input field, and removing an enum value. A test compares the snapshots against those of every released tag.

The API reference under `docs/api/` is generated from the same registry. After the snapshots, run:

```sh
make api-docs
```

and commit that too. `TestAdAPIDocsCurrent` in `internal/apidocs/apidocs_test.go` fails while a file is stale, missing or not part of the generated set. The reference starts at [the API index](../api/README.md).

## Test the module

Unit tests run with fakes and never against the machine they run on:

- Use `coretest.NewFakeClock` from `internal/core/coretest` for time, and the other fakes of that package (the agent, the dispatcher, the sealer, the audit log and the input validator) where a handler needs them.
- Fake the programs a module runs with a `core.Runner` of your own that records the `core.Cmd` it receives and returns scripted results. `tsHost` in `internal/modules/tailscale/testhost_test.go` is an example.
- Read files through an `fs.FS` that a test fills with fixtures, as `hostinfo` does.

Behaviour that needs a real host (systemd, Docker, the firewall) runs in the VM harness in `test/harness`; [ADR 0008](../adr/0008-test-harness.md) describes it.

The make targets that check a change:

```sh
make test-go
make lint-go
make test-web
make lint-web
make check
```

`make test-go` runs `go test ./...`, `make lint-go` runs `golangci-lint` and `go vet`, `make test-web` and `make lint-web` cover the web interface, and `make check` runs everything CI runs: lint, tests, the build, the static-binary check and the vulnerability scan. [CONTRIBUTING.md](../../CONTRIBUTING.md) lists every target. Action and resource names and the commands generated from them follow [ADR 0011](../adr/0011-actors-cli-names-and-layout.md) (`docs/adr/0011-actors-cli-names-and-layout.md`).

## Rules the registry enforces

`core.NewRegistry` refuses a module that breaks any of these rules, and reports every violation in one error. They are written in `internal/core/registry.go`; `TestRegistrationRules` in `internal/modules/registry_test.go` checks the naming rules again over the real registry.

Modules and names:

- A module id matches `^[a-z][a-z0-9]*$`, is not in `core.ReservedNames` and is registered once. The module version is at least 1.
- An action name matches `^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$` and starts with the module id and a dot. A resource name matches `^[a-z][a-z0-9]*\.[a-z][a-z0-9]*$`, starts with the module id and a dot, and has a `Read` function.
- An action's name and version pair is unique, the version is at least 1 and the side is controller or agent. A resource name is unique.
- No two actions, resources or streams map to the same CLI command, and none is the command group of another. The core's nouns and resources must not equal a module id.

Classes and lifecycle:

- A compensating action named by `CompensatedBy` is registered, and only a compensable action names one.
- A compensable action declares exactly one of an undo handler, `CompensatedBy` and `ForwardOnly`. A `ForwardOnly` action declares `Converges`. An undo handler needs a compensable action with at least one lock key.
- A break-glass action is controller-side.
- A revertible action is agent-side, takes at least one lock key and names `RepairedBy`; no other class names one. The repair action is registered, belongs to the same module and side, is compensable, irreversible or revertible, is not a task action, has no recovery handler, and takes only lock keys the revertible action holds.
- A composite is controller-side, declares `Unwind` as reverse or forward, has `Steps` and `Output` handlers, holds exactly the lock key `composite:<module>`, and has no secret field in its input. No other class declares a `composite:*` lock key.
- A read action takes no locks.
- An `ApplyTimeout` above 45 seconds requires `LongRunning`.
- A task action is compensable or irreversible, agent-side, takes lock keys, has an `ApplyTimeout`, and has a `Task` handler and no `Apply`.
- Each class has exactly its own handlers: a read action only `Read`, a compensable or irreversible action `Plan`, `Apply` (or `Task`) and `Verify`, a revertible action those plus `Snapshot` and `Revert`, a composite only `Steps` and `Output`.

Guards, scope and reveal:

- An action with `Guards` has an input type that embeds `core.Guarded`, and an input type that embeds it belongs to an action with `Guards`. Guard ids are non-empty and unique.
- `MinScope` is read, operate or admin and is not below the scope the class and side need.
- `Internal` and `Reveal` are for read actions, and an action is never both. Each has a reveal handler and no read handler, and a reveal action also declares `MinScope` admin. No other action has a reveal handler.

Owned paths, units and migrations:

- Owned paths follow the kinds of the table in the owned-paths section: a known kind, a unit path of the form `/etc/systemd/system/darkpanel-<name>.service` for any module but the core, absolute and clean paths for `restore`, `managed-dir` and `service-dir` (never `/`), a valid sysctl key with a `Pattern`, a `Pattern` only on `sysctl`, `managed-dir` and `service-dir`, `User` and `Subdirs` only on `service-dir`, and subdirectories that match `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$` without repeats.
- No other module declares the same owned path, user, or marked block id.
- A module's external units match `^[A-Za-z0-9@._-]+\.service$`, are listed once, and list at least one operation out of `state`, `start`, `stop` and `restart`, each once.
- `Migrations` has only the keys `core.SideController` and `core.SideAgent`.

Streams and background tasks:

- A stream name matches `^[a-z][a-z0-9-]*$`, is unique in its module, and has `Run`, `ParamsType` and `EventType`.
- A background task id matches `^[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`, starts with the module id and a dot, is unique, has a `Run` function and a non-negative `Every`.

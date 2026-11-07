# Docker

The `docker` module lists and controls the Docker Engine on a host: its
containers, images, networks and volumes, and the Compose projects that run
on it. It talks to the Engine through its socket and never runs the `docker`
program, except to read and run a Compose project. It runs in the agent on each
host. Its page in the web interface is `/hosts/<id>/docker`, with tabs for
containers, images, networks, volumes and projects, and a page for each
container with its logs, statistics, details and firewall exposure.

## Requirements

The host needs Docker Engine 25 or later: the socket `/var/run/docker.sock`
must exist, and the Engine API it reports must be `1.44` or later. Without
that, the module reports itself unavailable, and its actions fail with
`docker.engine_unavailable` and the reason, for example that the socket is
missing, that the daemon does not answer, or that the API is older than `1.44`.

Compose actions also need the Docker Compose plugin, installed where Docker
puts system plugins, in `/usr/libexec/docker/cli-plugins/docker-compose`.
"Compose" below lists everything they check.

Commands for a host take `--host <host>`; you can leave it out when you have
one host. The agent-side commands also take `--local`, which sends them to the
agent's socket on the host you are on, as root. `docker.container.reveal-env`
is the exception: it runs only through the controller.

## Actions and resources

Changing actions are of two kinds. A compensable action can be undone with
`darkpanel change undo <id>`, and the table says what the undo does. An
irreversible action cannot be undone, and its plan asks you to type a
confirmation phrase, which the table names; `--confirmation <phrase>` gives it
on the command line. For both, `--yes` skips the `Apply? [y/N]` question. None
of the Docker actions has a revert timer.

| Name | Command | Kind | Minimum scope | What it does |
|---|---|---|---|---|
| `docker.engine` | `darkpanel docker engine get` | Read | `read` | Shows whether the Engine answers, its version and API version, and the counts of containers and images. |
| `docker.containers` | `darkpanel docker containers get` | Read | `read` | Lists containers with their state, published ports and usage. |
| `docker.images` | `darkpanel docker images get` | Read | `read` | Lists images with their tags, size and the containers that use them. |
| `docker.networks` | `darkpanel docker networks get` | Read | `read` | Lists networks with their subnets and containers. |
| `docker.volumes` | `darkpanel docker volumes get` | Read | `read` | Lists volumes with the containers that use them. |
| `docker.projects` | `darkpanel docker projects get` | Read | `read` | Lists the Compose projects Darkpanel has seen. |
| `docker.container.inspect` | `darkpanel docker container inspect` | Read | `read` | Shows a container's configuration and state, with environment values hidden. |
| `docker.container.logs` | `darkpanel docker container logs` | Read | `read` | Reads the last lines of a container's logs. |
| `docker.container.reveal-env` | `darkpanel docker container reveal-env` | Read | `admin` | Returns a container's environment values once. |
| `docker.container.start` | `darkpanel docker container start` | Undone by an inverse | `operate` | Starts a container. Undo stops it, only if it was stopped before. |
| `docker.container.stop` | `darkpanel docker container stop` | Undone by an inverse | `operate` | Stops a container. Undo starts it, only if it was running before. |
| `docker.container.restart` | `darkpanel docker container restart` | Undone by an inverse | `operate` | Restarts a container. Undo starts it if it is stopped; the restart itself is not reversed. |
| `docker.container.remove` | `darkpanel docker container remove` | Irreversible | `operate` | Removes a container. You type its name. |
| `docker.image.pull` | `darkpanel docker image pull` | Undone by an inverse | `operate` | Pulls an image from its registry. Undo does nothing: a pulled or re-pointed tag stays. |
| `docker.image.remove` | `darkpanel docker image remove` | Irreversible | `operate` | Removes an image or one of its tags. You type the image reference. |
| `docker.image.prune` | `darkpanel docker image prune` | Irreversible | `operate` | Removes the dangling images no container uses. You type `prune`. |
| `docker.network.create` | `darkpanel docker network create` | Undone by an inverse | `operate` | Creates a network. Undo removes it, if no container uses it. |
| `docker.network.connect` | `darkpanel docker network connect` | Undone by an inverse | `operate` | Connects a container to a network. Undo disconnects it, only if this change connected it. |
| `docker.network.disconnect` | `darkpanel docker network disconnect` | Undone by an inverse | `operate` | Disconnects a container from a network. Undo reconnects it with its old endpoint settings, only if this change disconnected it. |
| `docker.network.remove` | `darkpanel docker network remove` | Irreversible | `operate` | Removes a network. You type its name. |
| `docker.volume.create` | `darkpanel docker volume create` | Undone by an inverse | `operate` | Creates a volume. Undo removes it, if no container uses it. |
| `docker.volume.remove` | `darkpanel docker volume remove` | Irreversible | `operate` | Removes a volume and the data in it. You type its name. |
| `docker.compose.up` | `darkpanel docker compose up` | Irreversible | `admin` | Creates or recreates the containers of a project. You type the project name. |
| `docker.compose.down` | `darkpanel docker compose down` | Irreversible | `admin` | Stops and removes the containers of a project. You type the project name. |
| `docker.compose.pull` | `darkpanel docker compose pull` | Undone by an inverse | `admin` | Pulls the images of a project. Undo does nothing. |
| `docker.compose.restart` | `darkpanel docker compose restart` | Undone by an inverse | `admin` | Restarts the containers of a project. Undo starts the ones that were running before and are stopped now. |

An undo reads the record of what the change did and acts only on effects that
happened. If someone changed the object since, for example by starting a
container you stopped, the undo does not reverse that later change and reports
a conflict instead.

## Containers, images, networks and volumes

The tabs of the web page show the resources. The containers tab starts, stops,
restarts and removes a container. Opening a container shows:

- its details, from `docker.container.inspect`: state, image, command, working
  directory, user, restart policy, environment names, labels, published ports,
  mounts and networks;
- its logs, with a follow option. `docker.container.logs` reads the last
  lines: `tail` from 1 to 5000, 200 by default, `since` as an RFC 3339 time, and
  `timestamps`. Following uses the stream command
  `darkpanel docker logs --container <name> --follow`, with the same `since` and
  `tail`;
- its statistics, CPU and memory, from the stream
  `darkpanel docker stats --container <name> --follow`;
- its firewall exposure, which "Exposure in container views" describes.

`docker.container.remove` takes `force` to remove a running container and
`volumes` to remove its anonymous volumes. The pull of an image can take long.
Its progress is the stream `darkpanel docker pull --change-id <id> --follow`.

`docker.image.prune` lists in its plan exactly the images it will remove and
the bytes it will reclaim, and the apply removes exactly that list. Only
dangling images are removed.

## Secrets

`docker.container.inspect` shows each environment variable's name and replaces
its value with `[redacted]`, for every container. It also replaces every
element of the command and every argument of the entrypoint after its first
element, and the value of every label other than the ones Compose and
Darkpanel set, with `[redacted]`.

`docker.container.reveal-env` returns the values. It needs the `admin` scope,
delivers them once, as secrets are delivered, and records who asked in the
audit log. It refuses containers that belong to a Darkpanel module, because
those hold credentials the module manages. It runs only through the controller,
so `--local` does not work with it.

The logs of containers that belong to a Darkpanel module are filtered: values
that the module registered as secrets are removed from the lines.

## Guards

Two refusals protect objects from a careless change:

- `docker.in_use`: removing a network or a volume that a container uses fails
  at the plan and names the containers.
- `docker.owned`: a change to an object that carries the label
  `darkpanel.module` is refused. Another Darkpanel module, such as the
  firewall, the reverse proxy or the tunnel connector, created it and manages
  it. Use that module's pages to change it. The firewall never touches such
  objects either.

## Compose

A Compose project is a directory with Compose files that run several
containers together. The module runs four Compose commands on a project:

- `docker.compose.up` creates or recreates the containers.
- `docker.compose.down` stops and removes them.
- `docker.compose.pull` pulls the images.
- `docker.compose.restart` restarts the containers.

All four need the `admin` scope and are long-running: the change is accepted at
once and you follow it as it progresses. Compose files are your own, and
Darkpanel never writes them. Running one runs it as root, and the plan says so:
"runs the project's Compose file as root". `up` and `down` ask you to type the
project name.

Every Compose action takes `project`, the project's name, and an optional list
of Compose profiles, `profiles`. When you give the list, it replaces the
project's stored one.

### Projects

Darkpanel finds projects from the labels Compose puts on containers, such as
`com.docker.compose.project`, and from the directory, files and environment
files those labels name. It records each one the first time it sees it, so a
project stays in the list after `down` has removed its containers. A project
that Darkpanel has never seen cannot be run: start it once with Compose
yourself so that discovery finds it.

Compose profiles are not stored in the labels. Each project has a `profile`
list of its own that you set, empty by default. Every run passes it as
`--profile`, and the plan shows it. The projects tab has a Profiles field. The
list `projects` also shows the config hash, and whether the Compose files
changed since the last run, which the plan shows too.

### What a project must satisfy

Compose runs as root and reads files that someone can edit, so the module
checks before it plans and again before it applies. It refuses with
`docker.compose_unavailable` and the reason when:

- the project's directory or any of its files is not an absolute, clean path,
  is missing, is not a regular file or directory, or has a symlink in its path;
- any component of the path is writable by its group or others, other than a
  root-owned sticky directory such as `/tmp`;
- a file or an ancestor is owned by anyone other than root and the owner of the
  project directory;
- the Docker configuration directory
  `/var/lib/darkpanel/agent/docker-config`, when it exists, is a symlink, is
  not a directory or is not owned by root; or its `config.json` sets
  `credsStore`, `credHelpers` or `cliPluginsExtraDirs`; or it holds a
  `cli-plugins` entry;
- the Compose plugin `/usr/libexec/docker/cli-plugins/docker-compose` or
  its directory is a symlink, is not owned by root, or is writable by its
  group or others; or a `docker-compose` exists in
  `/usr/local/lib/docker/cli-plugins`,
  `/usr/local/libexec/docker/cli-plugins` or `/usr/lib/docker/cli-plugins`,
  which the Docker CLI would run first; or one of those three directories
  exists and is not owned by root or is writable by its group or others;
- `docker compose config` fails, which includes a remote `include` or `extends`
  from Git or OCI, because Darkpanel turns both remote loaders off;
- a service has a `provider` key, which would make Compose run a program on the
  host;
- the resolved configuration has a key that Compose's documented schema does
  not list, at the top level, in a service, or in a network, volume, config
  or secret. Keys that start with `x-` are accepted. The `models` key, at
  the top level and in a service, is refused too although the schema lists
  it, because it hands work to a program on the host;
- a service has a build section and its image is not present locally. `up`
  always runs with `--no-build`, so Compose never starts a build.

It refuses with `docker.owned` when the project is named `darkpanel` or starts
with it; when a service, network or volume in its configuration sets the
`darkpanel.module` label itself; when its configuration names a container,
network or volume that carries that label; or when one of its current
containers carries it.

Pulling from a private registry through Compose is not supported, because the
Compose run has no stored credentials.

## Exposure in container views

The container page shows the rows of the firewall module's `firewall.exposure`
for the container's published ports, so you see who can reach each one. Other
modules can add buttons to the page, and the firewall adds Allow and Deny.
Those plan `firewall.container.allow` and `firewall.container.deny` directly
for the container, with the firewall's own diff, guards and revert countdown.
They are disabled with the reason when the firewall module or its Docker
integration is not available. The firewall page describes the integration.

## Troubleshooting

| Code | Cause | What to do |
|---|---|---|
| `docker.engine_unavailable` | The Docker socket is missing, the daemon does not answer, or its API is older than `1.44`. | Install or start Docker Engine 25 or later. The message says which of the three it is. |
| `docker.in_use` | The network or volume you are removing is used by a container. | Remove or disconnect the containers the message names first. |
| `docker.owned` | The object or project belongs to a Darkpanel module, or a Compose project is named `darkpanel*`. | Change it through the module that owns it. Rename a project of your own that starts with `darkpanel`. |
| `docker.compose_unavailable` | Compose cannot run the project, or the module refuses it. The message gives the reason. | Fix the reason in "What a project must satisfy" and plan again. |

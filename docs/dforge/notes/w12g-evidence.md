# w12g evidence

Evidence for the assumptions of docs/dforge/plans/2026-11-15-v1-w12g-security-followup.md (Task 1), gathered on 2026-11-15 on the development host over HTTPS with `curl`, without Docker.

## NPM 2.16.0 image

Read from the registry by the method of `docs/dforge/notes/w12e-evidence.md` `## NPM 2.16.0 image`: index `sha256:4393e642e233e5efd5adeb7f10918d4aaea71c9ed875b94190273fc8e87f9594` (its digest checked with `sha256sum`), `linux/amd64` manifest `sha256:57d85bffc776c01909c7b24e127a69a72b7d8c75d387e6cb26c762978fc43e4c`, config `sha256:5c80aaf5e26c1f45a6ce9f58ad00edceabb717d68236ff5b06d6f4d1f1271742`, 32 layers. The config's environment holds `NPM_BUILD_VERSION=2.16.0` and `OPENRESTY_VERSION=1.31.1.1`, no `PUID` or `PGID`, and no `User` (`.config.User` is null); the entrypoint is `/init` (s6-overlay). Paths below are inside the image. The nginx binary was read with `strings`, never run.

### 1. The user of `nginx -t`, `nginx -s reload` and nginx itself

The backend tests and reloads nginx by running the binary directly (`/app/internal/nginx.js:108-121`):

```js
	test: () => {
		debug(logger, "Testing Nginx configuration");
		return utils.execFile("/usr/sbin/nginx", ["-t", "-g", "error_log off;"]);
	},
	...
	reload: () => {
		return internalNginx.test().then(() => {
			logger.info("Reloading Nginx");
			return utils.execFile("/usr/sbin/nginx", ["-s", "reload"]);
		});
	},
```

`execFile` is node's `child_process.execFile` (`/app/lib/utils.js:1`, `import { exec as nodeExec, execFile as nodeExecFile } from "node:child_process";`), so both commands run as the backend's user. The backend and nginx are started as the same user (`/etc/s6-overlay/s6-rc.d/backend/run:18` and `/etc/s6-overlay/s6-rc.d/nginx/run:9`):

```sh
		s6-setuidgid "$PUID:$PGID" bash -c "export HOME=$NPMHOME;export CERTBOT_VERSION=$CERTBOT_VERSION;node --abort_on_uncaught_exception --max_old_space_size=250 index.js"
```
```sh
exec s6-setuidgid "$PUID:$PGID" nginx
```

The defaults are in the scripts, not in the image's environment (`/usr/bin/common.sh:12-13,28-36`, sourced by both `run` files):

```sh
PUID=${PUID:-0}
PGID=${PGID:-0}
...
if [[ "$PUID" -ne '0' ]] && [ "$PGID" = '0' ]; then
	# set group id to same as user id,
	# the user probably forgot to specify the group id and
	# it would be rediculous to intentionally use the root group
	# for a non-root user
	PGID=$PUID
fi

export PUID PGID
```

So nginx's master runs as `PUID:PGID`, root (`0:0`) when neither is set. When `PUID` is set to a non-zero value and `PGID` is unset or `0`, the image uses `PGID=PUID`. The plan's Contracts (Reloader) say only "both numeric" and "`0:0` when neither is set"; the image's rule for a `PUID` without a `PGID` is the one above, which Task 3 has to follow to run as the same group as nginx.

The workers run as `npm` (`/etc/nginx/nginx.conf:3-4`, `pid /run/nginx/nginx.pid;` and `user npm;`), whose uid and gid are set to `PUID` and `PGID` at start (`/etc/s6-overlay/s6-rc.d/prepare/10-usergroup.sh:10,13,25`, `usermod -u "$PUID" "$NPMUSER"`, `useradd -o -u "$PUID" -U ...`, `groupmod -o -g "$PGID" "$NPMGROUP"`). A non-root master ignores `user` (the binary's message, `the "user" directive makes sense only if the master process runs with super-user privileges, ignored`), so with a non-root `PUID` the workers run as `PUID` too.

Gate D2, the user: an exec as `PUID:PGID` (root when unset) is the master's own user, and `nginx -s reload` sends its signal to the pid in `/run/nginx/nginx.pid`, which the master wrote as that user. It passes.

### 2. What `nginx -t` opens and who owns it

The configuration names (`/etc/nginx/nginx.conf:3,12,31`, `/etc/nginx/conf.d/default.conf:11-12`, `/etc/nginx/conf.d/include/log-proxy.conf:4`, `/app/templates/proxy_host.conf:25-26`):

```
pid /run/nginx/nginx.pid;
error_log /data/logs/fallback_error.log warn;
	client_body_temp_path         /tmp/nginx/body 1 2;
	access_log /data/logs/fallback_http_access.log standard;
	error_log /data/logs/fallback_http_error.log warn;
access_log /data/logs/fallback_http_access.log proxy;
  access_log /data/logs/proxy-host-{{ id }}_access.log proxy;
  error_log /data/logs/proxy-host-{{ id }}_error.log warn;
```

`proxy_temp_path` is not set (no match under `/etc/nginx` or `/app/templates` outside the bundled documentation), so the build's default applies: the binary was configured with `--http-proxy-temp-path=/var/cache/nginx/proxy_temp`, `--http-client-body-temp-path=/var/cache/nginx/client_temp`, `--error-log-path=/var/log/nginx/error.log`, `--pid-path=/var/run/nginx.pid` and `--prefix=/etc/nginx/nginx` (`strings /usr/sbin/nginx`). The cache paths are `/var/lib/nginx/cache/public` and `/var/lib/nginx/cache/private` (`/etc/nginx/nginx.conf:46-47`).

The directories are created at start (`/etc/s6-overlay/s6-rc.d/prepare/20-paths.sh:18-40`, `/data/logs`, `/run/nginx`, `/tmp/nginx/body`, `/var/log/nginx`, `/var/lib/nginx/cache/*`, `/var/cache/nginx/proxy_temp`; `chmod 777 /var/log/nginx/error.log`; `chmod -R 777 /var/cache/nginx`) and handed to `PUID:PGID`, recursively, before nginx starts (`/etc/s6-overlay/s6-rc.d/prepare/30-ownership.sh:9-47`):

```sh
chown root /tmp/nginx

locations=(
	"/data"
	"/etc/letsencrypt"
	"/run/nginx"
	"/tmp/nginx"
	"/var/cache/nginx"
	"/var/lib/logrotate"
	"/var/lib/nginx"
	"/var/log/nginx"
	"/etc/nginx/nginx"
	"/etc/nginx/nginx.conf"
	"/etc/nginx/conf.d"
)
...
	if [ "$have" != "$PUID:$PGID" ]; then
		if [ "$recursive" = 'true' ] && [ -d "$dir" ]; then
			chown -R "$PUID:$PGID" "$dir"
```

`nginx -t` opens the log files it names, creating a missing one as the user it runs as, and creates missing temp directories (nginx's start-up code, which test mode runs too; inference from nginx's behaviour, not quoted from the image). Run as `PUID:PGID`, it creates them as the same user NPM's own `nginx -t` does, so the exec leaves no file with an owner NPM would not have given it. Live ownership is the harness step of Task 9.

### 3. Process titles and version

The binary is `nginx version: openresty/1.31.1.1` (`strings /usr/sbin/nginx`, also the `OPENRESTY_VERSION=1.31.1.1` of the config). Its strings hold the title prefix `nginx: ` and the titles `master process`, `worker process`, `cache manager process` and `cache loader process`, and the old workers' `worker process is shutting down`. nginx's `ngx_setproctitle` writes the prefix and the title over `argv[0]`, so the titles are `nginx: master process nginx` (the master was started as `nginx`) and `nginx: worker process` (nginx source behaviour; the strings are quoted from the binary). An old worker during a graceful shutdown is `nginx: worker process is shutting down`: it also starts with `nginx: worker process`, but its pid was in the listing taken before the signal, which is what the reloader compares.

Gate D2, the titles: they are the Contracts' `nginx: worker process`. It passes.

### 4. `nginx` on the exec's `PATH`

`/usr/sbin/nginx` is a regular file of layer 10 (`-rwxr-xr-x 0/0 15770048 usr/sbin/nginx`), and `/etc/nginx/bin/openresty -> /usr/sbin/nginx`; no later layer whites it out. The config's `PATH` is `/opt/certbot/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`, which an exec inherits, so `nginx` resolves to `/usr/sbin/nginx`.

## Engine API

From the Engine API v1.49 OpenAPI document (`https://docs.docker.com/reference/api/engine/version/v1.49.yaml`, fetched 2026-11-15) and, where it is silent, moby's source at `v28.3.3` (`https://raw.githubusercontent.com/moby/moby/v28.3.3/<path>`).

**ExecCreate** (`POST /containers/{id}/exec`, operationId `ContainerExec`): body `ExecConfig` with `AttachStdin`, `AttachStdout`, `AttachStderr` (booleans), `Tty`, `Env`, `Cmd` ("Command to run, as a string or array of strings."), `Privileged`, `User` ("The user, and optionally, group to run the exec process inside the container. Format is one of: `user`, `user:group`, `uid`, or `uid:gid`."), `WorkingDir`. Answers `201` with `IDResponse` (`Id`), `404` "no such container", `409` "container is paused", `500`.

**ExecStart** (`POST /exec/{id}/start`): "If detach is true, this endpoint returns immediately after starting the command." Body `ExecStartConfig` with `Detach` ("Detach from the command.") and `Tty` ("Allocate a pseudo-TTY."), and `ConsoleSize`. Answers `200`, `404` "No such exec instance", `409` "Container is stopped or paused".

**ExecInspect** (`GET /exec/{id}/json`): answer `ExecInspectResponse` with `CanRemove`, `DetachKeys`, `ID`, `Running` (boolean), `ExitCode` (integer), `ProcessConfig`, `OpenStdin`, `OpenStderr`, `OpenStdout`, `ContainerID`, `Pid`; `404` "No such exec instance". The document does not say what `ExitCode` holds while the command runs. The daemon answers with `backend.ExecInspect`, whose field is a pointer (`api/types/backend/backend.go:115`, `ExitCode      *int`), copied from the exec's config (`daemon/inspect.go:220`, `ExitCode:      e.ExitCode,`), which is set only when the process exits (`container/exec.go:24,72-74`, `ExitCode     *int` and `func (c *ExecConfig) SetExitCode(code int) { c.ExitCode = &code }`). So the answer holds `"ExitCode": null` until the command has exited. Moby's own client type decodes it into a plain `int` (`api/types/container/exec.go:51`), which is why the Contracts' `ExecInspect` uses `*int`.

**ContainerTop** (`GET /containers/{id}/top`): "On Unix systems, this is done by running the `ps` command." Query `ps_args`, "The arguments to pass to `ps`. For example, `aux`", default `-ef`. Answer `ContainerTopResponse`: `Titles` ("The ps column titles", array of strings) and `Processes` ("Each process running in the container, where each process is an array of values corresponding to the titles."); `404` "no such container", `500`. In the daemon (`daemon/top_unix.go`), `ps` runs on the host with the arguments split at spaces and `-q<pids of the container>` appended, retried without `-q` if `ps` refuses it (`:180-188`); the output's first line becomes `Titles`, a column titled `PID` is required (`:74-83`, `Couldn't find PID field in ps output`), only rows whose pid belongs to the container are kept (`:110-114`), and the fields beyond the last title are joined with spaces into the last column (`:51-56`). With `-eo pid,ppid,args` the titles are `PID`, `PPID` and `COMMAND` (procps' header for `args`; inference, the host's `ps` is not in the image) and the last column holds the whole process title, spaces included. Pids are the host's, the same in every listing, which is all the reloader compares. A stopped or restarting container is refused (`:150-165`).

**nginx's reload** (`https://nginx.org/en/docs/control.html`, "Changing Configuration"): "In order for nginx to re-read the configuration file, a HUP signal should be sent to the master process. The master process first checks the syntax validity, then tries to apply new configuration, that is, to open log files and new listen sockets. If this fails, it rolls back changes and continues to work with old configuration. If this succeeds, it starts new worker processes, and sends messages to old worker processes requesting them to shut down gracefully. Old worker processes close listen sockets and continue to service old clients. After all clients are serviced, old worker processes are shut down." So a new worker pid is the sign that the new configuration was applied, and none appears when it was rolled back.

**A static address on connect** (`https://docs.docker.com/reference/cli/docker/network/connect/`): the options table gives `--ip` as "IPv4 address (e.g., 172.30.100.104)" and `--ip6` as "IPv6 address (e.g., 2001:db8::33)", and the section "Specify the IP address a container will use on a given network (--ip)" says only "You can specify the IP address you want to be assigned to the container's interface." The page's `--ip` examples use networks created with `docker network create --subnet 172.20.0.0/16 --ip-range 172.20.240.0/20 multi-host-network`, and it says "If specified, the container's IP address(es) is reapplied when a stopped container is restarted. If the IP address is no longer available, the container fails to start." It does not state the subnet requirement itself; the daemon checks a requested address against the network's configured IPAM subnets (`daemon/container_operations.go:550,565-578`, `ipamConfig.IsInRange(nwIPv4Subnets, nwIPv6Subnets)`). Task 9's `m6n-rec` is created with `--subnet`, which meets it either way.

## What the harness step of Task 9 must prove live

The exec's user can run `nginx -t` and signal the master. This note shows that the exec runs as the master's own `PUID:PGID` and that NPM's backend runs both commands as that user, but only a running container shows that the Engine's exec applies the user and that the signal reaches the master.

No file under nginx's log or temp directories changes owner. The reading shows that `nginx -t` as `PUID:PGID` creates files as the user NPM itself uses, but nginx's start-up file handling is inferred from its behaviour, not observed; the step compares owners under `/data/logs`, `/var/cache/nginx`, `/tmp/nginx` and `/run/nginx` before and after a reload.

`ContainerTop` lists the workers on all four systems. The daemon runs the host's `ps`, which this image does not contain, so the `COMMAND` title and the `-q` handling depend on each system's procps; the step checks that a new `nginx: worker process` pid appears after the reload on debian-12, debian-13, ubuntu-22.04 and ubuntu-24.04.

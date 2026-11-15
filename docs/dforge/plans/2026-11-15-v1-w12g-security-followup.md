# v1 w12g security follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the residuals of the 2026-11-15 panel re-review (security register rows 1, 176, 188, 190-199): nginx is reloaded by a confirmed `docker exec` instead of a proxy-host update, the attach and the undo of a detach disconnect NPM before any lookup or listing can stretch the exposure, NPM's addresses are verified at agent start and on a timer, the upgrade refuses when nginx could not start, and the smaller defects, the Tailscale link guard, the harness and the threat model follow.

**Architecture:** Task 1 reads the pinned image and the Engine API for what the exec reload assumes. Tasks 2-3 add the Engine's exec and process-listing calls and the reloader built on them. Task 4 adds the two error codes and the guard-side changes (enabled hosts only, unanswered static forwards, the unresolved-name refusal). Task 5 wires the reloader into the module and reorders the attach and the undo of a detach. Task 6 gives the upgrade and the undo of an upgrade the unanswered-forward refusal and reports unverified starts. Task 7 adds the `npm.instance.reconcile` action and Task 8 the `npm.reconcile` task that applies it. Task 9 holds the link guard, the test hygiene and the harness. Task 10 brings the threat model, the user documentation and the registers.

**Tech Stack:** Go 1.27.4, Docker Engine API (exec, top, events), Markdown.

**Spec:** docs/dforge/specs/2026-11-15-v1-w12g-security-followup-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w12g-tmp/integ && export TMPDIR=/root/.cache/w12g-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/core/lifecycle/ && timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

## Global Constraints

- Scope is the rows this plan cites: security register (`docs/dforge/registers/2026-11-06-v1-security-review.md`) rows 1, 176, 188, 190-199. Out of scope (spec D10): the old workers' residual (w12e D1 (a)), the DNS re-pointing risk (w12e D7), a static-address join, a lock key on the attach, and an automatic re-attach after a reconcile.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. The live proof of the exec reload, the reconcile and the unanswered-forward refusal is the harness step of Task 9, which runs on CI's VMs after the merge (gate 7), never here. Task 1 fetches the registry and the Engine documentation over HTTPS into the scratchpad with `curl`, never through Docker.
- Every `go`, `golangci-lint` and `make` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w12g-tmp/<task number>`, also where a step's command omits one of them. Run `mkdir -p` on that directory first and remove it when the task ends. A `go test -race` of `./internal/modules/npm/` runs as `timeout 1500 go test -race -timeout 24m -count=1` (550-600 s on this host).
- Refusal codes and detail strings are exactly as Contracts gives them; a test asserts them.
- Every new deadline is a named constant in the file that uses it (values in Contracts).
- Comments follow the repository's rule: none that say what the code does; a comment only for a non-obvious why. Fix the stale comments the tasks name.
- No new dependency; `go.mod` does not change.
- The harness (`test/harness`, build tag `harness`) is compiled by `go vet -tags harness ./test/...` here and run only by CI; a harness change is proven here by that vet and by the harness's own unit tests, and live by gate 7 on the merge.

## Contracts

- **Engine calls (Task 2, consumed by Task 3).** New file `internal/platform/dockerapi/exec.go`:
  ```go
  type ExecCreate struct{ Cmd []string; User string }
  func (c *Client) ExecCreate(ctx context.Context, container string, o ExecCreate) (string, error)
  func (c *Client) ExecStart(ctx context.Context, id string) error
  type ExecInspect struct{ ID string; Running bool; ExitCode *int }
  func (c *Client) ExecInspect(ctx context.Context, id string) (ExecInspect, error)
  type TopResult struct{ Titles []string; Processes [][]string }
  func (c *Client) ContainerTop(ctx context.Context, container, psArgs string) (TopResult, error)
  ```
  `ExecCreate` posts `{"AttachStdin":false,"AttachStdout":false,"AttachStderr":false,"Cmd":[...],"User":"..."}` to `/containers/{container}/exec` and returns the `Id` of the 201 answer. `ExecStart` posts `{"Detach":true,"Tty":false}` to `/exec/{id}/start`. `ExecInspect` reads `GET /exec/{id}/json`; `ExitCode` is nil while the answer holds `null`. `ContainerTop` reads `GET /containers/{container}/top?ps_args=<psArgs>`. An empty container or id is `ErrEmptyID`, as the other calls do.
- **Reloader (Task 3, consumed by Task 5).** New file `internal/modules/npm/nginx_exec.go`:
  ```go
  type execEngine interface {
  	InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
  	ExecCreate(ctx context.Context, container string, o dockerapi.ExecCreate) (string, error)
  	ExecStart(ctx context.Context, id string) error
  	ExecInspect(ctx context.Context, id string) (dockerapi.ExecInspect, error)
  	ContainerTop(ctx context.Context, container, psArgs string) (dockerapi.TopResult, error)
  }
  var errNginxTest = errors.New("nginx's configuration test failed")
  func newNginxReloader(e execEngine) *nginxReloader
  func (r *nginxReloader) Reload(ctx context.Context) error
  ```
  The container is the constant `containerName`. The two command lines are fixed: `[]string{"nginx", "-t", "-g", "error_log off;"}` and `[]string{"nginx", "-s", "reload"}`; the process listing uses `psArgs` `-eo pid,ppid,args`. `Reload` returns nil only when `nginx -t` exited 0, `nginx -s reload` exited 0 and, polling `ContainerTop` every `nginxReloadPoll` (100 ms) until `nginxReloadWait` (10 s, the bound of the whole call) is spent, a process whose `args` start with `nginx: worker process` has a pid that was not in the listing taken before the signal. A non-zero exit of the first command is `errNginxTest` (`errors.Is`). Every other failure is an error that is not `errNginxTest`: no exit code, still running at the bound, a non-zero exit of the second command, no new worker at the bound, an Engine error, a missing or stopped container, a non-numeric `PUID` or `PGID`, an exec of an earlier timed-out call still running. The user is `PUID:PGID` of the container's `Config.Env` (both numeric), `0:0` when neither is set. The reloader serializes `Reload` with a mutex, and remembers the id of an exec that was still running at the bound; the next `Reload` inspects it and refuses with `errNginxBusy` while it still runs.
- **Reload in the module (Task 5, consumed by Tasks 6-7).** `deps` gains `reload func(ctx context.Context) error`, built in `productionNM` as `newNginxReloader(client).Reload`; `func (n nm) reloadNginx(ctx context.Context) error` calls it. The old `reloadNginx(ctx, env) (bool, error)` and its listing and update are deleted. Test hook: `thHost` gains `nginx *thNginx` with `reloads int`, `fail(err error)` (the next reload returns err once), `onReload func()` (called at the start of each reload), set as `deps.reload` by `thHost.nm()`.
- **Codes (Task 4).** In `internal/core/errcode/errcode.go`: `NPMForwardUnanswered Code = "npm.forward_unanswered"`, status 409, title `Forward host has no answer`; `NPMForwardUnresolved Code = "npm.forward_unresolved"`, status 409, title `Forward host could not be resolved`.
- **Guard side (Task 4, consumed by Tasks 5-7).** `forwardTargets` skips every proxy host whose `Enabled` is false. `type unansweredForward struct{ Where, Host string }` and `func (n nm) unansweredForwards(ctx context.Context, env core.Env) ([]unansweredForward, error)`: for every enabled proxy host, each custom location whose forward host, cut at its first `/`, is neither empty, nor holds `$`, nor is a literal address, and gets a definite not-found from `forwardAnswers`' sources, in proxy host order. `Where` is `location <path> of proxy host <id>`. `func unansweredDetail(us []unansweredForward) string` joins them as `<where> forwards to <host>, which has no answer` with `; ` between. The unresolved-name refusals (`forwardAnswers`) carry `NPMForwardUnresolved` and the detail `<where> forwards to <host>, which could not be resolved: the resolver did not answer; a stopped or removed container's name is a usual cause` (the Docker-names timeout keeps its `retry` text and no host).
- **Refusal of a failed reload (Task 5).** `func (n nm) reloadRefusal(ctx context.Context, env core.Env, err error, where string) error`: for `errNginxTest`, `NPMForwardUnanswered` with `unansweredDetail` when the list is not empty (the listing is best effort; a failed listing falls through), else `NPMReserved` with `nginx could not be reloaded <where>: nginx's configuration test failed`; for any other error `NPMUnhealthy` with `nginx could not be reloaded <where>: <err>`. `where` is `before darkpanel-npm joins network <net>` or `after darkpanel-npm joined network <net>`. Both are `core.Definite` before a join; after a join the caller adds `; it was disconnected again`.
- **Reconcile (Tasks 7 and 8).** New file `internal/modules/npm/reconcile.go`. Action `npm.instance.reconcile`: class compensable, `ForwardOnly`, `Converges`, side agent, `MinScope` admin, `LockKeys` `["npm"]`, `ApplyTimeout` 45 s, empty input `ReconcileInput struct{}`, output `Reconciled struct{ Dropped []DroppedNetwork }` with `DroppedNetwork struct{ Network string; Address string; Claimants []string }`. Task `npm.reconcile` (`reconcileID`), `Every` 5 minutes (`reconcileEvery`), `LockKeys` `["npm"]`, `Watch` waking on a container `start` event of `darkpanel-npm` and a network `connect` event whose `Actor.Attributes["container"]` is its id or name. `dockerAPI` gains `Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error)` with `type dockerEvents interface{ Next() (dockerapi.Event, error); Close() error }`, as the firewall declares them.

## Assumptions (evidence)

- The pinned NPM's reload is `nginx -t -g "error_log off;"` then `nginx -s reload` (`docs/dforge/notes/w12e-evidence.md` section 1, `/app/internal/nginx.js:108-121`), and a failed second test renames the regenerated host file to `.err` only on the update path (section 1), which the exec path does not take.
- The user the exec starts with, and whether `nginx -t` leaves files another user owns, are unverified — Task 1 reads the image for the user nginx runs as and the `PUID`/`PGID` defaults, and the harness step of Task 9 checks ownership live on the four systems (spec A7); the exec runs as the container's `PUID:PGID`, root when unset (spec D2).
- The Engine's exec and top answers have the shapes of Contracts (Docker Engine API v1.49, `docs.docker.com/reference/api/engine/version/v1.49/`: `ExecInspect` has `Running` and `ExitCode`, `ContainerTop` has `Titles` and `Processes`) — Task 1 reads the documentation and records it; Task 2's tests pin the client to those shapes.
- A new nginx worker appears after `nginx -s reload` is applied and a rejected reload keeps the old workers (nginx.org/en/docs/control.html, "reload": the master starts new workers and shuts the old ones down gracefully; on a failed configuration it keeps the old ones) — Task 1 quotes it.
- `lifecycle.ApplySystem` refuses an irreversible action from a background task (`internal/core/lifecycle/system.go:28-30`), so `npm.instance.reconcile` is declared as `panel.listener.revoke` is (`internal/modules/panel/listener_revoke.go:19-25`).
- `firewall.reconcile` is the model of a task with a timer and a Docker-events watch (`internal/modules/firewall/reconcile.go:17-34,162-183`), and the runner skips a pass while the keys are held (`internal/agent/runtime/background.go:202-206`).
- `forwardTargets`, `forwardAnswers`, `forwardGuard`, `forwardTaken`, `verifyHeld`, `dropNetwork` and `afterJoin` are at the lines the spec names (read 2026-11-15 at 6c41959f).
- `dockerMembers`, which `forwardAnswers` calls, inspects `darkpanel-npm` (`internal/modules/npm/proxyhost_add.go:324-325`), so a test that counts NPM inspections between two events must record the count per event, not per name (Task 5's trace does).
- The first `InspectContainer` of an undo of a finished upgrade is `undoForwardCheck`'s (`internal/modules/npm/upgrade_recover.go:61`), so a fault queued on `InspectContainer` hits the refusal, not `verifyHeld`; Task 6's test fails the name lookup after the restore instead.
- The test Engine's NPM fixture is created running by `thHost.deployed()` (`internal/modules/npm/testhost_test.go:1219`), and `nwSetup` already calls it once; a test needing the container id reads it from `h.docker.container(containerName)`.
- The live proof of the exec reload, the process listing, the reconcile and the unanswered-forward refusal needs a Docker daemon, which Host safety forbids on this host (as w12e's A3a did). Substitution: Task 1's image and API reading and the unit tests of Tasks 2-8. The live proof is Task 9's harness steps, and gate 7 runs them on the branch, pushed and dispatched with `gh workflow run harness.yml --ref <branch>`, before the merge into `main` (`docs/dforge/gates.md` gate 7: "any branch whose diff against `main` touches `test/harness/`"), so a premise that is wrong is found before it is merged. If the exec cannot run `nginx -t` or signal the master, the reloader's user changes (spec Riskiest assumption) and Tasks 3 and 5 are amended in place.
- `docker network connect --ip` is accepted by the Engine only on a network created with a user-configured subnet (Docker Engine documentation, "docker network connect --ip"); Task 9's `m6n-rec` is created with `--subnet`.
- A dotted `.invalid` name gets a definite not-found from the harness VMs' resolver, where a single label did not (`docs/dforge/notes/w12e-evidence.md:324-326`) — unverified — Task 9's harness step uses it and gate 7 proves it.
- The test Engine assigns an address only when it is static or pinned (`internal/modules/npm/testhost_test.go:321`, `pin`), so a test that needs NPM to take a freed address pins it.
- The module's tests count reloads through `w12eReloads`, which counts proxy-host updates (`internal/modules/npm/w12e_attach_test.go:31-40`); Task 5 rewrites it to count `h.nginx.reloads`.

## Task index

1. Evidence: the exec reload on the pinned image and the Engine's exec and top API
2. Engine client: exec and process listing
3. The nginx reloader
4. Codes, enabled hosts only, unanswered static forwards and the unresolved-name refusal
5. Reload by exec in the module, and the attach's and the undo's check order
6. The upgrade and the undo of an upgrade refuse an unanswered static forward; report an unverified start
7. The reconcile action
8. The reconcile task
9. Link guard, test hygiene and the harness
10. Threat model, user documentation and registers

---

### Task 1: Evidence: the exec reload on the pinned image and the Engine's exec and top API

**Files:**
- Create: `docs/dforge/notes/w12g-evidence.md`

**Interfaces:**
- Produces: the evidence note (Assumptions): `## NPM 2.16.0 image` (the answers of Step 2) and `## Engine API` (Step 3), each with file paths or URLs and quoted lines.

**Items:** 2026-11-06-v1-security-review#188

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 0 = 3

- [ ] **Step 1: No new test, because the task records evidence and changes no code**

- [ ] **Step 2: Read the pinned image without Docker**

In the scratchpad, in a new empty directory, follow the method of `docs/dforge/notes/w12e-evidence.md` `## NPM 2.16.0 image`: get an anonymous pull token from `https://auth.docker.io/token?service=registry.docker.io&scope=repository:jc21/nginx-proxy-manager:pull`, fetch the index of digest `sha256:4393e642e233e5efd5adeb7f10918d4aaea71c9ed875b94190273fc8e87f9594` from `https://registry-1.docker.io/v2/jc21/nginx-proxy-manager/manifests/<digest>` (index and manifest media types in `Accept`), take the `linux/amd64` manifest and download its layers, each `curl` with `--max-time 300`, extract only `app/`, `etc/nginx/`, `etc/s6-overlay/` and `etc/passwd`. Answer, each with the file path and the quoted lines:

1. Which user does NPM's backend run `nginx -t` and `nginx -s reload` as (`/app/internal/nginx.js` `utils.exec`, the s6 service that starts the backend, `PUID`/`PGID` defaults)? Which user does nginx's master and worker run as? Do `PUID` and `PGID` have defaults in the image's environment or scripts?
2. Which directories does `nginx -t` write to or open files in (`error_log`, `access_log`, `client_body_temp_path`, `proxy_temp_path`, the pid file `/run/nginx/nginx.pid`), and who owns them at startup?
3. Are the `ps` titles of nginx's processes `nginx: master process` and `nginx: worker process` (nginx's `setproctitle`)? Quote the nginx version.
4. Is `nginx` on the image's `PATH` for an exec (`which nginx` equivalent: the symlink or the openresty path)?

- [ ] **Step 3: Read the Engine documentation**

Fetch `https://docs.docker.com/reference/api/engine/version/v1.49/` sections ExecCreate, ExecStart, ExecInspect and ContainerTop with `curl --max-time 60` into the scratchpad (or the OpenAPI document `https://docs.docker.com/reference/api/engine/version/v1.49.yaml`). Record the request and answer fields of each call the Contracts name: the `Detach` and `Tty` members of ExecStart, `Running` and `ExitCode` (null while running) of ExecInspect, `Titles` and `Processes` and the `ps_args` query of ContainerTop. Quote nginx.org's "reload" paragraph of `https://nginx.org/en/docs/control.html` that says a failed configuration keeps the old workers.

State in the note, in two sentences each, what the harness step of Task 9 must prove live that this reading cannot: the exec's user can run `nginx -t` and signal the master, no file under nginx's log or temp directories changes owner, `ContainerTop` lists the workers on all four systems.

**Gate (D2):** if question 1 shows the master runs as a user the exec's `PUID:PGID` (root when unset) cannot signal, or question 3 shows the process titles differ from the Contracts' `nginx: worker process`, stop before Task 3, record `Gate D2: <finding>` in the ledger, and return D2 to the owner with the user or title found.

- [ ] **Step 4: Commit**

```bash
git add docs/dforge/notes/w12g-evidence.md
git commit -m "docs(notes): record the w12g image and Engine evidence"
```

---

### Task 2: Engine client: exec and process listing

**Files:**
- Create: `internal/platform/dockerapi/exec.go`
- Test: `internal/platform/dockerapi/exec_test.go`

**Interfaces:**
- Consumes: `newFakeDaemon`, `request`, `Client.call` (existing, `client.go`, `client_test.go`).
- Produces: the Engine calls (Contracts, Engine calls).

**Items:** 2026-11-06-v1-security-review#188

**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 1 - coupling 0 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

```go
package dockerapi

import (
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"testing"
)

func TestExecCreateStartInspect(t *testing.T) {
	mux := http.NewServeMux()
	var created map[string]any
	var started map[string]any
	mux.HandleFunc("POST /v1.49/containers/{id}/exec", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") == "gone" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"No such container: gone"}`)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"Id":"e1"}`)
	})
	mux.HandleFunc("POST /v1.49/exec/{id}/start", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&started); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /v1.49/exec/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "running":
			_, _ = io.WriteString(w, `{"ID":"running","Running":true,"ExitCode":null}`)
		case "failed":
			_, _ = io.WriteString(w, `{"ID":"failed","Running":false,"ExitCode":1}`)
		default:
			_, _ = io.WriteString(w, `{"ID":"e1","Running":false,"ExitCode":0}`)
		}
	})
	c := newFakeDaemon(t, "1.49", mux)
	ctx := t.Context()
	id, err := c.ExecCreate(ctx, "darkpanel-npm", ExecCreate{Cmd: []string{"nginx", "-t"}, User: "0:0"})
	if err != nil || id != "e1" {
		t.Fatalf("create = %q, %v", id, err)
	}
	wantCreate := map[string]any{"AttachStdin": false, "AttachStdout": false, "AttachStderr": false, "Cmd": []any{"nginx", "-t"}, "User": "0:0"}
	if !reflect.DeepEqual(created, wantCreate) {
		t.Fatalf("create body = %v", created)
	}
	if err := c.ExecStart(ctx, id); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(started, map[string]any{"Detach": true, "Tty": false}) {
		t.Fatalf("start body = %v", started)
	}
	for id, want := range map[string]ExecInspect{
		"running": {ID: "running", Running: true},
		"e1":      {ID: "e1", ExitCode: new(0)},
		"failed":  {ID: "failed", ExitCode: new(1)},
	} {
		got, err := c.ExecInspect(ctx, id)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("inspect %s = %+v, %v; want %+v", id, got, err, want)
		}
	}
	if _, err := c.ExecCreate(ctx, "gone", ExecCreate{Cmd: []string{"x"}}); !IsNotFound(err) {
		t.Fatalf("create in a missing container err = %v", err)
	}
	if _, err := c.ExecCreate(ctx, "", ExecCreate{}); err == nil {
		t.Fatal("an empty container was accepted")
	}
	if err := c.ExecStart(ctx, ""); err == nil {
		t.Fatal("an empty exec id was accepted")
	}
}

func TestContainerTop(t *testing.T) {
	mux := http.NewServeMux()
	var query string
	mux.HandleFunc("GET /v1.49/containers/{id}/top", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		_, _ = io.WriteString(w, `{"Titles":["PID","PPID","COMMAND"],"Processes":[["10","1","nginx: master process nginx"],["11","10","nginx: worker process"]]}`)
	})
	c := newFakeDaemon(t, "1.49", mux)
	got, err := c.ContainerTop(t.Context(), "darkpanel-npm", "-eo pid,ppid,args")
	if err != nil {
		t.Fatal(err)
	}
	want := TopResult{Titles: []string{"PID", "PPID", "COMMAND"}, Processes: [][]string{{"10", "1", "nginx: master process nginx"}, {"11", "10", "nginx: worker process"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("top = %+v", got)
	}
	if query != "ps_args=-eo+pid%2Cppid%2Cargs" {
		t.Fatalf("query = %q", query)
	}
	if _, err := c.ContainerTop(t.Context(), "", ""); err == nil {
		t.Fatal("an empty container was accepted")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/2 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/2 timeout 300 go test -count=1 -run 'TestExecCreateStartInspect|TestContainerTop' ./internal/platform/dockerapi/`
Expected: FAIL to compile with `c.ExecCreate undefined`.

- [ ] **Step 3: Implement**

1. `exec.go`: the types and methods of Contracts, Engine calls. Build requests as the neighbouring calls do (`c.call(ctx, request{method, path, query, body}, &out)`, `requireID("container", container)` and `requireID("exec", id)`, `url.PathEscape`). `ExecCreate`'s body is a struct with the fields `AttachStdin`, `AttachStdout`, `AttachStderr` (always false), `Cmd` and `User` (omit `User` when empty); `ExecStart`'s body is `{Detach: true, Tty: false}`; `ExecInspect`'s `ExitCode` is `*int` and decodes `null` as nil; `ContainerTop` sends the query `ps_args` through `url.Values`.
2. `new(0)` in the test is Go 1.26+ `new(expr)`; keep it, the module is on Go 1.27.4.
3. `live_test.go` stays unchanged: the live proof of the exec and top calls is the harness step of Task 9 on the pinned NPM.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/2 timeout 300 go test -count=1 ./internal/platform/dockerapi/ && rm -rf /root/.cache/w12g-tmp/2`.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/dockerapi/exec.go internal/platform/dockerapi/exec_test.go
git commit -m "feat(dockerapi): add exec and container top calls"
```

---

### Task 3: The nginx reloader

**Files:**
- Create: `internal/modules/npm/nginx_exec.go`
- Modify: `internal/modules/npm/nginx_reload.go` (the `nginxReloadWait` constant moves to `nginx_exec.go`)
- Test: `internal/modules/npm/nginx_exec_test.go`

**Interfaces:**
- Consumes: the Engine calls (Contracts, Engine calls); `containerName`.
- Produces: the reloader (Contracts, Reloader).

**Items:** 2026-11-06-v1-security-review#188

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// fakeExec scripts the Engine under the reloader. exits maps a command line (joined by spaces) to its exit code; a
// command in hang never finishes; a command in noCode finishes with no exit code. workers is the list of worker pids
// that ContainerTop answers before the reload signal, and after is the list it answers once the reload was started.
type fakeExec struct {
	mu         sync.Mutex
	env        []string
	inspectErr error
	exits      map[string]int
	hang       map[string]bool
	noCode     map[string]bool
	workers    []string
	after      []string
	created    []string
	cmds       [][]string
	users      []string
	containers []string
	stopped    bool
	ids        map[string]string
	started    map[string]bool
	reloaded   bool
}

func newFakeExec() *fakeExec {
	return &fakeExec{
		exits: map[string]int{}, hang: map[string]bool{}, noCode: map[string]bool{}, ids: map[string]string{}, started: map[string]bool{},
		workers: []string{"11"}, after: []string{"12"},
	}
}

func (f *fakeExec) InspectContainer(_ context.Context, _ string) (dockerapi.ContainerJSON, error) {
	if f.inspectErr != nil {
		return dockerapi.ContainerJSON{}, f.inspectErr
	}
	return dockerapi.ContainerJSON{Config: dockerapi.ContainerConfig{Env: f.env}, State: dockerapi.ContainerState{Running: !f.stopped}}, nil
}

func (f *fakeExec) ExecCreate(_ context.Context, container string, o dockerapi.ExecCreate) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(o.Cmd, " ")
	id := "x" + line
	f.created = append(f.created, line)
	f.cmds = append(f.cmds, o.Cmd)
	f.users = append(f.users, o.User)
	f.containers = append(f.containers, container)
	f.ids[id] = line
	return id, nil
}

func (f *fakeExec) ExecStart(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started[id] = true
	if f.ids[id] == "nginx -s reload" {
		f.reloaded = true
	}
	return nil
}

func (f *fakeExec) ExecInspect(_ context.Context, id string) (dockerapi.ExecInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := f.ids[id]
	switch {
	case f.hang[line]:
		return dockerapi.ExecInspect{ID: id, Running: true}, nil
	case f.noCode[line]:
		return dockerapi.ExecInspect{ID: id}, nil
	}
	code := f.exits[line]
	return dockerapi.ExecInspect{ID: id, ExitCode: &code}, nil
}

func (f *fakeExec) ContainerTop(_ context.Context, _, _ string) (dockerapi.TopResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pids := f.workers
	if f.reloaded {
		pids = f.after
	}
	rows := [][]string{{"10", "1", "nginx: master process nginx"}}
	for _, p := range pids {
		rows = append(rows, []string{p, "10", "nginx: worker process"})
	}
	return dockerapi.TopResult{Titles: []string{"PID", "PPID", "COMMAND"}, Processes: rows}, nil
}

const (
	testCmd   = "nginx -t -g error_log off;"
	reloadCmd = "nginx -s reload"
)

func quickReloader(f *fakeExec) *nginxReloader {
	r := newNginxReloader(f)
	r.wait, r.poll = 300*time.Millisecond, 10*time.Millisecond
	return r
}

func TestNginxReloaderConfirmsANewWorker(t *testing.T) {
	f := newFakeExec()
	if err := quickReloader(f).Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.cmds) != 2 || !slices.Equal(f.cmds[0], []string{"nginx", "-t", "-g", "error_log off;"}) || !slices.Equal(f.cmds[1], []string{"nginx", "-s", "reload"}) {
		t.Errorf("commands %q, want the test and then the reload as two exact argument lists", f.cmds)
	}
	for i, c := range f.containers {
		if c != containerName || f.users[i] != "0:0" {
			t.Errorf("command %d ran in %q as %q, want %q as 0:0", i, c, f.users[i], containerName)
		}
	}
}

func TestNginxReloaderFailures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arrange  func(f *fakeExec)
		wantTest bool
		started  int
	}{
		{"the configuration test fails", func(f *fakeExec) { f.exits[testCmd] = 1 }, true, 1},
		{"the reload signal fails", func(f *fakeExec) { f.exits[reloadCmd] = 1 }, false, 2},
		{"the test returns no exit code", func(f *fakeExec) { f.noCode[testCmd] = true }, false, 1},
		{"the test is still running at the bound", func(f *fakeExec) { f.hang[testCmd] = true }, false, 1},
		{"no new worker follows the signal", func(f *fakeExec) { f.after = f.workers }, false, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeExec()
			tc.arrange(f)
			err := quickReloader(f).Reload(context.Background())
			if err == nil {
				t.Fatal("the reload was confirmed")
			}
			if errors.Is(err, errNginxTest) != tc.wantTest {
				t.Errorf("errors.Is(err, errNginxTest) = %v for %v, want %v", !tc.wantTest, err, tc.wantTest)
			}
			if len(f.created) != tc.started {
				t.Errorf("%d commands were created, want %d", len(f.created), tc.started)
			}
		})
	}
}

func TestNginxReloaderUser(t *testing.T) {
	f := newFakeExec()
	f.env = []string{"PATH=/bin", "PUID=1000", "PGID=1001"}
	if err := quickReloader(f).Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.users[0] != "1000:1001" {
		t.Errorf("user %q, want 1000:1001", f.users[0])
	}
	f = newFakeExec()
	f.env = []string{"PUID=abc"}
	if err := quickReloader(f).Reload(context.Background()); err == nil || len(f.created) != 0 {
		t.Errorf("a non-numeric PUID ran %d commands with error %v, want none and an error", len(f.created), err)
	}
	f = newFakeExec()
	f.inspectErr = dockerapi.ErrUnavailable
	if err := quickReloader(f).Reload(context.Background()); err == nil || errors.Is(err, errNginxTest) {
		t.Errorf("an unreadable container gave %v, want a failure that is not the test's", err)
	}
	f = newFakeExec()
	f.stopped = true
	if err := quickReloader(f).Reload(context.Background()); err == nil || errors.Is(err, errNginxTest) || len(f.created) != 0 {
		t.Errorf("a stopped container ran %d commands with error %v, want none and a failure that is not the test's", len(f.created), err)
	}
}

func TestNginxReloaderRefusesWhileAnEarlierExecRuns(t *testing.T) {
	f := newFakeExec()
	f.hang[testCmd] = true
	r := quickReloader(f)
	if err := r.Reload(context.Background()); err == nil {
		t.Fatal("a hung test was confirmed")
	}
	before := len(f.created)
	err := r.Reload(context.Background())
	if !errors.Is(err, errNginxBusy) {
		t.Fatalf("second reload = %v, want errNginxBusy", err)
	}
	if len(f.created) != before {
		t.Error("a second exec was created while the earlier one runs")
	}
	delete(f.hang, testCmd)
	if err := r.Reload(context.Background()); err != nil {
		t.Fatalf("after the earlier exec ended: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/3 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/3 timeout 300 go test -count=1 -run 'TestNginxReloader' ./internal/modules/npm/`
Expected: FAIL to compile with `undefined: newNginxReloader`.

- [ ] **Step 3: Implement**

1. `nginx_exec.go`: `execEngine`, `errNginxTest`, `errNginxBusy` (`errors.New("an earlier nginx command is still running")`), constants `nginxReloadWait = 10 * time.Second` and `nginxReloadPoll = 100 * time.Millisecond` (the old `nginxReloadWait` in `nginx_reload.go` is deleted by Task 5; declare it here and delete the old line in this task so the package compiles), `type nginxReloader struct { e execEngine; mu sync.Mutex; wait, poll time.Duration; pending string }` and `newNginxReloader`, which sets `wait` and `poll` to the constants.
2. `Reload(ctx)`: lock `mu`; run everything under one `context.WithTimeout(ctx, r.wait)`. If `pending` is set, `ExecInspect` it: still running → `errNginxBusy`; ended (any answer) → clear it. Resolve the user from `InspectContainer(containerName)`: read `PUID` and `PGID` from `Config.Env`; both absent → `"0:0"`; both present and made only of digits → `"<PUID>:<PGID>"`; any other case is an error before any exec. List the workers: `ContainerTop(containerName, "-eo pid,ppid,args")`, keep the pid column of every row whose command column starts with `nginx: worker process` (find the `PID` and `COMMAND` or `ARGS` titles by name, case-insensitively). Run the test command with `run`, then the reload command with `run`.
3. `run(ctx, cmd)`: `ExecCreate(containerName, {Cmd: cmd, User: user})`, `ExecStart(id)`, then poll `ExecInspect(id)` every `r.poll` until `Running` is false or `ctx` ends; on `ctx` ending set `r.pending = id` and return a non-`errNginxTest` error (`nginx command timed out`); an answer with `ExitCode == nil` and not running is an error (`no exit code`); a non-zero code returns `errNginxTest` for the test command and `fmt.Errorf("nginx reload exited %d", code)` for the reload.
4. After a zero exit of the reload, poll `ContainerTop` every `r.poll` until a worker pid not in the first listing appears, or `ctx` ends (`no new nginx worker after the reload`).
5. Errors from the Engine calls are wrapped with `fmt.Errorf("npm: nginx reload: %w", err)` and never include command output; none is read.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/3 timeout 600 go test -count=1 ./internal/modules/npm/ && rm -rf /root/.cache/w12g-tmp/3` (the package still builds because Task 3 keeps the old reload in place until Task 5; the old constant moved here).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/nginx_exec.go internal/modules/npm/nginx_exec_test.go internal/modules/npm/nginx_reload.go
git commit -m "feat(npm): add the nginx reloader that runs in the container"
```

---

### Task 4: Codes, enabled hosts only, unanswered static forwards and the unresolved-name refusal

**Files:**
- Modify: `internal/core/errcode/errcode.go`
- Modify: `internal/modules/npm/network_claims.go` (`forwardTargets`, `forwardAnswers`, the new `unansweredForwards` and `unansweredDetail`)
- Test: `internal/core/errcode/w12g_test.go`
- Test: `internal/modules/npm/w12g_guard_test.go`
- Test: the existing tests that assert the old `unavailable` code of an unresolved name (found in Step 3)

**Interfaces:**
- Consumes: `forwardAnswers`, `proxy.LiteralAddr`, `nwSetup`, `w12aSubnet`, `w12aSeed`, `w12eNoAnswer`, `npmapi.Location` (existing).
- Produces: the codes (Contracts, Codes) and the guard side (Contracts, Guard side).

**Items:** 2026-11-06-v1-security-review#193, 2026-11-06-v1-security-review#194

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 2 - spec 1 - coupling 2 - risk 0 = 5

- [ ] **Step 1: Write the failing test**

`internal/core/errcode/w12g_test.go`:

```go
package errcode

import (
	"net/http"
	"testing"
)

func TestW12gForwardCodes(t *testing.T) {
	for code, title := range map[Code]string{
		NPMForwardUnanswered: "Forward host has no answer",
		NPMForwardUnresolved: "Forward host could not be resolved",
	} {
		if !Known(code) || code.Status() != http.StatusConflict || code.Title() != title {
			t.Errorf("%s: known %v status %d title %q, want known, 409 and %q", code, Known(code), code.Status(), code.Title(), title)
		}
	}
	if NPMForwardUnanswered != "npm.forward_unanswered" || NPMForwardUnresolved != "npm.forward_unresolved" {
		t.Error("a code's wire name changed")
	}
}
```

`internal/modules/npm/w12g_guard_test.go`:

```go
package npm

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

func w12gLoc(path, host string) npmapi.Location {
	return npmapi.Location{Path: path, ForwardScheme: "http", ForwardHost: host, ForwardPort: 80}
}

func TestW12gForwardTargetsEnabledHostsOnly(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "10.9.0.20", 80)
	w12aSeed(h, "10.9.0.21", 80, w12gLoc("/x", "10.9.0.22"))
	h.npm.DisableProxyHost(2)
	got, err := h.nm().forwardTargets(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].addr != netip.MustParseAddr("10.9.0.20") || got[0].where != "proxy host 1" {
		t.Errorf("targets %+v, want only proxy host 1's 10.9.0.20", got)
	}
}

func TestW12gUnansweredForwards(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "def.invalid", 80,
		w12gLoc("/a", "gone.invalid"), w12gLoc("/b", "10.9.0.5"), w12gLoc("/c", "$host"), w12gLoc("/d", "ok.invalid"), w12gLoc("/e", "gone2.invalid/x"))
	w12aSeed(h, "def2.invalid", 80, w12gLoc("/z", "off.invalid"))
	h.npm.DisableProxyHost(2)
	n := h.nm()
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "ok.invalid" {
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	got, err := n.unansweredForwards(context.Background(), h.env(""))
	if err != nil {
		t.Fatal(err)
	}
	want := []unansweredForward{{Where: "location /a of proxy host 1", Host: "gone.invalid"}, {Where: "location /e of proxy host 1", Host: "gone2.invalid"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unanswered %+v, want %+v (the default forward host, a literal, a variable, an answering name and a disabled host are not listed)", got, want)
	}
	wantText := "location /a of proxy host 1 forwards to gone.invalid, which has no answer; location /e of proxy host 1 forwards to gone2.invalid, which has no answer"
	if text := unansweredDetail(got); text != wantText {
		t.Errorf("detail %q, want %q", text, wantText)
	}
}

func TestW12gUnresolvedNameRefusal(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "web-old", 80)
	n := h.nm()
	n.d.resolve = func(_ context.Context, host string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Err: "server misbehaving", Name: host, IsTemporary: true}
	}
	err := n.forwardGuard(context.Background(), h.env(""), user, netip.Addr{})
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMForwardUnresolved || !core.IsDefinite(err) {
		t.Fatalf("error %v, want a definite %s", err, errcode.NPMForwardUnresolved)
	}
	want := "proxy host 1 forwards to web-old, which could not be resolved: the resolver did not answer; a stopped or removed container's name is a usual cause"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/4 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/4 timeout 600 go test -count=1 -run 'TestW12g' ./internal/core/errcode/ ./internal/modules/npm/`
Expected: FAIL to compile with `undefined: NPMForwardUnanswered`.

- [ ] **Step 3: Implement**

1. `errcode.go`: the two codes next to `NPMReserved`, in the code table with status 409 and the titles of Contracts, Codes. If the package keeps a list of every code in a test or a docs table, add them there (run `rg -n "NPMReserved" internal docs --glob '!*_test.go'` and follow every hit that lists codes).
2. `network_claims.go`: extract from `forwardTargets` the listing of the proxy hosts and the cutting of each forward host at its first `/` into one helper, `forwardRefs(ctx, env) ([]forwardRef, error)`, that skips every host whose `Enabled` is false and returns, per forward, `where`, the cut host, the literal address (when it is one) and whether it is a location. `forwardTargets` keeps its output and builds on it. `unansweredForwards` builds on the same helper: take the location forwards whose host is neither empty, nor holds `$`, nor is a literal, resolve their names with `forwardAnswers` (Docker's names first), and return those with no answer in proxy host order. `unansweredDetail` joins them as Contracts gives.
3. `forwardAnswers` keeps its signature. A name whose lookup gives neither an answer nor a definite not-found returns a new unexported `*unresolvedName{name string}`; `forwardTargets` turns it into `core.Definite(errcode.New(errcode.NPMForwardUnresolved, "<where> forwards to <host>, which could not be resolved: the resolver did not answer; a stopped or removed container's name is a usual cause"))`, with `where` and `host` of the first forward that uses the name. The Docker-names timeout keeps its text `Docker's names of the forward hosts could not be read in time; retry` and takes the code `NPMForwardUnresolved` too. `verifyHeld` (`instance_upgrade.go:526`) still receives an error it treats as unverified.
4. Find the tests that assert the old code or detail with `rg -n "resolver did not answer|errcode.Unavailable" internal/modules/npm/*_test.go` and update each to `NPMForwardUnresolved` and the new detail.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/4 timeout 1500 go test -count=1 ./internal/core/errcode/ ./internal/modules/npm/ && rm -rf /root/.cache/w12g-tmp/4`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/errcode internal/modules/npm
git commit -m "feat(npm): look only at enabled hosts and name unanswered forwards"
```

---

### Task 5: Reload by exec in the module, and the attach's and the undo's check order

**Files:**
- Modify: `internal/modules/npm/nginx_reload.go`
- Modify: `internal/modules/npm/deps.go` (`deps.reload`, `productionNM`)
- Modify: `internal/modules/npm/network_claims.go` (`Apply`, `check`, `networkUndoDisconnect`, `reloadBeforeJoin`, `afterJoin`, `joinedProblem`, the comments at the old lines 823-826 and 768-771)
- Modify: `internal/modules/npm/testhost_test.go` (`thNginx`, `thHost.nginx`, `nm()`)
- Modify: `internal/modules/npm/w12e_attach_test.go`, `internal/modules/npm/w12e_security_residuals_acceptance_test.go`, `internal/modules/npm/network_claims_test.go` (the stale comment at the old line 1159)
- Test: `internal/modules/npm/w12g_attach_test.go`

**Interfaces:**
- Consumes: the reloader (Contracts, Reloader), the codes and guard side (Tasks 4).
- Produces: the module's reload (Contracts, Reload in the module) and the refusal (Contracts, Refusal of a failed reload).

**Items:** 2026-11-06-v1-security-review#1, 2026-11-06-v1-security-review#176, 2026-11-06-v1-security-review#188, 2026-11-06-v1-security-review#190, 2026-11-06-v1-security-review#196, 2026-11-06-v1-security-review#198, 2026-11-06-v1-security-review#199

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

First add to `testhost_test.go` (no test of its own; the tests below use it):

```go
// thNginx is the module's nginx reload: it counts the reloads and answers each with the next queued error.
type thNginx struct {
	mu       sync.Mutex
	reloads  int
	next     []error
	onReload func()
}

func (g *thNginx) fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next = append(g.next, err)
}

func (g *thNginx) reload(context.Context) error {
	g.mu.Lock()
	g.reloads++
	hook := g.onReload
	var err error
	if len(g.next) > 0 {
		err, g.next = g.next[0], g.next[1:]
	}
	g.mu.Unlock()
	if hook != nil {
		hook()
	}
	return err
}
```

`thHost` gains `nginx *thNginx` (created in `newTHHost`) and `nm()` sets `reload: h.nginx.reload`. Then `w12g_attach_test.go`:

```go
package npm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12gTrace wraps the test Engine and records the order of the calls an attach makes, with the number of requests NPM
// had received at each.
type w12gTrace struct {
	*thDocker
	h      *thHost
	mu     sync.Mutex
	events []string
	counts []int
	hook   map[string]func()
	strip  string
	lost   error
}

func newW12gTrace(h *thHost) *w12gTrace {
	return &w12gTrace{thDocker: h.docker, h: h, hook: map[string]func(){}}
}

func (d *w12gTrace) rec(ev string) {
	d.mu.Lock()
	d.events = append(d.events, ev)
	d.counts = append(d.counts, len(d.h.npm.Requests()))
	d.mu.Unlock()
}

// index is the position of the first event ev, -1 when there is none.
func (d *w12gTrace) index(ev string) int {
	for i, e := range d.events {
		if e == ev {
			return i
		}
	}
	return -1
}

func (d *w12gTrace) ConnectNetwork(ctx context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	if f := d.hook["connect"]; f != nil {
		f()
	}
	d.rec("connect")
	err := d.thDocker.ConnectNetwork(ctx, network, container, ep)
	if err == nil && d.lost != nil {
		return d.lost
	}
	return err
}

func (d *w12gTrace) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	c, err := d.thDocker.InspectContainer(ctx, id)
	if err == nil && d.strip != "" && id == containerName {
		delete(c.NetworkSettings.Networks, d.strip)
	}
	d.rec("inspect:" + id)
	return c, err
}

func (d *w12gTrace) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	d.rec("disconnect")
	return d.thDocker.DisconnectNetwork(ctx, network, container, force)
}

// after returns the events that follow the first event ev.
func (d *w12gTrace) after(ev string) []string {
	for i, e := range d.events {
		if e == ev {
			return d.events[i+1:]
		}
	}
	return nil
}

const w12gAddr = "10.9.0.7"

// w12gHolder gives appnet a subnet, puts a container holding w12gAddr on it and a proxy host that forwards to it by
// literal and to up.invalid by name; the guard passes while the holder is there. The Engine hands NPM w12gAddr next.
func w12gHolder(t *testing.T, h *thHost, user dockerapi.Network) {
	t.Helper()
	w12aSubnet(t, h, user)
	h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
	w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
	h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
}

func w12gAttach(t *testing.T, h *thHost, n nm, in AttachInput) error {
	t.Helper()
	h.addChange("c1", "npm.network.attach")
	_, err := n.networkAttach().Handler.Apply(context.Background(), h.env("c1"), nwJSON(t, in))
	return err
}

func TestW12gAttachMakesNoCallBetweenConnectAndInspection(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		tr.rec("resolve")
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	if err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err != nil {
		t.Fatal(err)
	}
	ci := tr.index("connect")
	if ci < 0 || ci+1 >= len(tr.events) || tr.events[ci+1] != "inspect:"+containerName {
		t.Fatalf("events %v, want the inspection of NPM right after the connect", tr.events)
	}
	if tr.counts[ci] != tr.counts[ci+1] {
		t.Errorf("NPM got %d requests at the connect and %d at the next inspection; a listing sits in between", tr.counts[ci], tr.counts[ci+1])
	}
}

func TestW12gAttachDisconnectsBeforeAnyLookupOrReload(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	tr := newW12gTrace(h)
	tr.hook["connect"] = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	h.nginx.onReload = func() { tr.rec("reload") }
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		tr.rec("resolve")
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	_ = nwRefused(t, err, errcode.NPMReserved)
	between := tr.after("connect")
	cut := 0
	for i, e := range between {
		if e == "disconnect" {
			cut = i
			break
		}
	}
	for _, e := range between[:cut] {
		if e == "reload" || e == "resolve" {
			t.Errorf("%q ran between the connect and the disconnect: %v", e, between)
		}
	}
	if h.nginx.reloads != 1 {
		t.Errorf("%d reloads, want only the one before the join", h.nginx.reloads)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachRereadsTheNetworkAfterTheFirstReload(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	h.nginx.onReload = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	n := h.nm()
	n.d.resolve = w12eAnswer("203.0.113.9")
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	_ = nwRefused(t, err, errcode.NPMReserved)
	if nwCalls(h, "ConnectNetwork") != 0 {
		t.Error("the attach connected NPM to an address a proxy host forwards to")
	}
	w12eNoChange(t, h, user, 0)
}

func TestW12gReloadRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		seed   func(h *thHost)
		fail   error
		code   errcode.Code
		detail string
	}{
		{"the test fails, no unanswered forward", func(h *thHost) { w12aSeed(h, "up.invalid", 81) }, errNginxTest, errcode.NPMReserved,
			"nginx could not be reloaded before darkpanel-npm joins network appnet: nginx's configuration test failed"},
		{"the test fails, a location has no answer", func(h *thHost) { w12aSeed(h, "up.invalid", 81, w12gLoc("/a", "gone.invalid")) }, errNginxTest,
			errcode.NPMForwardUnanswered, "location /a of proxy host 1 forwards to gone.invalid, which has no answer"},
		{"the exec fails", func(h *thHost) { w12aSeed(h, "up.invalid", 81) }, errors.New("boom"), errcode.NPMUnhealthy,
			"nginx could not be reloaded before darkpanel-npm joins network appnet: boom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, user := nwSetup(t)
			w12aSubnet(t, h, user)
			tc.seed(h)
			h.nginx.fail(tc.fail)
			n := h.nm()
			n.d.resolve = w12eNoAnswer
			err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
			coded := nwRefused(t, err, tc.code)
			if coded.Detail != tc.detail {
				t.Errorf("detail %q, want %q", coded.Detail, tc.detail)
			}
			if nwCalls(h, "ConnectNetwork") != 0 {
				t.Error("a refused reload was followed by a connect")
			}
		})
	}
}

func TestW12gSecondReloadFailureDisconnectsBeforeTheDetail(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	h.nginx.fail(nil)
	h.nginx.fail(errNginxTest)
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	coded := nwRefused(t, err, errcode.NPMReserved)
	if want := "nginx could not be reloaded after darkpanel-npm joined network appnet: nginx's configuration test failed; it was disconnected again"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	di := tr.index("disconnect")
	if di < 0 {
		t.Fatalf("events %v hold no disconnect", tr.events)
	}
	if got := len(h.npm.Requests()); got <= tr.counts[di] {
		t.Errorf("NPM had %d requests at the disconnect and %d at the end: the detail's listing did not follow the disconnect", tr.counts[di], got)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachOnAJoinedNetwork(t *testing.T) {
	in := AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}
	setup := func(t *testing.T, forward string) (*thHost, dockerapi.Network) {
		h, user := nwSetup(t)
		w12aSubnet(t, h, user)
		nwConnect(t, h, user, nwStatic(w12gAddr))
		w12aSeed(h, forward, 3000)
		return h, user
	}
	t.Run("no claim and a hit: NPM leaves before any reload", func(t *testing.T) {
		h, user := setup(t, w12gAddr)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		_ = nwRefused(t, w12gAttach(t, h, n, in), errcode.NPMReserved)
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("NPM stayed on a network where it holds a forwarded address")
		}
		if h.nginx.reloads != 0 {
			t.Errorf("%d reloads, want none: a hit disconnects NPM before nginx is reloaded", h.nginx.reloads)
		}
	})
	t.Run("no claim and no hit: one reload and the claim", func(t *testing.T) {
		h, user := setup(t, "10.9.0.40")
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		if err := w12gAttach(t, h, n, in); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 1 || nwEndpoint(t, h, user.ID) == nil || len(nwClaims(t, h, nwNet)) != 1 {
			t.Errorf("%d reloads, on the network %v, %d claims; want 1, yes and 1", h.nginx.reloads, nwEndpoint(t, h, user.ID) != nil, len(nwClaims(t, h, nwNet)))
		}
	})
	t.Run("a claim exists: the attach only adds a claim", func(t *testing.T) {
		h, user := setup(t, w12gAddr)
		nwClaim(t, h, nwNet, "someone-else", "c0", true)
		n := h.nm()
		n.d.resolve = w12eNoAnswer
		before := len(h.npm.Requests())
		if err := w12gAttach(t, h, n, in); err != nil {
			t.Fatal(err)
		}
		if h.nginx.reloads != 0 || len(h.npm.Requests()) != before || nwEndpoint(t, h, user.ID) == nil {
			t.Errorf("a claim-only attach made %d reloads, %d NPM requests and left NPM on the network %v; want 0, 0 and yes",
				h.nginx.reloads, len(h.npm.Requests())-before, nwEndpoint(t, h, user.ID) != nil)
		}
	})
}

func TestW12gAttachWhoseConnectAnswerIsLostChecksTheAddress(t *testing.T) {
	h, user := nwSetup(t)
	w12gHolder(t, h, user)
	tr := newW12gTrace(h)
	tr.hook["connect"] = func() {
		if err := h.docker.RemoveContainer(context.Background(), "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Error(err)
		}
	}
	tr.lost = errors.New("connection reset by peer")
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	if err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"}); err == nil {
		t.Fatal("an attach whose connect answer was lost succeeded")
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM stayed on a network where it holds a forwarded address after a lost connect answer")
	}
	if tr.index("disconnect") < 0 {
		t.Errorf("events %v hold no disconnect", tr.events)
	}
}

// w12gUndoEffect detaches NPM from appnet and returns the effect the undo of that detach takes.
func w12gUndoEffect(t *testing.T, h *thHost, user dockerapi.Network) core.Effect {
	t.Helper()
	w12aSubnet(t, h, user)
	nwSeedAttached(t, h, user, nwClaimant, false)
	if _, err := nwApplyDetach(t, h, "c2", DetachInput{Network: nwNet, Claimant: nwClaimant}); err != nil {
		t.Fatal(err)
	}
	var endpoint core.Effect
	for _, e := range h.journal.effects() {
		if e.Kind == core.EffectModify {
			endpoint = e
		}
	}
	return endpoint
}

func TestW12gUndoOfADetachChecksInTheSameOrder(t *testing.T) {
	ctx := context.Background()
	t.Run("a freed address is caught right after the connect", func(t *testing.T) {
		h, user := nwSetup(t)
		endpoint := w12gUndoEffect(t, h, user)
		h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
		w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
		h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
		tr := newW12gTrace(h)
		tr.hook["connect"] = func() {
			if err := h.docker.RemoveContainer(ctx, "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Error(err)
			}
		}
		h.nginx.onReload = func() { tr.rec("reload") }
		n := h.nm()
		n.d.docker = tr
		n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
			tr.rec("resolve")
			return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
		}
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		_ = nwRefused(t, err, errcode.NPMReserved)
		for _, e := range tr.after("connect") {
			if e == "disconnect" {
				break
			}
			if e == "reload" || e == "resolve" {
				t.Errorf("%q ran between the connect and the disconnect: %v", e, tr.after("connect"))
			}
		}
		if nwEndpoint(t, h, user.ID) != nil {
			t.Error("the undo left NPM on a network where it holds a forwarded address")
		}
	})
	t.Run("the network is read again after the first reload", func(t *testing.T) {
		h, user := nwSetup(t)
		endpoint := w12gUndoEffect(t, h, user)
		h.docker.putContainer(thContainer("holder", thEndpoint(nwNet, w12gAddr)))
		w12aSeed(h, w12gAddr, 3000, w12gLoc("/n", "up.invalid"))
		h.docker.pin(nwNet, netip.MustParseAddr(w12gAddr))
		h.nginx.onReload = func() {
			if err := h.docker.RemoveContainer(ctx, "holder", dockerapi.RemoveContainerOptions{Force: true}); err != nil {
				t.Error(err)
			}
		}
		tr := newW12gTrace(h)
		n := h.nm()
		n.d.docker = tr
		n.d.resolve = w12eAnswer("203.0.113.9")
		_, err := n.networkDetach().Handler.Inverse(ctx, h.env("u1"), endpoint)
		_ = nwRefused(t, err, errcode.NPMReserved)
		if tr.index("connect") >= 0 {
			t.Errorf("events %v: the undo connected NPM to an address a proxy host forwards to", tr.events)
		}
	})
}

func TestW12gAJoinedNPMWithNoEndpointIsDisconnected(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	w12aSeed(h, "up.invalid", 81)
	tr := newW12gTrace(h)
	tr.strip = nwNet
	n := h.nm()
	n.d.docker = tr
	n.d.resolve = w12eAnswer("203.0.113.9")
	err := w12gAttach(t, h, n, AttachInput{Network: nwNet, Claimant: nwClaimant, Target: "web"})
	if err == nil || !core.IsDefinite(err) || !strings.HasSuffix(err.Error(), "; it was disconnected again") {
		t.Fatalf("error %v, want a definite refusal that says NPM was disconnected again", err)
	}
	w12eNoChange(t, h, user, 1)
}

func TestW12gAttachDeadlines(t *testing.T) {
	h, _ := nwSetup(t)
	need := 2*(resolveTimeout+time.Second) + 2*nginxReloadWait + nginxDisconnectWait
	if got := h.nm().networkAttach().Spec.ApplyTimeout; need > got {
		t.Errorf("two lookup passes, two reloads and a disconnect take up to %v, more than the attach's %v", need, got)
	}
	if got := h.nm().networkDetach().Spec.ApplyTimeout; need > got {
		t.Errorf("two lookup passes, two reloads and a disconnect take up to %v, more than the detach's %v", need, got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/5 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/5 timeout 600 go test -count=1 -run 'TestW12g' ./internal/modules/npm/`
Expected: FAIL to compile with `h.nginx undefined`, then, once `thNginx` is wired, FAIL on the order and refusal assertions.

- [ ] **Step 3: Implement**

1. `deps.go`: `reload func(ctx context.Context) error`, set in `productionNM` to `newNginxReloader(<the Engine client>).Reload`; the production Engine client must satisfy `execEngine`.
2. `nginx_reload.go`: delete the listing-and-update implementation and its comment; `func (n nm) reloadNginx(ctx context.Context) error { return n.d.reload(ctx) }` and `reloadRefusal` of Contracts, Refusal of a failed reload. Add the two `where` strings to `network_claims.go` constants.
3. `network_claims.go`, `Apply` of attach: `check` no longer runs the forward guard when called from `Apply` (add a `guard bool` parameter: `Plan` passes true, `Apply` false). In `Apply`, with `ck.connect`, and outside the `h.n.run` closure so that a refusal journals no effect: `reloadBeforeJoin` (refuse through `reloadRefusal`, Definite, nothing changed), then re-inspect the network, read `forwardTargets` once and run `forwardTaken` against the fresh network (this is the guard and the re-read of spec D1 in one step), then connect, then `afterJoin(ctx, env, network, targets)`.
4. `afterJoin(ctx, env, network, targets)`: inspect NPM at once and compare the addresses it took on `network` with `targets` (literal and the name answers already read) with no listing and no lookup; a hit, or a joined NPM with no endpoint on the network, disconnects NPM (`disconnectAgain`) and refuses. Only then reload (`reloadNginx`), read `forwardTargets` afresh and compare again. A failed reload disconnects first and only then builds the refusal through `reloadRefusal` with `; it was disconnected again`. A connect error that is not a refusal inspects NPM under `context.WithoutCancel` and, when NPM is on the network, runs the same `afterJoin` before returning the original error. Keep the existing rules about a failed disconnect (`networkAttach` returns a failure that is neither Definite nor 4xx).
5. The undo of a detach (`networkUndoDisconnect`) takes the same order: reload, re-inspect and guard on fresh targets, connect, `afterJoin`. NPM already on the network and with no claim on it runs `afterJoin` once, which disconnects before any reload on a hit and reloads once otherwise (a network with a claim stays a claim-only change and runs nothing); on `darkpanel-npm` a hit never disconnects and fails with `npm.reserved`. Attach `Apply` with `ep != nil` and no claims takes the same `afterJoin` path (spec D7 (a)); with a claim it only adds the claim.
6. Fix the stale comments: `forwardTargets`' ("every join of NPM reloads nginx before and after it, so nginx holds no older answer") and the `networkUndoDisconnect` doc now describe the exec reload and the check order; `network_claims_test.go`'s comment that says a repeated undo is done "whatever proxy hosts forward to".
7. Migrate the tests (Contracts, Reload in the module): in `w12e_attach_test.go` delete `w12eReloads` and use `h.nginx.reloads`; replace the three `h.npm.SetNginxOnline(false, …)` with `h.nginx.fail(errNginxTest)`; the subtest "NPM's API does not answer the second reload" becomes `h.nginx.fail(errors.New("boom"))` expecting `errcode.NPMUnhealthy` with a detail beginning `nginx could not be reloaded after darkpanel-npm joined network appnet: `; the subtest "no enabled proxy host" now expects 2 reloads; `TestW12eAttachDeadlines` and `TestW12eDetachDeadline` are replaced by `TestW12gAttachDeadlines`, delete them. Fix the three references in `w12e_security_residuals_acceptance_test.go` the same way (`rg -n "w12eReloads|SetNginxOnline" internal/modules/npm`).

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/5 timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/5 timeout 900 go test -count=1 ./internal/modules/cloudflare/ ./internal/modules/panel/ && rm -rf /root/.cache/w12g-tmp/5` (the composites that attach NPM still complete and unwind, spec A20; `rg -n 'unwind|Unwind' internal/modules/cloudflare internal/modules/panel internal/modules/npm/composite_deploy_expose_test.go` lists the existing unwind tests that run here).

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm
git commit -m "fix(npm): reload nginx by exec and check an attach before any lookup"
```

---

### Task 6: The upgrade and the undo of an upgrade refuse an unanswered static forward; report an unverified start

**Files:**
- Modify: `internal/modules/npm/instance_upgrade.go` (`forwardCheck`, `Apply`, `verifyStart`, `rollback`)
- Modify: `internal/modules/npm/upgrade_recover.go` (`undoForwardCheck`)
- Test: `internal/modules/npm/w12g_upgrade_test.go`

**Interfaces:**
- Consumes: `unansweredForwards`, `unansweredDetail` (Contracts, Guard side), `w12aUpSetup`, `urSetup`, `w12eDone`, `w12eNoAnswer`.
- Produces: the upgrade's refusal with code `npm.forward_unanswered`.

**Items:** 2026-11-06-v1-security-review#192, 2026-11-06-v1-security-review#198

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const w12gUnansweredDetail = "location /x of proxy host 1 forwards to gone.invalid, which has no answer"

func TestW12gUpgradeRefusesAnUnansweredStaticForward(t *testing.T) {
	ctx := context.Background()
	s := w12aUpSetup(t)
	w12aSeed(s.h, "app.example.net", 8080, w12gLoc("/x", "gone.invalid"))
	s.n.d.resolve = w12eNoAnswer
	_, err := s.n.instanceUpgrade().Handler.Plan(ctx, s.h.env(""), json.RawMessage(`{}`))
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("plan detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	_, err = s.apply()
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("apply detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	if len(s.effects()) != 0 || s.dock.counts["StopContainer"] != 0 || len(s.dock.created) != 0 {
		t.Error("a refused upgrade journaled an effect, stopped NPM or created a container")
	}
}

func TestW12gUpgradeAllowsAnUnansweredDefaultForward(t *testing.T) {
	s := w12aUpSetup(t)
	w12aSeed(s.h, "gone.invalid", 8080)
	s.n.d.resolve = w12eNoAnswer
	if _, err := s.apply(); err != nil {
		t.Fatalf("an unanswered default forward host refused the upgrade: %v", err)
	}
}

func TestW12gUndoOfAFinishedUpgradeRefusesAnUnansweredStaticForward(t *testing.T) {
	f := urSetup(t)
	f.replace(urTo)
	w12eDone(t, f, w12eAppNet(), nil)
	w12aSeed(f.h, "app.example.net", 8080, w12gLoc("/x", "gone.invalid"))
	f.n.d.resolve = w12eNoAnswer
	mark := f.mark()
	err := f.inverse(urResource)
	if coded := nwRefused(t, err, errcode.NPMForwardUnanswered); coded.Detail != w12gUnansweredDetail {
		t.Errorf("detail %q, want %q", coded.Detail, w12gUnansweredDetail)
	}
	if urCalled(f.callsSince(mark), "RemoveContainer") || f.phase() != "done" {
		t.Errorf("the refused undo removed a container or changed phase done to %q", f.phase())
	}
	if c, ok := f.h.docker.container(urNewID); !ok || !c.State.Running {
		t.Errorf("container %s found %v running %v; want the upgraded one running", urNewID, ok, c.State.Running)
	}
}

// w12gStartHook runs at before each StartContainer reaches the test Engine.
type w12gStartHook struct {
	*thDocker
	at func()
}

func (d w12gStartHook) StartContainer(ctx context.Context, id string) (bool, error) {
	d.at()
	return d.thDocker.StartContainer(ctx, id)
}

func TestW12gAnUnverifiedStartIsWrittenToTheRow(t *testing.T) {
	ctx := context.Background()
	f := urSetup(t)
	f.replace(urTo)
	recorded := recordedTarget{Where: "proxy host 1", Host: "up.invalid", Name: true, Addr: netip.MustParseAddr("203.0.113.9")}
	w12eDone(t, f, w12eAppNet(), &[]recordedTarget{recorded})
	var failing atomic.Bool
	f.n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		if failing.Load() {
			return nil, &net.DNSError{Err: "server misbehaving", Name: "up.invalid", IsTemporary: true}
		}
		return []netip.Addr{netip.MustParseAddr("203.0.113.9")}, nil
	}
	f.n.d.docker = w12gStartHook{f.h.docker, func() { failing.Store(true) }}
	err := f.inverse(urResource)
	var coded *errcode.Error
	if errors.As(err, &coded) && coded.Code == errcode.NPMReserved {
		t.Fatalf("an unverified start was reported as a hit: %v", err)
	}
	if f.phase() == "failed" {
		t.Error("an unverified start turned the undo into a failed rollback")
	}
	row, ok, lerr := loadUpgrade(ctx, f.h.db.Read(), urChange)
	if lerr != nil || !ok {
		t.Fatalf("the upgrade row: %v %v", ok, lerr)
	}
	if !strings.Contains(row.Result, "the started container's addresses were unverified") {
		t.Errorf("the row's result %q does not say the started container's addresses were unverified", row.Result)
	}
	if c, ok := f.h.docker.container(containerName); !ok || !c.State.Running {
		t.Error("NPM is not running after an unverified start")
	}
}
```

The rollback and the recovery take the same `verifyStart` call: add the two variants of `TestW12eStartVerifiesHeldAddresses` that drive `recoverOriginal` and `rollback` (its `recovery` and `rollback` rows, `w12e_upgrade_test.go:73-200`) with the same `w12gStartHook` and resolver, and assert the same result text and that the phase is not `failed`.

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/6 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/6 timeout 900 go test -count=1 -run 'TestW12gUpgrade|TestW12gUndoOf|TestW12gAnUnverified' ./internal/modules/npm/`
Expected: FAIL: the plan returns no refusal.

- [ ] **Step 3: Implement**

1. `forwardCheck` (plan, `Apply` before the pull and after it) and `undoForwardCheck` call `unansweredForwards` after their address checks pass and refuse, Definite, with `errcode.NPMForwardUnanswered` and `unansweredDetail`. The `Apply` refusal leaves no effect journaled and NPM never stopped, as the existing refusals do.
2. `verifyStart` writes `held.Unverified` to the upgrade row's result (`setUpgradePhase`'s result argument or the row's result column the rollback already uses) as `the started container's addresses were unverified: <cause>` and names it in the change's error only where the change fails anyway; the rollback, undo or recovery completes with NPM running. `npm.reconcile` (Task 8) verifies the running container at its next pass; say so in the comment above `verifyStart` in one line.
3. The comment above `forwardCheck` describes the new refusal in one sentence (why: nginx cannot load a static name with no answer).

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/6 timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && rm -rf /root/.cache/w12g-tmp/6`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm
git commit -m "fix(npm): refuse an upgrade nginx could not start and report unverified starts"
```

---

### Task 7: The reconcile action

**Files:**
- Create: `internal/modules/npm/reconcile.go` (the hits, the action)
- Modify: `internal/modules/npm/actions.go` (the action is listed with the module's actions)
- Modify: `internal/modules/npm/instance_upgrade.go`, `internal/modules/npm/network_claims.go` (one shared `heldAddresses(c dockerapi.ContainerJSON) map[string][]netip.Addr` replaces the copies of the loop that reads the addresses NPM holds)
- Modify: `docs/user/modules/npm.md` (the action)
- Test: `internal/modules/npm/reconcile_action_test.go`

**Interfaces:**
- Consumes: `forwardTargets` and `forwardRefs` (Task 4), `reloadNginx` (Task 5), `disconnectAgain`, `claims`, `nwSetup`, `nwConnect`, `nwStatic`, `nwClaim`, `w12aSeed`, `w12gAddr` (Task 5's test file).
- Produces: the action and the hits (Contracts, Reconcile): `ReconcileInput`, `Reconciled`, `DroppedNetwork`, `func (n nm) instanceReconcile() core.ActionDef`, `type reconcileHit struct{ Network, NetworkID string; Addr netip.Addr; Where string }`, `func (n nm) reconcileHits(ctx context.Context, env core.Env) ([]reconcileHit, error)`.

**Items:** 2026-11-06-v1-security-review#191

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12gHit puts NPM on appnet at 10.9.0.7 with a proxy host that forwards there, and a claim of the claimant.
func w12gHit(t *testing.T) (*thHost, dockerapi.Network) {
	t.Helper()
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	nwClaim(t, h, nwNet, nwClaimant, "c0", false)
	w12aSeed(h, w12gAddr, 3000)
	return h, user
}

func w12gReconcile(t *testing.T, h *thHost) (Reconciled, error) {
	t.Helper()
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	h.addChange("r1", "npm.instance.reconcile")
	raw, err := n.instanceReconcile().Handler.Apply(context.Background(), h.env("r1"), nwJSON(t, ReconcileInput{}))
	var out Reconciled
	if err == nil {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
	}
	return out, err
}

func TestW12gReconcileDisconnectsNPMFromAForwardedAddress(t *testing.T) {
	h, user := w12gHit(t)
	out, err := w12gReconcile(t, h)
	if err != nil {
		t.Fatal(err)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("NPM stayed on a network where it holds a forwarded address")
	}
	if h.nginx.reloads != 1 {
		t.Errorf("%d reloads, want 1 after the disconnect", h.nginx.reloads)
	}
	if got := len(nwClaims(t, h, nwNet)); got != 1 {
		t.Errorf("%d claims left, want the claim kept", got)
	}
	if len(out.Dropped) != 1 || out.Dropped[0].Network != nwNet || out.Dropped[0].Address != w12gAddr ||
		len(out.Dropped[0].Claimants) != 1 || out.Dropped[0].Claimants[0] != nwClaimant {
		t.Errorf("dropped %+v, want %s at %s with claimant %s", out.Dropped, nwNet, w12gAddr, nwClaimant)
	}
}

func TestW12gReconcileApplyFindsNoHitAndChangesNothing(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	w12aSeed(h, "10.9.0.40", 3000)
	out, err := w12gReconcile(t, h)
	if err != nil || len(out.Dropped) != 0 {
		t.Fatalf("an apply with no hit returned %+v and %v", out, err)
	}
	if nwEndpoint(t, h, user.ID) == nil || h.nginx.reloads != 0 {
		t.Error("an apply with no hit disconnected NPM or reloaded nginx")
	}
}

func TestW12gReconcileFailedReloadLeavesNPMDisconnected(t *testing.T) {
	h, user := w12gHit(t)
	h.nginx.fail(errors.New("boom"))
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
	}
	if want := "nginx could not be reloaded after darkpanel-npm left network appnet: boom"; coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
	if nwEndpoint(t, h, user.ID) != nil {
		t.Error("a failed reload put NPM back on the network")
	}
}

func TestW12gReconcileAHitOnNPMsOwnNetworkLeavesNPMRunning(t *testing.T) {
	h, _ := nwSetup(t)
	own, ok := h.docker.network(networkName)
	if !ok {
		t.Fatal("NPM's own network is missing")
	}
	ep := nwEndpoint(t, h, own.ID)
	if ep == nil {
		t.Fatal("NPM is not on its own network")
	}
	w12aSeed(h, ep.IPAddress.String(), 3000)
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMReserved {
		t.Fatalf("error %v, want %s", err, errcode.NPMReserved)
	}
	if nwEndpoint(t, h, own.ID) == nil {
		t.Error("NPM was taken off its own network")
	}
	if c, ok := h.docker.container(containerName); !ok || !c.State.Running {
		t.Error("NPM was stopped")
	}
}

func TestW12gReconcilePlanListsTheHitsAndHashesThemNotTheTargets(t *testing.T) {
	ctx := context.Background()
	h, _ := w12gHit(t)
	w12aSeed(h, "rot.invalid", 80)
	var mu sync.Mutex
	answer := "203.0.113.9"
	n := h.nm()
	n.d.resolve = func(context.Context, string) ([]netip.Addr, error) {
		mu.Lock()
		defer mu.Unlock()
		return []netip.Addr{netip.MustParseAddr(answer)}, nil
	}
	plan := func() core.PlanOutcome {
		out, err := n.instanceReconcile().Handler.Plan(ctx, h.env(""), nwJSON(t, ReconcileInput{}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := plan()
	mu.Lock()
	answer = "203.0.113.10"
	mu.Unlock()
	second := plan()
	if first.StateHash == "" || first.StateHash != second.StateHash {
		t.Errorf("the state hash changed from %q to %q when only an unrelated name's answer changed", first.StateHash, second.StateHash)
	}
	found := false
	for _, l := range first.Diff {
		found = found || strings.Contains(l.Text, "container darkpanel-npm leaves network "+nwNet)
	}
	if !found {
		t.Errorf("diff %+v does not say NPM leaves %s", first.Diff, nwNet)
	}
	h2, user2 := nwSetup(t)
	w12aSubnet(t, h2, user2)
	nwConnect(t, h2, user2, nwStatic(w12gAddr))
	out, err := h2.nm().instanceReconcile().Handler.Plan(ctx, h2.env(""), nwJSON(t, ReconcileInput{}))
	if err != nil || len(out.Diff) != 0 {
		t.Errorf("a plan with no hit gave diff %+v and %v, want an empty diff", out.Diff, err)
	}
}

func TestW12gReconcileFailedDisconnectStopsNPM(t *testing.T) {
	h, _ := w12gHit(t)
	h.docker.Fail("DisconnectNetwork", errors.New("engine down"))
	_, err := w12gReconcile(t, h)
	if err == nil || core.IsDefinite(err) {
		t.Fatalf("error %v, want a failure that is not definite", err)
	}
	if c, ok := h.docker.container(containerName); !ok || c.State.Running {
		t.Error("NPM kept running with a forwarded address it could not leave")
	}
}

func TestW12gReconcileFailedReloadNamesTheUnansweredForwards(t *testing.T) {
	h, _ := w12gHit(t)
	w12aSeed(h, "def.invalid", 80, w12gLoc("/a", "gone.invalid"))
	h.nginx.fail(errNginxTest)
	_, err := w12gReconcile(t, h)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NPMUnhealthy {
		t.Fatalf("error %v, want %s", err, errcode.NPMUnhealthy)
	}
	want := "nginx could not be reloaded after darkpanel-npm left network appnet: nginx's configuration test failed; location /a of proxy host 2 forwards to gone.invalid, which has no answer"
	if coded.Detail != want {
		t.Errorf("detail %q, want %q", coded.Detail, want)
	}
}

func TestW12gReconcileVerifyRequiresNoForwardedAddress(t *testing.T) {
	ctx := context.Background()
	h, _ := w12gHit(t)
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	h.addChange("r1", "npm.instance.reconcile")
	if err := n.instanceReconcile().Handler.Verify(ctx, h.env("r1"), nwJSON(t, ReconcileInput{}), nwJSON(t, Reconciled{})); err == nil {
		t.Error("Verify passed while NPM holds a forwarded address")
	}
	if _, err := w12gReconcile(t, h); err != nil {
		t.Fatal(err)
	}
	if err := n.instanceReconcile().Handler.Verify(ctx, h.env("r1"), nwJSON(t, ReconcileInput{}), nwJSON(t, Reconciled{})); err != nil {
		t.Errorf("Verify after the disconnect: %v", err)
	}
}

func TestW12gReconcileActionDeclaration(t *testing.T) {
	h, _ := nwSetup(t)
	spec := h.nm().instanceReconcile().Spec
	if spec.Name != "npm.instance.reconcile" || spec.Class != core.ClassCompensable || !spec.ForwardOnly || !spec.Converges ||
		spec.MinScope != core.ScopeAdmin || spec.ApplyTimeout != 45*time.Second || len(spec.LockKeys) != 1 || spec.LockKeys[0] != "npm" {
		t.Errorf("action spec %+v, want npm.instance.reconcile, compensable, forward-only, converging, admin, 45s, the npm key", spec)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/7 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/7 timeout 900 go test -count=1 -run 'TestW12gReconcile' ./internal/modules/npm/`
Expected: FAIL to compile with `n.instanceReconcile undefined`.

- [ ] **Step 3: Implement**

1. `reconcile.go`: `ReconcileInput struct{}`, `Reconciled struct{ Dropped []DroppedNetwork \`json:"dropped"\` }`, `DroppedNetwork struct{ Network string \`json:"network"\`; Address string \`json:"address"\`; Claimants []string \`json:"claimants"\` }`, `reconcileHit`.
2. `reconcileHits`: inspect `darkpanel-npm` (missing or not running: no hits, nil); no instance row: no hits; read `forwardTargets` (enabled hosts); compare the addresses NPM holds on every network (`heldAddresses`) with the targets, literal addresses first and then names; when the lookups in `forwardTargets` fail, compare only the literal forwards (read through `forwardRefs`) and log the rest at warn level through `env.Log`.
3. `instanceReconcile()`: `core.DefineMutating[ReconcileInput, Reconciled](core.ActionSpec{Name: "npm.instance.reconcile", Summary: "Take NPM off a network where it holds an address a proxy host forwards to.", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey}, ForwardOnly: true, Converges: true, MinScope: core.ScopeAdmin, ApplyTimeout: 45 * time.Second}, handler)`, as `panel.listener.revoke` declares its own (`internal/modules/panel/listener_revoke.go:19-25`, without its `BreakGlass` and controller side). `Plan` finds the hits and returns a diff line per network (`container darkpanel-npm leaves network <n>`; none when there is no hit) and a state hash over the hits (network, address, NPM's endpoint id). `Apply` finds the hits again under the lock; none returns an empty `Reconciled`; for each hit on a network other than `darkpanel-npm` it disconnects NPM (`disconnectAgain`, journaled as the detach journals its disconnect) and collects the claimants through `claims`; a hit whose disconnect fails stops NPM as `verifyHeld` does for a hit it cannot drop (`instance_upgrade.go:499-504`); a hit on `darkpanel-npm` fails with `npm.reserved` and leaves NPM running; then `reloadNginx`, and a failure returns an `npm.unhealthy` error (for every failure, `errNginxTest` included; `reloadRefusal` is not used) with the detail `nginx could not be reloaded after darkpanel-npm left network <n>[, <n>]: <err>`, followed by `; ` and `unansweredDetail` when the failure is `errNginxTest` and `unansweredForwards` lists any (a failed listing adds nothing), NPM staying disconnected. `Verify` inspects NPM and requires that it holds no forwarded address on any network; call the handler's `Verify` in the test the way `network_claims_test.go` calls the attach's (`rg -n 'Handler.Verify' internal/modules/npm/network_claims_test.go`) and adapt the call's argument types to the erased handler.
4. `actions.go`: list the action. `heldAddresses` replaces the loops in `joinedProblem` and `verifyHeld`.
5. `docs/user/modules/npm.md`: one table row for `npm.instance.reconcile` (columns as the neighbouring rows; the CLI cell as they write a command with none) and two sentences under "What it changes on the host": the agent takes NPM off a network where it holds an address a proxy host forwards to, and the operator attaches NPM again once the forward is fixed. The page's tests (`internal/docs`) forbid the words `register row`, `wave <n>`, `task <n>` and `plan <n>`.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/7 timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/7 timeout 600 go test -count=1 ./internal/docs/ ./internal/core/... && rm -rf /root/.cache/w12g-tmp/7`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm docs/user/modules/npm.md
git commit -m "feat(npm): add the action that takes NPM off a forwarded address"
```

---

### Task 8: The reconcile task

**Files:**
- Modify: `internal/modules/npm/reconcile.go` (`reconcileTask`, `Run`, `Watch`)
- Modify: `internal/modules/npm/npm.go` (`BackgroundTasks`)
- Modify: `internal/modules/npm/deps.go` (`dockerAPI.Events`, `dockerEvents`, the production wrapper that returns a nil stream with its error, as `internal/modules/firewall/wire.go:89-92` does)
- Modify: `internal/modules/npm/testhost_test.go` (`thDocker.events`, `thDocker.Events`)
- Test: `internal/modules/npm/reconcile_task_test.go`

**Interfaces:**
- Consumes: `reconcileHits`, `instanceReconcile`, `Reconciled` (Task 7), `w12gHit`, `w12gAddr`, `nwSetup`, `nwConnect`, `nwStatic`, `w12aSeed`.
- Produces: the task (Contracts, Reconcile): `func (n nm) reconcileTask() core.BackgroundTask` and `func (m module) BackgroundTasks() []core.BackgroundTask`.

**Items:** 2026-11-06-v1-security-review#191

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

```go
package npm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// w12gTask runs one pass of the reconcile task against h. Apply runs the action's own handler, as the engine would, and
// counts the calls.
func w12gTask(t *testing.T, h *thHost, task core.BackgroundTask, n nm, applies *int) error {
	t.Helper()
	h.addChange("r1", "npm.instance.reconcile")
	env := h.env("r1")
	tc := core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		*applies++
		if action != "npm.instance.reconcile" {
			t.Errorf("the task applied %q, want npm.instance.reconcile", action)
		}
		_, err := n.instanceReconcile().Handler.Apply(ctx, env, nwJSON(t, input))
		return core.ChangeView{}, err
	}}
	return task.Run(context.Background(), tc)
}

func w12gNM(h *thHost) nm {
	n := h.nm()
	n.d.resolve = w12eNoAnswer
	return n
}

func TestW12gReconcilePassAppliesOnAHit(t *testing.T) {
	h, user := w12gHit(t)
	n := w12gNM(h)
	applies := 0
	if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil {
		t.Fatal(err)
	}
	if applies != 1 || nwEndpoint(t, h, user.ID) != nil {
		t.Errorf("the pass applied %d times and NPM is on the network %v; want once and no", applies, nwEndpoint(t, h, user.ID) != nil)
	}
}

func TestW12gReconcileAPassWithNoHitAppliesNothing(t *testing.T) {
	h, user := nwSetup(t)
	w12aSubnet(t, h, user)
	nwConnect(t, h, user, nwStatic(w12gAddr))
	w12aSeed(h, "10.9.0.40", 3000)
	n := w12gNM(h)
	applies := 0
	if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
		t.Errorf("a pass with no hit applied %d times with error %v, want none", applies, err)
	}
}

func TestW12gReconcileSkips(t *testing.T) {
	t.Run("no instance", func(t *testing.T) {
		h := newTHHost(t)
		n := w12gNM(h)
		applies := 0
		if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v, want none", applies, err)
		}
	})
	t.Run("NPM is not running", func(t *testing.T) {
		h, _ := w12gHit(t)
		if _, err := h.docker.StopContainer(context.Background(), containerName, time.Second); err != nil {
			t.Fatal(err)
		}
		n := w12gNM(h)
		applies := 0
		if err := w12gTask(t, h, n.reconcileTask(), n, &applies); err != nil || applies != 0 {
			t.Errorf("applied %d times with error %v, want none", applies, err)
		}
	})
}

func TestW12gReconcileReportsTheSameHitOnNPMsOwnNetworkOnce(t *testing.T) {
	h, _ := nwSetup(t)
	own, _ := h.docker.network(networkName)
	ep := nwEndpoint(t, h, own.ID)
	if ep == nil {
		t.Fatal("NPM is not on its own network")
	}
	w12aSeed(h, ep.IPAddress.String(), 3000)
	n := w12gNM(h)
	task := n.reconcileTask()
	applies := 0
	for range 3 {
		err := w12gTask(t, h, task, n, &applies)
		var coded *errcode.Error
		if err != nil && !(errors.As(err, &coded) && coded.Code == errcode.NPMReserved) {
			t.Fatalf("pass error %v", err)
		}
	}
	if applies != 1 {
		t.Errorf("the same hit was applied %d times, want once", applies)
	}
}

func TestW12gReconcileTaskDeclaration(t *testing.T) {
	h, _ := nwSetup(t)
	task := h.nm().reconcileTask()
	if task.ID != "npm.reconcile" || task.Every != 5*time.Minute || len(task.LockKeys) != 1 || task.LockKeys[0] != "npm" || task.Watch == nil {
		t.Errorf("task %+v, want npm.reconcile every 5 minutes under the npm key with a watch", task)
	}
	tasks := module{n: h.nm()}.BackgroundTasks()
	if len(tasks) != 1 || tasks[0].ID != "npm.reconcile" {
		t.Errorf("the module's background tasks %+v, want npm.reconcile", tasks)
	}
}

func TestW12gReconcileWatchWakesOnStartAndConnect(t *testing.T) {
	h, _ := nwSetup(t)
	h.docker.events = make(chan dockerapi.Event, 8)
	n := h.nm()
	npmC, _ := h.docker.container(containerName)
	npmID := npmC.ID
	var wakes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := n.reconcileTask().Watch(ctx, h.env(""), func() { wakes.Add(1) }); err != nil {
			t.Errorf("watch: %v", err)
		}
	}()
	send := func(ev dockerapi.Event) { h.docker.events <- ev }
	send(dockerapi.Event{Type: "container", Action: "start", Actor: dockerapi.EventActor{ID: npmID, Attributes: map[string]string{"name": containerName}}})
	send(dockerapi.Event{Type: "container", Action: "start", Actor: dockerapi.EventActor{ID: "other", Attributes: map[string]string{"name": "web"}}})
	send(dockerapi.Event{Type: "network", Action: "connect", Actor: dockerapi.EventActor{ID: "n1", Attributes: map[string]string{"container": npmID}}})
	send(dockerapi.Event{Type: "network", Action: "connect", Actor: dockerapi.EventActor{ID: "n1", Attributes: map[string]string{"container": "other"}}})
	deadline := time.Now().Add(2 * time.Second)
	for wakes.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	cancel()
	wg.Wait()
	if got := wakes.Load(); got != 2 {
		t.Errorf("%d wakes, want 2 (NPM's start and NPM's connect)", got)
	}
}
```

`dockerapi.EventActor` stands for the Engine client's event actor type: read its real name in `internal/platform/dockerapi/streams.go` and use it. `thDocker` gets `events chan dockerapi.Event`; its `Events(ctx, opts)` returns a stream whose `Next` returns what arrives on `events`, and `ctx.Err()` once the context is done.

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/8 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/8 timeout 900 go test -count=1 -run 'TestW12gReconcile' ./internal/modules/npm/`
Expected: FAIL to compile with `n.reconcileTask undefined`.

- [ ] **Step 3: Implement**

1. `reconcile.go`: constants `reconcileID = "npm.reconcile"` and `reconcileEvery = 5 * time.Minute`; `n.reconcileTask()` returns the `core.BackgroundTask` of Contracts, Reconcile. `Run`: `reconcileHits`; none clears the remembered key and returns nil; otherwise build a key from the hits (network, address, where); a key equal to the remembered one, whose last apply failed, does not apply again; else `tc.Apply(ctx, "npm.instance.reconcile", ReconcileInput{})` and remember the key. A failed listing returns the error (the runner backs off). The remembered key lives in the task closure (memory only, so it repeats after an agent restart).
2. `Watch`: `n.d.docker.Events(ctx, EventsOptions{Filters: {"type": {"container", "network"}, "event": {"start", "connect"}}})`, and `wake()` for a container `start` whose actor id or `name` attribute is `darkpanel-npm` or NPM's container id, and for a network `connect` whose `container` attribute is `darkpanel-npm` or equals the id `InspectContainer(containerName)` answers when the event arrives (the id changes after an upgrade, so it is not cached); on a stream error return nil when the context ended, else `fmt.Errorf("npm: reconcile: watch Docker events: %w", err)`, as `firewall.reconcileWatch` does (`internal/modules/firewall/reconcile.go:162-183`).
3. `npm.go`: `func (m module) BackgroundTasks() []core.BackgroundTask { return []core.BackgroundTask{m.n.reconcileTask()} }`. `deps.go`: `Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error)` on `dockerAPI`, `type dockerEvents interface{ Next() (dockerapi.Event, error); Close() error }`, and the production Engine wrapper.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/8 timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ ./internal/agent/... && rm -rf /root/.cache/w12g-tmp/8`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm
git commit -m "feat(npm): add the reconcile task with a timer and a Docker-events watch"
```

---

### Task 9: Link guard, test hygiene and the harness

**Files:**
- Modify: `internal/modules/tailscale/linkguard.go` (the NPM exposure with no public binding)
- Modify: `internal/modules/firewall/guard/w12e_linklocal_test.go` (a row of `TestW12eNamesIPv6Range`)
- Modify: `internal/modules/tailscale/linkguard_test.go` (rows of `TestW12eTailnetClientOfAnExposure`)
- Modify: `internal/modules/firewall/guard/w12e_security_residuals_acceptance_test.go` (`qaSpan`)
- Modify: `test/harness/m6_npm_test.go` (`reloadFollowsName`, `reloadCall`, two new steps)
- Modify: `test/harness/vm/vm.go` only if Step 3 shows a command text still reaches a failure message with a secret (it should not after `reloadCall` goes)

**Interfaces:**
- Consumes: the reload and the reconcile of Tasks 5, 7 and 8 (live), `w12eNamesIPv6Range`, `m4ApplySettled`, `m3dDocker`, `m3Run`, `m6nRunCmd` and the neighbouring harness helpers.
- Produces: nothing a later task consumes.

**Items:** 2026-11-06-v1-security-review#195, 2026-11-06-v1-security-review#197, 2026-11-06-v1-security-review#199

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 3 - spec 2 - coupling 0 - risk 1 = 6

Rule S: `files + spec + coupling` is 5 here, below the 6 that splits a task; the harness part is the only coupled one and Step 3 below is verified by `go vet` only, so keep it in this task.

- [ ] **Step 1: Write the failing tests**

In `linkguard_test.go`, add this row to the table of `TestW12eTailnetClientOfAnExposure` (and the `want` text field it needs: add `wantIn: []string{"no public bindings"}`):

```go
		{"an NPM exposure with no public binding cannot be shown safe", map[string]string{"100.64.0.9": "tailscale0", "172.30.0.2": "dp-panel"},
			core.Guarded{Guard: &core.GuardContext{Complete: true,
				Client: &core.Peer{Addr: netip.MustParseAddr("100.64.0.9"), Port: 51000},
				Via: &core.ProxyPath{ExposureID: "e1", Kind: core.ProxyKindNPM, ProxyAddr: netip.MustParseAddr("172.30.0.2"),
					ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}},
			false, []string{"no public bindings"}},
```

In `w12e_linklocal_test.go`, make sure `TestW12eNamesIPv6Range`'s table (two fields, `msg` and `want`) has the row that proves `qaSpan`'s weakness; add if missing:

```go
		{"cut exposure exp1 from :-: to [fe80::1]:443", false},
```

In `w12e_security_residuals_acceptance_test.go` the assertion `qaSpan.MatchString(msg)` becomes `w12eNamesIPv6Range(msg)`; delete the `qaSpan` variable and the `regexp` import when nothing else uses it.

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w12g-tmp/9 && GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/9 timeout 600 go test -count=1 -run 'TestW12eTailnetClientOfAnExposure|TestW12eNamesIPv6Range' ./internal/modules/tailscale/ ./internal/modules/firewall/guard/`
Expected: FAIL: the new link-guard row finds `OK` true.

- [ ] **Step 3: Implement**

1. `linkguard.go`, in the NPM branch next to the client flow (the `if g.Via.Kind == core.ProxyKindNPM && g.Client != nil ...` block): when `len(g.Via.Public) == 0`, `add(linkRaw{label: "panel", unknown: "the exposure has no public bindings"})`, as `guard/flows.go:285-287` does.
2. The harness (`test/harness/m6_npm_test.go`, compiled by `go vet -tags harness ./test/...` here, run by CI):
   - Delete `reloadCall` and the call to it in `reloadFollowsName`, together with the `strings`, `bytes`, `json` and `npmapi` imports they alone use. Nothing in the harness sends a request to NPM's API with a token any more (spec A8).
   - In `reloadFollowsName`'s failure half, drop the `sudo docker exec … nginx -t` precondition check that quotes nginx's output only if it is the single use; keep it otherwise. The real `attach(nets[1])` is the proof: assert it is refused, that its `ch.Error.Code` is `npm.forward_unresolved` (the single-label `m6n-echo` gets no definite answer on the VMs) or `npm.forward_unanswered`, that the error detail names `m6n-echo`, and that `nets[1]` holds no `darkpanel-npm`.
   - New step `unansweredStaticForward` (called from the sequence that calls `reloadFollowsName`, after it): give a second helper container the dotted network alias `m6n-dotted.invalid` on `m6nAppNet` (`docker run --network-alias`, through `m6nRunCmd` or the neighbouring helper that accepts an alias), add a proxy host forwarding to `m6n-dotted.invalid:<port>` with `npm.proxyhost.add`, remove the helper container, and assert that (a) `npm.network.attach` of a fresh network is refused with `npm.forward_unanswered` and a detail naming `m6n-dotted.invalid`, with NPM on no member list of that network, and (b) `npm.instance.upgrade` is refused at plan with the same code. Restore (container and proxy host removed) in `t.Cleanup`. A dotted `.invalid` name gets a definite not-found from the VMs' resolver (RFC 6761); if the live run shows otherwise, the refusal is `npm.forward_unresolved` and the step records that as a failure of Assumptions.
   - New step `reconcileUndoesAStartDarkpanelDidNotMake`: on a fresh network `m6n-rec` created with `--subnet 10.77.0.0/24` (the Engine accepts `--ip` only on a network with a user-configured subnet), attach NPM through `npm.network.attach`; start a helper container on it and add a proxy host forwarding to its address; stop the helper; `docker network disconnect m6n-rec darkpanel-npm` and `docker network connect --ip <the helper's address> m6n-rec darkpanel-npm` (a connect Darkpanel does not make); assert with `eventually(…, 120*time.Second, …)` that `darkpanel-npm` is no longer a member of `m6n-rec` and that a change `npm.instance.reconcile` of actor `system:npm.reconcile` is completed; clean up.
   - New check in `reloadFollowsName`'s moved-name half: list `-printf '%u:%g %p\n'` of the nginx log and temp directories the evidence note of Task 1 names (`docker exec darkpanel-npm find <dirs> -printf …`) before the first attach and after it, and require the two outputs equal (spec A7). Use the PUID/PGID the note found.
3. `vm.go`: none expected. Verify with `rg -n "Bearer|Authorization" test/harness` that no match is left.

- [ ] **Step 4: Run test to verify it passes**

Run: the Step 2 command. Expected: PASS. Then `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/9 timeout 600 go vet -tags harness ./test/... ./cmd/... && rg -n "Bearer|Authorization" test/harness && echo FOUND || echo none; GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/9 timeout 900 go test -count=1 ./internal/modules/tailscale/ ./internal/modules/firewall/... && rm -rf /root/.cache/w12g-tmp/9`. Expected: the vet is clean and the `rg` prints `none`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/tailscale internal/modules/firewall/guard test/harness
git commit -m "fix(harness): drive the real attach and add the reconcile and unanswered-forward steps"
```

---

### Task 10: Threat model, user documentation and registers

**Files:**
- Create: `internal/modules/npm/w12g_acceptance_test.go`
- Modify: `internal/modules/npm/w12e_security_residuals_acceptance_test.go` (the parser loop of `TestA12_EveryNamedRegressionTestExists` moves into `requireTestDeclared`)
- Modify: `internal/docs/modules_b_test.go` (the facts of the `npm` page)
- Modify: `docs/dforge/notes/w12g-evidence.md` (the `## Revert check` section)
- Modify: `docs/security/threat-model.md`
- Modify: `docs/user/modules/npm.md`
- Modify: `docs/dforge/registers/2026-11-06-v1-security-review.md`
- Modify: `docs/dforge/registers/2026-10-21-v1-completion.md` (row 37, R73, if its note names these rows)

**Interfaces:**
- Consumes: everything above.
- Produces: the records the panel re-review and the M3 audit read.

**Items:** 2026-11-06-v1-security-review#1, 2026-11-06-v1-security-review#176, 2026-11-06-v1-security-review#188, 2026-11-06-v1-security-review#190, 2026-11-06-v1-security-review#191, 2026-11-06-v1-security-review#192, 2026-11-06-v1-security-review#193, 2026-11-06-v1-security-review#194, 2026-11-06-v1-security-review#195, 2026-11-06-v1-security-review#196, 2026-11-06-v1-security-review#197, 2026-11-06-v1-security-review#198, 2026-11-06-v1-security-review#199

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 3 - spec 1 - coupling 0 - risk 1 = 5

- [ ] **Step 1: Write the failing test**

`internal/modules/npm/w12g_acceptance_test.go`, in the form of `TestA12_EveryNamedRegressionTestExists` in `w12e_security_residuals_acceptance_test.go` (read it first and copy its parser and its helper; it checks declarations only, and its name and comment say so):

```go
package npm

import "testing"

// TestA23_EveryFixHasItsNamedRegressionTestDeclared checks that the tests the register rows cite exist. Whether each
// fails when its fix is reverted is recorded in docs/dforge/notes/w12g-evidence.md, section Revert check.
func TestA23_EveryFixHasItsNamedRegressionTestDeclared(t *testing.T) {
	for _, name := range []string{
		"TestW12gAttachMakesNoCallBetweenConnectAndInspection", // rows 1, 176, 190
		"TestW12gAttachDisconnectsBeforeAnyLookupOrReload",     // rows 1, 176, 190
		"TestW12gAttachRereadsTheNetworkAfterTheFirstReload",   // row 190
		"TestNginxReloaderConfirmsANewWorker",                  // row 188
		"TestW12gReloadRefusals",                               // rows 188, 198
		"TestW12gReconcileDisconnectsNPMFromAForwardedAddress", // row 191
		"TestW12gUpgradeRefusesAnUnansweredStaticForward",      // row 192
		"TestW12gUnansweredForwards",                           // rows 192, 193
	} {
		requireTestDeclared(t, name)
	}
}
```

`TestA12_EveryNamedRegressionTestExists` holds its parser inline (`w12e_security_residuals_acceptance_test.go:1047-1064`); extract that loop into `func requireTestDeclared(t *testing.T, name string)` in the same file, call it from A12 and from this test, and keep A12's behaviour unchanged.

- [ ] **Step 2: Run the revert check and correct the threat model**

Revert check, for each of the eight tests of Step 1: temporarily undo only the fix it pins (the reorder in `afterJoin` for the first two, the re-read in `Apply` for the third, the confirmation of a new worker in `Reload` for the fourth, the `reloadRefusal` classification for the fifth, the disconnect in `Apply` of the reconcile action for the sixth, the `unansweredForwards` call in `forwardCheck` for the seventh, the `Enabled` filter of `forwardRefs` for the eighth), run the test (`timeout 900 go test -count=1 -run '<name>$' ./internal/modules/npm/`), record the failing line in the `## Revert check` section of `docs/dforge/notes/w12g-evidence.md`, and restore the fix with `git checkout -- <file>`. Never commit a reverted fix; confirm `git diff --stat` shows only the note before Step 5.

Threat model:

In `docs/security/threat-model.md` change, and each time keep the sentence's other content:
1. The reload paragraph (the one that says each reload is confirmed by `meta.nginx_online`): nginx is reloaded by `docker exec` in `darkpanel-npm` (`nginx -t -g "error_log off;"`, then `nginx -s reload`), confirmed by exit codes and by a new nginx worker in the Engine's process listing; nothing is written to NPM, no proxy host is renamed or overwritten, the output of `nginx -t` is never read. State that the test of an unanswered static name fails and the refusal names it (`npm.forward_unanswered`).
2. The residual-risk lines that describe the update path: the re-sent proxy host body (UI edit overwritten) and the `.err` rename are deleted, the failed-test line says a custom location whose static name stops answering makes every join and every upgrade refuse, and that the upgrade and its undo now refuse up front.
3. The starts-Darkpanel-does-not-make line: `npm.reconcile` checks at agent start, every 5 minutes and on NPM's start or connect events; list the limits of spec D3 (API start, a pass skipped for a held lock, a change in `needs_repair`, a re-pointed name acting unattended, the epoch of the last npm change, 288 logins a day) and add the sentence marked as the owner's: "The owner accepted this at the spec review of 2026-11-15 (U3)."
4. The literal-address sentence ("no caller can stretch it"): for an attach it holds the exposure to the interval from the connect until the disconnect that follows the inspection (at most 10 seconds, repeatable by retrying) and add: "The owner accepted this at the spec review of 2026-11-15 (U2)." Add that a disabled proxy host that an NPM administrator enables is caught by the next reconcile pass, at most 5 minutes later.
5. The sentence that says a proxy-host update against a running NPM of the pinned image was not run: the harness ran the attach and the exec reload live (`docs/dforge/notes/w12e-evidence.md`, `docs/dforge/notes/w12g-evidence.md`); keep the sentence only for the Engine disconnect of `darkpanel-npm` if it still was not run.
6. The line about 5xx details: an unresolved forward name now refuses with `npm.forward_unresolved` (409) and keeps its detail; the listing failure still shows a 5xx code's title only.

- [ ] **Step 3: Update the user page and the registers**

In `internal/docs/modules_b_test.go` add `"npm.forward_unanswered"` and `"npm.forward_unresolved"` to the `npm` page's facts. In `docs/user/modules/npm.md`: the reload now runs inside NPM's container and writes nothing to NPM; the upgrade and its undo refuse while an enabled custom location forwards to a name with no answer (`npm.forward_unanswered`); a name that cannot be resolved refuses with `npm.forward_unresolved` and says which proxy host. No word of the page tests' forbidden list.

In the security register, for each of rows 1, 176, 188 and 190-199:
```bash
bash /root/.claude-alt2/plugins/cache/darkraise/dforge/4.17.1/scripts/register set docs/dforge/registers/2026-11-06-v1-security-review.md <id> verify --assigned docs/dforge/plans/2026-11-15-v1-w12g-security-followup.md --note "w12g <sha7 of the last task commit>: <one line of what changed and the test that pins it>"
```
Rows 1, 176, 188 and 190 add `; owes the panel re-review` to the note. Keep each row's Acceptance cell as it is, and add to rows 1, 176 and 190 the clause `the panel re-review closes it, or the owner accepts the interval of spec D1 (U2)`.

- [ ] **Step 4: Verify**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w12g-tmp/10 timeout 600 go test -count=1 ./internal/docs/ && bash /root/.claude-alt2/plugins/cache/darkraise/dforge/4.17.1/scripts/register check docs/dforge/registers/2026-11-06-v1-security-review.md && rg -in "register row|wave [0-9]|task [0-9]|plan [0-9]|W[0-9]+-R[0-9]+" docs/user/modules/npm.md` (the last command must print nothing).
Expected: PASS, `register: 0 errors`, no output from `rg`.

- [ ] **Step 5: Commit**

```bash
git add docs internal/modules/npm/w12g_acceptance_test.go
git commit -m "docs: record the w12g fixes in the threat model, the user page and the registers"
```

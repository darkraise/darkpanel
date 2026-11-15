# v1 w12g security follow-up Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the residuals of the 2026-11-15 panel re-review (security register rows 1, 176, 188, 190-199): nginx is reloaded by a confirmed `docker exec` instead of a proxy-host update, the attach and the undo of a detach disconnect NPM before any lookup or listing can stretch the exposure, NPM's addresses are verified at agent start and on a timer, the upgrade refuses when nginx could not start, and the smaller defects, the Tailscale link guard, the harness and the threat model follow.

**Architecture:** Task 1 reads the pinned image and the Engine API for what the exec reload assumes. Tasks 2-3 add the Engine's exec and process-listing calls and the reloader built on them. Task 4 adds the two error codes and the guard-side changes (enabled hosts only, unanswered static forwards, the unresolved-name refusal). Task 5 wires the reloader into the module and reorders the attach and the undo of a detach. Task 6 gives the upgrade and the undo of an upgrade the unanswered-forward refusal and reports unverified starts. Task 7 adds the `npm.reconcile` task and its action. Task 8 holds the link guard, the test hygiene and the harness. Task 9 brings the threat model, the user documentation and the registers.

**Tech Stack:** Go 1.27.4, Docker Engine API (exec, top, events), Markdown.

**Spec:** docs/dforge/specs/2026-11-15-v1-w12g-security-followup-design.md

**Execution:** inline — `claude --model opus --effort high` — the hardest self-implemented task totals 8 at risk 3

**Parallelism:** sequential — inline execution

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w12g-tmp/integ && export TMPDIR=/root/.cache/w12g-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... ./cmd/... && timeout 1500 go test -timeout 20m -count=1 ./... && timeout 600 go test -race -timeout 10m -count=1 ./internal/core/lifecycle/ && timeout 1500 go test -race -timeout 24m -count=1 ./internal/modules/npm/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`

**CI:** `.github/workflows/ci.yml .github/workflows/fuzz.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

## Global Constraints

- Scope is the rows this plan cites: security register (`docs/dforge/registers/2026-11-06-v1-security-review.md`) rows 1, 176, 188, 190-199. Out of scope (spec D10): the old workers' residual (w12e D1 (a)), the DNS re-pointing risk (w12e D7), a static-address join, a lock key on the attach, and an automatic re-attach after a reconcile.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd; runs `systemctl`, `docker`, `ufw`, `iptables`, `nft` or `nmap`; binds an address other than `127.0.0.1` (port 0); or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/etc/ufw`, `/run/darkpanel*`, or `/proc` of another process. The live proof of the exec reload, the reconcile and the unanswered-forward refusal is the harness step of Task 8, which runs on CI's VMs after the merge (gate 7), never here. Task 1 fetches the registry and the Engine documentation over HTTPS into the scratchpad with `curl`, never through Docker.
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
- **Refusal of a failed reload (Task 5, consumed by Task 7).** `func (n nm) reloadRefusal(ctx context.Context, env core.Env, err error, where string) error`: for `errNginxTest`, `NPMForwardUnanswered` with `unansweredDetail` when the list is not empty (the listing is best effort; a failed listing falls through), else `NPMReserved` with `nginx could not be reloaded <where>: nginx's configuration test failed`; for any other error `NPMUnhealthy` with `nginx could not be reloaded <where>: <err>`. `where` is `before darkpanel-npm joins network <net>` or `after darkpanel-npm joined network <net>`. Both are `core.Definite` before a join; after a join the caller adds `; it was disconnected again`.
- **Reconcile (Task 7).** New file `internal/modules/npm/reconcile.go`. Action `npm.instance.reconcile`: class compensable, `ForwardOnly`, `Converges`, side agent, `MinScope` admin, `LockKeys` `["npm"]`, `ApplyTimeout` 45 s, empty input `ReconcileInput struct{}`, output `Reconciled struct{ Dropped []DroppedNetwork }` with `DroppedNetwork struct{ Network string; Address string; Claimants []string }`. Task `npm.reconcile` (`reconcileID`), `Every` 5 minutes (`reconcileEvery`), `LockKeys` `["npm"]`, `Watch` waking on a container `start` event of `darkpanel-npm` and a network `connect` event whose `Actor.Attributes["container"]` is its id or name. `dockerAPI` gains `Events(ctx context.Context, o dockerapi.EventsOptions) (dockerEvents, error)` with `type dockerEvents interface{ Next() (dockerapi.Event, error); Close() error }`, as the firewall declares them.

## Assumptions (evidence)

- The pinned NPM's reload is `nginx -t -g "error_log off;"` then `nginx -s reload` (`docs/dforge/notes/w12e-evidence.md` section 1, `/app/internal/nginx.js:108-121`), and a failed second test renames the regenerated host file to `.err` only on the update path (section 1), which the exec path does not take.
- The user the exec starts with, and whether `nginx -t` leaves files another user owns, are unverified — Task 1 reads the image for the user nginx runs as and the `PUID`/`PGID` defaults, and the harness step of Task 8 checks ownership live on the four systems (spec A7); the exec runs as the container's `PUID:PGID`, root when unset (spec D2).
- The Engine's exec and top answers have the shapes of Contracts (Docker Engine API v1.49, `docs.docker.com/reference/api/engine/version/v1.49/`: `ExecInspect` has `Running` and `ExitCode`, `ContainerTop` has `Titles` and `Processes`) — Task 1 reads the documentation and records it; Task 2's tests pin the client to those shapes.
- A new nginx worker appears after `nginx -s reload` is applied and a rejected reload keeps the old workers (nginx.org/en/docs/control.html, "reload": the master starts new workers and shuts the old ones down gracefully; on a failed configuration it keeps the old ones) — Task 1 quotes it.
- `lifecycle.ApplySystem` refuses an irreversible action from a background task (`internal/core/lifecycle/system.go:28-30`), so `npm.instance.reconcile` is declared as `panel.listener.revoke` is (`internal/modules/panel/listener_revoke.go:19-25`).
- `firewall.reconcile` is the model of a task with a timer and a Docker-events watch (`internal/modules/firewall/reconcile.go:17-34,162-183`), and the runner skips a pass while the keys are held (`internal/agent/runtime/background.go:202-206`).
- `forwardTargets`, `forwardAnswers`, `forwardGuard`, `forwardTaken`, `verifyHeld`, `dropNetwork` and `afterJoin` are at the lines the spec names (read 2026-11-15 at 6c41959f).
- A dotted `.invalid` name gets a definite not-found from the harness VMs' resolver, where a single label did not (`docs/dforge/notes/w12e-evidence.md:324-326`) — unverified — Task 8's harness step uses it and gate 7 proves it.
- The test Engine assigns an address only when it is static or pinned (`internal/modules/npm/testhost_test.go:321`, `pin`), so a test that needs NPM to take a freed address pins it.
- The module's tests count reloads through `w12eReloads`, which counts proxy-host updates (`internal/modules/npm/w12e_attach_test.go:42-50`); Task 5 rewrites it to count `h.nginx.reloads`.

## Task index

1. Evidence: the exec reload on the pinned image and the Engine's exec and top API
2. Engine client: exec and process listing
3. The nginx reloader
4. Codes, enabled hosts only, unanswered static forwards and the unresolved-name refusal
5. Reload by exec in the module, and the attach's and the undo's check order
6. The upgrade and the undo of an upgrade refuse an unanswered static forward; report an unverified start
7. `npm.reconcile`
8. Link guard, test hygiene and the harness
9. Threat model, user documentation and registers

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

State in the note, in two sentences each, what the harness step of Task 8 must prove live that this reading cannot: the exec's user can run `nginx -t` and signal the master, no file under nginx's log or temp directories changes owner, `ContainerTop` lists the workers on all four systems.

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
3. Add the three exec endpoints and the top endpoint to the `live_test.go` list of calls only if it lists endpoints; otherwise leave it (the live proof is Task 8).

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
	users      []string
	containers []string
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
	return dockerapi.ContainerJSON{Config: dockerapi.ContainerConfig{Env: f.env}}, nil
}

func (f *fakeExec) ExecCreate(_ context.Context, container string, o dockerapi.ExecCreate) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	line := strings.Join(o.Cmd, " ")
	id := "x" + line
	f.created = append(f.created, line)
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
	if got := strings.Join(f.created, "|"); got != testCmd+"|"+reloadCmd {
		t.Errorf("commands %q, want the test and then the reload", got)
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


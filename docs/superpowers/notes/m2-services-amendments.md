## A1 — Header
Reason: Prune must not delete generations newer than keep or current.tmp, because agent enroll writes without the agent lock while the daemon runs (spec section 2 keeps only the previous generation until a handshake).
Cost if wrong: Leftover newer generations and a stale current.tmp linger until the next save, which is harmless.
### Old
````text
`dir` is `Paths.AgentTLSDir` (mode 0700, created when missing). A generation is `gen-<n>/` (mode 0700) holding `agent.key` (PEM PKCS #8 Ed25519), `agent.crt`, `ca.crt` (PEM) and `controller.json` (`{"address", "san"}`), each mode 0600. `SaveGeneration` refuses credentials whose certificate is not an Ed25519 agent certificate of `Key` with EKU clientAuth, signed by `CA`, naming one host, or whose endpoint is not `host:port` plus a controller SAN; it writes each file with `O_EXCL` and fsyncs it, fsyncs the generation directory, links `current.tmp` to the relative name `gen-<n>`, renames it over `current` and fsyncs `dir`. `n` is one more than every `gen-*` in `dir`, complete or not, so a half-written generation is never reused. `LoadCredentials` reads `current` once (a target that is not `gen-<n>` is refused), reads all four files from that directory and applies the same checks; no `current` answers `ErrNotEnrolled`. `PruneGenerations` deletes every generation except `keep` and a leftover `current.tmp`; only the daemon calls it, after a session the controller accepted with generation `keep`.
````
### New
````text
`dir` is `Paths.AgentTLSDir` (mode 0700, created when missing). A generation is `gen-<n>/` (mode 0700) holding `agent.key` (PEM PKCS #8 Ed25519), `agent.crt`, `ca.crt` (PEM) and `controller.json` (`{"address", "san"}`), each mode 0600. `SaveGeneration` refuses credentials whose certificate is not an Ed25519 agent certificate of `Key` with EKU clientAuth, signed by `CA`, naming one host, or whose endpoint is not `host:port` plus a controller SAN; it writes each file with `O_EXCL` and fsyncs it, fsyncs the generation directory, links `current.tmp` to the relative name `gen-<n>`, renames it over `current` and fsyncs `dir`. `n` is one more than every `gen-*` in `dir`, complete or not, so a half-written generation is never reused. `LoadCredentials` reads `current` once (a target that is not `gen-<n>` is refused), reads all four files from that directory and applies the same checks; no `current` answers `ErrNotEnrolled`. `PruneGenerations` deletes every generation numbered below `keep`, complete or not, and nothing else: a newer generation and `current.tmp` stay, because `darkpanel agent enroll` may be writing them beside the running daemon without the agent lock (`SaveGeneration` replaces a leftover `current.tmp` and numbers past every leftover generation, and the next handshake's prune removes them); only the daemon calls it, after a session the controller accepted with generation `keep`.
````

## A2 — Header
Reason: P10-C7 restates the prune as "every other generation"; it must match the narrowed P10-C3.
Cost if wrong: A Task 27 reviewer flags the unchanged `PruneGenerations(dir, c.Generation)` call as not pruning newer generations.
### Old
````text
`Run` requires `Paths.AgentDir`, `AgentSocket`, `AgentTLSDir`, `Updater`, `Runtime.Clock` and `Runtime.Log` (`runtime.Open` checks the rest), then, in order: takes `flock(LOCK_EX|LOCK_NB)` on `AgentDir/install.AgentLockName` for as long as it runs (a second daemon fails with `daemon: another agent runs and holds <path>`); opens the runtime as the host the current generation names, or as `UnenrolledHostID` without credentials (unreadable credentials are logged and served as unenrolled); runs `Updater.Recover(ctx, update.RecoverStart, "")` and logs any error at warn level, `update.in_progress` included, without stopping; serves the agent socket (`socket.Listen` with `api.NewSocketHandler(api.Config{Agent: rt, Log: Runtime.Log}, Identity)`); then runs the dial loop until `ctx` ends, when it returns nil. Errors returned: the lock, `runtime.Open`, `socket.Listen`. The dial loop reads the current generation before every attempt: without credentials it polls every `CredentialPoll`; credentials naming another host than the runtime serves (an enrollment, or credentials removed) end the loop, and `Run` reopens the runtime as that identity once no request is running (every socket and session request holds a read lock), keeping the socket. With credentials it connects (`connect`, `DialTimeout`); an accepted session resets the backoff, prunes every other generation, sets `controllerConnected` true until the session ends, serves `api.NewHandler(api.Config{Agent: rt, Log: Runtime.Log, OnHello: <logs the controller's hello>})` with `http.Server.Serve(session)`, and runs `transport.Heartbeat(ctx, session, cfg.Heartbeat)`. After any attempt or session it waits `Backoff.Next()` on `Runtime.Clock`. `ControllerConnected` is nil while unenrolled. Nothing else waits on the controller (R28).
````
### New
````text
`Run` requires `Paths.AgentDir`, `AgentSocket`, `AgentTLSDir`, `Updater`, `Runtime.Clock` and `Runtime.Log` (`runtime.Open` checks the rest), then, in order: takes `flock(LOCK_EX|LOCK_NB)` on `AgentDir/install.AgentLockName` for as long as it runs (a second daemon fails with `daemon: another agent runs and holds <path>`); opens the runtime as the host the current generation names, or as `UnenrolledHostID` without credentials (unreadable credentials are logged and served as unenrolled); runs `Updater.Recover(ctx, update.RecoverStart, "")` and logs any error at warn level, `update.in_progress` included, without stopping; serves the agent socket (`socket.Listen` with `api.NewSocketHandler(api.Config{Agent: rt, Log: Runtime.Log}, Identity)`); then runs the dial loop until `ctx` ends, when it returns nil. Errors returned: the lock, `runtime.Open`, `socket.Listen`. The dial loop reads the current generation before every attempt: without credentials it polls every `CredentialPoll`; credentials naming another host than the runtime serves (an enrollment, or credentials removed) end the loop, and `Run` reopens the runtime as that identity once no request is running (every socket and session request holds a read lock), keeping the socket. With credentials it connects (`connect`, `DialTimeout`); an accepted session resets the backoff, prunes the generations older than the session's (`PruneGenerations(AgentTLSDir, c.Generation)`), sets `controllerConnected` true until the session ends, serves `api.NewHandler(api.Config{Agent: rt, Log: Runtime.Log, OnHello: <logs the controller's hello>})` with `http.Server.Serve(session)`, and runs `transport.Heartbeat(ctx, session, cfg.Heartbeat)`. After any attempt or session it waits `Backoff.Next()` on `Runtime.Clock`. `ControllerConnected` is nil while unenrolled. Nothing else waits on the controller (R28).
````

## A3 — Task 17
Reason: The implementation deletes newer generations and current.tmp, which races with an agent enroll running beside the daemon.
Cost if wrong: Leftover newer generations and a stale current.tmp linger until the next save, which is harmless.
### Old
````text
// PruneGenerations deletes every generation of dir except keep, and a
// leftover current.tmp. The daemon calls it after the first successful
// handshake with generation keep.
func PruneGenerations(dir string, keep int) error {
	gens, err := listGenerations(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range gens {
		if n != keep {
			errs = append(errs, os.RemoveAll(filepath.Join(dir, generationName(n))))
		}
	}
	if err := os.Remove(filepath.Join(dir, currentTemp)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
````
### New
````text
// PruneGenerations deletes every generation of dir numbered below keep. The
// daemon calls it after the first successful handshake with generation keep.
// Newer generations and current.tmp stay: darkpanel agent enroll writes them
// without the agent lock while the daemon runs, and SaveGeneration already
// replaces a leftover current.tmp and numbers past every leftover generation.
func PruneGenerations(dir string, keep int) error {
	gens, err := listGenerations(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range gens {
		if n < keep {
			errs = append(errs, os.RemoveAll(filepath.Join(dir, generationName(n))))
		}
	}
	if err := errors.Join(errs...); err != nil {
````

## A4 — Task 17
Reason: No test pruned while a newer generation existed, so the race went untested.
Cost if wrong: One extra save and two prunes in a unit test.
### Old
````text
	if err := PruneGenerations(dir, 2); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-2"}) {
		t.Errorf("generations after pruning = %v, want gen-2 alone", gens)
	}
}
````
### New
````text
	// An enrollment beside the running daemon may already have written a
	// newer generation; a handshake with generation 2 must not delete it.
	third := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, third); err != nil || n != 3 {
		t.Fatalf("third SaveGeneration = %d, %v", n, err)
	}
	if err := PruneGenerations(dir, 2); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-2", "gen-3"}) {
		t.Errorf("generations after pruning = %v, want gen-2 and gen-3", gens)
	}
	if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, third) {
		t.Errorf("LoadCredentials after pruning = %+v, %v; want the third generation", got, err)
	}
	if err := PruneGenerations(dir, 3); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-3"}) {
		t.Errorf("generations after the second pruning = %v, want gen-3 alone", gens)
	}
}
````

## A5 — Task 27
Reason: The session doc comment restates the old rule ("every other credential generation"); it must match the narrowed P10-C3.
Cost if wrong: A wrong comment that misleads the next reader of the prune call.
### Old
````text
// session connects with c and serves the agent API on the session until it
// ends. A session the controller accepted resets the backoff and deletes
// every other credential generation.
func (d *daemon) session(ctx context.Context, c *Credentials, b *transport.Backoff) error {
````
### New
````text
// session connects with c and serves the agent API on the session until it
// ends. A session the controller accepted resets the backoff and deletes
// the credential generations older than c's.
func (d *daemon) session(ctx context.Context, c *Credentials, b *transport.Backoff) error {
````

## A6 — Task 28
Reason: Task 28's replace step quotes Task 27's session comment verbatim, and its replacement restates the old rule; both must follow the Task 27 amendment.
Cost if wrong: Task 28's implementer cannot find the text to replace and stops, or brings back the wrong rule in the comment.
### Old
````text
// ends. A session the controller accepted resets the backoff and deletes
// every other credential generation.
```

with:

```go
// session connects with c and serves the agent API on the session until it
// ends, or until the certificate was renewed over it. A session the
// controller accepted resets the backoff and deletes every other credential
// generation.
````
### New
````text
// ends. A session the controller accepted resets the backoff and deletes
// the credential generations older than c's.
```

with:

```go
// session connects with c and serves the agent API on the session until it
// ends, or until the certificate was renewed over it. A session the
// controller accepted resets the backoff and deletes the credential
// generations older than c's.
````

## A7 — Task 25
Reason: Two separate EnrollTimeout windows let Authority.Enroll commit after the connection deadline, so the issued certificate's answer is lost; one deadline bounds the connection through its answer, as EnrollTimeout's definition says.
Cost if wrong: Two lines of churn; Authority.Enroll gets a slightly shorter window, the remainder of the connection's 30 seconds.
### Old
````text
	_ = conn.SetDeadline(time.Now().Add(s.cfg.EnrollTimeout))
	req, err := http.ReadRequest(bufio.NewReader(io.LimitReader(conn, maxEnrollRequest)))
	if err != nil {
		log.Debug("fleet: unreadable enrollment request", "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, s.cfg.EnrollTimeout)
````
### New
````text
	// One deadline bounds both the connection and Authority.Enroll, so an
	// enrollment cannot commit after its answer can no longer be sent.
	deadline := time.Now().Add(s.cfg.EnrollTimeout)
	_ = conn.SetDeadline(deadline)
	req, err := http.ReadRequest(bufio.NewReader(io.LimitReader(conn, maxEnrollRequest)))
	if err != nil {
		log.Debug("fleet: unreadable enrollment request", "err", err)
		return
	}
	ctx, cancel := context.WithDeadline(s.ctx, deadline)
````

## A8 — Header
Reason: P12's pickup promise holds only for a daemon without a live session; P10-C7 reads credentials between attempts only.
Cost if wrong: P12 documents a looser pickup time than it could, with no behaviour change.
### Old
````text
- `darkpanel agent enroll [--token-file <path>]`: the token from `daemon.ReadTokenFile(path, 0)` (`os.Getuid()` under `--dev`), else the hidden prompt or standard input (P12's), then `daemon.Enroll(ctx, p.AgentTLSDir, token)`. A running daemon takes the new generation up within `CredentialPoll`; no unit restart is needed.
````
### New
````text
- `darkpanel agent enroll [--token-file <path>]`: the token from `daemon.ReadTokenFile(path, 0)` (`os.Getuid()` under `--dev`), else the hidden prompt or standard input (P12's), then `daemon.Enroll(ctx, p.AgentTLSDir, token)`. No unit restart is needed: a running daemon without a live session takes the new generation up on its next attempt (within `CredentialPoll` while unenrolled, after its backoff wait otherwise); a daemon with a live session takes it up only after that session ends. Revoke and `core.host.enroll-local` end the session, and `core.host.token` is refused for an enrolled host, so P12 must not promise or test a pickup bound for a daemon with a live session.
````

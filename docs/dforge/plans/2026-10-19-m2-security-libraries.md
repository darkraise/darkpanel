# M2 Security Libraries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build M2 plan 2, parts P1 (agent authority), P2 (transport) and P5 (update library): the controller's agent certificate authority, enrollment tokens and the fleet migration; the mutual-TLS policies, yamux session, heartbeat and redial backoff; and the self-update library with release verification, transaction records, staging, swap, health check, rollback, stepping and recovery. Wave 2 parts P8, P10 and P11 and the assembly part P12 consume these packages.

**Architecture:** Three packages with no file in common. `internal/controller/fleet` (files `ca*.go`, `certs*.go`, `tokens*.go` only; P8 owns the rest of the package later) holds the agent root, the controller listener certificate, agent certificate issuance and revocation, and enrollment tokens, all over controller.db; `internal/core/store/migrations/controller/0005_fleet.sql` adds its tables. `internal/transport` holds what both ends of the agent connection share and knows nothing about actions or databases: SAN and token formats, the enrollment bodies, the controller's ALPN-selected TLS policies, the agent's TLS configurations, the yamux session with stream limits, the heartbeat and the redial backoff. `internal/update` is the root-side updater over agent.db, `UpdateDir`, `BinaryDir` and the `platform.Systemd` interface; the core module's update actions (`internal/update/actions.go`, P11) and the CLI commands (P12) call it.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `crypto/tls` (TLS 1.3 only), `crypto/ed25519`, `crypto/x509`, `github.com/hashicorp/yamux` v0.1.2, `aead.dev/minisign` v0.3.0, `modernc.org/sqlite` through `internal/core/store`, `golang.org/x/sys/unix` (flock), `github.com/oklog/ulid/v2`.

**Spec:** docs/dforge/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves

**Plan review:** 2026-10-20 — dr-superpowers:judge-opus — executability 16 / coherence 17 / coverage 17 / assumptions 16 (round 1)

**Parallelism:** waves — 1-2 | 3-4 | 5 | 6 | 7 | 8 | 9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20

**Integration check:** `GOTOOLCHAIN=local go build ./... && GOTOOLCHAIN=local go test -count=1 ./... && GOTOOLCHAIN=local go vet ./... && golangci-lint run --allow-serial-runners && GOTOOLCHAIN=local go mod tidy -diff`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is a small tmpfs that parallel Go builds fill: every `go` command and `golangci-lint` run with `TMPDIR` set to a directory on disk, `TMPDIR=/root/.cache/plan2-tmp/<task number>` (create it with `mkdir -p` and empty it when the task ends).
- Dependencies: no task edits `go.mod` or `go.sum`; every module this plan imports is already required (foundation C1). After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing. `internal/deps/deps.go` stays; the assembly part removes it.
- Files: a task touches only the files its **Files:** block names. The plan as a whole touches only `internal/transport/`, `internal/controller/fleet/ca*.go`, `internal/controller/fleet/certs*.go`, `internal/controller/fleet/tokens*.go`, `internal/update/` except `internal/update/actions.go` and `internal/update/trustedkeys/`, `internal/core/store/migrations/controller/0005_fleet.sql` and `internal/core/store/migrations_test.go`.
- JSON fields are camelCase. New tables are `STRICT` and store times as fixed-width RFC 3339 UTC text, layout `2006-01-02T15:04:05.000000000Z`, as every M1 table does (`internal/core/lifecycle/journal.go:39`), so times compare as text.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix (`fleet:`, `transport:`, `update:`); a typed error built with `errcode.New` or `errcode.Wrap` carries its code instead.
- Security defaults are never configurable: TLS 1.3 only (`MinVersion: tls.VersionTLS13`), no system roots on either side, Ed25519 keys only for the agent authority and agent certificates.
- Nothing in this plan talks to the real systemd, D-Bus, network or `/usr/local`: systemd is a fake implementing `platform.Systemd`, release servers are `httptest` servers on `127.0.0.1:0`, every path comes from `paths.Under(t.TempDir())` or `t.TempDir()`, sockets live under `os.MkdirTemp("", "dp")`, and time comes from `coretest.NewFakeClock` wherever a duration above one second is waited on. Tasks of one wave therefore share no port, path or global state.
- Tasks of one wave that share a Go package (`internal/update`) declare no top-level identifier, test helper included, that another task of the plan declares; the **Interfaces:** block of each task lists the top-level names it adds.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean. `--allow-serial-runners` is required because golangci-lint holds a shared lock and tasks of one wave may lint together.
- Each task of a wave runs in its own worktree from the wave's base, so a package-wide command sees only merged work plus the task's own files.
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.

## Contracts

Every name below is produced by exactly one task and consumed as written. Later M2 parts (P8 fleet service, P9 host actions, P10 agent daemon, P11 update actions and rollout, P12 assembly) consume them too. Foundation contracts are cited as F-C<n> (`docs/dforge/plans/2026-10-19-m2-foundation.md`, Contracts).

**C1. Fleet migration (Task 10), `internal/core/store/migrations/controller/0005_fleet.sql`.**

```sql
ALTER TABLE core_hosts ADD COLUMN state TEXT NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'enrolled', 'revoked'));
ALTER TABLE core_hosts ADD COLUMN enrolled_at TEXT;
ALTER TABLE core_hosts ADD COLUMN last_seen_at TEXT;
ALTER TABLE core_hosts ADD COLUMN agent_version TEXT;
ALTER TABLE core_hosts ADD COLUMN protocol_version INTEGER;
ALTER TABLE core_hosts ADD COLUMN os TEXT;
ALTER TABLE core_hosts ADD COLUMN arch TEXT;

CREATE TABLE core_agent_certs (
    serial     TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    not_before TEXT NOT NULL,
    not_after  TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX core_agent_certs_host ON core_agent_certs (host_id);

CREATE TABLE core_enroll_tokens (
    host_id     TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    hash        BLOB NOT NULL,
    expires_at  TEXT NOT NULL,
    consumed_at TEXT,
    created_at  TEXT NOT NULL
) STRICT;

CREATE TABLE core_host_update (
    host_id         TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    channel         TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    days            TEXT,
    time_utc        TEXT,
    last_window     TEXT,
    last_rollout_id TEXT
) STRICT;
```

An existing M1 local host row becomes `pending` through the column default. `core_host_update` is written by P9 and read by P11: `days` is the schedule's days as lower-case names joined by `,` in the order `mon,tue,wed,thu,fri,sat,sun` (for example `mon,thu`), `time_utc` is `HH:MM`, both null when the host has no schedule; `last_window` is a window id `<YYYY-MM-DD>T<HH:MM>`.

**C2. Shared identity, token and enrollment bodies (Task 5), `internal/transport`.**

```go
const (
	ALPNAgent  = "darkpanel-agent/1"
	ALPNEnroll = "darkpanel-enroll/1"
	EnrollPath = "/enroll/v1"           // POST over an ALPNEnroll connection
	RenewPath  = "/controller/v1/renew" // POST over an agent-opened session stream
	TokenPrefix = "dpe1."
)
func HostURI(hostID string) string             // "darkpanel://host/" + hostID
func ControllerURI(controllerID string) string // "darkpanel://controller/" + controllerID
// HostIDOf returns the host id of an agent certificate: the certificate has
// exactly one URI SAN, it is HostURI(id) with id 1 to 64 characters of
// [0-9A-Za-z], and it has no DNS, IP or e-mail SANs.
func HostIDOf(cert *x509.Certificate) (string, error)
func SerialText(serial *big.Int) string        // serial.Text(16): lower-case hex, no padding
func Fingerprint(cert *x509.Certificate) string // lower-case hex SHA-256 of cert.Raw

type Token struct {
	Address       string `json:"a"` // host:port the agent dials
	Fingerprint   string `json:"f"` // Fingerprint of the agent root
	ControllerSAN string `json:"s"` // ControllerURI(controller id)
	HostID        string `json:"h"`
	Secret        string `json:"t"` // 16 random bytes, base64url without padding
}
func (t Token) String() string // TokenPrefix + base64url without padding of the JSON object
// ParseToken trims surrounding white space and fails with
// errcode.EnrollTokenInvalid unless the text is TokenPrefix plus base64url
// without padding of a JSON object with exactly the five members, all
// non-empty strings, f 64 lower-case hex digits, s a ControllerURI, and t
// decoding to 16 bytes.
func ParseToken(s string) (Token, error)
// SecretHash is the SHA-256 of the 16 decoded secret bytes, the value
// core_enroll_tokens.hash stores.
func (t Token) SecretHash() ([]byte, error)

type EnrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"` // PEM "CERTIFICATE REQUEST"
}
type EnrollResponse struct {
	Certificate string `json:"certificate"` // PEM
	CA          string `json:"ca"`          // PEM of the agent root
}
type RenewRequest struct {
	CSR string `json:"csr"`
}
type RenewResponse struct {
	Certificate string `json:"certificate"`
}
```

`ParseToken` also requires `a` to split with `net.SplitHostPort`, and `h` and the controller id in `s` to be 1 to 64 characters of [0-9A-Za-z], the id rule of `HostIDOf`. Every refusal has the same detail, `the enrollment token is malformed`. The enrollment and renewal bodies live in `token.go`.

**C3. Controller TLS policy (Task 6), `internal/transport`.**

```go
// PeerCheck decides whether a client certificate that verified against the
// agent root may connect: nil when hostID names an enrolled host and serial
// is one of its unrevoked, unexpired certificates.
type PeerCheck func(ctx context.Context, hostID, serial string) error
type ServerConfig struct {
	Certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error) // the chain [leaf, root]
	AgentRoots  *x509.CertPool                                       // the agent root alone
	CheckPeer   PeerCheck
}
type Peer struct {
	Conn     *tls.Conn
	Protocol string // ALPNAgent or ALPNEnroll
	HostID   string // empty for ALPNEnroll
	Serial   string // SerialText of the client leaf; empty for ALPNEnroll
}
// Handshake runs the controller side of the TLS handshake on raw within ctx
// and closes raw on any failure.
func Handshake(ctx context.Context, raw net.Conn, cfg ServerConfig) (*Peer, error)
```

TLS 1.3 only. The policy is selected by the client's offered ALPN; a client offering both protocols or neither is refused. `ALPNAgent`: `RequireAnyClientCert`, the chain verified against `AgentRoots` alone with EKU `clientAuth`, `HostIDOf` the leaf, then `CheckPeer` with the handshake's context. `ALPNEnroll`: no client certificate requested; a presented one is ignored. Session tickets are disabled (`SessionTicketsDisabled`), so every connection runs the certificate checks; a resumed session would skip `VerifyPeerCertificate`. Only the client's leaf is verified; presented intermediates are ignored. A leaf without the clientAuth EKU is refused even though `x509.Verify` accepts a certificate with no EKU.

**C4. Agent TLS, dial and backoff (Task 7), `internal/transport`.**

```go
// AgentConfig is the agent's ALPNAgent configuration: cert as client
// certificate; the controller's leaf must verify against ca alone with EKU
// serverAuth and carry the URI SAN controllerSAN.
func AgentConfig(cert tls.Certificate, ca *x509.Certificate, controllerSAN string) *tls.Config
// EnrollConfig is the ALPNEnroll configuration: no client certificate; the
// last certificate the controller presents must have the SHA-256
// fingerprint, and the leaf must verify against that certificate alone with
// EKU serverAuth and carry the URI SAN controllerSAN.
func EnrollConfig(fingerprint, controllerSAN string) *tls.Config
// Dial connects to address over TCP and completes the handshake with cfg
// within ctx.
func Dial(ctx context.Context, address string, cfg *tls.Config) (*tls.Conn, error)

const (
	BackoffMin = time.Second
	BackoffMax = time.Minute
)
type Backoff struct{ /* unexported */ }
func NewBackoff() *Backoff
// Next returns a delay drawn uniformly from [0, min(BackoffMax, BackoffMin*2^n)]
// for the n-th call since the last Reset (full jitter).
func (b *Backoff) Next() time.Duration
func (b *Backoff) Reset()
```

Both configurations also require the negotiated ALPN to be their own protocol and the leaf to carry exactly one URI SAN, equal to `controllerSAN`. `Backoff` is not safe for concurrent use.

**C5. Session (Task 8), `internal/transport`.**

```go
const (
	MaxControllerStreams = 32 // concurrent streams the controller opens on one session
	MaxAgentStreams      = 4  // concurrent streams the agent opens on one session
)
type Session struct{ /* unexported */ }
var _ net.Listener = (*Session)(nil)
// ControllerSession is the yamux server end: Open is limited to
// MaxControllerStreams, Accept to MaxAgentStreams.
func ControllerSession(conn net.Conn) (*Session, error)
// AgentSession is the yamux client end: Open is limited to MaxAgentStreams,
// Accept to MaxControllerStreams.
func AgentSession(conn net.Conn) (*Session, error)
// Open waits for a free slot until ctx ends (the error wraps ctx.Err()),
// then opens a stream.
func (s *Session) Open(ctx context.Context) (net.Conn, error)
// Accept waits for a free slot, then returns the next stream the peer
// opened; on a closed session it fails with an error wrapping
// net.ErrClosed, which http.Server.Serve returns.
func (s *Session) Accept() (net.Conn, error)
func (s *Session) Addr() net.Addr
func (s *Session) Close() error
func (s *Session) Done() <-chan struct{} // closed when the session ends
func (s *Session) Ping() (time.Duration, error)
```

yamux configuration: `AcceptBacklog` 64, `StreamOpenTimeout` 10 s, `StreamCloseTimeout` 30 s, `EnableKeepAlive` false, `ConnectionWriteTimeout` 10 s, `LogOutput` `io.Discard`. `Session` is a `net.Listener`, so `http.Server.Serve(session)` serves the peer's streams, and `Open` is the dialer an HTTP client over the session uses. A slot frees only when the stream is closed at both ends: the peer closed or reset it, or 30 s (`StreamCloseTimeout`) passed after the local close, so a peer cannot cycle more than its limit through half-closed streams. yamux resets a stream beyond the 64-stream accept backlog; the remaining exposure is 64 queued streams per authenticated session beyond the accepted ones, each buffering at most the 256 KiB stream window.

**C6. Heartbeat (Task 9), `internal/transport`.**

```go
const (
	HeartbeatInterval = 15 * time.Second
	HeartbeatTimeout  = 10 * time.Second
	HeartbeatFailures = 3
)
type HeartbeatConfig struct {
	Interval, Timeout time.Duration // zero means HeartbeatInterval, HeartbeatTimeout
	Failures          int           // zero means HeartbeatFailures
	OnAlive           func()        // called after every successful ping; may be nil
}
// Heartbeat pings s every Interval, waiting at most Timeout for each answer,
// until ctx ends (nil) or s closes (nil). After Failures consecutive failed
// pings it closes s and returns an error.
func Heartbeat(ctx context.Context, s *Session, cfg HeartbeatConfig) error
```

A ping that fails because the session closed returns nil, not an error.

**C7. Agent authority (Task 11), `internal/controller/fleet`.**

```go
const PurposeAgent = "agent" // core_ca.purpose of the agent root
const (
	CertValidity   = 365 * 24 * time.Hour // controller listener and agent certificates
	CertRenewAfter = CertValidity * 2 / 3
)
type Authority struct{ /* unexported */ }
// LoadOrCreateAuthority loads the agent root from core_ca and the controller
// id from core_meta (key controller_id), creating either on the first start
// inside a write transaction that finds none. The root is Ed25519, valid 10
// years; its key is sealed with associated data core_ca.key_ct.<row id>.
func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error)
func (a *Authority) ControllerID() string  // a ULID
func (a *Authority) ControllerSAN() string // transport.ControllerURI(a.ControllerID())
func (a *Authority) Root() *x509.Certificate
func (a *Authority) RootPEM() []byte
func (a *Authority) Pool() *x509.CertPool // holds the root alone
func (a *Authority) Fingerprint() string  // transport.Fingerprint(a.Root())
// GetCertificate serves the agent listener's chain [leaf, root]: issued on
// the first call and reissued once CertRenewAfter has passed since the
// current one was issued. The leaf is Ed25519, valid CertValidity, EKU
// serverAuth only, URI SAN ControllerSAN, no DNS or IP SANs.
func (a *Authority) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)
```

**C8. Agent certificates and revocation (Task 12), `internal/controller/fleet`.**

```go
// IssueAgentCert checks csrPEM and, inside tx, issues hostID a certificate
// and inserts its core_agent_certs row. The certificate is valid
// CertValidity, EKU clientAuth only, URI SAN transport.HostURI(hostID) and
// nothing else, with a 128-bit random serial. A CSR that is not one PEM
// "CERTIFICATE REQUEST" block, whose signature does not verify or whose key
// is not Ed25519 fails with request.invalid_input; its subject and
// extensions are ignored. A host id that transport.HostIDOf cannot read
// back from the issued certificate is refused with a plain error and nothing
// is stored. It returns the certificate in PEM.
func (a *Authority) IssueAgentCert(ctx context.Context, tx *sql.Tx, hostID string, csrPEM []byte) ([]byte, error)
// Renew issues an enrolled host a new certificate in a write transaction of
// its own; earlier serials stay valid until Handshake or their expiry. A
// host that is not enrolled fails with auth.forbidden.
func (a *Authority) Renew(ctx context.Context, hostID string, csrPEM []byte) ([]byte, error)
// CheckPeer is the transport.PeerCheck: nil when hostID is enrolled and
// serial is its unrevoked certificate with now before not_after; otherwise
// auth.forbidden.
func (a *Authority) CheckPeer(ctx context.Context, hostID, serial string) error
// Handshake records a successful handshake with serial: every other
// unrevoked serial of hostID whose created_at is earlier is revoked.
func (a *Authority) Handshake(ctx context.Context, hostID, serial string) error
// RevokeHost, inside tx, sets revoked_at on every unrevoked serial of hostID,
// sets the host's state to revoked and deletes its enrollment token, so a
// token issued before the revocation cannot enroll the host after it (a
// revoked host may enroll, spec section 3). An unknown host is not_found.
func RevokeHost(ctx context.Context, tx *sql.Tx, hostID string, now time.Time) error
```

**C9. Enrollment tokens (Task 13), `internal/controller/fleet`.**

```go
const TokenTTL = time.Hour
// IssueToken creates or replaces hostID's token inside tx and returns it with
// its expiry. address is the host:port the agent dials. It checks no host
// state; the host actions (P9) apply the state rules. An empty address is
// request.invalid_input; an unknown host is not_found.
func (a *Authority) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error)
type Enrollment struct {
	HostID      string
	Certificate []byte // PEM
	CA          []byte // a.RootPEM()
}
// Enroll consumes token and issues the host's certificate in one write
// transaction (spec section 3, Consumption): the host becomes enrolled and
// enrolled_at is set. Every failure, of the token or of the CSR, is
// enroll.token_invalid, and so is a token whose s or f is not this
// controller's SAN or root fingerprint; a database failure is returned as
// it is. The CSR is checked before the transaction, so a token presented
// with a bad CSR is not consumed.
func (a *Authority) Enroll(ctx context.Context, token string, csrPEM []byte) (Enrollment, error)
```

`core.host.enroll-local` (P9) calls `RevokeHost` before `IssueToken` in its one transaction.

**C10. Enrollment rate limiter (Task 2), `internal/controller/fleet/tokens_limit.go`.** It carries package `fleet`'s package comment, because Task 2 creates the package and revive requires one.

```go
const (
	EnrollPerSource = 10 // requests per source address per minute
	EnrollGlobal    = 60 // requests from all sources per minute
)
type EnrollLimiter struct{ /* unexported */ }
func NewEnrollLimiter(clock core.Clock) *EnrollLimiter
// Allow admits one enrollment request from source (an IP address without a
// port) or fails with enroll.rate_limited when the last minute already holds
// EnrollPerSource admitted requests from source or EnrollGlobal from all
// sources. A refused request is not counted.
func (l *EnrollLimiter) Allow(source string) error
```

`Allow` keys on the source string as given; the enrollment endpoint (P8) passes an IPv6 source grouped by /64, as the login limiter does (`internal/controller/auth/ratelimit.go:66-88`), so one /64 cannot spend the global limit.

**C11. Versions and steps (Task 1), `internal/update`.**

```go
type Version struct {
	Major, Minor, Patch uint64
	Pre                 string // prerelease without the leading "-"; empty for a release
}
// ParseVersion accepts the F-C5 semantic version pattern: no "v" prefix and
// no build metadata.
func ParseVersion(s string) (Version, error)
func (v Version) String() string
func (v Version) Compare(w Version) int // semantic version precedence: -1, 0 or 1

type Release struct {
	Version string `json:"version"`
	Channel string `json:"channel"` // the channel the release was signed for
}
// InChannel returns the releases a host on channel may take, by ascending
// precedence: stable keeps "stable" entries, beta keeps every entry.
// Entries whose version does not parse are dropped.
func InChannel(releases []Release, channel string) []Release
// Steps returns the releases an update from current to target passes
// through, target last, chosen from InChannel(releases, channel).
func Steps(current, target, channel string, releases []Release, force bool) ([]Release, error)
```

Steps rules: a development build (version `dev`) cannot update (`request.invalid_input`); `target` must be an entry of `InChannel(releases, channel)` (else `update.not_found`); a target equal to `current` is `request.invalid_input`; a target of another major is `request.invalid_input`; a lower target is `update.downgrade_refused` unless `force`, and a forced downgrade may go back at most one minor and is the single step `[target]`; a higher target of the same minor is `[target]`; a higher minor steps through the newest entry of every minor between, then `target` (a missing intermediate minor is `update.not_found`).

**C12. Release index (Task 4), `internal/update`.**

```go
const IndexTTL = time.Hour
type Index struct {
	FormatVersion int       `json:"formatVersion"`
	Releases      []Release `json:"releases"`
}
type IndexSource struct{ /* unexported */ }
func NewIndexSource(baseURL string, client *http.Client, clock core.Clock) *IndexSource
// Releases returns the entries of <baseURL>/index/releases.json, fetched at
// most once per IndexTTL; a failed fetch is not cached. A status other than
// 200, a body over 1 MiB or a formatVersion other than 1 is an error.
func (s *IndexSource) Releases(ctx context.Context) ([]Release, error)
```

**C13. Health probes (Task 3), `internal/update`.**

```go
const (
	HealthDeadline = 80 * time.Second // after the swap
	HealthInterval = 2 * time.Second
)
type ControllerHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}
// ControllerProbe reads GET /healthz over the controller socket at socket.
func ControllerProbe(socket string) func(ctx context.Context) (ControllerHealth, error)
type Probes struct {
	Controller func(ctx context.Context) (ControllerHealth, error) // nil when the host has no controller unit
	Agent      func(ctx context.Context) (core.AgentHello, error)
}
// Wait applies the health rule (spec section 8) every HealthInterval until
// both probes pass (nil), the controller reports `to` with a status other
// than ok (error at once), or deadline passes (error).
func (p Probes) Wait(ctx context.Context, clock core.Clock, from, to string, deadline time.Time) error
```

**C14. Transaction records and the update lock (Task 14), `internal/update`.**

```go
const (
	RecordFormat = 1
	LockTimeout  = 5 * time.Second
)
type Record struct {
	FormatVersion int               `json:"formatVersion"`
	ID            string            `json:"id"`
	OperationID   string            `json:"operationId"`
	ParentID      *string           `json:"parentId"`
	Kind          string            `json:"kind"`
	FromVersion   string            `json:"fromVersion"`
	ToVersion     string            `json:"toVersion"`
	TargetVersion string            `json:"targetVersion"`
	State         string            `json:"state"`
	Error         *core.ErrorDetail `json:"error"`
	StartedAt     time.Time         `json:"startedAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	ChangeID      string            `json:"changeId"`
	Steps         []Release         `json:"steps"`
	Deadline      *time.Time        `json:"deadline"`
	PrevSHA256    string            `json:"prevSha256"`
	NewSHA256     string            `json:"newSha256"`
}
func (r Record) Transaction() core.UpdateTransaction
func ReadRecord(p paths.Paths, id string) (Record, error)  // wraps fs.ErrNotExist when missing
func WriteRecord(p paths.Paths, r Record) error            // temp file, fsync, rename, fsync UpdateDir; mode 0600
func ListRecords(p paths.Paths) ([]Record, error)          // every record, by StartedAt then ID
func OperationRecords(all []Record, operationID string) []Record // by StartedAt then ID
// Finished reports whether an operation is over: any record failed or
// rolled-back, or the last record committed with ToVersion equal to
// TargetVersion; a rollback-kind operation is over when its record is
// rolled-back or failed.
func Finished(op []Record) bool
type Lock struct{ /* unexported */ }
var ErrLockTimeout = errors.New("update: the update lock is held by another process")
// AcquireLock takes an exclusive flock on path (created 0600), retrying
// until timeout passes (ErrLockTimeout) or ctx ends.
func AcquireLock(ctx context.Context, path string, timeout time.Duration) (*Lock, error)
func (l *Lock) Release() error
```

Records accept any `formatVersion` of 1 or more and ignore unknown members (ADR 0004: every binary of a major reads every record format of that major); a missing format or an id that does not match the file name is refused. `Steps` holds every step of the operation with the channel its index entry carried, so each step verifies its signature against the selected entry's channel (spec section 8, Channels).

**C15. Binary files (Task 15), `internal/update`.** Every rename and every write below is followed by an fsync of the file and of `BinaryDir`.

```go
func FileSHA256(path string) (string, error) // lower-case hex
// Stage copies src to p.NewBinary(tx) with mode 0755 and returns its hash.
func Stage(p paths.Paths, tx, src string) (string, error)
// KeepPrevious copies p.Binary() to darkpanel.prev.tmp, renames it over
// p.PrevBinary(), hard-links p.PrevBinary() as p.RecoveryBinary(),
// replacing an older one, and returns the previous binary's hash.
func KeepPrevious(p paths.Paths) (string, error)
// Swap renames p.NewBinary(tx) over p.Binary().
func Swap(p paths.Paths, tx string) error
// RestorePrevious renames p.Binary() to p.FailedBinary() and p.PrevBinary()
// to p.Binary(), resuming from whichever rename a crash left undone, told
// apart by the hashes. Before any rename, a p.PrevBinary() whose hash is not
// prevSHA fails with update.prev_mismatch and nothing is renamed. When
// darkpanel already holds prevSHA, which includes a crash before the swap,
// it returns nil and renames nothing; a darkpanel that is neither prevSHA
// nor newSHA fails with update.prev_mismatch and renames nothing.
func RestorePrevious(p paths.Paths, prevSHA, newSHA string) error
```

**C16. Release download and verification (Task 16), `internal/update`.**

```go
// TarballName is darkpanel_<version>_linux_<arch>.tar.gz (F-C16).
func TarballName(version, arch string) string
// VerifyChecksums accepts checksums when one key of keys has a signature in
// sigs (keyed by trustedkeys.Key.ID) that verifies in minisign hashed mode
// with the trusted comment exactly
// "version=<version> channel=<channel> purpose=checksums"; otherwise
// update.signature_invalid.
func VerifyChecksums(checksums []byte, sigs map[string][]byte, keys []trustedkeys.Key, version, channel string) error
// Download fetches rel's tarball for arch, checksums.txt and
// checksums.txt.<ID>.minisig for every key from <baseURL>/v<version>/ into
// dir (a missing signature file is skipped), verifies the signature against
// rel.Channel and the tarball's checksums.txt line, and extracts the archive
// root's darkpanel to dir/darkpanel with mode 0755, returning its path.
func Download(ctx context.Context, client *http.Client, baseURL string, keys []trustedkeys.Key, rel Release, arch, dir string) (string, error)
```

**C17. Updater and operation start (Task 17), `internal/update`.**

```go
const (
	ControllerUnit = "darkpanel-controller.service"
	AgentUnit      = "darkpanel-agent.service"
	RollbackUnit   = "darkpanel-rollback.service"
	ChangeWait     = 60 * time.Second // the runner waits this long for its change to complete
	RecoverBudget  = 30 * time.Second // the most one recover command works
)
func RunnerUnit(tx string) string         // "darkpanel-update-" + tx + ".service"
func RollbackRunnerUnit(tx string) string // "darkpanel-rollback-" + tx + ".service"
func DeadlineTimer(tx string) string      // "darkpanel-update-deadline-" + tx + ".timer"

type Config struct {
	Paths   paths.Paths
	DB      *store.DB // agent.db
	Systemd platform.Systemd
	Clock   core.Clock
	Log     *slog.Logger
	Version string            // this binary's version, buildinfo.Version
	Arch    string            // runtime.GOARCH
	BaseURL string            // trustedkeys.BaseURL()
	Keys    []trustedkeys.Key // trustedkeys.Keys()
	HTTP    *http.Client
	Index   *IndexSource
	Probes  Probes
	// SelfTest runs a staged binary's self-test; nil runs "<binary> selftest"
	// with a 30 s limit.
	SelfTest func(ctx context.Context, binary string) error
	// NewerAgents lists enrolled hosts whose agent reports a version newer
	// than version; set on the controller's host only (spec section 8,
	// Manual rollback), nil elsewhere.
	NewerAgents func(ctx context.Context, version string) ([]string, error)
}
type Updater struct{ /* unexported */ }
func New(cfg Config) (*Updater, error)
// CheckApply applies the update rules without side effects: the apply action's Plan.
func (u *Updater) CheckApply(ctx context.Context, in core.UpdateApplyInput) ([]Release, error)
// StartUpdate is the apply action's Apply (spec section 8, Apply returns
// before the restart): the rules again, Reserve(ReserveAll, op:<operation
// id>), the first record pending with changeID, and the transient unit
// RunnerUnit(tx) running p.Binary() "update" "run" tx with OnFailure
// RollbackUnit. On a failure after the reservation it records failed (when
// the record exists) and releases the reservation.
func (u *Updater) StartUpdate(ctx context.Context, changeID string, in core.UpdateApplyInput) (core.UpdateTransaction, error)
// Status answers core.update.status/v1.
func (u *Updater) Status(ctx context.Context, in core.UpdateStatusInput) (core.UpdateStatus, error)
// Transaction reads one record's transaction: the apply action's Verify.
func (u *Updater) Transaction(ctx context.Context, id string) (core.UpdateTransaction, error)
```

`StartUpdate` takes the update lock before `Reserve`, so a recover sweep, which runs under the same lock, never releases a reservation whose record is not written yet. A new record starts strictly after the newest record, because ULIDs sort in creation order only within one process and an operation's records are ordered by `StartedAt`. After starting a runner unit the starter polls its state for up to 5 s (100 × 50 ms) while holding the lock. `New` requires `Paths.UpdateDir`, `Paths.BinaryDir`, `DB`, `Systemd`, `Clock` and `Version`, and defaults `Log` to a discarding logger, `HTTP` to `http.DefaultClient` and `SelfTest` to running `<binary> selftest`; `CheckApply` without an `Index` is an error. `Status` with both ids set is `request.invalid_input`, and an unknown change gives an empty list; `Transaction` of a missing id is `not_found`. A failed self-test is recorded as `update.interrupted`. Operation and transaction ids are ULIDs. The reservation owner is `lifecycle.ReservationOwner(operationID)` (F-C8). The runner and recover processes open agent.db with `store.Open` while the agent daemon has it open; they take no directory lock.

**C18. Rollback (Task 18), `internal/update`.**

```go
// RollBack is the one automatic rollback of a swapped record (spec section
// 8, Rollback): under the update lock it claims swapped -> rolling-back
// (another caller finds a later state and returns nil), restores the
// previous binary, records rolled-back, releases the reservation, stops
// DeadlineTimer(tx), deletes darkpanel.recovery and, unless atBoot, resets
// and restarts the controller unit when it exists and then the agent unit.
// A record already rolling-back is finished the same way.
func (u *Updater) RollBack(ctx context.Context, tx string, atBoot bool) error
// CheckRollback applies the rollback guards without side effects.
func (u *Updater) CheckRollback(ctx context.Context, in core.UpdateRollbackInput) error
// StartRollback is the rollback action's Apply: the update lock, the
// guards, the reservation, a rollback-kind record pending, and the transient
// unit RollbackRunnerUnit(tx) running p.Binary() "update" "rollback"
// "--direct" "--transaction" tx with OnFailure RollbackUnit.
func (u *Updater) StartRollback(ctx context.Context, changeID string, in core.UpdateRollbackInput) (core.UpdateTransaction, error)
// RunRollback runs a rollback-kind record: darkpanel update rollback
// --direct --transaction <tx>.
func (u *Updater) RunRollback(ctx context.Context, tx string) error
// DirectRollback is darkpanel update rollback --direct without a
// transaction: the rollback guards, a rollback-kind record, then
// RunRollback in this process.
func (u *Updater) DirectRollback(ctx context.Context, force bool) (core.UpdateTransaction, error)
```

Rollback guards: no records, no `darkpanel.prev`, or a newest operation that rolled back or failed is `update.nothing_to_roll_back`; an unfinished newest operation is `update.in_progress`; a `darkpanel.prev` whose hash differs from the committed update's `prevSha256` is `update.prev_mismatch`; `NewerAgents` naming any host without `force` is `update.downgrade_refused`. A rollback-kind record carries the committed update's `prevSha256` and `newSha256` and empty `steps`. A prev mismatch during a rollback records failed, releases the reservation, stops the deadline timer and touches no file in `BinaryDir`: `darkpanel.recovery` is kept, because it may be the only copy left of the binary `prevSha256` names (spec section 8: a mismatch leaves the binaries alone). `DirectRollback` holds the update lock from its record's write through the rollback. A rollback also deletes a leftover `darkpanel.new-<tx>` (a crash after `swapped` was recorded and before the rename). Automatic rollbacks and pre-swap failures record `update.interrupted` with a message naming the cause (host restart, deadline, service or runner failure, health check), because spec section 11 has no health-failure code. Every recover path takes the update lock with `LockTimeout`; on `ErrLockTimeout` it kills the active runner unit of the lock's operation (`Kill` with `SIGKILL`, then `Stop`) and takes the lock again.

**C19. Runner (Task 19), `internal/update`.**

```go
// Run is darkpanel update run <tx>: it moves the record through pending,
// downloading, staged, swapped and committed (spec section 8, The runner),
// rolls back through RollBack on a failed health check, and starts the next
// step of a stepped operation after an intermediate commit.
func (u *Updater) Run(ctx context.Context, tx string) error
```

Downloads go to `UpdateDir/download-<tx>`, removed afterwards. A failed health check rolls back with `update.interrupted` carrying the check's message, and `Run` returns an error, so its transient unit fails and `darkpanel-rollback.service` runs `Recover(RecoverFailure)`, which finds nothing swapped and only sweeps. A record past `pending`, or one another process moved on, makes `Run` return nil. Accepted outcome: when the health check passes but the update lock then cannot be taken within `LockTimeout`, `Run` returns an error and the update is rolled back by the deadline timer or `Recover(RecoverFailure)`; only a recover pass holds the lock that long, and the operator can update again.

**C20. Recovery (Task 20), `internal/update`.**

```go
type RecoverMode int
const (
	RecoverStart    RecoverMode = iota // agent start: no rollback, no restarts
	RecoverBoot                        // darkpanel-update-recover.service: rollbacks without restarts
	RecoverFailure                     // darkpanel-rollback.service: rollbacks with restarts
	RecoverDeadline                    // darkpanel update recover --deadline <tx>
)
// Recover runs, within RecoverBudget and under the update lock: pre-swap
// recovery, then (except RecoverStart) the rollback of every swapped or
// rolling-back record (RecoverDeadline: of tx only), then stepping
// continuation, then the sweep of finished operations' op: reservations.
func (u *Updater) Recover(ctx context.Context, mode RecoverMode, tx string) error
```

`RecoverStart` takes the plain lock: after `LockTimeout` it returns `update.in_progress` and never kills a runner. Its caller, the agent daemon (P10), logs that error and keeps starting, and never exits on it: a runner may hold the lock while it copies and fsyncs a binary of up to 256 MiB, and an agent that exited would start a recover pass through `OnFailure=` in the middle of the update. A unit counts as alive when it is `active`, `activating`, `reloading` or `deactivating`. Recover runs every step even after an earlier one fails, and joins the errors. The `Updater` keeps an unexported `lockTimeout`, set to `LockTimeout`, so tests can shorten it.

**C21. Test helpers.** Test-only names a later task of this plan reuses; P8 adds files to package `fleet` and must declare none of the fleet names below.

- `internal/transport` — `pki_test.go` (Task 6): `type testCA struct{ cert *x509.Certificate; key ed25519.PrivateKey }`, `newTestCA(t) *testCA`, `(ca *testCA) pool() *x509.CertPool`, `type certSpec struct{ uris, dns []string; eku []x509.ExtKeyUsage; notAfter time.Time }`, `(ca *testCA) issue(t, certSpec) tls.Certificate` (chain [leaf, root], `Leaf` set), `(ca *testCA) agentCert(t, hostID) tls.Certificate`, `(ca *testCA) controllerCert(t, controllerID) tls.Certificate`, `serverConfig(ca *testCA, cert tls.Certificate) ServerConfig` (admits every peer), `type dialer func(ctx context.Context, addr string) (*tls.Conn, error)`, `tlsDialer(*tls.Config) dialer`, `type handshakeResult struct{ peer *Peer; serverErr error; client *tls.Conn; clientErr error }`, `handshake(t, ServerConfig, dialer) handshakeResult`, `handshakeCtx(ctx, t, ServerConfig, dialer) handshakeResult`. `client_test.go` (Task 7): `agentDialer(tls.Certificate, *x509.Certificate, string) dialer`, `enrollDialer(fingerprint, san string) dialer`. `session_test.go` (Task 8): `sessionPair(t) (ctrl, agent *Session)` over `net.Pipe`, closed at test end.
- `internal/controller/fleet` — `ca_test.go` (Task 11): `var testStart time.Time` (2026-10-19 12:00 UTC), `newSealer(t) *secret.Sealer`, `openControllerDB(t) *store.DB`, `openControllerDBAt(t, path string) *store.DB`, `newAuthority(t) (*Authority, *store.DB, *coretest.FakeClock)`, `leafOf(t, a *Authority, c *tls.Certificate) *x509.Certificate`. `certs_test.go` (Task 12): `addHost(t, db *store.DB, id, state string)`, `csrPEM(t, key crypto.Signer, tmpl *x509.CertificateRequest) []byte`, `newCSR(t) ([]byte, ed25519.PrivateKey)`, `parseCert(t, data []byte) *x509.Certificate`, `issue(t, a *Authority, db *store.DB, hostID string) *x509.Certificate`, `wantCode(t, err error, code errcode.Code, what string)`, `countCerts(t, db *store.DB) int`. `tokens_test.go` (Task 13): `const testAddress = "198.18.0.10:7443"`, `issueToken(t, a *Authority, db *store.DB, hostID string) (string, time.Time)`, `hostState(t, db *store.DB, hostID string) string`, `var orders`, `race(a, b func())`.
- `internal/update` — `record_test.go` (Task 14): `recordPaths(t) paths.Paths` (`UpdateDir` created 0700), `recordTime`, `testRecord(id, op string, offset time.Duration) Record`. `binaries_test.go` (Task 15): `binaryPaths(t, installed string) paths.Paths`, `writeBinaryFile(t, path, content string)`, `sumOf(content string) string`, `binaryContent(t, path string) string` (`"<missing>"` for an absent file). `release_test.go` (Task 16): `newTestSigner(t) testSigner` (`.Key trustedkeys.Key`, `.sign(data []byte, comment string) []byte`, hashed mode), `releaseComment(version, channel string) string`, `checksumLine(data []byte, name string) string`, `type tarEntry struct{ Name, Body string; Type byte; Linkname string }`, `testTarball(t, entries ...tarEntry) []byte`, `newReleaseServer(t) *releaseServer` (`.URL`, `.client()`, `.put`, `.get`, `.remove`, `.publish(t, version, channel, arch string, binary []byte, signers ...testSigner)`, `.setIndex(t, releases ...Release)`). `updater_test.go` (Task 17): `newUpdaterEnv(t, installed) *updaterEnv` (fields `p`, `db`, `sd`, `clock`, `rel`, `signer`, `mu`, `selfTests`, `selfErr`, `agentHello`, `newer`; methods `config(version) Config`, `updater(t, version) *Updater`), `envReleases`, `binaryOf`, `installedVersion`, `openAgentDB`, `lockRows`, `wantNoLocks`, `insertChange(t, db, id, class, state)`, `wantCode(t, err, code)`, `mustRecord`. `systemd_test.go` (Task 17): `systemdCall`, `fakeSystemd` (`setState`, `failCall` where unit `""` matches any unit, `callsOf`, `unitsOf`, the `onKill` hook, every `platform.Systemd` method), `newFakeSystemd`. `rollback_test.go` (Task 18): `swappedUpdate`, `committedUpdate`, `wantRolledBack`. `runner_test.go` (Task 19): `setChangeState`, `driveClock(clock, step, first, fn)`, `startUpdate`, `lockOwnerOp`. `recover_test.go` (Task 20): `crashedRunner`. Production helper `sleepOn(ctx, clock core.Clock, d time.Duration) error` (Task 3, `health.go`) is reused by Task 19.

## Assumptions (evidence)

- Every module this plan imports is already in `go.mod`: `github.com/hashicorp/yamux v0.1.2`, `aead.dev/minisign v0.3.0`, `github.com/oklog/ulid/v2 v2.1.2`, `golang.org/x/sys v0.49.0` (`go.mod`, read 2026-10-19), so no task touches the dependency manifest.
- yamux v0.1.2 bounds `Ping` by `Config.ConnectionWriteTimeout`, not by a parameter (`session.go:327-357` in the module cache, read 2026-10-19); C5 sets it to 10 s and the heartbeat (C6) applies its own `Timeout` on top.
- Adding `0005_fleet.sql` keeps the existing schema-version assertions passing: they test `>= 4` (`internal/controller/server/e2e_test.go:353`, `listeners_test.go:116`, `server_test.go:139`), and the `schemaVersion: 3` expectations in `internal/controller/api/*_test.go` come from a stub, not from the migrations (`rg -n schemaVersion`, 2026-10-19).
- `store.Migrate` already backs the database up before applying any migration of a new binary version (`internal/core/store/migrate.go:77-87,133-142`), so the update library restarts services and needs no backup code of its own (R34); the harness scenario 10 checks it end to end.
- The token format and the enrollment and renewal bodies live in `internal/transport` (C2) rather than in `internal/controller/fleet`, because the agent daemon (P10) parses tokens and sends the bodies and must not import a controller package; the spec assigns the format to section 3 and no file owner.
- The enrollment rate limiter (C10) is in `internal/controller/fleet/tokens_limit.go`: spec section 3 states the limits beside token consumption, and the file matches P1's `tokens*.go` ownership. The enrollment endpoint (P8) calls `Allow`.
- A record's `steps` holds each step's version with its index entry's channel (C14), not versions alone, because each step's signature must be checked against its selected entry's channel (spec section 8, Channels) and the runner of a later step has no index entry otherwise.
- The M2 core register's rows 1-15, 17, 18 and 21 (`docs/dforge/registers/2026-10-19-m2-fleet-lifecycle-core.md`) are each discharged across several parts and their acceptance includes harness scenarios, so they stay assigned to the spec and close with the assembly and harness plan; no task here carries an `**Items:**` line, which is the one plan-lint warning about registers.
- Codex is off this session (`codex-gate usable=false reason=plugin-not-enabled`, 2026-10-19), so no task carries an `**Executor:**` line and plan review runs on `dr-superpowers:judge-opus`.
- Every task's code was dry-run before it was written into this plan: implemented in a scratch worktree from `65ae6b9`, with its tests, `go vet`, `golangci-lint` and `go mod tidy -diff` passing, on 2026-10-19. Each part's bullets below record what the dry run settled.

Transport (P2):

- Go's TLS server calls `GetConfigForClient` before it picks the protocol version, so the per-client TLS 1.3 minimum refuses a TLS 1.2 client: in the dry run `TestHandshakeRefuses/TLS_1.2` gave `client offered only unsupported versions: [303]` (2026-10-19).
- `x509.Verify` accepts a leaf with no EKU for any requested usage, so the "no extended key usage" refusals come from the explicit EKU checks in `verifyAgent` and `verifyController` (dry run 2026-10-19).
- Each `Handshake` builds its own `tls.Config`, so ticket keys are never shared; `SessionTicketsDisabled` is set anyway (gosec G123, and a later shared configuration). `TestHandshakeNoResumption` passed before and after the flag, so it guards resumption only if a shared configuration appears.
- yamux v0.1.2 resets a stream beyond `AcceptBacklog` (`session.go:704-715`), force-closes a stream and resets the peer 30 s after a local close (`stream.go:359-396`), discards data for a removed stream (`session.go:594-603`), and closes the session when a SYN is not acknowledged within 10 s (module source, 2026-10-19). The two timeout paths are cited, not tested, to keep the suite fast.

Agent authority (P1):

- SQLite accepts `ALTER TABLE ... ADD COLUMN state TEXT NOT NULL DEFAULT 'pending' CHECK (...)` on the existing `core_hosts` and gives existing rows the default: `TestFleetMigrationKeepsTheM1LocalHost` passed in the dry run (modernc.org/sqlite v1.60.1, 2026-10-19), and `go test ./...` passed with `0005_fleet.sql` in place.
- revive's `package-comments` rule fails a package without a package comment (`golangci-lint run --allow-serial-runners ./internal/controller/fleet/...` on a package holding only `tokens_limit.go`, 2026-10-19), so Task 2 carries package `fleet`'s comment (C10).
- Go's TLS 1.3 accepts the authority's Ed25519 chain: `TestControllerCertificateHandshake` passed in the dry run (2026-10-19).
- Run truly at once, the competing write won the enrollment race 40 of 40 rounds, because `Enroll` checks the CSR before it takes the database writer (scratch test, 2026-10-19); the race tests therefore run each order one after the other as well as concurrently, so both outcomes are exercised.
- Decision (spec gap): `RevokeHost` deletes the host's token (C8). Spec section 3 lets a revoked host enroll, so without it a revocation serialised before an enrollment leaves the host enrolled with a valid certificate; with the delete disabled `TestRevocationAgainstEnrollment/other_write_first` fails (dry run, 2026-10-19).
- Decisions: `CheckPeer` checks `not_after` against the authority's clock only, because `not_before` and the chain are the transport's x509 verification; `Handshake` revokes serials with an earlier `created_at`, so two certificates issued at one clock reading never revoke each other (tests advance the fake clock between issues); the agent root's `core_ca.created_at` uses the fixed-width layout, while the web root's row uses `time.RFC3339` (`internal/controller/listen/authority.go:150`).

Update library (P5):

- `minisign.Verify` accepts both legacy `Ed` and hashed `ED` signatures (aead.dev/minisign v0.3.0 `minisign.go:191-207`), so `VerifyChecksums` requires `Algorithm == minisign.HashEdDSA` and refuses legacy `Ed`; the release workflow and the harness sign in hashed mode (F-C16).
- ADR 0007 does not address updates across major versions (`docs/adr/0007-release-and-update.md:61-62,89-93`). Decision: refused with `request.invalid_input`, because the support window and the schema rules are per major. A development build (`dev`) and an update to the running version are refused the same way.
- Decisions on inputs: intermediate steps come from the host's channel, so a beta host steps through the newest beta of each minor; `InChannel` drops entries of an unknown channel; `checksums.txt` lines are accepted in `sha256sum` text (`"  "`) and binary (`" *"`) form, and the tarball's name must appear exactly once; only `darkpanel` or `./darkpanel`, a regular file, is extracted, and a refused archive leaves no binary behind.
- Size limits, this plan's choice: index 1 MiB, `checksums.txt` 64 KiB, a signature 4 KiB, tarball and binary 256 MiB each, decompressed archive 512 MiB.
- The health check bounds each probe call by `HealthInterval` of real time and treats a probe error as "not yet"; a controller reporting a version that is neither `from` nor `to` is "not yet"; a missing agent probe is an error.
- The update lock is exercised only between two file descriptors of one process; `flock` excludes across processes by the same rule (flock(2)), and the harness exercises it for real.

- Record order across processes: with ULID order alone, a step's next record written by another process at the same clock reading sorted before its parent, and `TestRunSteps` failed 2 of 5 runs in the dry run (2026-10-19); new records now start strictly after the newest one (C17), pinned by `TestStartUpdateOrdersAfterTheNewestRecord`.
- The sweep race: with `Reserve` before the update lock, a recover sweep holding the lock could release a reservation whose record was not written yet (dry run, 2026-10-19); `StartUpdate`, `StartRollback` and `DirectRollback` take the lock first (C17, C18).
- `DirectRollback` restarts the services like every rollback, so a unit the operator had stopped starts again, because systemd's `Restart` starts an inactive unit (`RestartUnit`, systemd D-Bus API).
- Unverified — the harness (P13) verifies it: real systemd reports a transient unit as no longer `inactive` within 5 s of `StartTransientUnit`; the fake reports it at once. Real `flock` between separate processes and real systemd kill and reset behaviour are likewise exercised only in the harness.
- Every task is risk 3 except Tasks 1 to 4, and a risk-3 task runs alone, so only the first two waves hold two tasks each; Tasks 3 and 4 depend on Task 1 (Task 4 uses `Release`, Task 3 needs the package comment of Task 1's `version.go`).
- Task 10 edits `internal/core/store/migrations_test.go` (the controller row of `TestCoreMigrationsApply` and new functions at the end), which no part of the spec's decomposition owns; plan 3 (P4, agent `0005_ownership_kinds.sql`), running in a parallel session, will likely edit the agent row of the same table, so whichever plan merges second resolves a small conflict in that table.
- The migration backup that a restarted service takes before migrating (spec Verification, update bullet; R34) is the M1 behaviour of `store.Migrate`, covered by `TestMigrateFreshDatabase`, `TestMigrateBacksUpOnlyWhenPending` and `TestMigrateSkipsBackupForSameVersion` (`internal/core/store/migrate_test.go:39,90,147`); the update library adds no code to it, and harness scenario 10 (P13) checks it across a real update.
- Open for the owner: on a beta host stepping across minors to a stable target, Task 1 takes the newest entry of each intermediate minor in the host's channel, which may be a prerelease (`1.3.3-beta.1` over `1.3.2`); spec section 8 says "the newest patch of 1.3" without naming the channel. Stepping through stable entries only when the target is stable would be a one-line change in `Steps`.
- `/tmp` on this host is a 4 GiB tmpfs that filled to 97% during the dry runs and made SQLite fail with `database or disk is full` (2026-10-19). The executing session should export `TMPDIR` on disk (for example `/root/.cache/plan2-tmp`) for every `go` command; the update and fleet suites take about 20 s each under `-race`.

## Task index

1. Release versions and update steps
2. Enrollment rate limiter
3. Health probes
4. Release index source
5. Transport identity, token and enrollment bodies
6. Controller TLS policy and handshake
7. Agent TLS, dial and redial backoff
8. Session with stream limits
9. Heartbeat
10. Fleet migration
11. Agent authority
12. Agent certificates and revocation
13. Enrollment tokens and consumption
14. Transaction records and the update lock
15. Binary staging, swap and restore
16. Release download and verification
17. Updater and operation start
18. Rollback
19. Runner and stepping
20. Recovery

---

### Task 1: Release versions and update steps

**Files:**
- Create: `internal/update/version.go`
- Test: `internal/update/version_test.go`

**Interfaces:**
- Consumes: F-C2 (`errcode.InvalidInput`, `errcode.UpdateNotFound`, `errcode.UpdateDowngradeRefused`), F-C5 (`core.ChannelStable`, `core.ChannelBeta`).
- Produces: C11 (`Version`, `ParseVersion`, `Version.String`, `Version.Compare`, `Release`, `InChannel`, `Steps`). Top-level names added: `versionPattern`, `Version`, `ParseVersion`, `comparePrerelease`, `isDigits`, `Release`, `InChannel`, `Steps`; tests: `TestParseVersion`, `TestVersionCompare`, `mustVersion`, `stepReleases`, `TestInChannel`, `TestSteps`, `TestStepsKeepTheEntryChannel`. The file also carries the package comment of `internal/update`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/update/version_test.go`:

```go
package update

import (
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestParseVersion(t *testing.T) {
	tests := []struct {
		in   string
		want Version
		ok   bool
	}{
		{"1.2.3", Version{Major: 1, Minor: 2, Patch: 3}, true},
		{"0.0.0", Version{}, true},
		{"1.3.0-beta.1", Version{Major: 1, Minor: 3, Pre: "beta.1"}, true},
		{"10.20.30-rc-1.x.07", Version{Major: 10, Minor: 20, Patch: 30, Pre: "rc-1.x.07"}, true},
		{"v1.2.3", Version{}, false},
		{"1.2", Version{}, false},
		{"01.2.3", Version{}, false},
		{"1.2.3+build", Version{}, false},
		{"1.2.3-", Version{}, false},
		{"1.2.3-beta..1", Version{}, false},
		{"dev", Version{}, false},
		{"99999999999999999999.0.0", Version{}, false},
	}
	for _, tt := range tests {
		got, err := ParseVersion(tt.in)
		if (err == nil) != tt.ok {
			t.Errorf("ParseVersion(%q) error = %v, want ok %v", tt.in, err, tt.ok)
			continue
		}
		if tt.ok && got != tt.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
		if tt.ok && got.String() != tt.in {
			t.Errorf("ParseVersion(%q).String() = %q", tt.in, got.String())
		}
	}
}

func TestVersionCompare(t *testing.T) {
	// Each version has lower precedence than the next (semver 2.0.0 section 11).
	ordered := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
		"1.0.1", "1.2.0-beta.1", "1.2.0", "1.10.0", "2.0.0",
	}
	for i, x := range ordered {
		for j, y := range ordered {
			a, b := mustVersion(t, x), mustVersion(t, y)
			want := 0
			switch {
			case i < j:
				want = -1
			case i > j:
				want = 1
			}
			if got := a.Compare(b); got != want {
				t.Errorf("%s.Compare(%s) = %d, want %d", a, b, got, want)
			}
		}
	}
	if got := mustVersion(t, "1.0.0-beta.007").Compare(mustVersion(t, "1.0.0-beta.7")); got != 0 {
		t.Errorf("numeric identifiers with leading zeros compare %d, want 0", got)
	}
	long := mustVersion(t, "1.0.0-123456789012345678901234567890")
	if got := long.Compare(mustVersion(t, "1.0.0-99")); got != 1 {
		t.Errorf("a numeric identifier above uint64 compares %d, want 1", got)
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var stepReleases = []Release{
	{Version: "1.5.0", Channel: "stable"},
	{Version: "1.2.0", Channel: "stable"},
	{Version: "1.2.4", Channel: "stable"},
	{Version: "1.3.0", Channel: "stable"},
	{Version: "1.3.2", Channel: "stable"},
	{Version: "1.3.3-beta.1", Channel: "beta"},
	{Version: "1.4.1", Channel: "stable"},
	{Version: "1.4.0", Channel: "stable"},
	{Version: "1.6.0-beta.1", Channel: "beta"},
	{Version: "1.8.0", Channel: "stable"},
	{Version: "2.0.0", Channel: "stable"},
	{Version: "not-a-version", Channel: "stable"},
	{Version: "1.9.0", Channel: "nightly"},
}

func TestInChannel(t *testing.T) {
	versions := func(rs []Release) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.Version)
		}
		return out
	}
	stable := []string{"1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.4.0", "1.4.1", "1.5.0", "1.8.0", "2.0.0"}
	beta := []string{"1.2.0", "1.2.4", "1.3.0", "1.3.2", "1.3.3-beta.1", "1.4.0", "1.4.1", "1.5.0", "1.6.0-beta.1", "1.8.0", "2.0.0"}
	if got := versions(InChannel(stepReleases, "stable")); !reflect.DeepEqual(got, stable) {
		t.Errorf("stable = %v, want %v", got, stable)
	}
	if got := versions(InChannel(stepReleases, "beta")); !reflect.DeepEqual(got, beta) {
		t.Errorf("beta = %v, want %v", got, beta)
	}
	if got := InChannel(stepReleases, "nightly"); len(got) != 0 {
		t.Errorf("unknown channel = %v, want none", got)
	}
}

func TestSteps(t *testing.T) {
	tests := []struct {
		name            string
		current, target string
		channel         string
		force           bool
		want            []string
		code            errcode.Code
	}{
		{"one minor at a time", "1.2.0", "1.5.0", "stable", false, []string{"1.3.2", "1.4.1", "1.5.0"}, ""},
		{"beta steps through the newest beta", "1.2.0", "1.4.1", "beta", false, []string{"1.3.3-beta.1", "1.4.1"}, ""},
		{"patch of the same minor", "1.2.0", "1.2.4", "stable", false, []string{"1.2.4"}, ""},
		{"next minor", "1.4.0", "1.5.0", "stable", false, []string{"1.5.0"}, ""},
		{"beta release on a beta host", "1.5.0", "1.6.0-beta.1", "beta", false, []string{"1.6.0-beta.1"}, ""},
		{"prerelease to its release", "1.3.3-beta.1", "1.4.0", "beta", false, []string{"1.4.0"}, ""},
		{"forced downgrade of one minor", "1.5.0", "1.4.0", "stable", true, []string{"1.4.0"}, ""},
		{"forced downgrade of a patch", "1.2.4", "1.2.0", "stable", true, []string{"1.2.0"}, ""},
		{"forced downgrade of two minors", "1.5.0", "1.3.2", "stable", true, nil, errcode.UpdateDowngradeRefused},
		{"unforced downgrade", "1.5.0", "1.4.1", "stable", false, nil, errcode.UpdateDowngradeRefused},
		{"missing intermediate minor", "1.5.0", "1.8.0", "stable", false, nil, errcode.UpdateNotFound},
		{"target not in the channel", "1.5.0", "1.6.0-beta.1", "stable", false, nil, errcode.UpdateNotFound},
		{"target not published", "1.5.0", "1.5.9", "stable", false, nil, errcode.UpdateNotFound},
		{"another major", "1.8.0", "2.0.0", "stable", false, nil, errcode.InvalidInput},
		{"the running version", "1.5.0", "1.5.0", "stable", false, nil, errcode.InvalidInput},
		{"a development build", "dev", "1.5.0", "stable", false, nil, errcode.InvalidInput},
		{"an unknown channel", "1.2.0", "1.2.4", "nightly", false, nil, errcode.InvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Steps(tt.current, tt.target, tt.channel, stepReleases, tt.force)
			if tt.code != "" {
				var ce *errcode.Error
				if !errors.As(err, &ce) || ce.Code != tt.code {
					t.Fatalf("Steps = %v, %v; want %s", got, err, tt.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var versions []string
			for _, r := range got {
				versions = append(versions, r.Version)
			}
			if !reflect.DeepEqual(versions, tt.want) {
				t.Errorf("Steps = %v, want %v", versions, tt.want)
			}
		})
	}
}

func TestStepsKeepTheEntryChannel(t *testing.T) {
	got, err := Steps("1.2.0", "1.4.1", "beta", stepReleases, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []Release{{Version: "1.3.3-beta.1", Channel: "beta"}, {Version: "1.4.1", Channel: "stable"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Steps = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run 'TestParseVersion|TestVersionCompare|TestInChannel|TestSteps'`
Expected: FAIL — the package does not build: `internal/update/version_test.go:14:8: undefined: Version`

- [ ] **Step 3: Write the implementation**

Create `internal/update/version.go`:

```go
// Package update is the agent's self-updater (ADR 0007): it chooses releases
// from the release index, verifies and stages them, swaps the installed
// binary, health-checks the restarted services and rolls back, keeping one
// transaction record per step.
package update

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// versionPattern is the semantic version pattern of the update action inputs:
// no "v" prefix and no build metadata.
var versionPattern = regexp.MustCompile(
	`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

// Version is a parsed semantic version.
type Version struct {
	Major, Minor, Patch uint64
	Pre                 string // prerelease without the leading "-"; empty for a release
}

// ParseVersion parses s, which must match the update actions' semantic
// version pattern.
func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("update: %q is not a semantic version", s)
	}
	var v Version
	for i, dst := range []*uint64{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.ParseUint(m[i+1], 10, 64)
		if err != nil {
			return Version{}, fmt.Errorf("update: %q is not a semantic version: %w", s, err)
		}
		*dst = n
	}
	v.Pre = m[4]
	return v, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders v and w by semantic version precedence: -1 when v is lower,
// 0 when they are equal, 1 when v is higher.
func (v Version) Compare(w Version) int {
	if c := cmp.Compare(v.Major, w.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Minor, w.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Patch, w.Patch); c != 0 {
		return c
	}
	switch {
	case v.Pre == w.Pre:
		return 0
	case v.Pre == "":
		return 1
	case w.Pre == "":
		return -1
	}
	a, b := strings.Split(v.Pre, "."), strings.Split(w.Pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePrerelease(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

// comparePrerelease orders two prerelease identifiers: numeric ones by value,
// below every alphanumeric one, which compare as ASCII. Numbers are compared
// as digit strings, so an identifier too large for uint64 still orders.
func comparePrerelease(a, b string) int {
	an, bn := isDigits(a), isDigits(b)
	switch {
	case an && bn:
		a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
		if c := cmp.Compare(len(a), len(b)); c != 0 {
			return c
		}
		return strings.Compare(a, b)
	case an:
		return -1
	case bn:
		return 1
	}
	return strings.Compare(a, b)
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// Release is one entry of the release index: a published version and the
// channel its signature was made for.
type Release struct {
	Version string `json:"version"`
	Channel string `json:"channel"`
}

// InChannel returns the releases a host on channel may take, by ascending
// precedence: stable keeps the stable entries, beta keeps the stable and beta
// entries, and any other channel keeps none. Entries whose version does not
// parse are dropped.
func InChannel(releases []Release, channel string) []Release {
	type entry struct {
		r Release
		v Version
	}
	var kept []entry
	for _, r := range releases {
		switch {
		case r.Channel == core.ChannelStable && (channel == core.ChannelStable || channel == core.ChannelBeta):
		case r.Channel == core.ChannelBeta && channel == core.ChannelBeta:
		default:
			continue
		}
		v, err := ParseVersion(r.Version)
		if err != nil {
			continue
		}
		kept = append(kept, entry{r, v})
	}
	slices.SortStableFunc(kept, func(a, b entry) int { return a.v.Compare(b.v) })
	out := make([]Release, len(kept))
	for i, e := range kept {
		out[i] = e.r
	}
	return out
}

// Steps returns the releases an update from current to target passes through,
// target last, chosen from InChannel(releases, channel). An update never
// crosses more than one minor version at a time (ADR 0007): it steps through
// the newest release of every minor in between. A downgrade needs force and
// goes back at most one minor, in one step. Updates between major versions
// are refused, because the agent support window and the schema
// compatibility rules hold only within a major.
func Steps(current, target, channel string, releases []Release, force bool) ([]Release, error) {
	if channel != core.ChannelStable && channel != core.ChannelBeta {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("unknown release channel %q", channel))
	}
	cur, err := ParseVersion(current)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("this binary's version %s is not a release version, so it cannot be updated", current))
	}
	want, err := ParseVersion(target)
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("%q is not a semantic version", target))
	}
	avail := InChannel(releases, channel)
	idx := slices.IndexFunc(avail, func(r Release) bool {
		v, _ := ParseVersion(r.Version)
		return v.Compare(want) == 0
	})
	if idx < 0 {
		return nil, errcode.New(errcode.UpdateNotFound,
			fmt.Sprintf("release %s is not published in the %s channel", target, channel))
	}
	entry := avail[idx]
	if want.Major != cur.Major {
		return nil, errcode.New(errcode.InvalidInput,
			fmt.Sprintf("updates from %s to %s cross a major version, which is not supported", current, target))
	}
	switch c := want.Compare(cur); {
	case c == 0:
		return nil, errcode.New(errcode.InvalidInput, fmt.Sprintf("version %s is already running", current))
	case c < 0 && !force:
		return nil, errcode.New(errcode.UpdateDowngradeRefused,
			fmt.Sprintf("%s is older than the running %s; a downgrade needs force", target, current))
	case c < 0 && cur.Minor-want.Minor > 1:
		return nil, errcode.New(errcode.UpdateDowngradeRefused,
			fmt.Sprintf("a forced downgrade goes back at most one minor version, and %s is further behind %s", target, current))
	case c < 0:
		return []Release{entry}, nil
	}
	var steps []Release
	for minor := cur.Minor + 1; minor < want.Minor; minor++ {
		var newest *Release
		for i := range avail {
			v, _ := ParseVersion(avail[i].Version)
			if v.Major == cur.Major && v.Minor == minor {
				newest = &avail[i]
			}
		}
		if newest == nil {
			return nil, errcode.New(errcode.UpdateNotFound,
				fmt.Sprintf("no release of %d.%d is published in the %s channel, so %s cannot be reached one minor at a time",
					cur.Major, minor, channel, target))
		}
		steps = append(steps, *newest)
	}
	return append(steps, entry), nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/version.go internal/update/version_test.go
git commit -m "feat(update): add versions and update steps"
```

### Task 2: Enrollment rate limiter

**Files:**
- Create: `internal/controller/fleet/tokens_limit.go`
- Test: `internal/controller/fleet/tokens_limit_test.go`

**Interfaces:**
- Consumes: `core.Clock`, `coretest.NewFakeClock`, `errcode.EnrollRateLimited` (all on main).
- Produces: C10; creates package `fleet` and carries its package comment. Top-level names added: `EnrollPerSource`, `EnrollGlobal`, `enrollWindow`, `EnrollLimiter`, `NewEnrollLimiter`, `(*EnrollLimiter).Allow`, `(*EnrollLimiter).prune`, `dropBefore`; tests: `limiterStart`, `wantRateLimited`, `TestEnrollLimiterPerSource`, `TestEnrollLimiterGlobal`, `TestEnrollLimiterWindowSlides`, `TestEnrollLimiterDoesNotCountRefusals`, `TestEnrollLimiterForgetsQuietSources`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/controller/fleet/tokens_limit_test.go`:

```go
package fleet

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// limiterStart is the fake clock's start for the limiter tests.
var limiterStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func wantRateLimited(t *testing.T, err error, what string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.EnrollRateLimited {
		t.Fatalf("%s: error = %v, want %s", what, err, errcode.EnrollRateLimited)
	}
}

func TestEnrollLimiterPerSource(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for i := range EnrollPerSource {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "request above the per-source limit")
	if err := l.Allow("192.0.2.2"); err != nil {
		t.Fatalf("another source was refused: %v", err)
	}
}

func TestEnrollLimiterGlobal(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for i := range EnrollGlobal {
		source := fmt.Sprintf("198.51.100.%d", i/EnrollPerSource)
		if err := l.Allow(source); err != nil {
			t.Fatalf("request %d from %s: %v", i+1, source, err)
		}
	}
	wantRateLimited(t, l.Allow("203.0.113.9"), "request above the global limit")
	clock.Advance(time.Minute)
	if err := l.Allow("203.0.113.9"); err != nil {
		t.Fatalf("request a minute later: %v", err)
	}
}

func TestEnrollLimiterWindowSlides(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for range EnrollPerSource / 2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(30 * time.Second)
	for range EnrollPerSource - EnrollPerSource/2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "full window")
	// The first half leaves the window exactly one minute after it was admitted.
	clock.Advance(30 * time.Second)
	for i := range EnrollPerSource / 2 {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatalf("request %d after the first half expired: %v", i+1, err)
		}
	}
	wantRateLimited(t, l.Allow("192.0.2.1"), "refilled window")
}

func TestEnrollLimiterDoesNotCountRefusals(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for range EnrollPerSource {
		if err := l.Allow("192.0.2.1"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(59 * time.Second)
	for range 100 {
		wantRateLimited(t, l.Allow("192.0.2.1"), "refused request")
	}
	clock.Advance(time.Second)
	if err := l.Allow("192.0.2.1"); err != nil {
		t.Fatalf("refused requests extended the limit: %v", err)
	}
}

func TestEnrollLimiterForgetsQuietSources(t *testing.T) {
	clock := coretest.NewFakeClock(limiterStart)
	l := NewEnrollLimiter(clock)
	for _, s := range []string{"192.0.2.1", "192.0.2.2", "192.0.2.3"} {
		if err := l.Allow(s); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute)
	if err := l.Allow("192.0.2.4"); err != nil {
		t.Fatal(err)
	}
	if n := len(l.sources); n != 1 {
		t.Fatalf("tracked sources = %d after a quiet minute, want 1", n)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: FAIL — the build fails with `undefined: NewEnrollLimiter` and `undefined: EnrollPerSource`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/fleet/tokens_limit.go`. It carries the package comment because it is the package's first file; golangci-lint's revive `package-comments` rule fails the package without one.

```go
// Package fleet manages the controller's agents: the agent certificate
// authority, enrollment and the agents' sessions with the controller.
package fleet

import (
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Enrollment rate limits (spec section 3), counted over a sliding minute.
const (
	EnrollPerSource = 10 // requests per source address per minute
	EnrollGlobal    = 60 // requests from all sources per minute
)

const enrollWindow = time.Minute

// EnrollLimiter admits enrollment requests in memory. Only admitted requests
// are kept, so it never holds more than EnrollGlobal times however many
// sources a flood uses, and a client that keeps retrying while refused does
// not push its own limit further out.
type EnrollLimiter struct {
	mu      sync.Mutex
	clock   core.Clock
	all     []time.Time            // admitted request times, oldest first
	sources map[string][]time.Time // the same times by source, oldest first
}

// NewEnrollLimiter returns a limiter that reads time from clock.
func NewEnrollLimiter(clock core.Clock) *EnrollLimiter {
	return &EnrollLimiter{clock: clock, sources: map[string][]time.Time{}}
}

// Allow admits one enrollment request from source (an IP address without a
// port) or fails with enroll.rate_limited when the last minute already holds
// EnrollPerSource admitted requests from source or EnrollGlobal from all
// sources. A refused request is not counted.
func (l *EnrollLimiter) Allow(source string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.prune(now)
	if len(l.sources[source]) >= EnrollPerSource || len(l.all) >= EnrollGlobal {
		return errcode.New(errcode.EnrollRateLimited, "too many enrollment requests; wait a minute and try again")
	}
	l.all = append(l.all, now)
	l.sources[source] = append(l.sources[source], now)
	return nil
}

// prune drops every time that left the window and every source left with
// none.
func (l *EnrollLimiter) prune(now time.Time) {
	cutoff := now.Add(-enrollWindow)
	l.all = dropBefore(l.all, cutoff)
	for s, times := range l.sources {
		if times = dropBefore(times, cutoff); len(times) == 0 {
			delete(l.sources, s)
		} else {
			l.sources[s] = times
		}
	}
}

// dropBefore removes the leading times at or before cutoff: a request
// admitted exactly one minute ago has left the window.
func dropBefore(times []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	return times[i:]
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: `ok  github.com/darkraise/darkpanel/internal/controller/fleet`

Run: `GOTOOLCHAIN=local go vet ./internal/controller/fleet/ && golangci-lint run --allow-serial-runners ./internal/controller/fleet/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` and no other output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/fleet/tokens_limit.go internal/controller/fleet/tokens_limit_test.go
git commit -m "feat(fleet): add the enrollment rate limiter"
```

### Task 3: Health probes

**Files:**
- Create: `internal/update/health.go`
- Test: `internal/update/health_test.go`

**Interfaces:**
- Consumes: F-C4 (`core.AgentHello.BinaryVersion`, `core.AgentHello.ControllerConnected`), `core.Clock`, `coretest.NewFakeClock` and `FakeClock.Pending`.
- Produces: C13 (`HealthDeadline`, `HealthInterval`, `ControllerHealth`, `ControllerProbe`, `Probes`, `Probes.Wait`). Top-level names added: `HealthDeadline`, `HealthInterval`, `maxHealthBytes`, `ControllerHealth`, `ControllerProbe`, `Probes` (methods `Wait`, `check`), `sleepOn`; tests: `healthAnswers` (method `probe`), `healthAnswer`, `healthCtrl`, `healthCtrlErr`, `healthHello`, `healthAgentErr`, `runHealthWait`, `TestProbesWait`, `TestProbesWaitStops`, `TestControllerProbe`. Task 19 reuses `sleepOn`.

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The task uses no name of Task 1, but it runs after Task 1 merged, because Task 1's `version.go` carries the `internal/update` package comment that revive requires; `health.go` must not add its own. It shares a wave with Task 4, so its unexported names are prefixed so that they cannot meet Task 4's.

- [ ] **Step 1: Write the failing test**

Create `internal/update/health_test.go`:

```go
package update

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// healthAnswers returns the n-th answer on the n-th call and repeats the last
// one; it fails a probe called without a deadline.
type healthAnswers[T any] struct {
	mu      sync.Mutex
	calls   int
	answers []healthAnswer[T]
}

type healthAnswer[T any] struct {
	value T
	err   error
}

func (h *healthAnswers[T]) probe(ctx context.Context) (T, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var zero T
	if _, ok := ctx.Deadline(); !ok {
		return zero, errors.New("probe called without a deadline")
	}
	a := h.answers[min(h.calls, len(h.answers)-1)]
	h.calls++
	return a.value, a.err
}

func healthCtrl(version, status string) healthAnswer[ControllerHealth] {
	return healthAnswer[ControllerHealth]{value: ControllerHealth{Status: status, Version: version}}
}

func healthCtrlErr(err error) healthAnswer[ControllerHealth] {
	return healthAnswer[ControllerHealth]{err: err}
}

func healthHello(version string, connected *bool) healthAnswer[core.AgentHello] {
	return healthAnswer[core.AgentHello]{value: core.AgentHello{BinaryVersion: version, ControllerConnected: connected}}
}

func healthAgentErr(err error) healthAnswer[core.AgentHello] {
	return healthAnswer[core.AgentHello]{err: err}
}

// runHealthWait runs Wait with a deadline HealthDeadline ahead and advances
// the fake clock one HealthInterval whenever Wait sleeps. It returns how many
// times the clock had to move and Wait's error.
func runHealthWait(t *testing.T, p Probes) (int, error) {
	t.Helper()
	clock := coretest.NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	deadline := clock.Now().Add(HealthDeadline)
	done := make(chan error, 1)
	go func() { done <- p.Wait(context.Background(), clock, "1.2.0", "1.3.0", deadline) }()
	advances := 0
	for {
		select {
		case err := <-done:
			return advances, err
		case <-time.After(time.Millisecond):
		}
		if clock.Pending() > 0 {
			clock.Advance(HealthInterval)
			advances++
		}
		if advances > 100 {
			t.Fatal("Wait did not stop after the deadline")
		}
	}
}

func TestProbesWait(t *testing.T) {
	yes, no := true, false
	deadlineAdvances := int(HealthDeadline / HealthInterval)
	tests := []struct {
		name       string
		controller []healthAnswer[ControllerHealth] // nil: the host has no controller
		agent      []healthAnswer[core.AgentHello]
		wantErr    string
		advances   int
	}{
		{"both pass at once", []healthAnswer[ControllerHealth]{healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 0},
		{"controller refused, then old, then new", []healthAnswer[ControllerHealth]{
			healthCtrlErr(errors.New("connection refused")), healthCtrl("1.2.0", "ok"), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 2},
		{"controller timeout is not yet", []healthAnswer[ControllerHealth]{
			healthCtrlErr(context.DeadlineExceeded), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 1},
		{"new controller degraded fails at once", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "ok"), healthCtrl("1.3.0", "degraded")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, `status "degraded"`, 1},
		{"old controller degraded is not yet", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "degraded"), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 1},
		{"controller never moves", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "deadline", deadlineAdvances},
		{"agent refused, then old, then new", nil, []healthAnswer[core.AgentHello]{
			healthAgentErr(errors.New("connection refused")), healthHello("1.2.0", &yes), healthHello("1.3.0", &yes)}, "", 2},
		{"agent not connected to the controller yet", nil, []healthAnswer[core.AgentHello]{
			healthHello("1.3.0", &no), healthHello("1.3.0", &yes)}, "", 1},
		{"unenrolled agent passes on its version", nil, []healthAnswer[core.AgentHello]{healthHello("1.3.0", nil)}, "", 0},
		{"agent never connects", []healthAnswer[ControllerHealth]{healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &no)}, "deadline", deadlineAdvances},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p Probes
			if tt.controller != nil {
				p.Controller = (&healthAnswers[ControllerHealth]{answers: tt.controller}).probe
			}
			p.Agent = (&healthAnswers[core.AgentHello]{answers: tt.agent}).probe
			advances, err := runHealthWait(t, p)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Wait = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Wait = %v, want an error mentioning %q", err, tt.wantErr)
			}
			if advances != tt.advances {
				t.Errorf("the clock moved %d times, want %d", advances, tt.advances)
			}
		})
	}
}

func TestProbesWaitStops(t *testing.T) {
	clock := coretest.NewFakeClock(time.Unix(0, 0))
	if err := (Probes{}).Wait(context.Background(), clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute)); err == nil {
		t.Error("Wait without an agent probe passed")
	}
	p := Probes{Agent: (&healthAnswers[core.AgentHello]{answers: []healthAnswer[core.AgentHello]{healthHello("1.2.0", nil)}}).probe}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Wait(ctx, clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute)) }()
	for clock.Pending() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Wait after cancel = %v, want context.Canceled", err)
	}
}

func TestControllerProbe(t *testing.T) {
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "controller.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	var mu sync.Mutex
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.3.0","commit":"abc","schemaVersion":5}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := ControllerProbe(socket)
	got, err := probe(ctx)
	if err != nil || got != (ControllerHealth{Status: "ok", Version: "1.3.0"}) {
		t.Fatalf("probe = %+v, %v", got, err)
	}
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	if _, err := probe(ctx); err == nil {
		t.Error("a 503 answer was accepted")
	}
	if _, err := ControllerProbe(filepath.Join(dir, "missing.sock"))(ctx); err == nil {
		t.Error("a missing socket was accepted")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run 'TestProbesWait|TestControllerProbe'`
Expected: FAIL — `internal/update/health_test.go:44:54: undefined: ControllerHealth`

- [ ] **Step 3: Write the implementation**

Create `internal/update/health.go`:

```go
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// The health window after a swap (spec section 8, Time budget): the deadline
// timer fires HealthDeadline after the swap, and the runner polls every
// HealthInterval until then.
const (
	HealthDeadline = 80 * time.Second
	HealthInterval = 2 * time.Second
)

// maxHealthBytes bounds the controller's /healthz answer.
const maxHealthBytes = 64 << 10

// ControllerHealth is the part of the controller socket's /healthz answer
// the health rule reads.
type ControllerHealth struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// ControllerProbe reads GET /healthz over the controller socket at socket,
// which answers the version with no credentials.
func ControllerProbe(socket string) func(ctx context.Context) (ControllerHealth, error) {
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socket)
		},
		DisableKeepAlives: true,
	}}
	return func(ctx context.Context) (ControllerHealth, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://controller/healthz", nil)
		if err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return ControllerHealth{}, fmt.Errorf("update: controller health answered %s", resp.Status)
		}
		var h ControllerHealth
		if err := json.NewDecoder(io.LimitReader(resp.Body, maxHealthBytes)).Decode(&h); err != nil {
			return ControllerHealth{}, fmt.Errorf("update: controller health: %w", err)
		}
		return h, nil
	}
}

// Probes are the health checks of the restarted services.
type Probes struct {
	// Controller is nil when the host has no controller unit.
	Controller func(ctx context.Context) (ControllerHealth, error)
	Agent      func(ctx context.Context) (core.AgentHello, error)
}

// Wait applies the health rule (spec section 8) every HealthInterval of
// clock until both probes pass, which returns nil. A refused connection, a
// timeout or a report of version from is "not yet". A controller reporting
// version to with a status other than ok fails at once; still not passing at
// deadline fails too.
func (p Probes) Wait(ctx context.Context, clock core.Clock, from, to string, deadline time.Time) error {
	if p.Agent == nil {
		return errors.New("update: health check: no agent probe")
	}
	for {
		passed, err := p.check(ctx, to)
		if err != nil {
			return err
		}
		if passed {
			return nil
		}
		now := clock.Now()
		if !now.Before(deadline) {
			return fmt.Errorf("update: health check: the services did not report version %s healthy by the deadline (they may still run %s)", to, from)
		}
		if err := sleepOn(ctx, clock, min(HealthInterval, deadline.Sub(now))); err != nil {
			return err
		}
	}
}

// check runs each probe once, each bounded by HealthInterval, and reports
// whether both passed.
func (p Probes) check(ctx context.Context, to string) (bool, error) {
	controllerOK := true
	if p.Controller != nil {
		pctx, cancel := context.WithTimeout(ctx, HealthInterval)
		h, err := p.Controller(pctx)
		cancel()
		switch {
		case err != nil || h.Version != to:
			controllerOK = false
		case h.Status != "ok":
			return false, fmt.Errorf("update: health check: the controller reports version %s with status %q", to, h.Status)
		}
	}
	pctx, cancel := context.WithTimeout(ctx, HealthInterval)
	hello, err := p.Agent(pctx)
	cancel()
	agentOK := err == nil && hello.BinaryVersion == to &&
		(hello.ControllerConnected == nil || *hello.ControllerConnected)
	return controllerOK && agentOK, nil
}

// sleepOn waits d on clock, or until ctx ends.
func sleepOn(ctx context.Context, clock core.Clock, d time.Duration) error {
	fired := make(chan struct{})
	t := clock.AfterFunc(d, func() { close(fired) })
	select {
	case <-fired:
		return nil
	case <-ctx.Done():
		t.Stop()
		return ctx.Err()
	}
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/health.go internal/update/health_test.go
git commit -m "feat(update): add the update health probes"
```

### Task 4: Release index source

**Files:**
- Create: `internal/update/index.go`
- Test: `internal/update/index_test.go`

**Interfaces:**
- Consumes: C11 (`Release`, Task 1), `coretest.NewFakeClock`.
- Produces: C12 (`IndexTTL`, `Index`, `IndexSource`, `NewIndexSource`, `IndexSource.Releases`). Top-level names added: `IndexTTL`, `maxIndexBytes`, `Index`, `IndexSource` (methods `Releases`, `fetch`), `NewIndexSource`; tests: `indexServer`, `indexBody`, `TestIndexSourceCachesForAnHour`, `TestIndexSourceDoesNotCacheFailures`, `TestIndexSourceRefusesBadIndexes`.

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/update/index_test.go`:

```go
package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// indexServer answers /index/releases.json with the body and status that
// answer returns for the n-th request, counting from 1.
func indexServer(t *testing.T, answer func(n int64) (int, string)) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index/releases.json" {
			http.NotFound(w, r)
			return
		}
		status, body := answer(hits.Add(1))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

const indexBody = `{"formatVersion": 1, "releases": [{"version": "1.2.0", "channel": "stable"}, {"version": "1.3.0-beta.1", "channel": "beta"}]}`

func TestIndexSourceCachesForAnHour(t *testing.T) {
	srv, hits := indexServer(t, func(int64) (int, string) { return 200, indexBody })
	clock := coretest.NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	src := NewIndexSource(srv.URL+"/", srv.Client(), clock)
	ctx := context.Background()
	want := []Release{{Version: "1.2.0", Channel: "stable"}, {Version: "1.3.0-beta.1", Channel: "beta"}}
	for _, step := range []struct {
		advance time.Duration
		hits    int64
	}{{0, 1}, {59 * time.Minute, 1}, {time.Minute, 2}, {30 * time.Minute, 2}} {
		clock.Advance(step.advance)
		got, err := src.Releases(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Releases = %+v, want %+v", got, want)
		}
		if hits.Load() != step.hits {
			t.Fatalf("after %v: %d fetches, want %d", step.advance, hits.Load(), step.hits)
		}
	}
	got, _ := src.Releases(ctx)
	got[0].Version = "9.9.9"
	if again, _ := src.Releases(ctx); again[0].Version != "1.2.0" {
		t.Error("a caller's change reached the cached index")
	}
}

func TestIndexSourceDoesNotCacheFailures(t *testing.T) {
	srv, hits := indexServer(t, func(n int64) (int, string) {
		if n == 1 {
			return 500, "boom"
		}
		return 200, indexBody
	})
	src := NewIndexSource(srv.URL, srv.Client(), coretest.NewFakeClock(time.Unix(0, 0)))
	if _, err := src.Releases(context.Background()); err == nil {
		t.Fatal("a 500 answer was accepted")
	}
	if _, err := src.Releases(context.Background()); err != nil {
		t.Fatalf("the second fetch failed: %v", err)
	}
	if hits.Load() != 2 {
		t.Errorf("%d fetches, want 2", hits.Load())
	}
}

func TestIndexSourceRefusesBadIndexes(t *testing.T) {
	tests := map[string]struct {
		status int
		body   string
		want   string
	}{
		"not found":     {404, "", "404"},
		"format 2":      {200, `{"formatVersion": 2, "releases": []}`, "format"},
		"no format":     {200, `{"releases": []}`, "format"},
		"not json":      {200, `<html>`, "decode"},
		"over one MiB":  {200, `{"formatVersion": 1, "releases": [], "pad": "` + strings.Repeat("x", 1<<20) + `"}`, "1048576"},
		"trailing data": {200, indexBody + ` {}`, "decode"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			srv, _ := indexServer(t, func(int64) (int, string) { return tt.status, tt.body })
			_, err := NewIndexSource(srv.URL, srv.Client(), coretest.NewFakeClock(time.Unix(0, 0))).Releases(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Releases error = %v, want one mentioning %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run TestIndexSource`
Expected: FAIL — `internal/update/index_test.go:39:9: undefined: NewIndexSource`

- [ ] **Step 3: Write the implementation**

Create `internal/update/index.go`:

```go
package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// IndexTTL is how long a fetched release index is reused. The index only
// selects versions; each release's own signature makes it trusted, so a stale
// index can delay an update but never install an unsigned one.
const IndexTTL = time.Hour

// maxIndexBytes bounds the release index, which lists one short entry per
// published version.
const maxIndexBytes = 1 << 20

// Index is the release index file <base>/index/releases.json.
type Index struct {
	FormatVersion int       `json:"formatVersion"`
	Releases      []Release `json:"releases"`
}

// IndexSource reads the release index, at most once per IndexTTL per process.
type IndexSource struct {
	url    string
	client *http.Client
	clock  core.Clock

	mu      sync.Mutex // serialises fetches, so callers within the hour share one
	cached  []Release
	fetched time.Time
	valid   bool
}

// NewIndexSource reads the index under baseURL, the release base URL.
func NewIndexSource(baseURL string, client *http.Client, clock core.Clock) *IndexSource {
	return &IndexSource{
		url:    strings.TrimSuffix(baseURL, "/") + "/index/releases.json",
		client: client,
		clock:  clock,
	}
}

// Releases returns the index's entries. A failed fetch is not cached, so the
// next call tries again.
func (s *IndexSource) Releases(ctx context.Context) ([]Release, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	// A clock set back before the last fetch also refetches.
	if s.valid && !now.Before(s.fetched) && now.Sub(s.fetched) < IndexTTL {
		return slices.Clone(s.cached), nil
	}
	releases, err := s.fetch(ctx)
	if err != nil {
		return nil, err
	}
	s.cached, s.fetched, s.valid = releases, now, true
	return slices.Clone(releases), nil
}

func (s *IndexSource) fetch(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: release index %s answered %s", s.url, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("update: release index: %w", err)
	}
	if len(body) > maxIndexBytes {
		return nil, fmt.Errorf("update: release index %s is over %d bytes", s.url, maxIndexBytes)
	}
	var idx Index
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&idx); err != nil {
		return nil, fmt.Errorf("update: release index: decode: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("update: release index: decode: data after the index object")
	}
	if idx.FormatVersion != 1 {
		return nil, fmt.Errorf("update: release index has format %d, want 1", idx.FormatVersion)
	}
	return idx.Releases, nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/index.go internal/update/index_test.go
git commit -m "feat(update): add the release index source"
```

### Task 5: Transport identity, token and enrollment bodies

**Files:**
- Create: `internal/transport/identity.go`
- Create: `internal/transport/token.go`
- Test: `internal/transport/identity_test.go`

**Interfaces:**
- Consumes: F-C2 (`errcode.EnrollTokenInvalid`).
- Produces: C2. Top-level names added: `ALPNAgent`, `ALPNEnroll`, `EnrollPath`, `RenewPath`, `TokenPrefix`, `HostURI`, `ControllerURI`, `HostIDOf`, `SerialText`, `Fingerprint`, `Token` (`String`, `SecretHash`), `ParseToken`, `EnrollRequest`, `EnrollResponse`, `RenewRequest`, `RenewResponse`; unexported `hostURIPrefix`, `controllerURIPrefix`, `idPattern`, `secretSize`, `fingerprintPattern`, `malformed`; tests `namedCert`, `validToken`, `encode`, `TestURIs`, `TestHostIDOf`, `TestSerialAndFingerprint`, `TestTokenRoundTrip`, `TestParseTokenRefuses`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The package is new; `identity.go` carries its package comment. The enrollment bodies live in `token.go` beside the token, and both files share one test file.

- [ ] **Step 1: Write the failing test**

`internal/transport/identity_test.go`:

```go
package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// namedCert returns a self-signed certificate carrying the given URI, DNS
// and IP names.
func namedCert(t *testing.T, uris, dns []string, ips []net.IP) *x509.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	for _, s := range uris {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestURIs(t *testing.T) {
	if got := HostURI("01HOST"); got != "darkpanel://host/01HOST" {
		t.Errorf("HostURI = %q", got)
	}
	if got := ControllerURI("01CTRL"); got != "darkpanel://controller/01CTRL" {
		t.Errorf("ControllerURI = %q", got)
	}
}

func TestHostIDOf(t *testing.T) {
	long := strings.Repeat("A", 64)
	tests := []struct {
		name   string
		uris   []string
		dns    []string
		ips    []net.IP
		wantID string // empty: refused
	}{
		{"host", []string{"darkpanel://host/01JABCDEF"}, nil, nil, "01JABCDEF"},
		{"64 characters", []string{"darkpanel://host/" + long}, nil, nil, long},
		{"65 characters", []string{"darkpanel://host/" + long + "A"}, nil, nil, ""},
		{"no URI", nil, nil, nil, ""},
		{"two URIs", []string{"darkpanel://host/A1", "darkpanel://host/B2"}, nil, nil, ""},
		{"controller URI", []string{"darkpanel://controller/A1"}, nil, nil, ""},
		{"empty id", []string{"darkpanel://host/"}, nil, nil, ""},
		{"nested path", []string{"darkpanel://host/A1/B2"}, nil, nil, ""},
		{"punctuation", []string{"darkpanel://host/A-1"}, nil, nil, ""},
		{"query", []string{"darkpanel://host/A1?x=1"}, nil, nil, ""},
		{"fragment", []string{"darkpanel://host/A1#x"}, nil, nil, ""},
		{"user info", []string{"darkpanel://u@host/A1"}, nil, nil, ""},
		{"port", []string{"darkpanel://host:1/A1"}, nil, nil, ""},
		{"other scheme", []string{"https://host/A1"}, nil, nil, ""},
		{"escaped path", []string{"darkpanel://host/A%31"}, nil, nil, ""},
		{"DNS name too", []string{"darkpanel://host/A1"}, []string{"agent.example"}, nil, ""},
		{"IP address too", []string{"darkpanel://host/A1"}, nil, []net.IP{net.IPv4(127, 0, 0, 1)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := HostIDOf(namedCert(t, tt.uris, tt.dns, tt.ips))
			switch {
			case tt.wantID == "" && err == nil:
				t.Fatalf("HostIDOf = %q, want an error", id)
			case tt.wantID != "" && (err != nil || id != tt.wantID):
				t.Fatalf("HostIDOf = %q, %v; want %q", id, err, tt.wantID)
			}
		})
	}
}

func TestSerialAndFingerprint(t *testing.T) {
	serial, _ := new(big.Int).SetString("00ab01", 16)
	if got := SerialText(serial); got != "ab01" {
		t.Errorf("SerialText = %q, want ab01", got)
	}
	cert := namedCert(t, nil, nil, nil)
	sum := sha256.Sum256(cert.Raw)
	if got := Fingerprint(cert); got != hex.EncodeToString(sum[:]) {
		t.Errorf("Fingerprint = %q", got)
	}
}

// validToken is a well-formed token with a 16-byte secret.
func validToken() Token {
	return Token{
		Address:       "controller.example:7443",
		Fingerprint:   strings.Repeat("ab", 32),
		ControllerSAN: ControllerURI("01JCONTROLLER"),
		HostID:        "01JHOST",
		Secret:        base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
	}
}

// encode wraps a JSON text as a token.
func encode(json string) string {
	return TokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(json))
}

func TestTokenRoundTrip(t *testing.T) {
	want := validToken()
	text := want.String()
	if !strings.HasPrefix(text, "dpe1.") || strings.ContainsAny(text, "=+/") {
		t.Fatalf("String = %q, want dpe1. and base64url without padding", text)
	}
	got, err := ParseToken("  " + text + "\n")
	if err != nil || got != want {
		t.Fatalf("ParseToken = %+v, %v; want %+v", got, err, want)
	}
	hash, err := got.SecretHash()
	sum := sha256.Sum256([]byte("0123456789abcdef"))
	if err != nil || string(hash) != string(sum[:]) {
		t.Fatalf("SecretHash = %x, %v; want %x", hash, err, sum)
	}
}

func TestParseTokenRefuses(t *testing.T) {
	f := strings.Repeat("ab", 32)
	secret := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	member := func(a, f, s, h, t string) string {
		return `{"a":"` + a + `","f":"` + f + `","s":"` + s + `","h":"` + h + `","t":"` + t + `"}`
	}
	ctrl := ControllerURI("01JC")
	valid := validToken().String()
	tests := []struct{ name, text string }{
		{"empty", ""},
		{"wrong prefix", "dpe2." + strings.TrimPrefix(valid, TokenPrefix)},
		{"no prefix", strings.TrimPrefix(valid, TokenPrefix)},
		{"padded base64", valid + "=="},
		{"standard base64", TokenPrefix + base64.StdEncoding.EncodeToString([]byte(member("c:1", f, ctrl, "H1", secret)))},
		{"not JSON", encode("not json")},
		{"JSON array", encode(`["a"]`)},
		{"trailing JSON", encode(member("c:1", f, ctrl, "H1", secret) + "{}")},
		{"missing member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1"}`)},
		{"extra member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1","t":"` + secret + `","x":"y"}`)},
		{"number member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1","t":7}`)},
		{"null member", encode(`{"a":null,"f":"` + f + `","s":"` + ctrl + `","h":"H1","t":"` + secret + `"}`)},
		{"empty address", encode(member("", f, ctrl, "H1", secret))},
		{"address without port", encode(member("controller", f, ctrl, "H1", secret))},
		{"upper-case fingerprint", encode(member("c:1", strings.ToUpper(f), ctrl, "H1", secret))},
		{"short fingerprint", encode(member("c:1", f[:62], ctrl, "H1", secret))},
		{"host SAN", encode(member("c:1", f, HostURI("H1"), "H1", secret))},
		{"bare controller SAN", encode(member("c:1", f, ControllerURI(""), "H1", secret))},
		{"bad host id", encode(member("c:1", f, ctrl, "H-1", secret))},
		{"short secret", encode(member("c:1", f, ctrl, "H1", base64.RawURLEncoding.EncodeToString([]byte("short"))))},
		{"padded secret", encode(member("c:1", f, ctrl, "H1", secret+"=="))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := ParseToken(tt.text)
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseToken = %+v, %v; want %s", tok, err, errcode.EnrollTokenInvalid)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/transport/`
Expected: FAIL — `internal/transport/identity_test.go:55:12: undefined: HostURI` (and the other new names)

- [ ] **Step 3: Write the implementation**

`internal/transport/identity.go`:

```go
// Package transport is the connection between an agent and its controller
// below the agent API (ADR 0003): the identities both ends check, the
// enrollment token, the TLS policies, the yamux session and the heartbeat.
// It knows nothing about actions or databases; the controller passes the
// lookups it needs as callbacks.
package transport

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// Application protocols of the agent listener. The controller picks its TLS
// policy by the one the client offers.
const (
	ALPNAgent  = "darkpanel-agent/1"
	ALPNEnroll = "darkpanel-enroll/1"
)

// Routes outside the agent API.
const (
	// EnrollPath is posted to over an ALPNEnroll connection.
	EnrollPath = "/enroll/v1"
	// RenewPath is posted to over a stream the agent opens on its session.
	RenewPath = "/controller/v1/renew"
)

const (
	hostURIPrefix       = "darkpanel://host/"
	controllerURIPrefix = "darkpanel://controller/"
)

// idPattern is a host or controller id as a URI SAN carries it: ULIDs in
// production, short ids in tests, never anything a URI would escape.
var idPattern = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// HostURI is the URI SAN of an agent certificate.
func HostURI(hostID string) string { return hostURIPrefix + hostID }

// ControllerURI is the URI SAN of the controller's agent-listener
// certificate, which agents pin.
func ControllerURI(controllerID string) string { return controllerURIPrefix + controllerID }

// HostIDOf returns the host id an agent certificate names. The certificate
// must carry exactly one URI SAN, HostURI of an id of 1 to 64 letters and
// digits, and no DNS, IP or e-mail SAN, so nothing but the host id can
// identify the peer.
func HostIDOf(cert *x509.Certificate) (string, error) {
	if len(cert.DNSNames) > 0 || len(cert.IPAddresses) > 0 || len(cert.EmailAddresses) > 0 {
		return "", errors.New("transport: an agent certificate carries DNS, IP or e-mail names")
	}
	if len(cert.URIs) != 1 {
		return "", fmt.Errorf("transport: an agent certificate carries %d URI names, want 1", len(cert.URIs))
	}
	uri := cert.URIs[0].String()
	id, ok := strings.CutPrefix(uri, hostURIPrefix)
	// Comparing the whole text rejects a port, user info, query, fragment or
	// escaping that a field-by-field check could let through.
	if !ok || !idPattern.MatchString(id) || uri != HostURI(id) {
		return "", fmt.Errorf("transport: %q is not a host identity", uri)
	}
	return id, nil
}

// SerialText is a certificate serial as core_agent_certs stores it:
// lower-case hexadecimal without padding.
func SerialText(serial *big.Int) string { return serial.Text(16) }

// Fingerprint is the lower-case hex SHA-256 of a certificate's DER encoding.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
```

`internal/transport/token.go`:

```go
package transport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TokenPrefix starts every enrollment token; the digit is the format version.
const TokenPrefix = "dpe1."

// secretSize is the length of a token's secret in bytes: 128 bits, so a
// plain SHA-256 is enough to store it.
const secretSize = 16

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Token is an enrollment token's content. It tells the agent where to dial
// and which root and controller identity to accept before it sends
// anything, and gives the controller the host and the secret to check.
type Token struct {
	Address       string `json:"a"` // host:port the agent dials
	Fingerprint   string `json:"f"` // Fingerprint of the agent root
	ControllerSAN string `json:"s"` // ControllerURI of the controller id
	HostID        string `json:"h"`
	Secret        string `json:"t"` // 16 random bytes, base64url without padding
}

// String is the token as the operator copies it.
func (t Token) String() string {
	// A struct of strings always encodes.
	raw, _ := json.Marshal(t) //nolint:gosec // G117: the token exists to carry its secret
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// SecretHash is the SHA-256 of the decoded secret, the value
// core_enroll_tokens.hash stores.
func (t Token) SecretHash() ([]byte, error) {
	secret, err := base64.RawURLEncoding.Strict().DecodeString(t.Secret)
	if err != nil || len(secret) != secretSize {
		return nil, malformed()
	}
	sum := sha256.Sum256(secret)
	return sum[:], nil
}

// ParseToken reads a token the operator pasted. Every refusal is the same
// enroll.token_invalid, so a probe learns nothing about which part failed.
func ParseToken(s string) (Token, error) {
	body, ok := strings.CutPrefix(strings.TrimSpace(s), TokenPrefix)
	if !ok {
		return Token{}, malformed()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil {
		return Token{}, malformed()
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || len(members) != 5 {
		return Token{}, malformed()
	}
	values := make(map[string]string, len(members))
	for _, key := range []string{"a", "f", "s", "h", "t"} {
		m, ok := members[key]
		if !ok {
			return Token{}, malformed()
		}
		// A null member decodes without error and leaves v empty.
		var v string
		if err := json.Unmarshal(m, &v); err != nil || v == "" {
			return Token{}, malformed()
		}
		values[key] = v
	}
	t := Token{Address: values["a"], Fingerprint: values["f"], ControllerSAN: values["s"], HostID: values["h"], Secret: values["t"]}
	controllerID, isController := strings.CutPrefix(t.ControllerSAN, controllerURIPrefix)
	if _, _, err := net.SplitHostPort(t.Address); err != nil ||
		!fingerprintPattern.MatchString(t.Fingerprint) ||
		!isController || !idPattern.MatchString(controllerID) ||
		!idPattern.MatchString(t.HostID) {
		return Token{}, malformed()
	}
	if _, err := t.SecretHash(); err != nil {
		return Token{}, err
	}
	return t, nil
}

func malformed() error {
	return errcode.New(errcode.EnrollTokenInvalid, "the enrollment token is malformed")
}

// EnrollRequest is the body of POST EnrollPath.
type EnrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"` // PEM "CERTIFICATE REQUEST"
}

// EnrollResponse answers EnrollRequest.
type EnrollResponse struct {
	Certificate string `json:"certificate"` // PEM
	CA          string `json:"ca"`          // PEM of the agent root
}

// RenewRequest is the body of POST RenewPath.
type RenewRequest struct {
	CSR string `json:"csr"`
}

// RenewResponse answers RenewRequest.
type RenewResponse struct {
	Certificate string `json:"certificate"`
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s ./internal/transport/`
Expected: PASS

Run: `GOTOOLCHAIN=local go vet ./internal/transport/ && golangci-lint run --allow-serial-runners ./internal/transport/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/transport/identity_test.go internal/transport/identity.go internal/transport/token.go
git commit -m "feat(transport): add identities and enrollment token"
```

### Task 6: Controller TLS policy and handshake

**Files:**
- Create: `internal/transport/server.go`
- Test: `internal/transport/pki_test.go`
- Test: `internal/transport/server_test.go`

**Interfaces:**
- Consumes: C2 (`ALPNAgent`, `ALPNEnroll`, `HostIDOf`, `SerialText`, `HostURI`, `ControllerURI`), Task 5.
- Produces: C3; the C21 test helpers of `pki_test.go`. Top-level names added: `PeerCheck`, `ServerConfig`, `Peer`, `Handshake`; unexported `policy`, `verifyAgent`; tests `testCA`, `newTestCA`, `certSpec`, `serverConfig`, `dialer`, `tlsDialer`, `handshakeResult`, `handshake`, `handshakeCtx`, `rawClient`, `ctxKey`, `ptr`, `TestHandshakeAgent`, `TestHandshakeCheckPeerContext`, `TestHandshakeNoResumption`, `TestHandshakeEnroll`, `TestHandshakeRefuses`.

**Depends on:** Task 5

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`pki_test.go` is a test helper Tasks 7 to 9 reuse (C21); `serverConfig` admits every peer. `InsecureSkipVerify` appears only in test clients, which exercise the controller's policy alone.

- [ ] **Step 1: Write the failing test**

`internal/transport/pki_test.go`:

```go
package transport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"
)

// testCA is a throwaway Ed25519 authority for the TLS tests.
type testCA struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testCA{cert: cert, key: key}
}

// pool holds the root alone.
func (ca *testCA) pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// certSpec describes a leaf the test authority issues.
type certSpec struct {
	uris     []string
	dns      []string
	eku      []x509.ExtKeyUsage
	notAfter time.Time // zero: an hour from now
}

// issue returns the chain [leaf, root] with the leaf's key.
func (ca *testCA) issue(t *testing.T, s certSpec) tls.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	notAfter := s.notAfter
	if notAfter.IsZero() {
		notAfter = time.Now().Add(time.Hour)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		NotBefore:    time.Now().Add(-2 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  s.eku,
		DNSNames:     s.dns,
	}
	for _, raw := range s.uris {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key, Leaf: leaf}
}

// agentCert is a valid agent certificate for hostID.
func (ca *testCA) agentCert(t *testing.T, hostID string) tls.Certificate {
	return ca.issue(t, certSpec{uris: []string{HostURI(hostID)}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})
}

// controllerCert is a valid agent-listener certificate for controllerID.
func (ca *testCA) controllerCert(t *testing.T, controllerID string) tls.Certificate {
	return ca.issue(t, certSpec{uris: []string{ControllerURI(controllerID)}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
}

// serverConfig is a controller configuration presenting cert, trusting
// ca's agents and admitting every peer.
func serverConfig(ca *testCA, cert tls.Certificate) ServerConfig {
	return ServerConfig{
		Certificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return &cert, nil },
		AgentRoots:  ca.pool(),
		CheckPeer:   func(context.Context, string, string) error { return nil },
	}
}

// dialer opens a client connection to addr.
type dialer func(ctx context.Context, addr string) (*tls.Conn, error)

// tlsDialer dials with cfg.
func tlsDialer(cfg *tls.Config) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		d := tls.Dialer{Config: cfg}
		c, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		return c.(*tls.Conn), nil
	}
}

// handshakeResult is what each end of one test handshake saw.
type handshakeResult struct {
	peer      *Peer
	serverErr error
	client    *tls.Conn
	clientErr error
}

// handshake runs Handshake on one loopback connection that dial opens and
// closes both ends when the test ends.
func handshake(t *testing.T, cfg ServerConfig, dial dialer) handshakeResult {
	t.Helper()
	return handshakeCtx(context.Background(), t, cfg, dial)
}

// handshakeCtx is handshake with the server's Handshake run within ctx.
func handshakeCtx(ctx context.Context, t *testing.T, cfg ServerConfig, dial dialer) handshakeResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	type served struct {
		peer *Peer
		err  error
	}
	done := make(chan served, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			done <- served{nil, err}
			return
		}
		p, err := Handshake(ctx, raw, cfg)
		done <- served{p, err}
	}()
	var r handshakeResult
	r.client, r.clientErr = dial(ctx, ln.Addr().String())
	s := <-done
	r.peer, r.serverErr = s.peer, s.err
	t.Cleanup(func() {
		if r.client != nil {
			_ = r.client.Close()
		}
		if r.peer != nil {
			_ = r.peer.Conn.Close()
		}
	})
	return r
}
```

`internal/transport/server_test.go`:

```go
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// rawClient is a client that checks nothing about the controller, so these
// tests exercise the controller's policy alone.
func rawClient(protos []string, cert *tls.Certificate, maxVersion uint16) dialer {
	cfg := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         protos,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         maxVersion,
	}
	if cert != nil {
		c := *cert
		cfg.GetClientCertificate = func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &c, nil }
	}
	return tlsDialer(cfg)
}

type ctxKey struct{}

func TestHandshakeAgent(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	var gotHost, gotSerial string
	cfg.CheckPeer = func(_ context.Context, hostID, serial string) error {
		gotHost, gotSerial = hostID, serial
		return nil
	}
	r := handshake(t, cfg, rawClient([]string{ALPNAgent}, &cert, 0))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	serial := SerialText(cert.Leaf.SerialNumber)
	if r.peer.Protocol != ALPNAgent || r.peer.HostID != "01JHOST" || r.peer.Serial != serial {
		t.Errorf("peer = %+v, want agent 01JHOST %s", r.peer, serial)
	}
	if gotHost != "01JHOST" || gotSerial != serial {
		t.Errorf("CheckPeer got %q %q", gotHost, gotSerial)
	}
	if v := r.peer.Conn.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Errorf("version = %x, want TLS 1.3", v)
	}
}

func TestHandshakeCheckPeerContext(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	var got any
	cfg.CheckPeer = func(ctx context.Context, _, _ string) error {
		got = ctx.Value(ctxKey{})
		return nil
	}
	ctx := context.WithValue(context.Background(), ctxKey{}, "marker")
	r := handshakeCtx(ctx, t, cfg, rawClient([]string{ALPNAgent}, &cert, 0))
	if r.serverErr != nil {
		t.Fatal(r.serverErr)
	}
	if got != "marker" {
		t.Errorf("CheckPeer context value = %v, want the Handshake context's", got)
	}
}

func TestHandshakeNoResumption(t *testing.T) {
	ca := newTestCA(t)
	cert := ca.agentCert(t, "01JHOST")
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	checks := 0
	cfg.CheckPeer = func(context.Context, string, string) error {
		checks++
		return nil
	}
	client := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         []string{ALPNAgent},
		MinVersion:         tls.VersionTLS13,
		// A fixed server name keys the session cache across the two listeners.
		ServerName:           "controller",
		ClientSessionCache:   tls.NewLRUClientSessionCache(4),
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return &cert, nil },
	}
	for i := range 2 {
		r := handshake(t, cfg, tlsDialer(client))
		if r.serverErr != nil || r.clientErr != nil {
			t.Fatalf("handshake %d: server %v, client %v", i, r.serverErr, r.clientErr)
		}
		if r.client.ConnectionState().DidResume {
			t.Fatalf("handshake %d resumed a session, skipping the certificate checks", i)
		}
		// A read processes any session ticket the controller sent.
		if err := r.client.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		_, _ = r.client.Read(make([]byte, 1))
	}
	if checks != 2 {
		t.Errorf("CheckPeer ran %d times, want 2", checks)
	}
}

func TestHandshakeEnroll(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	cfg.CheckPeer = func(context.Context, string, string) error {
		t.Error("CheckPeer called for an enrollment connection")
		return nil
	}
	asked := false
	client := &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // G402: the controller's policy is under test
		NextProtos:         []string{ALPNEnroll},
		MinVersion:         tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			asked = true
			return &tls.Certificate{}, nil
		},
	}
	r := handshake(t, cfg, tlsDialer(client))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	if r.peer.Protocol != ALPNEnroll || r.peer.HostID != "" || r.peer.Serial != "" {
		t.Errorf("peer = %+v, want an anonymous enrollment peer", r.peer)
	}
	if asked {
		t.Error("the controller asked an enrolling client for a certificate")
	}
}

func TestHandshakeRefuses(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	clientAuth := []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	valid := ca.agentCert(t, "01JHOST")
	tests := []struct {
		name       string
		protos     []string
		cert       *tls.Certificate
		maxVersion uint16
		refuse     bool // CheckPeer refuses
	}{
		{name: "both protocols", protos: []string{ALPNAgent, ALPNEnroll}, cert: &valid},
		{name: "no protocol", protos: nil, cert: &valid},
		{name: "another protocol", protos: []string{"h2"}, cert: &valid},
		{name: "no client certificate", protos: []string{ALPNAgent}},
		{name: "another authority", protos: []string{ALPNAgent}, cert: ptr(other.agentCert(t, "01JHOST"))},
		{name: "serverAuth only", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}))},
		{name: "no extended key usage", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}}))},
		{name: "no URI", protos: []string{ALPNAgent}, cert: ptr(ca.issue(t, certSpec{eku: clientAuth}))},
		{name: "two URIs", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("A1"), HostURI("B2")}, eku: clientAuth}))},
		{name: "controller URI", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: clientAuth}))},
		{name: "DNS name too", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, dns: []string{"agent.example"}, eku: clientAuth}))},
		{name: "expired", protos: []string{ALPNAgent},
			cert: ptr(ca.issue(t, certSpec{uris: []string{HostURI("01JHOST")}, eku: clientAuth, notAfter: time.Now().Add(-time.Minute)}))},
		{name: "refused by the peer check", protos: []string{ALPNAgent}, cert: &valid, refuse: true},
		{name: "TLS 1.2", protos: []string{ALPNAgent}, cert: &valid, maxVersion: tls.VersionTLS12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
			if tt.refuse {
				cfg.CheckPeer = func(context.Context, string, string) error {
					return errcode.New(errcode.Forbidden, "certificate revoked")
				}
			}
			r := handshake(t, cfg, rawClient(tt.protos, tt.cert, tt.maxVersion))
			if r.serverErr == nil {
				t.Fatalf("Handshake accepted the client: %+v", r.peer)
			}
			t.Logf("refused: %v", r.serverErr)
			if r.client == nil {
				return
			}
			// The controller closed the connection: a read ends at once.
			if err := r.client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if _, err := r.client.Read(make([]byte, 1)); err == nil {
				t.Fatal("the refused connection is still open")
			}
		})
	}
}

func ptr[T any](v T) *T { return &v }
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/transport/`
Expected: FAIL — `internal/transport/pki_test.go:118:53: undefined: ServerConfig` (and `Peer`, `Handshake`)

- [ ] **Step 3: Write the implementation**

`internal/transport/server.go`:

```go
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"slices"
)

// PeerCheck decides whether a client certificate that verified against the
// agent root may connect: nil when hostID names an enrolled host and serial
// is one of its unrevoked, unexpired certificates. The transport has no
// database, so the controller supplies the lookup.
type PeerCheck func(ctx context.Context, hostID, serial string) error

// ServerConfig is the controller's side of the agent listener.
type ServerConfig struct {
	// Certificate returns the listener's chain [leaf, root].
	Certificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// AgentRoots holds the agent root alone; system roots never apply.
	AgentRoots *x509.CertPool
	CheckPeer  PeerCheck
}

// Peer is one accepted connection. For ALPNAgent the host id comes from the
// verified certificate and nothing else identifies the peer.
type Peer struct {
	Conn     *tls.Conn
	Protocol string // ALPNAgent or ALPNEnroll
	HostID   string // empty for ALPNEnroll
	Serial   string // SerialText of the client leaf; empty for ALPNEnroll
}

// Handshake runs the controller side of the TLS handshake on raw within ctx
// and closes raw on any failure.
func Handshake(ctx context.Context, raw net.Conn, cfg ServerConfig) (*Peer, error) {
	conn := tls.Server(raw, &tls.Config{
		MinVersion:             tls.VersionTLS13,
		SessionTicketsDisabled: true,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			return policy(hello, cfg)
		},
	})
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("transport: handshake with %s: %w", raw.RemoteAddr(), err)
	}
	state := conn.ConnectionState()
	peer := &Peer{Conn: conn, Protocol: state.NegotiatedProtocol}
	if peer.Protocol == ALPNAgent {
		leaf := state.PeerCertificates[0]
		// verifyAgent accepted this leaf, so its identity parses.
		peer.HostID, _ = HostIDOf(leaf)
		peer.Serial = SerialText(leaf.SerialNumber)
	}
	return peer, nil
}

// policy picks the configuration for the protocol the client offers. A
// client offering both could be steered to the weaker one, so it is refused.
func policy(hello *tls.ClientHelloInfo, cfg ServerConfig) (*tls.Config, error) {
	agent := slices.Contains(hello.SupportedProtos, ALPNAgent)
	enroll := slices.Contains(hello.SupportedProtos, ALPNEnroll)
	c := &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: cfg.Certificate,
		// A resumed session skips VerifyPeerCertificate, so a revoked
		// certificate could come back on a ticket.
		SessionTicketsDisabled: true,
	}
	switch {
	case agent && enroll:
		return nil, errors.New("transport: the client offers both the agent and the enrollment protocol")
	case agent:
		ctx := hello.Context()
		c.NextProtos = []string{ALPNAgent}
		c.ClientAuth = tls.RequireAnyClientCert
		c.VerifyPeerCertificate = func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyAgent(ctx, rawCerts, cfg)
		}
	case enroll:
		c.NextProtos = []string{ALPNEnroll}
	default:
		return nil, errors.New("transport: the client offers neither the agent nor the enrollment protocol")
	}
	return c, nil
}

// verifyAgent checks an agent's certificate against the agent root alone.
func verifyAgent(ctx context.Context, rawCerts [][]byte, cfg ServerConfig) error {
	if len(rawCerts) == 0 {
		return errors.New("transport: the agent presented no certificate")
	}
	// Only the leaf counts: the root issues agent certificates itself, so a
	// presented intermediate could only widen what verifies.
	leaf, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("transport: agent certificate: %w", err)
	}
	opts := x509.VerifyOptions{Roots: cfg.AgentRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("transport: agent certificate: %w", err)
	}
	// Verify lets a certificate without extended key usages through for any
	// usage.
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return errors.New("transport: the agent certificate lacks the clientAuth usage")
	}
	hostID, err := HostIDOf(leaf)
	if err != nil {
		return err
	}
	if err := cfg.CheckPeer(ctx, hostID, SerialText(leaf.SerialNumber)); err != nil {
		return fmt.Errorf("transport: host %s: %w", hostID, err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s -v -run 'TestHandshake' ./internal/transport/`
Expected: PASS. In the `-v` output every `TestHandshakeRefuses` subtest logs `refused:` with its own reason: both protocols, neither protocol (twice), `client didn't provide a certificate`, `signed by unknown authority`, `incompatible key usage`, `lacks the clientAuth usage`, `0 URI names`, `2 URI names`, `is not a host identity`, `DNS, IP or e-mail names`, `has expired`, `auth.forbidden: certificate revoked`, `unsupported versions: [303]`.

Run: `GOTOOLCHAIN=local go vet ./internal/transport/ && golangci-lint run --allow-serial-runners ./internal/transport/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/transport/pki_test.go internal/transport/server_test.go internal/transport/server.go
git commit -m "feat(transport): add the controller TLS policy"
```

### Task 7: Agent TLS, dial and redial backoff

**Files:**
- Create: `internal/transport/client.go`
- Create: `internal/transport/backoff.go`
- Test: `internal/transport/client_test.go`

**Interfaces:**
- Consumes: C2, C3 (`Handshake` through the Task 6 helpers), the C21 helpers of `pki_test.go`; Tasks 5 and 6.
- Produces: C4; the C21 helpers `agentDialer`, `enrollDialer`. Top-level names added: `AgentConfig`, `EnrollConfig`, `Dial`, `BackoffMin`, `BackoffMax`, `Backoff` (`Next`, `Reset`), `NewBackoff`; unexported `verifyController`; tests `agentDialer`, `enrollDialer`, `serveRaw`, `TestAgentConfigAccepts`, `TestAgentConfigRefuses`, `TestAgentConfigRequiresProtocol`, `TestEnrollConfig`, `TestEnrollConfigSendsNoCertificate`, `TestDialFails`, `TestBackoffCeilings`, `TestBackoffJitter`.

**Depends on:** Task 5, Task 6

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Go's hostname check cannot match a URI SAN, so both configurations set `InsecureSkipVerify` and do the whole check in `VerifyConnection`, as spec section 4 says; the `//nolint:gosec` lines carry that reason.

- [ ] **Step 1: Write the failing test**

`internal/transport/client_test.go`:

```go
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"
	"time"
)

// agentDialer dials with AgentConfig.
func agentDialer(cert tls.Certificate, ca *x509.Certificate, san string) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		return Dial(ctx, addr, AgentConfig(cert, ca, san))
	}
}

// enrollDialer dials with EnrollConfig.
func enrollDialer(fingerprint, san string) dialer {
	return func(ctx context.Context, addr string) (*tls.Conn, error) {
		return Dial(ctx, addr, EnrollConfig(fingerprint, san))
	}
}

func TestAgentConfigAccepts(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	r := handshake(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	if r.peer.HostID != "01JHOST" || r.client.ConnectionState().NegotiatedProtocol != ALPNAgent {
		t.Errorf("peer %+v, protocol %q", r.peer, r.client.ConnectionState().NegotiatedProtocol)
	}
}

func TestAgentConfigRefuses(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	serverAuth := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	tests := []struct {
		name string
		cert tls.Certificate
	}{
		{"another authority", other.controllerCert(t, "01JCTRL")},
		{"missing SAN", ca.issue(t, certSpec{eku: serverAuth})},
		{"different SAN", ca.controllerCert(t, "01JOTHER")},
		{"two SANs", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL"), ControllerURI("01JOTHER")}, eku: serverAuth})},
		{"clientAuth only", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}})},
		{"no extended key usage", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}})},
		{"expired", ca.issue(t, certSpec{uris: []string{ControllerURI("01JCTRL")}, eku: serverAuth, notAfter: time.Now().Add(-time.Minute)})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := handshake(t, serverConfig(ca, tt.cert), agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
			if r.clientErr == nil {
				t.Fatal("the agent accepted the controller")
			}
			t.Logf("refused: %v", r.clientErr)
		})
	}
}

// serveRaw accepts one connection with a plain TLS server using cfg and
// returns the server's connection state once its handshake ends.
func serveRaw(t *testing.T, cfg *tls.Config, dial dialer) (tls.ConnectionState, error, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	type served struct {
		state tls.ConnectionState
		err   error
	}
	done := make(chan served, 1)
	go func() {
		raw, err := ln.Accept()
		if err != nil {
			done <- served{err: err}
			return
		}
		conn := tls.Server(raw, cfg)
		t.Cleanup(func() { _ = conn.Close() })
		err = conn.HandshakeContext(ctx)
		done <- served{conn.ConnectionState(), err}
	}()
	client, clientErr := dial(ctx, ln.Addr().String())
	if client != nil {
		t.Cleanup(func() { _ = client.Close() })
	}
	s := <-done
	return s.state, s.err, clientErr
}

func TestAgentConfigRequiresProtocol(t *testing.T) {
	ca := newTestCA(t)
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{ca.controllerCert(t, "01JCTRL")},
		ClientAuth:   tls.RequireAnyClientCert,
	}
	_, _, clientErr := serveRaw(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if clientErr == nil {
		t.Fatal("the agent accepted a controller that negotiated no protocol")
	}
}

func TestEnrollConfig(t *testing.T) {
	ca := newTestCA(t)
	other := newTestCA(t)
	san := ControllerURI("01JCTRL")
	good := ca.controllerCert(t, "01JCTRL")
	// A leaf from another authority, presented with the pinned root.
	foreign := other.controllerCert(t, "01JCTRL")
	foreign.Certificate = [][]byte{foreign.Certificate[0], ca.cert.Raw}
	tests := []struct {
		name        string
		cert        tls.Certificate
		fingerprint string
		san         string
		ok          bool
	}{
		{"pinned root", good, Fingerprint(ca.cert), san, true},
		{"wrong fingerprint", good, Fingerprint(other.cert), san, false},
		{"different SAN", good, Fingerprint(ca.cert), ControllerURI("01JOTHER"), false},
		{"leaf of another authority", foreign, Fingerprint(ca.cert), san, false},
		{"clientAuth only", ca.issue(t, certSpec{uris: []string{san}, eku: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}), Fingerprint(ca.cert), san, false},
		{"root alone", tls.Certificate{Certificate: [][]byte{ca.cert.Raw}, PrivateKey: ca.key}, Fingerprint(ca.cert), san, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := handshake(t, serverConfig(ca, tt.cert), enrollDialer(tt.fingerprint, tt.san))
			if tt.ok != (r.clientErr == nil) {
				t.Fatalf("client error %v, want ok=%v", r.clientErr, tt.ok)
			}
			if tt.ok && (r.serverErr != nil || r.peer.Protocol != ALPNEnroll) {
				t.Fatalf("server %v, peer %+v", r.serverErr, r.peer)
			}
			if !tt.ok {
				t.Logf("refused: %v", r.clientErr)
			}
		})
	}
}

func TestEnrollConfigSendsNoCertificate(t *testing.T) {
	ca := newTestCA(t)
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{ca.controllerCert(t, "01JCTRL")},
		NextProtos:   []string{ALPNEnroll},
		ClientAuth:   tls.RequestClientCert,
	}
	state, serverErr, clientErr := serveRaw(t, cfg, enrollDialer(Fingerprint(ca.cert), ControllerURI("01JCTRL")))
	if serverErr != nil || clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", serverErr, clientErr)
	}
	if len(state.PeerCertificates) != 0 {
		t.Fatalf("the enrolling client sent %d certificates", len(state.PeerCertificates))
	}
}

func TestDialFails(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Dial(ctx, addr, EnrollConfig(Fingerprint(newTestCA(t).cert), ControllerURI("C1"))); err == nil {
		t.Fatal("Dial to a closed port succeeded")
	}
}

func TestBackoffCeilings(t *testing.T) {
	b := NewBackoff()
	b.rand = func(n int64) int64 { return n - 1 }
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60, 60}
	for i, w := range want {
		if got := b.Next(); got != w*time.Second {
			t.Fatalf("call %d: Next = %v, want %v", i, got, w*time.Second)
		}
	}
	b.Reset()
	if got := b.Next(); got != time.Second {
		t.Fatalf("after Reset: Next = %v, want 1s", got)
	}
}

func TestBackoffJitter(t *testing.T) {
	b := NewBackoff()
	var low bool
	for i := range 200 {
		ceiling := min(BackoffMax, BackoffMin<<min(i, 6))
		d := b.Next()
		if d < 0 || d > ceiling {
			t.Fatalf("call %d: Next = %v, outside [0, %v]", i, d, ceiling)
		}
		low = low || d < ceiling/2
	}
	if !low {
		t.Error("no delay fell in the lower half: the jitter is not full")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/transport/`
Expected: FAIL — `internal/transport/client_test.go:15:10: undefined: Dial` (and `AgentConfig`, `EnrollConfig`, `NewBackoff`)

- [ ] **Step 3: Write the implementation**

`internal/transport/client.go`:

```go
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
)

// AgentConfig is the agent's ALPNAgent configuration: cert is its client
// certificate, and the controller's leaf must verify against ca alone with
// EKU serverAuth and carry the URI SAN controllerSAN.
func AgentConfig(cert tls.Certificate, ca *x509.Certificate, controllerSAN string) *tls.Config {
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPNAgent},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			return &cert, nil
		},
		// Go's hostname check cannot match a URI SAN, so VerifyConnection
		// does the whole check, chain included.
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection verifies the chain and the pinned SAN
		VerifyConnection: func(cs tls.ConnectionState) error {
			return verifyController(cs, ALPNAgent, roots, controllerSAN)
		},
	}
}

// EnrollConfig is the ALPNEnroll configuration: no client certificate; the
// last certificate the controller presents must have the SHA-256
// fingerprint the enrollment token names, and the leaf must verify against
// that certificate alone with EKU serverAuth and carry the URI SAN
// controllerSAN. Nothing is sent before these checks pass.
func EnrollConfig(fingerprint, controllerSAN string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		NextProtos: []string{ALPNEnroll},
		// As AgentConfig: VerifyConnection does the whole check.
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection pins the root and verifies the chain and SAN
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("transport: the controller presented no certificate")
			}
			root := cs.PeerCertificates[len(cs.PeerCertificates)-1]
			if Fingerprint(root) != fingerprint {
				return errors.New("transport: the controller's root is not the one the enrollment token names")
			}
			roots := x509.NewCertPool()
			roots.AddCert(root)
			return verifyController(cs, ALPNEnroll, roots, controllerSAN)
		},
	}
}

// verifyController checks the controller's leaf against roots alone.
func verifyController(cs tls.ConnectionState, protocol string, roots *x509.CertPool, san string) error {
	if cs.NegotiatedProtocol != protocol {
		return fmt.Errorf("transport: the controller negotiated %q, want %q", cs.NegotiatedProtocol, protocol)
	}
	if len(cs.PeerCertificates) == 0 {
		return errors.New("transport: the controller presented no certificate")
	}
	leaf := cs.PeerCertificates[0]
	opts := x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("transport: controller certificate: %w", err)
	}
	// Verify lets a certificate without extended key usages through for any
	// usage.
	if !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
		return errors.New("transport: the controller certificate lacks the serverAuth usage")
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != san {
		return fmt.Errorf("transport: the controller certificate does not name %s alone", san)
	}
	return nil
}

// Dial connects to address over TCP and completes the TLS handshake with
// cfg within ctx.
func Dial(ctx context.Context, address string, cfg *tls.Config) (*tls.Conn, error) {
	d := tls.Dialer{Config: cfg}
	conn, err := d.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", address, err)
	}
	return conn.(*tls.Conn), nil
}
```

`internal/transport/backoff.go`:

```go
package transport

import (
	"math/rand/v2"
	"time"
)

// Bounds of the agent's redial delay.
const (
	BackoffMin = time.Second
	BackoffMax = time.Minute
)

// Backoff spaces the agent's redials: exponential from BackoffMin to
// BackoffMax with full jitter, so agents that lost a controller together do
// not return together. It is not safe for concurrent use.
type Backoff struct {
	attempt int
	rand    func(n int64) int64 // a uniform value in [0, n)
}

// NewBackoff returns a backoff at its first delay.
func NewBackoff() *Backoff {
	return &Backoff{rand: rand.Int64N} //nolint:gosec // G404: jitter needs no unpredictability
}

// Next returns a delay drawn uniformly from [0, min(BackoffMax,
// BackoffMin*2^n)] for the n-th call since the last Reset.
func (b *Backoff) Next() time.Duration {
	ceiling := BackoffMin
	for i := 0; i < b.attempt && ceiling < BackoffMax; i++ {
		ceiling *= 2
	}
	if ceiling < BackoffMax {
		b.attempt++
	}
	ceiling = min(ceiling, BackoffMax)
	return time.Duration(b.rand(int64(ceiling) + 1))
}

// Reset returns the backoff to its first delay, after a session that came up.
func (b *Backoff) Reset() { b.attempt = 0 }
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s -v -run 'TestAgentConfig|TestEnrollConfig|TestDial|TestBackoff' ./internal/transport/`
Expected: PASS. `TestAgentConfigRefuses` logs, in order: `signed by unknown authority`, `does not name darkpanel://controller/01JCTRL alone` (three times), `incompatible key usage`, `lacks the serverAuth usage`, `has expired`. `TestEnrollConfig` logs: `root is not the one the enrollment token names`, `does not name darkpanel://controller/01JOTHER alone`, `signed by unknown authority`, `incompatible key usage`, `lacks the serverAuth usage`.

Run: `GOTOOLCHAIN=local go vet ./internal/transport/ && golangci-lint run --allow-serial-runners ./internal/transport/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/transport/client_test.go internal/transport/client.go internal/transport/backoff.go
git commit -m "feat(transport): add agent TLS, dial and backoff"
```

### Task 8: Session with stream limits

**Files:**
- Create: `internal/transport/session.go`
- Test: `internal/transport/session_test.go`

**Interfaces:**
- Consumes: C3, C4 (end-to-end test only), the C21 helpers of `pki_test.go` and `client_test.go`; Tasks 6 and 7.
- Produces: C5; the C21 helper `sessionPair`. Top-level names added: `MaxControllerStreams`, `MaxAgentStreams`, `Session` (`Open`, `Accept`, `Addr`, `Close`, `Done`, `Ping`), `ControllerSession`, `AgentSession`; unexported `newSession`, `muxConfig`, `stream`; tests `sessionPair`, `sessionClient`, `serveOn`, `get`, `TestSessionOverMutualTLS`, `TestSessionOpenLimit`, `TestSessionAcceptLimit`, `TestSessionHalfOpenStreamHoldsSlot`, `TestSessionClosed`.

**Depends on:** Task 6, Task 7

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

A slot is released only when the stream is closed at both ends (the read side ends after the local close), so a peer holding its halves open cannot cycle more streams than the limit; `TestSessionHalfOpenStreamHoldsSlot` fails if the slot is released at the local `Close`. `TestSessionAcceptLimit` opens raw yamux streams through the unexported `mux` field so the agent's own Open limit does not hide the controller's Accept limit.

- [ ] **Step 1: Write the failing test**

`internal/transport/session_test.go`:

```go
package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sessionPair returns the two ends of a session over an in-memory
// connection, closed when the test ends.
func sessionPair(t *testing.T) (ctrl, agent *Session) {
	t.Helper()
	c1, c2 := net.Pipe()
	ctrl, err := ControllerSession(c1)
	if err != nil {
		t.Fatal(err)
	}
	agent, err = AgentSession(c2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = agent.Close()
	})
	return ctrl, agent
}

// sessionClient is an HTTP client whose every connection is a stream s
// opens.
func sessionClient(s *Session) *http.Client {
	return &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return s.Open(ctx) },
		},
	}
}

// serveOn serves h on the streams the peer of s opens until s closes, and
// reports Serve's result.
func serveOn(s *Session, h http.Handler) <-chan error {
	done := make(chan error, 1)
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}
	go func() { done <- srv.Serve(s) }()
	return done
}

func get(t *testing.T, c *http.Client, method, url string) string {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader("body"))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("%s %s: %d %q %v", method, url, resp.StatusCode, body, err)
	}
	return string(body)
}

func TestSessionOverMutualTLS(t *testing.T) {
	ca := newTestCA(t)
	cfg := serverConfig(ca, ca.controllerCert(t, "01JCTRL"))
	r := handshake(t, cfg, agentDialer(ca.agentCert(t, "01JHOST"), ca.cert, ControllerURI("01JCTRL")))
	if r.serverErr != nil || r.clientErr != nil {
		t.Fatalf("handshake: server %v, client %v", r.serverErr, r.clientErr)
	}
	ctrl, err := ControllerSession(r.peer.Conn)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := AgentSession(r.client)
	if err != nil {
		t.Fatal(err)
	}

	agentAPI := http.NewServeMux()
	agentAPI.HandleFunc("GET /agent/v1/hello", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "hello from the agent")
	})
	controllerAPI := http.NewServeMux()
	controllerAPI.HandleFunc("POST "+RenewPath, func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		_, _ = io.WriteString(w, "renewed "+string(b))
	})
	agentServed := serveOn(agent, agentAPI)
	ctrlServed := serveOn(ctrl, controllerAPI)

	toAgent, toController := sessionClient(ctrl), sessionClient(agent)
	for range 3 {
		if got := get(t, toAgent, "GET", "http://agent/agent/v1/hello"); got != "hello from the agent" {
			t.Fatalf("hello = %q", got)
		}
	}
	if got := get(t, toController, "POST", "http://controller"+RenewPath); got != "renewed body" {
		t.Fatalf("renew = %q", got)
	}
	toAgent.CloseIdleConnections()
	toController.CloseIdleConnections()

	if err := ctrl.Close(); err != nil {
		t.Fatal(err)
	}
	for name, served := range map[string]<-chan error{"agent": agentServed, "controller": ctrlServed} {
		select {
		case err := <-served:
			if !errors.Is(err, net.ErrClosed) {
				t.Errorf("%s Serve = %v, want net.ErrClosed", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s Serve did not end after the session closed", name)
		}
	}
	select {
	case <-agent.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the agent end did not see the session close")
	}
}

func TestSessionOpenLimit(t *testing.T) {
	ctrl, agent := sessionPair(t)
	tests := []struct {
		name             string
		opener, acceptor *Session
		limit            int
	}{
		{"controller", ctrl, agent, MaxControllerStreams},
		{"agent", agent, ctrl, MaxAgentStreams},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			go func() {
				for {
					c, err := tt.acceptor.Accept()
					if err != nil {
						return
					}
					// Close each stream once the opener closes its end, so the
					// acceptor's own limit never holds the opener back.
					go func() {
						_, _ = io.Copy(io.Discard, c)
						_ = c.Close()
					}()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			streams := make([]net.Conn, 0, tt.limit)
			for range tt.limit {
				c, err := tt.opener.Open(ctx)
				if err != nil {
					t.Fatalf("Open %d: %v", len(streams), err)
				}
				streams = append(streams, c)
			}
			short, cancelShort := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancelShort()
			if c, err := tt.opener.Open(short); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("Open beyond the limit = %v, %v; want it to wait until the context ends", c, err)
			}
			if err := streams[0].Close(); err != nil {
				t.Fatal(err)
			}
			c, err := tt.opener.Open(ctx)
			if err != nil {
				t.Fatalf("Open after a stream closed: %v", err)
			}
			streams = append(streams[1:], c)
			for _, c := range streams {
				_ = c.Close()
			}
		})
	}
}

func TestSessionAcceptLimit(t *testing.T) {
	ctrl, agent := sessionPair(t)
	// Open raw streams so the agent's own Open limit does not hide the
	// controller's Accept limit.
	raw := map[uint32]net.Conn{}
	for range MaxAgentStreams + 1 {
		st, err := agent.mux.OpenStream()
		if err != nil {
			t.Fatal(err)
		}
		raw[st.StreamID()] = st
	}
	accepted := make([]net.Conn, 0, MaxAgentStreams)
	for range MaxAgentStreams {
		c, err := ctrl.Accept()
		if err != nil {
			t.Fatal(err)
		}
		accepted = append(accepted, c)
	}
	extra := make(chan net.Conn, 1)
	go func() {
		c, err := ctrl.Accept()
		if err == nil {
			extra <- c
		}
	}()
	select {
	case <-extra:
		t.Fatal("Accept returned a stream beyond the limit")
	case <-time.After(100 * time.Millisecond):
	}
	// A slot frees once both ends have closed the stream.
	first := accepted[0].(*stream)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if err := raw[first.StreamID()].Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-extra:
		_ = c.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return the waiting stream after a slot freed")
	}
}

func TestSessionHalfOpenStreamHoldsSlot(t *testing.T) {
	ctrl, agent := sessionPair(t)
	held := make(chan net.Conn, MaxAgentStreams)
	go func() {
		for {
			c, err := ctrl.Accept()
			if err != nil {
				return
			}
			held <- c // never closed until the test says so
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range MaxAgentStreams {
		c, err := agent.Open(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Close(); err != nil {
			t.Fatal(err)
		}
	}
	short, cancelShort := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancelShort()
	if c, err := agent.Open(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Open while the peer holds every stream half-open = %v, %v; want it to wait", c, err)
	}
	if err := (<-held).Close(); err != nil {
		t.Fatal(err)
	}
	c, err := agent.Open(ctx)
	if err != nil {
		t.Fatalf("Open after the peer closed a stream: %v", err)
	}
	_ = c.Close()
}

func TestSessionClosed(t *testing.T) {
	ctrl, agent := sessionPair(t)
	if err := ctrl.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctrl.Done():
	default:
		t.Fatal("Done is open after Close")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := ctrl.Open(ctx); err == nil {
		t.Fatal("Open on a closed session succeeded")
	}
	if _, err := ctrl.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept on a closed session = %v, want net.ErrClosed", err)
	}
	select {
	case <-agent.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the peer did not see the session close")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/transport/`
Expected: FAIL — `internal/transport/session_test.go:16:46: undefined: Session` (and `ControllerSession`, `AgentSession`)

- [ ] **Step 3: Write the implementation**

`internal/transport/session.go`:

```go
package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
)

// Stream limits per session (spec section 4).
const (
	MaxControllerStreams = 32 // concurrent streams the controller opens on one session
	MaxAgentStreams      = 4  // concurrent streams the agent opens on one session
)

// Session is the yamux session over one authenticated agent connection.
// The controller opens streams to the agent API; the agent opens streams to
// the controller only to renew its certificate. A stream holds its slot
// until both ends have closed it, so an HTTP client's idle pooled
// connections count.
// Session is a net.Listener for the streams the peer opens, so
// http.Server.Serve can serve them.
type Session struct {
	mux     *yamux.Session
	opens   chan struct{} // one token per stream this end opened
	accepts chan struct{} // one token per stream this end accepted
}

var _ net.Listener = (*Session)(nil)

// ControllerSession is the yamux server end of conn: Open is limited to
// MaxControllerStreams, Accept to MaxAgentStreams.
func ControllerSession(conn net.Conn) (*Session, error) {
	mux, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: controller session: %w", err)
	}
	return newSession(mux, MaxControllerStreams, MaxAgentStreams), nil
}

// AgentSession is the yamux client end of conn: Open is limited to
// MaxAgentStreams, Accept to MaxControllerStreams.
func AgentSession(conn net.Conn) (*Session, error) {
	mux, err := yamux.Client(conn, muxConfig())
	if err != nil {
		return nil, fmt.Errorf("transport: agent session: %w", err)
	}
	return newSession(mux, MaxAgentStreams, MaxControllerStreams), nil
}

func newSession(mux *yamux.Session, opens, accepts int) *Session {
	return &Session{mux: mux, opens: make(chan struct{}, opens), accepts: make(chan struct{}, accepts)}
}

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.AcceptBacklog = 64
	// The controller's heartbeat decides when a session is dead.
	c.EnableKeepAlive = false
	// yamux bounds a ping by the write timeout.
	c.ConnectionWriteTimeout = 10 * time.Second
	c.StreamOpenTimeout = 10 * time.Second
	c.StreamCloseTimeout = 30 * time.Second
	c.LogOutput = io.Discard
	return c
}

// Open waits for a free slot until ctx ends, then opens a stream; the slot
// frees when the stream is closed at both ends.
func (s *Session) Open(ctx context.Context) (net.Conn, error) {
	select {
	case s.opens <- struct{}{}:
	case <-ctx.Done():
		return nil, fmt.Errorf("transport: open stream: %w", ctx.Err())
	case <-s.mux.CloseChan():
		return nil, fmt.Errorf("transport: open stream: %w", net.ErrClosed)
	}
	st, err := s.mux.OpenStream()
	if err != nil {
		<-s.opens
		return nil, fmt.Errorf("transport: open stream: %w", err)
	}
	return &stream{Stream: st, release: func() { <-s.opens }}, nil
}

// Accept waits for a free slot, then returns the next stream the peer
// opened. Streams beyond the limit wait in yamux's accept backlog of 64,
// and yamux resets any stream beyond that. Once the session is closed it
// fails with an error wrapping net.ErrClosed.
func (s *Session) Accept() (net.Conn, error) {
	select {
	case s.accepts <- struct{}{}:
	case <-s.mux.CloseChan():
		return nil, fmt.Errorf("transport: accept stream: %w", net.ErrClosed)
	}
	st, err := s.mux.AcceptStream()
	if err != nil {
		<-s.accepts
		if s.mux.IsClosed() {
			err = net.ErrClosed
		}
		return nil, fmt.Errorf("transport: accept stream: %w", err)
	}
	return &stream{Stream: st, release: func() { <-s.accepts }}, nil
}

// Addr is the local address of the session's connection.
func (s *Session) Addr() net.Addr { return s.mux.Addr() }

// Close ends the session, its streams and its connection.
func (s *Session) Close() error { return s.mux.Close() }

// Done is closed when the session ends, from either side.
func (s *Session) Done() <-chan struct{} { return s.mux.CloseChan() }

// Ping measures one round trip; yamux fails it after the 10 s write
// timeout.
func (s *Session) Ping() (time.Duration, error) { return s.mux.Ping() }

// stream returns its slot once it is closed at both ends.
type stream struct {
	*yamux.Stream
	once    sync.Once
	release func()
}

func (st *stream) Close() error {
	err := st.Stream.Close()
	st.once.Do(func() { go st.releaseWhenClosed() })
	return err
}

// releaseWhenClosed frees the slot when the read side ends: the peer closed
// its half, reset the stream, or yamux gave up on it StreamCloseTimeout
// after the local close. Freeing it at the local close would let a peer
// that keeps its halves open cycle more streams than the limit.
func (st *stream) releaseWhenClosed() {
	_ = st.SetReadDeadline(time.Time{})
	_, _ = io.Copy(io.Discard, st.Stream)
	st.release()
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=5 -timeout 300s -run 'TestSession' ./internal/transport/`
Expected: PASS

Run: `GOTOOLCHAIN=local go vet ./internal/transport/ && golangci-lint run --allow-serial-runners ./internal/transport/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/transport/session_test.go internal/transport/session.go
git commit -m "feat(transport): add the yamux session with limits"
```

### Task 9: Heartbeat

**Files:**
- Create: `internal/transport/heartbeat.go`
- Test: `internal/transport/heartbeat_test.go`

**Interfaces:**
- Consumes: C5 (`Session.Ping`, `Done`, `Close`), the C21 helper `sessionPair`; Task 8.
- Produces: C6. Top-level names added: `HeartbeatInterval`, `HeartbeatTimeout`, `HeartbeatFailures`, `HeartbeatConfig`, `Heartbeat`; unexported `withDefaults`, `pinger`, `heartbeat`, `ping`; tests `fakePinger`, `newFakePinger`, `runHeartbeat`, `TestHeartbeatDefaults`, `TestHeartbeatSuccessResetsCount`, `TestHeartbeatTimesOut`, `TestHeartbeatStops`, `TestHeartbeatOverSession`.

**Depends on:** Task 8

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The loop runs over the unexported `pinger` interface so a scripted fake can prove that a success resets the failure count; one subtest runs the real `Heartbeat` over a session, and one over a connection nobody reads, so no ping is answered. Every interval in the tests is milliseconds.

- [ ] **Step 1: Write the failing test**

`internal/transport/heartbeat_test.go`:

```go
package transport

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePinger answers pings from a script: each entry is one ping's result,
// and pings past the end succeed. A blocking fake never answers.
type fakePinger struct {
	mu       sync.Mutex
	script   []error
	pings    int
	block    bool
	done     chan struct{}
	closeOne sync.Once
}

func newFakePinger(script ...error) *fakePinger {
	return &fakePinger{script: script, done: make(chan struct{})}
}

func (f *fakePinger) Ping() (time.Duration, error) {
	f.mu.Lock()
	f.pings++
	block := f.block
	var err error
	if len(f.script) > 0 {
		err, f.script = f.script[0], f.script[1:]
	}
	f.mu.Unlock()
	if block {
		<-f.done
		return 0, errors.New("closed")
	}
	return time.Millisecond, err
}

func (f *fakePinger) Done() <-chan struct{} { return f.done }

func (f *fakePinger) Close() error {
	f.closeOne.Do(func() { close(f.done) })
	return nil
}

func (f *fakePinger) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *fakePinger) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pings
}

// runHeartbeat runs heartbeat on p and returns its result, failing the test
// when it runs longer than five seconds.
func runHeartbeat(ctx context.Context, t *testing.T, p pinger, cfg HeartbeatConfig) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- heartbeat(ctx, p, cfg) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat did not return")
		return nil
	}
}

func TestHeartbeatDefaults(t *testing.T) {
	got := HeartbeatConfig{}.withDefaults()
	if got.Interval != 15*time.Second || got.Timeout != 10*time.Second || got.Failures != 3 {
		t.Fatalf("defaults = %+v", got)
	}
	set := HeartbeatConfig{Interval: time.Second, Timeout: 2 * time.Second, Failures: 5}.withDefaults()
	if set.Interval != time.Second || set.Timeout != 2*time.Second || set.Failures != 5 {
		t.Fatalf("explicit values replaced: %+v", set)
	}
}

func TestHeartbeatSuccessResetsCount(t *testing.T) {
	fail := errors.New("no answer")
	p := newFakePinger(fail, fail, nil, fail, fail, nil, fail, fail, fail)
	var alive atomic.Int32
	cfg := HeartbeatConfig{Interval: time.Millisecond, Timeout: time.Second, OnAlive: func() { alive.Add(1) }}
	err := runHeartbeat(context.Background(), t, p, cfg)
	if err == nil || !errors.Is(err, fail) {
		t.Fatalf("heartbeat = %v, want the third consecutive failure", err)
	}
	if n := p.count(); n != 9 {
		t.Errorf("pings = %d, want 9: two failures followed by a success must not close", n)
	}
	if n := alive.Load(); n != 2 {
		t.Errorf("OnAlive calls = %d, want 2", n)
	}
	if !p.closed() {
		t.Error("the session is still open after three failed pings")
	}
}

func TestHeartbeatTimesOut(t *testing.T) {
	p := newFakePinger()
	p.block = true
	err := runHeartbeat(context.Background(), t, p, HeartbeatConfig{Interval: time.Millisecond, Timeout: 5 * time.Millisecond, Failures: 2})
	if err == nil || !p.closed() {
		t.Fatalf("heartbeat = %v, closed %v; want unanswered pings to close the session", err, p.closed())
	}
}

func TestHeartbeatStops(t *testing.T) {
	t.Run("context ends", func(t *testing.T) {
		p := newFakePinger()
		ctx, cancel := context.WithCancel(context.Background())
		var alive atomic.Int32
		cfg := HeartbeatConfig{Interval: time.Millisecond, OnAlive: func() {
			if alive.Add(1) == 3 {
				cancel()
			}
		}}
		if err := runHeartbeat(ctx, t, p, cfg); err != nil {
			t.Fatalf("heartbeat = %v, want nil", err)
		}
		if p.closed() {
			t.Error("the heartbeat closed the session when its context ended")
		}
	})
	t.Run("session closes", func(t *testing.T) {
		p := newFakePinger()
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		if err := runHeartbeat(context.Background(), t, p, HeartbeatConfig{Interval: time.Millisecond}); err != nil {
			t.Fatalf("heartbeat = %v, want nil", err)
		}
	})
}

func TestHeartbeatOverSession(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		ctrl, _ := sessionPair(t)
		ctx, cancel := context.WithCancel(context.Background())
		var alive atomic.Int32
		cfg := HeartbeatConfig{Interval: 5 * time.Millisecond, OnAlive: func() {
			if alive.Add(1) == 3 {
				cancel()
			}
		}}
		done := make(chan error, 1)
		go func() { done <- Heartbeat(ctx, ctrl, cfg) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Heartbeat = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no three answered pings within five seconds")
		}
	})
	t.Run("unanswered", func(t *testing.T) {
		// Nothing reads the far end, so no ping is ever answered.
		near, far := net.Pipe()
		t.Cleanup(func() { _ = far.Close() })
		ctrl, err := ControllerSession(near)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- Heartbeat(context.Background(), ctrl, HeartbeatConfig{Interval: 5 * time.Millisecond, Timeout: 20 * time.Millisecond})
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("Heartbeat = nil, want an error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("unanswered pings did not end the heartbeat")
		}
		select {
		case <-ctrl.Done():
		default:
			t.Fatal("the session is still open")
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/transport/`
Expected: FAIL — `internal/transport/heartbeat_test.go:68:56: undefined: pinger` (and `HeartbeatConfig`, `heartbeat`)

- [ ] **Step 3: Write the implementation**

`internal/transport/heartbeat.go`:

```go
package transport

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Heartbeat defaults (spec section 4): three missed pings, 45 s, take a
// host offline.
const (
	HeartbeatInterval = 15 * time.Second
	HeartbeatTimeout  = 10 * time.Second
	HeartbeatFailures = 3
)

// HeartbeatConfig tunes Heartbeat; the zero value is the spec's.
type HeartbeatConfig struct {
	Interval, Timeout time.Duration // zero means HeartbeatInterval, HeartbeatTimeout
	Failures          int           // zero means HeartbeatFailures
	OnAlive           func()        // called after every successful ping; may be nil
}

func (c HeartbeatConfig) withDefaults() HeartbeatConfig {
	if c.Interval == 0 {
		c.Interval = HeartbeatInterval
	}
	if c.Timeout == 0 {
		c.Timeout = HeartbeatTimeout
	}
	if c.Failures == 0 {
		c.Failures = HeartbeatFailures
	}
	return c
}

// pinger is the part of a Session the heartbeat uses.
type pinger interface {
	Ping() (time.Duration, error)
	Done() <-chan struct{}
	Close() error
}

// Heartbeat pings s every Interval, waiting at most Timeout for each answer,
// until ctx ends (nil) or s closes (nil). After Failures consecutive failed
// pings it closes s and returns an error.
func Heartbeat(ctx context.Context, s *Session, cfg HeartbeatConfig) error {
	return heartbeat(ctx, s, cfg)
}

func heartbeat(ctx context.Context, s pinger, cfg HeartbeatConfig) error {
	cfg = cfg.withDefaults()
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	failed := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.Done():
			return nil
		case <-ticker.C:
		}
		err := ping(ctx, s, cfg.Timeout)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			failed = 0
			if cfg.OnAlive != nil {
				cfg.OnAlive()
			}
			continue
		}
		select {
		case <-s.Done():
			// The peer or the owner ended the session; that is not a failure
			// of the heartbeat.
			return nil
		default:
		}
		if failed++; failed >= cfg.Failures {
			return errors.Join(fmt.Errorf("transport: %d heartbeats in a row failed: %w", failed, err), s.Close())
		}
	}
}

// ping waits at most timeout for one answer. A ping that outlives the wait
// ends on its own: yamux bounds it by the session's write timeout.
func ping(ctx context.Context, s pinger, timeout time.Duration) error {
	answered := make(chan error, 1)
	go func() {
		_, err := s.Ping()
		answered <- err
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-answered:
		return err
	case <-timer.C:
		return fmt.Errorf("transport: no heartbeat answer within %s", timeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=3 -timeout 300s ./internal/transport/`
Expected: PASS

Run: `GOTOOLCHAIN=local go vet ./internal/transport/ && golangci-lint run --allow-serial-runners ./internal/transport/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/transport/heartbeat_test.go internal/transport/heartbeat.go
git commit -m "feat(transport): add the session heartbeat"
```

### Task 10: Fleet migration

**Files:**
- Create: `internal/core/store/migrations/controller/0005_fleet.sql`
- Modify: `internal/core/store/migrations_test.go` (the import block, the controller row of `TestCoreMigrationsApply`, and four new functions appended at the end)

**Interfaces:**
- Consumes: `store.CoreMigrations`, `store.Migrate` and the store package's test helpers `openTemp`, `migrateOpts`, `tableExists` and its unexported `migrationName` (all on main).
- Produces: C1. Top-level names added (tests): `controllerSetUpTo`, `migratedController`, `TestFleetMigrationKeepsTheM1LocalHost`, `TestFleetMigrationConstraints`, `TestFleetTablesAreStrict`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

In `internal/core/store/migrations_test.go`, replace the import block with:

```go
import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)
```

In `TestCoreMigrationsApply`, replace the controller row

```go
		{SideController, []string{"core_meta", "core_hosts", "core_module_state", "core_ca"}},
```

with

```go
		{SideController, []string{"core_meta", "core_hosts", "core_module_state", "core_ca",
			"core_agent_certs", "core_enroll_tokens", "core_host_update"}},
```

Append at the end of the file:

```go
// controllerSetUpTo returns the controller's core migrations numbered at most
// last, so a test can build a database as an older release left it.
func controllerSetUpTo(t *testing.T, last int) MigrationSet {
	t.Helper()
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	files, err := fs.Glob(set.FS, "*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sub := fstest.MapFS{}
	for _, name := range files {
		m := migrationName.FindStringSubmatch(name)
		if n, _ := strconv.Atoi(m[1]); n > last {
			continue
		}
		data, err := fs.ReadFile(set.FS, name)
		if err != nil {
			t.Fatal(err)
		}
		sub[name] = &fstest.MapFile{Data: data}
	}
	return MigrationSet{Module: set.Module, FS: sub}
}

// migratedController returns a controller database with every core
// migration applied.
func migratedController(t *testing.T) *DB {
	t.Helper()
	db := openTemp(t)
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestFleetMigrationKeepsTheM1LocalHost(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	if _, err := Migrate(ctx, db, migrateOpts(t, "m1", time.Now()), controllerSetUpTo(t, 4), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx,
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('01L', 'local', 1, '2026-10-17T00:00:00.000000000Z')"); err != nil {
		t.Fatal(err)
	}
	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "m2", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 5 || res.Applied[0].Name != "fleet" {
		t.Fatalf("applied %+v, want 0005_fleet first", res.Applied)
	}
	var state string
	var enrolled, seen, version, os, arch *string
	var protocol *int64
	if err := db.Read().QueryRowContext(ctx,
		`SELECT state, enrolled_at, last_seen_at, agent_version, protocol_version, os, arch FROM core_hosts WHERE id = '01L'`).
		Scan(&state, &enrolled, &seen, &version, &protocol, &os, &arch); err != nil {
		t.Fatal(err)
	}
	if state != "pending" || enrolled != nil || seen != nil || version != nil || protocol != nil || os != nil || arch != nil {
		t.Fatalf("migrated local host: state %q, enrolled %v, seen %v, version %v, protocol %v, os %v, arch %v",
			state, enrolled, seen, version, protocol, os, arch)
	}
}

func TestFleetMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	db := migratedController(t)
	exec := func(query string, args ...any) error {
		_, err := db.Writer().ExecContext(ctx, query, args...)
		return err
	}
	const at = "2026-10-19T00:00:00.000000000Z"
	if err := exec("INSERT INTO core_hosts (id, name, created_at) VALUES ('01H', 'web', ?)", at); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := db.Read().QueryRowContext(ctx, "SELECT state FROM core_hosts WHERE id = '01H'").Scan(&state); err != nil || state != "pending" {
		t.Fatalf("default state = %q, %v; want pending", state, err)
	}
	for _, s := range []string{"enrolled", "revoked", "pending"} {
		if err := exec("UPDATE core_hosts SET state = ? WHERE id = '01H'", s); err != nil {
			t.Fatalf("state %s refused: %v", s, err)
		}
	}
	if err := exec(`INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a1', '01H', ?, ?, ?)`, at, at, at); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES ('01H', x'00', ?, ?)`, at, at); err != nil {
		t.Fatal(err)
	}
	if err := exec(`INSERT INTO core_host_update (host_id, channel) VALUES ('01H', 'beta')`); err != nil {
		t.Fatal(err)
	}

	refused := []struct {
		name, query string
		args        []any
	}{
		{"unknown host state", "UPDATE core_hosts SET state = 'lost' WHERE id = '01H'", nil},
		{"null host state", "UPDATE core_hosts SET state = NULL WHERE id = '01H'", nil},
		{"certificate of an unknown host", `INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a2', 'nope', ?, ?, ?)`, []any{at, at, at}},
		{"duplicate serial", `INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES ('a1', '01H', ?, ?, ?)`, []any{at, at, at}},
		{"second token of a host", `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES ('01H', x'01', ?, ?)`, []any{at, at}},
		{"token without a hash", `INSERT INTO core_enroll_tokens (host_id, expires_at, created_at) VALUES ('01H', ?, ?)`, []any{at, at}},
		{"text hash", `UPDATE core_enroll_tokens SET hash = 'abc' WHERE host_id = '01H'`, nil},
		{"unknown channel", `UPDATE core_host_update SET channel = 'nightly' WHERE host_id = '01H'`, nil},
		{"update policy of an unknown host", `INSERT INTO core_host_update (host_id, channel) VALUES ('nope', 'stable')`, nil},
	}
	for _, tc := range refused {
		if err := exec(tc.query, tc.args...); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}

	if err := exec("DELETE FROM core_hosts WHERE id = '01H'"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"core_agent_certs", "core_enroll_tokens", "core_host_update"} {
		var n int
		if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s rows after the host was deleted = %d, %v; want 0", table, n, err)
		}
	}
}

func TestFleetTablesAreStrict(t *testing.T) {
	db := migratedController(t)
	for _, table := range []string{"core_agent_certs", "core_enroll_tokens", "core_host_update"} {
		var sql string
		if err := db.Read().QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&sql); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(strings.TrimSpace(sql), "STRICT") {
			t.Errorf("%s is not STRICT: %s", table, sql)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/core/store/ -run 'TestCoreMigrationsApply|TestFleet'`
Expected: FAIL — `table core_agent_certs missing`, `applied [], want 0005_fleet first`, `no such column: state`, and `sql: no rows in result set` from `TestFleetTablesAreStrict`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/controller/0005_fleet.sql`:

```sql
-- Fleet tables (M2 spec sections 2, 3 and 8): host enrollment state, agent
-- certificates, enrollment tokens and per-host update policy. Times are
-- fixed-width RFC 3339 UTC text, so they compare as text. Online state is
-- never stored: it belongs to the controller's live sessions. An M1 local host
-- row becomes pending through the column default.

ALTER TABLE core_hosts ADD COLUMN state TEXT NOT NULL DEFAULT 'pending'
    CHECK (state IN ('pending', 'enrolled', 'revoked'));
ALTER TABLE core_hosts ADD COLUMN enrolled_at TEXT;
ALTER TABLE core_hosts ADD COLUMN last_seen_at TEXT;
ALTER TABLE core_hosts ADD COLUMN agent_version TEXT;
ALTER TABLE core_hosts ADD COLUMN protocol_version INTEGER;
ALTER TABLE core_hosts ADD COLUMN os TEXT;
ALTER TABLE core_hosts ADD COLUMN arch TEXT;

-- One row per issued agent certificate; serials are 128-bit random, stored
-- as lower-case hex without padding.
CREATE TABLE core_agent_certs (
    serial     TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    not_before TEXT NOT NULL,
    not_after  TEXT NOT NULL,
    revoked_at TEXT,
    created_at TEXT NOT NULL
) STRICT;
CREATE INDEX core_agent_certs_host ON core_agent_certs (host_id);

-- At most one token per host; a new token replaces the row. hash is the
-- SHA-256 of the token's 128-bit secret, which is never stored.
CREATE TABLE core_enroll_tokens (
    host_id     TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    hash        BLOB NOT NULL,
    expires_at  TEXT NOT NULL,
    consumed_at TEXT,
    created_at  TEXT NOT NULL
) STRICT;

-- days is the schedule's days joined by ',' in the order mon..sun and
-- time_utc is HH:MM, both null without a schedule; last_window is the id
-- <YYYY-MM-DD>T<HH:MM> of the last window the scheduler claimed.
CREATE TABLE core_host_update (
    host_id         TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    channel         TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    days            TEXT,
    time_utc        TEXT,
    last_window     TEXT,
    last_rollout_id TEXT
) STRICT;
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/core/store/`
Expected: `ok  github.com/darkraise/darkpanel/internal/core/store`

Every package that migrates a controller database must still pass with the new migration (the health checks assert `schemaVersion >= 4`, not an exact number):

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 600s ./internal/controller/... ./internal/core/... ./cmd/...`
Expected: every package `ok`.

Run: `GOTOOLCHAIN=local go vet ./internal/core/store/ && golangci-lint run --allow-serial-runners ./internal/core/store/...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/controller/0005_fleet.sql internal/core/store/migrations_test.go
git commit -m "feat(store): add the fleet migration"
```

### Task 11: Agent authority

**Files:**
- Create: `internal/controller/fleet/ca.go`
- Test: `internal/controller/fleet/ca_test.go`

**Interfaces:**
- Consumes: C2 from Task 5 (`transport.ControllerURI`, `transport.Fingerprint`); `store.DB`, `core.Sealer`, `core.Clock`, `secret.NewSealer`, `coretest.NewFakeClock` (on main).
- Produces: C7, and the test helpers of C21 (fleet) that Tasks 12 and 13 use. Top-level names added: `PurposeAgent`, `CertValidity`, `CertRenewAfter`, `backdate`, `timeFormat`, `formatTime`, `Authority` and its methods `ControllerID`, `ControllerSAN`, `Root`, `RootPEM`, `Pool`, `Fingerprint`, `GetCertificate`, `issueListener`, `LoadOrCreateAuthority`, `rootRow`, `querier`, `selectRoot`, `selectControllerID`, `keyAD`, `newRoot`, `openRoot`, `randomSerial`; tests: `testStart`, `newSealer`, `openControllerDB`, `openControllerDBAt`, `newAuthority`, `leafOf`, `TestAuthorityCreatesRoot`, `TestAuthorityLoadsTheSameRoot`, `TestAuthorityConcurrentStartsAgree`, `TestAuthorityIgnoresOtherPurposes`, `TestAuthorityRefusesAnotherKeyOrMovedRow`, `TestControllerCertificate`, `TestControllerCertificateRenewal`, `TestControllerCertificateHandshake`.

**Depends on:** Task 5

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/controller/fleet/ca_test.go`:

```go
package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// testStart is the fake clock's start; whole seconds, as X.509 times are.
var testStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// newSealer returns a production sealer over a random key.
func newSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	key := make([]byte, secret.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	s, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// openControllerDB returns a migrated controller database in a temporary
// directory.
func openControllerDB(t *testing.T) *store.DB {
	t.Helper()
	return openControllerDBAt(t, filepath.Join(t.TempDir(), "controller.db"))
}

// openControllerDBAt opens the controller database at path, applying the
// core migrations when they are pending.
func openControllerDBAt(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups"), Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

// newAuthority returns an authority over a fresh controller database and its
// fake clock.
func newAuthority(t *testing.T) (*Authority, *store.DB, *coretest.FakeClock) {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), db, newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return a, db, clock
}

func TestAuthorityCreatesRoot(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	sealer := newSealer(t)
	a, err := LoadOrCreateAuthority(ctx, db, sealer, coretest.NewFakeClock(testStart))
	if err != nil {
		t.Fatal(err)
	}
	cert := a.Root()
	if _, ok := cert.PublicKey.(ed25519.PublicKey); !ok {
		t.Fatalf("root key is %T, want Ed25519", cert.PublicKey)
	}
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Fatalf("root constraints: IsCA %v, valid %v, MaxPathLen %d, zero %v",
			cert.IsCA, cert.BasicConstraintsValid, cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Fatalf("root key usage = %v", cert.KeyUsage)
	}
	if !cert.NotAfter.Equal(testStart.AddDate(10, 0, 0)) || !cert.NotBefore.Equal(testStart.Add(-time.Hour)) {
		t.Fatalf("root validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("root is not self-signed: %v", err)
	}
	if a.Fingerprint() != transport.Fingerprint(cert) {
		t.Fatalf("Fingerprint = %s, want %s", a.Fingerprint(), transport.Fingerprint(cert))
	}
	block, rest := pem.Decode(a.RootPEM())
	if block == nil || block.Type != "CERTIFICATE" || !bytes.Equal(block.Bytes, cert.Raw) || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("RootPEM is not the root as one CERTIFICATE block: %q", a.RootPEM())
	}
	if _, err := cert.Verify(x509.VerifyOptions{Roots: a.Pool()}); err != nil {
		t.Fatalf("the root does not verify against Pool: %v", err)
	}

	if _, err := ulid.ParseStrict(a.ControllerID()); err != nil {
		t.Fatalf("ControllerID %q is not a ULID: %v", a.ControllerID(), err)
	}
	if a.ControllerSAN() != "darkpanel://controller/"+a.ControllerID() {
		t.Fatalf("ControllerSAN = %q", a.ControllerSAN())
	}
	var storedID string
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'controller_id'`).Scan(&storedID); err != nil || storedID != a.ControllerID() {
		t.Fatalf("core_meta controller_id = %q, %v; want %q", storedID, err, a.ControllerID())
	}

	var id, purpose, certPEM, createdAt string
	var keyCT []byte
	if err := db.Read().QueryRowContext(ctx,
		`SELECT id, purpose, cert_pem, key_ct, created_at FROM core_ca`).Scan(&id, &purpose, &certPEM, &keyCT, &createdAt); err != nil {
		t.Fatal(err)
	}
	if purpose != PurposeAgent || certPEM != string(a.RootPEM()) || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("core_ca row: purpose %q, created_at %q, cert_pem matches %v", purpose, createdAt, certPEM == string(a.RootPEM()))
	}
	der, err := sealer.Open("core_ca.key_ct."+id, keyCT)
	if err != nil {
		t.Fatalf("key_ct does not open with its row's associated data: %v", err)
	}
	if bytes.Contains(keyCT, der) {
		t.Fatal("key_ct holds the key in clear")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatal(err)
	}
	if k, ok := key.(ed25519.PrivateKey); !ok || !k.Public().(ed25519.PublicKey).Equal(cert.PublicKey) {
		t.Fatal("the sealed key is not the root's key")
	}
}

func TestAuthorityLoadsTheSameRoot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	first, err := LoadOrCreateAuthority(ctx, openControllerDBAt(t, path), sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	db := openControllerDBAt(t, path)
	second, err := LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() != first.Fingerprint() || !second.key.Equal(first.key) || second.ControllerID() != first.ControllerID() {
		t.Fatal("a second start created a new root or controller id")
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", n, err)
	}
}

func TestAuthorityConcurrentStartsAgree(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	const starts = 4
	dbs := make([]*store.DB, starts)
	for i := range dbs {
		dbs[i] = openControllerDBAt(t, path)
	}
	prints := make([]string, starts)
	ids := make([]string, starts)
	errs := make([]error, starts)
	var ready, wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range starts {
		ready.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-gate
			a, err := LoadOrCreateAuthority(ctx, dbs[i], sealer, clock)
			if err != nil {
				errs[i] = err
				return
			}
			prints[i], ids[i] = a.Fingerprint(), a.ControllerID()
		}()
	}
	ready.Wait()
	close(gate)
	wg.Wait()
	for i := range starts {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if prints[i] != prints[0] || ids[i] != ids[0] {
			t.Fatalf("start %d has root %s and id %s, start 0 has %s and %s", i, prints[i], ids[i], prints[0], ids[0])
		}
	}
	var roots, metas int
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&roots); err != nil || roots != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", roots, err)
	}
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_meta WHERE key = 'controller_id'`).Scan(&metas); err != nil || metas != 1 {
		t.Fatalf("controller_id rows = %d, %v; want 1", metas, err)
	}
}

func TestAuthorityIgnoresOtherPurposes(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES ('web1', 'web', 'not a certificate', x'00', '2026-10-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	a, err := LoadOrCreateAuthority(ctx, db, newSealer(t), coretest.NewFakeClock(testStart))
	if err != nil {
		t.Fatalf("the web root row stopped the agent root: %v", err)
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca WHERE purpose = ?`, PurposeAgent).Scan(&n); err != nil || n != 1 {
		t.Fatalf("agent roots = %d, %v; want 1", n, err)
	}
	if a.Root().Subject.CommonName == "" {
		t.Fatal("agent root has no common name")
	}
}

func TestAuthorityRefusesAnotherKeyOrMovedRow(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	if _, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock)
	if err == nil || !strings.Contains(err.Error(), "fleet: agent authority") {
		t.Fatalf("load with another controller key: error = %v", err)
	}

	db = openControllerDB(t)
	sealer := newSealer(t)
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_ca SET id = 'moved'`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err == nil {
		t.Fatal("a root whose row id changed opened; the associated data must bind the key to its row")
	}
}

// leafOf returns the listener certificate's leaf, checking the chain is
// [leaf, root].
func leafOf(t *testing.T, a *Authority, c *tls.Certificate) *x509.Certificate {
	t.Helper()
	if len(c.Certificate) != 2 || !bytes.Equal(c.Certificate[1], a.Root().Raw) {
		t.Fatalf("chain has %d certificates; want [leaf, root]", len(c.Certificate))
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if c.Leaf == nil || !bytes.Equal(c.Leaf.Raw, leaf.Raw) {
		t.Fatal("tls.Certificate.Leaf is not the chain's leaf")
	}
	return leaf
}

func TestControllerCertificate(t *testing.T) {
	a, _, _ := newAuthority(t)
	c, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafOf(t, a, c)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		t.Fatalf("the leaf does not verify against the agent root for serverAuth: %v", err)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth || len(leaf.UnknownExtKeyUsage) != 0 {
		t.Fatalf("leaf EKU = %v, want serverAuth only", leaf.ExtKeyUsage)
	}
	if len(leaf.URIs) != 1 || leaf.URIs[0].String() != a.ControllerSAN() {
		t.Fatalf("leaf URIs = %v, want [%s]", leaf.URIs, a.ControllerSAN())
	}
	if len(leaf.DNSNames) != 0 || len(leaf.IPAddresses) != 0 || len(leaf.EmailAddresses) != 0 {
		t.Fatalf("leaf has DNS %v, IP %v or e-mail %v SANs", leaf.DNSNames, leaf.IPAddresses, leaf.EmailAddresses)
	}
	if leaf.IsCA {
		t.Fatal("the leaf is a CA")
	}
	pub, ok := leaf.PublicKey.(ed25519.PublicKey)
	if !ok {
		t.Fatalf("leaf key is %T, want Ed25519", leaf.PublicKey)
	}
	if priv, ok := c.PrivateKey.(ed25519.PrivateKey); !ok || !priv.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatal("the private key does not match the leaf")
	}
	if !leaf.NotBefore.Equal(testStart.Add(-time.Hour)) || !leaf.NotAfter.Equal(testStart.Add(CertValidity)) {
		t.Fatalf("leaf validity %v to %v", leaf.NotBefore, leaf.NotAfter)
	}
}

func TestControllerCertificateRenewal(t *testing.T) {
	a, _, clock := newAuthority(t)
	first, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(CertRenewAfter - time.Second)
	same, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if same != first {
		t.Fatal("the leaf was reissued before two thirds of its life")
	}
	clock.Advance(time.Second)
	renewed, err := a.GetCertificate(nil)
	if err != nil {
		t.Fatal(err)
	}
	if renewed == first || leafOf(t, a, renewed).SerialNumber.Cmp(leafOf(t, a, first).SerialNumber) == 0 {
		t.Fatal("the leaf was not reissued after two thirds of its life")
	}
	if !leafOf(t, a, renewed).NotBefore.Equal(clock.Now().Add(-time.Hour)) {
		t.Fatal("the renewed leaf does not start from the renewal time")
	}
}

// TestControllerCertificateHandshake proves Go's TLS 1.3 accepts the Ed25519
// chain: the client checks the chain against the agent root alone and the
// controller SAN itself, as the agent does.
func TestControllerCertificateHandshake(t *testing.T) {
	a, _, _ := newAuthority(t)
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = serverConn.Close(); _ = clientConn.Close() })
	server := tls.Server(serverConn, &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: a.GetCertificate})
	client := tls.Client(clientConn, &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true, //nolint:gosec // G402: VerifyConnection below does the whole check.
		VerifyConnection: func(cs tls.ConnectionState) error {
			leaf := cs.PeerCertificates[0]
			if _, err := leaf.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return err
			}
			if len(leaf.URIs) != 1 || leaf.URIs[0].String() != a.ControllerSAN() {
				t.Errorf("handshake leaf URIs = %v", leaf.URIs)
			}
			return nil
		},
	})
	errs := make(chan error, 1)
	go func() { errs <- server.Handshake() }()
	if err := client.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
	if v := client.ConnectionState().Version; v != tls.VersionTLS13 {
		t.Fatalf("TLS version %x, want 1.3", v)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: FAIL — the build fails with `undefined: Authority`, `undefined: LoadOrCreateAuthority` and `undefined: PurposeAgent`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/fleet/ca.go`:

```go
package fleet

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// PurposeAgent is the core_ca purpose of the agent certificate root (spec
// section 2). It is separate from the web root (ADR 0010).
const PurposeAgent = "agent"

// Certificate lifetimes (spec section 2).
const (
	CertValidity   = 365 * 24 * time.Hour // controller listener and agent certificates
	CertRenewAfter = CertValidity * 2 / 3
)

// backdate is how far before its issue time a certificate becomes valid, so
// a peer whose clock runs a little behind still accepts it.
const backdate = time.Hour

// timeFormat is the fixed-width RFC 3339 UTC layout every core table uses,
// so times compare as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// Authority is the controller's agent certificate root: Ed25519, valid 10
// years, used to issue the agent listener's certificate and every agent's
// client certificate. It also holds the controller id that both kinds of
// certificate name.
type Authority struct {
	db           *store.DB
	clock        core.Clock
	controllerID string
	root         *x509.Certificate
	key          ed25519.PrivateKey
	pem          []byte
	pool         *x509.CertPool
	fingerprint  string

	mu       sync.Mutex // serialises listener certificate issuance
	listener *tls.Certificate
	issuedAt time.Time
}

// LoadOrCreateAuthority loads the agent root from core_ca and the controller
// id from core_meta (key controller_id), creating either on the first start
// inside a write transaction that finds none, so starts that overlap agree.
// The root's private key is stored sealed with associated data
// core_ca.key_ct.<row id>.
func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error) {
	row, foundRoot, err := selectRoot(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	id, foundID, err := selectControllerID(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	if !foundRoot || !foundID {
		now := clock.Now()
		fresh, err := newRoot(sealer, now)
		if err != nil {
			return nil, err
		}
		freshID := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
		err = db.Write(ctx, func(tx *sql.Tx) error {
			if row, foundRoot, err = selectRoot(ctx, tx); err != nil {
				return err
			}
			if !foundRoot {
				row = fresh
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES (?, ?, ?, ?, ?)`,
					row.id, PurposeAgent, row.certPEM, row.keyCT, row.createdAt); err != nil {
					return fmt.Errorf("fleet: agent authority: store root: %w", err)
				}
			}
			if id, foundID, err = selectControllerID(ctx, tx); err != nil {
				return err
			}
			if !foundID {
				id = freshID
				if _, err := tx.ExecContext(ctx,
					`INSERT INTO core_meta (key, value) VALUES ('controller_id', ?)`, id); err != nil {
					return fmt.Errorf("fleet: agent authority: store controller id: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			if !strings.HasPrefix(err.Error(), "fleet: agent authority") {
				err = fmt.Errorf("fleet: agent authority: %w", err)
			}
			return nil, err
		}
	}
	a, err := openRoot(row, sealer)
	if err != nil {
		return nil, err
	}
	a.db, a.clock, a.controllerID = db, clock, id
	return a, nil
}

// ControllerID is the controller's ULID, named by its listener certificate.
func (a *Authority) ControllerID() string { return a.controllerID }

// ControllerSAN is the URI SAN of the controller's listener certificate,
// which agents pin.
func (a *Authority) ControllerSAN() string { return transport.ControllerURI(a.controllerID) }

// Root returns the agent root certificate.
func (a *Authority) Root() *x509.Certificate { return a.root }

// RootPEM returns the root certificate as one PEM CERTIFICATE block.
func (a *Authority) RootPEM() []byte { return append([]byte(nil), a.pem...) }

// Pool returns a pool holding the agent root alone.
func (a *Authority) Pool() *x509.CertPool { return a.pool.Clone() }

// Fingerprint returns the lowercase hex SHA-256 of the root's DER encoding.
func (a *Authority) Fingerprint() string { return a.fingerprint }

// GetCertificate serves tls.Config.GetCertificate for the agent listener with
// the chain [leaf, root]. The leaf is kept in memory only: it is issued on
// the first call and reissued once CertRenewAfter has passed since then, so
// a long-running controller never presents an expired one.
func (a *Authority) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.clock.Now()
	if a.listener != nil && now.Sub(a.issuedAt) < CertRenewAfter {
		return a.listener, nil
	}
	cert, err := a.issueListener(now)
	if err != nil {
		return nil, err
	}
	a.listener, a.issuedAt = cert, now
	return cert, nil
}

func (a *Authority) issueListener(now time.Time) (*tls.Certificate, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: generate key: %w", err)
	}
	san, err := url.Parse(a.ControllerSAN())
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel controller " + a.controllerID},
		URIs:                  []*url.URL{san},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(CertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.root, pub, a.key)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("fleet: listener certificate: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.root.Raw}, PrivateKey: priv, Leaf: leaf}, nil
}

type rootRow struct {
	id, certPEM, createdAt string
	keyCT                  []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func selectRoot(ctx context.Context, q querier) (rootRow, bool, error) {
	var r rootRow
	err := q.QueryRowContext(ctx,
		`SELECT id, cert_pem, key_ct, created_at FROM core_ca WHERE purpose = ? ORDER BY created_at, id LIMIT 1`,
		PurposeAgent).Scan(&r.id, &r.certPEM, &r.keyCT, &r.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rootRow{}, false, nil
	}
	if err != nil {
		return rootRow{}, false, fmt.Errorf("fleet: agent authority: read core_ca: %w", err)
	}
	return r, true, nil
}

func selectControllerID(ctx context.Context, q querier) (string, bool, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'controller_id'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("fleet: agent authority: read controller id: %w", err)
	}
	return id, true, nil
}

func keyAD(id string) string { return "core_ca.key_ct." + id }

func newRoot(sealer core.Sealer, now time.Time) (rootRow, error) {
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return rootRow{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel agent root " + id},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: create root: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: encode key: %w", err)
	}
	defer clear(pkcs8)
	ct, err := sealer.Seal(keyAD(id), pkcs8)
	if err != nil {
		return rootRow{}, fmt.Errorf("fleet: agent authority: seal key: %w", err)
	}
	return rootRow{
		id:        id,
		certPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyCT:     ct,
		createdAt: formatTime(now),
	}, nil
}

func openRoot(r rootRow, sealer core.Sealer) (*Authority, error) {
	block, _ := pem.Decode([]byte(r.certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("fleet: agent authority: root %s: cert_pem holds no certificate", r.id)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: %w", r.id, err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("fleet: agent authority: root %s is not a CA certificate", r.id)
	}
	pkcs8, err := sealer.Open(keyAD(r.id), r.keyCT)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: open key: %w", r.id, err)
	}
	defer clear(pkcs8)
	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent authority: root %s: parse key: %w", r.id, err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok || !key.Public().(ed25519.PublicKey).Equal(cert.PublicKey) {
		return nil, fmt.Errorf("fleet: agent authority: root %s: the key does not match the certificate", r.id)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &Authority{
		root:        cert,
		key:         key,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		pool:        pool,
		fingerprint: transport.Fingerprint(cert),
	}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("fleet: serial: %w", err)
	}
	return serial, nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: `ok  github.com/darkraise/darkpanel/internal/controller/fleet`

Run: `GOTOOLCHAIN=local go vet ./internal/controller/fleet/ && golangci-lint run --allow-serial-runners ./internal/controller/fleet/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` and no other output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/fleet/ca.go internal/controller/fleet/ca_test.go
git commit -m "feat(fleet): add the agent certificate authority"
```

### Task 12: Agent certificates and revocation

**Files:**
- Create: `internal/controller/fleet/certs.go`
- Test: `internal/controller/fleet/certs_test.go`

**Interfaces:**
- Consumes: C1 from Task 10 (`core_hosts.state`, `core_agent_certs`, `core_enroll_tokens`); C7 and the C21 (fleet) helpers `newAuthority`, `testStart` from Task 11; C2 from Task 5 (`transport.HostURI`, `HostIDOf`, `SerialText`).
- Produces: C8, and the C21 (fleet) helpers Task 13 uses. Top-level names added: `(*Authority).IssueAgentCert`, `csrKey`, `(*Authority).Renew`, `(*Authority).CheckPeer`, `(*Authority).Handshake`, `RevokeHost`; tests: `addHost`, `csrPEM`, `newCSR`, `parseCert`, `issue`, `wantCode`, `countCerts`, `TestIssueAgentCert`, `TestIssueAgentCertRefusesBadCSRs`, `TestCheckPeer`, `TestRenewKeepsBothSerialsUntilTheHandshake`, `TestRenewRefusesHostsThatAreNotEnrolled`, `TestHandshakeRevokesOnlyEarlierSerials`, `TestRevokeHost`.

**Depends on:** Task 5, Task 10, Task 11

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/controller/fleet/certs_test.go`:

```go
package fleet

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

// addHost inserts a core_hosts row in state.
func addHost(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(),
		`INSERT INTO core_hosts (id, name, created_at, state) VALUES (?, ?, '2026-10-19T00:00:00.000000000Z', ?)`,
		id, "host-"+id, state); err != nil {
		t.Fatal(err)
	}
}

// csrPEM returns tmpl signed by key as one PEM CERTIFICATE REQUEST block.
func csrPEM(t *testing.T, key crypto.Signer, tmpl *x509.CertificateRequest) []byte {
	t.Helper()
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
}

// newCSR returns a CSR for a fresh Ed25519 key, as an agent makes it.
func newCSR(t *testing.T) ([]byte, ed25519.PrivateKey) {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return csrPEM(t, key, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}}), key
}

// parseCert parses one PEM CERTIFICATE block.
func parseCert(t *testing.T, data []byte) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("not one CERTIFICATE block: %q", data)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// issue issues hostID a certificate for a fresh key in a write transaction.
func issue(t *testing.T, a *Authority, db *store.DB, hostID string) *x509.Certificate {
	t.Helper()
	csr, _ := newCSR(t)
	var out []byte
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		out, err = a.IssueAgentCert(context.Background(), tx, hostID, csr)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return parseCert(t, out)
}

func wantCode(t *testing.T, err error, code errcode.Code, what string) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("%s: error = %v, want %s", what, err, code)
	}
}

func countCerts(t *testing.T, db *store.DB) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_agent_certs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestIssueAgentCert(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "enrolled")
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := url.Parse(transport.HostURI("H2"))
	// Every identity field names something else; the certificate must name H1 alone.
	csr := csrPEM(t, key, &x509.CertificateRequest{
		Subject:        pkix.Name{CommonName: "H2", Organization: []string{"evil"}},
		DNSNames:       []string{"controller.example"},
		IPAddresses:    []net.IP{net.ParseIP("192.0.2.1")},
		EmailAddresses: []string{"root@example.com"},
		URIs:           []*url.URL{other},
	})
	var out []byte
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		out, err = a.IssueAgentCert(context.Background(), tx, "H1", csr)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cert := parseCert(t, out)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: a.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("the certificate does not verify for clientAuth against the agent root: %v", err)
	}
	if id, err := transport.HostIDOf(cert); err != nil || id != "H1" {
		t.Fatalf("HostIDOf = %q, %v; want H1", id, err)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth || len(cert.UnknownExtKeyUsage) != 0 {
		t.Fatalf("EKU = %v, want clientAuth only", cert.ExtKeyUsage)
	}
	if len(cert.DNSNames)+len(cert.IPAddresses)+len(cert.EmailAddresses) != 0 || len(cert.Subject.Organization) != 0 {
		t.Fatalf("CSR identity fields reached the certificate: %v %v %v %v", cert.DNSNames, cert.IPAddresses, cert.EmailAddresses, cert.Subject)
	}
	if !cert.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the certificate does not carry the CSR's key")
	}
	if cert.IsCA || cert.SerialNumber.BitLen() > 128 || cert.SerialNumber.Sign() <= 0 {
		t.Fatalf("IsCA %v, serial %s", cert.IsCA, cert.SerialNumber)
	}
	if !cert.NotBefore.Equal(testStart.Add(-time.Hour)) || !cert.NotAfter.Equal(testStart.Add(CertValidity)) {
		t.Fatalf("validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	var host, notBefore, notAfter, createdAt string
	var revoked *string
	if err := db.Read().QueryRow(`SELECT host_id, not_before, not_after, revoked_at, created_at FROM core_agent_certs WHERE serial = ?`,
		transport.SerialText(cert.SerialNumber)).Scan(&host, &notBefore, &notAfter, &revoked, &createdAt); err != nil {
		t.Fatalf("no core_agent_certs row for the serial: %v", err)
	}
	if host != "H1" || revoked != nil || notBefore != "2026-10-19T11:00:00.000000000Z" ||
		notAfter != "2027-10-19T12:00:00.000000000Z" || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("row: host %s, not_before %s, not_after %s, revoked %v, created %s", host, notBefore, notAfter, revoked, createdAt)
	}
}

func TestIssueAgentCertRefusesBadCSRs(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "enrolled")
	good, _ := newCSR(t)
	block, _ := pem.Decode(good)
	tampered := bytes.Clone(block.Bytes)
	tampered[len(tampered)-1] ^= 0xff
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		csr  []byte
	}{
		{"not PEM", []byte("not a request")},
		{"empty", nil},
		{"wrong block type", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})},
		{"two blocks", append(bytes.Clone(good), good...)},
		{"garbage DER", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte{0x30, 0x03, 0x02, 0x01, 0x01}})},
		{"bad signature", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: tampered})},
		{"ECDSA key", csrPEM(t, ecKey, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "agent"}})},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := db.Write(context.Background(), func(tx *sql.Tx) error {
				_, err := a.IssueAgentCert(context.Background(), tx, "H1", tc.csr)
				return err
			})
			wantCode(t, err, errcode.InvalidInput, tc.name)
			if n := countCerts(t, db); n != 0 {
				t.Fatalf("certificate rows = %d after a refused CSR", n)
			}
		})
	}
}

func TestCheckPeer(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	good := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	otherHost := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	revoked := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_agent_certs SET revoked_at = ? WHERE serial = ?`, formatTime(clock.Now()), revoked); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckPeer(ctx, "H1", good); err != nil {
		t.Fatalf("valid certificate refused: %v", err)
	}
	refused := []struct{ name, host, serial string }{
		{"unknown serial", "H1", "abc"},
		{"serial of another host", "H1", otherHost},
		{"revoked serial", "H1", revoked},
		{"unknown host", "H9", good},
	}
	for _, tc := range refused {
		wantCode(t, a.CheckPeer(ctx, tc.host, tc.serial), errcode.Forbidden, tc.name)
	}
	for _, state := range []string{"pending", "revoked"} {
		if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = ? WHERE id = 'H1'`, state); err != nil {
			t.Fatal(err)
		}
		wantCode(t, a.CheckPeer(ctx, "H1", good), errcode.Forbidden, "host "+state)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
		t.Fatal(err)
	}
	clock.Advance(CertValidity - time.Second)
	if err := a.CheckPeer(ctx, "H1", good); err != nil {
		t.Fatalf("certificate refused a second before it expires: %v", err)
	}
	clock.Advance(time.Second)
	wantCode(t, a.CheckPeer(ctx, "H1", good), errcode.Forbidden, "expired serial")
}

func TestRenewKeepsBothSerialsUntilTheHandshake(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	old := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	clock.Advance(CertRenewAfter)
	csr, key := newCSR(t)
	out, err := a.Renew(ctx, "H1", csr)
	if err != nil {
		t.Fatal(err)
	}
	renewed := parseCert(t, out)
	if !renewed.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the renewed certificate does not carry the new key")
	}
	if id, err := transport.HostIDOf(renewed); err != nil || id != "H1" {
		t.Fatalf("renewed HostIDOf = %q, %v", id, err)
	}
	fresh := transport.SerialText(renewed.SerialNumber)
	for _, s := range []string{old, fresh} {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused after renewal: %v", s, err)
		}
	}
	// An agent whose renewal was interrupted before it saved the new
	// certificate keeps connecting with the old one, which must stay valid.
	if err := a.Handshake(ctx, "H1", old); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{old, fresh} {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused after a handshake with the old one: %v", s, err)
		}
	}
	if err := a.Handshake(ctx, "H1", fresh); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a.CheckPeer(ctx, "H1", old), errcode.Forbidden, "old serial after the new one's handshake")
	if err := a.CheckPeer(ctx, "H1", fresh); err != nil {
		t.Fatalf("new serial refused: %v", err)
	}
}

func TestRenewRefusesHostsThatAreNotEnrolled(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "HP", "pending")
	addHost(t, db, "HR", "revoked")
	for _, host := range []string{"HP", "HR", "H9"} {
		csr, _ := newCSR(t)
		_, err := a.Renew(context.Background(), host, csr)
		wantCode(t, err, errcode.Forbidden, "renew "+host)
	}
	addHost(t, db, "H1", "enrolled")
	_, err := a.Renew(context.Background(), "H1", []byte("not a request"))
	wantCode(t, err, errcode.InvalidInput, "renew with a bad CSR")
	if n := countCerts(t, db); n != 0 {
		t.Fatalf("certificate rows = %d after refused renewals", n)
	}
}

func TestHandshakeRevokesOnlyEarlierSerials(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	var serials []string
	for range 3 {
		serials = append(serials, transport.SerialText(issue(t, a, db, "H1").SerialNumber))
		clock.Advance(time.Minute)
	}
	other := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	if err := a.Handshake(ctx, "H1", serials[1]); err != nil {
		t.Fatal(err)
	}
	wantCode(t, a.CheckPeer(ctx, "H1", serials[0]), errcode.Forbidden, "earlier serial")
	for _, s := range serials[1:] {
		if err := a.CheckPeer(ctx, "H1", s); err != nil {
			t.Fatalf("serial %s refused: %v", s, err)
		}
	}
	if err := a.CheckPeer(ctx, "H2", other); err != nil {
		t.Fatalf("another host's serial was revoked: %v", err)
	}
	if err := a.Handshake(ctx, "H1", "unknown"); err != nil {
		t.Fatalf("handshake with an unknown serial: %v", err)
	}
	if err := a.CheckPeer(ctx, "H1", serials[2]); err != nil {
		t.Fatalf("an unknown serial's handshake revoked a valid one: %v", err)
	}
}

func TestRevokeHost(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "enrolled")
	addHost(t, db, "H2", "enrolled")
	first := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	second := transport.SerialText(issue(t, a, db, "H1").SerialNumber)
	other := transport.SerialText(issue(t, a, db, "H2").SerialNumber)
	for _, host := range []string{"H1", "H2"} {
		if _, err := db.Writer().ExecContext(ctx,
			`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, created_at) VALUES (?, x'00', '2026-10-19T13:00:00.000000000Z', '2026-10-19T12:00:00.000000000Z')`,
			host); err != nil {
			t.Fatal(err)
		}
	}
	clock.Advance(time.Minute)
	if err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	var tokens int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens WHERE host_id = 'H1'`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatalf("revoked host's tokens = %d, %v; want 0", tokens, err)
	}
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens WHERE host_id = 'H2'`).Scan(&tokens); err != nil || tokens != 1 {
		t.Fatalf("another host's tokens = %d, %v; want 1", tokens, err)
	}
	var state string
	if err := db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = 'H1'`).Scan(&state); err != nil || state != "revoked" {
		t.Fatalf("state = %q, %v; want revoked", state, err)
	}
	var revokedAt string
	if err := db.Read().QueryRow(`SELECT revoked_at FROM core_agent_certs WHERE serial = ?`, first).Scan(&revokedAt); err != nil ||
		revokedAt != "2026-10-19T12:01:00.000000000Z" {
		t.Fatalf("revoked_at = %q, %v", revokedAt, err)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{first, second} {
		wantCode(t, a.CheckPeer(ctx, "H1", s), errcode.Forbidden, "revoked host's serial")
	}
	if err := a.CheckPeer(ctx, "H2", other); err != nil {
		t.Fatalf("another host's serial was revoked: %v", err)
	}
	err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H9", clock.Now()) })
	wantCode(t, err, errcode.NotFound, "revoke an unknown host")
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: FAIL — the build fails with `a.IssueAgentCert undefined (type *Authority has no field or method IssueAgentCert)` and `a.CheckPeer undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/fleet/certs.go`:

```go
package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// IssueAgentCert checks csrPEM and, inside tx, issues hostID a certificate
// and inserts its core_agent_certs row. The certificate is valid
// CertValidity, EKU clientAuth only, with the URI SAN transport.HostURI(hostID)
// and nothing else and a 128-bit random serial. Only the CSR's key is used:
// its subject and extensions are ignored, because the host a certificate
// names comes from the token or the session, never from the agent. A CSR that
// is not one PEM CERTIFICATE REQUEST block, whose signature does not verify
// or whose key is not Ed25519 fails with request.invalid_input. It returns
// the certificate in PEM.
func (a *Authority) IssueAgentCert(ctx context.Context, tx *sql.Tx, hostID string, csrPEM []byte) ([]byte, error) {
	pub, err := csrKey(csrPEM)
	if err != nil {
		return nil, err
	}
	san, err := url.Parse(transport.HostURI(hostID))
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	now := a.clock.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel host " + hostID},
		URIs:                  []*url.URL{san},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(CertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.root, pub, a.key)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: %w", err)
	}
	// The listener identifies a peer by HostIDOf alone, so a host id it
	// cannot read back must never be certified.
	if id, err := transport.HostIDOf(cert); err != nil || id != hostID {
		return nil, fmt.Errorf("fleet: agent certificate: host id %q cannot be named in a certificate", hostID)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core_agent_certs (serial, host_id, not_before, not_after, created_at) VALUES (?, ?, ?, ?, ?)`,
		transport.SerialText(serial), hostID, formatTime(cert.NotBefore), formatTime(cert.NotAfter), formatTime(now)); err != nil {
		return nil, fmt.Errorf("fleet: agent certificate: store: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}

// csrKey returns the Ed25519 key of a CSR whose signature verifies.
func csrKey(csrPEM []byte) (ed25519.PublicKey, error) {
	block, rest := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errcode.New(errcode.InvalidInput, "the certificate request must be one PEM CERTIFICATE REQUEST block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "the certificate request does not parse")
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "the certificate request's signature does not verify")
	}
	pub, ok := csr.PublicKey.(ed25519.PublicKey)
	if !ok {
		return nil, errcode.New(errcode.InvalidInput, "the certificate request's key must be Ed25519")
	}
	return pub, nil
}

// Renew issues an enrolled host a new certificate in a write transaction of
// its own. Earlier serials stay valid until Handshake sees the new one or
// they expire, so an agent that loses the answer keeps its old certificate.
// A host that is not enrolled fails with auth.forbidden.
func (a *Authority) Renew(ctx context.Context, hostID string, csrPEM []byte) ([]byte, error) {
	var out []byte
	err := a.db.Write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state)
		switch {
		case errors.Is(err, sql.ErrNoRows) || err == nil && state != core.HostEnrolled:
			return errcode.New(errcode.Forbidden, fmt.Sprintf("host %s is not enrolled", hostID))
		case err != nil:
			return fmt.Errorf("fleet: renew: read host: %w", err)
		}
		out, err = a.IssueAgentCert(ctx, tx, hostID, csrPEM)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// CheckPeer is the agent listener's transport.PeerCheck: nil when hostID is
// enrolled and serial is its unrevoked certificate with the current time
// before not_after; otherwise auth.forbidden.
func (a *Authority) CheckPeer(ctx context.Context, hostID, serial string) error {
	var ok bool
	err := a.db.Read().QueryRowContext(ctx,
		`SELECT h.state = ? AND c.revoked_at IS NULL AND c.not_after > ?
		 FROM core_agent_certs c JOIN core_hosts h ON h.id = c.host_id
		 WHERE c.serial = ? AND c.host_id = ?`,
		core.HostEnrolled, formatTime(a.clock.Now()), serial, hostID).Scan(&ok)
	switch {
	case errors.Is(err, sql.ErrNoRows) || err == nil && !ok:
		return errcode.New(errcode.Forbidden, fmt.Sprintf("certificate %s of host %s is not valid", serial, hostID))
	case err != nil:
		return fmt.Errorf("fleet: check peer: %w", err)
	}
	return nil
}

// Handshake records a successful handshake with serial: every other
// unrevoked serial of hostID issued before it is revoked, because the agent
// has switched to the newer certificate. A serial issued after it, such as
// a renewal the agent has not saved yet, stays valid.
func (a *Authority) Handshake(ctx context.Context, hostID, serial string) error {
	err := a.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE core_agent_certs SET revoked_at = ?
			 WHERE host_id = ? AND serial <> ? AND revoked_at IS NULL
			   AND created_at < (SELECT created_at FROM core_agent_certs WHERE serial = ? AND host_id = ?)`,
			formatTime(a.clock.Now()), hostID, serial, serial, hostID)
		return err
	})
	if err != nil {
		return fmt.Errorf("fleet: handshake: %w", err)
	}
	return nil
}

// RevokeHost sets revoked_at on every unrevoked serial of hostID, the host's
// state to revoked and deletes its enrollment token, inside tx. A revoked
// host may enroll, so a token issued before the revocation must not outlive
// it. An unknown host is not_found.
func RevokeHost(ctx context.Context, tx *sql.Tx, hostID string, now time.Time) error {
	res, err := tx.ExecContext(ctx, `UPDATE core_hosts SET state = ? WHERE id = ?`, core.HostRevoked, hostID)
	if err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	} else if n == 0 {
		return errcode.New(errcode.NotFound, "no host "+hostID)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE core_agent_certs SET revoked_at = ? WHERE host_id = ? AND revoked_at IS NULL`,
		formatTime(now), hostID); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_enroll_tokens WHERE host_id = ?`, hostID); err != nil {
		return fmt.Errorf("fleet: revoke host: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: `ok  github.com/darkraise/darkpanel/internal/controller/fleet`

Run: `GOTOOLCHAIN=local go vet ./internal/controller/fleet/ && golangci-lint run --allow-serial-runners ./internal/controller/fleet/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` and no other output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/fleet/certs.go internal/controller/fleet/certs_test.go
git commit -m "feat(fleet): issue, check and revoke agent certificates"
```

### Task 13: Enrollment tokens and consumption

**Files:**
- Create: `internal/controller/fleet/tokens.go`
- Test: `internal/controller/fleet/tokens_test.go`

**Interfaces:**
- Consumes: C1 from Task 10 (`core_enroll_tokens`); C7 from Task 11 and its C21 (fleet) helpers `newAuthority`, `testStart`; C8 and its C21 (fleet) helpers `addHost`, `newCSR`, `parseCert`, `wantCode`, `countCerts` from Task 12 (`IssueAgentCert`, `csrKey`, `CheckPeer`, `RevokeHost`); C2 from Task 5 (`transport.Token`, `ParseToken`, `Token.SecretHash`, `Token.String`, `ControllerURI`, `SerialText`, `HostIDOf`).
- Produces: C9. Top-level names added: `TokenTTL`, `errTokenInvalid`, `(*Authority).IssueToken`, `Enrollment`, `(*Authority).Enroll`; tests: `testAddress`, `issueToken`, `hostState`, `orders`, `race`, `TestIssueTokenAndEnroll`, `TestIssueTokenReplacesTheEarlierToken`, `TestIssueTokenRefusals`, `TestEnrollRefusals`, `TestRevokedHostReenrolls`, `TestTokenReplacementAgainstEnrollment`, `TestRevocationAgainstEnrollment`.

**Depends on:** Task 5, Task 10, Task 11, Task 12

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/controller/fleet/tokens_test.go`:

```go
package fleet

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/base64"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/transport"
)

const testAddress = "198.18.0.10:7443"

// issueToken issues hostID a token in a write transaction of its own.
func issueToken(t *testing.T, a *Authority, db *store.DB, hostID string) (string, time.Time) {
	t.Helper()
	var token string
	var expires time.Time
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		token, expires, err = a.IssueToken(context.Background(), tx, hostID, testAddress)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return token, expires
}

func hostState(t *testing.T, db *store.DB, hostID string) string {
	t.Helper()
	var state string
	if err := db.Read().QueryRow(`SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIssueTokenAndEnroll(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	token, expires := issueToken(t, a, db, "H1")
	if !expires.Equal(testStart.Add(TokenTTL)) {
		t.Fatalf("expiresAt = %v, want %v", expires, testStart.Add(TokenTTL))
	}
	parsed, err := transport.ParseToken(token)
	if err != nil {
		t.Fatalf("the issued token does not parse: %v", err)
	}
	if parsed.Address != testAddress || parsed.Fingerprint != a.Fingerprint() || parsed.ControllerSAN != a.ControllerSAN() || parsed.HostID != "H1" {
		t.Fatalf("token = %+v", parsed)
	}
	if secret, err := base64.RawURLEncoding.DecodeString(parsed.Secret); err != nil || len(secret) != 16 {
		t.Fatalf("secret = %q, %v; want 16 bytes", parsed.Secret, err)
	}
	want, err := parsed.SecretHash()
	if err != nil {
		t.Fatal(err)
	}
	var hash []byte
	var expiresAt, createdAt string
	var consumed *string
	if err := db.Read().QueryRow(`SELECT hash, expires_at, consumed_at, created_at FROM core_enroll_tokens WHERE host_id = 'H1'`).
		Scan(&hash, &expiresAt, &consumed, &createdAt); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(hash, want) || expiresAt != "2026-10-19T13:00:00.000000000Z" || consumed != nil || createdAt != "2026-10-19T12:00:00.000000000Z" {
		t.Fatalf("token row: hash ok %v, expires %s, consumed %v, created %s", bytes.Equal(hash, want), expiresAt, consumed, createdAt)
	}
	if bytes.Contains([]byte(token), hash) {
		t.Fatal("the row stores the token text")
	}

	clock.Advance(TokenTTL - time.Second)
	csr, key := newCSR(t)
	got, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if got.HostID != "H1" || !bytes.Equal(got.CA, a.RootPEM()) {
		t.Fatalf("enrollment host %q, CA matches %v", got.HostID, bytes.Equal(got.CA, a.RootPEM()))
	}
	cert := parseCert(t, got.Certificate)
	if id, err := transport.HostIDOf(cert); err != nil || id != "H1" {
		t.Fatalf("HostIDOf = %q, %v", id, err)
	}
	if !cert.PublicKey.(ed25519.PublicKey).Equal(key.Public()) {
		t.Fatal("the certificate does not carry the CSR's key")
	}
	if err := a.CheckPeer(ctx, "H1", transport.SerialText(cert.SerialNumber)); err != nil {
		t.Fatalf("the enrolled certificate is refused: %v", err)
	}
	var enrolledAt string
	if err := db.Read().QueryRow(`SELECT enrolled_at FROM core_hosts WHERE id = 'H1'`).Scan(&enrolledAt); err != nil ||
		enrolledAt != "2026-10-19T12:59:59.000000000Z" || hostState(t, db, "H1") != "enrolled" {
		t.Fatalf("host after enrollment: state %s, enrolled_at %q, %v", hostState(t, db, "H1"), enrolledAt, err)
	}
	if err := db.Read().QueryRow(`SELECT consumed_at FROM core_enroll_tokens WHERE host_id = 'H1'`).Scan(&consumed); err != nil || consumed == nil {
		t.Fatalf("token not marked consumed: %v, %v", consumed, err)
	}
}

func TestIssueTokenReplacesTheEarlierToken(t *testing.T) {
	a, db, _ := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	first, _ := issueToken(t, a, db, "H1")
	second, _ := issueToken(t, a, db, "H1")
	csr, _ := newCSR(t)
	_, err := a.Enroll(ctx, first, csr)
	wantCode(t, err, errcode.EnrollTokenInvalid, "replaced token")
	if _, err := a.Enroll(ctx, second, csr); err != nil {
		t.Fatalf("replacement token: %v", err)
	}
	var rows int
	if err := db.Read().QueryRow(`SELECT count(*) FROM core_enroll_tokens`).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("token rows = %d, %v; want 1", rows, err)
	}
}

func TestIssueTokenRefusals(t *testing.T) {
	a, db, _ := newAuthority(t)
	addHost(t, db, "H1", "pending")
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, _, err := a.IssueToken(context.Background(), tx, "H9", testAddress)
		return err
	})
	wantCode(t, err, errcode.NotFound, "token for an unknown host")
	err = db.Write(context.Background(), func(tx *sql.Tx) error {
		_, _, err := a.IssueToken(context.Background(), tx, "H1", "")
		return err
	})
	wantCode(t, err, errcode.InvalidInput, "token without an address")
}

func TestEnrollRefusals(t *testing.T) {
	type setup struct {
		a     *Authority
		db    *store.DB
		clock *coretest.FakeClock
		token string
	}
	tamper := func(t *testing.T, token string, edit func(*transport.Token)) string {
		t.Helper()
		parsed, err := transport.ParseToken(token)
		if err != nil {
			t.Fatal(err)
		}
		edit(&parsed)
		return parsed.String()
	}
	tests := []struct {
		name string
		// prepare changes the state and returns the token and CSR to present.
		prepare func(t *testing.T, s setup) (string, []byte)
	}{
		{"reused token", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			if _, err := s.a.Enroll(context.Background(), s.token, csr); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Writer().Exec(`UPDATE core_hosts SET state = 'revoked' WHERE id = 'H1'`); err != nil {
				t.Fatal(err)
			}
			return s.token, csr
		}},
		{"expired token", func(t *testing.T, s setup) (string, []byte) {
			s.clock.Advance(TokenTTL)
			csr, _ := newCSR(t)
			return s.token, csr
		}},
		{"wrong secret", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) {
				tk.Secret = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{7}, 16))
			}), csr
		}},
		{"token moved to another host", func(t *testing.T, s setup) (string, []byte) {
			addHost(t, s.db, "H2", "pending")
			issueToken(t, s.a, s.db, "H2")
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.HostID = "H2" }), csr
		}},
		{"host without a token", func(t *testing.T, s setup) (string, []byte) {
			addHost(t, s.db, "H3", "pending")
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.HostID = "H3" }), csr
		}},
		{"another controller", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.ControllerSAN = transport.ControllerURI("01OTHER") }), csr
		}},
		{"another root", func(t *testing.T, s setup) (string, []byte) {
			csr, _ := newCSR(t)
			return tamper(t, s.token, func(tk *transport.Token) { tk.Fingerprint = string(bytes.Repeat([]byte{'a'}, 64)) }), csr
		}},
		{"malformed token", func(t *testing.T, _ setup) (string, []byte) {
			csr, _ := newCSR(t)
			return "dpe1.not-a-token", csr
		}},
		{"host already enrolled", func(t *testing.T, s setup) (string, []byte) {
			if _, err := s.db.Writer().Exec(`UPDATE core_hosts SET state = 'enrolled' WHERE id = 'H1'`); err != nil {
				t.Fatal(err)
			}
			csr, _ := newCSR(t)
			return s.token, csr
		}},
		{"bad CSR", func(_ *testing.T, s setup) (string, []byte) {
			return s.token, []byte("not a request")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, db, clock := newAuthority(t)
			addHost(t, db, "H1", "pending")
			token, _ := issueToken(t, a, db, "H1")
			s := setup{a: a, db: db, clock: clock, token: token}
			presented, csr := tc.prepare(t, s)
			certsBefore := countCerts(t, db)
			stateBefore := hostState(t, db, "H1")
			_, err := a.Enroll(context.Background(), presented, csr)
			wantCode(t, err, errcode.EnrollTokenInvalid, tc.name)
			if n := countCerts(t, db); n != certsBefore {
				t.Fatalf("certificate rows %d -> %d after a refused enrollment", certsBefore, n)
			}
			if state := hostState(t, db, "H1"); state != stateBefore {
				t.Fatalf("host state %s -> %s after a refused enrollment", stateBefore, state)
			}
		})
	}
}

func TestRevokedHostReenrolls(t *testing.T) {
	a, db, clock := newAuthority(t)
	ctx := context.Background()
	addHost(t, db, "H1", "pending")
	token, _ := issueToken(t, a, db, "H1")
	csr, _ := newCSR(t)
	first, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if err := db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) }); err != nil {
		t.Fatal(err)
	}
	token, _ = issueToken(t, a, db, "H1")
	csr, _ = newCSR(t)
	second, err := a.Enroll(ctx, token, csr)
	if err != nil {
		t.Fatalf("re-enrollment of a revoked host: %v", err)
	}
	oldSerial := transport.SerialText(parseCert(t, first.Certificate).SerialNumber)
	wantCode(t, a.CheckPeer(ctx, "H1", oldSerial), errcode.Forbidden, "certificate from before the revocation")
	if err := a.CheckPeer(ctx, "H1", transport.SerialText(parseCert(t, second.Certificate).SerialNumber)); err != nil {
		t.Fatalf("re-enrolled certificate refused: %v", err)
	}
}

// orders are the ways an enrollment and a competing write can meet: one
// after the other in either order, and at once. Run at once, the competing
// write usually wins, because Enroll checks the CSR before it takes the
// writer, so the two sequential orders are what make both outcomes certain.
var orders = []struct {
	name   string
	rounds int
	run    func(enroll, other func())
}{
	{"enrollment first", 1, func(enroll, other func()) { enroll(); other() }},
	{"other write first", 1, func(enroll, other func()) { other(); enroll() }},
	{"at once", 20, race},
}

// race runs a and b at once, as near the same instant as goroutines allow.
func race(a, b func()) {
	var ready, done sync.WaitGroup
	gate := make(chan struct{})
	for _, f := range []func(){a, b} {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-gate
			f()
		}()
	}
	ready.Wait()
	close(gate)
	done.Wait()
}

func TestTokenReplacementAgainstEnrollment(t *testing.T) {
	ctx := context.Background()
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			for i := range order.rounds {
				a, db, _ := newAuthority(t)
				addHost(t, db, "H1", "pending")
				first, _ := issueToken(t, a, db, "H1")
				csr, _ := newCSR(t)
				var enrollErr, issueErr error
				var second string
				order.run(func() { _, enrollErr = a.Enroll(ctx, first, csr) }, func() {
					issueErr = db.Write(ctx, func(tx *sql.Tx) error {
						var err error
						second, _, err = a.IssueToken(ctx, tx, "H1", testAddress)
						return err
					})
				})
				if issueErr != nil {
					t.Fatalf("round %d: IssueToken: %v", i, issueErr)
				}
				_, secondErr := a.Enroll(ctx, second, csr)
				if enrollErr == nil {
					// The first token was consumed before it was replaced; the
					// host is enrolled, so its replacement cannot enroll it again.
					wantCode(t, secondErr, errcode.EnrollTokenInvalid, "replacement after enrollment")
				} else {
					wantCode(t, enrollErr, errcode.EnrollTokenInvalid, "replaced token")
					if secondErr != nil {
						t.Fatalf("round %d: the replacement token failed after the replaced one was refused: %v", i, secondErr)
					}
				}
				if n := countCerts(t, db); n != 1 || hostState(t, db, "H1") != "enrolled" {
					t.Fatalf("round %d: %d certificates, state %s; want 1, enrolled", i, n, hostState(t, db, "H1"))
				}
			}
		})
	}
}

func TestRevocationAgainstEnrollment(t *testing.T) {
	ctx := context.Background()
	for _, order := range orders {
		t.Run(order.name, func(t *testing.T) {
			for i := range order.rounds {
				a, db, clock := newAuthority(t)
				addHost(t, db, "H1", "pending")
				token, _ := issueToken(t, a, db, "H1")
				csr, _ := newCSR(t)
				var enrolled Enrollment
				var enrollErr, revokeErr error
				order.run(func() { enrolled, enrollErr = a.Enroll(ctx, token, csr) }, func() {
					revokeErr = db.Write(ctx, func(tx *sql.Tx) error { return RevokeHost(ctx, tx, "H1", clock.Now()) })
				})
				if revokeErr != nil {
					t.Fatalf("round %d: RevokeHost: %v", i, revokeErr)
				}
				if enrollErr == nil {
					serial := transport.SerialText(parseCert(t, enrolled.Certificate).SerialNumber)
					wantCode(t, a.CheckPeer(ctx, "H1", serial), errcode.Forbidden, "certificate enrolled before the revocation")
				} else {
					wantCode(t, enrollErr, errcode.EnrollTokenInvalid, "enrollment after the revocation")
				}
				if state := hostState(t, db, "H1"); state != "revoked" {
					t.Fatalf("round %d: state %s, want revoked", i, state)
				}
				_, err := a.Enroll(ctx, token, csr)
				wantCode(t, err, errcode.EnrollTokenInvalid, "token after the revocation")
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/controller/fleet/`
Expected: FAIL — the build fails with `a.IssueToken undefined (type *Authority has no field or method IssueToken)`, `undefined: TokenTTL` and `a.Enroll undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/fleet/tokens.go`:

```go
package fleet

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// TokenTTL is how long an enrollment token works (spec section 3).
const TokenTTL = time.Hour

// errTokenInvalid ends an enrollment transaction; Enroll answers every
// refusal with the same code, so a caller learns nothing about which check
// failed.
var errTokenInvalid = errors.New("fleet: enrollment token refused")

// IssueToken creates or replaces hostID's enrollment token inside tx and
// returns it with its expiry. address is the host:port the agent dials.
// Only the SHA-256 of the token's 128-bit secret is stored; a secret that
// random needs no slow hash. It checks no host state: the host actions apply
// the state rules. An unknown host is not_found.
func (a *Authority) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	if address == "" {
		return "", time.Time{}, errcode.New(errcode.InvalidInput, "an enrollment token needs the controller address the agent dials")
	}
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM core_hosts WHERE id = ?`, hostID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", time.Time{}, errcode.New(errcode.NotFound, "no host "+hostID)
	case err != nil:
		return "", time.Time{}, fmt.Errorf("fleet: issue token: read host: %w", err)
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: secret: %w", err)
	}
	token := transport.Token{
		Address:       address,
		Fingerprint:   a.fingerprint,
		ControllerSAN: a.ControllerSAN(),
		HostID:        hostID,
		Secret:        base64.RawURLEncoding.EncodeToString(secret),
	}
	clear(secret)
	hash, err := token.SecretHash()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: %w", err)
	}
	now := a.clock.Now()
	expires := now.Add(TokenTTL)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at) VALUES (?, ?, ?, NULL, ?)
		 ON CONFLICT (host_id) DO UPDATE SET hash = excluded.hash, expires_at = excluded.expires_at,
		   consumed_at = NULL, created_at = excluded.created_at`,
		hostID, hash, formatTime(expires), formatTime(now)); err != nil {
		return "", time.Time{}, fmt.Errorf("fleet: issue token: store: %w", err)
	}
	return token.String(), expires, nil
}

// Enrollment is what a consumed token yields: the agent's certificate and
// the root it chains to, both PEM.
type Enrollment struct {
	HostID      string
	Certificate []byte
	CA          []byte
}

// Enroll consumes token and issues the host's certificate in one write
// transaction (spec section 3, Consumption): the token must be the host's
// current one, unconsumed and unexpired, and the host pending or revoked;
// the host becomes enrolled. A concurrent IssueToken or RevokeHost
// serialises on the same writer, so a replaced or revoked token can never
// enroll. Every failure of the token or of the CSR is enroll.token_invalid.
func (a *Authority) Enroll(ctx context.Context, token string, csrPEM []byte) (Enrollment, error) {
	refused := func(cause error) (Enrollment, error) {
		return Enrollment{}, errcode.Wrap(errcode.EnrollTokenInvalid, cause, "the enrollment token is not valid")
	}
	t, err := transport.ParseToken(token)
	if err != nil {
		return refused(err)
	}
	if t.ControllerSAN != a.ControllerSAN() || t.Fingerprint != a.fingerprint {
		return refused(errors.New("fleet: the token names another controller"))
	}
	hash, err := t.SecretHash()
	if err != nil {
		return refused(err)
	}
	if _, err := csrKey(csrPEM); err != nil {
		return refused(err)
	}
	var cert []byte
	err = a.db.Write(ctx, func(tx *sql.Tx) error {
		var stored []byte
		var expires, state string
		var consumed sql.NullString
		err := tx.QueryRowContext(ctx,
			`SELECT t.hash, t.expires_at, t.consumed_at, h.state
			 FROM core_enroll_tokens t JOIN core_hosts h ON h.id = t.host_id WHERE t.host_id = ?`,
			t.HostID).Scan(&stored, &expires, &consumed, &state)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errTokenInvalid
		case err != nil:
			return fmt.Errorf("fleet: enroll: read token: %w", err)
		}
		now := a.clock.Now()
		if subtle.ConstantTimeCompare(stored, hash) != 1 || consumed.Valid || expires <= formatTime(now) ||
			(state != core.HostPending && state != core.HostRevoked) {
			return errTokenInvalid
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE core_enroll_tokens SET consumed_at = ? WHERE host_id = ? AND hash = ? AND consumed_at IS NULL`,
			formatTime(now), t.HostID, hash)
		if err != nil {
			return fmt.Errorf("fleet: enroll: consume token: %w", err)
		}
		if n, err := res.RowsAffected(); err != nil {
			return fmt.Errorf("fleet: enroll: consume token: %w", err)
		} else if n != 1 {
			return errTokenInvalid
		}
		if cert, err = a.IssueAgentCert(ctx, tx, t.HostID, csrPEM); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_hosts SET state = ?, enrolled_at = ? WHERE id = ?`,
			core.HostEnrolled, formatTime(now), t.HostID); err != nil {
			return fmt.Errorf("fleet: enroll: mark host enrolled: %w", err)
		}
		return nil
	})
	var coded *errcode.Error
	switch {
	case errors.Is(err, errTokenInvalid) || errors.As(err, &coded):
		return refused(err)
	case err != nil:
		return Enrollment{}, err
	}
	return Enrollment{HostID: t.HostID, Certificate: cert, CA: a.RootPEM()}, nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 180s ./internal/controller/fleet/`
Expected: `ok  github.com/darkraise/darkpanel/internal/controller/fleet` (about 15 s with `-race`, 8 s without).

Run: `GOTOOLCHAIN=local go vet ./internal/controller/fleet/ && golangci-lint run --allow-serial-runners ./internal/controller/fleet/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` and no other output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/fleet/tokens.go internal/controller/fleet/tokens_test.go
git commit -m "feat(fleet): issue and consume enrollment tokens"
```

### Task 14: Transaction records and the update lock

**Files:**
- Create: `internal/update/record.go`
- Test: `internal/update/record_test.go`

**Interfaces:**
- Consumes: C11 (`Release`, Task 1), F-C3 (`paths.Paths.UpdateDir`, `UpdateRecord`), F-C5 (`core.UpdateTransaction`, `core.ErrorDetail`, the `core.Update*` state and kind constants).
- Produces: C14 (`RecordFormat`, `LockTimeout`, `Record`, `Record.Transaction`, `ReadRecord`, `WriteRecord`, `ListRecords`, `OperationRecords`, `Finished`, `Lock`, `ErrLockTimeout`, `AcquireLock`, `Lock.Release`). Top-level names added: `RecordFormat`, `recordID`, `recordFile`, `Record` (method `Transaction`), `ReadRecord`, `WriteRecord`, `ListRecords`, `OperationRecords`, `sortRecords`, `Finished`, `writeSynced`, `syncDir`, `LockTimeout`, `lockPoll`, `ErrLockTimeout`, `Lock` (method `Release`), `AcquireLock`; tests: `recordPaths`, `recordTime`, `testRecord`, `TestRecordRoundTrip`, `TestReadRecordFormats`, `TestListRecords`, `TestFinished`, `TestAcquireLock`, `TestAcquireLockWaitsForRelease`. Task 15 reuses `recordID` and `syncDir`; Tasks 17 to 20 reuse `recordPaths` and `testRecord` (C21).

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/update/record_test.go`:

```go
package update

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

// recordPaths returns a layout under a temporary root with UpdateDir created
// as the agent creates it.
func recordPaths(t *testing.T) paths.Paths {
	t.Helper()
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.UpdateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return p
}

var recordTime = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

// testRecord returns a pending update record started at recordTime plus
// offset.
func testRecord(id, op string, offset time.Duration) Record {
	return Record{
		ID: id, OperationID: op, Kind: core.UpdateKindUpdate,
		FromVersion: "1.2.0", ToVersion: "1.3.0", TargetVersion: "1.3.0",
		State: core.UpdatePending, StartedAt: recordTime.Add(offset), UpdatedAt: recordTime.Add(offset),
		Steps: []Release{{Version: "1.3.0", Channel: "stable"}},
	}
}

func TestRecordRoundTrip(t *testing.T) {
	p := recordPaths(t)
	parent := "01PARENT"
	deadline := recordTime.Add(80 * time.Second)
	r := testRecord("01TX", "01OP", 0)
	r.ParentID = &parent
	r.State = core.UpdateSwapped
	r.Error = &core.ErrorDetail{Code: "update.interrupted", Message: "the runner stopped"}
	r.ChangeID = "01CHANGE"
	r.Deadline = &deadline
	r.PrevSHA256, r.NewSHA256 = "aa", "bb"
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRecord(p, "01TX")
	if err != nil {
		t.Fatal(err)
	}
	r.FormatVersion = RecordFormat
	if !reflect.DeepEqual(got, r) {
		t.Fatalf("ReadRecord = %+v, want %+v", got, r)
	}
	info, err := os.Stat(p.UpdateRecord("01TX"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("record mode = %v, %v; want 0600", info.Mode(), err)
	}
	entries, _ := os.ReadDir(p.UpdateDir)
	if len(entries) != 1 {
		t.Errorf("UpdateDir holds %d entries, want only the record", len(entries))
	}
	raw, _ := os.ReadFile(p.UpdateRecord("01TX"))
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	var names []string
	for k := range members {
		names = append(names, k)
	}
	slices.Sort(names)
	want := []string{"changeId", "deadline", "error", "formatVersion", "fromVersion", "id", "kind", "newSha256",
		"operationId", "parentId", "prevSha256", "startedAt", "state", "steps", "targetVersion", "toVersion", "updatedAt"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("record members = %v, want %v", names, want)
	}
	wantTx := core.UpdateTransaction{
		ID: "01TX", OperationID: "01OP", ParentID: &parent, Kind: core.UpdateKindUpdate,
		FromVersion: "1.2.0", ToVersion: "1.3.0", TargetVersion: "1.3.0", State: core.UpdateSwapped,
		Error: r.Error, StartedAt: recordTime, UpdatedAt: recordTime,
	}
	if tx := got.Transaction(); !reflect.DeepEqual(tx, wantTx) {
		t.Errorf("Transaction = %+v, want %+v", tx, wantTx)
	}
}

func TestReadRecordFormats(t *testing.T) {
	p := recordPaths(t)
	write := func(id, body string) {
		t.Helper()
		if err := os.WriteFile(p.UpdateRecord(id), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A newer binary of the same major adds members only (ADR 0004); this
	// binary must still read its records during a rollback.
	write("NEWER", `{"formatVersion": 2, "id": "NEWER", "state": "swapped", "addedLater": {"x": 1}}`)
	if r, err := ReadRecord(p, "NEWER"); err != nil || r.State != core.UpdateSwapped {
		t.Errorf("a newer additive record = %+v, %v", r, err)
	}
	write("NOFORMAT", `{"id": "NOFORMAT", "state": "swapped"}`)
	if _, err := ReadRecord(p, "NOFORMAT"); err == nil {
		t.Error("a record without a format version was accepted")
	}
	write("OTHER", `{"formatVersion": 1, "id": "ELSE"}`)
	if _, err := ReadRecord(p, "OTHER"); err == nil {
		t.Error("a record naming another id was accepted")
	}
	write("BROKEN", `{"formatVersion": 1,`)
	if _, err := ReadRecord(p, "BROKEN"); err == nil {
		t.Error("a truncated record was accepted")
	}
	if _, err := ReadRecord(p, "MISSING"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing record = %v, want fs.ErrNotExist", err)
	}
	for _, id := range []string{"", "../x", "a.b", "a/b"} {
		if _, err := ReadRecord(p, id); err == nil {
			t.Errorf("ReadRecord(%q) was accepted", id)
		}
		if err := WriteRecord(p, Record{ID: id}); err == nil {
			t.Errorf("WriteRecord(%q) was accepted", id)
		}
	}
}

func TestListRecords(t *testing.T) {
	p := paths.Under(t.TempDir())
	if all, err := ListRecords(p); err != nil || len(all) != 0 {
		t.Fatalf("ListRecords without UpdateDir = %v, %v", all, err)
	}
	p = recordPaths(t)
	for _, r := range []Record{
		testRecord("C", "OP2", time.Minute),
		testRecord("B", "OP1", 0),
		testRecord("A", "OP1", 0),
		testRecord("D", "OP1", 2*time.Minute),
	} {
		if err := WriteRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"lock", ".E.json.tmp", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(p.UpdateDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	all, err := ListRecords(p)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rs []Record) []string {
		var out []string
		for _, r := range rs {
			out = append(out, r.ID)
		}
		return out
	}
	if got := ids(all); !reflect.DeepEqual(got, []string{"A", "B", "C", "D"}) {
		t.Errorf("ListRecords = %v", got)
	}
	if got := ids(OperationRecords(all, "OP1")); !reflect.DeepEqual(got, []string{"A", "B", "D"}) {
		t.Errorf("OperationRecords = %v", got)
	}
	if err := os.WriteFile(p.UpdateRecord("Z"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListRecords(p); err == nil {
		t.Error("ListRecords skipped an unreadable record")
	}
}

func TestFinished(t *testing.T) {
	step := func(state, to, target string) Record {
		r := testRecord("X", "OP", 0)
		r.State, r.ToVersion, r.TargetVersion = state, to, target
		return r
	}
	rollback := func(state string) Record {
		r := step(state, "1.2.0", "1.2.0")
		r.Kind = core.UpdateKindRollback
		return r
	}
	tests := []struct {
		name string
		op   []Record
		want bool
	}{
		{"no record", nil, false},
		{"pending", []Record{step(core.UpdatePending, "1.3.0", "1.3.0")}, false},
		{"swapped", []Record{step(core.UpdateSwapped, "1.3.0", "1.3.0")}, false},
		{"rolling back", []Record{step(core.UpdateRollingBack, "1.3.0", "1.3.0")}, false},
		{"committed final step", []Record{step(core.UpdateCommitted, "1.3.0", "1.3.0")}, true},
		{"committed intermediate step", []Record{step(core.UpdateCommitted, "1.3.0", "1.5.0")}, false},
		{"intermediate committed, next pending", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdatePending, "1.4.0", "1.5.0")}, false},
		{"stepped to the target", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdateCommitted, "1.4.0", "1.5.0"),
			step(core.UpdateCommitted, "1.5.0", "1.5.0")}, true},
		{"failed step after a commit", []Record{
			step(core.UpdateCommitted, "1.3.0", "1.5.0"), step(core.UpdateFailed, "1.4.0", "1.5.0")}, true},
		{"rolled back", []Record{step(core.UpdateRolledBack, "1.3.0", "1.3.0")}, true},
		{"rollback pending", []Record{rollback(core.UpdatePending)}, false},
		{"rollback rolling back", []Record{rollback(core.UpdateRollingBack)}, false},
		{"rollback done", []Record{rollback(core.UpdateRolledBack)}, true},
		{"rollback failed", []Record{rollback(core.UpdateFailed)}, true},
	}
	for _, tt := range tests {
		if got := Finished(tt.op); got != tt.want {
			t.Errorf("%s: Finished = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestAcquireLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	ctx := context.Background()
	first, err := AcquireLock(ctx, path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("lock file mode = %v, %v; want 0600", info.Mode(), err)
	}

	start := time.Now()
	if _, err := AcquireLock(ctx, path, 100*time.Millisecond); !errors.Is(err, ErrLockTimeout) {
		t.Fatalf("a second AcquireLock = %v, want ErrLockTimeout", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("AcquireLock gave up after %v, before its timeout", elapsed)
	}

	cancelled, cancel := context.WithCancel(ctx)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := AcquireLock(cancelled, path, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcquireLock after cancel = %v, want context.Canceled", err)
	}

	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(ctx, path, time.Second)
	if err != nil {
		t.Fatalf("AcquireLock after Release: %v", err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireLockWaitsForRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")
	first, err := AcquireLock(context.Background(), path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = first.Release()
	}()
	second, err := AcquireLock(context.Background(), path, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireLock while the holder releases: %v", err)
	}
	_ = second.Release()
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run 'TestRecordRoundTrip|TestReadRecordFormats|TestListRecords|TestFinished|TestAcquireLock'`
Expected: FAIL — `internal/update/record_test.go:34:54: undefined: Record`

- [ ] **Step 3: Write the implementation**

Create `internal/update/record.go`:

```go
package update

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

// RecordFormat is the transaction record format this binary writes. Formats
// change only additively within a major version, so a reader accepts every
// format from 1 up and ignores members it does not know (ADR 0004).
const RecordFormat = 1

// recordID bounds transaction ids, which name files under UpdateDir.
var recordID = regexp.MustCompile(`^[0-9A-Za-z]{1,64}$`)

// recordFile matches a record's file name; the lock and temporary files never
// match.
var recordFile = regexp.MustCompile(`^([0-9A-Za-z]{1,64})\.json$`)

// Record is one transaction of an update or rollback operation, stored as
// UpdateDir/<id>.json: the transaction fields core.update.status reports,
// plus what the runner and the rollback need.
type Record struct {
	FormatVersion int               `json:"formatVersion"`
	ID            string            `json:"id"`
	OperationID   string            `json:"operationId"`
	ParentID      *string           `json:"parentId"`
	Kind          string            `json:"kind"`
	FromVersion   string            `json:"fromVersion"`
	ToVersion     string            `json:"toVersion"`
	TargetVersion string            `json:"targetVersion"`
	State         string            `json:"state"`
	Error         *core.ErrorDetail `json:"error"`
	StartedAt     time.Time         `json:"startedAt"`
	UpdatedAt     time.Time         `json:"updatedAt"`
	// ChangeID is the change that started the operation; empty for a stepping
	// child and for a rollback started without the agent.
	ChangeID string `json:"changeId"`
	// Steps is every step of the operation with the channel its index entry
	// carried, written before the first swap.
	Steps      []Release  `json:"steps"`
	Deadline   *time.Time `json:"deadline"`
	PrevSHA256 string     `json:"prevSha256"`
	NewSHA256  string     `json:"newSha256"`
}

// Transaction is the record as core.update.status reports it.
func (r Record) Transaction() core.UpdateTransaction {
	return core.UpdateTransaction{
		ID:            r.ID,
		OperationID:   r.OperationID,
		ParentID:      r.ParentID,
		Kind:          r.Kind,
		FromVersion:   r.FromVersion,
		ToVersion:     r.ToVersion,
		TargetVersion: r.TargetVersion,
		State:         r.State,
		Error:         r.Error,
		StartedAt:     r.StartedAt,
		UpdatedAt:     r.UpdatedAt,
	}
}

// ReadRecord reads the record of transaction id. A missing record's error
// wraps fs.ErrNotExist.
func ReadRecord(p paths.Paths, id string) (Record, error) {
	if !recordID.MatchString(id) {
		return Record{}, fmt.Errorf("update: %q is not a transaction id", id)
	}
	data, err := os.ReadFile(p.UpdateRecord(id))
	if err != nil {
		return Record{}, fmt.Errorf("update: read record %s: %w", id, err)
	}
	var r Record
	if err := json.Unmarshal(data, &r); err != nil {
		return Record{}, fmt.Errorf("update: read record %s: %w", id, err)
	}
	switch {
	case r.FormatVersion < 1:
		return Record{}, fmt.Errorf("update: record %s has no format version", id)
	case r.ID != id:
		return Record{}, fmt.Errorf("update: record %s names transaction %q", id, r.ID)
	}
	return r, nil
}

// WriteRecord replaces the record of r.ID with format RecordFormat. The record
// is written to a temporary file, synced and renamed into place, and UpdateDir
// is synced, so a crash leaves the old or the new record, never a torn one.
// UpdateDir must exist; the agent creates it with its own mode.
func WriteRecord(p paths.Paths, r Record) error {
	if !recordID.MatchString(r.ID) {
		return fmt.Errorf("update: %q is not a transaction id", r.ID)
	}
	r.FormatVersion = RecordFormat
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode record %s: %w", r.ID, err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(p.UpdateDir, "."+r.ID+".json.tmp")
	if err := writeSynced(tmp, data, 0o600); err != nil {
		return fmt.Errorf("update: write record %s: %w", r.ID, err)
	}
	if err := os.Rename(tmp, p.UpdateRecord(r.ID)); err != nil {
		return errors.Join(fmt.Errorf("update: write record %s: %w", r.ID, err), os.Remove(tmp))
	}
	if err := syncDir(p.UpdateDir); err != nil {
		return fmt.Errorf("update: write record %s: %w", r.ID, err)
	}
	return nil
}

// ListRecords returns every record under UpdateDir, ordered by StartedAt then
// ID; none when UpdateDir does not exist. An unreadable record is an error,
// never skipped, because skipping a swapped record would skip its rollback.
func ListRecords(p paths.Paths) ([]Record, error) {
	entries, err := os.ReadDir(p.UpdateDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("update: list records: %w", err)
	}
	var out []Record
	for _, e := range entries {
		m := recordFile.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		r, err := ReadRecord(p, m[1])
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	sortRecords(out)
	return out, nil
}

// OperationRecords returns the records of operationID from all, ordered by
// StartedAt then ID.
func OperationRecords(all []Record, operationID string) []Record {
	var out []Record
	for _, r := range all {
		if r.OperationID == operationID {
			out = append(out, r)
		}
	}
	sortRecords(out)
	return out
}

func sortRecords(rs []Record) {
	slices.SortStableFunc(rs, func(a, b Record) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

// Finished reports whether the operation whose records are op, ordered as
// OperationRecords orders them, is over: any record failed or rolled back,
// or its last record committed the target version. A committed intermediate
// step is not finished, because the next step still holds the reservation.
// An operation with no record is not finished; what a missing record means
// is the caller's to decide.
func Finished(op []Record) bool {
	if len(op) == 0 {
		return false
	}
	for _, r := range op {
		if r.State == core.UpdateFailed || r.State == core.UpdateRolledBack {
			return true
		}
	}
	last := op[len(op)-1]
	return last.Kind == core.UpdateKindUpdate && last.State == core.UpdateCommitted &&
		last.ToVersion == last.TargetVersion
}

// writeSynced writes data to a new or truncated file at path with perm and
// syncs it before closing.
func writeSynced(path string, data []byte, perm os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G304: path is under paths.Paths, never from a request
	if err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return errors.Join(err, f.Close())
	}
	if _, err := f.Write(data); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	return f.Close()
}

// syncDir syncs a directory, so that renames and links in it survive a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: dir comes from paths.Paths, never from a request
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		return errors.Join(err, d.Close())
	}
	return d.Close()
}

// LockTimeout is how long a recover path waits for the update lock before it
// stops the runner that holds it (spec section 8, Taking the update lock).
const LockTimeout = 5 * time.Second

// lockPoll is how often AcquireLock retries a held lock.
const lockPoll = 20 * time.Millisecond

// ErrLockTimeout is AcquireLock's error when the lock stayed held for the
// whole timeout.
var ErrLockTimeout = errors.New("update: the update lock is held by another process")

// Lock is a held exclusive flock on the update lock file. The lock
// belongs to the open file, so it ends with the process that holds it even
// when that process is killed, and Go's close-on-exec keeps a restarted
// service from inheriting it.
type Lock struct {
	f *os.File
}

// AcquireLock takes an exclusive flock on path, creating the file with mode
// 0600, and retries a held lock until timeout passes (ErrLockTimeout) or ctx
// ends.
func AcquireLock(ctx context.Context, path string, timeout time.Duration) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600) //nolint:gosec // G304: the path is paths.UpdateLock, never from a request
	if err != nil {
		return nil, fmt.Errorf("update: open the update lock: %w", err)
	}
	deadline := time.Now().Add(timeout)
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) //nolint:gosec // G115: a file descriptor fits an int
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("update: take the update lock: %w", err), f.Close())
		}
		if !time.Now().Before(deadline) {
			return nil, errors.Join(ErrLockTimeout, f.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(ctx.Err(), f.Close())
		case <-time.After(lockPoll):
		}
	}
}

// Release drops the lock.
func (l *Lock) Release() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("update: release the update lock: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/record.go internal/update/record_test.go
git commit -m "feat(update): add transaction records and the lock"
```

### Task 15: Binary staging, swap and restore

**Files:**
- Create: `internal/update/binaries.go`
- Test: `internal/update/binaries_test.go`

**Interfaces:**
- Consumes: Task 14 (`recordID`, `syncDir`), F-C2 (`errcode.UpdatePrevMismatch`), F-C3 (`Binary`, `PrevBinary`, `RecoveryBinary`, `FailedBinary`, `NewBinary`).
- Produces: C15 (`FileSHA256`, `Stage`, `KeepPrevious`, `Swap`, `RestorePrevious`). Top-level names added: `FileSHA256`, `Stage`, `KeepPrevious`, `Swap`, `RestorePrevious`, `hashIfExists`, `copySynced`, `removeIfExists`; tests: `binaryPaths`, `writeBinaryFile`, `sumOf`, `binaryContent`, `TestStageKeepAndSwap`, `TestRestorePrevious`. Task 16 reuses `removeIfExists`; Tasks 17 to 20 reuse `binaryPaths`, `writeBinaryFile`, `sumOf` and `binaryContent` (C21).

**Depends on:** Task 14

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/update/binaries_test.go`:

```go
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
)

// binaryPaths returns a layout under a temporary root whose BinaryDir holds
// an installed binary with content installed.
func binaryPaths(t *testing.T, installed string) paths.Paths {
	t.Helper()
	p := paths.Under(t.TempDir())
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, p.Binary(), installed)
	return p
}

func writeBinaryFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // G306: a test binary is executable
		t.Fatal(err)
	}
}

func sumOf(content string) string {
	s := sha256.Sum256([]byte(content))
	return hex.EncodeToString(s[:])
}

// binaryContent is the content at path, or "<missing>".
func binaryContent(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test path
	if errors.Is(err, fs.ErrNotExist) {
		return "<missing>"
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStageKeepAndSwap(t *testing.T) {
	p := binaryPaths(t, "old binary")
	src := filepath.Join(t.TempDir(), "darkpanel")
	writeBinaryFile(t, src, "new binary")
	if err := os.Chmod(src, 0o600); err != nil {
		t.Fatal(err)
	}

	newSum, err := Stage(p, "01TX", src)
	if err != nil {
		t.Fatal(err)
	}
	if newSum != sumOf("new binary") || binaryContent(t, p.NewBinary("01TX")) != "new binary" {
		t.Fatalf("Stage = %s; staged %q", newSum, binaryContent(t, p.NewBinary("01TX")))
	}
	if info, _ := os.Stat(p.NewBinary("01TX")); info.Mode().Perm() != 0o755 {
		t.Errorf("staged mode = %v, want 0755", info.Mode().Perm())
	}
	if _, err := Stage(p, "../x", src); err == nil {
		t.Error("Stage accepted a bad transaction id")
	}

	writeBinaryFile(t, p.RecoveryBinary(), "an older recovery binary")
	prevSum, err := KeepPrevious(p)
	if err != nil {
		t.Fatal(err)
	}
	if prevSum != sumOf("old binary") || binaryContent(t, p.PrevBinary()) != "old binary" {
		t.Fatalf("KeepPrevious = %s; prev %q", prevSum, binaryContent(t, p.PrevBinary()))
	}
	prev, _ := os.Stat(p.PrevBinary())
	recovery, err := os.Stat(p.RecoveryBinary())
	if err != nil || !os.SameFile(prev, recovery) {
		t.Fatalf("darkpanel.recovery is not a hard link of darkpanel.prev: %v", err)
	}

	if err := Swap(p, "01TX"); err != nil {
		t.Fatal(err)
	}
	if binaryContent(t, p.Binary()) != "new binary" || binaryContent(t, p.NewBinary("01TX")) != "<missing>" {
		t.Errorf("after Swap darkpanel = %q, staged = %q", binaryContent(t, p.Binary()), binaryContent(t, p.NewBinary("01TX")))
	}
	entries, _ := os.ReadDir(p.BinaryDir)
	for _, e := range entries {
		switch e.Name() {
		case "darkpanel", "darkpanel.prev", "darkpanel.recovery":
		default:
			t.Errorf("BinaryDir holds a leftover %s", e.Name())
		}
	}
	if sum, err := FileSHA256(p.Binary()); err != nil || sum != sumOf("new binary") {
		t.Errorf("FileSHA256 = %s, %v", sum, err)
	}
}

func TestRestorePrevious(t *testing.T) {
	oldSum, newSum := sumOf("old"), sumOf("new")
	tests := []struct {
		name                 string
		binary, prev, failed string // content, "" for absent
		code                 errcode.Code
		wantBinary, wantPrev string
		wantFailed           string
	}{
		{"before either rename", "new", "old", "", "", "old", "<missing>", "new"},
		{"after the first rename", "", "old", "new", "", "old", "<missing>", "new"},
		{"after both renames", "old", "", "new", "", "old", "<missing>", "new"},
		{"crash before the swap", "old", "old", "", "", "old", "old", "<missing>"},
		{"previous binary changed", "new", "other", "", errcode.UpdatePrevMismatch, "new", "other", "<missing>"},
		{"previous binary missing", "new", "", "", errcode.UpdatePrevMismatch, "new", "<missing>", "<missing>"},
		{"unknown installed binary", "other", "old", "", errcode.UpdatePrevMismatch, "other", "old", "<missing>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := paths.Under(t.TempDir())
			if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{p.Binary(): tt.binary, p.PrevBinary(): tt.prev, p.FailedBinary(): tt.failed} {
				if content != "" {
					writeBinaryFile(t, path, content)
				}
			}
			err := RestorePrevious(p, oldSum, newSum)
			var ce *errcode.Error
			switch {
			case tt.code == "" && err != nil:
				t.Fatalf("RestorePrevious = %v", err)
			case tt.code != "" && (!errors.As(err, &ce) || ce.Code != tt.code):
				t.Fatalf("RestorePrevious = %v, want %s", err, tt.code)
			}
			got := [3]string{binaryContent(t, p.Binary()), binaryContent(t, p.PrevBinary()), binaryContent(t, p.FailedBinary())}
			if want := [3]string{tt.wantBinary, tt.wantPrev, tt.wantFailed}; got != want {
				t.Errorf("darkpanel, .prev, .failed = %q, want %q", got, want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run 'TestStageKeepAndSwap|TestRestorePrevious'`
Expected: FAIL — `internal/update/binaries_test.go:61:17: undefined: Stage`

- [ ] **Step 3: Write the implementation**

Create `internal/update/binaries.go`:

```go
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/paths"
)

// FileSHA256 is the lower-case hex SHA-256 of the file at path.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: path is under paths.Paths, never from a request
	if err != nil {
		return "", fmt.Errorf("update: hash %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("update: hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Stage copies src, a verified binary, to p.NewBinary(tx) with mode 0755 and
// returns its hash. The staged file sits next to darkpanel, on the same
// filesystem, so the swap is a rename.
func Stage(p paths.Paths, tx, src string) (string, error) {
	if !recordID.MatchString(tx) {
		return "", fmt.Errorf("update: %q is not a transaction id", tx)
	}
	sum, err := copySynced(src, p.NewBinary(tx), 0o755)
	if err != nil {
		return "", errors.Join(fmt.Errorf("update: stage %s: %w", tx, err), removeIfExists(p.NewBinary(tx)))
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return "", fmt.Errorf("update: stage %s: %w", tx, err)
	}
	return sum, nil
}

// KeepPrevious copies the installed binary to darkpanel.prev and hard-links
// that as darkpanel.recovery, which the recovery units run until the update
// is over, and returns the previous binary's hash. Both are replaced by a
// rename, so a crash leaves either the old file or the complete new one.
func KeepPrevious(p paths.Paths) (string, error) {
	tmp := filepath.Join(p.BinaryDir, "darkpanel.prev.tmp")
	sum, err := copySynced(p.Binary(), tmp, 0o755)
	if err != nil {
		return "", errors.Join(fmt.Errorf("update: keep the previous binary: %w", err), removeIfExists(tmp))
	}
	if err := os.Rename(tmp, p.PrevBinary()); err != nil {
		return "", fmt.Errorf("update: keep the previous binary: %w", err)
	}
	link := filepath.Join(p.BinaryDir, "darkpanel.recovery.tmp")
	if err := removeIfExists(link); err != nil {
		return "", fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Link(p.PrevBinary(), link); err != nil {
		return "", fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Rename(link, p.RecoveryBinary()); err != nil {
		return "", fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return "", fmt.Errorf("update: keep the previous binary: %w", err)
	}
	return sum, nil
}

// Swap renames the staged binary of tx over the installed one.
func Swap(p paths.Paths, tx string) error {
	if !recordID.MatchString(tx) {
		return fmt.Errorf("update: %q is not a transaction id", tx)
	}
	if err := os.Rename(p.NewBinary(tx), p.Binary()); err != nil {
		return fmt.Errorf("update: swap %s: %w", tx, err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: swap %s: %w", tx, err)
	}
	return nil
}

// RestorePrevious puts the previous binary back: darkpanel moves to
// darkpanel.failed and darkpanel.prev to darkpanel. It resumes from whichever
// rename a crash left undone, telling the binaries apart by their hashes:
// darkpanel already holding prevSHA means there is nothing left to do (also
// when the crash came before the swap). Anything else it cannot account for,
// a darkpanel.prev that is missing or is not prevSHA, or a darkpanel that is
// neither binary, fails with update.prev_mismatch and renames nothing, so an
// operator finds the files as they were.
func RestorePrevious(p paths.Paths, prevSHA, newSHA string) error {
	bin, err := hashIfExists(p.Binary())
	if err != nil {
		return err
	}
	if bin == prevSHA {
		return nil
	}
	prev, err := hashIfExists(p.PrevBinary())
	if err != nil {
		return err
	}
	switch {
	case prev == "":
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is missing, so the previous binary cannot be restored")
	case prev != prevSHA:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary this update replaced")
	case bin != "" && bin != newSHA:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel is neither the previous nor the updated binary")
	}
	if bin != "" {
		if err := os.Rename(p.Binary(), p.FailedBinary()); err != nil {
			return fmt.Errorf("update: set the failed binary aside: %w", err)
		}
		if err := syncDir(p.BinaryDir); err != nil {
			return fmt.Errorf("update: set the failed binary aside: %w", err)
		}
	}
	if err := os.Rename(p.PrevBinary(), p.Binary()); err != nil {
		return fmt.Errorf("update: restore the previous binary: %w", err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: restore the previous binary: %w", err)
	}
	return nil
}

// hashIfExists is FileSHA256, or "" when there is no file at path.
func hashIfExists(path string) (string, error) {
	sum, err := FileSHA256(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return sum, err
}

// copySynced copies src to dst with perm, syncing dst before it returns the
// copy's hash.
func copySynced(src, dst string, perm os.FileMode) (string, error) {
	in, err := os.Open(src) //nolint:gosec // G304: src is a staged or installed binary path
	if err != nil {
		return "", err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm) //nolint:gosec // G304: dst is under paths.BinaryDir
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if err := out.Chmod(perm); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if err := out.Sync(); err != nil {
		return "", errors.Join(err, out.Close())
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/binaries.go internal/update/binaries_test.go
git commit -m "feat(update): stage, swap and restore binaries"
```

### Task 16: Release download and verification

**Files:**
- Create: `internal/update/download.go`
- Test: `internal/update/download_test.go`, `internal/update/release_test.go` (the signed-release test helpers, C21)

**Interfaces:**
- Consumes: C11 (`ParseVersion`, `Release`, `InChannel`, Task 1), C12 (`NewIndexSource`, `Index`, Task 4), Task 15 (`removeIfExists`), F-C2 (`errcode.UpdateSignatureInvalid`, `errcode.UpdateNotFound`), F-C15 (`trustedkeys.Key`), F-C16 (file names and trusted comment), `aead.dev/minisign` v0.3.0.
- Produces: C16 (`TarballName`, `VerifyChecksums`, `Download`) and the C21 release helpers. Top-level names added: `maxChecksumsBytes`, `maxSignatureBytes`, `maxTarballBytes`, `maxBinaryBytes`, `maxArchiveBytes`, `archName`, `checksumHex`, `errNotServed`, `TarballName`, `Download`, `checksumFor`, `fetch`, `fetchBytes`, `fetchFile`, `extractBinary`, `writeExtracted`, `trustedComment`, `VerifyChecksums`; tests: `TestDownload`, `TestDownloadRefuses`, `TestDownloadBetaHostTakesStableRelease`, `oversizedTarball`, `TestVerifyChecksums`, and in `release_test.go` `testSigner` (method `sign`), `newTestSigner`, `releaseComment`, `tarEntry`, `testTarball`, `releaseServer` (methods `client`, `put`, `get`, `remove`, `publish`, `setIndex`), `newReleaseServer`, `checksumLine`.

**Depends on:** Task 1, Task 4, Task 15

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

Create `internal/update/release_test.go`:

```go
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// testSigner is a generated minisign key pair and its trusted-key entry.
type testSigner struct {
	Key  trustedkeys.Key
	priv minisign.PrivateKey
}

func newTestSigner(t *testing.T) testSigner {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return testSigner{Key: trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}, priv: priv}
}

// sign returns a minisign signature of data in hashed mode (algorithm ED),
// as the release workflow makes them, with comment as its trusted comment.
func (s testSigner) sign(data []byte, comment string) []byte {
	r := minisign.NewReader(bytes.NewReader(data))
	_, _ = io.Copy(io.Discard, r)
	return r.SignWithComments(s.priv, comment, "signature from darkpanel test key "+s.Key.ID)
}

// releaseComment is the trusted comment of a release's checksums.txt (F-C16).
func releaseComment(version, channel string) string {
	return fmt.Sprintf("version=%s channel=%s purpose=checksums", version, channel)
}

// tarEntry is one file of a test tarball; Type zero means a regular file.
type tarEntry struct {
	Name     string
	Body     []byte
	Type     byte
	Linkname string
}

// testTarball builds a gzipped tar archive of entries.
func testTarball(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.Type
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.Name, Typeflag: typ, Linkname: e.Linkname, Mode: 0o755}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.Body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.Body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer serves release files in the production layout under URL:
// index/releases.json and v<version>/<file>. Files can be replaced or removed
// between requests.
type releaseServer struct {
	URL   string
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	s := &releaseServer{files: map[string][]byte{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		body, ok := s.files[strings.TrimPrefix(r.URL.Path, "/")]
		s.mu.Unlock()
		if !ok || r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	s.URL = s.srv.URL
	t.Cleanup(s.srv.Close)
	return s
}

// client is an HTTP client for the server.
func (s *releaseServer) client() *http.Client { return s.srv.Client() }

func (s *releaseServer) put(name string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[name] = bytes.Clone(body)
}

func (s *releaseServer) get(name string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return bytes.Clone(s.files[name])
}

func (s *releaseServer) remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, name)
}

// publish publishes version for arch with binary as its darkpanel: the
// tarball (darkpanel, LICENSE, README.md), checksums.txt listing it and an
// SBOM, and one checksums.txt.<ID>.minisig per signer, each with the trusted
// comment for version and channel.
func (s *releaseServer) publish(t *testing.T, version, channel, arch string, binary []byte, signers ...testSigner) {
	t.Helper()
	tarball := testTarball(t,
		tarEntry{Name: "darkpanel", Body: binary},
		tarEntry{Name: "LICENSE", Body: []byte("license")},
		tarEntry{Name: "README.md", Body: []byte("readme")})
	sbom := []byte(`{"bomFormat": "CycloneDX"}`)
	dir := "v" + version + "/"
	name := TarballName(version, arch)
	s.put(dir+name, tarball)
	s.put(dir+"darkpanel_"+version+".cdx.json", sbom)
	checksums := checksumLine(tarball, name) + checksumLine(sbom, "darkpanel_"+version+".cdx.json")
	s.put(dir+"checksums.txt", []byte(checksums))
	for _, signer := range signers {
		s.put(dir+"checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(checksums), releaseComment(version, channel)))
	}
}

// setIndex publishes index/releases.json listing releases.
func (s *releaseServer) setIndex(t *testing.T, releases ...Release) {
	t.Helper()
	body, err := json.Marshal(Index{FormatVersion: 1, Releases: releases})
	if err != nil {
		t.Fatal(err)
	}
	s.put("index/releases.json", body)
}

// checksumLine is one sha256sum output line for data named name.
func checksumLine(data []byte, name string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "  " + name + "\n"
}
```

Create `internal/update/download_test.go`:

```go
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestDownload(t *testing.T) {
	signer, second := newTestSigner(t), newTestSigner(t)
	srv := newReleaseServer(t)
	srv.publish(t, "1.3.0", "stable", "amd64", []byte("binary 1.3.0"), signer)
	dir := t.TempDir()
	got, err := Download(context.Background(), srv.client(), srv.URL+"/", []trustedkeys.Key{second.Key, signer.Key},
		Release{Version: "1.3.0", Channel: "stable"}, "amd64", dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(dir, "darkpanel") {
		t.Errorf("Download = %s", got)
	}
	if b, _ := os.ReadFile(got); string(b) != "binary 1.3.0" { //nolint:gosec // G304: a test path
		t.Errorf("extracted %q", b)
	}
	if info, _ := os.Stat(got); info.Mode().Perm() != 0o755 {
		t.Errorf("extracted mode %v, want 0755", info.Mode().Perm())
	}
}

func TestDownloadRefuses(t *testing.T) {
	signer := newTestSigner(t)
	keys := []trustedkeys.Key{signer.Key}
	tarballName := TarballName("1.3.0", "amd64")
	// resign replaces the 1.3.0 tarball with one of entries and re-signs
	// checksums.txt, so only the archive's content is wrong.
	resign := func(srv *releaseServer, tarball []byte) {
		srv.put("v1.3.0/"+tarballName, tarball)
		sums := checksumLine(tarball, tarballName)
		srv.put("v1.3.0/checksums.txt", []byte(sums))
		srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(sums), releaseComment("1.3.0", "stable")))
	}
	tests := []struct {
		name    string
		rel     Release
		tamper  func(t *testing.T, srv *releaseServer)
		code    errcode.Code
		message string
	}{
		{"not published", Release{Version: "1.9.0", Channel: "stable"}, nil, errcode.UpdateNotFound, ""},
		{"signature file missing", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			srv.remove("v1.3.0/checksums.txt." + signer.Key.ID + ".minisig")
		}, errcode.UpdateSignatureInvalid, ""},
		{"older checksums.txt replayed", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			srv.put("v1.3.0/checksums.txt", srv.get("v1.2.0/checksums.txt"))
			srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", srv.get("v1.2.0/checksums.txt."+signer.Key.ID+".minisig"))
		}, errcode.UpdateSignatureInvalid, ""},
		{"signed for beta, listed as stable", Release{Version: "1.4.0-beta.1", Channel: "stable"}, nil,
			errcode.UpdateSignatureInvalid, ""},
		{"tarball does not match its line", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			srv.put("v1.3.0/"+tarballName, testTarball(t, tarEntry{Name: "darkpanel", Body: []byte("evil")}))
		}, errcode.UpdateSignatureInvalid, ""},
		{"tarball not listed", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			sums := checksumLine([]byte("x"), "darkpanel_1.3.0_linux_arm64.tar.gz")
			srv.put("v1.3.0/checksums.txt", []byte(sums))
			srv.put("v1.3.0/checksums.txt."+signer.Key.ID+".minisig", signer.sign([]byte(sums), releaseComment("1.3.0", "stable")))
		}, errcode.UpdateNotFound, ""},
		{"checksums.txt over its limit", Release{Version: "1.3.0", Channel: "stable"}, func(_ *testing.T, srv *releaseServer) {
			sums := strings.Repeat("#", 64<<10+1)
			srv.put("v1.3.0/checksums.txt", []byte(sums))
		}, "", "bytes"},
		{"no darkpanel in the archive", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "LICENSE", Body: []byte("l")}))
		}, "", "no darkpanel"},
		{"darkpanel outside the archive root", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "../darkpanel", Body: []byte("x")}, tarEntry{Name: "sub/darkpanel", Body: []byte("x")}))
		}, "", "no darkpanel"},
		{"darkpanel is a symlink", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "darkpanel", Type: tar.TypeSymlink, Linkname: "/bin/sh"}))
		}, "", "regular file"},
		{"darkpanel twice", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, testTarball(t, tarEntry{Name: "darkpanel", Body: []byte("a")}, tarEntry{Name: "./darkpanel", Body: []byte("b")}))
		}, "", "twice"},
		{"darkpanel over its limit", Release{Version: "1.3.0", Channel: "stable"}, func(t *testing.T, srv *releaseServer) {
			resign(srv, oversizedTarball(t))
		}, "", "bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newReleaseServer(t)
			srv.publish(t, "1.2.0", "stable", "amd64", []byte("binary 1.2.0"), signer)
			srv.publish(t, "1.3.0", "stable", "amd64", []byte("binary 1.3.0"), signer)
			srv.publish(t, "1.4.0-beta.1", "beta", "amd64", []byte("binary 1.4.0-beta.1"), signer)
			if tt.tamper != nil {
				tt.tamper(t, srv)
			}
			dir := t.TempDir()
			_, err := Download(context.Background(), srv.client(), srv.URL, keys, tt.rel, "amd64", dir)
			var ce *errcode.Error
			switch {
			case err == nil:
				t.Fatal("Download accepted the release")
			case tt.code != "" && (!errors.As(err, &ce) || ce.Code != tt.code):
				t.Fatalf("Download = %v, want %s", err, tt.code)
			case tt.message != "" && !strings.Contains(err.Error(), tt.message):
				t.Fatalf("Download = %v, want an error mentioning %q", err, tt.message)
			}
			if _, err := os.Stat(filepath.Join(dir, "darkpanel")); !errors.Is(err, os.ErrNotExist) {
				t.Error("a refused release left a darkpanel binary")
			}
		})
	}
}

func TestDownloadBetaHostTakesStableRelease(t *testing.T) {
	signer := newTestSigner(t)
	srv := newReleaseServer(t)
	srv.publish(t, "1.3.0-beta.1", "beta", "arm64", []byte("beta binary"), signer)
	srv.publish(t, "1.3.0", "stable", "arm64", []byte("stable binary"), signer)
	srv.setIndex(t, Release{Version: "1.3.0-beta.1", Channel: "beta"}, Release{Version: "1.3.0", Channel: "stable"})
	releases, err := NewIndexSource(srv.URL, srv.client(), coretest.NewFakeClock(time.Unix(0, 0))).Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	beta := InChannel(releases, "beta")
	newest := beta[len(beta)-1]
	// A host on beta takes the stable entry as listed: the trusted comment is
	// checked against the entry's channel, not the host's.
	bin, err := Download(context.Background(), srv.client(), srv.URL, []trustedkeys.Key{signer.Key}, newest, "arm64", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(bin); string(b) != "stable binary" { //nolint:gosec // G304: a test path
		t.Errorf("a beta host installed %q, want the stable binary", b)
	}
	if _, err := Download(context.Background(), srv.client(), srv.URL, []trustedkeys.Key{signer.Key},
		Release{Version: "1.3.0", Channel: "beta"}, "arm64", t.TempDir()); err == nil {
		t.Error("a stable signature was accepted for a beta entry")
	}
}

// oversizedTarball is a gzipped tar whose darkpanel header claims one byte
// more than the binary limit; the body is never written, because the limit
// is checked before anything is read.
func oversizedTarball(t *testing.T) []byte {
	t.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	if err := tw.WriteHeader(&tar.Header{Name: "darkpanel", Typeflag: tar.TypeReg, Size: maxBinaryBytes + 1, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestVerifyChecksums(t *testing.T) {
	oldKey, newKey, otherKey := newTestSigner(t), newTestSigner(t), newTestSigner(t)
	checksums := []byte(checksumLine([]byte("tarball"), "darkpanel_1.3.0_linux_amd64.tar.gz"))
	comment := releaseComment("1.3.0", "stable")
	signed := func(signers ...testSigner) map[string][]byte {
		sigs := map[string][]byte{}
		for _, s := range signers {
			sigs[s.Key.ID] = s.sign(checksums, comment)
		}
		return sigs
	}
	legacy := minisign.SignWithComments(oldKey.priv, checksums, comment, "legacy")
	keys := func(signers ...testSigner) []trustedkeys.Key {
		var out []trustedkeys.Key
		for _, s := range signers {
			out = append(out, s.Key)
		}
		return out
	}
	tests := []struct {
		name      string
		data      []byte
		sigs      map[string][]byte
		trusted   []trustedkeys.Key
		version   string
		channel   string
		wantValid bool
	}{
		// Key rotation (ADR 0007): a dual-signed release verifies under the
		// old list, the new list and both.
		{"dual-signed, old key trusted", checksums, signed(oldKey, newKey), keys(oldKey), "1.3.0", "stable", true},
		{"dual-signed, new key trusted", checksums, signed(oldKey, newKey), keys(newKey), "1.3.0", "stable", true},
		{"dual-signed, both trusted", checksums, signed(oldKey, newKey), keys(oldKey, newKey), "1.3.0", "stable", true},
		{"new-only release, both trusted", checksums, signed(newKey), keys(oldKey, newKey), "1.3.0", "stable", true},
		{"old-only release, new key trusted", checksums, signed(oldKey), keys(newKey), "1.3.0", "stable", false},
		{"no signature", checksums, map[string][]byte{}, keys(oldKey), "1.3.0", "stable", false},
		{"no trusted key", checksums, signed(oldKey), nil, "1.3.0", "stable", false},
		{"older version replayed", checksums, signed(oldKey), keys(oldKey), "1.4.0", "stable", false},
		{"other channel", checksums, signed(oldKey), keys(oldKey), "1.3.0", "beta", false},
		{"tampered checksums", append([]byte("x"), checksums...), signed(oldKey), keys(oldKey), "1.3.0", "stable", false},
		{"untrusted key under a trusted name", checksums,
			map[string][]byte{oldKey.Key.ID: otherKey.sign(checksums, comment)}, keys(oldKey), "1.3.0", "stable", false},
		{"legacy Ed signature", checksums, map[string][]byte{oldKey.Key.ID: legacy}, keys(oldKey), "1.3.0", "stable", false},
		{"garbage signature, then a good one", checksums,
			map[string][]byte{oldKey.Key.ID: []byte("garbage"), newKey.Key.ID: newKey.sign(checksums, comment)},
			keys(oldKey, newKey), "1.3.0", "stable", true},
		{"malformed trusted key skipped", checksums, signed(newKey),
			[]trustedkeys.Key{{ID: oldKey.Key.ID, PublicKey: "not a key"}, newKey.Key}, "1.3.0", "stable", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyChecksums(tt.data, tt.sigs, tt.trusted, tt.version, tt.channel)
			if tt.wantValid && err != nil {
				t.Fatalf("VerifyChecksums = %v, want nil", err)
			}
			var ce *errcode.Error
			if !tt.wantValid && (!errors.As(err, &ce) || ce.Code != errcode.UpdateSignatureInvalid) {
				t.Fatalf("VerifyChecksums = %v, want %s", err, errcode.UpdateSignatureInvalid)
			}
		})
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 120s ./internal/update/ -run 'TestVerifyChecksums|TestDownload'`
Expected: FAIL — `internal/update/download_test.go:27:14: undefined: Download`

- [ ] **Step 3: Write the implementation**

Create `internal/update/download.go`:

```go
package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// Limits on what a release download reads, far above a real release's sizes.
const (
	maxChecksumsBytes = 64 << 10
	maxSignatureBytes = 4 << 10
	maxTarballBytes   = 256 << 20
	maxBinaryBytes    = 256 << 20
	// maxArchiveBytes bounds the decompressed archive, so a small tarball
	// cannot expand without limit.
	maxArchiveBytes = 512 << 20
)

var (
	archName     = regexp.MustCompile(`^[a-z0-9]+$`)
	checksumHex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errNotServed = errors.New("not published")
)

// TarballName is the release tarball of version for arch (F-C16).
func TarballName(version, arch string) string {
	return fmt.Sprintf("darkpanel_%s_linux_%s.tar.gz", version, arch)
}

// Download fetches rel for arch from <baseURL>/v<version>/ into dir, which
// must be empty: checksums.txt, one signature file per trusted key (a missing
// one is skipped), and the tarball. It verifies the signature against
// rel.Channel, the channel of the selected index entry, and the tarball
// against its checksums.txt line, then extracts the archive root's darkpanel
// to dir/darkpanel with mode 0755 and returns that path. Every URL is built
// here from baseURL, so a caller can name a version but never a location.
func Download(ctx context.Context, client *http.Client, baseURL string, keys []trustedkeys.Key, rel Release, arch, dir string) (string, error) {
	if _, err := ParseVersion(rel.Version); err != nil {
		return "", err
	}
	if !archName.MatchString(arch) {
		return "", fmt.Errorf("update: %q is not an architecture", arch)
	}
	base := strings.TrimSuffix(baseURL, "/") + "/v" + rel.Version + "/"
	checksums, err := fetchBytes(ctx, client, base+"checksums.txt", maxChecksumsBytes)
	if errors.Is(err, errNotServed) {
		return "", errcode.Wrap(errcode.UpdateNotFound, err, fmt.Sprintf("release %s is not published", rel.Version))
	}
	if err != nil {
		return "", err
	}
	sigs := map[string][]byte{}
	for _, k := range keys {
		if sig, err := fetchBytes(ctx, client, base+"checksums.txt."+k.ID+".minisig", maxSignatureBytes); err == nil {
			sigs[k.ID] = sig
		}
	}
	// A cancelled download must not be reported as a bad signature.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := VerifyChecksums(checksums, sigs, keys, rel.Version, rel.Channel); err != nil {
		return "", err
	}
	name := TarballName(rel.Version, arch)
	want, err := checksumFor(checksums, name)
	if err != nil {
		return "", err
	}
	tarball := filepath.Join(dir, name)
	got, err := fetchFile(ctx, client, base+name, tarball, maxTarballBytes)
	if errors.Is(err, errNotServed) {
		return "", errcode.Wrap(errcode.UpdateNotFound, err, fmt.Sprintf("release %s has no %s", rel.Version, name))
	}
	if err != nil {
		return "", err
	}
	if got != want {
		return "", errcode.New(errcode.UpdateSignatureInvalid, fmt.Sprintf("%s does not match the signed checksums.txt", name))
	}
	binary := filepath.Join(dir, "darkpanel")
	if err := extractBinary(tarball, binary); err != nil {
		return "", errors.Join(err, removeIfExists(binary))
	}
	return binary, nil
}

// checksumFor returns the hash checksums.txt lists for name, in sha256sum's
// text ("  ") or binary (" *") form.
func checksumFor(checksums []byte, name string) (string, error) {
	var found []string
	for _, line := range strings.Split(string(checksums), "\n") {
		if len(line) < 67 || !checksumHex.MatchString(line[:64]) {
			continue
		}
		if sep := line[64:66]; (sep == "  " || sep == " *") && line[66:] == name {
			found = append(found, line[:64])
		}
	}
	switch len(found) {
	case 0:
		return "", errcode.New(errcode.UpdateNotFound, fmt.Sprintf("checksums.txt lists no %s", name))
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("update: checksums.txt lists %s %d times", name, len(found))
}

// fetch opens url; an answer of 404 is errNotServed.
func fetch(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("update: download %s: %w", url, errNotServed)
	}
	_ = resp.Body.Close()
	return nil, fmt.Errorf("update: download %s: answered %s", url, resp.Status)
}

func fetchBytes(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update: download %s: over %d bytes", url, limit)
	}
	return data, nil
}

// fetchFile downloads url into a new file at path and returns its hash.
func fetchFile(ctx context.Context, client *http.Client, url, path string, limit int64) (string, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path is under the transaction's download directory
	if err != nil {
		return "", fmt.Errorf("update: download %s: %w", url, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("over %d bytes", limit)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("update: download %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary writes the regular file darkpanel at the archive's root to
// dst with mode 0755. Any other entry is skipped, so a name like
// ../darkpanel or sub/darkpanel never reaches the disk; a second darkpanel,
// or one that is not a regular file, refuses the archive.
func extractBinary(tarball, dst string) error {
	in, err := os.Open(tarball) //nolint:gosec // G304: the tarball Download just wrote
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	defer func() { _ = in.Close() }()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	tr := tar.NewReader(io.LimitReader(gz, maxArchiveBytes))
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("update: extract: %w", err)
		}
		if hdr.Name != "darkpanel" && hdr.Name != "./darkpanel" {
			continue
		}
		switch {
		case found:
			return errors.New("update: extract: the archive holds darkpanel twice")
		case hdr.Typeflag != tar.TypeReg:
			return errors.New("update: extract: darkpanel in the archive is not a regular file")
		case hdr.Size > maxBinaryBytes:
			return fmt.Errorf("update: extract: darkpanel is over %d bytes", maxBinaryBytes)
		}
		if err := writeExtracted(dst, tr, hdr.Size); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return errors.New("update: extract: the archive holds no darkpanel at its root")
	}
	return nil
}

func writeExtracted(dst string, r io.Reader, size int64) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // G302,G304: an executable in the transaction's download directory
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	_, err = io.CopyN(f, r, size)
	if err == nil {
		err = f.Chmod(0o755) //nolint:gosec // G302: the binary must be executable
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	return nil
}

// trustedComment is the exact trusted comment of a release's checksums.txt
// signature (F-C16). It binds the signature to one version and channel, so a
// replayed older checksums.txt or a beta signature cannot pass for another
// release.
func trustedComment(version, channel string) string {
	return fmt.Sprintf("version=%s channel=%s purpose=checksums", version, channel)
}

// VerifyChecksums accepts checksums when one key of keys has a signature in
// sigs, keyed by trustedkeys.Key.ID, that verifies in minisign's hashed mode
// (algorithm ED) with the trusted comment for version and channel. A missing,
// malformed or failing signature is one unsuccessful candidate, so a key
// rotation's dual-signed releases verify under either key (ADR 0007).
// Legacy Ed signatures are refused: the release workflow signs in hashed
// mode only, and install.sh verifies only that mode.
func VerifyChecksums(checksums []byte, sigs map[string][]byte, keys []trustedkeys.Key, version, channel string) error {
	want := trustedComment(version, channel)
	for _, k := range keys {
		raw, ok := sigs[k.ID]
		if !ok {
			continue
		}
		var pub minisign.PublicKey
		if err := pub.UnmarshalText([]byte(k.PublicKey)); err != nil {
			continue
		}
		var sig minisign.Signature
		if err := sig.UnmarshalText(raw); err != nil {
			continue
		}
		if sig.Algorithm != minisign.HashEdDSA || sig.TrustedComment != want {
			continue
		}
		if minisign.Verify(pub, checksums, raw) {
			return nil
		}
	}
	return errcode.New(errcode.UpdateSignatureInvalid,
		fmt.Sprintf("no trusted key signed checksums.txt of version %s for the %s channel", version, channel))
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
GOTOOLCHAIN=local go test -count=1 -race -timeout 120s ./internal/update/
GOTOOLCHAIN=local go vet ./internal/update/
golangci-lint run --allow-serial-runners ./internal/update/...
GOTOOLCHAIN=local go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/update/download.go internal/update/download_test.go internal/update/release_test.go
git commit -m "feat(update): download and verify releases"
```

### Task 17: Updater and operation start

The updater every update process builds: its configuration, transaction ids, the update lock for actions and runners, the start of an update operation (the apply action's Apply) and `core.update.status`. Three rules matter beyond the contract. `StartUpdate` takes the update lock before it reserves, so a recover pass, which runs under the same lock and sweeps reservations that have no record, can never sweep one whose record is about to be written. A new record starts strictly after the newest one (`startTime`), because ULIDs are monotonic only within one process and the records of an operation are ordered by `StartedAt`. `startRunner` waits until systemd reports the new runner unit started, so a recover pass that later finds the unit inactive knows the runner is gone. The fake systemd in `systemd_test.go` records every call; Tasks 18 to 20 reuse it and the environment in `updater_test.go`.

**Files:**
- Create: `internal/update/updater.go`
- Test: `internal/update/updater_test.go`, `internal/update/systemd_test.go`

**Interfaces:**
- Consumes: C11 (`Steps`, `Release`), C12 (`IndexSource`), C13 (`Probes`), C14 (`Record`, `WriteRecord`, `ReadRecord`, `ListRecords`, `OperationRecords`, `AcquireLock`, `Lock`, `ErrLockTimeout`, `LockTimeout`, `recordID`), F-C3 (paths), F-C5 (`core.UpdateApplyInput`, `core.UpdateStatusInput`, `core.UpdateStatus`, `core.UpdateTransaction`, `core.ErrorDetail`), F-C8 (`lifecycle.Reserve`, `Release`, `ReserveAll`, `ReservationOwner`), F-C13 (`platform.Systemd`, `TransientService`, `UnitState`), F-C15 (`trustedkeys.Key`); test helpers of Tasks 14, 15 and 16 (C21: `recordTime`, `testRecord`, `writeBinaryFile`, `newReleaseServer`, `newTestSigner`, `testSigner`)
- Produces: C17; C21 (Task 17 helpers). Top-level names added: `ControllerUnit`, `AgentUnit`, `RollbackUnit`, `ChangeWait`, `RecoverBudget`, `selfTestTimeout`, `statusOperations`, `RunnerUnit`, `RollbackRunnerUnit`, `DeadlineTimer`, `Config`, `Updater` (methods `newID`, `now`, `startTime`, `lock`, `unlock`, `save`, `release`, `reserve`, `begin`, `startRunner`, `CheckApply`, `StartUpdate`, `Status`, `Transaction`), `New`, `detail`, `runSelfTest`; tests: `envReleases`, `binaryOf`, `updaterEnv` (methods `config`, `updater`), `newUpdaterEnv`, `openAgentDB`, `installedVersion`, `lockRows`, `wantNoLocks`, `insertChange`, `wantCode`, `mustRecord`, `systemdCall`, `fakeSystemd` (methods `setState`, `failCall`, `callsOf`, `unitsOf`, `record` and the `platform.Systemd` methods), `newFakeSystemd`, and the `Test*` functions of both files

**Depends on:** Task 1, Task 4, Task 3, Task 14, Task 15, Task 16

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

`internal/update/systemd_test.go`:

```go
package update

import (
	"context"
	"sync"
	"syscall"

	"github.com/darkraise/darkpanel/internal/platform"
)

// systemdCall is one call the fake systemd received.
type systemdCall struct {
	Method  string
	Unit    string
	Service platform.TransientService
	Timer   platform.TransientTimer
	Signal  syscall.Signal
}

// fakeSystemd is a platform.Systemd that records every call and keeps unit
// states in memory: started units are active, stopped ones inactive, killed
// ones failed, and a unit never touched reports LoadState not-found.
type fakeSystemd struct {
	mu     sync.Mutex
	calls  []systemdCall
	states map[string]platform.UnitState
	errs   map[string]error // keyed "<method> <unit>"; unit "" matches any unit
	// onKill runs, outside the lock, after Kill recorded its call; a test
	// stops a frozen runner's work from it.
	onKill func(unit string)
}

var _ platform.Systemd = (*fakeSystemd)(nil)

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{states: map[string]platform.UnitState{}, errs: map[string]error{}}
}

// setState makes unit loaded in activeState.
func (f *fakeSystemd) setState(unit, activeState string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[unit] = platform.UnitState{LoadState: "loaded", ActiveState: activeState, SubState: activeState}
}

// failCall makes method on unit, or on every unit when unit is "", return
// err.
func (f *fakeSystemd) failCall(method, unit string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[method+" "+unit] = err
}

// callsOf returns the calls of method, in order.
func (f *fakeSystemd) callsOf(method string) []systemdCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []systemdCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// unitsOf returns the units method was called on, in order.
func (f *fakeSystemd) unitsOf(method string) []string {
	var out []string
	for _, c := range f.callsOf(method) {
		out = append(out, c.Unit)
	}
	return out
}

// record stores c and returns the error set for it, applying state when the
// call succeeds and state is not empty.
func (f *fakeSystemd) record(c systemdCall, state string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	if err := f.errs[c.Method+" "+c.Unit]; err != nil {
		return err
	}
	if err := f.errs[c.Method+" "]; err != nil {
		return err
	}
	if state != "" {
		f.states[c.Unit] = platform.UnitState{LoadState: "loaded", ActiveState: state, SubState: state}
	}
	return nil
}

func (f *fakeSystemd) StartTransientService(_ context.Context, name string, svc platform.TransientService) error {
	return f.record(systemdCall{Method: "StartTransientService", Unit: name, Service: svc}, "active")
}

func (f *fakeSystemd) StartTransientTimer(_ context.Context, name string, t platform.TransientTimer) error {
	return f.record(systemdCall{Method: "StartTransientTimer", Unit: name, Timer: t}, "active")
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Start", Unit: unit}, "active")
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Stop", Unit: unit}, "inactive")
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	return f.record(systemdCall{Method: "Restart", Unit: unit}, "active")
}

func (f *fakeSystemd) Kill(_ context.Context, unit string, sig syscall.Signal) error {
	err := f.record(systemdCall{Method: "Kill", Unit: unit, Signal: sig}, "failed")
	f.mu.Lock()
	hook := f.onKill
	f.mu.Unlock()
	if err == nil && hook != nil {
		hook(unit)
	}
	return err
}

func (f *fakeSystemd) ResetFailed(_ context.Context, unit string) error {
	f.mu.Lock()
	st, ok := f.states[unit]
	f.mu.Unlock()
	state := ""
	if ok && st.ActiveState == "failed" {
		state = "inactive"
	}
	return f.record(systemdCall{Method: "ResetFailed", Unit: unit}, state)
}

func (f *fakeSystemd) Enable(_ context.Context, unitFiles ...string) error {
	for _, u := range unitFiles {
		if err := f.record(systemdCall{Method: "Enable", Unit: u}, ""); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSystemd) Disable(_ context.Context, unitFiles ...string) error {
	for _, u := range unitFiles {
		if err := f.record(systemdCall{Method: "Disable", Unit: u}, ""); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSystemd) Reload(context.Context) error {
	return f.record(systemdCall{Method: "Reload"}, "")
}

func (f *fakeSystemd) State(_ context.Context, unit string) (platform.UnitState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errs["State "+unit]; err != nil {
		return platform.UnitState{}, err
	}
	if st, ok := f.states[unit]; ok {
		return st, nil
	}
	return platform.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
}
```

`internal/update/updater_test.go`:

```go
package update

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// envReleases are the stable releases every updater test environment
// publishes, plus one beta.
var envReleases = []Release{
	{Version: "1.1.0", Channel: core.ChannelStable},
	{Version: "1.2.0", Channel: core.ChannelStable},
	{Version: "1.3.0", Channel: core.ChannelStable},
	{Version: "1.3.1", Channel: core.ChannelStable},
	{Version: "1.4.0", Channel: core.ChannelStable},
	{Version: "1.5.0", Channel: core.ChannelStable},
	{Version: "1.6.0-beta.1", Channel: core.ChannelBeta},
}

// binaryOf is the content of the fake darkpanel binary of version.
func binaryOf(version string) string { return "darkpanel " + version }

// updaterEnv is a host under a temporary root: BinaryDir with the installed
// fake binary, UpdateDir, a migrated agent.db, a fake systemd, a fake clock
// and a release server publishing envReleases signed by one key. Its probes
// report the version of whatever binary is installed, as restarted services
// would.
type updaterEnv struct {
	p      paths.Paths
	db     *store.DB
	sd     *fakeSystemd
	clock  *coretest.FakeClock
	rel    *releaseServer
	signer testSigner

	mu        sync.Mutex
	selfTests []string
	selfErr   error
	// agentHello, when set, replaces the agent probe's answer.
	agentHello func(installed string) (core.AgentHello, error)
	newer      []string
}

func newUpdaterEnv(t *testing.T, installed string) *updaterEnv {
	t.Helper()
	root := t.TempDir()
	e := &updaterEnv{
		p:      paths.Under(root),
		sd:     newFakeSystemd(),
		clock:  coretest.NewFakeClock(recordTime),
		rel:    newReleaseServer(t),
		signer: newTestSigner(t),
	}
	for _, dir := range []string{e.p.UpdateDir, e.p.BinaryDir, e.p.AgentDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBinaryFile(t, e.p.Binary(), binaryOf(installed))
	e.db = openAgentDB(t, e.p.AgentDB())
	for _, r := range envReleases {
		e.rel.publish(t, r.Version, r.Channel, "amd64", []byte(binaryOf(r.Version)), e.signer)
	}
	e.rel.setIndex(t, envReleases...)
	e.sd.setState(AgentUnit, "active")
	return e
}

// openAgentDB opens and migrates an agent database at path.
func openAgentDB(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 5, Now: recordTime,
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

// installedVersion is the version of the binary at path, "" when it is not
// a fake binary.
func installedVersion(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test path
	if err != nil {
		return ""
	}
	v, ok := strings.CutPrefix(string(b), "darkpanel ")
	if !ok {
		return ""
	}
	return v
}

// config is the updater configuration of a process running version.
func (e *updaterEnv) config(version string) Config {
	return Config{
		Paths: e.p, DB: e.db, Systemd: e.sd, Clock: e.clock, Version: version, Arch: "amd64",
		BaseURL: e.rel.URL, Keys: []trustedkeys.Key{e.signer.Key}, HTTP: e.rel.client(),
		Index: NewIndexSource(e.rel.URL, e.rel.client(), e.clock),
		Probes: Probes{
			Controller: func(context.Context) (ControllerHealth, error) {
				return ControllerHealth{Status: "ok", Version: installedVersion(e.p.Binary())}, nil
			},
			Agent: func(context.Context) (core.AgentHello, error) {
				installed := installedVersion(e.p.Binary())
				e.mu.Lock()
				hook := e.agentHello
				e.mu.Unlock()
				if hook != nil {
					return hook(installed)
				}
				return core.AgentHello{BinaryVersion: installed}, nil
			},
		},
		SelfTest: func(_ context.Context, binary string) error {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.selfTests = append(e.selfTests, binary)
			return e.selfErr
		},
		NewerAgents: func(context.Context, string) ([]string, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return slices.Clone(e.newer), nil
		},
	}
}

// updater is an Updater of a process running version.
func (e *updaterEnv) updater(t *testing.T, version string) *Updater {
	t.Helper()
	u, err := New(e.config(version))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// lockRows lists core_locks as "key owner", sorted.
func lockRows(t *testing.T, db *store.DB) []string {
	t.Helper()
	rows, err := db.Read().Query("SELECT key || ' ' || owner FROM core_locks ORDER BY key, owner")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// wantNoLocks fails unless core_locks is empty.
func wantNoLocks(t *testing.T, db *store.DB) {
	t.Helper()
	if rows := lockRows(t, db); len(rows) != 0 {
		t.Errorf("core_locks = %v, want empty", rows)
	}
}

// insertChange stores a change of class in state, with the plan it needs.
func insertChange(t *testing.T, db *store.DB, id string, class core.Class, state string) {
	t.Helper()
	at := recordTime.Format("2006-01-02T15:04:05.000000000Z")
	err := db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash,
			diff_json, guards_json, confirmation, created_at, expires_at)
			VALUES (?, 'h1', 'demo.x', 1, 'user:a', '', '', '[]', '[]', '', ?, ?)`, "plan-"+id, at, at); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by,
			actor, state, input_json, created_at, updated_at)
			VALUES (?, ?, 'h1', 'demo.x', 1, ?, '', 'user:a', ?, '{}', ?, ?)`, id, "plan-"+id, string(class), state, at, at)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// mustRecord reads the record of id.
func mustRecord(t *testing.T, p paths.Paths, id string) Record {
	t.Helper()
	r, err := ReadRecord(p, id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewRequiresItsDependencies(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	full := e.config("1.2.0")
	for name, change := range map[string]func(*Config){
		"paths":   func(c *Config) { c.Paths = paths.Paths{} },
		"db":      func(c *Config) { c.DB = nil },
		"systemd": func(c *Config) { c.Systemd = nil },
		"clock":   func(c *Config) { c.Clock = nil },
		"version": func(c *Config) { c.Version = "" },
	} {
		cfg := full
		change(&cfg)
		if _, err := New(cfg); err == nil {
			t.Errorf("New without %s succeeded", name)
		}
	}
	if _, err := New(full); err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestStartUpdate(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	tr, err := u.StartUpdate(ctx, "01CHANGE", core.UpdateApplyInput{Version: "1.5.0", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != core.UpdatePending || tr.OperationID != tr.ID || tr.FromVersion != "1.2.0" ||
		tr.ToVersion != "1.3.1" || tr.TargetVersion != "1.5.0" || tr.Kind != core.UpdateKindUpdate {
		t.Errorf("transaction = %+v", tr)
	}
	r := mustRecord(t, e.p, tr.ID)
	wantSteps := []Release{
		{Version: "1.3.1", Channel: core.ChannelStable},
		{Version: "1.4.0", Channel: core.ChannelStable},
		{Version: "1.5.0", Channel: core.ChannelStable},
	}
	if r.ChangeID != "01CHANGE" || !reflect.DeepEqual(r.Steps, wantSteps) {
		t.Errorf("record = %+v", r)
	}
	if units := e.sd.unitsOf("StartTransientService"); !slices.Equal(units, []string{RunnerUnit(tr.ID)}) {
		t.Fatalf("transient units = %v", units)
	}
	starts := e.sd.callsOf("StartTransientService")
	svc := starts[0].Service
	if !slices.Equal(svc.ExecStart, []string{e.p.Binary(), "update", "run", tr.ID}) ||
		!slices.Equal(svc.OnFailure, []string{RollbackUnit}) {
		t.Errorf("runner unit = %+v", svc)
	}
	if got, want := lockRows(t, e.db), []string{"* " + lifecycle.ReservationOwner(tr.ID)}; !slices.Equal(got, want) {
		t.Errorf("core_locks = %v, want %v", got, want)
	}
}

func TestStartUpdateRefusals(t *testing.T) {
	ctx := context.Background()
	stable := func(v string) core.UpdateApplyInput {
		return core.UpdateApplyInput{Version: v, Channel: core.ChannelStable}
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		in    core.UpdateApplyInput
		code  errcode.Code
	}{
		{"downgrade without force", nil, stable("1.1.0"), errcode.UpdateDowngradeRefused},
		{"unpublished", nil, stable("1.9.0"), errcode.UpdateNotFound},
		{"beta release on stable", nil, stable("1.6.0-beta.1"), errcode.UpdateNotFound},
		{"unconfirmed revertible change", func(t *testing.T, e *updaterEnv) {
			insertChange(t, e.db, "01REV", core.ClassRevertible, core.StatePending)
		}, stable("1.3.1"), errcode.UpdateUnconfirmedChanges},
		{"a change holds a lock", func(t *testing.T, e *updaterEnv) {
			if _, err := e.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('ufw', '01CH')"); err != nil {
				t.Fatal(err)
			}
		}, stable("1.3.1"), errcode.UpdateUnconfirmedChanges},
		{"another operation", func(t *testing.T, e *updaterEnv) {
			if err := lifecycle.Reserve(ctx, e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner("01OTHER")); err != nil {
				t.Fatal(err)
			}
		}, stable("1.3.1"), errcode.UpdateInProgress},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			if tt.setup != nil {
				tt.setup(t, e)
			}
			before := lockRows(t, e.db)
			_, err := e.updater(t, "1.2.0").StartUpdate(ctx, "01CHANGE", tt.in)
			wantCode(t, err, tt.code)
			if after := lockRows(t, e.db); !slices.Equal(after, before) {
				t.Errorf("core_locks = %v, want %v", after, before)
			}
			if recs, _ := ListRecords(e.p); len(recs) != 0 {
				t.Errorf("records = %+v, want none", recs)
			}
			if n := len(e.sd.callsOf("StartTransientService")); n != 0 {
				t.Errorf("%d transient units started", n)
			}
		})
	}
}

func TestStartUpdateUnitFailure(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	e.sd.failCall("StartTransientService", "", errors.New("no bus"))
	_, err := u.StartUpdate(ctx, "01CHANGE", core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
	if err == nil {
		t.Fatal("StartUpdate succeeded although the runner unit did not start")
	}
	recs, lerr := ListRecords(e.p)
	if lerr != nil || len(recs) != 1 {
		t.Fatalf("records = %+v, %v", recs, lerr)
	}
	if r := recs[0]; r.State != core.UpdateFailed || r.Error == nil || !strings.Contains(r.Error.Message, "no bus") {
		t.Errorf("record = %+v", r)
	}
	wantNoLocks(t, e.db)
}

func TestCheckApplyHasNoSideEffects(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	steps, err := e.updater(t, "1.2.0").CheckApply(context.Background(),
		core.UpdateApplyInput{Version: "1.4.0", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{steps[0].Version, steps[1].Version}; !slices.Equal(got, []string{"1.3.1", "1.4.0"}) {
		t.Errorf("steps = %v", got)
	}
	wantNoLocks(t, e.db)
	if recs, _ := ListRecords(e.p); len(recs) != 0 {
		t.Errorf("records = %+v", recs)
	}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	// 22 operations of one record each, then a second step of the newest.
	for i := range 22 {
		id := "01OP" + string(rune('A'+i))
		r := testRecord(id, id, time.Duration(i)*time.Minute)
		r.ChangeID = "01CH" + string(rune('A'+i))
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
	}
	child := testRecord("01STEP", "01OPV", 30*time.Minute)
	if err := WriteRecord(e.p, child); err != nil {
		t.Fatal(err)
	}
	ids := func(st core.UpdateStatus) []string {
		var out []string
		for _, tr := range st.Transactions {
			out = append(out, tr.ID)
		}
		return out
	}
	st, err := u.Status(ctx, core.UpdateStatusInput{OperationID: "01OPV"})
	if err != nil || !slices.Equal(ids(st), []string{"01STEP", "01OPV"}) {
		t.Errorf("by operation = %v, %v", ids(st), err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{ChangeID: "01CHV"})
	if err != nil || !slices.Equal(ids(st), []string{"01STEP", "01OPV"}) {
		t.Errorf("by change = %v, %v", ids(st), err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{ChangeID: "01NONE"})
	if err != nil || st.Transactions == nil || len(st.Transactions) != 0 {
		t.Errorf("unknown change = %+v, %v", st, err)
	}
	st, err = u.Status(ctx, core.UpdateStatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(st)
	if len(got) != 21 || got[0] != "01STEP" || got[1] != "01OPV" || got[20] != "01OPC" || slices.Contains(got, "01OPB") {
		t.Errorf("newest 20 operations = %v", got)
	}
	_, err = u.Status(ctx, core.UpdateStatusInput{OperationID: "01OPA", ChangeID: "01CHA"})
	wantCode(t, err, errcode.InvalidInput)
}

func TestTransaction(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	u := e.updater(t, "1.2.0")
	if err := WriteRecord(e.p, testRecord("01TX", "01TX", 0)); err != nil {
		t.Fatal(err)
	}
	tr, err := u.Transaction(ctx, "01TX")
	if err != nil || tr.ID != "01TX" || tr.State != core.UpdatePending {
		t.Errorf("Transaction = %+v, %v", tr, err)
	}
	_, err = u.Transaction(ctx, "01MISSING")
	wantCode(t, err, errcode.NotFound)
}

func TestStartUpdateOrdersAfterTheNewestRecord(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	// A record made when the clock read later, as before the clock was set
	// back.
	old := testRecord("01ZZOLD", "01ZZOLD", time.Hour)
	old.State = core.UpdateFailed
	if err := WriteRecord(e.p, old); err != nil {
		t.Fatal(err)
	}
	tr, err := e.updater(t, "1.2.0").StartUpdate(context.Background(), "01CHANGE",
		core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	if !tr.StartedAt.After(old.StartedAt) {
		t.Errorf("new record started at %s, not after the newest record's %s", tr.StartedAt, old.StartedAt)
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 || all[1].ID != tr.ID {
		t.Errorf("records = %+v, %v; want the new one last", all, err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 300s ./internal/update/`
Expected: FAIL — the package does not compile:

```
internal/update/updater_test.go:82:16: undefined: AgentUnit
internal/update/updater_test.go:124:45: undefined: Config
internal/update/updater_test.go:159:61: undefined: Updater
internal/update/updater_test.go:161:12: undefined: New
```

- [ ] **Step 3: Write the implementation**

`internal/update/updater.go`:

```go
package update

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os/exec"
	"slices"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// The service units an update restarts and the unit their OnFailure= and
// every runner's OnFailure= start (spec section 7, Units).
const (
	ControllerUnit = "darkpanel-controller.service"
	AgentUnit      = "darkpanel-agent.service"
	RollbackUnit   = "darkpanel-rollback.service"
)

// Time limits of spec section 8: the runner waits ChangeWait for the change
// that started it, and one recover command works at most RecoverBudget, so a
// rollback finishes inside the two minutes S5 allows.
const (
	ChangeWait    = 60 * time.Second
	RecoverBudget = 30 * time.Second
)

// selfTestTimeout bounds a staged binary's self-test.
const selfTestTimeout = 30 * time.Second

// statusOperations is how many operations core.update.status reports when no
// operation or change is named.
const statusOperations = 20

// RunnerUnit is the transient unit that runs update transaction tx.
func RunnerUnit(tx string) string { return "darkpanel-update-" + tx + ".service" }

// RollbackRunnerUnit is the transient unit that runs rollback transaction tx.
func RollbackRunnerUnit(tx string) string { return "darkpanel-rollback-" + tx + ".service" }

// DeadlineTimer is the transient timer that rolls tx back when its health
// window ends.
func DeadlineTimer(tx string) string { return "darkpanel-update-deadline-" + tx + ".timer" }

// Config is what an Updater works on. The agent daemon, the runner and the
// recover commands each build one; only the agent's needs Index.
type Config struct {
	Paths   paths.Paths
	DB      *store.DB // agent.db
	Systemd platform.Systemd
	Clock   core.Clock
	Log     *slog.Logger
	Version string            // this binary's version, buildinfo.Version
	Arch    string            // runtime.GOARCH
	BaseURL string            // trustedkeys.BaseURL()
	Keys    []trustedkeys.Key // trustedkeys.Keys()
	HTTP    *http.Client
	Index   *IndexSource
	Probes  Probes
	// SelfTest runs a staged binary's self-test; nil runs "<binary> selftest"
	// with a 30 s limit.
	SelfTest func(ctx context.Context, binary string) error
	// NewerAgents lists enrolled hosts whose agent reports a version newer
	// than version; set on the controller's host only, nil elsewhere.
	NewerAgents func(ctx context.Context, version string) ([]string, error)
}

// Updater runs update and rollback operations on this host.
type Updater struct {
	cfg Config
	// lockTimeout is LockTimeout; tests shorten it.
	lockTimeout time.Duration

	idMu    sync.Mutex // guards entropy
	entropy io.Reader
}

// New returns an updater for cfg.
func New(cfg Config) (*Updater, error) {
	switch {
	case cfg.Paths.UpdateDir == "" || cfg.Paths.BinaryDir == "":
		return nil, errors.New("update: the update and binary directories are required")
	case cfg.DB == nil:
		return nil, errors.New("update: the agent database is required")
	case cfg.Systemd == nil:
		return nil, errors.New("update: systemd is required")
	case cfg.Clock == nil:
		return nil, errors.New("update: a clock is required")
	case cfg.Version == "":
		return nil, errors.New("update: the binary version is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.SelfTest == nil {
		cfg.SelfTest = runSelfTest
	}
	if cfg.HTTP == nil {
		cfg.HTTP = http.DefaultClient
	}
	return &Updater{cfg: cfg, lockTimeout: LockTimeout, entropy: ulid.Monotonic(rand.Reader, 0)}, nil
}

// newID returns a transaction id. Ids made by one process within one
// millisecond still sort in the order they were made, so the records of an
// operation stay in order when the clock does not move.
func (u *Updater) newID() string {
	u.idMu.Lock()
	defer u.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(u.cfg.Clock.Now()), u.entropy).String()
}

func (u *Updater) now() time.Time { return u.cfg.Clock.Now().UTC() }

// startTime is the StartedAt of a new record: now, or just after the newest
// record when the clock reads no later, so records keep the order they were
// made in even when the clock is set back or several processes make records
// within one clock reading.
func (u *Updater) startTime() (time.Time, error) {
	now := u.now()
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return time.Time{}, err
	}
	if n := len(all); n > 0 && !now.After(all[n-1].StartedAt) {
		now = all[n-1].StartedAt.Add(time.Nanosecond)
	}
	return now, nil
}

// lock takes the update lock for a runner or an action, which never stops
// another process to get it.
func (u *Updater) lock(ctx context.Context) (*Lock, error) {
	l, err := AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
	if errors.Is(err, ErrLockTimeout) {
		return nil, errcode.Wrap(errcode.UpdateInProgress, err, "another update process holds the update lock")
	}
	return l, err
}

// unlock releases l, logging a failure: the lock ends with the file anyway.
func (u *Updater) unlock(l *Lock) {
	if err := l.Release(); err != nil {
		u.cfg.Log.Warn("update: release the update lock", "error", err)
	}
}

// save writes r in state with err as its error, stamping UpdatedAt. The
// caller holds the update lock.
func (u *Updater) save(r *Record, state string, err *core.ErrorDetail) error {
	r.State, r.Error, r.UpdatedAt = state, err, u.now()
	return WriteRecord(u.cfg.Paths, *r)
}

// release drops the reservation of operation op.
func (u *Updater) release(ctx context.Context, op string) error {
	return lifecycle.Release(ctx, u.cfg.DB, lifecycle.ReservationOwner(op))
}

// detail is the record error of err: its code when it carries one.
func detail(err error) *core.ErrorDetail {
	e := errcode.From(err)
	msg := e.Detail
	if msg == "" {
		msg = err.Error()
	}
	return &core.ErrorDetail{Code: string(e.Code), Message: msg}
}

// reserve gives a new operation every lock and returns its id. The caller
// holds the update lock, so a recover pass, which sweeps reservations whose
// operation has no record, cannot sweep this one before its record exists.
func (u *Updater) reserve(ctx context.Context) (string, error) {
	tx := u.newID()
	if err := lifecycle.Reserve(ctx, u.cfg.DB, lifecycle.ReserveAll, lifecycle.ReservationOwner(tx)); err != nil {
		return "", err
	}
	return tx, nil
}

// begin records r, the first transaction of a new operation, and starts its
// transient runner unit running argv. The caller holds the update lock and
// the operation's reservation; when anything fails, begin records r failed,
// if r was written, and releases the reservation.
func (u *Updater) begin(ctx context.Context, r Record, unit string, argv []string) error {
	err := WriteRecord(u.cfg.Paths, r)
	if err == nil {
		if err = u.startRunner(ctx, unit, argv); err != nil {
			err = errors.Join(err, u.save(&r, core.UpdateFailed, detail(err)))
		}
	}
	if err != nil {
		return errors.Join(err, u.release(ctx, r.OperationID))
	}
	return nil
}

// startRunner starts a transient runner unit and waits until systemd reports
// it started, so a recover pass that later sees the unit inactive knows the
// runner is gone. The caller holds the update lock, which the new runner
// waits for before it touches its record.
func (u *Updater) startRunner(ctx context.Context, unit string, argv []string) error {
	err := u.cfg.Systemd.StartTransientService(ctx, unit, platform.TransientService{
		Description: "darkpanel update " + unit,
		ExecStart:   argv,
		OnFailure:   []string{RollbackUnit},
	})
	if err != nil {
		return fmt.Errorf("update: start %s: %w", unit, err)
	}
	for range 100 {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			return fmt.Errorf("update: start %s: %w", unit, err)
		}
		if st.ActiveState != "inactive" || st.LoadState == "not-found" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	u.cfg.Log.Warn("update: the runner unit has not started yet", "unit", unit)
	return nil
}

// CheckApply applies the update rules of in without side effects: the
// releases the update would install, target last.
func (u *Updater) CheckApply(ctx context.Context, in core.UpdateApplyInput) ([]Release, error) {
	if u.cfg.Index == nil {
		return nil, errors.New("update: no release index is configured")
	}
	releases, err := u.cfg.Index.Releases(ctx)
	if err != nil {
		return nil, err
	}
	return Steps(u.cfg.Version, in.Version, in.Channel, releases, in.Force)
}

// StartUpdate is core.update.apply's Apply: it checks the rules again and,
// under the update lock, reserves every lock for the new operation, records
// its first transaction pending for changeID and starts the runner, then
// returns before any service restarts (spec section 8, Apply returns before
// the restart).
func (u *Updater) StartUpdate(ctx context.Context, changeID string, in core.UpdateApplyInput) (core.UpdateTransaction, error) {
	steps, err := u.CheckApply(ctx, in)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	now, err := u.startTime()
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	tx, err := u.reserve(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	r := Record{
		ID: tx, OperationID: tx, Kind: core.UpdateKindUpdate,
		FromVersion: u.cfg.Version, ToVersion: steps[0].Version, TargetVersion: in.Version,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, ChangeID: changeID, Steps: steps,
	}
	if err := u.begin(ctx, r, RunnerUnit(tx), []string{u.cfg.Paths.Binary(), "update", "run", tx}); err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// Status answers core.update.status/v1: the records of one operation, of the
// operation a change started, or of the newest 20 operations, newest first.
func (u *Updater) Status(_ context.Context, in core.UpdateStatusInput) (core.UpdateStatus, error) {
	if in.OperationID != "" && in.ChangeID != "" {
		return core.UpdateStatus{}, errcode.New(errcode.InvalidInput, "name an operationId or a changeId, not both")
	}
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return core.UpdateStatus{}, err
	}
	var picked []Record
	switch {
	case in.OperationID != "":
		picked = OperationRecords(all, in.OperationID)
	case in.ChangeID != "":
		if i := slices.IndexFunc(all, func(r Record) bool { return r.ChangeID == in.ChangeID }); i >= 0 {
			picked = OperationRecords(all, all[i].OperationID)
		}
	default:
		ops := map[string]bool{}
		for i := len(all) - 1; i >= 0; i-- {
			if !ops[all[i].OperationID] && len(ops) == statusOperations {
				continue
			}
			ops[all[i].OperationID] = true
		}
		for _, r := range all {
			if ops[r.OperationID] {
				picked = append(picked, r)
			}
		}
	}
	out := make([]core.UpdateTransaction, 0, len(picked))
	for i := len(picked) - 1; i >= 0; i-- {
		out = append(out, picked[i].Transaction())
	}
	return core.UpdateStatus{Transactions: out}, nil
}

// Transaction reads the record of transaction id: core.update.apply's Verify
// checks that it exists and has not failed.
func (u *Updater) Transaction(_ context.Context, id string) (core.UpdateTransaction, error) {
	r, err := ReadRecord(u.cfg.Paths, id)
	if errors.Is(err, fs.ErrNotExist) {
		return core.UpdateTransaction{}, errcode.Wrap(errcode.NotFound, err, "no update transaction "+id)
	}
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// runSelfTest runs "<binary> selftest" with the agent's fixed environment
// (ADR 0009) and a time limit. The binary is the one this updater just
// verified and staged, never a path from a request.
func runSelfTest(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, selfTestTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "selftest") //nolint:gosec // G204: binary is the staged, verified release
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		const keep = 2048
		if len(out) > keep {
			out = out[len(out)-keep:]
		}
		return errcode.Wrap(errcode.UpdateInterrupted, err, fmt.Sprintf("the new binary's self-test failed: %s", out))
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 300s ./internal/update/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`

Run: `GOTOOLCHAIN=local go vet ./internal/update/ && golangci-lint run --allow-serial-runners ./internal/update/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/update/updater.go internal/update/updater_test.go internal/update/systemd_test.go
git commit -m "feat(update): start updates and report status"
```

### Task 18: Rollback

The one rollback implementation and the manual rollbacks built on it (spec section 8, Rollback, Rollback action and Manual rollback). `rollBackLocked` claims `swapped -> rolling-back` under the update lock, so of two racing callers only the claimant renames, and resumes a record found `rolling-back` from whichever rename a crash left undone. `recoveryLock` is the lock of every recover path: after the lock timeout it kills the runner unit of every active record and tries again, so a frozen runner cannot block the deadline rollback. The rollback guards and the rollback action's start (`CheckRollback`, `StartRollback`) live here rather than in Task 17, beside the code they guard. On `update.prev_mismatch` the rollback records failed and releases the reservation through `failKeepingBinaries`, which leaves `BinaryDir` alone, so `darkpanel.recovery` survives as the possibly last copy of the binary `prevSha256` names. `DirectRollback` holds the update lock from writing its record through running it (`runRollbackLocked`), so no recover pass can find its pending record without a runner unit and mark it interrupted.

**Files:**
- Create: `internal/update/rollback.go`
- Test: `internal/update/rollback_test.go`

**Interfaces:**
- Consumes: C14 (`ReadRecord`, `WriteRecord`, `ListRecords`, `OperationRecords`, `Finished`, `AcquireLock`, `ErrLockTimeout`), C15 (`RestorePrevious`, `hashIfExists`, `removeIfExists`, `syncDir`), Task 17 (`Updater`, `lock`, `unlock`, `save`, `release`, `reserve`, `begin`, `startTime`, `detail`, the unit names; test helpers `newUpdaterEnv`, `fakeSystemd`, `lockRows`, `wantNoLocks`, `wantCode`, `mustRecord`, `binaryOf`), Task 15's test helpers (`writeBinaryFile`, `sumOf`, `binaryContent`), Task 14's `testRecord`, F-C8 (`lifecycle.Reserve`, `ReserveAll`, `ReservationOwner`)
- Produces: C18; C21 (Task 18 helpers). Top-level names added: `active`, `runnerUnitOf`, `interruptedBy`; `Updater` methods `recoveryLock`, `RollBack`, `rollBackLocked`, `finish`, `failKeepingBinaries`, `stopTimer`, `restartServices`, `rollbackTarget`, `CheckRollback`, `newRollback`, `StartRollback`, `RunRollback`, `runRollbackLocked`, `linkRecovery`, `DirectRollback`; tests: `swappedUpdate`, `committedUpdate`, `wantRolledBack` and the `Test*` functions

**Depends on:** Task 14, Task 15, Task 17

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

`internal/update/rollback_test.go`:

```go
package update

import (
	"context"
	"os"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// swappedUpdate leaves e as a runner leaves a host right after the swap of
// a from -> to update: to installed, from kept as darkpanel.prev and
// darkpanel.recovery, the record swapped and the reservation held. It
// returns the record.
func swappedUpdate(t *testing.T, e *updaterEnv, tx, from, to string) Record {
	t.Helper()
	p := e.p
	writeBinaryFile(t, p.Binary(), binaryOf(to))
	writeBinaryFile(t, p.PrevBinary(), binaryOf(from))
	if err := os.Link(p.PrevBinary(), p.RecoveryBinary()); err != nil {
		t.Fatal(err)
	}
	deadline := e.clock.Now().Add(HealthDeadline)
	r := testRecord(tx, tx, 0)
	r.FromVersion, r.ToVersion, r.TargetVersion = from, to, to
	r.Steps = []Release{{Version: to, Channel: core.ChannelStable}}
	r.State, r.Deadline = core.UpdateSwapped, &deadline
	r.PrevSHA256, r.NewSHA256 = sumOf(binaryOf(from)), sumOf(binaryOf(to))
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Reserve(context.Background(), e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner(tx)); err != nil {
		t.Fatal(err)
	}
	return r
}

// committedUpdate leaves e as a finished from -> to update leaves it: to
// installed, from kept as darkpanel.prev and the record committed.
func committedUpdate(t *testing.T, e *updaterEnv, tx, from, to string) Record {
	t.Helper()
	writeBinaryFile(t, e.p.Binary(), binaryOf(to))
	writeBinaryFile(t, e.p.PrevBinary(), binaryOf(from))
	r := testRecord(tx, tx, 0)
	r.FromVersion, r.ToVersion, r.TargetVersion = from, to, to
	r.Steps = []Release{{Version: to, Channel: core.ChannelStable}}
	r.State = core.UpdateCommitted
	r.PrevSHA256, r.NewSHA256 = sumOf(binaryOf(from)), sumOf(binaryOf(to))
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	return r
}

// wantRolledBack fails unless from is installed again, to is set aside as
// darkpanel.failed, nothing else is left in BinaryDir and record tx is
// rolled-back with no reservation.
func wantRolledBack(t *testing.T, e *updaterEnv, tx, from, to string) {
	t.Helper()
	p := e.p
	got := map[string]string{
		"darkpanel": binaryContent(t, p.Binary()), "darkpanel.failed": binaryContent(t, p.FailedBinary()),
		"darkpanel.prev": binaryContent(t, p.PrevBinary()), "darkpanel.recovery": binaryContent(t, p.RecoveryBinary()),
		"darkpanel.new": binaryContent(t, p.NewBinary(tx)),
	}
	want := map[string]string{
		"darkpanel": binaryOf(from), "darkpanel.failed": binaryOf(to),
		"darkpanel.prev": "<missing>", "darkpanel.recovery": "<missing>", "darkpanel.new": "<missing>",
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s = %q, want %q", name, got[name], w)
		}
	}
	if r := mustRecord(t, p, tx); r.State != core.UpdateRolledBack {
		t.Errorf("record state = %s, want rolled-back", r.State)
	}
	wantNoLocks(t, e.db)
}

func TestRollBackSwapped(t *testing.T) {
	ctx := context.Background()
	for _, controller := range []bool{false, true} {
		e := newUpdaterEnv(t, "1.2.0")
		if controller {
			e.sd.setState(ControllerUnit, "active")
		}
		swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		e.sd.setState(AgentUnit, "failed")
		if err := e.updater(t, "1.2.0").RollBack(ctx, "01TX", false); err != nil {
			t.Fatal(err)
		}
		wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		r := mustRecord(t, e.p, "01TX")
		if r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) {
			t.Errorf("record error = %+v", r.Error)
		}
		if stops := e.sd.unitsOf("Stop"); !slices.Contains(stops, DeadlineTimer("01TX")) {
			t.Errorf("stopped units = %v, want the deadline timer", stops)
		}
		want := []string{AgentUnit}
		if controller {
			want = []string{ControllerUnit, AgentUnit}
		}
		if got := e.sd.unitsOf("Restart"); !slices.Equal(got, want) {
			t.Errorf("restarted = %v, want %v", got, want)
		}
		if got := e.sd.unitsOf("ResetFailed"); !slices.Equal(got, want) {
			t.Errorf("reset = %v, want %v before the restarts", got, want)
		}
	}
}

func TestRollBackAtBootRestartsNothing(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", true); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if got := e.sd.unitsOf("Restart"); len(got) != 0 {
		t.Errorf("restarted %v at boot", got)
	}
}

func TestRollBackResumesAfterEachStep(t *testing.T) {
	tests := []struct {
		name  string
		crash func(t *testing.T, e *updaterEnv)
	}{
		{"claimed, nothing renamed", func(*testing.T, *updaterEnv) {}},
		{"darkpanel set aside", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
		}},
		{"both renamed", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(e.p.PrevBinary(), e.p.Binary()); err != nil {
				t.Fatal(err)
			}
		}},
		{"both renamed, recovery deleted", func(t *testing.T, e *updaterEnv) {
			if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(e.p.PrevBinary(), e.p.Binary()); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(e.p.RecoveryBinary())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
			r.State = core.UpdateRollingBack
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
			tt.crash(t, e)
			if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", true); err != nil {
				t.Fatal(err)
			}
			wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		})
	}
}

func TestRollBackBeforeTheSwap(t *testing.T) {
	// The runner recorded swapped and crashed before renaming the staged
	// binary over darkpanel.
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	writeBinaryFile(t, e.p.Binary(), binaryOf("1.2.0"))
	writeBinaryFile(t, e.p.NewBinary("01TX"), binaryOf("1.3.0"))
	if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false); err != nil {
		t.Fatal(err)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.2.0") {
		t.Errorf("darkpanel = %q", got)
	}
	if got := binaryContent(t, e.p.NewBinary("01TX")); got != "<missing>" {
		t.Errorf("staged binary = %q, want it deleted", got)
	}
	if r := mustRecord(t, e.p, "01TX"); r.State != core.UpdateRolledBack {
		t.Errorf("state = %s", r.State)
	}
	wantNoLocks(t, e.db)
}

func TestRollBackPrevMismatch(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	// darkpanel.prev replaced by another file, not rewritten in place, so the
	// darkpanel.recovery link still holds the binary prevSha256 names.
	if err := os.Remove(e.p.PrevBinary()); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.PrevBinary(), "something else")
	err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false)
	wantCode(t, err, errcode.UpdatePrevMismatch)
	r := mustRecord(t, e.p, "01TX")
	if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(errcode.UpdatePrevMismatch) {
		t.Errorf("record = %s %+v", r.State, r.Error)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q, want it left alone", got)
	}
	if got := binaryContent(t, e.p.PrevBinary()); got != "something else" {
		t.Errorf("darkpanel.prev = %q, want it left alone", got)
	}
	if got := binaryContent(t, e.p.RecoveryBinary()); got != binaryOf("1.2.0") {
		t.Errorf("darkpanel.recovery = %q, want the binary prevSha256 names kept", got)
	}
	wantNoLocks(t, e.db)
	if got := e.sd.unitsOf("Restart"); len(got) != 0 {
		t.Errorf("restarted %v after a mismatch", got)
	}
}

func TestRollBackIgnoresOtherStates(t *testing.T) {
	for _, state := range []string{core.UpdateCommitted, core.UpdateRolledBack, core.UpdateFailed, core.UpdateStaged} {
		e := newUpdaterEnv(t, "1.2.0")
		r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		r.State = state
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
		if err := e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false); err != nil {
			t.Errorf("%s: %v", state, err)
		}
		if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
			t.Errorf("%s: darkpanel = %q, want it untouched", state, got)
		}
		if got := mustRecord(t, e.p, "01TX").State; got != state {
			t.Errorf("state = %s, want %s", got, state)
		}
	}
}

func TestRollbacksRace(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = e.updater(t, "1.2.0").RollBack(context.Background(), "01TX", false) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("rollback %d: %v", i, err)
		}
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if got := e.sd.unitsOf("Restart"); !slices.Equal(got, []string{AgentUnit}) {
		t.Errorf("restarts = %v, want one claimant to restart the agent once", got)
	}
}

func TestRecoveryLockStopsAFrozenRunner(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	frozen, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	// Killing the runner ends its process and with it the flock it holds.
	e.sd.onKill = func(unit string) {
		if unit == RunnerUnit("01TX") {
			_ = frozen.Release()
		}
	}
	u := e.updater(t, "1.2.0")
	u.lockTimeout = 100 * time.Millisecond
	if err := u.RollBack(context.Background(), "01TX", false); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	kills := e.sd.callsOf("Kill")
	if len(kills) != 1 || kills[0].Unit != RunnerUnit("01TX") || kills[0].Signal != syscall.SIGKILL {
		t.Errorf("kills = %+v", kills)
	}
	if stops := e.sd.unitsOf("Stop"); len(stops) == 0 || stops[0] != RunnerUnit("01TX") {
		t.Errorf("stops = %v, want the runner unit first", stops)
	}
}

func TestStartRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	u := e.updater(t, "1.3.0")
	if err := u.CheckRollback(ctx, core.UpdateRollbackInput{}); err != nil {
		t.Fatal(err)
	}
	tr, err := u.StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	if tr.Kind != core.UpdateKindRollback || tr.State != core.UpdatePending || tr.FromVersion != "1.3.0" ||
		tr.ToVersion != "1.2.0" || tr.TargetVersion != "1.2.0" || tr.OperationID != tr.ID {
		t.Errorf("transaction = %+v", tr)
	}
	r := mustRecord(t, e.p, tr.ID)
	if r.ChangeID != "01CHANGE" || r.PrevSHA256 != sumOf(binaryOf("1.2.0")) || r.NewSHA256 != sumOf(binaryOf("1.3.0")) {
		t.Errorf("record = %+v", r)
	}
	starts := e.sd.callsOf("StartTransientService")
	want := []string{e.p.Binary(), "update", "rollback", "--direct", "--transaction", tr.ID}
	if len(starts) != 1 || starts[0].Unit != RollbackRunnerUnit(tr.ID) || !slices.Equal(starts[0].Service.ExecStart, want) {
		t.Fatalf("transient units = %+v", starts)
	}
	if got := lockRows(t, e.db); !slices.Equal(got, []string{"* " + lifecycle.ReservationOwner(tr.ID)}) {
		t.Errorf("core_locks = %v", got)
	}
}

func TestRollbackGuards(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		force bool
		code  errcode.Code
	}{
		{"no update", func(*testing.T, *updaterEnv) {}, false, errcode.UpdateNothingToRollBack},
		{"no previous binary", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			_ = os.Remove(e.p.PrevBinary())
		}, false, errcode.UpdateNothingToRollBack},
		{"previous binary replaced", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			writeBinaryFile(t, e.p.PrevBinary(), "other")
		}, false, errcode.UpdatePrevMismatch},
		{"update not finished", func(t *testing.T, e *updaterEnv) {
			swappedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
		}, false, errcode.UpdateInProgress},
		{"update rolled back", func(t *testing.T, e *updaterEnv) {
			r := committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			r.State = core.UpdateRolledBack
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
		}, false, errcode.UpdateNothingToRollBack},
		{"update failed", func(t *testing.T, e *updaterEnv) {
			r := committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			r.State = core.UpdateFailed
			if err := WriteRecord(e.p, r); err != nil {
				t.Fatal(err)
			}
		}, false, errcode.UpdateNothingToRollBack},
		{"newer agents", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			e.newer = []string{"01HOSTB"}
		}, false, errcode.UpdateDowngradeRefused},
		{"newer agents with force", func(t *testing.T, e *updaterEnv) {
			committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
			e.newer = []string{"01HOSTB"}
		}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tt.setup(t, e)
			err := e.updater(t, "1.3.0").CheckRollback(context.Background(), core.UpdateRollbackInput{Force: tt.force})
			if tt.code == "" {
				if err != nil {
					t.Fatalf("CheckRollback: %v", err)
				}
				return
			}
			wantCode(t, err, tt.code)
		})
	}
}

func TestRunRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	// The transient unit runs the rollback in a process of its own.
	if err := e.updater(t, "1.3.0").RunRollback(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
	if r := mustRecord(t, e.p, tr.ID); r.Error != nil {
		t.Errorf("record error = %+v, want none", r.Error)
	}
	if got := e.sd.unitsOf("Restart"); !slices.Equal(got, []string{AgentUnit}) {
		t.Errorf("restarts = %v", got)
	}
	// A second run of the same record has nothing left to do.
	if err := e.updater(t, "1.2.0").RunRollback(ctx, tr.ID); err != nil {
		t.Errorf("second RunRollback: %v", err)
	}
}

func TestRunRollbackPrevChanged(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "01CHANGE", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.PrevBinary(), "other")
	err = e.updater(t, "1.3.0").RunRollback(ctx, tr.ID)
	wantCode(t, err, errcode.UpdatePrevMismatch)
	if r := mustRecord(t, e.p, tr.ID); r.State != core.UpdateFailed {
		t.Errorf("state = %s, want failed", r.State)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q, want it untouched", got)
	}
	wantNoLocks(t, e.db)
}

func TestRunRollbackResumesRollingBack(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").StartRollback(ctx, "", core.UpdateRollbackInput{})
	if err != nil {
		t.Fatal(err)
	}
	r := mustRecord(t, e.p, tr.ID)
	r.State = core.UpdateRollingBack
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.3.0").RunRollback(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
}

func TestDirectRollback(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	committedUpdate(t, e, "01UP", "1.2.0", "1.3.0")
	tr, err := e.updater(t, "1.3.0").DirectRollback(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if tr.State != core.UpdateRolledBack || tr.Kind != core.UpdateKindRollback || tr.ToVersion != "1.2.0" {
		t.Errorf("transaction = %+v", tr)
	}
	wantRolledBack(t, e, tr.ID, "1.2.0", "1.3.0")
	if r := mustRecord(t, e.p, tr.ID); r.ChangeID != "" {
		t.Errorf("change id = %q, want none", r.ChangeID)
	}
	_, err = e.updater(t, "1.2.0").DirectRollback(ctx, false)
	wantCode(t, err, errcode.UpdateNothingToRollBack)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 300s ./internal/update/`
Expected: FAIL — the package does not compile:

```
internal/update/rollback_test.go:96:35: e.updater(t, "1.2.0").RollBack undefined (type *Updater has no field or method RollBack)
internal/update/rollback_test.go:123:34: e.updater(t, "1.2.0").RollBack undefined (type *Updater has no field or method RollBack)
```

- [ ] **Step 3: Write the implementation**

`internal/update/rollback.go`:

```go
package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// active reports whether a record's operation may still be working on it.
func active(state string) bool {
	switch state {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged, core.UpdateSwapped, core.UpdateRollingBack:
		return true
	}
	return false
}

// runnerUnitOf is the transient unit that runs r.
func runnerUnitOf(r Record) string {
	if r.Kind == core.UpdateKindRollback {
		return RollbackRunnerUnit(r.ID)
	}
	return RunnerUnit(r.ID)
}

// recoveryLock takes the update lock for a recover path. A runner frozen
// while it holds the lock would block the deadline rollback, so after
// LockTimeout it kills the runner unit of every active record, which ends
// the flock with the process, and tries once more (spec section 8, Taking
// the update lock).
func (u *Updater) recoveryLock(ctx context.Context) (*Lock, error) {
	l, err := AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
	if !errors.Is(err, ErrLockTimeout) {
		return l, err
	}
	all, lerr := ListRecords(u.cfg.Paths)
	if lerr != nil {
		return nil, errors.Join(err, lerr)
	}
	for _, r := range all {
		if !active(r.State) {
			continue
		}
		unit := runnerUnitOf(r)
		u.cfg.Log.Warn("update: the update lock is held; stopping the runner", "unit", unit)
		if kerr := u.cfg.Systemd.Kill(ctx, unit, syscall.SIGKILL); kerr != nil {
			u.cfg.Log.Warn("update: kill the runner", "unit", unit, "error", kerr)
		}
		if serr := u.cfg.Systemd.Stop(ctx, unit); serr != nil {
			u.cfg.Log.Warn("update: stop the runner", "unit", unit, "error", serr)
		}
	}
	return AcquireLock(ctx, u.cfg.Paths.UpdateLock(), u.lockTimeout)
}

// RollBack is the one automatic rollback of a swapped record: under the
// update lock it claims swapped -> rolling-back, puts the previous binary
// back, records rolled-back, releases the reservation, stops the deadline
// timer, deletes darkpanel.recovery and, unless atBoot, restarts the
// services. A record already rolling-back is finished the same way; a record
// in any other state was handled by another caller, and RollBack returns nil.
func (u *Updater) RollBack(ctx context.Context, tx string, atBoot bool) error {
	l, err := u.recoveryLock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	return u.rollBackLocked(ctx, tx, atBoot, interruptedBy("the update was rolled back before it was committed"))
}

// interruptedBy is a record error with code update.interrupted.
func interruptedBy(msg string) *core.ErrorDetail {
	return &core.ErrorDetail{Code: string(errcode.UpdateInterrupted), Message: msg}
}

// rollBackLocked is RollBack with the update lock held. cause becomes the
// record's error unless the record already carries one.
func (u *Updater) rollBackLocked(ctx context.Context, tx string, atBoot bool, cause *core.ErrorDetail) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	switch r.State {
	case core.UpdateSwapped:
		if r.Error == nil {
			r.Error = cause
		}
		if err := u.save(&r, core.UpdateRollingBack, r.Error); err != nil {
			return err
		}
	case core.UpdateRollingBack:
	default:
		return nil
	}
	if err := RestorePrevious(u.cfg.Paths, r.PrevSHA256, r.NewSHA256); err != nil {
		var coded *errcode.Error
		if !errors.As(err, &coded) {
			// The record stays rolling-back, so the next recover pass resumes.
			return err
		}
		u.cfg.Log.Error("update: the previous binary cannot be restored", "transaction", tx, "error", err)
		return errors.Join(err, u.failKeepingBinaries(ctx, &r, detail(err)))
	}
	// A crash between recording swapped and the swap leaves the staged binary.
	if err := removeIfExists(u.cfg.Paths.NewBinary(tx)); err != nil {
		return fmt.Errorf("update: remove the staged binary: %w", err)
	}
	return u.finish(ctx, &r, core.UpdateRolledBack, r.Error, !atBoot)
}

// finish records r terminal and ends its operation: the reservation is
// released, the deadline timer stopped and darkpanel.recovery deleted, and
// with restart the services restarted.
func (u *Updater) finish(ctx context.Context, r *Record, state string, cause *core.ErrorDetail, restart bool) error {
	if err := u.save(r, state, cause); err != nil {
		return err
	}
	var errs []error
	if err := u.release(ctx, r.OperationID); err != nil {
		errs = append(errs, err)
	}
	u.stopTimer(ctx, r.ID)
	if err := removeIfExists(u.cfg.Paths.RecoveryBinary()); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the recovery binary: %w", err))
	}
	if restart {
		errs = append(errs, u.restartServices(ctx))
	}
	return errors.Join(errs...)
}

// failKeepingBinaries records r failed and ends its operation without
// touching BinaryDir: after a mismatch darkpanel.recovery may be the only
// copy left of the binary prevSha256 names, so it is kept for the operator.
func (u *Updater) failKeepingBinaries(ctx context.Context, r *Record, cause *core.ErrorDetail) error {
	if err := u.save(r, core.UpdateFailed, cause); err != nil {
		return err
	}
	err := u.release(ctx, r.OperationID)
	u.stopTimer(ctx, r.ID)
	return err
}

// stopTimer stops tx's deadline timer. A timer that already fired, or never
// existed, has nothing to stop.
func (u *Updater) stopTimer(ctx context.Context, tx string) {
	if err := u.cfg.Systemd.Stop(ctx, DeadlineTimer(tx)); err != nil {
		u.cfg.Log.Debug("update: stop the deadline timer", "timer", DeadlineTimer(tx), "error", err)
	}
}

// restartServices restarts the controller unit when the host has one, then
// the agent unit. A unit that hit its start limit refuses a plain start, so
// its failed state is reset first.
func (u *Updater) restartServices(ctx context.Context) error {
	var errs []error
	for _, unit := range []string{ControllerUnit, AgentUnit} {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			errs = append(errs, fmt.Errorf("update: read %s: %w", unit, err))
			continue
		}
		if st.LoadState == "not-found" {
			continue
		}
		if err := u.cfg.Systemd.ResetFailed(ctx, unit); err != nil {
			u.cfg.Log.Debug("update: reset the failed state", "unit", unit, "error", err)
		}
		if err := u.cfg.Systemd.Restart(ctx, unit); err != nil {
			errs = append(errs, fmt.Errorf("update: restart %s: %w", unit, err))
		}
	}
	return errors.Join(errs...)
}

// rollbackTarget applies the rollback guards: the newest operation is a
// committed update, darkpanel.prev is the binary it replaced, and, on the
// controller's host, no enrolled agent runs a newer version than that binary
// unless force. It returns the update's last record.
func (u *Updater) rollbackTarget(ctx context.Context, force bool) (Record, error) {
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return Record{}, err
	}
	if len(all) == 0 {
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack, "no update has been installed on this host")
	}
	op := OperationRecords(all, all[len(all)-1].OperationID)
	last := op[len(op)-1]
	switch {
	case !Finished(op):
		return Record{}, errcode.New(errcode.UpdateInProgress, fmt.Sprintf("update operation %s is not finished", last.OperationID))
	case last.Kind != core.UpdateKindUpdate || last.State != core.UpdateCommitted:
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack,
			fmt.Sprintf("the newest operation %s did not commit an update", last.OperationID))
	}
	prev, err := hashIfExists(u.cfg.Paths.PrevBinary())
	switch {
	case err != nil:
		return Record{}, err
	case prev == "":
		return Record{}, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
	case prev != last.PrevSHA256:
		return Record{}, errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary the last update replaced")
	}
	if u.cfg.NewerAgents != nil && !force {
		hosts, err := u.cfg.NewerAgents(ctx, last.FromVersion)
		if err != nil {
			return Record{}, err
		}
		if len(hosts) > 0 {
			return Record{}, errcode.New(errcode.UpdateDowngradeRefused, fmt.Sprintf(
				"the agents of hosts %s run a version newer than %s; roll back with force to leave them unsupported",
				strings.Join(hosts, ", "), last.FromVersion))
		}
	}
	return last, nil
}

// CheckRollback applies the rollback guards without side effects: the
// rollback action's Plan.
func (u *Updater) CheckRollback(ctx context.Context, in core.UpdateRollbackInput) error {
	_, err := u.rollbackTarget(ctx, in.Force)
	return err
}

// newRollback applies the rollback guards, reserves every lock for a new
// rollback operation of the last update and returns its first record,
// pending. The caller holds the update lock.
func (u *Updater) newRollback(ctx context.Context, force bool, changeID string) (Record, error) {
	last, err := u.rollbackTarget(ctx, force)
	if err != nil {
		return Record{}, err
	}
	now, err := u.startTime()
	if err != nil {
		return Record{}, err
	}
	tx, err := u.reserve(ctx)
	if err != nil {
		return Record{}, err
	}
	return Record{
		ID: tx, OperationID: tx, Kind: core.UpdateKindRollback,
		FromVersion: last.ToVersion, ToVersion: last.FromVersion, TargetVersion: last.FromVersion,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, ChangeID: changeID,
		Steps: []Release{}, PrevSHA256: last.PrevSHA256, NewSHA256: last.NewSHA256,
	}, nil
}

// StartRollback is core.update.rollback's Apply: the guards, the
// reservation, a rollback record pending, and the transient unit that runs
// it. Its result is observed through core.update.status, so the restart it
// causes never interrupts its own change.
func (u *Updater) StartRollback(ctx context.Context, changeID string, in core.UpdateRollbackInput) (core.UpdateTransaction, error) {
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	r, err := u.newRollback(ctx, in.Force, changeID)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	argv := []string{u.cfg.Paths.Binary(), "update", "rollback", "--direct", "--transaction", r.ID}
	if err := u.begin(ctx, r, RollbackRunnerUnit(r.ID), argv); err != nil {
		return core.UpdateTransaction{}, err
	}
	return r.Transaction(), nil
}

// RunRollback runs rollback record tx, as darkpanel update rollback --direct
// --transaction <tx> does: it links darkpanel.prev as darkpanel.recovery, so
// a crash mid-rename is finished by update recover like any other, claims
// rolling-back and performs an automatic rollback's renames and restarts.
// A rollback is final; there is no health window.
func (u *Updater) RunRollback(ctx context.Context, tx string) error {
	l, err := u.lock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	return u.runRollbackLocked(ctx, tx)
}

// runRollbackLocked is RunRollback with the update lock held.
func (u *Updater) runRollbackLocked(ctx context.Context, tx string) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.Kind != core.UpdateKindRollback {
		return fmt.Errorf("update: transaction %s is not a rollback", tx)
	}
	switch r.State {
	case core.UpdatePending:
	case core.UpdateRollingBack:
		return u.rollBackLocked(ctx, tx, false, nil)
	default:
		return nil
	}
	if err := u.linkRecovery(r); err != nil {
		return errors.Join(err, u.failKeepingBinaries(ctx, &r, detail(err)))
	}
	if err := u.save(&r, core.UpdateRollingBack, nil); err != nil {
		return err
	}
	return u.rollBackLocked(ctx, tx, false, nil)
}

// linkRecovery hard-links darkpanel.prev, which must still be the binary r
// restores, as darkpanel.recovery.
func (u *Updater) linkRecovery(r Record) error {
	p := u.cfg.Paths
	prev, err := hashIfExists(p.PrevBinary())
	switch {
	case err != nil:
		return err
	case prev == "":
		return errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
	case prev != r.PrevSHA256:
		return errcode.New(errcode.UpdatePrevMismatch, "darkpanel.prev is not the binary the last update replaced")
	}
	tmp := filepath.Join(p.BinaryDir, "darkpanel.recovery.tmp")
	if err := removeIfExists(tmp); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Link(p.PrevBinary(), tmp); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := os.Rename(tmp, p.RecoveryBinary()); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	if err := syncDir(p.BinaryDir); err != nil {
		return fmt.Errorf("update: link the recovery binary: %w", err)
	}
	return nil
}

// DirectRollback is darkpanel update rollback --direct without a
// transaction, the break-glass path when no agent answers: the guards, a
// rollback record and RunRollback, all in this process. The update lock is
// held throughout: a recover pass in between would find a pending rollback
// record with no runner unit and mark it interrupted.
func (u *Updater) DirectRollback(ctx context.Context, force bool) (core.UpdateTransaction, error) {
	l, err := u.lock(ctx)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	defer u.unlock(l)
	r, err := u.newRollback(ctx, force, "")
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	if err := WriteRecord(u.cfg.Paths, r); err != nil {
		return core.UpdateTransaction{}, errors.Join(err, u.release(ctx, r.OperationID))
	}
	if err := u.runRollbackLocked(ctx, r.ID); err != nil {
		return core.UpdateTransaction{}, err
	}
	done, err := ReadRecord(u.cfg.Paths, r.ID)
	if err != nil {
		return core.UpdateTransaction{}, err
	}
	return done.Transaction(), nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 300s ./internal/update/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`

Run: `GOTOOLCHAIN=local go vet ./internal/update/ && golangci-lint run --allow-serial-runners ./internal/update/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/update/rollback.go internal/update/rollback_test.go
git commit -m "feat(update): add update rollback"
```

### Task 19: Runner and stepping

`darkpanel update run <tx>` (spec section 8, The runner, Health rule and Stepping). The runner touches its record and `BinaryDir` only under the update lock and re-reads the record each time it takes the lock; a record another process moved on (a deadline rollback during the health window) is left alone. It holds no lock while it downloads, self-tests, restarts the services or health-checks, so a recover path never has to stop a healthy runner. After an intermediate commit it records the next step and starts its runner with the binary just installed, keeping the operation's reservation.

**Files:**
- Create: `internal/update/runner.go`
- Test: `internal/update/runner_test.go`

**Interfaces:**
- Consumes: C13 (`Probes.Wait`, `HealthDeadline`, `HealthInterval`, `sleepOn`), C14, C15 (`Stage`, `KeepPrevious`, `Swap`), C16 (`Download`, `TarballName`), Task 17 (`Updater`, `lock`, `unlock`, `save`, `startTime`, `startRunner`, `detail`, `ChangeWait`, the unit names; test helpers `newUpdaterEnv`, `insertChange`, `lockRows`, `wantNoLocks`, `wantCode`, `mustRecord`, `binaryOf`, `installedVersion`), Task 18 (`rollBackLocked`, `finish`, `stopTimer`, `restartServices`, `interruptedBy`; test helper `wantRolledBack`), Task 16's test helpers (`newTestSigner`), F-C13 (`platform.TransientTimer`)
- Produces: C19; C21 (Task 19 helpers). Top-level names added: `changePoll`, `timerAccuracy`, `errMoved`, `errSwapFailed`; `Updater` methods `Run`, `waitForChange`, `locked`, `downloadDir`, `stage`, `swap`, `probes`, `failBeforeSwap`, `failPreSwap`, `commit`, `nextStep`; tests: `setChangeState`, `driveClock`, `startUpdate`, `lockOwnerOp` and the `Test*` functions

**Depends on:** Task 3, Task 16, Task 17, Task 18

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

`internal/update/runner_test.go`:

```go
package update

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// setChangeState moves change id to state.
func setChangeState(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	if _, err := db.Writer().Exec("UPDATE core_changes SET state = ? WHERE id = ?", state, id); err != nil {
		t.Fatal(err)
	}
}

// driveClock runs fn and advances clock by step every millisecond of real
// time until fn returns, for code that waits on the clock. When first is not
// nil it runs once fn first waits on the clock, before any advance.
func driveClock(clock *coretest.FakeClock, step time.Duration, first func(), fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	if first != nil {
		for clock.Pending() == 0 {
			select {
			case err := <-done:
				return err
			case <-time.After(time.Millisecond):
			}
		}
		first()
	}
	for {
		select {
		case err := <-done:
			return err
		case <-time.After(time.Millisecond):
			clock.Advance(step)
		}
	}
}

// startUpdate starts an update to version from a completed change, as the
// apply action does, and returns its first transaction id.
func startUpdate(t *testing.T, e *updaterEnv, installed, version string) string {
	t.Helper()
	insertChange(t, e.db, "01CHANGE", core.ClassCompensable, core.StateCompleted)
	tr, err := e.updater(t, installed).StartUpdate(context.Background(), "01CHANGE",
		core.UpdateApplyInput{Version: version, Channel: core.ChannelStable})
	if err != nil {
		t.Fatal(err)
	}
	return tr.ID
}

func TestRunUpdate(t *testing.T) {
	for _, controller := range []bool{false, true} {
		e := newUpdaterEnv(t, "1.2.0")
		if controller {
			e.sd.setState(ControllerUnit, "active")
		}
		tx := startUpdate(t, e, "1.2.0", "1.3.1")
		start := e.clock.Now()
		if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err != nil {
			t.Fatal(err)
		}
		r := mustRecord(t, e.p, tx)
		if r.State != core.UpdateCommitted || r.Error != nil {
			t.Fatalf("record = %s %+v", r.State, r.Error)
		}
		if r.NewSHA256 != sumOf(binaryOf("1.3.1")) || r.PrevSHA256 != sumOf(binaryOf("1.2.0")) ||
			r.Deadline == nil || !r.Deadline.Equal(start.Add(HealthDeadline)) {
			t.Errorf("record = %+v", r)
		}
		p := e.p
		for path, want := range map[string]string{
			p.Binary(): binaryOf("1.3.1"), p.PrevBinary(): binaryOf("1.2.0"),
			p.RecoveryBinary(): "<missing>", p.NewBinary(tx): "<missing>",
		} {
			if got := binaryContent(t, path); got != want {
				t.Errorf("%s = %q, want %q", path, got, want)
			}
		}
		if _, err := os.Stat(e.updater(t, "1.2.0").downloadDir(tx)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("download directory left behind: %v", err)
		}
		if !slices.Equal(e.selfTests, []string{p.NewBinary(tx)}) {
			t.Errorf("self-tests = %v", e.selfTests)
		}
		timers := e.sd.callsOf("StartTransientTimer")
		if len(timers) != 1 || timers[0].Unit != DeadlineTimer(tx) {
			t.Fatalf("timers = %+v", timers)
		}
		tm := timers[0].Timer
		if tm.OnActive != HealthDeadline || tm.Accuracy != time.Second ||
			!slices.Equal(tm.Service.ExecStart, []string{p.RecoveryBinary(), "update", "recover", "--deadline", tx}) {
			t.Errorf("timer = %+v", tm)
		}
		if !slices.Contains(e.sd.unitsOf("Stop"), DeadlineTimer(tx)) {
			t.Error("the deadline timer was not stopped after the commit")
		}
		want := []string{AgentUnit}
		if controller {
			want = []string{ControllerUnit, AgentUnit}
		}
		if got := e.sd.unitsOf("Restart"); !slices.Equal(got, want) {
			t.Errorf("restarts = %v, want %v", got, want)
		}
		wantNoLocks(t, e.db)
	}
}

func TestRunSteps(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.5.0")
	owner := "* " + lifecycle.ReservationOwner(tx)
	installed := "1.2.0"
	for _, want := range []string{"1.3.1", "1.4.0", "1.5.0"} {
		if err := e.updater(t, installed).Run(ctx, tx); err != nil {
			t.Fatalf("step to %s: %v", want, err)
		}
		r := mustRecord(t, e.p, tx)
		if r.State != core.UpdateCommitted || r.ToVersion != want || r.OperationID != lockOwnerOp(owner) {
			t.Fatalf("step record = %+v", r)
		}
		if got := binaryContent(t, e.p.Binary()); got != binaryOf(want) {
			t.Fatalf("darkpanel = %q after the step to %s", got, want)
		}
		installed = want
		if want == "1.5.0" {
			break
		}
		// The reservation is held between steps, and the next step's runner
		// was started with the binary just installed.
		if got := lockRows(t, e.db); !slices.Equal(got, []string{owner}) {
			t.Fatalf("core_locks between steps = %v", got)
		}
		starts := e.sd.callsOf("StartTransientService")
		next := starts[len(starts)-1]
		all, err := ListRecords(e.p)
		if err != nil {
			t.Fatal(err)
		}
		child := all[len(all)-1]
		if child.ParentID == nil || *child.ParentID != tx || child.State != core.UpdatePending ||
			child.FromVersion != want || child.ChangeID != "" || child.TargetVersion != "1.5.0" {
			t.Fatalf("next step record = %+v", child)
		}
		if next.Unit != RunnerUnit(child.ID) || !slices.Equal(next.Service.ExecStart, []string{e.p.Binary(), "update", "run", child.ID}) {
			t.Fatalf("next runner = %+v", next)
		}
		tx = child.ID
	}
	wantNoLocks(t, e.db)
	if got := binaryContent(t, e.p.PrevBinary()); got != binaryOf("1.4.0") {
		t.Errorf("darkpanel.prev = %q", got)
	}
	if got := binaryContent(t, e.p.RecoveryBinary()); got != "<missing>" {
		t.Errorf("darkpanel.recovery = %q after the last step", got)
	}
	st, err := e.updater(t, "1.5.0").Status(ctx, core.UpdateStatusInput{ChangeID: "01CHANGE"})
	if err != nil || len(st.Transactions) != 3 {
		t.Errorf("status = %+v, %v", st, err)
	}
}

// lockOwnerOp is the operation id of a "* op:<id>" lock row.
func lockOwnerOp(row string) string { return strings.TrimPrefix(row, "* op:") }

func TestRunWaitsForItsChange(t *testing.T) {
	tests := []struct {
		name  string
		state string
		later func(t *testing.T, e *updaterEnv)
		ok    bool
	}{
		{"completed", core.StateCompleted, nil, true},
		{"completes after the runner started", core.StateApplying, func(t *testing.T, e *updaterEnv) {
			setChangeState(t, e.db, "01CHANGE", core.StateCompleted)
		}, true},
		{"failed", core.StateFailed, nil, false},
		{"interrupted", core.StateApplying, func(t *testing.T, e *updaterEnv) {
			setChangeState(t, e.db, "01CHANGE", core.StateInterrupted)
		}, false},
		{"never completes", core.StateApplying, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			e := newUpdaterEnv(t, "1.2.0")
			insertChange(t, e.db, "01CHANGE", core.ClassCompensable, core.StateApplying)
			tr, err := e.updater(t, "1.2.0").StartUpdate(ctx, "01CHANGE",
				core.UpdateApplyInput{Version: "1.3.1", Channel: core.ChannelStable})
			if err != nil {
				t.Fatal(err)
			}
			setChangeState(t, e.db, "01CHANGE", tt.state)
			u := e.updater(t, "1.2.0")
			var first func()
			if tt.later != nil {
				first = func() { tt.later(t, e) }
			}
			err = driveClock(e.clock, changePoll, first, func() error { return u.Run(ctx, tr.ID) })
			r := mustRecord(t, e.p, tr.ID)
			if tt.ok {
				if err != nil || r.State != core.UpdateCommitted {
					t.Fatalf("Run = %v, state %s", err, r.State)
				}
				return
			}
			wantCode(t, err, errcode.UpdateInterrupted)
			if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) {
				t.Errorf("record = %s %+v", r.State, r.Error)
			}
			if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.2.0") {
				t.Errorf("darkpanel = %q", got)
			}
			if len(e.selfTests) != 0 {
				t.Error("the release was staged although the change did not complete")
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRunWithoutAChangeDoesNotWait(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.3.1")
	r := mustRecord(t, e.p, tx)
	r.ChangeID = ""
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, tx).State; got != core.UpdateCommitted {
		t.Errorf("state = %s", got)
	}
}

func TestRunHealthFailureRollsBack(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(e *updaterEnv, u *Updater)
		detail string
	}{
		{"agent never reports the new version", func(e *updaterEnv, _ *Updater) {
			e.agentHello = func(string) (core.AgentHello, error) { return core.AgentHello{BinaryVersion: "1.2.0"}, nil }
		}, "did not report"},
		{"agent not connected to the controller", func(e *updaterEnv, _ *Updater) {
			disconnected := false
			e.agentHello = func(v string) (core.AgentHello, error) {
				return core.AgentHello{BinaryVersion: v, ControllerConnected: &disconnected}, nil
			}
		}, "did not report"},
		{"controller degraded", func(e *updaterEnv, u *Updater) {
			e.sd.setState(ControllerUnit, "active")
			u.cfg.Probes.Controller = func(context.Context) (ControllerHealth, error) {
				return ControllerHealth{Status: "degraded", Version: installedVersion(e.p.Binary())}, nil
			}
		}, "degraded"},
		{"restart refused", func(e *updaterEnv, _ *Updater) {
			e.sd.failCall("Restart", AgentUnit, errors.New("unit masked"))
		}, "unit masked"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tx := startUpdate(t, e, "1.2.0", "1.3.1")
			u := e.updater(t, "1.2.0")
			tt.setup(e, u)
			err := driveClock(e.clock, HealthInterval, nil, func() error { return u.Run(context.Background(), tx) })
			if err == nil {
				t.Fatal("Run succeeded although the health check failed")
			}
			wantRolledBack(t, e, tx, "1.2.0", "1.3.1")
			r := mustRecord(t, e.p, tx)
			if r.Error == nil || r.Error.Code != string(errcode.UpdateInterrupted) || !strings.Contains(r.Error.Message, tt.detail) {
				t.Errorf("record error = %+v, want %q", r.Error, tt.detail)
			}
		})
	}
}

func TestRunPreSwapFailures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, e *updaterEnv)
		code  errcode.Code
	}{
		{"signed by an untrusted key", func(t *testing.T, e *updaterEnv) {
			e.rel.publish(t, "1.3.1", core.ChannelStable, "amd64", []byte(binaryOf("1.3.1")), newTestSigner(t))
			e.rel.remove("v1.3.1/checksums.txt." + e.signer.Key.ID + ".minisig")
		}, errcode.UpdateSignatureInvalid},
		{"tarball removed", func(_ *testing.T, e *updaterEnv) {
			e.rel.remove("v1.3.1/" + TarballName("1.3.1", "amd64"))
		}, errcode.UpdateNotFound},
		{"self-test fails", func(_ *testing.T, e *updaterEnv) {
			e.selfErr = errcode.New(errcode.UpdateInterrupted, "the new binary's self-test failed")
		}, errcode.UpdateInterrupted},
		{"deadline timer refused", func(_ *testing.T, e *updaterEnv) {
			e.sd.failCall("StartTransientTimer", "", errors.New("no bus"))
		}, errcode.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			tx := startUpdate(t, e, "1.2.0", "1.3.1")
			tt.setup(t, e)
			err := e.updater(t, "1.2.0").Run(context.Background(), tx)
			if err == nil {
				t.Fatal("Run succeeded")
			}
			r := mustRecord(t, e.p, tx)
			if r.State != core.UpdateFailed || r.Error == nil || r.Error.Code != string(tt.code) {
				t.Errorf("record = %s %+v, want failed with %s", r.State, r.Error, tt.code)
			}
			p := e.p
			for path, want := range map[string]string{
				p.Binary(): binaryOf("1.2.0"), p.NewBinary(tx): "<missing>", p.RecoveryBinary(): "<missing>",
			} {
				if got := binaryContent(t, path); got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
			if got := e.sd.unitsOf("Restart"); len(got) != 0 {
				t.Errorf("restarted %v before a swap", got)
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRunLeavesAMovedRecordAlone(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.3.1")
	// The deadline rollback runs while the runner health-checks; the
	// runner must then not commit.
	rolled := false
	e.agentHello = func(string) (core.AgentHello, error) {
		if !rolled {
			rolled = true
			if err := e.updater(t, "1.2.0").RollBack(ctx, tx, false); err != nil {
				t.Error(err)
			}
		}
		return core.AgentHello{BinaryVersion: "1.3.1"}, nil
	}
	if err := e.updater(t, "1.2.0").Run(ctx, tx); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, tx, "1.2.0", "1.3.1")
	// A record past pending is not run again.
	if err := e.updater(t, "1.2.0").Run(ctx, tx); err != nil {
		t.Errorf("second Run: %v", err)
	}
}

func TestRunNextStepStartFailure(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	tx := startUpdate(t, e, "1.2.0", "1.4.0")
	e.sd.failCall("StartTransientService", "", errors.New("no bus"))
	if err := e.updater(t, "1.2.0").Run(context.Background(), tx); err == nil {
		t.Fatal("Run succeeded although the next step did not start")
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 {
		t.Fatalf("records = %+v, %v", all, err)
	}
	if all[0].State != core.UpdateCommitted || all[1].State != core.UpdateFailed {
		t.Errorf("states = %s, %s; want committed, failed", all[0].State, all[1].State)
	}
	// The host stays on the last committed minor.
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.1") {
		t.Errorf("darkpanel = %q", got)
	}
	wantNoLocks(t, e.db)
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 300s ./internal/update/`
Expected: FAIL — the package does not compile:

```
internal/update/runner_test.go:74:35: e.updater(t, "1.2.0").Run undefined (type *Updater has no field or method Run)
internal/update/runner_test.go:94:46: e.updater(t, "1.2.0").downloadDir undefined (type *Updater has no field or method downloadDir)
internal/update/runner_test.go:214:30: undefined: changePoll
```

- [ ] **Step 3: Write the implementation**

`internal/update/runner.go`:

```go
package update

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// changePoll is how often the runner reads the state of the change that
// started it.
const changePoll = 500 * time.Millisecond

// timerAccuracy is the deadline timer's AccuracySec, kept small so the
// rollback stays inside the time budget.
const timerAccuracy = time.Second

// errMoved reports that another process moved a record on while the runner
// was not holding the update lock.
var errMoved = errors.New("update: the record moved on")

// Run is darkpanel update run <tx>: it moves update record tx through
// pending, downloading, staged, swapped and committed (spec section 8, The
// runner), rolls back on a failed health check, and after an intermediate
// commit starts the next step of a stepped operation.
func (u *Updater) Run(ctx context.Context, tx string) error {
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.Kind != core.UpdateKindUpdate {
		return fmt.Errorf("update: transaction %s is not an update", tx)
	}
	if r.State != core.UpdatePending {
		u.cfg.Log.Info("update: the transaction is already past pending", "transaction", tx, "state", r.State)
		return nil
	}
	if err := u.waitForChange(ctx, r.ChangeID); err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	err = u.locked(ctx, tx, core.UpdatePending, func(r *Record) error {
		return u.save(r, core.UpdateDownloading, nil)
	})
	if err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	if err := u.stage(ctx, r); err != nil {
		return u.failBeforeSwap(ctx, tx, err)
	}
	var deadline time.Time
	err = u.locked(ctx, tx, core.UpdateStaged, func(r *Record) error {
		var err error
		deadline, err = u.swap(ctx, r)
		return err
	})
	switch {
	case errors.Is(err, errSwapFailed):
		return err
	case err != nil:
		return u.failBeforeSwap(ctx, tx, err)
	}
	health := u.restartServices(ctx)
	if health == nil {
		health = u.probes(ctx).Wait(ctx, u.cfg.Clock, r.FromVersion, r.ToVersion, deadline)
	}
	l, err := u.lock(ctx)
	if err != nil {
		// The deadline timer rolls the update back.
		return err
	}
	defer u.unlock(l)
	r, err = ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.State != core.UpdateSwapped {
		u.cfg.Log.Info("update: the transaction was finished by another process", "transaction", tx, "state", r.State)
		return nil
	}
	if health != nil {
		return errors.Join(health, u.rollBackLocked(ctx, tx, false, interruptedBy(health.Error())))
	}
	return u.commit(ctx, r)
}

// waitForChange waits up to ChangeWait for the change that started the
// operation to complete, so the restart the update causes can never
// interrupt it. A stepping child has no change and does not wait.
func (u *Updater) waitForChange(ctx context.Context, changeID string) error {
	if changeID == "" {
		return nil
	}
	deadline := u.cfg.Clock.Now().Add(ChangeWait)
	for {
		var state string
		err := u.cfg.DB.Read().QueryRowContext(ctx, "SELECT state FROM core_changes WHERE id = ?", changeID).Scan(&state)
		switch {
		case err == nil && state == core.StateCompleted:
			return nil
		case err == nil && (state == core.StateFailed || state == core.StateInterrupted):
			return errcode.New(errcode.UpdateInterrupted, fmt.Sprintf("change %s, which started the update, is %s", changeID, state))
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("update: read change %s: %w", changeID, err)
		}
		if !u.cfg.Clock.Now().Before(deadline) {
			return errcode.New(errcode.UpdateInterrupted,
				fmt.Sprintf("change %s, which started the update, did not complete within %s", changeID, ChangeWait))
		}
		if err := sleepOn(ctx, u.cfg.Clock, changePoll); err != nil {
			return err
		}
	}
}

// locked runs fn on record tx under the update lock when the record is in
// state from; errMoved when it is not.
func (u *Updater) locked(ctx context.Context, tx, from string, fn func(r *Record) error) error {
	l, err := u.lock(ctx)
	if err != nil {
		return err
	}
	defer u.unlock(l)
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return err
	}
	if r.State != from {
		return fmt.Errorf("%w: transaction %s is %s, not %s", errMoved, tx, r.State, from)
	}
	return fn(&r)
}

func (u *Updater) downloadDir(tx string) string {
	return filepath.Join(u.cfg.Paths.UpdateDir, "download-"+tx)
}

// stage downloads and verifies r's release, stages the binary next to
// darkpanel, records its hash, self-tests it and records staged.
func (u *Updater) stage(ctx context.Context, r Record) error {
	i := slices.IndexFunc(r.Steps, func(s Release) bool { return s.Version == r.ToVersion })
	if i < 0 {
		return fmt.Errorf("update: transaction %s has no step for %s", r.ID, r.ToVersion)
	}
	dir := u.downloadDir(r.ID)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("update: clear the download directory: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return fmt.Errorf("update: create the download directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	u.cfg.Log.Info("update: downloading", "transaction", r.ID, "version", r.ToVersion)
	binary, err := Download(ctx, u.cfg.HTTP, u.cfg.BaseURL, u.cfg.Keys, r.Steps[i], u.cfg.Arch, dir)
	if err != nil {
		return err
	}
	err = u.locked(ctx, r.ID, core.UpdateDownloading, func(r *Record) error {
		sum, err := Stage(u.cfg.Paths, r.ID, binary)
		if err != nil {
			return err
		}
		r.NewSHA256 = sum
		return u.save(r, core.UpdateDownloading, nil)
	})
	if err != nil {
		return err
	}
	if err := u.cfg.SelfTest(ctx, u.cfg.Paths.NewBinary(r.ID)); err != nil {
		return err
	}
	return u.locked(ctx, r.ID, core.UpdateDownloading, func(r *Record) error {
		return u.save(r, core.UpdateStaged, nil)
	})
}

// errSwapFailed marks a failure after the record says swapped; the
// rollback has run already.
var errSwapFailed = errors.New("update: the swap failed and was rolled back")

// swap keeps the previous binary, arms the deadline timer, records swapped
// with its deadline and renames the staged binary over darkpanel, returning
// the deadline. The timer runs darkpanel.recovery, which KeepPrevious has
// just linked, so the rollback works whatever the new binary does.
func (u *Updater) swap(ctx context.Context, r *Record) (time.Time, error) {
	p := u.cfg.Paths
	prev, err := KeepPrevious(p)
	if err != nil {
		return time.Time{}, err
	}
	r.PrevSHA256 = prev
	if err := u.save(r, core.UpdateStaged, nil); err != nil {
		return time.Time{}, err
	}
	err = u.cfg.Systemd.StartTransientTimer(ctx, DeadlineTimer(r.ID), platform.TransientTimer{
		Description: "darkpanel update " + r.ID + " health deadline",
		OnActive:    HealthDeadline,
		Accuracy:    timerAccuracy,
		Service: platform.TransientService{
			Description: "darkpanel update " + r.ID + " deadline rollback",
			ExecStart:   []string{p.RecoveryBinary(), "update", "recover", "--deadline", r.ID},
		},
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("update: start the deadline timer: %w", err)
	}
	deadline := u.now().Add(HealthDeadline)
	r.Deadline = &deadline
	if err := u.save(r, core.UpdateSwapped, nil); err != nil {
		u.stopTimer(ctx, r.ID)
		return time.Time{}, err
	}
	if err := Swap(p, r.ID); err != nil {
		return time.Time{}, errors.Join(errSwapFailed, err, u.rollBackLocked(ctx, r.ID, false, detail(err)))
	}
	return deadline, nil
}

// probes are the configured probes, without the controller's on a host
// that has no controller unit.
func (u *Updater) probes(ctx context.Context) Probes {
	p := u.cfg.Probes
	if st, err := u.cfg.Systemd.State(ctx, ControllerUnit); err == nil && st.LoadState == "not-found" {
		p.Controller = nil
	}
	return p
}

// failBeforeSwap records tx failed with cause when it is still before the
// swap, deletes what it staged and ends its operation. It returns cause.
func (u *Updater) failBeforeSwap(ctx context.Context, tx string, cause error) error {
	if errors.Is(cause, errMoved) {
		u.cfg.Log.Info("update: the transaction was finished by another process", "transaction", tx, "detail", cause)
		return nil
	}
	l, err := u.lock(ctx)
	if err != nil {
		return errors.Join(cause, err)
	}
	defer u.unlock(l)
	r, err := ReadRecord(u.cfg.Paths, tx)
	if err != nil {
		return errors.Join(cause, err)
	}
	switch r.State {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged:
	default:
		return cause
	}
	u.cfg.Log.Error("update: the update failed before the swap", "transaction", tx, "error", cause)
	return errors.Join(cause, u.failPreSwap(ctx, &r, detail(cause)))
}

// failPreSwap records r failed before its swap: the staged binary and the
// download are deleted and the operation ends. The caller holds the lock.
func (u *Updater) failPreSwap(ctx context.Context, r *Record, cause *core.ErrorDetail) error {
	var errs []error
	if err := removeIfExists(u.cfg.Paths.NewBinary(r.ID)); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the staged binary: %w", err))
	}
	if err := os.RemoveAll(u.downloadDir(r.ID)); err != nil {
		errs = append(errs, fmt.Errorf("update: remove the download: %w", err))
	}
	return errors.Join(append(errs, u.finish(ctx, r, core.UpdateFailed, cause, false))...)
}

// commit records r committed after a passed health check. The last step
// ends the operation; an intermediate one keeps the reservation and starts
// the next step. The caller holds the lock.
func (u *Updater) commit(ctx context.Context, r Record) error {
	if err := u.save(&r, core.UpdateCommitted, nil); err != nil {
		return err
	}
	u.stopTimer(ctx, r.ID)
	if r.ToVersion == r.TargetVersion {
		u.cfg.Log.Info("update: committed", "transaction", r.ID, "version", r.ToVersion)
		var errs []error
		if err := removeIfExists(u.cfg.Paths.RecoveryBinary()); err != nil {
			errs = append(errs, fmt.Errorf("update: remove the recovery binary: %w", err))
		}
		return errors.Join(append(errs, u.release(ctx, r.OperationID))...)
	}
	return u.nextStep(ctx, r)
}

// nextStep records the step after the committed intermediate step r pending
// and starts its runner with the binary r installed. The caller holds the
// lock; the new runner waits for it.
func (u *Updater) nextStep(ctx context.Context, r Record) error {
	i := slices.IndexFunc(r.Steps, func(s Release) bool { return s.Version == r.ToVersion })
	if i < 0 || i+1 >= len(r.Steps) {
		return fmt.Errorf("update: transaction %s has no step after %s", r.ID, r.ToVersion)
	}
	now, err := u.startTime()
	if err != nil {
		return err
	}
	parent := r.ID
	child := Record{
		ID: u.newID(), OperationID: r.OperationID, ParentID: &parent, Kind: core.UpdateKindUpdate,
		FromVersion: r.ToVersion, ToVersion: r.Steps[i+1].Version, TargetVersion: r.TargetVersion,
		State: core.UpdatePending, StartedAt: now, UpdatedAt: now, Steps: r.Steps,
	}
	if err := WriteRecord(u.cfg.Paths, child); err != nil {
		return err
	}
	u.cfg.Log.Info("update: starting the next step", "transaction", child.ID, "version", child.ToVersion)
	argv := []string{u.cfg.Paths.Binary(), "update", "run", child.ID}
	if err := u.startRunner(ctx, RunnerUnit(child.ID), argv); err != nil {
		return errors.Join(err, u.failPreSwap(ctx, &child, detail(err)))
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 300s ./internal/update/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`

Run: `GOTOOLCHAIN=local go vet ./internal/update/ && golangci-lint run --allow-serial-runners ./internal/update/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/update/runner.go internal/update/runner_test.go
git commit -m "feat(update): add the update runner"
```

### Task 20: Recovery

`darkpanel update recover` in its three forms and the agent daemon's start (spec section 8, Pre-swap recovery, Rollback, Stepping). Under the update lock it fails every record before its swap whose runner unit is gone, rolls back every swapped or rolling-back record (at agent start: none; with `--deadline`: its own only), starts the next step of an operation that crashed between an intermediate commit and the next record, and finally releases the reservations of operations that are finished or have no record. The agent's start takes the plain lock and never kills a runner, because the runner itself restarted the agent and is health-checking it.

**Files:**
- Create: `internal/update/recover.go`
- Test: `internal/update/recover_test.go`

**Interfaces:**
- Consumes: C14 (`ListRecords`, `OperationRecords`, `Finished`, `recordID`), Task 17 (`Updater`, `lock`, `unlock`, `RecoverBudget`, the unit names; test helpers `newUpdaterEnv`, `lockRows`, `wantNoLocks`, `wantCode`, `mustRecord`, `binaryOf`, `fakeSystemd`), Task 18 (`recoveryLock`, `rollBackLocked`, `runnerUnitOf`, `interruptedBy`; test helpers `swappedUpdate`, `wantRolledBack`), Task 19 (`failPreSwap`, `nextStep`, `downloadDir`, `Run`), F-C8 (`lifecycle.Release`, `ReservationOwner`), F-C13 (`platform.UnitState`)
- Produces: C20. Top-level names added: `RecoverMode`, `RecoverStart`, `RecoverBoot`, `RecoverFailure`, `RecoverDeadline`, `recoverCause`, `runnerAlive`, `operations`; `Updater` methods `Recover`, `recoverPreSwap`, `continueSteps`, `sweep`; tests: `crashedRunner` and the `Test*` functions

**Depends on:** Task 17, Task 18, Task 19

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

`internal/update/recover_test.go`:

```go
package update

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// crashedRunner leaves e as a runner killed in state leaves it, for an
// update from 1.2.0 to 1.3.1 with the reservation held, and returns the
// record. staged writes darkpanel.new-<tx>, kept keeps the previous binary
// as darkpanel.prev and darkpanel.recovery, renamed swaps.
func crashedRunner(t *testing.T, e *updaterEnv, state string, staged, kept, renamed bool) Record {
	t.Helper()
	p := e.p
	r := testRecord("01TX", "01TX", 0)
	r.ToVersion, r.TargetVersion = "1.3.1", "1.3.1"
	r.Steps = []Release{{Version: "1.3.1", Channel: core.ChannelStable}}
	r.State = state
	if staged {
		writeBinaryFile(t, p.NewBinary(r.ID), binaryOf("1.3.1"))
		r.NewSHA256 = sumOf(binaryOf("1.3.1"))
	}
	if kept {
		writeBinaryFile(t, p.PrevBinary(), binaryOf("1.2.0"))
		if err := os.Link(p.PrevBinary(), p.RecoveryBinary()); err != nil {
			t.Fatal(err)
		}
		r.PrevSHA256 = sumOf(binaryOf("1.2.0"))
	}
	if renamed {
		if err := os.Rename(p.NewBinary(r.ID), p.Binary()); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteRecord(p, r); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Reserve(context.Background(), e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner(r.ID)); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestRecoverAfterACrashAtEachRunnerStep(t *testing.T) {
	tests := []struct {
		name                  string
		state                 string
		staged, kept, renamed bool
		download              bool
		want                  string
	}{
		{"pending", core.UpdatePending, false, false, false, false, core.UpdateFailed},
		{"downloading", core.UpdateDownloading, false, false, false, true, core.UpdateFailed},
		{"binary staged", core.UpdateDownloading, true, false, false, false, core.UpdateFailed},
		{"staged", core.UpdateStaged, true, false, false, false, core.UpdateFailed},
		{"previous binary kept", core.UpdateStaged, true, true, false, false, core.UpdateFailed},
		{"swapped, not renamed", core.UpdateSwapped, true, true, false, false, core.UpdateRolledBack},
		{"swapped and renamed", core.UpdateSwapped, true, true, true, false, core.UpdateRolledBack},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newUpdaterEnv(t, "1.2.0")
			u := e.updater(t, "1.2.0")
			r := crashedRunner(t, e, tt.state, tt.staged, tt.kept, tt.renamed)
			if tt.download {
				if err := os.MkdirAll(u.downloadDir(r.ID), 0o700); err != nil {
					t.Fatal(err)
				}
				writeBinaryFile(t, filepath.Join(u.downloadDir(r.ID), "partial"), "half a tarball")
			}
			e.sd.setState(RunnerUnit(r.ID), "failed")
			if err := u.Recover(context.Background(), RecoverFailure, ""); err != nil {
				t.Fatal(err)
			}
			got := mustRecord(t, e.p, r.ID)
			if got.State != tt.want || got.Error == nil || got.Error.Code != string(errcode.UpdateInterrupted) {
				t.Errorf("record = %s %+v, want %s with update.interrupted", got.State, got.Error, tt.want)
			}
			p := e.p
			for path, want := range map[string]string{
				p.Binary(): binaryOf("1.2.0"), p.NewBinary(r.ID): "<missing>", p.RecoveryBinary(): "<missing>",
			} {
				if c := binaryContent(t, path); c != want {
					t.Errorf("%s = %q, want %q", path, c, want)
				}
			}
			if _, err := os.Stat(u.downloadDir(r.ID)); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("download directory left behind: %v", err)
			}
			wantNoLocks(t, e.db)
		})
	}
}

func TestRecoverSparesALiveRunner(t *testing.T) {
	for _, active := range []string{"active", "activating", "deactivating"} {
		e := newUpdaterEnv(t, "1.2.0")
		r := crashedRunner(t, e, core.UpdateDownloading, true, false, false)
		e.sd.setState(RunnerUnit(r.ID), active)
		if err := e.updater(t, "1.2.0").Recover(context.Background(), RecoverStart, ""); err != nil {
			t.Fatal(err)
		}
		if got := mustRecord(t, e.p, r.ID).State; got != core.UpdateDownloading {
			t.Errorf("%s runner: state = %s, want it left downloading", active, got)
		}
		if got := binaryContent(t, e.p.NewBinary(r.ID)); got != binaryOf("1.3.1") {
			t.Errorf("%s runner: staged binary = %q", active, got)
		}
		if got := lockRows(t, e.db); len(got) != 1 {
			t.Errorf("%s runner: core_locks = %v, want the reservation kept", active, got)
		}
	}
}

func TestRecoverStartLeavesASwappedUpdate(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, "01TX").State; got != core.UpdateSwapped {
		t.Errorf("state = %s, want swapped: the runner is health-checking", got)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.3.0") {
		t.Errorf("darkpanel = %q", got)
	}
	if got := lockRows(t, e.db); len(got) != 1 {
		t.Errorf("core_locks = %v, want the reservation kept", got)
	}
}

func TestRecoverRollsBack(t *testing.T) {
	tests := []struct {
		mode     RecoverMode
		restarts bool
		message  string
	}{
		{RecoverBoot, false, "host restarted"},
		{RecoverFailure, true, "failed before"},
		{RecoverDeadline, true, "deadline"},
	}
	for _, tt := range tests {
		e := newUpdaterEnv(t, "1.2.0")
		swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
		// A crash-looping agent has hit its start limit.
		e.sd.setState(AgentUnit, "failed")
		if err := e.updater(t, "1.2.0").Recover(context.Background(), tt.mode, "01TX"); err != nil {
			t.Fatalf("mode %d: %v", tt.mode, err)
		}
		wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
		if r := mustRecord(t, e.p, "01TX"); r.Error == nil || !strings.Contains(r.Error.Message, tt.message) {
			t.Errorf("mode %d: record error = %+v, want %q", tt.mode, r.Error, tt.message)
		}
		restarts := e.sd.unitsOf("Restart")
		if tt.restarts != (len(restarts) > 0) {
			t.Errorf("mode %d: restarts = %v", tt.mode, restarts)
		}
		if tt.restarts {
			e.sd.mu.Lock()
			calls := slices.Clone(e.sd.calls)
			e.sd.mu.Unlock()
			reset := slices.IndexFunc(calls, func(c systemdCall) bool { return c.Method == "ResetFailed" && c.Unit == AgentUnit })
			restart := slices.IndexFunc(calls, func(c systemdCall) bool { return c.Method == "Restart" && c.Unit == AgentUnit })
			if reset < 0 || restart < reset {
				t.Errorf("mode %d: the agent was restarted without its failed state reset first", tt.mode)
			}
		}
	}
}

func TestRecoverBootResumesAfterARename(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	r := swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	r.State = core.UpdateRollingBack
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(e.p.Binary(), e.p.FailedBinary()); err != nil {
		t.Fatal(err)
	}
	if err := e.updater(t, "1.2.0").Recover(context.Background(), RecoverBoot, ""); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
}

func TestRecoverDeadlineTouchesOnlyItsTransaction(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverDeadline, "01OTHER"); err != nil {
		t.Fatal(err)
	}
	if got := mustRecord(t, e.p, "01TX").State; got != core.UpdateSwapped {
		t.Errorf("state = %s, want swapped", got)
	}
	if err := e.updater(t, "1.3.0").Recover(context.Background(), RecoverDeadline, ""); err == nil {
		t.Error("RecoverDeadline accepted an empty transaction id")
	}
}

func TestRecoverDeadlineStopsAFrozenRunner(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	e.sd.setState(RunnerUnit("01TX"), "active")
	frozen, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	e.sd.onKill = func(unit string) {
		if unit == RunnerUnit("01TX") {
			_ = frozen.Release()
		}
	}
	u := e.updater(t, "1.2.0")
	u.lockTimeout = 100 * time.Millisecond
	if err := u.Recover(context.Background(), RecoverDeadline, "01TX"); err != nil {
		t.Fatal(err)
	}
	wantRolledBack(t, e, "01TX", "1.2.0", "1.3.0")
	if kills := e.sd.unitsOf("Kill"); !slices.Equal(kills, []string{RunnerUnit("01TX")}) {
		t.Errorf("kills = %v", kills)
	}
}

func TestRecoverStartWaitsForTheLock(t *testing.T) {
	e := newUpdaterEnv(t, "1.2.0")
	swappedUpdate(t, e, "01TX", "1.2.0", "1.3.0")
	held, err := AcquireLock(context.Background(), e.p.UpdateLock(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	u := e.updater(t, "1.3.0")
	u.lockTimeout = 50 * time.Millisecond
	err = u.Recover(context.Background(), RecoverStart, "")
	wantCode(t, err, errcode.UpdateInProgress)
	if kills := e.sd.unitsOf("Kill"); len(kills) != 0 {
		t.Errorf("agent start killed %v", kills)
	}
}

func TestRecoverContinuesAStep(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	// The runner committed 1.3.1 of a 1.2.0 -> 1.4.0 update and crashed
	// before it recorded the next step.
	r := testRecord("01TX", "01TX", 0)
	r.ToVersion, r.TargetVersion, r.State = "1.3.1", "1.4.0", core.UpdateCommitted
	r.Steps = []Release{{Version: "1.3.1", Channel: core.ChannelStable}, {Version: "1.4.0", Channel: core.ChannelStable}}
	if err := WriteRecord(e.p, r); err != nil {
		t.Fatal(err)
	}
	writeBinaryFile(t, e.p.Binary(), binaryOf("1.3.1"))
	if err := lifecycle.Reserve(ctx, e.db, lifecycle.ReserveAll, lifecycle.ReservationOwner("01TX")); err != nil {
		t.Fatal(err)
	}
	u := e.updater(t, "1.3.1")
	if err := u.Recover(ctx, RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	all, err := ListRecords(e.p)
	if err != nil || len(all) != 2 {
		t.Fatalf("records = %+v, %v", all, err)
	}
	child := all[1]
	if child.State != core.UpdatePending || child.ParentID == nil || *child.ParentID != "01TX" ||
		child.FromVersion != "1.3.1" || child.ToVersion != "1.4.0" || child.OperationID != "01TX" {
		t.Errorf("next step = %+v", child)
	}
	starts := e.sd.callsOf("StartTransientService")
	if len(starts) != 1 || starts[0].Unit != RunnerUnit(child.ID) {
		t.Fatalf("started = %+v", starts)
	}
	if got := lockRows(t, e.db); !slices.Equal(got, []string{"* " + lifecycle.ReservationOwner("01TX")}) {
		t.Errorf("core_locks = %v, want the reservation kept for the next step", got)
	}
	// The started step runs to the target and releases the reservation.
	if err := e.updater(t, "1.3.1").Run(ctx, child.ID); err != nil {
		t.Fatal(err)
	}
	if got := binaryContent(t, e.p.Binary()); got != binaryOf("1.4.0") {
		t.Errorf("darkpanel = %q", got)
	}
	wantNoLocks(t, e.db)
}

func TestRecoverSweep(t *testing.T) {
	ctx := context.Background()
	e := newUpdaterEnv(t, "1.2.0")
	write := func(id, state, to, target string, offset time.Duration) {
		r := testRecord(id, id, offset)
		r.State, r.ToVersion, r.TargetVersion = state, to, target
		if err := WriteRecord(e.p, r); err != nil {
			t.Fatal(err)
		}
	}
	write("01DONE", core.UpdateCommitted, "1.3.0", "1.3.0", 0)
	write("01FAIL", core.UpdateFailed, "1.3.0", "1.3.0", time.Minute)
	write("01LIVE", core.UpdateSwapped, "1.3.0", "1.3.0", 2*time.Minute)
	for _, row := range [][2]string{
		{"*", "op:01NONE"}, {"a", "op:01DONE"}, {"b", "op:01FAIL"}, {"c", "op:01LIVE"}, {"ufw", "01CHANGE"},
	} {
		if _, err := e.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES (?, ?)", row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.updater(t, "1.2.0").Recover(ctx, RecoverStart, ""); err != nil {
		t.Fatal(err)
	}
	want := []string{"c op:01LIVE", "ufw 01CHANGE"}
	if got := lockRows(t, e.db); !slices.Equal(got, want) {
		t.Errorf("core_locks = %v, want %v: only finished operations' reservations go", got, want)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -timeout 300s ./internal/update/`
Expected: FAIL — the package does not compile:

```
internal/update/recover_test.go:83:16: u.Recover undefined (type *Updater has no field or method Recover)
internal/update/recover_test.go:83:46: undefined: RecoverFailure
internal/update/recover_test.go:111:65: undefined: RecoverStart
```

- [ ] **Step 3: Write the implementation**

`internal/update/recover.go`:

```go
package update

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/platform"
)

// RecoverMode says which caller runs Recover.
type RecoverMode int

const (
	// RecoverStart is the agent daemon's start: no rollback, no restarts.
	RecoverStart RecoverMode = iota
	// RecoverBoot is darkpanel-update-recover.service, ordered before the
	// services: rollbacks rename and record but restart nothing.
	RecoverBoot
	// RecoverFailure is darkpanel-rollback.service, started by OnFailure= of
	// a service or a runner: rollbacks restart the services.
	RecoverFailure
	// RecoverDeadline is darkpanel update recover --deadline <tx>, run by the
	// deadline timer: it rolls back tx only.
	RecoverDeadline
)

// Recover brings the update records to a consistent state, within
// RecoverBudget and under the update lock (spec section 8): pre-swap
// recovery, then, except at agent start, the rollback of every swapped or
// rolling-back record (with RecoverDeadline, of tx only), then stepping
// continuation, then the sweep of finished operations' reservations. Every
// step runs even when an earlier one failed; the errors are joined.
func (u *Updater) Recover(ctx context.Context, mode RecoverMode, tx string) error {
	if mode == RecoverDeadline && !recordID.MatchString(tx) {
		return fmt.Errorf("update: %q is not a transaction id", tx)
	}
	ctx, cancel := context.WithTimeout(ctx, RecoverBudget)
	defer cancel()
	var (
		l   *Lock
		err error
	)
	if mode == RecoverStart {
		// The agent restarting during an update must not stop the runner
		// that restarted it.
		l, err = u.lock(ctx)
	} else {
		l, err = u.recoveryLock(ctx)
	}
	if err != nil {
		return err
	}
	defer u.unlock(l)

	var errs []error
	all, err := ListRecords(u.cfg.Paths)
	if err != nil {
		return err
	}
	for _, r := range all {
		if err := u.recoverPreSwap(ctx, r); err != nil {
			errs = append(errs, err)
		}
	}
	if mode != RecoverStart {
		for _, r := range all {
			if r.State != core.UpdateSwapped && r.State != core.UpdateRollingBack {
				continue
			}
			if mode == RecoverDeadline && r.ID != tx {
				continue
			}
			if err := u.rollBackLocked(ctx, r.ID, mode == RecoverBoot, recoverCause(mode)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if all, err = ListRecords(u.cfg.Paths); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := u.continueSteps(ctx, all); err != nil {
		errs = append(errs, err)
	}
	if all, err = ListRecords(u.cfg.Paths); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := u.sweep(ctx, all); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// recoverCause is the error of a record a recover pass rolls back.
func recoverCause(mode RecoverMode) *core.ErrorDetail {
	switch mode {
	case RecoverBoot:
		return interruptedBy("the host restarted before the update was committed")
	case RecoverDeadline:
		return interruptedBy("the services did not report healthy before the deadline")
	default:
		return interruptedBy("a service or the runner failed before the update was committed")
	}
}

// recoverPreSwap fails a record before its swap whose runner unit is gone:
// pending, downloading or staged with the unit inactive, failed or unknown
// to systemd. Its staged binary is deleted and its reservation released.
func (u *Updater) recoverPreSwap(ctx context.Context, r Record) error {
	switch r.State {
	case core.UpdatePending, core.UpdateDownloading, core.UpdateStaged:
	default:
		return nil
	}
	unit := runnerUnitOf(r)
	st, err := u.cfg.Systemd.State(ctx, unit)
	if err != nil {
		return fmt.Errorf("update: read %s: %w", unit, err)
	}
	if runnerAlive(st) {
		return nil
	}
	u.cfg.Log.Warn("update: the runner stopped before the swap", "transaction", r.ID, "unit", unit, "state", st.ActiveState)
	return u.failPreSwap(ctx, &r, interruptedBy("the runner stopped before the swap"))
}

// runnerAlive reports whether a runner unit is still running or starting.
// A unit that is stopping counts as alive: it may still hold files, and the
// next recover pass sees it gone.
func runnerAlive(st platform.UnitState) bool {
	if st.LoadState == "not-found" {
		return false
	}
	switch st.ActiveState {
	case "active", "activating", "reloading", "deactivating":
		return true
	}
	return false
}

// continueSteps starts the next step of every unfinished operation whose
// last record is a committed intermediate step: a crash between that commit
// and the next step's record leaves the operation holding its reservation
// with nothing running. The caller holds the update lock.
func (u *Updater) continueSteps(ctx context.Context, all []Record) error {
	var errs []error
	for _, op := range operations(all) {
		if Finished(op) {
			continue
		}
		last := op[len(op)-1]
		if last.Kind != core.UpdateKindUpdate || last.State != core.UpdateCommitted || last.ToVersion == last.TargetVersion {
			continue
		}
		u.cfg.Log.Info("update: continuing a stepped update", "operation", last.OperationID, "after", last.ToVersion)
		if err := u.nextStep(ctx, last); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// operations groups all, ordered as ListRecords orders it, by operation, in
// the order the operations started.
func operations(all []Record) [][]Record {
	index := map[string]int{}
	var out [][]Record
	for _, r := range all {
		i, ok := index[r.OperationID]
		if !ok {
			i = len(out)
			index[r.OperationID] = i
			out = append(out, nil)
		}
		out[i] = append(out[i], r)
	}
	return out
}

// sweep releases the reservation of every operation that has no record or
// is finished. Reservation owners start "op:", which no change id does, so a
// change's locks are never touched. The caller holds the update lock, under
// which every reservation gets its record.
func (u *Updater) sweep(ctx context.Context, all []Record) error {
	rows, err := u.cfg.DB.Read().QueryContext(ctx, "SELECT DISTINCT owner FROM core_locks WHERE owner LIKE 'op:%'")
	if err != nil {
		return fmt.Errorf("update: read the reservations: %w", err)
	}
	var owners []string
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			_ = rows.Close()
			return fmt.Errorf("update: read the reservations: %w", err)
		}
		owners = append(owners, owner)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("update: read the reservations: %w", err)
	}
	var errs []error
	for _, owner := range owners {
		op := strings.TrimPrefix(owner, lifecycle.ReservationOwner(""))
		if recs := OperationRecords(all, op); len(recs) > 0 && !Finished(recs) {
			continue
		}
		u.cfg.Log.Info("update: releasing the reservation of a finished operation", "operation", op)
		if err := lifecycle.Release(ctx, u.cfg.DB, owner); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
```

- [ ] **Step 4: Run the tests and the checks**

Run: `GOTOOLCHAIN=local go test -race -count=1 -timeout 300s ./internal/update/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/update`

Run: `GOTOOLCHAIN=local go vet ./internal/update/ && golangci-lint run --allow-serial-runners ./internal/update/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from vet and tidy, `0 issues.` from golangci-lint

- [ ] **Step 5: Commit**

```bash
git add internal/update/recover.go internal/update/recover_test.go
git commit -m "feat(update): recover interrupted updates"
```

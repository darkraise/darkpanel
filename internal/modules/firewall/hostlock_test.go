package firewall

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// w10flBlocker is the runner of a fake host that can hold "ufw reload"
// after the files were written and before UFW reloaded. While it holds the
// reload, torn is true: the files describe a state the loaded rules do not
// yet match.
type w10flBlocker struct {
	core.Runner
	armed   atomic.Bool
	torn    atomic.Bool
	reached chan struct{}
	release chan struct{}
}

func newW10flBlocker(inner core.Runner) *w10flBlocker {
	return &w10flBlocker{Runner: inner, reached: make(chan struct{}), release: make(chan struct{})}
}

// Run holds the next "ufw reload" once armed, until release is closed, and
// lets everything else through.
func (b *w10flBlocker) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if cmd.Path != ufwPath || strings.Join(cmd.Args, " ") != "reload" || !b.armed.CompareAndSwap(true, false) {
		return b.Runner.Run(ctx, cmd)
	}
	b.torn.Store(true)
	b.reached <- struct{}{}
	<-b.release
	res, err := b.Runner.Run(ctx, cmd)
	b.torn.Store(false)
	return res, err
}

// w10flQueue queues a writer on the host-state lock from inside a path that
// holds it. Armed, the first loaded-state read of a path starts a writer
// and returns once the writer waits. A path that then takes the read lock a
// second time blocks behind that writer, which waits for the path's first
// read lock: a deadlock a lone reader hides. A path that holds the write
// lock only makes the writer wait for its end.
type w10flQueue struct {
	mu    *sync.RWMutex
	armed atomic.Bool
	wg    sync.WaitGroup
}

func (q *w10flQueue) hook(t *testing.T) {
	if !q.armed.CompareAndSwap(true, false) {
		return
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		q.mu.Lock()
		q.mu.Unlock() //nolint:staticcheck // SA2001: the writer only has to queue and pass
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
		if !q.mu.TryRLock() {
			return
		}
		q.mu.RUnlock()
	}
	t.Error("the queued writer never waited for the lock")
}

// step runs fn with the queue armed and fails when it takes over 10 s.
func (q *w10flQueue) step(t *testing.T, name string, fn func() error) {
	t.Helper()
	q.armed.Store(true)
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("%s deadlocked: the path takes the host-state lock twice", name)
	}
	q.wg.Wait()
}

// w10flTapReads makes every loaded-state read of f call on first.
func w10flTapReads(f *fw, on func()) {
	read := f.d.readLoaded
	f.d.readLoaded = func(ctx context.Context, env core.Env) (fwstate.Loaded, error) {
		on()
		return read(ctx, env)
	}
}

// w10flRig is a fake host with a UFW whose reload can be held, a firewall
// with its own host-state lock over it, and a Docker daemon. compare
// reports a difference while a reload is held, as the loaded rules lag the
// files then.
type w10flRig struct {
	h        *fakeHost
	f        fw
	block    *w10flBlocker
	watching atomic.Bool
	reads    chan struct{}
}

func newW10flRig(t *testing.T) *w10flRig {
	t.Helper()
	h, f, _ := pnHost(t)
	h.ufw["logging high"] = func(s *fhState) int { s.LogLevel = "high"; return 0 }
	r := &w10flRig{h: h, block: newW10flBlocker(h), reads: make(chan struct{}, 64)}
	f.d = dstDeps(h, newFakeDocker())
	f.d.compare = func(_, _ fwstate.Loaded, _ func(fwstate.Family, string, string) bool) []fwstate.ChainDiff {
		if !r.block.torn.Load() {
			return nil
		}
		return []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyA: []string{"-j ACCEPT"}}}
	}
	f.mu = new(sync.RWMutex)
	w10flTapReads(&f, func() {
		if r.watching.Load() {
			select {
			case r.reads <- struct{}{}:
			default:
			}
		}
	})
	r.f = f
	return r
}

// env is the Env of change c1 whose programs go through the blocker.
func (r *w10flRig) env() core.Env {
	env := r.h.env()
	env.Exec = r.block
	return env
}

// w10flLogging is a change of UFW's logging level.
func w10flLogging(fwstate.Config, fwstate.Loaded) (change, error) {
	op, err := loggingOp("high")
	return change{ops: []ufwOp{op}}, err
}

// w10flResource finds a resource of the firewall module built over f.
func w10flResource(t *testing.T, f fw, name string) core.ResourceDef {
	t.Helper()
	for _, rd := range (module{f: f}).Manifest().Resources {
		if rd.Name == name {
			return rd
		}
	}
	t.Fatalf("the manifest has no resource %s", name)
	return core.ResourceDef{}
}

// w10flStatusRead reads firewall.status through rd and fails when the read
// saw a torn state.
func w10flStatusRead(ctx context.Context, rd core.ResourceDef, env core.Env) error {
	raw, err := rd.Read(ctx, env)
	if err != nil {
		return err
	}
	var st Status
	if err := json.Unmarshal(raw, &st); err != nil {
		return err
	}
	if !st.InSync || len(st.OutOfSync) > 0 || st.SyncError != "" {
		return fmt.Errorf("status read a torn state: inSync %v, out of sync %+v, sync error %q", st.InSync, st.OutOfSync, st.SyncError)
	}
	return nil
}

// TestW10FlReadsWaitForTheReload: a status read or a plan that starts while
// a mutation is between its file write and its ufw reload, whether the
// mutation is an apply or a revert, does not read until the reload is done,
// and then sees the host in sync.
func TestW10FlReadsWaitForTheReload(t *testing.T) {
	for _, mutating := range []string{"apply", "revert"} {
		for _, reader := range []string{"status", "plan"} {
			t.Run(mutating+" then "+reader, func(t *testing.T) {
				ctx := t.Context()
				r := newW10flRig(t)
				env := r.env()
				m := hkMutation(r.f, nil)
				snap, err := m.Snapshot(ctx, env, hkInput{})
				if err != nil {
					t.Fatal(err)
				}
				run := func() error { _, err := m.Apply(ctx, env, hkInput{}); return err }
				if mutating == "revert" {
					if err := run(); err != nil {
						t.Fatal(err)
					}
					run = func() error { _, err := m.Revert(ctx, env, snap); return err }
				}
				r.block.armed.Store(true)
				mutated := make(chan error, 1)
				go func() { mutated <- run() }()
				select {
				case <-r.block.reached:
				case err := <-mutated:
					t.Fatalf("the %s ended without a reload: %v", mutating, err)
				case <-time.After(10 * time.Second):
					t.Fatalf("the %s never reached its reload", mutating)
				}

				readEnv := r.h.env()
				readEnv.ChangeID = ""
				read := func() error {
					if reader == "status" {
						return w10flStatusRead(ctx, w10flResource(t, r.f, "firewall.status"), readEnv)
					}
					out, err := hkMutation(r.f, w10flLogging).Plan(ctx, readEnv, hkInput{})
					if err != nil {
						return err
					}
					if len(out.Guards) != 1 || !out.Guards[0].OK {
						return fmt.Errorf("plan read a torn state: guards %+v", out.Guards)
					}
					return nil
				}
				r.watching.Store(true)
				readDone := make(chan error, 1)
				go func() { readDone <- read() }()
				select {
				case <-r.reads:
					t.Errorf("the %s read the host between the %s's file write and its reload", reader, mutating)
				case <-time.After(300 * time.Millisecond):
				}
				close(r.block.release)
				for name, ch := range map[string]chan error{mutating: mutated, reader: readDone} {
					select {
					case err := <-ch:
						if err != nil {
							t.Errorf("%s: %v", name, err)
						}
					case <-time.After(10 * time.Second):
						t.Fatalf("%s did not finish after the reload", name)
					}
				}
			})
		}
	}
}

// TestW10FlNoPathDeadlocks: every entry point that takes the host-state
// lock runs to its end while a writer queues for the lock, so none takes
// the lock twice; a reconcile pass holds no lock when it applies a change
// through the engine.
func TestW10FlNoPathDeadlocks(t *testing.T) {
	t.Run("a mutation's calls and the reads", func(t *testing.T) {
		ctx := t.Context()
		r := newW10flRig(t)
		q := &w10flQueue{mu: r.f.mu}
		w10flTapReads(&r.f, func() { q.hook(t) })
		env := r.h.env()
		planEnv := env
		planEnv.ChangeID = ""
		m := hkMutation(r.f, nil)
		var snap []byte
		var out ChangeOutput
		q.step(t, "Plan", func() error { _, err := m.Plan(ctx, planEnv, hkInput{}); return err })
		q.step(t, "Snapshot", func() (err error) { snap, err = m.Snapshot(ctx, env, hkInput{}); return })
		q.step(t, "Apply", func() (err error) { out, err = m.Apply(ctx, env, hkInput{}); return })
		q.step(t, "Verify", func() error { return m.Verify(ctx, env, hkInput{}, out) })
		q.step(t, "Observe", func() error { _, err := m.Observe(ctx, env, r.h.journal.effects[0]); return err })
		for _, name := range []string{"firewall.status", "firewall.docker", "firewall.exposure"} {
			rd := w10flResource(t, r.f, name)
			q.step(t, name, func() error { _, err := rd.Read(ctx, planEnv); return err })
		}
		for _, def := range (module{f: r.f}).Manifest().Actions {
			if def.Spec.Name != "firewall.snapshot.compare" {
				continue
			}
			raw := json.RawMessage(`{"from":"` + string(snap) + `"}`)
			q.step(t, def.Spec.Name, func() error { _, err := def.Handler.Read(ctx, planEnv, raw); return err })
		}
		q.step(t, "Revert", func() error { _, err := m.Revert(ctx, env, snap); return err })
	})

	t.Run("reconcile", func(t *testing.T) {
		h, _, f := rcHost(t, true)
		v := &dfVariants{t: t, filter: map[string]string{"nft": dfFilterDocker}, raw: map[string]string{"nft": dfRawDocker}}
		h.onReadVariant = func(fam fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
			return v.read(t.Context(), core.Env{}, fam, variant, tables)
		}
		h.onInterfaces = func() ([]netstat.Interface, error) { return dfInterfaces, nil }
		f.mu = new(sync.RWMutex)
		q := &w10flQueue{mu: f.mu}
		w10flTapReads(&f, func() { q.hook(t) })
		var calls []rcApplied
		tc := rcTask(h, &calls, nil)
		apply := tc.Apply
		var held atomic.Bool
		tc.Apply = func(ctx context.Context, action string, input any) (core.ChangeView, error) {
			q.wg.Wait()
			if !f.mu.TryLock() {
				held.Store(true)
			} else {
				f.mu.Unlock()
			}
			return apply(ctx, action, input)
		}
		q.step(t, "reconcile", func() error { return f.reconcile(t.Context(), tc) })
		if len(calls) != 1 {
			t.Fatalf("the pass applied %v, want one change so that the lock is checked at the apply", calls)
		}
		if held.Load() {
			t.Error("the pass holds the host-state lock while it applies a change")
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		_, m, env, _ := dunHost(t)
		m.f.mu = new(sync.RWMutex)
		q := &w10flQueue{mu: m.f.mu}
		w10flTapReads(&m.f, func() { q.hook(t) })
		q.step(t, "Uninstall", func() error { return m.Uninstall(t.Context(), env, false, map[string]string{"rules": "keep"}) })
	})
}

// w10flSites lists, by enclosing function, where the package's own files
// call lockRead and lockWrite.
func w10flSites(t *testing.T) (reads, writes []string) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			name := fd.Name.Name
			if fd.Recv != nil {
				name = w10flRecvName(fd.Recv.List[0].Type) + "." + name
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch sel.Sel.Name {
					case "lockRead":
						reads = append(reads, name)
					case "lockWrite":
						writes = append(writes, name)
					}
				}
				return true
			})
		}
	}
	slices.Sort(reads)
	slices.Sort(writes)
	return reads, writes
}

func w10flRecvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return w10flRecvName(x.X)
	case *ast.IndexExpr:
		return w10flRecvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return fmt.Sprintf("%T", e)
}

// TestW10FlLockSites: the host-state lock is taken at the exported entry
// points and nowhere else, so that no helper a locked path calls takes it
// again. The write lock covers the paths that change UFW's files, the
// loaded rules or the Docker block; the read lock covers every path that
// reads them.
func TestW10FlLockSites(t *testing.T) {
	reads, writes := w10flSites(t)
	wantWrites := []string{"fw.reconcile", "module.Uninstall", "mutation.Apply", "mutation.Revert"}
	wantReads := []string{"fw.reconcileRead", "fw.snapshotCompare", "mutation.Observe", "mutation.Plan", "mutation.Snapshot", "mutation.Verify", "readLocked"}
	if !slices.Equal(writes, wantWrites) {
		t.Errorf("the write lock is taken in %q, want exactly %q", writes, wantWrites)
	}
	if !slices.Equal(reads, wantReads) {
		t.Errorf("the read lock is taken in %q, want exactly %q", reads, wantReads)
	}
}

// TestW10FlOneLockPerModule: every module instance has its own lock, and a
// firewall built without Module has none.
func TestW10FlOneLockPerModule(t *testing.T) {
	a, b := Module().(module), Module().(module)
	if a.f.mu == nil || b.f.mu == nil || a.f.mu == b.f.mu {
		t.Errorf("module locks %p and %p, want two distinct locks", a.f.mu, b.f.mu)
	}
	if productionFW().mu == nil {
		t.Error("productionFW has no lock")
	}
}

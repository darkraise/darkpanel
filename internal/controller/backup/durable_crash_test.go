package backup

import (
	"errors"
	"io/fs"
	"sync"
)

var errCrash = errors.New("backup test: simulated crash")

// crashOps counts every call of every Ops method and of every File method of a file it created
// (1-based). Call number at fails with errCrash without running when before is true, or runs and
// then fails with errCrash when before is false; every later call fails with errCrash without running.
type crashOps struct {
	inner  Ops
	at     int
	before bool

	mu      sync.Mutex
	calls   int
	crashed bool
	files   []File
}

var _ Ops = (*crashOps)(nil)

func newCrashOps(inner Ops, at int, before bool) *crashOps {
	return &crashOps{inner: inner, at: at, before: before}
}

// Calls returns the calls counted so far.
func (c *crashOps) Calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// Crashed reports whether the crash happened.
func (c *crashOps) Crashed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.crashed
}

// duCall passes one call through the gate; run makes the call on the inner Ops or file. The mutex
// is held while run runs so the crash and the closing of the files are one step to every caller.
func (c *crashOps) duCall(run func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crashed {
		return errCrash
	}
	c.calls++
	if c.calls != c.at {
		return run()
	}
	c.crashed = true
	if !c.before {
		_ = run()
	}
	// The operating system closes a dead process's descriptors: what was written stays, nothing is
	// rolled back.
	for _, f := range c.files {
		_ = f.Close()
	}
	c.files = nil
	return errCrash
}

func (c *crashOps) Create(path string, perm fs.FileMode) (File, error) {
	var inner File
	err := c.duCall(func() error {
		f, err := c.inner.Create(path, perm)
		if err == nil {
			inner = f
			c.files = append(c.files, f)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &duCrashFile{ops: c, inner: inner}, nil
}

func (c *crashOps) Rename(from, to string) error {
	return c.duCall(func() error { return c.inner.Rename(from, to) })
}

func (c *crashOps) Remove(path string) error {
	return c.duCall(func() error { return c.inner.Remove(path) })
}

func (c *crashOps) SyncDir(dir string) error {
	return c.duCall(func() error { return c.inner.SyncDir(dir) })
}

func (c *crashOps) Secure(path string, uid, gid int, perm fs.FileMode) error {
	return c.duCall(func() error { return c.inner.Secure(path, uid, gid, perm) })
}

func (c *crashOps) Exists(path string) (bool, error) {
	var ok bool
	err := c.duCall(func() error {
		var err error
		ok, err = c.inner.Exists(path)
		return err
	})
	if errors.Is(err, errCrash) {
		return false, err
	}
	return ok, err
}

// duCrashFile is a File of crashOps.Create: its calls pass the gate of the crashOps that made it.
type duCrashFile struct {
	ops   *crashOps
	inner File
}

func (f *duCrashFile) Write(p []byte) (int, error) {
	var n int
	err := f.ops.duCall(func() error {
		var err error
		n, err = f.inner.Write(p)
		return err
	})
	return n, err
}

func (f *duCrashFile) Sync() error {
	return f.ops.duCall(f.inner.Sync)
}

func (f *duCrashFile) Close() error {
	return f.ops.duCall(f.inner.Close)
}

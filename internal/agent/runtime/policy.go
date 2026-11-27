package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// PolicyPath is the host-local policy file (spec 1.3): only a root command
// on the host writes it, so the controller cannot turn on what it denies.
const PolicyPath = "/etc/darkpanel/agent-policy.json"

// PolicyReason is the reason of a module the policy file denies.
const PolicyReason = "Turned off on this host by its policy file"

// PolicyModules are the only modules the policy file may deny.
var PolicyModules = []string{"console", "jobs"}

// maxPolicySize bounds the policy file; a valid one is far smaller.
const maxPolicySize = 64 << 10

// PolicyFile is the policy file's content.
type PolicyFile struct {
	DenyModules []string `json:"denyModules"`
}

// Policy is the host-local policy: the modules this host turns off whatever
// the controller pushes. The zero value denies nothing.
type Policy struct {
	deny []string // sorted, each one of PolicyModules
}

// Denied reports whether the policy turns module off.
func (p Policy) Denied(module string) bool { return slices.Contains(p.deny, module) }

// Modules are the denied modules, sorted.
func (p Policy) Modules() []string { return slices.Clone(p.deny) }

func (p Policy) String() string { return "deny [" + strings.Join(p.deny, " ") + "]" }

// denyAll is the policy of a file that exists and is invalid when there is
// no earlier policy to keep: it fails closed.
func denyAll() Policy { return Policy{deny: slices.Clone(PolicyModules)} }

// NewPolicy is the policy denying modules, which must each be one of
// PolicyModules.
func NewPolicy(modules []string) (Policy, error) {
	var deny []string
	for _, m := range modules {
		if !slices.Contains(PolicyModules, m) {
			return Policy{}, fmt.Errorf("the policy file may deny only %s, not %q", strings.Join(PolicyModules, " and "), m)
		}
		if !slices.Contains(deny, m) {
			deny = append(deny, m)
		}
	}
	slices.Sort(deny)
	return Policy{deny: deny}, nil
}

// ReadPolicy reads the policy file at path, which must be a regular file
// owned by owner (root in production) that neither its group nor others can
// write, in a directory held to the same rules, holding exactly one
// PolicyFile object. An absent file denies nothing.
func ReadPolicy(path string, owner uint32) (Policy, error) {
	// Whoever can write the directory can remove the file, and an absent
	// file denies nothing, so the directory is held to the file's rules.
	dir := filepath.Dir(path)
	var dst unix.Stat_t
	err := unix.Stat(dir, &dst)
	switch {
	case errors.Is(err, unix.ENOENT):
		return Policy{}, nil
	case err != nil:
		return Policy{}, fmt.Errorf("read the policy file's directory: %w", err)
	}
	switch {
	case dst.Uid != owner:
		return Policy{}, fmt.Errorf("the policy file's directory %s is owned by uid %d, not root", dir, dst.Uid)
	case dst.Mode&0o022 != 0:
		return Policy{}, fmt.Errorf("the policy file's directory %s is writable by its group or others (mode %04o)", dir, dst.Mode&0o7777)
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the configured policy file
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return Policy{}, nil
	case errors.Is(err, syscall.ELOOP):
		return Policy{}, fmt.Errorf("the policy file %s is a symbolic link", path)
	case err != nil:
		return Policy{}, fmt.Errorf("read the policy file: %w", err)
	}
	defer func() { _ = f.Close() }()
	// The checks look at the open file, so a rename between them and the
	// read cannot swap it.
	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil { //nolint:gosec // G115: a file descriptor fits in an int
		return Policy{}, fmt.Errorf("read the policy file: %w", err)
	}
	switch {
	case st.Mode&unix.S_IFMT != unix.S_IFREG:
		return Policy{}, fmt.Errorf("the policy file %s is not a regular file", path)
	case st.Uid != owner:
		return Policy{}, fmt.Errorf("the policy file %s is owned by uid %d, not root", path, st.Uid)
	case st.Mode&0o022 != 0:
		return Policy{}, fmt.Errorf("the policy file %s is writable by its group or others (mode %04o)", path, st.Mode&0o7777)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxPolicySize+1))
	if err != nil {
		return Policy{}, fmt.Errorf("read the policy file: %w", err)
	}
	if len(data) > maxPolicySize {
		return Policy{}, fmt.Errorf("the policy file %s is larger than %d bytes", path, maxPolicySize)
	}
	pf, err := ParsePolicy(data)
	if err != nil {
		return Policy{}, fmt.Errorf("the policy file %s: %w", path, err)
	}
	return NewPolicy(pf.DenyModules)
}

// ParsePolicy decodes the policy file's content: one object with no member
// but denyModules.
func ParsePolicy(data []byte) (PolicyFile, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var pf PolicyFile
	if err := dec.Decode(&pf); err != nil {
		return PolicyFile{}, fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return PolicyFile{}, errors.New("invalid JSON: data after the object")
	}
	return pf, nil
}

// Policy is the policy in force.
func (r *Runtime) Policy() Policy {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.policy
}

// loadPolicy reads the policy file as the agent starts. A file that exists
// and is invalid denies every module the file may name: the agent fails
// closed rather than run what the host's root may have meant to turn off.
func (r *Runtime) loadPolicy() {
	if r.cfg.PolicyFile == "" {
		return
	}
	p, err := ReadPolicy(r.cfg.PolicyFile, r.cfg.PolicyOwner)
	if err != nil {
		r.cfg.Log.Error("runtime: the policy file is invalid; denying "+strings.Join(PolicyModules, " and "), "error", err)
		p = denyAll()
	}
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.policy = p
}

// ReloadPolicy reads the policy file again, as the agent socket's policy
// command asks. An invalid file is refused and the policy in force stays.
// Each module whose denial changed takes the path a flag change takes: its
// availability is checked again, its EnablementHook is called with its
// stored flag unless it is now denied, and its background tasks follow.
func (r *Runtime) ReloadPolicy(ctx context.Context) error {
	if r.cfg.PolicyFile == "" {
		return errcode.New(errcode.InvalidInput, "this agent reads no policy file")
	}
	// The file is read under enableMu, so of two overlapping reloads the
	// later one applies what the file held last.
	r.enableMu.Lock()
	defer r.enableMu.Unlock()
	p, err := ReadPolicy(r.cfg.PolicyFile, r.cfg.PolicyOwner)
	if err != nil {
		return errcode.New(errcode.InvalidInput, err.Error()+"; the policy in force stays")
	}
	r.statusMu.Lock()
	old := r.policy
	r.policy = p
	r.statusMu.Unlock()
	r.cfg.Log.Info("runtime: policy file read", "policy", p.String())
	// The policy is in force now, so a failure on one module must not keep
	// the others from following it.
	var errs []error
	for _, m := range r.modules() {
		if old.Denied(m.ID) == p.Denied(m.ID) {
			continue
		}
		r.setStatus(r.check(ctx, m))
		enabled, err := r.moduleEnabled(ctx, m.ID)
		if err != nil {
			// callHook passes false for a denied module whatever the flag.
			r.cfg.Log.Warn("runtime: read the enabled flag after a policy reload", "module", m.ID, "error", err)
			errs = append(errs, err)
		}
		r.callHook(ctx, m.ID, enabled)
		if p.Denied(m.ID) {
			r.CloseModuleChannels(m.ID)
		}
		if err := r.syncTasks(ctx, m.ID); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("runtime: the policy is in force, but: %w", err)
	}
	return nil
}

// deniedError is the refusal of an enable the policy file denies.
func deniedError(moduleID string) error {
	return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: %s", moduleID, PolicyReason))
}

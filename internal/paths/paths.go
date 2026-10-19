// Package paths resolves every file and directory the controller and agent
// halves use (spec section 1). Components take a Paths value, so tests pass
// temporary paths.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Paths are the locations of one installation.
type Paths struct {
	ControllerDir    string // controller.db and backups/
	AgentDir         string // agent.db and backups/
	ControllerKey    string
	AgentKey         string
	ControllerSocket string
	AgentSocket      string // the agent's break-glass socket
	AgentTLSDir      string // the agent's credential generations
	UpdateDir        string // update transaction records and the update lock
	BinaryDir        string // the installed binary and its update siblings
	BinaryLink       string // the symlink on PATH to the installed binary
}

// ControllerDB is the controller database file.
func (p Paths) ControllerDB() string { return filepath.Join(p.ControllerDir, "controller.db") }

// AgentDB is the agent database file.
func (p Paths) AgentDB() string { return filepath.Join(p.AgentDir, "agent.db") }

// ControllerBackups is where migrations back up the controller database.
func (p Paths) ControllerBackups() string { return filepath.Join(p.ControllerDir, "backups") }

// AgentBackups is where migrations back up the agent database.
func (p Paths) AgentBackups() string { return filepath.Join(p.AgentDir, "backups") }

// Binary is the installed binary that the units run.
func (p Paths) Binary() string { return filepath.Join(p.BinaryDir, "darkpanel") }

// PrevBinary is the binary an update replaced, kept for rollback.
func (p Paths) PrevBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.prev") }

// RecoveryBinary is the hard link of PrevBinary that the recovery units run
// while an update is unfinished.
func (p Paths) RecoveryBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.recovery") }

// FailedBinary is where a rollback moves the binary that failed.
func (p Paths) FailedBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.failed") }

// NewBinary is where update transaction tx stages its binary, on the same
// filesystem as Binary so that the swap is a rename.
func (p Paths) NewBinary(tx string) string { return filepath.Join(p.BinaryDir, "darkpanel.new-"+tx) }

// UpdateLock is the file every update and rollback step locks.
func (p Paths) UpdateLock() string { return filepath.Join(p.UpdateDir, "lock") }

// UpdateRecord is the record of update transaction tx.
func (p Paths) UpdateRecord(tx string) string { return filepath.Join(p.UpdateDir, tx+".json") }

// AgentTLSCurrent is the symlink to the agent's current credential generation.
func (p Paths) AgentTLSCurrent() string { return filepath.Join(p.AgentTLSDir, "current") }

// Production is the layout of an installed controller host.
func Production() Paths {
	return Paths{
		ControllerDir:    "/var/lib/darkpanel/controller",
		AgentDir:         "/var/lib/darkpanel/agent",
		ControllerKey:    "/etc/darkpanel/controller.key",
		AgentKey:         "/etc/darkpanel/agent.key",
		ControllerSocket: "/run/darkpanel-controller/controller.sock",
		AgentSocket:      "/run/darkpanel-agent/agent.sock",
		AgentTLSDir:      "/etc/darkpanel/agent",
		UpdateDir:        "/var/lib/darkpanel/update",
		BinaryDir:        "/usr/local/lib/darkpanel",
		BinaryLink:       "/usr/local/bin/darkpanel",
	}
}

// Under is the development layout rooted at root, with the sockets in
// root/run.
func Under(root string) Paths {
	return Paths{
		ControllerDir:    filepath.Join(root, "controller"),
		AgentDir:         filepath.Join(root, "agent"),
		ControllerKey:    filepath.Join(root, "keys", "controller.key"),
		AgentKey:         filepath.Join(root, "keys", "agent.key"),
		ControllerSocket: filepath.Join(root, "run", "controller.sock"),
		AgentSocket:      filepath.Join(root, "run", "agent.sock"),
		AgentTLSDir:      filepath.Join(root, "keys", "agent"),
		UpdateDir:        filepath.Join(root, "update"),
		BinaryDir:        filepath.Join(root, "bin"),
		BinaryLink:       filepath.Join(root, "link", "darkpanel"),
	}
}

// Resolve returns the production layout, or with dev the layout under
// $XDG_STATE_HOME/darkpanel-dev with the sockets in
// $XDG_RUNTIME_DIR/darkpanel-dev when that variable is set.
func Resolve(dev bool) (Paths, error) {
	return resolve(dev, os.Getenv, os.UserHomeDir)
}

func resolve(dev bool, getenv func(string) string, home func() (string, error)) (Paths, error) {
	if !dev {
		return Production(), nil
	}
	// The XDG base directory specification says relative values are invalid
	// and must be ignored.
	state := getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		h, err := home()
		if err != nil {
			return Paths{}, fmt.Errorf("paths: XDG_STATE_HOME is unset and the home directory is unknown: %w", err)
		}
		if !filepath.IsAbs(h) {
			return Paths{}, errors.New("paths: XDG_STATE_HOME is unset and the home directory is not absolute")
		}
		state = filepath.Join(h, ".local", "state")
	}
	p := Under(filepath.Join(state, "darkpanel-dev"))
	if runtime := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtime) {
		p.ControllerSocket = filepath.Join(runtime, "darkpanel-dev", "controller.sock")
		p.AgentSocket = filepath.Join(runtime, "darkpanel-dev", "agent.sock")
	}
	return p, nil
}

// MkdirAll creates every directory of p that is missing, with mode 0700.
// Existing directories keep their mode.
func (p Paths) MkdirAll() error {
	dirs := []string{
		p.ControllerDir, p.AgentDir,
		filepath.Dir(p.ControllerKey), filepath.Dir(p.AgentKey), filepath.Dir(p.ControllerSocket),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("paths: create %s: %w", d, err)
		}
	}
	return nil
}

// MkdirAgent creates the agent's directories of p that are missing, with mode
// 0700: the credential generations, the update records and the socket's
// directory. Existing directories keep their mode. MkdirAll leaves them out
// because the controller runs it as its own unprivileged user.
func (p Paths) MkdirAgent() error {
	for _, d := range []string{p.AgentTLSDir, p.UpdateDir, filepath.Dir(p.AgentSocket)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("paths: create %s: %w", d, err)
		}
	}
	return nil
}

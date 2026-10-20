package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"text/template"

	"github.com/darkraise/darkpanel/internal/update"
)

//go:embed units/*
var unitTemplates embed.FS

// UnitFile is one rendered unit file or drop-in.
type UnitFile struct {
	Path    string
	Content []byte
}

// unitData is what the templates in units/ read.
type unitData struct {
	Binary         string // the installed binary, which the services run
	Recovery       string // the recovery binary, which the rollback units run
	ControllerDir  string // the only directory the controller may write
	AgentListen    []string
	AgentAdvertise string
}

// agentAddress is the character set install.sh allows in the flags it
// passes on; the unit files hold the values unquoted.
var agentAddress = regexp.MustCompile(`^[][A-Za-z0-9.:-]+$`)

// CheckAgentAddress refuses a value of --agent-listen or --agent-advertise
// that is not host:port with a port from 1 to 65535.
func CheckAgentAddress(s string) error {
	host, port, err := net.SplitHostPort(s)
	n, perr := strconv.Atoi(port)
	if !agentAddress.MatchString(s) || err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("install: agent address %q is not host:port", s)
	}
	return nil
}

// Units renders the unit files of role: the agent, rollback and boot
// recovery units, and for the controller role also the controller unit and
// its drop-in holding the agent listener flags. Both flags are for the
// controller role only.
func Units(l Layout, role Role, agentListen []string, agentAdvertise string) ([]UnitFile, error) {
	if role != RoleController && (len(agentListen) > 0 || agentAdvertise != "") {
		return nil, errors.New("install: --agent-listen and --agent-advertise are for the controller role")
	}
	for _, a := range agentListen {
		if err := CheckAgentAddress(a); err != nil {
			return nil, err
		}
	}
	if agentAdvertise != "" {
		if err := CheckAgentAddress(agentAdvertise); err != nil {
			return nil, err
		}
	}
	data := unitData{
		Binary: l.Paths.Binary(), Recovery: l.Paths.RecoveryBinary(), ControllerDir: l.Paths.ControllerDir,
		AgentListen: agentListen, AgentAdvertise: agentAdvertise,
	}
	files := []struct{ template, path string }{
		{update.AgentUnit, l.UnitPath(update.AgentUnit)},
		{update.RollbackUnit, l.UnitPath(update.RollbackUnit)},
		{RecoverUnit, l.UnitPath(RecoverUnit)},
	}
	if role == RoleController {
		files = append(files,
			struct{ template, path string }{update.ControllerUnit, l.UnitPath(update.ControllerUnit)},
			struct{ template, path string }{DropInName, l.DropInPath()},
		)
	}
	out := make([]UnitFile, 0, len(files))
	for _, f := range files {
		content, err := renderUnit(f.template, data)
		if err != nil {
			return nil, err
		}
		out = append(out, UnitFile{Path: f.path, Content: content})
	}
	return out, nil
}

func renderUnit(name string, data unitData) ([]byte, error) {
	tmpl, err := template.ParseFS(unitTemplates, "units/"+name)
	if err != nil {
		return nil, fmt.Errorf("install: unit template %s: %w", name, err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("install: render %s: %w", name, err)
	}
	return b.Bytes(), nil
}

// EnabledUnits are the unit files system install enables for role. The
// rollback unit has no [Install] section: only OnFailure= starts it.
func EnabledUnits(role Role) []string {
	units := []string{update.AgentUnit, RecoverUnit}
	if role == RoleController {
		units = append([]string{update.ControllerUnit}, units...)
	}
	return units
}

// writeIfChanged writes data to path with mode 0644 unless the file already
// holds exactly data, and reports whether it wrote. The new content goes to a
// temporary file in the same directory, synced and renamed over path, so a
// crash leaves the old file or the new one.
func writeIfChanged(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(path) //nolint:gosec // G304: the path comes from the install layout
	switch {
	case err == nil && bytes.Equal(old, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: unit directories are world-readable
		return false, fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err != nil {
		return false, fmt.Errorf("write %s: %w", path, err)
	}
	return true, nil
}

// syncDir makes the entries of dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the path comes from the install layout
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

package firewall

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// dpkgStatus is dpkg's database of installed packages. "ufw version" is not
// on the exec allowlist, so UFW's version comes from here.
const dpkgStatus = "/var/lib/dpkg/status"

// minUFW is the oldest UFW Darkpanel supports: rule comments, which carry
// Darkpanel's tags, need 0.35.
var minUFW = []int{0, 35}

// preflight is the firewall's preflight (spec 2): UFW is installed at
// version 0.35 or later, and the save and restore tools of the iptables
// variant each family uses exist. ip6tables is optional.
func (f fw) preflight() []core.PreflightCheck {
	return []core.PreflightCheck{
		{ID: "ufw", Run: f.checkUFW},
		{ID: "ufw-version", Run: f.checkUFWVersion},
		{ID: "iptables-tools", Run: f.checkTools},
	}
}

// hostPath is p under the module's root.
func (f fw) hostPath(p string) string { return filepath.Join(f.d.root, p) }

// regular fails unless p (under the root) is a regular file.
func (f fw) regular(p string) error {
	fi, err := os.Stat(f.hostPath(p))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return missingFile(p)
	case err != nil:
		return fmt.Errorf("%s: %w", p, err)
	case !fi.Mode().IsRegular():
		return fmt.Errorf("%s is not a regular file", p)
	}
	return nil
}

func (f fw) checkUFW(context.Context, core.Env) error {
	if err := f.regular(ufwPath); err != nil {
		return fmt.Errorf("install UFW: %w", err)
	}
	return nil
}

func (f fw) checkUFWVersion(context.Context, core.Env) error {
	data, err := os.ReadFile(f.hostPath(dpkgStatus))
	if err != nil {
		return fmt.Errorf("cannot read UFW's version from %s: %w", dpkgStatus, err)
	}
	version, ok := installedVersion(data, "ufw")
	if !ok {
		return fmt.Errorf("the ufw package is not installed according to %s", dpkgStatus)
	}
	atLeast, err := upstreamAtLeast(version, minUFW)
	switch {
	case err != nil:
		return fmt.Errorf("cannot read UFW's version %q: %w", version, err)
	case !atLeast:
		return fmt.Errorf("UFW %s is older than 0.35, which Darkpanel needs for rule comments", version)
	}
	return nil
}

// installedVersion returns the Version of package pkg in a dpkg status
// database when its Status says it is installed.
func installedVersion(data []byte, pkg string) (string, bool) {
	for _, para := range bytes.Split(data, []byte("\n\n")) {
		fields := map[string]string{}
		sc := bufio.NewScanner(bytes.NewReader(para))
		sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			if k, v, ok := strings.Cut(line, ":"); ok {
				fields[k] = strings.TrimSpace(v)
			}
		}
		if fields["Package"] == pkg {
			return fields["Version"], strings.HasSuffix(fields["Status"], " installed") && fields["Version"] != ""
		}
	}
	return "", false
}

// upstreamAtLeast reports whether the upstream part of a Debian version
// (without the epoch and the Debian revision) is at least want, comparing
// dot-separated numbers.
func upstreamAtLeast(version string, want []int) (bool, error) {
	v := version
	if _, rest, ok := strings.Cut(v, ":"); ok {
		v = rest
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		v = v[:i]
	}
	if v == "" {
		return false, errors.New("no upstream version")
	}
	var got []int
	for _, part := range strings.Split(v, ".") {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false, fmt.Errorf("%q is not a number", part)
		}
		got = append(got, n)
	}
	for i := 0; i < max(len(got), len(want)); i++ {
		var g, w int
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return g > w, nil
		}
	}
	return true, nil
}

func (f fw) checkTools(ctx context.Context, env core.Env) error {
	for _, tool := range []string{"iptables", "ip6tables"} {
		path := "/usr/sbin/" + tool
		if err := f.regular(path); err != nil {
			var missing missingFile
			if tool == "ip6tables" && errors.As(err, &missing) {
				continue
			}
			return fmt.Errorf("install iptables: %w", err)
		}
		res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: []string{"--version"}})
		switch {
		case err != nil:
			return fmt.Errorf("%s --version: %w", path, err)
		case res.ExitCode != 0:
			return fmt.Errorf("%s --version exited %d", path, res.ExitCode)
		}
		line, _, _ := strings.Cut(strings.TrimSpace(string(res.Stdout)), "\n")
		var variant string
		switch {
		case strings.HasSuffix(line, " (nf_tables)"):
			variant = "nft"
		case strings.HasSuffix(line, " (legacy)"):
			variant = "legacy"
		default:
			return fmt.Errorf("cannot tell the variant of %s from %q", path, line)
		}
		for _, verb := range []string{"save", "restore"} {
			if err := f.regular(path + "-" + variant + "-" + verb); err != nil {
				return fmt.Errorf("install iptables: %w", err)
			}
		}
	}
	return nil
}

// missingFile is regular's error for a path that does not exist.
type missingFile string

func (m missingFile) Error() string { return string(m) + " is missing" }

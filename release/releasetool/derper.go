package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
)

// derperBuilder builds derper version for linux/arch into the file dst.
type derperBuilder func(ctx context.Context, version, arch, dst string) error

// runDerper builds derper for one architecture or for all of artifact.Arches
// into an output directory, writes derper.json when every architecture was
// built, and rewrites an existing checksums.txt so the signature over it
// covers the new files. With -checksums, which the release workflow passes,
// a missing checksums.txt is an error before anything is built. args are the
// words after "derper".
func runDerper(ctx context.Context, args []string, stdout io.Writer, build derperBuilder) error {
	var dpVersion, out string
	arches := artifact.Arches
	needSums := false
	switch {
	case len(args) == 2:
		dpVersion, out = args[0], args[1] //nolint:gosec // G602: the case checks the length
	case len(args) == 3 && args[2] == "-checksums":
		dpVersion, out, needSums = args[0], args[1], true //nolint:gosec // G602: the case checks the length
	case len(args) == 4 && args[2] == "-arch":
		dpVersion, out, arches = args[0], args[1], []string{args[3]} //nolint:gosec // G602: the case checks the length
	default:
		return errors.New(usage)
	}
	if _, err := parseVersion(dpVersion); err != nil {
		return err
	}
	for _, arch := range arches {
		if !slices.Contains(artifact.Arches, arch) {
			return fmt.Errorf("architecture %q is not one of %s", arch, strings.Join(artifact.Arches, ", "))
		}
	}
	if needSums {
		//nolint:gosec // G703: the release directory named on the command line
		if _, err := os.Stat(filepath.Join(out, "checksums.txt")); err != nil {
			return fmt.Errorf("-checksums: the release directory's checksums.txt, which make release writes: %w", err)
		}
	}

	if err := os.MkdirAll(out, 0o755); err != nil { //nolint:gosec // G301: a release directory others read
		return err
	}
	sums := make(map[string]string, len(arches))
	for _, arch := range arches {
		name := artifact.BinaryName(artifact.DerperVersion, arch)
		dst := filepath.Join(out, name)
		if err := build(ctx, artifact.DerperVersion, arch, dst); err != nil {
			return fmt.Errorf("derper %s: %w", arch, err)
		}
		//nolint:gosec // G703: the release directory named on the command line
		info, err := os.Stat(dst)
		if err != nil {
			return fmt.Errorf("derper %s: %w", arch, err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("derper %s: the build wrote an empty %s", arch, dst)
		}
		sum, err := fileSHA256(dst)
		if err != nil {
			return fmt.Errorf("derper %s: %w", arch, err)
		}
		sums[arch] = sum
		if _, err := fmt.Fprintf(stdout, "%s  %s\n", sum, name); err != nil {
			return err
		}
	}

	if len(sums) == len(artifact.Arches) {
		m := artifact.Manifest{DarkpanelVersion: dpVersion, DerperVersion: artifact.DerperVersion, Artifacts: sums}
		b, err := m.Encode()
		if err != nil {
			return err
		}
		//nolint:gosec // G306: a published release asset
		if err := os.WriteFile(filepath.Join(out, artifact.ManifestName), b, 0o644); err != nil {
			return err
		}
	}

	//nolint:gosec // G703: the release directory named on the command line
	if _, err := os.Stat(filepath.Join(out, "checksums.txt")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return rewriteChecksums(out)
}

// rewriteChecksums writes dir/checksums.txt exactly as release/package.sh
// writes it, so a release directory's checksums.txt covers the files added
// after package.sh ran.
func rewriteChecksums(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if e.Type().IsRegular() && n != "checksums.txt" && !strings.HasSuffix(n, ".minisig") {
			names = append(names, n)
		}
	}
	// Byte order, as package.sh's LC_ALL=C sort gives.
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		sum, err := fileSHA256(filepath.Join(dir, n))
		if err != nil {
			return err
		}
		fmt.Fprintf(&b, "%s  %s\n", sum, n)
	}
	//nolint:gosec // G306: a published release asset
	return os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(b.String()), 0o644)
}

// fileSHA256 returns the lower-case hex SHA-256 of the file at path, an empty
// file included, as sha256sum does.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a file of the release directory named on the command line
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// derperBuildPassThrough are the runner's variables the derper build keeps:
// where go, its caches and its temporary files live, and how it reaches the
// network.
var derperBuildPassThrough = []string{
	"PATH", "HOME", "TMPDIR", "GOCACHE", "GOMODCACHE", "GOPATH", "GOROOT",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// derperBuildEnv is the environment of the go commands that build derper for
// linux/arch: the variables of environ named in derperBuildPassThrough, in
// that order, then a fixed Go configuration, so the runner's GOFLAGS,
// GOPROXY, GOSUMDB, GONOSUMDB, GOENV, GOWORK or GOTOOLCHAIN cannot change how
// the derper source is fetched, verified and built. GOTOOLCHAIN is pinned to
// local because without it go falls back to the auto of $GOROOT/go.env and
// may download a toolchain.
func derperBuildEnv(environ []string, arch string) []string {
	var env []string
	for _, name := range derperBuildPassThrough {
		for _, kv := range environ {
			if k, _, ok := strings.Cut(kv, "="); ok && k == name {
				env = append(env, kv)
				break
			}
		}
	}
	return append(env,
		"CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch, "GOFLAGS=",
		"GOPROXY=https://proxy.golang.org", "GOSUMDB=sum.golang.org", "GOENV=off", "GOWORK=off",
		"GOTOOLCHAIN=local")
}

// buildDerper builds tailscale.com/cmd/derper at version in a temporary
// module of its own, so this repository never requires tailscale.com.
func buildDerper(ctx context.Context, version, arch, dst string) error {
	// The commands run in the temporary directory, where a relative dst would
	// name a file that is removed with it.
	dst, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "derperbuild-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	env := derperBuildEnv(os.Environ(), arch)
	for _, argv := range [][]string{
		{"go", "mod", "init", "derperbuild"},
		{"go", "get", "tailscale.com/cmd/derper@" + version},
		{"go", "build", "-trimpath", "-ldflags", "-s -w", "-o", dst, "tailscale.com/cmd/derper"},
	} {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // G204: a fixed go command line
		cmd.Dir = dir
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if err != nil {
			const keep = 2 << 10
			if len(output) > keep {
				output = output[len(output)-keep:]
			}
			return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, output)
		}
	}
	return nil
}

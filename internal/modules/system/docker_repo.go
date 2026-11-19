package system

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// dockerKeyring is Docker's apt repository keyring (download.docker.com/linux/<id>/gpg, dearmored), as written
// to dockerKeyringPath.
//
//go:embed docker_keyring.gpg
var dockerKeyring []byte

// dockerKeyFingerprint pins docker_keyring.gpg to Docker's release key.
const dockerKeyFingerprint = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"

const (
	dockerKeyringPath = "/usr/share/keyrings/docker-archive-keyring.gpg"
	dockerListPath    = "/etc/apt/sources.list.d/docker.list"
	dockerRepoHost    = "download.docker.com"
	sourcesList       = "/etc/apt/sources.list"
	sourcesParts      = "/etc/apt/sources.list.d"
)

// repoFileNoun is the effect noun of a repository file the Docker install writes; the Docker uninstall removes
// only the files recorded under it.
const repoFileNoun = ID + ".file"

func repoFileResource(path string) string { return repoFileNoun + ":" + path }

// repoFile is one repository file: what Darkpanel would write and whether the host already holds exactly that.
type repoFile struct {
	path    string
	data    []byte
	present bool
}

func repoFileDoc(data []byte) core.StateDoc {
	sum := sha256.Sum256(data)
	return core.StateDoc{State: json.RawMessage(`{"sha256":"` + hex.EncodeToString(sum[:]) + `"}`)}
}

// dockerRepoFiles renders the keyring, refused with system.docker_keyring unless it holds the pinned key, and
// the source line for this host.
func (m module) dockerRepoFiles() (keyring, list []byte, err error) {
	keyring = dockerKeyring
	if m.d.keyring != nil {
		keyring = m.d.keyring
	}
	fps, err := platform.KeyringFingerprints(keyring)
	if err != nil || !slices.Contains(fps, dockerKeyFingerprint) {
		return nil, nil, errcode.New(errcode.SystemDockerKeyring,
			"the Docker repository keyring Darkpanel carries does not hold the pinned key "+dockerKeyFingerprint)
	}
	info, err := readOSRelease(m.d.readFile)
	if err != nil {
		return nil, nil, err
	}
	list = fmt.Appendf(nil, "deb [arch=%s signed-by=%s] https://%s/linux/%s %s stable\n",
		m.d.arch, dockerKeyringPath, dockerRepoHost, info.ID, info.Codename)
	return bytes.Clone(keyring), list, nil
}

// dockerRepoStatus compares the two files Darkpanel would write with the host's (absent, or identical and
// present) and refuses with system.docker_repo_conflict when either differs or another source entry already
// names download.docker.com. It writes nothing.
func (m module) dockerRepoStatus() ([]repoFile, error) {
	keyring, list, err := m.dockerRepoFiles()
	if err != nil {
		return nil, err
	}
	files := []repoFile{{path: dockerKeyringPath, data: keyring}, {path: dockerListPath, data: list}}
	for i := range files {
		f := &files[i]
		cur, err := m.d.readFile(f.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("system: read %s: %w", f.path, err)
		case bytes.Equal(cur, f.data):
			f.present = true
		default:
			return nil, errcode.New(errcode.SystemDockerRepoConflict, f.path+" exists and is not what Darkpanel would write; remove it first")
		}
	}
	others, err := m.otherDockerSources()
	if err != nil {
		return nil, err
	}
	if len(others) > 0 {
		return nil, errcode.New(errcode.SystemDockerRepoConflict,
			"another apt source already names "+dockerRepoHost+": "+strings.Join(others, ", ")+"; remove that entry first")
	}
	return files, nil
}

// otherDockerSources lists the source files other than dockerListPath with an uncommented line naming
// download.docker.com: the one-line sources.list and *.list files, and deb822 *.sources files.
func (m module) otherDockerSources() ([]string, error) {
	paths := []string{sourcesList}
	names, err := m.d.listDir(sourcesParts)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("system: list %s: %w", sourcesParts, err)
	}
	for _, n := range names {
		if p := sourcesParts + "/" + n; p != dockerListPath && (strings.HasSuffix(n, ".list") || strings.HasSuffix(n, ".sources")) {
			paths = append(paths, p)
		}
	}
	var found []string
	for _, p := range paths {
		b, err := m.d.readFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("system: read %s: %w", p, err)
		}
		for line := range strings.Lines(string(b)) {
			if l := strings.TrimSpace(line); !strings.HasPrefix(l, "#") && strings.Contains(l, dockerRepoHost) {
				found = append(found, p)
				break
			}
		}
	}
	return found, nil
}

// writeDockerRepo writes the absent files of files, each journaled as a create effect, so the undo removes only
// what this change wrote; written lists them.
func writeDockerRepo(ctx context.Context, env core.Env, files []repoFile) (written []string, err error) {
	for _, f := range files {
		if f.present {
			continue
		}
		doc := repoFileDoc(f.data)
		// The journal names a creation by its recovery identity until it is made.
		seq, err := env.Effects.Intend(ctx, core.Effect{
			Kind: core.EffectCreate, Resource: repoFileNoun + ":?" + f.path,
			Recovery: &core.RecoveryID{Kind: "name", Value: f.path}, Local: true, InverseLocal: true,
			Before: core.StateDoc{State: json.RawMessage("null")}, After: &doc,
		}, nil)
		if err != nil {
			return written, err
		}
		if err := env.Files.WriteFile(ctx, ID, f.path, f.data, 0o644); err != nil {
			return written, fmt.Errorf("system: write %s: %w", f.path, err)
		}
		written = append(written, f.path)
		if err := env.Effects.Made(ctx, seq, repoFileResource(f.path), doc); err != nil {
			return written, err
		}
	}
	return written, nil
}

// repoFilePath is the repository file effect e names, or "" when e names none.
func repoFilePath(e core.Effect) string {
	noun, identity, _ := strings.Cut(e.Resource, ":")
	identity = strings.TrimPrefix(identity, "?")
	if noun == repoFileNoun && (identity == dockerKeyringPath || identity == dockerListPath) {
		return identity
	}
	return ""
}

// observeRepoFile observes the repository file at path.
func (m module) observeRepoFile(path string) (core.Observation, error) {
	b, err := m.d.readFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Matches: 0, Exclusive: true}, nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("system: read %s: %w", path, err)
	}
	return core.Observation{Current: repoFileDoc(b), Resource: repoFileResource(path), Matches: 1, Exclusive: true}, nil
}

// strictRefresh refuses the output of the Docker source's apt-get update when any line reports a failed fetch
// or signature: every "Err:" line, and every "W:" or "E:" line about one. A failed run's own code applies
// otherwise.
func strictRefresh(out []byte, runErr error) error {
	var bad []string
	for line := range strings.Lines(string(out)) {
		l := strings.TrimRight(line, "\n")
		switch {
		case strings.HasPrefix(l, "Err:"):
			bad = append(bad, l)
		case (strings.HasPrefix(l, "W:") || strings.HasPrefix(l, "E:")) && fetchOrSignature(l):
			bad = append(bad, l)
		}
	}
	if len(bad) > 0 {
		_, tail := failureDetail(out)
		return errcode.New(errcode.SystemRepoSignature,
			"Docker's repository did not fetch or verify:\n"+strings.Join(bad, "\n")+"\n\napt's output:\n"+strings.Join(tail, "\n"))
	}
	return runErr
}

func fetchOrSignature(line string) bool {
	if classifyApt([]byte(line)) == errcode.SystemRepoSignature {
		return true
	}
	for _, p := range []string{"Failed to fetch", "Release file", "does not have a Release file", "not valid yet", "expired"} {
		if strings.Contains(line, p) {
			return true
		}
	}
	return false
}

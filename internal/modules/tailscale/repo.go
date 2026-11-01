package tailscale

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // G505: an OpenPGP version 4 fingerprint is defined as a SHA-1
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// repoKeyring is Tailscale's apt repository keyring, as written to keyringPath. It is a variable so a test can
// replace it.
//
//go:embed repo_keyring.gpg
var repoKeyring []byte

// keyringFingerprint pins repo_keyring.gpg to the primary key recorded in spec 4.1 (Keyring).
const keyringFingerprint = "2596A99EAAB33821893C0A79458CA832957F5868"

// repoFileNoun is the effect noun of a repository file package.install writes.
const repoFileNoun = ID + ".file"

// repoFileResource is the effect resource of the repository file at path.
func repoFileResource(path string) string { return repoFileNoun + ":" + path }

// repoFile is one repository file: what Darkpanel would write and whether the host already holds exactly that.
type repoFile struct {
	path    string
	data    []byte
	present bool
}

// repoFileDoc is the state of a repository file holding data.
func repoFileDoc(data []byte) core.StateDoc {
	sum := sha256.Sum256(data)
	return core.StateDoc{State: json.RawMessage(`{"sha256":"` + hex.EncodeToString(sum[:]) + `"}`)}
}

// keyringFingerprints lists the OpenPGP version 4 fingerprints of every public key and public subkey packet of
// the binary keyring b, in order. Keys of another version are skipped.
func keyringFingerprints(b []byte) ([]string, error) {
	var fps []string
	for len(b) > 0 {
		first := b[0]
		if first&0x80 == 0 {
			return nil, errors.New("tailscale: the keyring data is not an OpenPGP packet")
		}
		var tag byte
		var header, length int
		if first&0x40 != 0 {
			tag = first & 0x3f
			if len(b) < 2 {
				return nil, errors.New("tailscale: an OpenPGP packet header runs past the keyring data")
			}
			switch l := b[1]; {
			case l < 192:
				header, length = 2, int(l)
			case l < 224:
				if len(b) < 3 {
					return nil, errors.New("tailscale: an OpenPGP packet header runs past the keyring data")
				}
				header, length = 3, (int(l)-192)<<8+int(b[2])+192
			case l == 255:
				if len(b) < 6 {
					return nil, errors.New("tailscale: an OpenPGP packet header runs past the keyring data")
				}
				header, length = 6, int(binary.BigEndian.Uint32(b[2:6]))
			default:
				return nil, errors.New("tailscale: the keyring holds an OpenPGP packet of partial length")
			}
		} else {
			tag = first >> 2 & 0x0f
			switch first & 0x03 {
			case 0:
				header = 2
			case 1:
				header = 3
			case 2:
				header = 5
			case 3:
				header = 1
			}
			if len(b) < header {
				return nil, errors.New("tailscale: an OpenPGP packet header runs past the keyring data")
			}
			switch header {
			case 2:
				length = int(b[1])
			case 3:
				length = int(binary.BigEndian.Uint16(b[1:3]))
			case 5:
				length = int(binary.BigEndian.Uint32(b[1:5]))
			case 1:
				length = len(b) - 1
			}
		}
		if length > len(b)-header {
			return nil, errors.New("tailscale: an OpenPGP packet runs past the keyring data")
		}
		body := b[header : header+length]
		b = b[header+length:]
		if (tag != 6 && tag != 14) || len(body) == 0 || body[0] != 4 {
			continue
		}
		if len(body) > math.MaxUint16 {
			return nil, errors.New("tailscale: a version 4 key packet is longer than its fingerprint can encode")
		}
		h := sha1.New()                                                         //nolint:gosec // G401: an OpenPGP version 4 fingerprint is defined as a SHA-1
		h.Write(binary.BigEndian.AppendUint16([]byte{0x99}, uint16(len(body)))) //nolint:gosec // G115: the length was checked against math.MaxUint16 above
		h.Write(body)
		fps = append(fps, strings.ToUpper(hex.EncodeToString(h.Sum(nil))))
	}
	return fps, nil
}

// repoFiles renders the keyring (the bytes embedded from repo_keyring.gpg, pinned by keyringFingerprint)
// and the source list line "deb [signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg] https://pkgs.tailscale.com/stable/<ID> <Codename> main\n"
// for this host, or refuses an unsupported OS or architecture.
func (t ts) repoFiles() (keyring, sources []byte, err error) {
	info, err := t.d.osRelease()
	if err != nil {
		return nil, nil, fmt.Errorf("tailscale: read the OS release: %w", err)
	}
	if !slices.Contains(supportedReleases[info.ID], info.Codename) || (t.d.arch != "amd64" && t.d.arch != "arm64") {
		return nil, nil, errcode.New(errcode.TailscaleUnavailable,
			fmt.Sprintf("tailscale does not support %s %s on %s", info.ID, info.Codename, t.d.arch))
	}
	fps, err := keyringFingerprints(repoKeyring)
	if err != nil {
		return nil, nil, fmt.Errorf("tailscale: read the fingerprints of the embedded repository keyring: %w", err)
	}
	if !slices.Contains(fps, keyringFingerprint) {
		return nil, nil, fmt.Errorf("tailscale: the embedded repository keyring does not hold the pinned key fingerprint %s", keyringFingerprint)
	}
	sources = fmt.Appendf(nil, "deb [signed-by=%s] https://pkgs.tailscale.com/stable/%s %s main\n", keyringPath, info.ID, info.Codename)
	return bytes.Clone(repoKeyring), sources, nil
}

// repoStatus compares the repository files Darkpanel would write with the host's: the keyring, then the source
// list. An absent file is not present; an identical one is; one that differs is a definite
// tailscale.repo_conflict.
func (t ts) repoStatus() ([]repoFile, error) {
	keyring, sources, err := t.repoFiles()
	if err != nil {
		return nil, err
	}
	files := []repoFile{{path: keyringPath, data: keyring}, {path: sourcesPath, data: sources}}
	for i := range files {
		f := &files[i]
		cur, err := t.d.readFile(f.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("tailscale: read %s: %w", f.path, err)
		case bytes.Equal(cur, f.data):
			f.present = true
		default:
			return nil, core.Definite(errcode.New(errcode.TailscaleRepoConflict, f.path+" exists and is not what Darkpanel would write"))
		}
	}
	return files, nil
}

// ensureRepo writes the two files when absent (recording them as owned), uses them unrecorded when byte-identical,
// and refuses with tailscale.repo_conflict when either differs; written reports what it wrote. It journals a
// tailscale.file:<path> create effect only when env.Effects is not nil: package.install passes its journal (it
// observes and undoes the files); package.update, package.pin and Join pass an env whose Effects is nil.
func (t ts) ensureRepo(ctx context.Context, env core.Env) (written []string, err error) {
	files, err := t.repoStatus()
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.present {
			continue
		}
		doc := repoFileDoc(f.data)
		var seq int
		if env.Effects != nil {
			// The journal names a creation by its recovery identity until it is made.
			seq, err = env.Effects.Intend(ctx, core.Effect{
				Kind: core.EffectCreate, Resource: repoFileNoun + ":?" + f.path,
				Recovery: &core.RecoveryID{Kind: "name", Value: f.path}, Local: true, InverseLocal: true,
				Before: core.StateDoc{State: json.RawMessage("null")}, After: &doc,
			}, nil)
			if err != nil {
				return written, err
			}
		}
		if err := env.Files.WriteFile(ctx, ID, f.path, f.data, 0o644); err != nil {
			return written, fmt.Errorf("tailscale: write %s: %w", f.path, err)
		}
		written = append(written, f.path)
		if env.Effects != nil {
			if err := env.Effects.Made(ctx, seq, repoFileResource(f.path), doc); err != nil {
				return written, err
			}
		}
	}
	return written, nil
}

// aptInstall runs APT-1 then APT-2 under ctx.
func aptInstall(ctx context.Context, env core.Env) error {
	if _, err := run(ctx, env, "/usr/bin/apt-get", platform.AptUpdateTailscale()...); err != nil {
		return err
	}
	_, err := run(ctx, env, "/usr/bin/apt-get", platform.AptInstallTailscale()...)
	return err
}

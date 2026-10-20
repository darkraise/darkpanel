// Command releasetool holds the release workflow's logic that can be tested
// locally: tag and version checks, the releases.json index, whether a release
// becomes GitHub's Latest, minisign key ids, and the check that a release
// binary is the version it is released as, is not a test build and trusts
// exactly the keys that sign it.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
)

const usage = `usage: releasetool check-tag <tag>
       releasetool check-version <version>
       releasetool index < release-list.json > releases.json
       releasetool latest <version> < release-list.json
       releasetool keyid <file.minisig>
       releasetool check-build <version> <key id>[,<key id>] < version.json`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "releasetool:", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	cmd, rest := args[0], args[1:]
	switch {
	case cmd == "check-tag" && len(rest) == 1:
		v, err := parseTag(rest[0])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "version=%s\nchannel=%s\n", v.text, v.channel())
		return err
	case cmd == "check-version" && len(rest) == 1:
		_, err := parseVersion(rest[0])
		return err
	case cmd == "index" && len(rest) == 0:
		list, err := readList(stdin)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(buildIndex(list))
	case cmd == "latest" && len(rest) == 1:
		v, err := parseVersion(rest[0])
		if err != nil {
			return err
		}
		list, err := readList(stdin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, isLatest(list, v))
		return err
	case cmd == "keyid" && len(rest) == 1:
		sig, err := os.ReadFile(rest[0]) //nolint:gosec // G304: the signature file named on the command line
		if err != nil {
			return err
		}
		id, err := keyID(sig)
		if err != nil {
			return fmt.Errorf("%s: %w", rest[0], err)
		}
		_, err = fmt.Fprintln(stdout, id)
		return err
	case cmd == "check-build" && len(rest) == 2:
		info, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		keys := strings.FieldsFunc(rest[1], func(r rune) bool { return r == ',' })
		return checkBuild(info, rest[0], keys)
	}
	return errors.New(usage)
}

func readList(r io.Reader) ([]ghRelease, error) {
	var list []ghRelease
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("release list: %w", err)
	}
	return list, nil
}

// keyID returns the key id of a minisign signature file as 16 upper-case hex
// digits, the form the published file names use
// (checksums.txt.<key id>.minisig). It requires the hashed algorithm "ED",
// the only one install.sh and the updater accept.
func keyID(sig []byte) (string, error) {
	lines := strings.Split(string(sig), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "untrusted comment: ") {
		return "", errors.New("not a minisign signature file")
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil {
		return "", fmt.Errorf("signature line: %w", err)
	}
	if len(raw) != 74 {
		return "", fmt.Errorf("signature line is %d bytes, want 74", len(raw))
	}
	if string(raw[:2]) != "ED" {
		return "", fmt.Errorf("signature algorithm %q, want the hashed algorithm ED", raw[:2])
	}
	return fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])), nil
}

// buildInfo is the part of `darkpanel version --json` the release checks.
type buildInfo struct {
	Version     string   `json:"version"`
	TestBuild   *bool    `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

// checkBuild refuses a binary built for tests, one built as another version
// than the release it ships in, and one whose trusted keys differ from the
// keys signing the release: hosts running it could not verify the next
// release, or would trust a key the release does not use. A binary of the
// wrong version would be installed as the signed version, and the updater's
// stepping and support checks would rely on a version it does not report.
func checkBuild(versionJSON []byte, version string, signing []string) error {
	var b buildInfo
	if err := json.Unmarshal(versionJSON, &b); err != nil {
		return fmt.Errorf("version --json: %w", err)
	}
	if b.TestBuild == nil {
		return errors.New("version --json has no testBuild field")
	}
	if *b.TestBuild {
		return errors.New("the release binary is a test build")
	}
	if b.Version != version {
		return fmt.Errorf("the release binary reports version %q, but the release is %q", b.Version, version)
	}
	if len(signing) == 0 {
		return errors.New("no signing key ids")
	}
	got := slices.Sorted(slices.Values(b.TrustedKeys))
	want := slices.Sorted(slices.Values(signing))
	if !slices.Equal(got, want) {
		return fmt.Errorf("the binary trusts %v but the release is signed by %v", got, want)
	}
	return nil
}

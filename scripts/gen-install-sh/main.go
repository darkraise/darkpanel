// Command gen-install-sh renders install.sh from install.sh.in, embedding the
// trusted release keys and the release base URL of this build, so the script
// and the updater always trust the same keys. Build it with -tags harnesskeys
// for the harness's install.sh.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const (
	keysPlaceholder = "@DARKPANEL_KEYS@"
	basePlaceholder = "@DARKPANEL_BASE_URL@"
)

// ed25519SPKIPrefix is the DER SubjectPublicKeyInfo header of an Ed25519
// key; followed by the 32 key bytes it is what OpenSSL reads as a public key.
var ed25519SPKIPrefix = []byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00}

// The base URL lands inside single quotes in the script.
var baseURLPattern = regexp.MustCompile(`^https?://[A-Za-z0-9.:/_~-]+$`)

func main() {
	in := flag.String("in", "install.sh.in", "the template to render")
	out := flag.String("out", "install.sh", "the script to write")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-install-sh:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	template, err := os.ReadFile(in) //nolint:gosec // G304: the template path is the caller's argument
	if err != nil {
		return err
	}
	script, err := Generate(template, trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, script, 0o755); err != nil { //nolint:gosec // G306: install.sh is an executable script
		return err
	}
	// WriteFile keeps the mode of an existing file.
	return os.Chmod(out, 0o755) //nolint:gosec // G302: install.sh is an executable script
}

// Generate replaces the template's placeholders with one word per key and
// the base URL. Its output depends only on its arguments, so a regenerated
// install.sh can be compared with the committed one.
func Generate(template []byte, keys []trustedkeys.Key, baseURL string) ([]byte, error) {
	for _, p := range []string{keysPlaceholder, basePlaceholder} {
		if !bytes.Contains(template, []byte(p)) {
			return nil, fmt.Errorf("template has no %s", p)
		}
	}
	if !baseURLPattern.MatchString(baseURL) {
		return nil, fmt.Errorf("base URL %q must match %s", baseURL, baseURLPattern)
	}
	words := make([]string, 0, len(keys))
	for _, k := range keys {
		w, err := keyWord(k)
		if err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	script := bytes.ReplaceAll(template, []byte(keysPlaceholder), []byte(strings.Join(words, " ")))
	return bytes.ReplaceAll(script, []byte(basePlaceholder), []byte(baseURL)), nil
}

// keyWord is "<ID>:<id bytes as hex>:<DER public key, base64>", the form the
// script splits: the id names the signature file, the id bytes are compared
// with the signature's, and the DER key is what OpenSSL verifies with.
func keyWord(k trustedkeys.Key) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
		return "", fmt.Errorf("key %s: %q is not a minisign Ed25519 public key", k.ID, k.PublicKey)
	}
	if id := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])); id != k.ID {
		return "", fmt.Errorf("key %s: its public key carries id %s", k.ID, id)
	}
	der := append(append([]byte{}, ed25519SPKIPrefix...), raw[10:]...)
	return k.ID + ":" + hex.EncodeToString(raw[2:10]) + ":" + base64.StdEncoding.EncodeToString(der), nil
}

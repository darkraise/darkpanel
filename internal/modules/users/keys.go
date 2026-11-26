package users

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// minRSABits is the smallest RSA key a new key line may hold.
const minRSABits = 3072

// defaultKeysFile is sshd's AuthorizedKeysFile as sshd -T prints the
// default; any other value makes the key actions unavailable.
const defaultKeysFile = ".ssh/authorized_keys .ssh/authorized_keys2"

// newKeyTypes are the key types a new key line may have.
var newKeyTypes = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoSKED25519, ssh.KeyAlgoSKECDSA256, ssh.KeyAlgoRSA,
}

// Key is one key line of an authorized_keys file.
type Key struct {
	Type        string `json:"type"`
	Bits        int    `json:"bits"`
	Fingerprint string `json:"fingerprint" jsonschema_description:"SHA256:<base64>, as ssh-keygen -l prints it."`
	Comment     string `json:"comment"`
	Options     string `json:"options" jsonschema_description:"The line's options as text; empty when it has none."`
}

// KeyList is the answer of users.key.list.
type KeyList struct {
	Keys []Key `json:"keys"`
}

// KeyInput is the input of users.key.add.
type KeyInput struct {
	Name string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
	Key  string `json:"key" jsonschema:"maxLength=16384" jsonschema_description:"One public key line without options: ed25519, ecdsa, sk-ed25519, sk-ecdsa or RSA of at least 3072 bits."`
}

// KeyRemoveInput is the input of users.key.remove.
type KeyRemoveInput struct {
	Name        string `json:"name" jsonschema:"pattern=^[a-z_][a-z0-9_-]{0\\,31}$" jsonschema_description:"The account name."`
	Fingerprint string `json:"fingerprint" jsonschema:"pattern=^SHA256:[A-Za-z0-9+/]{43}$" jsonschema_description:"The key's SHA256 fingerprint."`
}

// KeyChanged names the account and the key a change acted on.
type KeyChanged struct {
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
}

func (m module) keyActions() []core.ActionDef {
	spec := func(verb, summary, by string) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + ".key." + verb, Version: 1, Summary: summary, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{lockKey}, MinScope: core.ScopeAdmin, CompensatedBy: ID + ".key." + by, Available: keysAvailable,
		}
	}
	add := spec("add", "Authorize one SSH public key for an account.", "remove")
	add.StepUp = true
	return []core.ActionDef{
		core.DefineRead[NameInput, KeyList](core.ActionSpec{
			Name: ID + ".key.list", Version: 1, Class: core.ClassRead, Side: core.SideAgent, Available: keysAvailable,
			Summary: "List an account's authorized SSH keys: type, bits, fingerprint, comment and options.",
		}, keyLister{m: m}),
		core.DefineMutating[KeyInput, KeyChanged](add, addHandler{keyHandler{m: m, add: true}}),
		core.DefineMutating[KeyRemoveInput, KeyChanged](spec("remove", "Remove an authorized SSH key from an account.", "add"), removeHandler{keyHandler{m: m}}),
	}
}

// keysAvailable makes the key actions unavailable when sshd reads keys from
// another file than the default.
func keysAvailable(ctx context.Context, env core.Env) error {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: platform.SshdPath, Args: platform.SshdEffective()})
	if err != nil {
		return fmt.Errorf("cannot read sshd's settings: %w", err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("sshd -T exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	for line := range lines(res.Stdout) {
		if value, ok := strings.CutPrefix(line, "authorizedkeysfile "); ok && strings.TrimSpace(value) != defaultKeysFile {
			return fmt.Errorf("sshd reads authorized keys from %s, not ~/.ssh/authorized_keys", strings.TrimSpace(value))
		}
	}
	return nil
}

// parseNewKey parses one new key line: no options, a type of newKeyTypes,
// RSA of at least 3072 bits. It returns the line as written to the file.
func parseNewKey(path, line string) (ssh.PublicKey, string, error) {
	line = strings.TrimSpace(line)
	notKey := invalid(path, "Not a public key line")
	if line == "" || strings.ContainsFunc(line, unicode.IsControl) {
		return nil, "", notKey
	}
	pub, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil || len(options) > 0 || len(bytes.TrimSpace(rest)) > 0 || !slices.Contains(newKeyTypes, pub.Type()) ||
		!strings.HasPrefix(line, pub.Type()+" ") {
		return nil, "", notKey
	}
	if bits := keyBits(pub); pub.Type() == ssh.KeyAlgoRSA && bits < minRSABits {
		return nil, "", invalid(path, fmt.Sprintf("RSA keys need at least %d bits", minRSABits))
	}
	return pub, line, nil
}

// keyBits is the size of pub's key, 0 when the type does not say.
func keyBits(pub ssh.PublicKey) int {
	if c, ok := pub.(ssh.CryptoPublicKey); ok {
		if k, ok := c.CryptoPublicKey().(*rsa.PublicKey); ok {
			return k.N.BitLen()
		}
	}
	switch pub.Type() {
	case ssh.KeyAlgoED25519, ssh.KeyAlgoSKED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoSKECDSA256:
		return 256
	case ssh.KeyAlgoECDSA384:
		return 384
	case ssh.KeyAlgoECDSA521:
		return 521
	}
	return 0
}

// keyLine is one parsed line of an authorized_keys file.
type keyLine struct {
	key  Key
	text string // the line with its line end
}

// parseKeys parses an authorized_keys file; lines that hold no key
// (comments, blank lines, unknown text) have a zero key.
func parseKeys(data []byte) []keyLine {
	var out []keyLine
	for line := range bytes.Lines(data) {
		kl := keyLine{text: string(line)}
		pub, comment, options, _, err := ssh.ParseAuthorizedKey(line)
		if err == nil {
			kl.key = Key{Type: pub.Type(), Bits: keyBits(pub), Fingerprint: ssh.FingerprintSHA256(pub), Comment: comment,
				Options: strings.Join(options, ",")}
		}
		out = append(out, kl)
	}
	return out
}

// countKeys is the number of key lines in data.
func countKeys(data []byte) int {
	n := 0
	for _, kl := range parseKeys(data) {
		if kl.key.Fingerprint != "" {
			n++
		}
	}
	return n
}

// readKeys reads name's authorized keys after checking the account.
func (m module) readKeys(ctx context.Context, env core.Env, name string) ([]byte, error) {
	if env.Home == nil {
		return nil, errors.New("users: no home file provider")
	}
	if _, _, err := m.account(name); err != nil {
		return nil, err
	}
	data, err := env.Home.ReadAuthorizedKeys(ctx, name)
	if err != nil {
		return nil, core.Definite(errcode.New(errcode.InvalidInput, err.Error()))
	}
	return data, nil
}

type keyLister struct{ m module }

func (l keyLister) Read(ctx context.Context, env core.Env, in NameInput) (KeyList, error) {
	data, err := l.m.readKeys(ctx, env, in.Name)
	if err != nil {
		return KeyList{}, err
	}
	out := KeyList{Keys: []Key{}}
	for _, kl := range parseKeys(data) {
		if kl.key.Fingerprint != "" {
			out.Keys = append(out.Keys, kl.key)
		}
	}
	return out, nil
}

// keyHandler adds (add) a key line or removes the lines of a fingerprint.
type keyHandler struct {
	m   module
	add bool
}

// desired is the file after the change: data with the new line appended,
// or without the lines of the fingerprint.
func (h keyHandler) desired(data []byte, name, line, fingerprint string) ([]byte, error) {
	present := false
	var kept bytes.Buffer
	for _, kl := range parseKeys(data) {
		if kl.key.Fingerprint == fingerprint {
			present = true
			continue
		}
		kept.WriteString(kl.text)
	}
	switch {
	case h.add && present:
		return nil, invalid("/key", "that key is authorized for "+name+" already")
	case !h.add && !present:
		return nil, invalid("/fingerprint", "no key "+fingerprint+" is authorized for "+name)
	case !h.add:
		return kept.Bytes(), nil
	}
	out := slices.Clone(data)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return append(out, line+"\n"...), nil
}

// plan reads the file and returns it with the change's file and the
// fingerprint it acts on.
func (h keyHandler) plan(ctx context.Context, env core.Env, name, key, fingerprint string) ([]byte, []byte, string, error) {
	line := ""
	if h.add {
		pub, l, err := parseNewKey("/key", key)
		if err != nil {
			return nil, nil, "", err
		}
		line, fingerprint = l, ssh.FingerprintSHA256(pub)
	}
	data, err := h.m.readKeys(ctx, env, name)
	if err != nil {
		return nil, nil, "", err
	}
	want, err := h.desired(data, name, line, fingerprint)
	if err != nil {
		return nil, nil, "", err
	}
	if len(want) > platform.MaxAuthorizedKeys {
		return nil, nil, "", invalid("/key", "the authorized keys file would be larger than 1 MiB")
	}
	return data, want, fingerprint, nil
}

func keysHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (h keyHandler) planOutcome(ctx context.Context, env core.Env, name, key, fingerprint string) (core.PlanOutcome, error) {
	data, _, fp, err := h.plan(ctx, env, name, key, fingerprint)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	line := core.DiffLine{Op: "+", Text: "Authorize the key " + fp + " for " + name}
	if !h.add {
		line = core.DiffLine{Op: "-", Text: "Remove the key " + fp + " from " + name}
	}
	return core.PlanOutcome{Diff: []core.DiffLine{line}, StateHash: keysHash(data)}, nil
}

func (h keyHandler) apply(ctx context.Context, env core.Env, name, key, fingerprint string) (KeyChanged, error) {
	_, want, fp, err := h.plan(ctx, env, name, key, fingerprint)
	if err != nil {
		return KeyChanged{}, err
	}
	if err := env.Home.WriteAuthorizedKeys(ctx, name, want); err != nil {
		return KeyChanged{}, err
	}
	return KeyChanged{Name: name, Fingerprint: fp}, nil
}

func (h keyHandler) verify(ctx context.Context, env core.Env, name, fingerprint string) error {
	data, err := h.m.readKeys(ctx, env, name)
	if err != nil {
		return err
	}
	present := slices.ContainsFunc(parseKeys(data), func(kl keyLine) bool { return kl.key.Fingerprint == fingerprint })
	if present != h.add {
		return fmt.Errorf("users: the authorized keys of %s do not show the change of %s", name, fingerprint)
	}
	return nil
}

// addHandler and removeHandler adapt keyHandler to each action's input.
type (
	addHandler    struct{ keyHandler }
	removeHandler struct{ keyHandler }
)

func (h addHandler) Plan(ctx context.Context, env core.Env, in KeyInput) (core.PlanOutcome, error) {
	return h.planOutcome(ctx, env, in.Name, in.Key, "")
}

func (h addHandler) Apply(ctx context.Context, env core.Env, in KeyInput) (KeyChanged, error) {
	return h.apply(ctx, env, in.Name, in.Key, "")
}

func (h addHandler) Verify(ctx context.Context, env core.Env, in KeyInput, _ KeyChanged) error {
	pub, _, err := parseNewKey("/key", in.Key)
	if err != nil {
		return err
	}
	return h.verify(ctx, env, in.Name, ssh.FingerprintSHA256(pub))
}

func (h removeHandler) Plan(ctx context.Context, env core.Env, in KeyRemoveInput) (core.PlanOutcome, error) {
	return h.planOutcome(ctx, env, in.Name, "", in.Fingerprint)
}

func (h removeHandler) Apply(ctx context.Context, env core.Env, in KeyRemoveInput) (KeyChanged, error) {
	return h.apply(ctx, env, in.Name, "", in.Fingerprint)
}

func (h removeHandler) Verify(ctx context.Context, env core.Env, in KeyRemoveInput, _ KeyChanged) error {
	return h.verify(ctx, env, in.Name, in.Fingerprint)
}

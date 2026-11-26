package users

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// pubLine returns an authorized_keys line for a crypto public key.
func pubLine(t *testing.T, pub any, comment string) string {
	t.Helper()
	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + comment
}

// skLine returns a security-key line: typ's wire format with the
// application "ssh:".
func skLine(t *testing.T, typ string, fields ...[]byte) string {
	t.Helper()
	wire := func(b []byte) []byte { return binary.BigEndian.AppendUint32(nil, uint32(len(b))) } //nolint:gosec // G115: short test fields
	var blob []byte
	for _, f := range append(append([][]byte{[]byte(typ)}, fields...), []byte("ssh:")) {
		blob = append(append(blob, wire(f)...), f...)
	}
	return typ + " " + base64.StdEncoding.EncodeToString(blob) + " sk@host"
}

func ed25519Line(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pubLine(t, pub, comment)
}

func rsaLine(t *testing.T, bits int) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return pubLine(t, &k.PublicKey, "rsa@host")
}

func fingerprint(t *testing.T, line string) string {
	t.Helper()
	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	return ssh.FingerprintSHA256(pub)
}

func TestKeyAddKeepsLines(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	old := ed25519Line(t, "old@host")
	orig := `from="10.0.0.1" ` + old + "\n# a comment\n\n"
	r.keys["alice"] = orig
	add, remove := addHandler{keyHandler{m: r.module(), add: true}}, removeHandler{keyHandler{m: r.module()}}
	line := ed25519Line(t, "new@host")
	in := KeyInput{Name: "alice", Key: "  " + line + "\n"}

	plan, err := add.Plan(ctx, r.env(adminP), in)
	fp := fingerprint(t, line)
	if err != nil || !slices.Equal(diffText(plan.Diff), []string{"+ Authorize the key " + fp + " for alice"}) {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	if _, err := add.Apply(ctx, r.env(adminP), in); err != nil {
		t.Fatal(err)
	}
	if got, want := r.keys["alice"], orig+line+"\n"; got != want {
		t.Errorf("after add\n%q\nwant\n%q", got, want)
	}
	if err := add.Verify(ctx, r.env(adminP), in, KeyChanged{}); err != nil {
		t.Errorf("verify add: %v", err)
	}
	_, err = add.Plan(ctx, r.env(adminP), in)
	wantCode(t, "add a key authorized already", err, errcode.InvalidInput)

	list, err := keyLister{m: r.module()}.Read(ctx, r.env(adminP), NameInput{Name: "alice"})
	if err != nil || len(list.Keys) != 2 || list.Keys[0].Options != `from="10.0.0.1"` || list.Keys[0].Comment != "old@host" ||
		list.Keys[0].Type != ssh.KeyAlgoED25519 || list.Keys[0].Bits != 256 || list.Keys[1].Fingerprint != fp {
		t.Errorf("list %+v, %v", list, err)
	}

	rm := KeyRemoveInput{Name: "alice", Fingerprint: fp}
	if _, err := remove.Plan(ctx, r.env(adminP), rm); err != nil {
		t.Fatal(err)
	}
	if _, err := remove.Apply(ctx, r.env(adminP), rm); err != nil {
		t.Fatal(err)
	}
	if r.keys["alice"] != orig {
		t.Errorf("after remove\n%q\nwant the original\n%q", r.keys["alice"], orig)
	}
	if err := remove.Verify(ctx, r.env(adminP), rm, KeyChanged{}); err != nil {
		t.Errorf("verify remove: %v", err)
	}
	_, err = remove.Plan(ctx, r.env(adminP), rm)
	wantCode(t, "remove a key not authorized", err, errcode.InvalidInput)

	// A file without a final line end gains one before the new line.
	r.keys["bob"] = old
	if _, err := add.Apply(ctx, r.env(adminP), KeyInput{Name: "bob", Key: line}); err != nil || r.keys["bob"] != old+"\n"+line+"\n" {
		t.Errorf("add to a file without a final line end: %q, %v", r.keys["bob"], err)
	}

	// The account is checked before the file is read.
	r.edit(passwdFile, "alice:x:1000:", "alice:x:999:")
	_, err = add.Apply(ctx, r.env(adminP), in)
	wantCode(t, "add after the UID changed", err, errcode.InvalidInput)
}

func TestKeyValidation(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	add := addHandler{keyHandler{m: r.module(), add: true}}
	ec256, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ec384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	skPoint, err := ec256.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	edPub, _, _ := ed25519.GenerateKey(rand.Reader)
	ed := ed25519Line(t, "a@host")
	for line, want := range map[string]string{
		`from="10.0.0.1" ` + ed:         "Not a public key line",
		`command="/bin/true" ` + ed:     "Not a public key line",
		"hello world":                   "Not a public key line",
		"":                              "Not a public key line",
		ed + "\n" + ed25519Line(t, "b"): "Not a public key line",
		rsaLine(t, 2048):                "RSA keys need at least 3072 bits",
	} {
		_, err := add.Plan(ctx, r.env(adminP), KeyInput{Name: "alice", Key: line})
		wantCode(t, "add "+line, err, errcode.InvalidInput)
		if err != nil && !strings.Contains(err.Error(), want) {
			t.Errorf("add %q: %v, want %q", line, err, want)
		}
	}
	for name, line := range map[string]string{
		"ed25519":    ed,
		"ecdsa 256":  pubLine(t, &ec256.PublicKey, "e@host"),
		"ecdsa 384":  pubLine(t, &ec384.PublicKey, "e@host"),
		"sk-ed25519": skLine(t, ssh.KeyAlgoSKED25519, edPub),
		"sk-ecdsa":   skLine(t, ssh.KeyAlgoSKECDSA256, []byte("nistp256"), skPoint.Bytes()),
		"rsa 3072":   rsaLine(t, 3072),
	} {
		if _, err := add.Plan(ctx, r.env(adminP), KeyInput{Name: "alice", Key: line}); err != nil {
			t.Errorf("add %s: %v", name, err)
		}
	}
	specs := map[string]core.ActionSpec{}
	for _, a := range r.module().Manifest().Actions {
		specs[a.Spec.Name] = a.Spec
	}
	if s := specs["users.key.add"]; !s.StepUp || s.CompensatedBy != "users.key.remove" || s.MinScope != core.ScopeAdmin ||
		!slices.Equal(s.LockKeys, []string{"users"}) {
		t.Errorf("key.add spec %+v", s)
	}
	if s := specs["users.key.remove"]; s.StepUp || s.CompensatedBy != "users.key.add" || s.Class != core.ClassCompensable {
		t.Errorf("key.remove spec %+v", s)
	}
}

func TestCreateWithKeys(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	h := createHandler{m: r.module()}
	k1, k2 := ed25519Line(t, "one@host"), ed25519Line(t, "two@host")

	_, err := h.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Keys: []string{k1, "not a key"}})
	wantCode(t, "create with an invalid key", err, errcode.InvalidInput)
	_, err = h.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Keys: []string{k1, k1}})
	wantCode(t, "create with a key listed twice", err, errcode.InvalidInput)
	if _, err := h.Apply(ctx, r.env(adminP), CreateInput{Name: "carol", Keys: []string{"not a key"}}); err == nil || len(r.events) != 0 {
		t.Errorf("apply with an invalid key: events %q, %v", r.events, err)
	}

	in := CreateInput{Name: "carol", Keys: []string{k1, k2}}
	plan, err := h.Plan(ctx, r.env(adminP), in)
	if err != nil || diffText(plan.Diff)[1] != "+ Authorize 2 SSH key(s) for carol" {
		t.Fatalf("plan %+v, %v", plan, err)
	}
	useradd := platform.LoginUseradd("carol", "/bin/bash")
	r.answer(platform.UseraddPath, useradd, "", func() {
		r.files[passwdFile] += "carol:x:1003:1003::/home/carol:/bin/bash\n"
		r.files[shadowFile] += "carol:!:20418:0:99999:7:::\n"
	})
	r.events = nil
	if _, err := h.Apply(ctx, r.env(adminP), in); err != nil {
		t.Fatal(err)
	}
	keysEvent := slices.Index(r.events, "write keys carol")
	if keysEvent < 0 || keysEvent < slices.Index(r.events, "run "+key(platform.UseraddPath, useradd)) {
		t.Errorf("events %q, want the keys written after useradd", r.events)
	}
	if got, want := r.keys["carol"], k1+"\n"+k2+"\n"; got != want {
		t.Errorf("carol's authorized_keys %q, want %q", got, want)
	}
	if err := h.Verify(ctx, r.env(adminP), in, Changed{}); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestNonDefaultKeysFile(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.answer(platform.SshdPath, platform.SshdEffective(), "port 22\nauthorizedkeysfile /etc/ssh/keys/%u\n")
	for _, a := range r.module().Manifest().Actions {
		if !strings.HasPrefix(a.Spec.Name, "users.key.") {
			continue
		}
		if a.Spec.Available == nil {
			t.Errorf("%s has no availability check", a.Spec.Name)
			continue
		}
		if err := a.Spec.Available(ctx, r.env(adminP)); err == nil || !strings.Contains(err.Error(), "/etc/ssh/keys/%u") {
			t.Errorf("%s availability: %v, want the keys file named", a.Spec.Name, err)
		}
	}
	_, err := createHandler{m: r.module()}.Plan(ctx, r.env(adminP), CreateInput{Name: "carol", Keys: []string{ed25519Line(t, "a")}})
	if err == nil || !strings.Contains(err.Error(), "/etc/ssh/keys/%u") {
		t.Errorf("create with keys: %v, want the keys file named", err)
	}
	if _, err := (createHandler{m: r.module()}).Plan(ctx, r.env(adminP), CreateInput{Name: "carol"}); err != nil {
		t.Errorf("create without keys: %v", err)
	}
	r.answer(platform.SshdPath, platform.SshdEffective(), "authorizedkeysfile .ssh/authorized_keys .ssh/authorized_keys2\n")
	if err := keysAvailable(ctx, r.env(adminP)); err != nil {
		t.Errorf("the default keys file: %v", err)
	}
}

func TestListCountsKeys(t *testing.T) {
	r := newRig(t)
	r.keys["alice"] = ed25519Line(t, "one") + "\n# a comment\n\n" + `from="10.0.0.1" ` + ed25519Line(t, "two") + "\n"
	r.homeErr["root"] = errors.New("platform: /root/.ssh/authorized_keys is a symbolic link")
	got, err := lister{m: r.module()}.Read(context.Background(), r.env(adminP), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]int{}
	for _, a := range got.Accounts {
		keys[a.Name] = a.Keys
	}
	if keys["alice"] != 2 || keys["bob"] != 0 || keys["root"] != 0 || len(got.Accounts) != 3 {
		t.Errorf("key counts %v of %d accounts, want alice 2, bob 0, root 0", keys, len(got.Accounts))
	}
}

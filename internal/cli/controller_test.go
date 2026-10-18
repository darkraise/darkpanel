package cli

import (
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func webCAReply(t *testing.T, pemText, fingerprint string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"pem": pemText, "fingerprint": fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestControllerWebCA(t *testing.T) {
	ca := newTestCA(t)
	rootPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}))
	get := func(reply string) exchange {
		return exchange{method: "GET", path: "/api/v1/controller/web-ca", reply: reply}
	}
	sock := newFakeSocket(t,
		get(webCAReply(t, rootPEM, ca.fingerprint())),
		get(webCAReply(t, "\x1b[2J\u202eheader\n"+rootPEM, strings.ToUpper(ca.fingerprint()))),
		get(webCAReply(t, rootPEM, newTestCA(t).fingerprint())),
		get(webCAReply(t, "not a certificate", ca.fingerprint())),
		get(webCAReply(t, rootPEM, ca.fingerprint())),
	)
	cli := cliEnv{}
	want := rootPEM + "SHA-256 fingerprint: " + ca.fingerprint() + "\n"
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != 0 || out != want {
		t.Errorf("web-ca: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != 0 || out != want {
		t.Errorf("preamble and upper-case fingerprint: exit %d\nout %q\nerr %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != ExitError || !strings.Contains(errOut, "does not match") {
		t.Errorf("wrong fingerprint: exit %d, err %q", code, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != ExitError || !strings.Contains(errOut, "no PEM certificate") {
		t.Errorf("no PEM: exit %d, err %q", code, errOut)
	}
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--json", "--socket", sock); code != 0 ||
		!jsonEqual([]byte(out), []byte(webCAReply(t, rootPEM, ca.fingerprint()))) {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "bogus"); code != ExitUsage {
		t.Errorf("controller bogus: exit %d, err %q", code, errOut)
	}
}

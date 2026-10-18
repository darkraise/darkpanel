package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/controller/auth"
)

// testTOTPSecret is a 20-byte TOTP secret in unpadded base32.
const testTOTPSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP" //nolint:gosec // G101: a test TOTP secret, not a credential

const testUserPassword = "correct horse battery" //nolint:gosec // G101: a test password, not a credential

func enrollmentChange(id, action string) string {
	return changeJSON(id, action, "compensable", "completed", fmt.Sprintf(`"revertDeadline":null,`+
		`"output":{"user":{"id":"u1","username":"ann","role":"admin","state":"pending","createdAt":"2026-10-18T12:00:00Z"},`+
		`"totpSecret":%q},"error":null,"compensatedBy":"core.user.delete"`, testTOTPSecret))
}

// activation is the plan and apply of core.user.activate with code.
func activation(code, state string) []exchange {
	extra := `"revertDeadline":null,"output":{"id":"u1","username":"ann","role":"admin","state":"active","createdAt":"2026-10-18T12:00:00Z"},` +
		`"error":null,"compensatedBy":"core.user.reset-totp"`
	if state == "failed" {
		extra = `"revertDeadline":null,"output":null,"error":{"code":"request.invalid_input","message":"invalid input"},` +
			`"compensatedBy":"core.user.reset-totp"`
	}
	return []exchange{
		{method: "POST", path: "/api/v1/actions/core.user.activate/v1/plan", body: `{"input":{"username":"ann","code":"` + code + `"}}`,
			reply: planJSON("pa", "core.user.activate", "", "", `[{"id":"user-pending","ok":true,"message":"ann is pending"}]`)},
		{method: "POST", path: "/api/v1/actions/core.user.activate/v1/apply", body: `{"planId":"pa"}`,
			reply: changeJSON("ca", "core.user.activate", "compensable", state, extra)},
	}
}

func TestUserCreateEnrollment(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := append([]exchange{
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/plan",
			body:  `{"input":{"username":"ann","password":"` + testUserPassword + `"}}`,
			reply: planJSON("pc", "core.user.create", "", "", `[{"id":"username-free","ok":true,"message":"ann is free"}]`)},
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/apply", body: `{"planId":"pc"}`,
			reply: enrollmentChange("cc", "core.user.create")},
	}, activation(code, "completed")...)
	sock := newFakeSocket(t, want...)
	cli := cliEnv{terminal: true, stdin: "y\n", secrets: []string{testUserPassword, testUserPassword, code}}
	out, errOut, exit := cli.run(t, "user", "create", "--username", "ann", "--socket", sock)
	uri := "otpauth://totp/Darkpanel:ann?secret=" + testTOTPSecret + "&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30"
	for _, s := range []string{"Change cc: completed\n", "URI: " + uri + "\n", "Secret: " + testTOTPSecret + "\n",
		"The secret will not be shown again", "▀", "Change ca: completed\n", "User ann is active"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q", s)
		}
	}
	if exit != 0 || errOut != "Password: \nRepeat the password: \nApply? [y/N] Code from the authenticator app: \n" {
		t.Errorf("exit %d, err %q\nout %s", exit, errOut, out)
	}
	if strings.Contains(out+errOut, testUserPassword) {
		t.Error("the password was echoed")
	}
}

func TestUserCreateWithoutTerminal(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sock := newFakeSocket(t, append([]exchange{
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/plan",
			body:  `{"input":{"username":"ann","password":"` + testUserPassword + `"}}`,
			reply: planJSON("pc", "core.user.create", "", "", `[]`)},
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/apply", body: `{"planId":"pc"}`,
			reply: enrollmentChange("cc", "core.user.create")},
	}, activation(code, "completed")...)...)
	out, errOut, exit := cliEnv{stdin: testUserPassword + "\n" + code + "\n"}.run(t, "user", "create", "--username", "ann", "--yes", "--socket", sock)
	if exit != 0 || !strings.Contains(out, "User ann is active") {
		t.Errorf("exit %d, out %q, err %q", exit, out, errOut)
	}
}

func TestUserCreateRefusals(t *testing.T) {
	sock := newFakeSocket(t)
	cases := []struct {
		name string
		cli  cliEnv
		args []string
		want string
	}{
		{"passwords differ", cliEnv{terminal: true, secrets: []string{testUserPassword, "other password!"}},
			[]string{"--username", "ann"}, "do not match"},
		{"no username", cliEnv{terminal: true}, nil, "--username"},
		{"no password", cliEnv{}, []string{"--username", "ann", "--yes"}, "no password"},
		{"--json", cliEnv{terminal: true}, []string{"--username", "ann", "--json"}, "--json"},
		{"--input", cliEnv{terminal: true}, []string{"--username", "ann", "--input", "-"}, "takes no --input"},
	}
	for _, c := range cases {
		_, errOut, exit := c.cli.run(t, append(append([]string{"user", "create"}, c.args...), "--socket", sock)...)
		if exit != ExitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, err %q", c.name, exit, errOut)
		}
	}
}

func TestResetTOTPEnrollment(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reset := []exchange{
		{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/plan", body: `{"input":{"username":"ann"}}`,
			reply: planJSON("pr", "core.user.reset-totp", "", "ann", `[]`)},
		{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/apply", body: `{"planId":"pr","confirmation":"ann"}`,
			reply: changeJSON("cr", "core.user.reset-totp", "irreversible", "completed", fmt.Sprintf(`"revertDeadline":null,`+
				`"output":{"user":{"id":"u1","username":"ann","role":"admin","state":"pending","createdAt":"2026-10-18T12:00:00Z"},`+
				`"totpSecret":%q},"error":null,"compensatedBy":null`, testTOTPSecret))},
	}
	sock := newFakeSocket(t, append(append(reset, activation(code, "completed")...), append(reset, activation("000000", "failed")...)...)...)
	args := []string{"user", "reset-totp", "--username", "ann", "--yes", "--confirmation", "ann", "--socket", sock}
	out, errOut, exit := cliEnv{stdin: code + "\n"}.run(t, args...)
	if exit != 0 || !strings.Contains(out, "Secret: "+testTOTPSecret) || !strings.Contains(out, "User ann is active") {
		t.Errorf("reset-totp: exit %d, out %q, err %q", exit, out, errOut)
	}
	_, errOut, exit = cliEnv{stdin: "000000\n"}.run(t, args...)
	if exit != ExitError || !strings.Contains(errOut, "darkpanel user activate --username ann --socket "+sock) {
		t.Errorf("wrong code: exit %d, err %q", exit, errOut)
	}
}

func TestResetTOTPRefusesNoncanonicalSecret(t *testing.T) {
	sock := newFakeSocket(t,
		exchange{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/plan", body: `{"input":{"username":"ann"}}`,
			reply: planJSON("pr", "core.user.reset-totp", "", "ann", `[]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/apply", body: `{"planId":"pr","confirmation":"ann"}`,
			reply: changeJSON("cr", "core.user.reset-totp", "irreversible", "completed", fmt.Sprintf(`"revertDeadline":null,`+
				`"output":{"user":{"id":"u1","username":"ann","role":"admin","state":"pending","createdAt":"2026-10-18T12:00:00Z"},`+
				`"totpSecret":%q},"error":null,"compensatedBy":null`, "JBSWY3DP\rEHPK3PXPJBSWY3DPEHPK3PXP"))},
	)
	out, errOut, exit := cliEnv{stdin: "000000\n"}.run(t, "user", "reset-totp", "--username", "ann", "--yes", "--confirmation", "ann", "--socket", sock)
	if exit == ExitOK || !strings.Contains(errOut, "no valid TOTP secret") || strings.Contains(out, "\r") || strings.Contains(out, "Secret:") {
		t.Errorf("exit %d, out %q, err %q", exit, out, errOut)
	}
}

func TestActivateHintKeepsTheConnectionFlags(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{nil, "darkpanel user activate --username ann"},
		{[]string{"--dev"}, "darkpanel user activate --username ann --dev"},
		{[]string{"--socket", "/run/dp.sock"}, "darkpanel user activate --username ann --socket /run/dp.sock"},
		{[]string{"--dev", "--socket", "/run/dp.sock"}, "darkpanel user activate --username ann --dev --socket /run/dp.sock"},
		{[]string{"--dev=false"}, "darkpanel user activate --username ann"},
	}
	for _, c := range cases {
		cmd := &cobra.Command{Use: "create"}
		addConnFlags(cmd, true, false)
		if err := cmd.ParseFlags(c.args); err != nil {
			t.Fatal(err)
		}
		if got := activateHint(cmd, "ann"); got != c.want {
			t.Errorf("%v: hint %q, want %q", c.args, got, c.want)
		}
	}
}

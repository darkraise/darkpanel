package cli

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// The actions whose generated commands run the TOTP enrollment (spec
// section 15), and the one that ends it.
const (
	actionUserCreate    = "core.user.create"
	actionUserResetTOTP = "core.user.reset-totp"
	actionUserActivate  = "core.user.activate"
)

// latestAction returns the highest version of the action name in reg.
func latestAction(reg *core.Registry, name string) (core.ActionDef, bool) {
	var def core.ActionDef
	found := false
	for _, a := range reg.Actions() {
		if a.Spec.Name == name {
			def, found = a, true
		}
	}
	return def, found
}

// wrapEnrollment makes the generated `user create` and `user reset-totp`
// commands run the enrollment flow. They keep their flags and annotations,
// so FindCommand still reports them; --input is hidden, because the flow
// asks for the password and the code itself.
func (a *app) wrapEnrollment(root *cobra.Command) {
	activate, ok := latestAction(a.opts.Registry, actionUserActivate)
	if !ok {
		return
	}
	for _, name := range []string{actionUserCreate, actionUserResetTOTP} {
		cmd, found := FindCommand(root, name)
		def, ok := latestAction(a.opts.Registry, name)
		if !found || !ok {
			continue
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runEnrollment(cmd, def, activate) }
		_ = cmd.Flags().MarkHidden("input")
	}
}

// runEnrollment applies def (core.user.create, which first asks for the
// password twice, or core.user.reset-totp), shows the new TOTP secret as a
// QR code with its otpauth URI and the secret below it, asks for a code and
// applies core.user.activate with it.
func (a *app) runEnrollment(cmd *cobra.Command, def, activate core.ActionDef) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return usagef("%s is interactive and has no --json output", cmd.CommandPath())
	}
	if in, _ := cmd.Flags().GetString("input"); in != "" {
		return usagef("%s asks for the password and the code itself and takes no --input", cmd.CommandPath())
	}
	username, _ := cmd.Flags().GetString("username")
	if username == "" {
		return usagef("required flag --username not set")
	}
	input := map[string]any{"username": username}
	if def.Spec.Name == actionUserCreate {
		password, err := a.newPassword()
		if err != nil {
			return err
		}
		input["password"] = password
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	ch, _, err := a.planAndApply(cmd, apiCalls(c, def, ""), input, false)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State))
	if err := changeOutcome(ch); err != nil {
		return err
	}
	var en struct {
		TOTPSecret string `json:"totpSecret"`
	}
	if err := json.Unmarshal(ch.Output, &en); err != nil {
		return fmt.Errorf("decode the enrollment: %w", err)
	}
	// DecodeString skips CR and LF, so only a secret that re-encodes to
	// itself is free of characters a terminal would act on.
	b32 := base32.StdEncoding.WithPadding(base32.NoPadding)
	if raw, err := b32.DecodeString(en.TOTPSecret); err != nil || en.TOTPSecret == "" || b32.EncodeToString(raw) != en.TOTPSecret {
		return errors.New("the controller answered no valid TOTP secret")
	}
	uri := enrollmentURI(username, en.TOTPSecret)
	_, _ = fmt.Fprintln(out, "Scan this QR code with an authenticator app, or enter the secret by hand:")
	qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
	_, _ = fmt.Fprintf(out, "URI: %s\nSecret: %s\nThe secret will not be shown again.\n", sanitize(uri), sanitize(en.TOTPSecret))
	retry := "activate the user with: " + activateHint(cmd, username)
	code, err := a.term.secret("Code from the authenticator app: ")
	if errors.Is(err, io.EOF) {
		return usagef("no code given; %s", retry)
	}
	if err != nil {
		return err
	}
	act, _, err := a.planAndApply(cmd, apiCalls(c, activate, ""), map[string]any{"username": username, "code": code}, true)
	if err != nil {
		return fmt.Errorf("%w; %s", err, retry)
	}
	_, _ = fmt.Fprintf(out, "Change %s: %s\n", sanitize(act.ID), sanitize(act.State))
	if err := changeOutcome(act); err != nil {
		return fmt.Errorf("%w; %s", err, retry)
	}
	_, err = fmt.Fprintf(out, "User %s is active and can log in.\n", sanitize(username))
	return err
}

// activateHint is the `user activate` command line that finishes the
// enrollment of username over the same socket as cmd.
func activateHint(cmd *cobra.Command, username string) string {
	hint := "darkpanel user activate --username " + username
	if dev, _ := cmd.Flags().GetBool("dev"); dev && cmd.Flags().Changed("dev") {
		hint += " --dev"
	}
	if cmd.Flags().Changed("socket") {
		path, _ := cmd.Flags().GetString("socket")
		hint += " --socket " + path
	}
	return hint
}

// newPassword reads the new user's password: twice on a terminal, without
// echo, refusing two different answers; once from standard input otherwise.
func (a *app) newPassword() (string, error) {
	password, err := a.term.secret("Password: ")
	if errors.Is(err, io.EOF) {
		return "", usagef("no password: run on a terminal, or give it as one line on standard input")
	}
	if err != nil || !a.term.isTerm {
		return password, err
	}
	again, err := a.term.secret("Repeat the password: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if again != password {
		return "", usagef("the passwords do not match")
	}
	return password, nil
}

// enrollmentURI is the otpauth URI of spec section 8.
func enrollmentURI(username, secret string) string {
	return "otpauth://totp/Darkpanel:" + url.PathEscape(username) + "?secret=" + secret +
		"&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30"
}

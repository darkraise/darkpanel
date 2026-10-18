package cli

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

// controllerCommand is the hand-written `controller` group; in M1 it has
// web-ca only.
func (a *app) controllerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Commands for the controller on this host",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	webCA := &cobra.Command{
		Use:   "web-ca",
		Short: "Print the web certificate authority's root certificate and its SHA-256 fingerprint",
		Long: "Print the root certificate of the controller's web certificate authority in PEM, to trust in a " +
			"browser, and its SHA-256 fingerprint, to pin as caFingerprint in the CLI config file. " +
			"It reads both from the controller socket.",
		Args: cobra.NoArgs,
		RunE: a.runWebCA,
	}
	addConnFlags(webCA, true, false)
	webCA.Flags().Bool("json", false, "print the raw JSON answer")
	cmd.AddCommand(webCA)
	return cmd
}

// runWebCA prints the root from GET /api/v1/controller/web-ca. The PEM is
// decoded, checked against the fingerprint and written re-encoded, so
// nothing but the certificate reaches the terminal.
func (a *app) runWebCA(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/controller/web-ca", nil, &raw); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(w, "%s\n", raw)
		return err
	}
	var resp struct {
		PEM         string `json:"pem"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode the web CA: %w", err)
	}
	block, _ := pem.Decode([]byte(resp.PEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("the controller answered no PEM certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("the controller's web CA certificate: %w", err)
	}
	sum := sha256.Sum256(block.Bytes)
	fingerprint := hex.EncodeToString(sum[:])
	if !strings.EqualFold(fingerprint, resp.Fingerprint) {
		return errors.New("the fingerprint the controller sent does not match its certificate")
	}
	if _, err := w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "SHA-256 fingerprint: %s\n", fingerprint)
	return err
}

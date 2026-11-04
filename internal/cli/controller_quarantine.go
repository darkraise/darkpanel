package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const quarantinePath = "/api/v1/controller/quarantine"

// quarantineRecord is one quarantined record in the quarantine routes'
// answers.
type quarantineRecord struct {
	ID              string   `json:"id"`
	Kind            string   `json:"kind"`
	Action          string   `json:"action"`
	HostID          string   `json:"hostId"`
	QuarantinedFrom string   `json:"quarantinedFrom"`
	Locks           []string `json:"locks"`
}

// quarantineStatus is the answer of GET /api/v1/controller/quarantine.
type quarantineStatus struct {
	Active     bool               `json:"active"`
	Tx         string             `json:"tx"`
	RestoredAt *time.Time         `json:"restoredAt"`
	Records    []quarantineRecord `json:"records"`
}

// quarantineHeldLock is a lock that stays held after a release.
type quarantineHeldLock struct {
	Key    string `json:"key"`
	Record string `json:"record"`
}

// quarantineCommand is the `quarantine` group of the `controller` commands:
// it lists the records a restore quarantined and resolves them, issues the
// enrollment tokens a restored controller needs, and releases the
// quarantine.
func (a *app) quarantineCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "quarantine",
		Short: "Inspect and release the restore quarantine",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	list := &cobra.Command{
		Use:   "list",
		Short: "List the records a restore quarantined",
		Args:  cobra.NoArgs,
		RunE:  a.runQuarantineList,
	}
	resolve := &cobra.Command{
		Use:   "resolve <record> <done|never|repair>",
		Short: "Settle a quarantined record by what happened to its work",
		Long: "Settle a quarantined record: done means the work finished, never means it was never applied, " +
			"and repair means it needs repair after the quarantine is released. " +
			"A record resolved as done or never releases its locks; one resolved as repair keeps them.",
		Args: cobra.ExactArgs(2),
		RunE: a.runQuarantineResolve,
	}
	enroll := &cobra.Command{
		Use:   "enroll-token <host>",
		Short: "Issue a token to enroll a host again",
		Args:  cobra.ExactArgs(1),
		RunE:  a.runQuarantineEnrollToken,
	}
	release := &cobra.Command{
		Use:   "release",
		Short: "End the restore quarantine",
		Args:  cobra.NoArgs,
		RunE:  a.runQuarantineRelease,
	}
	release.Flags().Bool("keep-unresolved", false, "release although records are unresolved; their locks stay held")
	for _, c := range []*cobra.Command{list, resolve, enroll, release} {
		addConnFlags(c, true, false)
		c.Flags().Bool("json", false, "print the raw JSON answer")
		cmd.AddCommand(c)
	}
	return cmd
}

// quarantineCall sends the request and returns the raw answer, which --json
// prints as it is.
func (a *app) quarantineCall(cmd *cobra.Command, method, path string, body any) (json.RawMessage, error) {
	c, err := a.connect(cmd)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	if err := c.do(cmd.Context(), method, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// printQuarantineJSON prints raw and a newline when --json is set, and says
// whether it did.
func printQuarantineJSON(cmd *cobra.Command, raw json.RawMessage) (bool, error) {
	if asJSON, _ := cmd.Flags().GetBool("json"); !asJSON {
		return false, nil
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
	return true, err
}

func (a *app) runQuarantineList(cmd *cobra.Command, _ []string) error {
	raw, err := a.quarantineCall(cmd, http.MethodGet, quarantinePath, nil)
	if err != nil {
		return err
	}
	if done, err := printQuarantineJSON(cmd, raw); done {
		return err
	}
	var st quarantineStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("decode the quarantine: %w", err)
	}
	w := cmd.OutOrStdout()
	if st.Active {
		restored := ""
		if st.RestoredAt != nil {
			restored = ", restored " + st.RestoredAt.UTC().Format(time.RFC3339)
		}
		if _, err := fmt.Fprintf(w, "Restore quarantine is active: restore %s%s.\n", sanitize(st.Tx), restored); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintln(w, "The controller is not in restore quarantine."); err != nil {
		return err
	}
	if len(st.Records) == 0 {
		_, err := fmt.Fprintln(w, "No records are quarantined.")
		return err
	}
	rows := make([][]string, 0, len(st.Records))
	for _, r := range st.Records {
		rows = append(rows, []string{r.ID, r.Kind, r.Action, dashIfEmpty(r.HostID), r.QuarantinedFrom, dashIfEmpty(strings.Join(r.Locks, ","))})
	}
	return writeTable(w, []string{"ID", "KIND", "ACTION", "HOST", "FROM", "LOCKS"}, rows)
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (a *app) runQuarantineResolve(cmd *cobra.Command, args []string) error {
	record, outcome := args[0], args[1]
	if record == "" {
		return usagef("the record id is required")
	}
	switch outcome {
	case "done", "never", "repair":
	default:
		return usagef("the outcome is done, never or repair, not %q", outcome)
	}
	raw, err := a.quarantineCall(cmd, http.MethodPost, quarantinePath+"/"+url.PathEscape(record)+"/resolve", map[string]string{"outcome": outcome})
	if err != nil {
		return err
	}
	if done, err := printQuarantineJSON(cmd, raw); done {
		return err
	}
	var r quarantineRecord
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("decode the resolved record: %w", err)
	}
	w := cmd.OutOrStdout()
	if outcome != "repair" {
		_, err := fmt.Fprintf(w, "Resolved %s %s as %s; its locks are released.\n", sanitize(r.Kind), sanitize(r.ID), outcome)
		return err
	}
	_, err = fmt.Fprintf(w, "Resolved %s %s as repair; its locks stay held.\nRepair it after release: darkpanel change repair %s\n",
		sanitize(r.Kind), sanitize(r.ID), sanitize(r.ID))
	return err
}

func (a *app) runQuarantineEnrollToken(cmd *cobra.Command, args []string) error {
	host := args[0]
	if host == "" {
		return usagef("the host is required")
	}
	raw, err := a.quarantineCall(cmd, http.MethodPost, quarantinePath+"/enroll-token", map[string]string{"hostId": host})
	if err != nil {
		return err
	}
	if done, err := printQuarantineJSON(cmd, raw); done {
		return err
	}
	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expiresAt"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode the enrollment token: %w", err)
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "Enrollment token for host %s (valid until %s, shown once):\n%s\n",
		sanitize(host), res.ExpiresAt.UTC().Format(time.RFC3339), sanitize(res.Token))
	return err
}

func (a *app) runQuarantineRelease(cmd *cobra.Command, _ []string) error {
	keep, _ := cmd.Flags().GetBool("keep-unresolved")
	raw, err := a.quarantineCall(cmd, http.MethodPost, quarantinePath+"/release", map[string]bool{"keepUnresolved": keep})
	if err != nil {
		return err
	}
	if done, err := printQuarantineJSON(cmd, raw); done {
		return err
	}
	var res struct {
		HeldLocks []quarantineHeldLock `json:"heldLocks"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("decode the release: %w", err)
	}
	return writeRelease(cmd.OutOrStdout(), res.HeldLocks)
}

func writeRelease(w io.Writer, held []quarantineHeldLock) error {
	if len(held) == 0 {
		_, err := fmt.Fprintln(w, "Restore quarantine released. No locks stay held.")
		return err
	}
	if _, err := fmt.Fprintln(w, "Restore quarantine released. These locks stay held by quarantined records:"); err != nil {
		return err
	}
	rows := make([][]string, 0, len(held))
	for _, l := range held {
		rows = append(rows, []string{l.Key, l.Record})
	}
	return writeTable(w, []string{"KEY", "RECORD"}, rows)
}

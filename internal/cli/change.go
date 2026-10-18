package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// changeColumns are the Change fields `change list` shows; --json has all.
var changeColumns = []string{"id", "hostId", "action", "version", "class", "state", "actor", "updatedAt", "revertDeadline"}

// changeCommand is the hand-written `change` group (spec section 15):
// list, show, confirm and revert over the network API's change routes.
func (a *app) changeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "List, show, confirm and revert changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(a.changeListCommand(), a.changeShowCommand(),
		a.changeOpCommand("confirm", core.StateConfirmed, "Confirm a pending revertible change before its revert deadline"),
		a.changeOpCommand("revert", core.StateReverted, "Revert a pending revertible change now"))
	return cmd
}

func (a *app) changeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List changes, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			q := url.Values{}
			for _, name := range []string{"state", "before"} {
				if v, _ := cmd.Flags().GetString(name); v != "" {
					q.Set(name, v)
				}
			}
			if limit, _ := cmd.Flags().GetInt("limit"); limit != 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if want, _ := cmd.Flags().GetString("host"); want != "" {
				host, err := resolveHost(cmd.Context(), c, want)
				if err != nil {
					return err
				}
				q.Set("hostId", host)
			}
			path := "/api/v1/changes"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
				return err
			}
			return printChanges(cmd, raw)
		},
	}
	cmd.Flags().String("state", "", "only changes in this state: applying, pending, confirmed, reverted, completed, failed or interrupted")
	cmd.Flags().String("host", "", "only changes on this host, by id or name")
	cmd.Flags().String("before", "", "only changes older than this change id")
	cmd.Flags().Int("limit", 0, "most changes to list, 1 to 500")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// printChanges writes the changeColumns of each change as a table.
func printChanges(cmd *cobra.Command, raw json.RawMessage) error {
	var resp struct {
		Changes []map[string]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode the changes: %w", err)
	}
	if len(resp.Changes) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return err
	}
	rows := make([][]string, 0, len(resp.Changes))
	for _, ch := range resp.Changes {
		row := make([]string, len(changeColumns))
		for i, col := range changeColumns {
			row[i] = cellText(ch[col])
		}
		rows = append(rows, row)
	}
	return writeTable(cmd.OutOrStdout(), changeColumns, rows)
}

func (a *app) changeShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/changes/"+url.PathEscape(args[0]), nil, &raw); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(w, "%s\n", raw)
				return err
			}
			if err := render(w, raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if ch.State == core.StateInterrupted && ch.Class == core.ClassCompensable && ch.CompensatedBy != nil {
				_, err = fmt.Fprintf(w, "\nThis change was interrupted and may be partly applied; its compensating action is %s:\n  darkpanel %s\n",
					sanitize(*ch.CompensatedBy), sanitize(strings.Join(CommandPath(*ch.CompensatedBy), " ")))
			}
			return err
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// changeOpCommand is `change confirm` or `change revert`; the change must
// end in state want.
func (a *app) changeOpCommand(op, want, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   op + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			path := "/api/v1/changes/" + url.PathEscape(args[0]) + "/" + op
			if err := c.do(cmd.Context(), http.MethodPost, path, nil, &raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
			} else {
				err = printChange(cmd.OutOrStdout(), ch, nil)
			}
			if err != nil {
				return err
			}
			if ch.State == want {
				return nil
			}
			if ch.Error != nil {
				return fmt.Errorf("change %s is %s, not %s: %s (%s)", ch.ID, ch.State, want, ch.Error.Message, ch.Error.Code)
			}
			return fmt.Errorf("change %s is %s, not %s", ch.ID, ch.State, want)
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

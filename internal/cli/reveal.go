package cli

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// annotationReveal marks the generated commands of reveal actions, whose
// --local the root refuses with its own reason.
const annotationReveal = "darkpanel.reveal"

// runReveal runs a reveal action through the controller's reveal route and
// prints the value once, as JSON on one line, with or without --json (spec
// 1.14). It has no --local form: a reveal is authorized and audited by the
// controller, never by the agent socket.
func (a *app) runReveal(cmd *cobra.Command, def core.ActionDef, fields []inputField) error {
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	var resp struct {
		Value json.RawMessage `json:"value"`
	}
	if err := c.do(cmd.Context(), http.MethodPost, actionPath(def, host, "reveal"), map[string]any{"input": input}, &resp); err != nil {
		return err
	}
	_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", resp.Value)
	return err
}

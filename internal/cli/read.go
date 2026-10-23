package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// addConnFlags adds the flags that choose the server: --socket and --dev
// for break-glass commands, which use the controller socket (spec section
// 14), --url for every other; and --host for agent-side commands.
func addConnFlags(cmd *cobra.Command, breakGlass, agent bool) {
	if breakGlass {
		cmd.Flags().String("socket", "", "controller socket path (default: the production path, or the development path with --dev)")
		cmd.Flags().Bool("dev", false, "use the development controller socket under $XDG_RUNTIME_DIR or $XDG_STATE_HOME")
	} else {
		cmd.Flags().String("url", "", "API URL (default: $"+EnvURL+", then the config file, then "+DefaultURL+")")
	}
	if agent {
		cmd.Flags().String("host", "", "host id or name; may be omitted when there is exactly one host")
	}
}

// connect returns the client cmd's connection flags choose.
func (a *app) connect(cmd *cobra.Command) (*client, error) {
	if cmd.Flags().Lookup("socket") != nil {
		path, _ := cmd.Flags().GetString("socket")
		if path == "" {
			dev, _ := cmd.Flags().GetBool("dev")
			p, err := a.opts.SelectPaths(cmd, dev)
			if err != nil {
				return nil, err
			}
			path = p.ControllerSocket
		}
		return newSocketClient(path), nil
	}
	u, _ := cmd.Flags().GetString("url")
	return connectNetwork(u, a.opts.Getenv, a.opts.UserHomeDir, cmd.ErrOrStderr())
}

// actionPath is the API path of op on def: under the host for an
// agent-side action, without one for a controller-side action.
func actionPath(def core.ActionDef, hostID, op string) string {
	tail := fmt.Sprintf("/actions/%s/v%d/%s", url.PathEscape(def.Spec.Name), def.Spec.Version, op)
	if def.Spec.Side == core.SideAgent {
		return "/api/v1/hosts/" + url.PathEscape(hostID) + tail
	}
	return "/api/v1" + tail
}

// hostRef is the part of core.host.list's Host the CLI reads.
type hostRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// resolveHost returns the id of the host --host names, by id or name, or
// of the only host when want is empty (spec section 15). Hosts come from
// core.host.list.
func resolveHost(ctx context.Context, c *client, want string) (string, error) {
	var resp struct {
		Output struct {
			Hosts []hostRef `json:"hosts"`
		} `json:"output"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, &resp); err != nil {
		return "", err
	}
	hosts := resp.Output.Hosts
	var listed []string
	for _, h := range hosts {
		listed = append(listed, h.ID+" ("+h.Name+")")
	}
	if want == "" {
		switch len(hosts) {
		case 0:
			return "", fmt.Errorf("the controller lists no hosts")
		case 1:
			return hosts[0].ID, nil
		}
		return "", usagef("there are %d hosts; choose one with --host: %s", len(hosts), strings.Join(listed, ", "))
	}
	var named []string
	for _, h := range hosts {
		if h.ID == want {
			return h.ID, nil
		}
		if h.Name == want {
			named = append(named, h.ID)
		}
	}
	switch len(named) {
	case 0:
		return "", usagef("no host %q; hosts: %s", want, strings.Join(listed, ", "))
	case 1:
		return named[0], nil
	}
	return "", usagef("several hosts are named %q; give the host id: %s", want, strings.Join(named, ", "))
}

// hostFor resolves cmd's --host for an agent-side action; a
// controller-side action has no host.
func hostFor(cmd *cobra.Command, c *client, side core.Side) (string, error) {
	if side != core.SideAgent {
		return "", nil
	}
	want, _ := cmd.Flags().GetString("host")
	return resolveHost(cmd.Context(), c, want)
}

// runRead runs a read action and shows its output; with --local the agent
// socket runs it.
func (a *app) runRead(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	local, err := useLocal(cmd)
	if err != nil {
		return err
	}
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	if local {
		return a.localRead(cmd, def, input, secrets)
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
		Output json.RawMessage `json:"output"`
	}
	if err := c.do(cmd.Context(), http.MethodPost, actionPath(def, host, "read"), map[string]any{"input": input}, &resp); err != nil {
		return err
	}
	return a.show(cmd, resp.Output, secrets)
}

// resourceCommand builds `<module> <resource> get`.
func (a *app) resourceCommand(e entry) (*cobra.Command, error) {
	res := *e.Resource
	secrets, err := secretOutputs(res.OutputType)
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{
		Use:         "get",
		Short:       "Read the " + res.Name + " resource of a host",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationResource: e.Name, annotationLocal: "true"},
	}
	addConnFlags(cmd, false, true)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		local, err := useLocal(cmd)
		if err != nil {
			return err
		}
		if local {
			return a.localResource(cmd, res.Name, secrets)
		}
		c, err := a.connect(cmd)
		if err != nil {
			return err
		}
		host, err := hostFor(cmd, c, core.SideAgent)
		if err != nil {
			return err
		}
		module, name, _ := strings.Cut(res.Name, ".")
		var raw json.RawMessage
		path := "/api/v1/hosts/" + url.PathEscape(host) + "/resources/" + url.PathEscape(module) + "/" + url.PathEscape(name)
		if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
			return err
		}
		return a.show(cmd, raw, secrets)
	}
	return cmd, nil
}

// show writes an output: the raw JSON with --json, else rendered for
// people with the note on secret outputs.
func (a *app) show(cmd *cobra.Command, raw json.RawMessage, secrets []string) error {
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(w, "%s\n", raw)
		return err
	}
	if err := render(w, raw); err != nil {
		return err
	}
	noteSecrets(w, secrets, raw)
	return nil
}

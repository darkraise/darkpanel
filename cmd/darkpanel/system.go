package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
)

// runInstall is system install's installer; tests replace it.
var runInstall = install.Install

// socketRequestTimeout bounds one request on the controller socket, as the
// CLI's own socket client does.
const socketRequestTimeout = 90 * time.Second

// agentHelloTimeout bounds the hello that tells whether the agent already
// holds a live controller session.
const agentHelloTimeout = 10 * time.Second

func newSystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "Install this host's system services",
		RunE:  runGroup,
	}
	cmd.AddCommand(newSystemInstallCmd())
	return cmd
}

func newSystemInstallCmd() *cobra.Command {
	var (
		roleFlag       string
		role           install.Role
		agentListen    []string
		agentAdvertise string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or repair the controller or agent role on this host",
		Long: "Install or repair the controller or agent role on this host: directories, users, keys, units, " +
			"enrollment and the started services. Rerun it to finish or repair an install. For the agent role " +
			"the enrollment token is read from standard input when it is not a terminal; install.sh pipes it.",
		Args: cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			var err error
			if role, err = install.ParseRole(roleFlag); err != nil {
				return err
			}
			if role != install.RoleController && (len(agentListen) > 0 || agentAdvertise != "") {
				return errors.New("--agent-listen and --agent-advertise are for the controller role")
			}
			addrs := slices.Clone(agentListen)
			if agentAdvertise != "" {
				addrs = append(addrs, agentAdvertise)
			}
			for _, a := range addrs {
				if err := install.CheckAgentAddress(a); err != nil {
					return err
				}
			}
			if geteuid() != 0 {
				return errors.New("system install runs as root; run it with sudo")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var token string
			if in := cmd.InOrStdin(); role == install.RoleAgent && !stdinTerminal(in) {
				data, err := io.ReadAll(io.LimitReader(in, maxStdinToken+1))
				if err != nil {
					return fmt.Errorf("read the token from standard input: %w", err)
				}
				if len(data) > maxStdinToken {
					return errors.New("standard input is larger than an enrollment token")
				}
				token = strings.TrimSpace(string(data))
			}
			sd, err := newSystemd(cmd.Context())
			if err != nil {
				return err
			}
			l := installLayout()
			cfg := install.Config{
				Layout: l, Role: role, AgentListen: agentListen, AgentAdvertise: agentAdvertise, Token: token,
				Modules: manifests(modules.All()), BinaryVersion: buildinfo.Version,
				Systemd: sd, Exec: platform.NewRunner(platform.ProductionRules()), Users: install.SystemUsers{},
				Agent: installAgent{socket: l.Paths.AgentSocket, tlsDir: l.Paths.AgentTLSDir},
				Clock: core.SystemClock{}, Out: cmd.OutOrStdout(),
			}
			if role == install.RoleController {
				cfg.Controller = newSocketController(l.Paths.ControllerSocket)
			}
			return errors.Join(runInstall(cmd.Context(), cfg), sd.Close())
		},
	}
	cmd.Flags().StringVar(&roleFlag, "role", "", "controller or agent")
	cmd.Flags().StringArrayVar(&agentListen, "agent-listen", nil,
		"controller role: extra address of this host for the agent listener, <ip>:<port> (repeatable)")
	cmd.Flags().StringVar(&agentAdvertise, "agent-advertise", "",
		"controller role: <host>:<port> that enrollment tokens tell agents to connect to")
	return cmd
}

func manifests(mods []core.Module) []core.Manifest {
	out := make([]core.Manifest, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Manifest())
	}
	return out
}

// socketController is install.Controller over the controller socket, whose
// peer credentials are the authentication.
type socketController struct {
	client *http.Client
}

func newSocketController(path string) socketController {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("controller socket %s: %w", path, err)
		}
		return c, nil
	}}
	return socketController{client: &http.Client{Transport: tr, Timeout: socketRequestTimeout}}
}

func (c socketController) Healthy(ctx context.Context) error {
	var health struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return fmt.Errorf("the controller reports status %q", health.Status)
	}
	return nil
}

// EnrollLocal plans core.host.enroll-local and applies it with the plan's
// confirmation phrase: the action is irreversible, and the installer stands
// for the operator who would type it.
func (c socketController) EnrollLocal(ctx context.Context) (string, error) {
	const route = "/api/v1/actions/core.host.enroll-local/v1/"
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	if err := c.do(ctx, http.MethodPost, route+"plan", map[string]any{"input": map[string]any{}}, &plan); err != nil {
		return "", err
	}
	apply := struct {
		PlanID       string `json:"planId"`
		Confirmation string `json:"confirmation"`
	}{PlanID: plan.PlanID}
	if plan.Confirmation != nil {
		apply.Confirmation = *plan.Confirmation
	}
	var change struct {
		State  string `json:"state"`
		Output struct {
			Token string `json:"token"`
		} `json:"output"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, route+"apply", apply, &change); err != nil {
		return "", err
	}
	switch {
	case change.Error != nil:
		return "", fmt.Errorf("core.host.enroll-local %s: %s (%s)", change.State, change.Error.Message, change.Error.Code)
	case change.Output.Token == "":
		return "", fmt.Errorf("core.host.enroll-local %s without a token", change.State)
	}
	return change.Output.Token, nil
}

// do sends body as JSON (nil sends none) and decodes a 2xx answer into out;
// any other answer fails with the problem's detail and code.
func (c socketController) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://controller"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		var p struct {
			Detail string `json:"detail"`
			Code   string `json:"code"`
		}
		if json.Unmarshal(data, &p) == nil && p.Detail != "" {
			return fmt.Errorf("%s %s: %s (%s)", method, path, p.Detail, p.Code)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// installAgent is install.Agent over this host's agent. Its Check asks the
// running agent first: a handshake beside a live session would replace that
// session, taking the host offline for a moment on every installer rerun.
type installAgent struct {
	socket string
	tlsDir string
}

func (a installAgent) Check(ctx context.Context) (install.CheckResult, error) {
	helloCtx, cancel := context.WithTimeout(ctx, agentHelloTimeout)
	defer cancel()
	client := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", a.socket)
	}})
	if h, err := client.Hello(helloCtx); err == nil && h.ControllerConnected != nil && *h.ControllerConnected {
		return install.CheckAccepted, nil
	}
	return daemonCheck(ctx, a.tlsDir)
}

func (a installAgent) Enroll(ctx context.Context, token string) error {
	_, err := daemonEnroll(ctx, a.tlsDir, token)
	return err
}

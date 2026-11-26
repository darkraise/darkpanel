package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
)

// AgentPolicyConfig is what darkpanel agent policy needs.
type AgentPolicyConfig struct {
	Path    string     // the policy file, runtime.PolicyPath in production
	Socket  string     // the agent socket that reloads it
	Owner   uint32     // the uid the file must be owned by: root, 0, in production
	Geteuid func() int // os.Geteuid in production
}

// NewAgentPolicyCommand is darkpanel agent policy deny|allow <module>...: it
// rewrites the host-local policy file, which only root on the host may
// change, and tells a running agent to read it again.
func NewAgentPolicyCommand(cfg AgentPolicyConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policy",
		Short: "Turn the console or jobs module off on this host, whatever the controller asks",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	modules := strings.Join(runtime.PolicyModules, " or ")
	for _, deny := range []bool{true, false} {
		use, short := "allow <module>...", "Stop denying "+modules+" on this host"
		if deny {
			use, short = "deny <module>...", "Deny "+modules+" on this host"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.MinimumNArgs(1),
			RunE:  func(cmd *cobra.Command, args []string) error { return runAgentPolicy(cmd, cfg, deny, args) },
		})
	}
	return cmd
}

func runAgentPolicy(cmd *cobra.Command, cfg AgentPolicyConfig, deny bool, args []string) error {
	if cfg.Geteuid() != 0 {
		return usagef("agent policy changes a file only root may write; run it with sudo")
	}
	if _, err := runtime.NewPolicy(args); err != nil {
		return usagef("%v", err)
	}
	cur, err := runtime.ReadPolicy(cfg.Path, cfg.Owner)
	if err != nil {
		// The agent reads an invalid file this way at start, so the change
		// starts from what is in force there, not from nothing.
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "darkpanel: %v; starting from denying %s\n", err,
			strings.Join(runtime.PolicyModules, " and "))
		cur, _ = runtime.NewPolicy(runtime.PolicyModules)
	}
	want := cur.Modules()
	for _, m := range args {
		switch {
		case deny && !slices.Contains(want, m):
			want = append(want, m)
		case !deny:
			want = slices.DeleteFunc(want, func(d string) bool { return d == m })
		}
	}
	next, err := runtime.NewPolicy(want)
	if err != nil {
		return err
	}
	if err := writePolicyFile(cfg.Path, next.Modules()); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if mods := next.Modules(); len(mods) > 0 {
		_, _ = fmt.Fprintf(out, "Denied on this host: %s\n", strings.Join(mods, ", "))
	} else {
		_, _ = fmt.Fprintln(out, "This host's policy denies no module")
	}
	err = reloadAgentPolicy(cmd.Context(), cfg.Socket)
	var noAgent *socketError
	switch {
	case errors.As(err, &noAgent) && noAgent.absent():
		_, _ = fmt.Fprintln(out, "The agent is not running; it reads the policy file when it starts")
		return nil
	case err != nil:
		return fmt.Errorf("the policy file is written, but the agent did not read it: %w", err)
	}
	_, _ = fmt.Fprintln(out, "The agent read the policy file")
	return nil
}

// writePolicyFile replaces the policy file atomically: a reader sees the old
// file or the new one, never part of either.
func writePolicyFile(path string, deny []string) error {
	if deny == nil {
		deny = []string{}
	}
	data, err := json.Marshal(runtime.PolicyFile{DenyModules: deny})
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".agent-policy-*.json")
	if err != nil {
		return fmt.Errorf("write the policy file: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(append(data, '\n'))
	if err == nil {
		// The spec gives the file mode 0644: anyone may read what is denied.
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		return fmt.Errorf("write the policy file: %w", err)
	}
	d, err := os.Open(dir) //nolint:gosec // G304: the policy file's own directory
	if err != nil {
		return fmt.Errorf("write the policy file: %w", err)
	}
	defer func() { _ = d.Close() }()
	if err := d.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) {
		return fmt.Errorf("write the policy file: %w", err)
	}
	return nil
}

// reloadAgentPolicy asks the agent on socket to read the policy file again.
func reloadAgentPolicy(ctx context.Context, socket string) error {
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", socket)
		if err != nil {
			return nil, &socketError{path: socket, err: err}
		}
		return c, nil
	}
	ctx, cancel := context.WithTimeout(ctx, localTimeout)
	defer cancel()
	return api.NewClient(api.ClientConfig{Dial: dial}).ReloadPolicy(ctx)
}

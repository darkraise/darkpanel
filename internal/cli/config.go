package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Connection settings (spec section 15).
const (
	EnvURL     = "DARKPANEL_URL"
	EnvToken   = "DARKPANEL_TOKEN"
	DefaultURL = "http://127.0.0.1:8080"
)

// fileConfig is the CLI config file, $XDG_CONFIG_HOME/darkpanel/cli.json.
type fileConfig struct {
	URL           string `json:"url"`
	Token         string `json:"token"`
	CAFingerprint string `json:"caFingerprint"`
}

// configPath is $XDG_CONFIG_HOME/darkpanel/cli.json, with ~/.config when
// the variable is unset or relative (the XDG specification ignores relative
// values).
func configPath(getenv func(string) string, home func() (string, error)) (string, error) {
	dir := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(dir) {
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("XDG_CONFIG_HOME is unset and the home directory is unknown: %w", err)
		}
		dir = filepath.Join(h, ".config")
	}
	return filepath.Join(dir, "darkpanel", "cli.json"), nil
}

// maxConfigBytes bounds the config file the CLI reads.
const maxConfigBytes = 1 << 20

// loadConfig reads the config file at path; a missing file is the zero
// config. Because the file holds a token and the URL the token is sent to,
// it must be a regular file, not a symbolic link, owned by the effective
// user, with no group or other permission bits. The checks run on the open
// descriptor, so the file cannot be swapped between check and read.
func loadConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	refuse := func(why string) (fileConfig, error) {
		return cfg, fmt.Errorf("the config file %s %s; run chmod 600 %s", path, why, path)
	}
	// O_NONBLOCK keeps a FIFO planted at path from blocking the open; the
	// regular-file check below then refuses it.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the path is the user's own config file
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return cfg, nil
	case errors.Is(err, syscall.ELOOP):
		return refuse("is a symbolic link; replace it with a regular file")
	case err != nil:
		return cfg, fmt.Errorf("read the config file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return cfg, fmt.Errorf("read the config file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return refuse("is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return refuse("is accessible by group or others")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if euid := os.Geteuid(); !ok || int64(st.Uid) != int64(euid) {
		return refuse(fmt.Sprintf("is not owned by the current user (uid %d)", euid))
	}
	dec := json.NewDecoder(io.LimitReader(f, maxConfigBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("the config file %s: %w", path, err)
	}
	return cfg, nil
}

// connectNetwork returns the client of the network API. The URL comes from
// flagURL, then DARKPANEL_URL, then the config file, then DefaultURL; the
// token from DARKPANEL_TOKEN, then the config file, whose token goes only to
// the config file's server (its URL, or DefaultURL when it names none), and
// stderr gets one line when it is withheld; the CA fingerprint from the
// config file. A token is never sent over plain http to a host that is not
// loopback (spec section 15, register row 91). The config file is read
// whenever it exists.
func connectNetwork(flagURL string, getenv func(string) string, home func() (string, error), stderr io.Writer) (*client, error) {
	var cfg fileConfig
	if path, err := configPath(getenv, home); err == nil {
		if cfg, err = loadConfig(path); err != nil {
			return nil, err
		}
	}
	raw := firstOf(flagURL, getenv(EnvURL), cfg.URL, DefaultURL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, usagef("server URL %q: want http://<host>:<port> or https://<host>:<port>", raw)
	}
	token := getenv(EnvToken)
	if token == "" && cfg.Token != "" {
		if fileServer := firstOf(cfg.URL, DefaultURL); sameServer(u, fileServer) {
			token = cfg.Token
		} else {
			_, _ = fmt.Fprintf(stderr, "darkpanel: the config file's token is sent only to %s, not to %s; set %s to send a token there\n",
				serverOf(fileServer), serverOf(u.String()), EnvToken)
		}
	}
	if token != "" && strings.EqualFold(u.Scheme, "http") && !isLoopbackHost(u.Hostname()) {
		return nil, usagef("refusing to send the API token over plain http to %s; use https, or http to a loopback address (127.0.0.0/8, ::1, or localhost in lowercase)", u.Host)
	}
	hc, err := newHTTPClient(u, cfg.CAFingerprint)
	if err != nil {
		return nil, err
	}
	return &client{base: strings.TrimSuffix(u.String(), "/"), token: token, http: hc}, nil
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// sameServer reports whether u names the server of the URL raw: the same
// scheme, host and port, with the scheme's default port when none is given,
// and the same escaped path but for a trailing slash. The escaped path keeps
// /team%2Fadmin apart from /team/admin.
func sameServer(u *url.URL, raw string) bool {
	f, err := url.Parse(raw)
	return err == nil && strings.EqualFold(u.Scheme, f.Scheme) && strings.EqualFold(hostPort(u), hostPort(f)) &&
		strings.TrimSuffix(u.EscapedPath(), "/") == strings.TrimSuffix(f.EscapedPath(), "/")
}

// hostPort is u's host and port, with the port the scheme implies when u
// names none.
func hostPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "80"
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		}
	}
	return net.JoinHostPort(u.Hostname(), port)
}

// serverOf names the server of the URL raw in a message: its scheme, host
// and port only. Userinfo, path and query are left out because any of them
// can carry a credential.
func serverOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "the configured URL"
	}
	return sanitize(u.Scheme + "://" + u.Host)
}

// isLoopbackHost reports whether host, a URL host without its port, is
// localhost or a loopback IP address. Only the lowercase "localhost" counts:
// Go's proxy exemption for it is case-sensitive, so another spelling may
// still be sent through HTTP_PROXY.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

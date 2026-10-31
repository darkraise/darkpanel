package docker

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) containerInspect() core.ActionDef {
	return core.DefineRead[ContainerInput, ContainerDetail](core.ActionSpec{
		Name: ID + ".container.inspect", Summary: "Show a container's configuration and state.", Version: 1,
		Side: core.SideAgent,
	}, containerInspectHandler{k: k})
}

// containerInspectHandler reads one container. Its answer never carries an
// environment value: those are served only by reveal-env, which is audited.
type containerInspectHandler struct{ k dk }

func (h containerInspectHandler) Read(ctx context.Context, _ core.Env, in ContainerInput) (ContainerDetail, error) {
	if in.Container == "" {
		return ContainerDetail{}, inspectInvalid("/container", "container is required")
	}
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return ContainerDetail{}, engineError("container", in.Container, err)
	}
	return containerDetailOf(c)
}

func (k dk) containerRevealEnv() core.ActionDef {
	return core.DefineReveal[ContainerInput, RevealedEnv](core.ActionSpec{
		Name: ID + ".container.reveal-env", Summary: "Reveal a container's environment values.", Version: 1,
		Side: core.SideAgent, MinScope: core.ScopeAdmin,
	}, func(ctx context.Context, env core.Env, in ContainerInput) (core.Revealed[RevealedEnv], error) {
		if in.Container == "" {
			return core.Revealed[RevealedEnv]{}, inspectInvalid("/container", "container is required")
		}
		c, err := k.d.docker.InspectContainer(ctx, in.Container)
		if err != nil {
			return core.Revealed[RevealedEnv]{}, engineError("container", in.Container, err)
		}
		name := containerName(c)
		// A module's container holds the secrets that module manages, which
		// it alone may hand out.
		if err := refuseOwned("container", name, c.Config.Labels); err != nil {
			return core.Revealed[RevealedEnv]{}, err
		}
		return core.Revealed[RevealedEnv]{
			Identity: "environment of container " + name + ", host " + env.HostID,
			Value:    RevealedEnv{Env: envVars(c.Config.Env, false)},
		}, nil
	})
}

// inspectInvalid refuses an input member at path.
func inspectInvalid(path, message string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: message,
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// envVars splits "NAME=value" entries at their first "="; an entry without
// one is a name with an empty value. With redact, every value is the mask.
func envVars(entries []string, redact bool) []EnvVar {
	vars := make([]EnvVar, 0, len(entries))
	for _, e := range entries {
		name, value, _ := strings.Cut(e, "=")
		if redact {
			value = logfilter.Mask
		}
		vars = append(vars, EnvVar{Name: name, Value: value})
	}
	return vars
}

// secretWord matches a name whose value is a secret: a password, token,
// key or credential, written in any case and with any separator. The short
// words auth, pass and pwd count only as a whole word of the name, so that
// author or compass do not.
var secretWord = regexp.MustCompile(`(?i)pass(word|wd|phrase)|secret|token|api[-_.]?key|credential|basic[-_.]?auth|` +
	`private[-_.]?key|access[-_.]?key|auth[-_.]?key|requirepass|masterauth|(^|[^a-z])(auth|pass|pwd)([^a-z]|$)`)

// secretName reports whether a flag, variable or label named name holds a
// secret. A name ending in file, path or dir holds where a secret is, which
// is shown.
func secretName(name string) bool {
	lower := strings.ToLower(strings.TrimRight(name, "-_."))
	for _, suffix := range []string{"file", "path", "dir"} {
		if strings.HasSuffix(lower, suffix) {
			return false
		}
	}
	return secretWord.MatchString(name)
}

var (
	// urlPassword is the password of a URL's user information, which runs to
	// the last @ before the host, since a password may hold an @.
	urlPassword = regexp.MustCompile(`(://[^/:@\s]*):[^/\s]*@`)
	// pairName is a NAME= or NAME: at the start of a piece of a token, the
	// name possibly quoted.
	pairName = regexp.MustCompile(`^(["']?)(-{0,2}[\w.-]+)(["']?)([=:])`)
	// flagWord is a token that is a flag alone, its value the next one.
	flagWord = regexp.MustCompile(`^-{1,2}[\w.-]+$`)
)

// shortSecretFlags are the one-letter password flags of common programs
// whose names say nothing: mysql's -p<password>, attached, and redis-cli's
// -a <password>.
var shortSecretFlags = map[string]struct {
	flag     string
	attached bool
}{
	"mysql": {"-p", true}, "mysqladmin": {"-p", true}, "mysqldump": {"-p", true},
	"mariadb": {"-p", true}, "mariadb-admin": {"-p", true}, "mariadb-dump": {"-p", true},
	"redis-cli": {"-a", false},
}

const pairSeparators = ",;{}[]()"

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// quotedEnd is the index after the quoted segment starting at s[i], or -1
// when its quote is never closed. A backslash escapes the next byte inside
// double quotes, as in JSON and the shell.
func quotedEnd(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		switch {
		case q == '"' && s[j] == '\\':
			j++
		case s[j] == q:
			return j + 1
		}
	}
	return -1
}

// tokens splits s at whitespace outside quotes. An unclosed quote runs to
// the end of s. seps[i] is the whitespace before toks[i]; tail is the
// whitespace after the last token.
func tokens(s string) (toks, seps []string, tail string) {
	i := 0
	for i < len(s) {
		j := i
		for j < len(s) && isSpace(s[j]) {
			j++
		}
		if j == len(s) {
			return toks, seps, s[i:]
		}
		k := j
		for k < len(s) && !isSpace(s[k]) {
			if s[k] == '"' || s[k] == '\'' {
				if e := quotedEnd(s, k); e >= 0 {
					k = e
					continue
				}
				k = len(s)
				break
			}
			k++
		}
		seps, toks = append(seps, s[i:j]), append(toks, s[j:k])
		i = k
	}
	return toks, seps, ""
}

// maskWhole is the mask in place of a whole value. A value that is one
// closed quoted string, followed by nothing but separators, keeps its
// quotes and separators; anything else becomes the bare mask.
func maskWhole(v string) string {
	if v != "" && (v[0] == '"' || v[0] == '\'') {
		if e := quotedEnd(v, 0); e >= 0 && strings.Trim(v[e:], pairSeparators) == "" {
			return v[:1] + logfilter.Mask + v[:1] + v[e:]
		}
	}
	return logfilter.Mask
}

// redactToken masks the secrets inside one token: a URL password, and the
// value of each NAME=value or NAME: value pair, JSON style pairs included,
// whose name is a secret's. A NAME= value runs to the end of the token. A
// NAME: value that is one closed quoted string is masked alone, so the pairs
// after it are read too; any other is masked to the end of the token.
// maskNext reports a secret NAME: with its value in the next token.
func redactToken(tok string) (out string, maskNext bool) {
	tok = urlPassword.ReplaceAllString(tok, "${1}:"+logfilter.Mask+"@")
	var b strings.Builder
	i := 0
	for i < len(tok) {
		if strings.IndexByte(pairSeparators, tok[i]) >= 0 {
			b.WriteByte(tok[i])
			i++
			continue
		}
		if m := pairName.FindStringSubmatch(tok[i:]); m != nil {
			b.WriteString(m[0])
			i += len(m[0])
			if !secretName(m[2]) {
				continue
			}
			rest := tok[i:]
			switch {
			case rest == "":
				return b.String(), m[4] == ":"
			case m[4] == ":" && (rest[0] == '"' || rest[0] == '\'') && quotedEnd(rest, 0) >= 0:
				e := quotedEnd(rest, 0)
				b.WriteString(rest[:1] + logfilter.Mask + rest[:1])
				i += e
				continue
			}
			b.WriteString(maskWhole(rest))
			return b.String(), false
		}
		if tok[i] == '"' || tok[i] == '\'' {
			e := quotedEnd(tok, i)
			if e < 0 {
				b.WriteString(tok[i:])
				break
			}
			b.WriteString(tok[i:e])
			i = e
			continue
		}
		j := i
		for j < len(tok) && strings.IndexByte(pairSeparators, tok[j]) < 0 && tok[j] != '"' && tok[j] != '\'' {
			j++
		}
		b.WriteString(tok[i:j])
		i = j
	}
	return b.String(), false
}

// redactSeq masks the secrets of a sequence of tokens: the arguments of a
// command, or the tokens of one string. Besides each token's own pairs it
// masks the token after a flag or NAME: named like a secret, unless that
// token is the next long flag, and the short password flags of the programs
// in shortSecretFlags. An argument holding whitespace, or a token that is
// one quoted string, is itself read as a string.
func redactSeq(toks []string, args bool) []string {
	out := make([]string, len(toks))
	maskNext := false
	prev := ""
	var short *struct {
		flag     string
		attached bool
	}
	for i, tok := range toks {
		// A NAME and its = or : may stand apart: "password" : "x".
		if (tok == ":" || tok == "=") && secretName(strings.Trim(prev, `"'`+pairSeparators)) {
			out[i], maskNext, prev = tok, true, tok
			continue
		}
		prev = tok
		if tok == "" {
			continue
		}
		if maskNext {
			maskNext = false
			if !strings.HasPrefix(tok, "--") {
				out[i] = maskWhole(tok)
				continue
			}
		}
		if short != nil {
			switch {
			case short.attached && len(tok) > len(short.flag) && strings.HasPrefix(tok, short.flag):
				out[i] = short.flag + logfilter.Mask
				continue
			case !short.attached && tok == short.flag:
				out[i], maskNext = tok, true
				continue
			}
		}
		if spec, ok := shortSecretFlags[path.Base(strings.Trim(tok, `"'`))]; ok {
			short = &spec
		}
		spaced := strings.ContainsAny(tok, " \t\n\r\v\f")
		if args && spaced {
			out[i] = redactText(tok)
			continue
		}
		if flagWord.MatchString(tok) && secretName(tok) {
			out[i], maskNext = tok, true
			continue
		}
		r, next := redactToken(tok)
		maskNext = next
		if q := tok[0]; r == tok && !next && spaced && (q == '"' || q == '\'') && len(tok) >= 2 && tok[len(tok)-1] == q {
			r = tok[:1] + redactText(tok[1:len(tok)-1]) + tok[:1]
		}
		out[i] = r
	}
	return out
}

// redactText masks the secrets of a string read as whitespace-separated
// tokens, keeping its whitespace.
func redactText(s string) string {
	toks, seps, tail := tokens(s)
	masked := redactSeq(toks, false)
	var b strings.Builder
	for i := range masked {
		b.WriteString(seps[i] + masked[i])
	}
	return b.String() + tail
}

// redactArgs masks the secrets of a command line, which inspect shows to
// every reader (redactSeq).
func redactArgs(args []string) []string {
	if args == nil {
		return nil
	}
	return redactSeq(args, true)
}

// redactLabels masks the values of labels named like a secret and the URL
// passwords and secret NAME=value pairs in the others.
func redactLabels(labels map[string]string) map[string]string {
	if labels == nil {
		return nil
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		if secretName(k) {
			out[k] = logfilter.Mask
			continue
		}
		out[k] = redactText(v)
	}
	return out
}

// containerDetailOf renders an inspected container with its environment
// values and the secrets of its command line and labels redacted.
func containerDetailOf(c dockerapi.ContainerJSON) (ContainerDetail, error) {
	created, err := time.Parse(time.RFC3339Nano, c.Created)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: created: %w", c.ID, err)
	}
	started, err := dstate.Instant(c.State.StartedAt)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: started: %w", c.ID, err)
	}
	finished, err := dstate.Instant(c.State.FinishedAt)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: finished: %w", c.ID, err)
	}
	name := containerName(c)
	d := ContainerDetail{
		ID: c.ID, Name: name, Image: c.Config.Image, ImageID: c.Image, Created: created.UTC(),
		State: c.State.Status, Running: c.State.Running, StartedAt: started, FinishedAt: finished,
		ExitCode: c.State.ExitCode, RestartPolicy: c.HostConfig.RestartPolicy.Name,
		Cmd: redactArgs(c.Config.Cmd), Entrypoint: redactArgs(c.Config.Entrypoint), WorkingDir: c.Config.WorkingDir, User: c.Config.User,
		Env: envVars(c.Config.Env, true), Labels: redactLabels(c.Config.Labels),
		Ports: []Port{}, Mounts: make([]Mount, 0, len(c.Mounts)), Networks: []EndpointView{},
		Project: c.Config.Labels["com.docker.compose.project"], Service: c.Config.Labels["com.docker.compose.service"],
		Owner: ownerOf(c.Config.Labels),
	}
	for key, bindings := range c.NetworkSettings.Ports {
		port, proto, ok := strings.Cut(key, "/")
		private, err := strconv.ParseUint(port, 10, 16)
		if !ok || err != nil {
			continue
		}
		for _, b := range bindings {
			public, err := strconv.ParseUint(b.HostPort, 10, 16)
			if err != nil {
				continue
			}
			d.Ports = append(d.Ports, Port{IP: b.HostIP, PrivatePort: uint16(private), PublicPort: uint16(public), Proto: proto})
		}
	}
	slices.SortFunc(d.Ports, func(a, b Port) int {
		return cmp.Or(cmp.Compare(a.PrivatePort, b.PrivatePort), cmp.Compare(a.Proto, b.Proto),
			cmp.Compare(a.PublicPort, b.PublicPort), cmp.Compare(a.IP, b.IP))
	})
	for _, m := range c.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	for network, ep := range c.NetworkSettings.Networks {
		if ep == nil {
			continue
		}
		v := EndpointView{Network: network, NetworkID: ep.NetworkID, Container: name, EndpointID: ep.EndpointID, Aliases: ep.Aliases}
		if ep.IPAddress.IsValid() {
			v.IPv4 = ep.IPAddress.String()
		}
		if ep.GlobalIPv6Address.IsValid() {
			v.IPv6 = ep.GlobalIPv6Address.String()
		}
		d.Networks = append(d.Networks, v)
	}
	slices.SortFunc(d.Networks, func(a, b EndpointView) int { return cmp.Compare(a.Network, b.Network) })
	return d, nil
}

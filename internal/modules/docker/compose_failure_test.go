package docker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const v1r05Secret = "placeholder-secret"

// v1r05Stderr is a Compose diagnostic whose last line quotes a value that came from an env file.
func v1r05Stderr() []byte {
	return []byte("warning: the TAG variable is not set\nservice \"web\" has an invalid image \"nginx:" + v1r05Secret + "\"\n\n")
}

// v1r05Clean fails the test when the error carries any part of the diagnostics the fake Compose printed.
func v1r05Clean(t *testing.T, err error) {
	t.Helper()
	text := err.Error() + " " + errcode.From(err).Detail
	for _, leaked := range []string{v1r05Secret, "invalid image", "TAG variable", "ZZZZ"} {
		if strings.Contains(text, leaked) {
			t.Errorf("error %q carries %q from Compose's stderr", text, leaked)
		}
	}
}

func TestV1r05FailedConfigReportsOnlyTheExitStatus(t *testing.T) {
	tests := []struct {
		name   string
		code   int
		stderr []byte
	}{
		{"stderr quotes a secret", 1, v1r05Stderr()},
		{"long stderr line", 15, []byte(strings.Repeat("Z", 500) + v1r05Secret + "\n")},
		{"no stderr", 2, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			p := ccProject(t, h, "shop")
			h.results[strings.Join(composeArgs(p, nil, "config", "--format", "json"), " ")] = core.CmdResult{ExitCode: tc.code, Stderr: tc.stderr}
			_, err := ccPrepare(h, "shop", nil)
			ccRefused(t, err, errcode.DockerComposeUnavailable)
			want := fmt.Sprintf(`docker compose config failed with exit status %d; run "docker compose -f /srv/shop/compose.yaml config" on the host to see why`, tc.code)
			if got := errcode.From(err).Detail; got != want {
				t.Errorf("detail %q, want %q", got, want)
			}
			v1r05Clean(t, err)
		})
	}
}

func TestV1r05FailedRunReportsOnlyTheVerbAndStatus(t *testing.T) {
	tests := []struct {
		verb   string
		def    func(dk) core.ActionDef
		args   []string
		code   int
		stderr []byte
	}{
		{"up", dk.composeUp, []string{"up", "-d", "--no-build"}, 1, v1r05Stderr()},
		{"down", dk.composeDown, []string{"down"}, 2, v1r05Stderr()},
		{"pull", dk.composePull, []string{"pull"}, 3, []byte(strings.Repeat("Z", 500) + v1r05Secret)},
		{"restart", dk.composeRestart, []string{"restart"}, 4, nil},
	}
	for _, tc := range tests {
		t.Run(tc.verb, func(t *testing.T) {
			h := newDKHost(t)
			p := cuProject(t, h)
			before := cuRow(t, h)
			h.results[cuKey(composeArgs(p, p.Profiles, tc.args...))] = core.CmdResult{ExitCode: tc.code, Stderr: tc.stderr}
			_, err := cuApply(t, h, tc.def(h.dk()), cuInput)
			want := fmt.Sprintf(`docker compose %s failed with exit status %d; run "docker compose -f /srv/shop/compose.yaml %s" on the host to see why`,
				tc.verb, tc.code, tc.verb)
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.DockerComposeUnavailable || coded.Detail != want {
				t.Fatalf("Apply = %v, want docker.compose_unavailable %q", err, want)
			}
			if core.IsDefinite(err) {
				t.Error("a failed run is marked definite, but the run may have changed containers")
			}
			v1r05Clean(t, err)
			if after := cuRow(t, h); after.RunHash != before.RunHash || !after.LastRun.Equal(before.LastRun) {
				t.Errorf("run recorded after a failure: %+v, was %+v", after, before)
			}
		})
	}
}

func TestV1r05FailureDetailRepeatsTheFlagForEveryComposeFile(t *testing.T) {
	p := projectRow{Name: "shop", Files: []string{"/srv/shop/compose.yaml", "/srv/shop/override.yaml"}}
	got := composeFailureDetail("up", p, 3)
	want := `docker compose up failed with exit status 3; run "docker compose -f /srv/shop/compose.yaml -f /srv/shop/override.yaml up" on the host to see why`
	if got != want {
		t.Errorf("composeFailureDetail = %q, want %q", got, want)
	}
}

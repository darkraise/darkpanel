package docker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r14Secret = "placeholder-secret"

// v1r14Key is the key effect documents fingerprint label values under on the test host.
func v1r14Key() []byte { return coretest.Sealer{}.Derive("docker.effect.fingerprint/v1") }

// v1r14Fingerprint is how a label value appears in an effect document.
func v1r14Fingerprint(value string) string {
	m := hmac.New(sha256.New, v1r14Key())
	m.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

// v1r14Unsalted is the digest effect documents used to hold, which a guessed value confirms offline.
func v1r14Unsalted(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// v1r14CheckDoc requires the document to hold the keyed fingerprint of the secret under the key token, and no
// trace of the value or of its unsalted digest.
func v1r14CheckDoc(t *testing.T, doc json.RawMessage) {
	t.Helper()
	text := string(doc)
	if strings.Contains(text, v1r14Secret) {
		t.Errorf("the document %s holds the label value", text)
	}
	if strings.Contains(text, v1r14Unsalted(v1r14Secret)) {
		t.Errorf("the document %s holds the unsalted digest of the label value", text)
	}
	var d struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"token": v1r14Fingerprint(v1r14Secret)}; !reflect.DeepEqual(d.Labels, want) {
		t.Errorf("labels %v, want %v", d.Labels, want)
	}
}

// TestV1r14PlanDiffsShowLabelKeysOnly: a plan's diff names each label it adds and never its value, and the plan's
// state hash is not the unkeyed SHA-256 of a request that holds the values.
func TestV1r14PlanDiffsShowLabelKeysOnly(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	tests := []struct {
		name  string
		def   core.ActionDef
		input func(token string) string
		diff  []core.DiffLine
		plain func(token string) (string, error)
	}{
		{
			name: "network", def: h.dk().networkCreate(),
			input: func(token string) string {
				return `{"name":"app","labels":{"team":"billing-placeholder","token":"` + token + `"}}`
			},
			diff: []core.DiffLine{
				{Op: "+", Text: "network app"}, {Op: " ", Text: "driver bridge"}, {Op: "+", Text: "label team"}, {Op: "+", Text: "label token"},
			},
			plain: func(token string) (string, error) {
				return manageHash(dockerapi.NetworkCreate{Name: "app", Driver: "bridge", Labels: map[string]string{"team": "billing-placeholder", "token": token}})
			},
		},
		{
			name: "volume", def: h.dk().volumeCreate(),
			input: func(token string) string {
				return `{"name":"data","labels":{"team":"billing-placeholder","token":"` + token + `"}}`
			},
			diff: []core.DiffLine{
				{Op: "+", Text: "volume data"}, {Op: " ", Text: "driver local"}, {Op: "+", Text: "label team"}, {Op: "+", Text: "label token"},
			},
			plain: func(token string) (string, error) {
				return manageHash(dockerapi.VolumeCreate{Name: "data", Driver: "local", Labels: map[string]string{"team": "billing-placeholder", "token": token}})
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := func(token string) core.PlanOutcome {
				t.Helper()
				out, err := tc.def.Handler.Plan(ctx, h.env(""), json.RawMessage(tc.input(token)))
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			out := plan(v1r14Secret)
			if !slices.Equal(out.Diff, tc.diff) {
				t.Errorf("diff %v, want %v", out.Diff, tc.diff)
			}
			for _, l := range out.Diff {
				if strings.Contains(l.Text, v1r14Secret) || strings.Contains(l.Text, "billing-placeholder") {
					t.Errorf("diff line %q shows a label value", l.Text)
				}
			}
			plain, err := tc.plain(v1r14Secret)
			if err != nil {
				t.Fatal(err)
			}
			if out.StateHash == plain {
				t.Error("the state hash is the unkeyed SHA-256 of the request, which a guessed label value confirms")
			}
			if other := plan("another-placeholder"); other.StateHash == out.StateHash {
				t.Error("a changed label value leaves the state hash unchanged")
			}
			if again := plan(v1r14Secret); again.StateHash != out.StateHash {
				t.Errorf("one input hashes as %s and as %s", out.StateHash, again.StateHash)
			}
		})
	}
}

// TestV1r14LabelInputsAreRedacted: the input the engine records, and core.audit.list reads back, holds no label
// value.
func TestV1r14LabelInputsAreRedacted(t *testing.T) {
	tests := map[string]struct {
		typ   reflect.Type
		input string
		name  string
	}{
		"network": {reflect.TypeFor[NetworkCreateInput](), `{"name":"app","labels":{"token":"` + v1r14Secret + `"}}`, `"name":"app"`},
		"volume":  {reflect.TypeFor[VolumeCreateInput](), `{"name":"data","labels":{"token":"` + v1r14Secret + `"}}`, `"name":"data"`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got := string(schema.Redact(tc.typ, json.RawMessage(tc.input)))
			if strings.Contains(got, v1r14Secret) {
				t.Errorf("the recorded input %s holds the label value", got)
			}
			if !strings.Contains(got, tc.name) {
				t.Errorf("the recorded input %s lost %s, which is no secret", got, tc.name)
			}
		})
	}
}

// TestV1r14EffectsHoldKeyedFingerprints: the effect documents of the four label-bearing effects hold the keyed
// fingerprint of a label value, an observation of the made effect agrees with its recorded post-state, and neither
// carries the value or its unsalted digest.
func TestV1r14EffectsHoldKeyedFingerprints(t *testing.T) {
	ctx := context.Background()
	withLabel := json.RawMessage(`"labels":{"token":"` + v1r14Secret + `"}`)
	for _, tc := range []struct {
		name, object string
		def          func(dk) core.ActionDef
	}{
		{"network create", `"name":"app"`, func(d dk) core.ActionDef { return d.networkCreate() }},
		{"volume create", `"name":"data"`, func(d dk) core.ActionDef { return d.volumeCreate() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newDKHost(t)
			def := tc.def(h.dk())
			if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{`+tc.object+`,`+string(withLabel)+`}`)); err != nil {
				t.Fatal(err)
			}
			e := h.journal.effects()[0]
			v1r14CheckDoc(t, e.Post.State)
			obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
			if err != nil {
				t.Fatal(err)
			}
			if string(obs.Current.State) != string(e.Post.State) {
				t.Errorf("observation %s differs from the recorded post-state %s", obs.Current.State, e.Post.State)
			}
		})
	}
	t.Run("network remove", func(t *testing.T) {
		h := newDKHost(t)
		old := dkNetwork("old", func(n *dockerapi.Network) { n.Labels = map[string]string{"token": v1r14Secret} })
		h.docker.putNetwork(old)
		if _, err := h.dk().networkRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"network":"`+old.ID+`"}`)); err != nil {
			t.Fatal(err)
		}
		v1r14CheckDoc(t, h.journal.effects()[0].Before.State)
	})
	t.Run("volume remove", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("old", map[string]string{"token": v1r14Secret}))
		if _, err := h.dk().volumeRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"volume":"old"}`)); err != nil {
			t.Fatal(err)
		}
		v1r14CheckDoc(t, h.journal.effects()[0].Before.State)
	})
}

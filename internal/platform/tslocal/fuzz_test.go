package tslocal

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// fz5Head returns at most the first 64 bytes of s, for a failure message.
func fz5Head(s string) string { return s[:min(len(s), 64)] }

// fz5Modelled is p without the members kept as read, which are not modelled.
func fz5Modelled(p Prefs) Prefs {
	p.raw = nil
	return p
}

// TestFz5PrefsCaseVariantKeys: tailscaled's decoder matches member names
// without regard to case and lets the later one win, so a document holding
// two spellings of a modelled member decodes to the same value after it is
// encoded and decoded again.
func TestFz5PrefsCaseVariantKeys(t *testing.T) {
	var first Prefs
	if err := json.Unmarshal([]byte(`{"hostname":"a","Hostname":"b"}`), &first); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	var second Prefs
	if err := json.Unmarshal(out, &second); err != nil {
		t.Fatal(err)
	}
	if first.Hostname != "b" || second.Hostname != first.Hostname {
		t.Fatalf("Hostname %q, then %q after %s", first.Hostname, second.Hostname, out)
	}
}

// FuzzPrefsJSON feeds arbitrary JSON to Prefs.UnmarshalJSON: input it accepts
// marshals, or is refused only for naming an exit node by both ID and
// address, and the marshalled form unmarshals again to the same modelled
// fields (R74).
func FuzzPrefsJSON(f *testing.F) {
	f.Add(`{"ControlURL":"https://controlplane.tailscale.com","RouteAll":false,"ExitNodeID":"","ExitNodeIP":"",` +
		`"CorpDNS":true,"RunSSH":false,"WantRunning":false,"LoggedOut":true,"ShieldsUp":false,"AdvertiseTags":null,` +
		`"Hostname":"","AdvertiseRoutes":null,"NetfilterMode":2,"NoSNAT":false,"AutoUpdate":{"Check":true,"Apply":null},"Config":null}`)
	f.Add(`{"ExitNodeIP":"100.64.0.1","AdvertiseRoutes":["10.0.0.0/24","0.0.0.0/0"],"AdvertiseTags":["tag:web"]}`)
	f.Add(`{"ExitNodeID":"n1","ExitNodeIP":"100.64.0.1"}`)
	f.Add(`{"hostname":"a","Hostname":"b"}`)
	f.Add(`{"Hostname":"a","Hostname":"b"}`)
	f.Add(`{}`)
	f.Add(`null`)
	f.Add(`[]`)
	f.Fuzz(func(t *testing.T, in string) {
		var first Prefs
		if err := json.Unmarshal([]byte(in), &first); err != nil {
			return
		}
		out, err := json.Marshal(first)
		if err != nil {
			if !errors.Is(err, errExitNodeBoth) || first.ExitNodeID == "" || !first.ExitNodeIP.IsValid() {
				t.Fatalf("accepted prefs %q do not marshal: %v", fz5Head(in), err)
			}
			return
		}
		var second Prefs
		if err := json.Unmarshal(out, &second); err != nil {
			t.Fatalf("marshalled prefs of %q do not unmarshal: %v", fz5Head(in), err)
		}
		if !reflect.DeepEqual(fz5Modelled(first), fz5Modelled(second)) {
			t.Fatalf("modelled fields of %q changed over a round trip: %q", fz5Head(in), fz5Head(string(out)))
		}
	})
}

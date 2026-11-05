package ingress

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// fz8Head returns at most the first 64 bytes of b, for a failure message.
func fz8Head(b []byte) []byte { return b[:min(len(b), 64)] }

// fz8CheckConfig fails unless c, read from in, reaches a fixed point after one
// write and read: that write and every later one give the same bytes, the
// second read equals the first, and Hash does not change.
func fz8CheckConfig(t *testing.T, in []byte, c Config) {
	t.Helper()
	first, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("a configuration read from %q does not marshal: %v", fz8Head(in), err)
	}
	var again Config
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("the marshalled configuration of %q does not unmarshal: %v", fz8Head(in), err)
	}
	second, err := again.MarshalJSON()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("the configuration of %q changes between two writes: %v", fz8Head(in), err)
	}
	var third Config
	if err := json.Unmarshal(second, &third); err != nil || !reflect.DeepEqual(again, third) {
		t.Fatalf("the configuration of %q changes between two reads: %v", fz8Head(in), err)
	}
	if h := Hash(c); h != Hash(c) || h != Hash(again) {
		t.Fatalf("Hash of the configuration of %q is not stable", fz8Head(in))
	}
}

// fz8CheckRule is fz8CheckConfig for one rule.
func fz8CheckRule(t *testing.T, in []byte, r Rule) {
	t.Helper()
	first, err := r.MarshalJSON()
	if err != nil {
		t.Fatalf("a rule read from %q does not marshal: %v", fz8Head(in), err)
	}
	var again Rule
	if err := json.Unmarshal(first, &again); err != nil {
		t.Fatalf("the marshalled rule of %q does not unmarshal: %v", fz8Head(in), err)
	}
	second, err := again.MarshalJSON()
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("the rule of %q changes between two writes: %v", fz8Head(in), err)
	}
	var third Rule
	if err := json.Unmarshal(second, &third); err != nil || !reflect.DeepEqual(again, third) {
		t.Fatalf("the rule of %q changes between two reads: %v", fz8Head(in), err)
	}
}

// FuzzConfigJSON feeds arbitrary JSON to Config.UnmarshalJSON and to
// Rule.UnmarshalJSON. Neither panics. A document either accepts reaches a
// fixed point after one write and read: the members it does not model keep
// their content but not their white space, so the first read is compared with
// the second and not with itself. Hash gives the same text for a
// configuration and for the one read back from its marshalled form (R74).
func FuzzConfigJSON(f *testing.F) {
	f.Add([]byte(`{"ingress":[` +
		`{"hostname":"a.example.com","originRequest":{"connectTimeout":30,"httpHostHeader":"in<&>.example","noTLSVerify":true},"service":"https://localhost:8443","x-extra":{"k":[1,2.50,"éé"]}},` +
		`{"service":"http_status:404"}],"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`))
	f.Add([]byte(`{"ingress":[` + inA + `,` + inEnd + `]}`))
	f.Add([]byte(`{ "ingress" : [ { "service" : "http://a:80" , "x" : { "k" : 1 } } ] , "y" : [ 1 , 2 ] }`))
	f.Add([]byte(inA))
	f.Add([]byte(`{"originRequest":null,"service":"s"}`))
	f.Add([]byte(`{"originRequest":{"noTLSVerify":null,"a":1},"service":"s"}`))
	f.Add([]byte(`{"ingress":[null]}`))
	f.Add([]byte(`{"ingress":null}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, in []byte) {
		var c Config
		if err := json.Unmarshal(in, &c); err == nil {
			fz8CheckConfig(t, in, c)
		}
		var r Rule
		if err := json.Unmarshal(in, &r); err == nil {
			fz8CheckRule(t, in, r)
		}
	})
}

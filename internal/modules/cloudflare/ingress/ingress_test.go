package ingress

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func inRead(t *testing.T, s string) Config {
	t.Helper()
	var c Config
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		t.Fatalf("unmarshal %s: %v", s, err)
	}
	return c
}

func inJSON(t *testing.T, c Config) string {
	t.Helper()
	b, err := c.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

const (
	inA   = `{"hostname":"a.example.com","service":"http://a:80"}`
	inB   = `{"hostname":"b.example.com","service":"http://b:80"}`
	inEnd = `{"service":"http_status:404"}`
)

func TestInRoundTripKeepsUnnamedMembers(t *testing.T) {
	src := `{"ingress":[` +
		`{"hostname":"a.example.com","originRequest":{"connectTimeout":30,"httpHostHeader":"in<&>.example","noTLSVerify":true},"service":"https://localhost:8443","x-extra":{"k":[1,2.50,"éé"]}},` +
		`{"service":"http_status:404"}],` +
		`"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`
	c := inRead(t, src)
	if len(c.Ingress) != 2 {
		t.Fatalf("%d rules, want 2", len(c.Ingress))
	}
	r := c.Ingress[0]
	if r.Hostname != "a.example.com" || r.Service != "https://localhost:8443" || r.Path != "" || !r.NoTLSVerify {
		t.Errorf("rule %+v", r)
	}
	if got := inJSON(t, c); got != src {
		t.Errorf("round trip\n got %s\nwant %s", got, src)
	}
}

func TestInRuleJSON(t *testing.T) {
	b, err := json.Marshal(Rule{Hostname: "a.example.com", Path: "^/x", Service: "http://a:80", NoTLSVerify: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hostname":"a.example.com","originRequest":{"noTLSVerify":true},"path":"^/x","service":"http://a:80"}`
	if string(b) != want {
		t.Errorf("got %s, want %s", b, want)
	}
	var r Rule
	if err := json.Unmarshal([]byte(inEnd), &r); err != nil {
		t.Fatal(err)
	}
	if r.Hostname != "" || r.Service != CatchAll || r.NoTLSVerify {
		t.Errorf("catch-all %+v", r)
	}
	b, err = json.Marshal(Rule{Service: "http://a:80"})
	if err != nil || string(b) != `{"service":"http://a:80"}` {
		t.Errorf("plain rule %s %v", b, err)
	}
}

func TestInEmptyConfigs(t *testing.T) {
	for _, src := range []string{`null`, `{}`, `{"ingress":null}`, `{"ingress":[]}`} {
		c := inRead(t, src)
		if got := inJSON(t, c); got != `{}` {
			t.Errorf("%s marshals as %s, want {}", src, got)
		}
		if got := Hash(c); got != Hash(Config{}) {
			t.Errorf("%s hashes as %s, want the empty configuration's", src, got)
		}
	}
	if got := Hash(Config{}); got != "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" {
		t.Errorf("empty hash %s", got)
	}
}

func TestInUnmarshalErrors(t *testing.T) {
	for _, src := range []string{
		`[]`,
		`"ingress"`,
		`{"ingress":{}}`,
		`{"ingress":[1]}`,
		`{"ingress":[{"hostname":5}]}`,
		`{"ingress":[{"service":["a"]}]}`,
		`{"ingress":[{"originRequest":[]}]}`,
		`{"ingress":[{"originRequest":{"noTLSVerify":"yes"}}]}`,
	} {
		var c Config
		err := json.Unmarshal([]byte(src), &c)
		if err == nil {
			t.Errorf("%s: no error", src)
			continue
		}
		if !strings.HasPrefix(err.Error(), "ingress: ") || strings.Contains(err.Error(), "ingress: ingress:") {
			t.Errorf("%s: error %q", src, err)
		}
	}
}

func TestInAdd(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		add     Rule
		want    string
		changed bool
	}{
		{"before the catch-all", `{"ingress":[` + inA + `,` + inEnd + `]}`, Rule{Hostname: "b.example.com", Service: "http://b:80"},
			`{"ingress":[` + inA + `,` + inB + `,` + inEnd + `]}`, true},
		{"catch-all added to an empty configuration", `{}`, Rule{Hostname: "b.example.com", Service: "http://b:80"},
			`{"ingress":[` + inB + `,` + inEnd + `]}`, true},
		{"catch-all added after the existing rules", `{"ingress":[` + inA + `]}`, Rule{Hostname: "b.example.com", Service: "http://b:80"},
			`{"ingress":[` + inA + `,` + inB + `,` + inEnd + `]}`, true},
		{"an operator's own last rule counts as the catch-all", `{"ingress":[` + inA + `,{"service":"http_status:503"}]}`, Rule{Hostname: "b.example.com", Service: "http://b:80"},
			`{"ingress":[` + inA + `,` + inB + `,{"service":"http_status:503"}]}`, true},
		{"equal rule already present", `{"ingress":[` + inA + `,` + inEnd + `]}`, Rule{Hostname: "a.example.com", Service: "http://a:80"},
			`{"ingress":[` + inA + `,` + inEnd + `]}`, false},
		{"hostnames compare without case", `{"ingress":[` + inA + `,` + inEnd + `]}`, Rule{Hostname: "A.Example.COM", Service: "http://a:80"},
			`{"ingress":[` + inA + `,` + inEnd + `]}`, false},
		{"noTLSVerify is written", `{"ingress":[` + inEnd + `]}`, Rule{Hostname: "b.example.com", Service: "https://b:443", NoTLSVerify: true},
			`{"ingress":[{"hostname":"b.example.com","originRequest":{"noTLSVerify":true},"service":"https://b:443"},` + inEnd + `]}`, true},
		{"other members and rules are carried over", `{"originRequest":{"connectTimeout":10},"ingress":[{"hostname":"a.example.com","originRequest":{"connectTimeout":5},"service":"http://a:80","x":[1,2]},` + inEnd + `]}`,
			Rule{Hostname: "b.example.com", Service: "http://b:80"},
			`{"ingress":[{"hostname":"a.example.com","originRequest":{"connectTimeout":5},"service":"http://a:80","x":[1,2]},` + inB + `,` + inEnd + `],"originRequest":{"connectTimeout":10}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := inRead(t, tc.src)
			in.Ingress = append(make([]Rule, 0, 16), in.Ingress...)
			before := inJSON(t, in)
			out, changed, err := Add(in, tc.add)
			if err != nil {
				t.Fatal(err)
			}
			if changed != tc.changed {
				t.Errorf("changed %v, want %v", changed, tc.changed)
			}
			if got := inJSON(t, out); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if after := inJSON(t, in); after != before {
				t.Errorf("Add changed its argument: %s, was %s", after, before)
			}
		})
	}
}

func TestInAddRefusals(t *testing.T) {
	c := inRead(t, `{"ingress":[`+inA+`,`+inEnd+`]}`)
	cases := []struct {
		name string
		add  Rule
		want string
	}{
		{"another service", Rule{Hostname: "a.example.com", Service: "http://other:80"}, "a.example.com"},
		{"another noTLSVerify", Rule{Hostname: "a.example.com", Service: "http://a:80", NoTLSVerify: true}, "a.example.com"},
		{"another path", Rule{Hostname: "a.example.com", Path: "^/x", Service: "http://a:80"}, "a.example.com"},
		{"no hostname", Rule{Service: "http://a:80"}, "hostname"},
		{"no service", Rule{Hostname: "c.example.com"}, "service"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed, err := Add(c, tc.add)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.HasPrefix(err.Error(), "ingress: ") || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q", err)
			}
			if changed || len(out.Ingress) != 0 {
				t.Errorf("a refusal returned changed=%v and %d rules", changed, len(out.Ingress))
			}
		})
	}
}

func TestInRemove(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		hostname string
		want     string
		changed  bool
	}{
		{"drops the rule", `{"ingress":[` + inA + `,` + inB + `,` + inEnd + `]}`, "b.example.com", `{"ingress":[` + inA + `,` + inEnd + `]}`, true},
		{"without case", `{"ingress":[` + inA + `,` + inB + `,` + inEnd + `]}`, "B.EXAMPLE.com", `{"ingress":[` + inA + `,` + inEnd + `]}`, true},
		{"every rule of the hostname", `{"ingress":[` + inB + `,{"hostname":"b.example.com","path":"^/x","service":"http://x:80"},` + inEnd + `]}`, "b.example.com", `{"ingress":[` + inEnd + `]}`, true},
		{"absent hostname", `{"ingress":[` + inA + `,` + inEnd + `]}`, "b.example.com", `{"ingress":[` + inA + `,` + inEnd + `]}`, false},
		{"the catch-all is never dropped", `{"ingress":[` + inA + `,` + inEnd + `]}`, "", `{"ingress":[` + inA + `,` + inEnd + `]}`, false},
		{"unrelated rules and members are carried over", `{"warp-routing":{"enabled":true},"ingress":[{"hostname":"a.example.com","originRequest":{"connectTimeout":5},"service":"http://a:80","x":[1,2]},` + inB + `,` + inEnd + `]}`, "b.example.com",
			`{"ingress":[{"hostname":"a.example.com","originRequest":{"connectTimeout":5},"service":"http://a:80","x":[1,2]},` + inEnd + `],"warp-routing":{"enabled":true}}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := inRead(t, tc.src)
			in.Ingress = append(make([]Rule, 0, 16), in.Ingress...)
			before := inJSON(t, in)
			out, changed := Remove(in, tc.hostname)
			if changed != tc.changed {
				t.Errorf("changed %v, want %v", changed, tc.changed)
			}
			if got := inJSON(t, out); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
			if after := inJSON(t, in); after != before {
				t.Errorf("Remove changed its argument: %s, was %s", after, before)
			}
		})
	}
}

func TestInLookup(t *testing.T) {
	c := inRead(t, `{"ingress":[`+inA+`,{"hostname":"b.example.com","originRequest":{"noTLSVerify":true},"path":"^/x","service":"https://b:443"},`+inEnd+`]}`)
	r, ok := Lookup(c, "B.example.com")
	if !ok || r.Hostname != "b.example.com" || r.Path != "^/x" || r.Service != "https://b:443" || !r.NoTLSVerify {
		t.Errorf("Lookup b: %+v %v", r, ok)
	}
	if _, ok := Lookup(c, "c.example.com"); ok {
		t.Error("Lookup found an absent hostname")
	}
	if _, ok := Lookup(c, ""); ok {
		t.Error("Lookup found the catch-all by an empty hostname")
	}
}

func TestInHash(t *testing.T) {
	base := `{"ingress":[` + inA + `,` + inEnd + `],"warp-routing":{"enabled":true,"x":1}}`
	h := Hash(inRead(t, base))
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(h) {
		t.Fatalf("hash %q is not 64 lowercase hex digits", h)
	}
	if got := Hash(inRead(t, `{"ingress":[{"service":"http_status:404"}]}`)); got != "d66fa8eea80c1402568e0afdc5007526058575873a20166b8da9a66c8e77c63a" {
		t.Errorf("hash of the bare catch-all %s", got)
	}
	same := map[string]string{
		"member order in an unnamed member": `{"ingress":[` + inA + `,` + inEnd + `],"warp-routing":{"x":1,"enabled":true}}`,
		"top-level member order":            `{"warp-routing":{"enabled":true,"x":1},"ingress":[` + inA + `,` + inEnd + `]}`,
		"rule member order":                 `{"ingress":[{"service":"http://a:80","hostname":"a.example.com"},{"service":"http_status:404"}],"warp-routing":{"enabled":true,"x":1}}`,
		"whitespace":                        "{\n \"ingress\": [ " + inA + ", " + inEnd + " ],\n \"warp-routing\": { \"enabled\": true, \"x\": 1 }\n}",
	}
	for name, src := range same {
		if got := Hash(inRead(t, src)); got != h {
			t.Errorf("%s changes the hash", name)
		}
	}
	plain := Hash(inRead(t, `{"ingress":[`+inA+`]}`))
	if got := Hash(inRead(t, `{"ingress":[{"hostname":"a.example.com","originRequest":{"noTLSVerify":false},"service":"http://a:80"}]}`)); got != plain {
		t.Error("an explicit noTLSVerify false changes the hash")
	}
	differs := map[string]string{
		"a changed service":        `{"ingress":[{"hostname":"a.example.com","service":"http://a:81"},` + inEnd + `],"warp-routing":{"enabled":true,"x":1}}`,
		"noTLSVerify true":         `{"ingress":[{"hostname":"a.example.com","originRequest":{"noTLSVerify":true},"service":"http://a:80"},` + inEnd + `],"warp-routing":{"enabled":true,"x":1}}`,
		"a changed unnamed member": `{"ingress":[` + inA + `,` + inEnd + `],"warp-routing":{"enabled":false,"x":1}}`,
		"rule order":               `{"ingress":[` + inEnd + `,` + inA + `],"warp-routing":{"enabled":true,"x":1}}`,
		"an added rule":            `{"ingress":[` + inA + `,` + inB + `,` + inEnd + `],"warp-routing":{"enabled":true,"x":1}}`,
	}
	for name, src := range differs {
		if got := Hash(inRead(t, src)); got == h {
			t.Errorf("%s leaves the hash unchanged", name)
		}
	}
}

package dispatch

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/schemas"
)

func a16Snapshot(extraProperty bool) []byte {
	props := `"name":{"type":"string"}`
	if extraProperty {
		props += `,"value":{"type":"string"}`
	}
	return []byte(`{"name":"demo.thing.set","version":1,"class":"safe","side":"agent","breakGlass":false,` +
		`"input":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{` + props + `},"required":["name"]},` +
		`"output":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}}`)
}

func a16Validate(t *testing.T, sch *jsonschema.Schema, doc string) error {
	t.Helper()
	v, err := jsonschema.UnmarshalJSON(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return sch.Validate(v)
}

func TestA16_IdenticalSnapshotsOfManyReleasesCompileOnce(t *testing.T) {
	reg, err := core.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	plain, withValue := a16Snapshot(false), a16Snapshot(true)
	ref := core.ActionRef{Name: "demo.thing.set", Version: 1}
	key := schemas.HashKey(ref)
	plainHash, err := schemas.SnapshotHashes(plain)
	if err != nil {
		t.Fatal(err)
	}
	valueHash, err := schemas.SnapshotHashes(withValue)
	if err != nil {
		t.Fatal(err)
	}
	if plainHash == valueHash {
		t.Fatal("the fixture's two snapshots must differ in hash")
	}

	released := fstest.MapFS{}
	for _, tag := range []string{"v1.2.0", "v1.3.0", "v1.4.0", "v1.6.0"} {
		released["released/"+tag+"/demo.thing.set/v1.json"] = &fstest.MapFile{Data: plain}
	}
	released["released/v1.5.0/demo.thing.set/v1.json"] = &fstest.MapFile{Data: withValue}

	compiled := 0
	orig := compileSnapshot
	compileSnapshot = func(url string, doc any) (*jsonschema.Schema, error) {
		compiled++
		return orig(url, doc)
	}
	t.Cleanup(func() { compileSnapshot = orig })

	n, err := newNegotiation(reg, released)
	if err != nil {
		t.Fatal(err)
	}
	if compiled != 2 {
		t.Errorf("compiled %d snapshots, want 2: the two distinct (action, hash) pairs once each", compiled)
	}
	if len(n.released[key]) != 2 || n.released[key][plainHash] == nil || n.released[key][valueHash] == nil {
		t.Fatalf("released[%s] holds %d hashes, want the plain and the with-value snapshot", key, len(n.released[key]))
	}

	kept := n.released[key][plainHash]
	if err := n.add(released, "released/v1.3.0/demo.thing.set/v1.json", ref); err != nil {
		t.Fatal(err)
	}
	if n.released[key][plainHash] != kept {
		t.Error("adding an identical snapshot again replaced the compiled schema")
	}
	if compiled != 2 {
		t.Errorf("adding an identical snapshot again compiled it: %d compiles, want 2", compiled)
	}

	plainSchema, valueSchema := n.released[key][plainHash], n.released[key][valueHash]
	for _, c := range []struct {
		name   string
		sch    *jsonschema.Schema
		doc    string
		wantOK bool
	}{
		{"plain accepts its field", plainSchema, `{"name":"a"}`, true},
		{"plain refuses a missing required field", plainSchema, `{}`, false},
		{"plain refuses a wrong type", plainSchema, `{"name":5}`, false},
		{"plain refuses a field it lacks", plainSchema, `{"name":"a","value":"b"}`, false},
		{"with-value accepts both fields", valueSchema, `{"name":"a","value":"b"}`, true},
		{"with-value accepts the plain input", valueSchema, `{"name":"a"}`, true},
		{"with-value refuses an unknown field", valueSchema, `{"name":"a","other":"b"}`, false},
	} {
		if err := a16Validate(t, c.sch, c.doc); (err == nil) != c.wantOK {
			t.Errorf("%s: validate %s = %v, want ok=%v", c.name, c.doc, err, c.wantOK)
		}
	}
}

func TestA16_ABrokenSnapshotAmongIdenticalOnesStillFails(t *testing.T) {
	reg, err := core.NewRegistry()
	if err != nil {
		t.Fatal(err)
	}
	released := fstest.MapFS{
		"released/v1.2.0/demo.thing.set/v1.json": &fstest.MapFile{Data: a16Snapshot(false)},
		"released/v1.3.0/demo.thing.set/v1.json": &fstest.MapFile{Data: []byte(`{"input": not json`)},
	}
	if _, err := newNegotiation(reg, released); err == nil || !strings.Contains(err.Error(), "v1.3.0") {
		t.Errorf("newNegotiation = %v, want an error naming the broken snapshot of v1.3.0", err)
	}
}

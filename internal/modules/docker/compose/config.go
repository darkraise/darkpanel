// Package compose reads the resolved configuration of a Compose project, the
// output of `docker compose config --format json`, and holds the refusals
// Darkpanel applies to it (spec 3, "Compose").
package compose

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

// MaxConfigBytes is the largest configuration Parse accepts.
const MaxConfigBytes = 4 << 20

// Config is the part of a resolved Compose configuration that Darkpanel reads.
type Config struct {
	Name     string
	TopKeys  []string           // every top-level key, sorted
	Services map[string]Service // by service name
	Networks map[string]Object  // top-level networks, volumes, configs, secrets by key
	Volumes  map[string]Object
	Configs  map[string]Object
	Secrets  map[string]Object
}

// Service is one service of a Config.
type Service struct {
	Keys          []string // every key of the service, sorted
	Image         string
	Build         bool // a build member is present
	Provider      bool // a provider member is present
	ContainerName string
	Networks      []string // network keys the service joins, sorted
	Volumes       []string // named-volume sources of its volume mounts, sorted
	Labels        map[string]string
	// ContainerRefs are the containers the service names through
	// network_mode, pid or ipc ("container:<name>"), volumes_from
	// ("container:<name>[:ro|:rw]") and external_links ("<name>[:<alias>]"),
	// sorted and distinct.
	ContainerRefs []string
}

// Object is one top-level network, volume, config or secret of a Config.
type Object struct {
	Keys     []string // every key of the object, sorted
	Name     string   // the resolved "name" member, "" when absent
	External bool
	Labels   map[string]string
}

// ImageName is the image a service runs: its image member, else
// "<project>-<service>" (Compose's name for a built image).
func ImageName(project, service string, s Service) string {
	if s.Image != "" {
		return s.Image
	}
	return project + "-" + service
}

// Parse reads the output of `docker compose config --format json`. It accepts
// one JSON object of at most MaxConfigBytes with no data after it, and the
// members it reads must have the types Compose documents. Every other member
// is recorded by key only.
func Parse(data []byte) (Config, error) {
	if len(data) > MaxConfigBytes {
		return Config{}, fmt.Errorf("compose: configuration exceeds the limit of %d bytes", MaxConfigBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	top, err := readObject(dec, "configuration")
	if err != nil {
		return Config{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Config{}, errors.New("compose: configuration has data after the top-level object")
	}

	c := Config{TopKeys: keysOf(top)}
	for _, m := range top {
		switch m.key {
		case "name":
			c.Name, err = stringOf(m.val, "name")
		case "services":
			c.Services, err = entriesOf(m.val, "services", parseService)
		case "networks":
			c.Networks, err = entriesOf(m.val, "networks", parseObject)
		case "volumes":
			c.Volumes, err = entriesOf(m.val, "volumes", parseObject)
		case "configs":
			c.Configs, err = entriesOf(m.val, "configs", parseObject)
		case "secrets":
			c.Secrets, err = entriesOf(m.val, "secrets", parseObject)
		}
		if err != nil {
			return Config{}, err
		}
	}
	return c, nil
}

func parseService(path string, raw json.RawMessage) (Service, error) {
	ms, err := objectOf(raw, path)
	if err != nil {
		return Service{}, err
	}
	s := Service{Keys: keysOf(ms)}
	var refs []string
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "network_mode", "pid", "ipc":
			var v string
			if v, err = stringOf(m.val, p); err == nil {
				if ref, ok := strings.CutPrefix(v, "container:"); ok {
					refs = append(refs, ref)
				}
			}
		case "volumes_from":
			var vs []string
			if vs, err = stringsOf(m.val, p); err == nil {
				for _, v := range vs {
					if rest, ok := strings.CutPrefix(v, "container:"); ok {
						ref, _, _ := strings.Cut(rest, ":")
						refs = append(refs, ref)
					}
				}
			}
		case "external_links":
			var vs []string
			if vs, err = stringsOf(m.val, p); err == nil {
				for _, v := range vs {
					ref, _, _ := strings.Cut(v, ":")
					refs = append(refs, ref)
				}
			}
		case "image":
			s.Image, err = stringOf(m.val, p)
		case "container_name":
			s.ContainerName, err = stringOf(m.val, p)
		case "labels":
			s.Labels, err = labelsOf(m.val, p)
		case "networks":
			var nets []member
			if nets, err = objectOf(m.val, p); err == nil {
				s.Networks = keysOf(nets)
			}
		case "volumes":
			s.Volumes, err = volumeSources(m.val, p)
		case "build":
			s.Build = true
		case "provider":
			s.Provider = true
		}
		if err != nil {
			return Service{}, err
		}
	}
	if refs = slices.DeleteFunc(refs, func(r string) bool { return r == "" }); len(refs) > 0 {
		slices.Sort(refs)
		s.ContainerRefs = slices.Compact(refs)
	}
	return s, nil
}

func parseObject(path string, raw json.RawMessage) (Object, error) {
	ms, err := objectOf(raw, path)
	if err != nil {
		return Object{}, err
	}
	o := Object{Keys: keysOf(ms)}
	for _, m := range ms {
		p := path + "." + m.key
		switch m.key {
		case "name":
			o.Name, err = stringOf(m.val, p)
		case "external":
			o.External, err = boolOf(m.val, p)
		case "labels":
			o.Labels, err = labelsOf(m.val, p)
		}
		if err != nil {
			return Object{}, err
		}
	}
	return o, nil
}

// volumeSources returns the sorted, distinct sources of the mounts of type
// volume that name a volume.
func volumeSources(raw json.RawMessage, path string) ([]string, error) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("compose: %s must be an array", path)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("compose: %s: %w", path, err)
	}
	var sources []string
	for i, e := range elems {
		p := fmt.Sprintf("%s[%d]", path, i)
		ms, err := objectOf(e, p)
		if err != nil {
			return nil, err
		}
		var typ, source string
		for _, m := range ms {
			switch m.key {
			case "type":
				typ, err = stringOf(m.val, p+".type")
			case "source":
				source, err = stringOf(m.val, p+".source")
			}
			if err != nil {
				return nil, err
			}
		}
		if typ == "volume" && source != "" {
			sources = append(sources, source)
		}
	}
	slices.Sort(sources)
	return slices.Compact(sources), nil
}

// member is one key of a JSON object and its raw value.
type member struct {
	key string
	val json.RawMessage
}

// readObject reads the next value of dec, which must be an object, and returns
// its members in document order. A repeated key is an error. path names the
// object in errors.
func readObject(dec *json.Decoder, path string) ([]member, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("compose: %s: %w", path, err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("compose: %s must be an object", path)
	}
	var ms []member
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("compose: %s: %w", path, err)
		}
		key, _ := kt.(string)
		if seen[key] {
			return nil, fmt.Errorf("compose: duplicate key %q in %s", key, path)
		}
		seen[key] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("compose: %s.%s: %w", path, key, err)
		}
		ms = append(ms, member{key, bytes.TrimSpace(v)})
	}
	if _, err := dec.Token(); err != nil {
		return nil, fmt.Errorf("compose: %s: %w", path, err)
	}
	return ms, nil
}

func objectOf(raw json.RawMessage, path string) ([]member, error) {
	return readObject(json.NewDecoder(bytes.NewReader(raw)), path)
}

// entriesOf reads an object whose members are all objects, parsing each with
// parse. An empty object gives a nil map.
func entriesOf[T any](raw json.RawMessage, path string, parse func(path string, raw json.RawMessage) (T, error)) (map[string]T, error) {
	ms, err := objectOf(raw, path)
	if err != nil {
		return nil, err
	}
	var out map[string]T
	for _, m := range ms {
		v, err := parse(path+"."+m.key, m.val)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = make(map[string]T, len(ms))
		}
		out[m.key] = v
	}
	return out, nil
}

// labelsOf reads an object whose values are all strings. An empty object gives
// a nil map.
func labelsOf(raw json.RawMessage, path string) (map[string]string, error) {
	ms, err := objectOf(raw, path)
	if err != nil {
		return nil, err
	}
	var out map[string]string
	for _, m := range ms {
		v, err := stringOf(m.val, path+"."+m.key)
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = make(map[string]string, len(ms))
		}
		out[m.key] = v
	}
	return out, nil
}

func stringOf(raw json.RawMessage, path string) (string, error) {
	var s string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &s) != nil {
		return "", fmt.Errorf("compose: %s must be a string", path)
	}
	return s, nil
}

// stringsOf reads an array whose elements are all strings.
func stringsOf(raw json.RawMessage, path string) ([]string, error) {
	if len(raw) == 0 || raw[0] != '[' {
		return nil, fmt.Errorf("compose: %s must be an array", path)
	}
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("compose: %s: %w", path, err)
	}
	out := make([]string, 0, len(elems))
	for i, e := range elems {
		s, err := stringOf(e, fmt.Sprintf("%s[%d]", path, i))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func boolOf(raw json.RawMessage, path string) (bool, error) {
	switch string(raw) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("compose: %s must be a boolean", path)
}

// keysOf returns the keys of ms, sorted; nil for none.
func keysOf(ms []member) []string {
	var keys []string
	for _, m := range ms {
		keys = append(keys, m.key)
	}
	slices.Sort(keys)
	return keys
}

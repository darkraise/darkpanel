package fake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	maxKeyDescription = 50
	maxKeyExpiry      = 7776000 // 90 days in seconds
)

// Key is one auth key; the fake answers it as the provider's key object, with Reusable, Ephemeral,
// Preauthorized and Tags under capabilities.devices.create, and returns Key (the secret) only from create.
type Key struct {
	ID, Description, Key               string
	Created, Expires                   time.Time
	Revoked                            bool
	Tags                               []string
	Reusable, Ephemeral, Preauthorized bool
}

func (k Key) clone() Key {
	k.Tags = slices.Clone(k.Tags)
	return k
}

// Keys answers copies of the stored keys in creation order.
func (s *Server) Keys() []Key {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Key, len(s.keys))
	for i, k := range s.keys {
		out[i] = k.clone()
	}
	return out
}

// AddKey stores a copy of k, assigning "k<sequence>" to an empty ID and the current time to a zero Created.
func (s *Server) AddKey(k Key) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k = k.clone()
	if k.ID == "" {
		s.seq++
		k.ID = "k" + strconv.Itoa(s.seq)
	}
	if k.Created.IsZero() {
		k.Created = time.Now().UTC()
	}
	s.keys = append(s.keys, k)
}

func (k Key) render() map[string]any {
	m := map[string]any{
		"id": k.ID, "keyType": "auth", "description": k.Description,
		"created": k.Created.UTC().Format(time.RFC3339), "expires": k.Expires.UTC().Format(time.RFC3339),
		"capabilities": map[string]any{"devices": map[string]any{"create": map[string]any{
			"reusable": k.Reusable, "ephemeral": k.Ephemeral, "preauthorized": k.Preauthorized, "tags": nonNil(k.Tags),
		}}},
	}
	if k.Revoked {
		m["revoked"] = k.Created.UTC().Format(time.RFC3339)
		m["invalid"] = true
	}
	return m
}

// keyRequest is the body of a create call.
type keyRequest struct {
	Capabilities struct {
		Devices struct {
			Create struct {
				Reusable      bool     `json:"reusable"`
				Ephemeral     bool     `json:"ephemeral"`
				Preauthorized bool     `json:"preauthorized"`
				Tags          []string `json:"tags"`
			} `json:"create"`
		} `json:"devices"`
	} `json:"capabilities"`
	ExpirySeconds int    `json:"expirySeconds"`
	Description   string `json:"description"`
}

// refusal names what is wrong with a create request, or "" when it is acceptable.
func (q keyRequest) refusal() string {
	tags := q.Capabilities.Devices.Create.Tags
	switch {
	case len(tags) == 0:
		return "requested tags are required"
	case slices.ContainsFunc(tags, func(t string) bool { return !strings.HasPrefix(t, "tag:") }):
		return "every tag must start with tag:"
	case len(q.Description) > maxKeyDescription:
		return "description is longer than " + strconv.Itoa(maxKeyDescription) + " characters"
	case q.ExpirySeconds < 0 || q.ExpirySeconds > maxKeyExpiry:
		return "expirySeconds is out of range"
	}
	return ""
}

func (s *Server) registerKeys() {
	read := []string{"auth_keys", "auth_keys:read"}
	write := []string{"auth_keys"}
	const collection = "/api/v2/tailnet/{t}/keys"

	s.route(http.MethodPost, collection, write, func(w http.ResponseWriter, r *http.Request) {
		var q keyRequest
		if err := json.NewDecoder(r.Body).Decode(&q); err != nil {
			writeMessage(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if msg := q.refusal(); msg != "" {
			writeMessage(w, http.StatusBadRequest, msg)
			return
		}
		expiry := q.ExpirySeconds
		if expiry == 0 {
			expiry = maxKeyExpiry
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		s.seq++
		id := "k" + strconv.Itoa(s.seq)
		now := time.Now().UTC()
		create := q.Capabilities.Devices.Create
		k := Key{
			ID: id, Description: q.Description, Key: "tskey-auth-" + id + "-secret",
			Created: now, Expires: now.Add(time.Duration(expiry) * time.Second),
			Tags: slices.Clone(create.Tags), Reusable: create.Reusable, Ephemeral: create.Ephemeral, Preauthorized: create.Preauthorized,
		}
		s.keys = append(s.keys, k)
		out := k.render()
		out["key"] = k.Key
		writeJSON(w, http.StatusOK, out)
	})
	s.route(http.MethodGet, collection, read, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		out := []map[string]any{}
		if r.URL.Query().Get("all") == "true" {
			for _, k := range s.keys {
				if !k.Revoked {
					out = append(out, k.render())
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"keys": out})
	})
	s.route(http.MethodGet, collection+"/{id}", read, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.keyIndex(r.PathValue("id"))
		if i < 0 {
			writeMessage(w, http.StatusNotFound, "key not found")
			return
		}
		writeJSON(w, http.StatusOK, s.keys[i].render())
	})
	s.route(http.MethodDelete, collection+"/{id}", write, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.keyIndex(r.PathValue("id"))
		if i < 0 {
			writeMessage(w, http.StatusNotFound, "key not found")
			return
		}
		s.keys = slices.Delete(s.keys, i, i+1)
		w.WriteHeader(http.StatusOK)
	})
}

// keyIndex is the position of the key with the given id, or -1; s.mu must be held.
func (s *Server) keyIndex(id string) int {
	return slices.IndexFunc(s.keys, func(k Key) bool { return k.ID == id })
}

package fake

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"time"
)

const (
	codeTunnelNotFound  = 1003
	codeTunnelExists    = 1013
	codeTunnelConnected = 1022
	codeIngressCatchAll = 1015

	clientVersion = "2026.1.0"
	coloName      = "fake01"
)

// Tunnel is one tunnel; the fake answers it as the provider's object (id, name, status, created_at,
// connections), with one connection entry per Connections and status "healthy" when Connections > 0,
// else "inactive".
type Tunnel struct {
	ID, Name    string
	CreatedAt   time.Time
	Connections int
}

type tunnelState struct {
	Tunnel
	config   json.RawMessage
	version  int
	configAt time.Time
}

// AddTunnel stores a tunnel created now and answers its id.
func (s *Server) AddTunnel(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addTunnel(name).ID
}

// addTunnel stores a tunnel; the caller holds s.mu.
func (s *Server) addTunnel(name string) *tunnelState {
	s.seq++
	t := &tunnelState{Tunnel: Tunnel{
		ID:        fmt.Sprintf("00000000-0000-4000-8000-%012d", s.seq),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}}
	s.tunnels = append(s.tunnels, t)
	return t
}

// Tunnels answers copies of the stored tunnels in creation order.
func (s *Server) Tunnels() []Tunnel {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Tunnel, len(s.tunnels))
	for i, t := range s.tunnels {
		out[i] = t.Tunnel
	}
	return out
}

// SetConnections sets how many connections the tunnel holds; an unknown id is ignored.
func (s *Server) SetConnections(tunnelID string, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tunnel(tunnelID); t != nil {
		t.Connections = n
	}
}

// Configuration answers a copy of the stored config member of the tunnel's configuration, nil when none.
func (s *Server) Configuration(tunnelID string) json.RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tunnel(tunnelID); t != nil {
		return slices.Clone(t.config)
	}
	return nil
}

// SetConfiguration stores raw as the config member of the tunnel's configuration without validating it, and
// bumps the version; an unknown id is ignored.
func (s *Server) SetConfiguration(tunnelID string, raw json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t := s.tunnel(tunnelID); t != nil {
		t.store(raw)
	}
}

func (t *tunnelState) store(raw json.RawMessage) {
	t.config = slices.Clone(raw)
	t.version++
	t.configAt = time.Now().UTC()
}

// tunnel finds a stored tunnel; the caller holds s.mu.
func (s *Server) tunnel(id string) *tunnelState {
	for _, t := range s.tunnels {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (s *Server) registerTunnels() {
	const base = "/accounts/{a}/cfd_tunnel"
	s.route(http.MethodGet, base, PermTunnelWrite, s.listTunnels)
	s.route(http.MethodPost, base, PermTunnelWrite, s.createTunnel)
	s.route(http.MethodGet, base+"/{id}", PermTunnelWrite, s.getTunnel)
	s.route(http.MethodDelete, base+"/{id}", PermTunnelWrite, s.deleteTunnel)
	s.route(http.MethodGet, base+"/{id}/token", PermTunnelWrite, s.tunnelToken)
	s.route(http.MethodGet, base+"/{id}/connections", PermTunnelWrite, s.tunnelConnections)
	s.route(http.MethodGet, base+"/{id}/configurations", PermTunnelWrite, s.getConfiguration)
	s.route(http.MethodPut, base+"/{id}/configurations", PermTunnelWrite, s.putConfiguration)
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func connectionEntry(t *tunnelState, i int) map[string]any {
	n := "conn-" + strconv.Itoa(i)
	return map[string]any{
		"id": n, "uuid": n, "colo_name": coloName, "origin_ip": "198.51.100." + strconv.Itoa(i),
		"opened_at": stamp(t.CreatedAt), "is_pending_reconnect": false,
	}
}

func renderTunnel(t *tunnelState, deletedAt any) map[string]any {
	status := "inactive"
	if t.Connections > 0 {
		status = "healthy"
	}
	conns := make([]any, 0, t.Connections)
	for i := 1; i <= t.Connections; i++ {
		c := connectionEntry(t, i)
		c["client_id"] = "fake-client-id"
		c["client_version"] = clientVersion
		conns = append(conns, c)
	}
	return map[string]any{
		"id": t.ID, "account_tag": AccountID, "name": t.Name, "status": status,
		"created_at": stamp(t.CreatedAt), "deleted_at": deletedAt, "connections": conns,
		"tun_type": "cfd_tunnel", "remote_config": true,
	}
}

func writeTunnelNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, codeTunnelNotFound, "Tunnel not found")
}

func (s *Server) listTunnels(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []any{}
	for _, t := range s.tunnels {
		if name == "" || t.Name == name {
			out = append(out, renderTunnel(t, nil))
		}
	}
	writeList(w, out)
}

func (s *Server) createTunnel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string `json:"name"`
		ConfigSrc string `json:"config_src"`
	}
	if err := decodeBody(r, &body); err != nil || body.Name == "" || body.ConfigSrc != "cloudflare" {
		writeError(w, http.StatusBadRequest, codeBadInput, "Invalid request")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tunnels {
		if t.Name == body.Name {
			writeError(w, http.StatusConflict, codeTunnelExists, "A tunnel with this name already exists")
			return
		}
	}
	writeResult(w, renderTunnel(s.addTunnel(body.Name), nil))
}

func (s *Server) getTunnel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	writeResult(w, renderTunnel(t, nil))
}

func (s *Server) deleteTunnel(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	if t.Connections > 0 {
		writeError(w, http.StatusBadRequest, codeTunnelConnected, "Cannot delete a tunnel with active connections")
		return
	}
	s.tunnels = slices.DeleteFunc(s.tunnels, func(x *tunnelState) bool { return x == t })
	writeResult(w, renderTunnel(t, stamp(time.Now())))
}

func (s *Server) tunnelToken(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	doc, _ := json.Marshal(map[string]string{"a": AccountID, "t": t.ID, "s": "fake-tunnel-secret"})
	writeResult(w, base64.StdEncoding.EncodeToString(doc))
}

func (s *Server) tunnelConnections(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	out := []any{}
	if t.Connections > 0 {
		conns := make([]any, 0, t.Connections)
		for i := 1; i <= t.Connections; i++ {
			conns = append(conns, connectionEntry(t, i))
		}
		out = append(out, map[string]any{
			"id": "connector-" + t.ID, "version": clientVersion, "arch": "linux_amd64",
			"run_at": stamp(t.CreatedAt), "conns": conns,
		})
	}
	writeList(w, out)
}

func (s *Server) getConfiguration(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	writeResult(w, renderConfiguration(t))
}

func renderConfiguration(t *tunnelState) map[string]any {
	at := t.CreatedAt
	var config any
	if t.config != nil {
		at = t.configAt
		config = slices.Clone(t.config)
	}
	return map[string]any{
		"tunnel_id": t.ID, "source": "cloudflare", "created_at": stamp(at), "version": t.version, "config": config,
	}
}

func (s *Server) putConfiguration(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tunnel(r.PathValue("id"))
	if t == nil {
		writeTunnelNotFound(w)
		return
	}
	var body struct {
		Config json.RawMessage `json:"config"`
	}
	var members map[string]json.RawMessage
	if err := decodeBody(r, &body); err != nil || json.Unmarshal(body.Config, &members) != nil || members == nil {
		writeError(w, http.StatusBadRequest, codeBadInput, "Invalid request")
		return
	}
	if raw, ok := members["ingress"]; ok {
		var rules []struct {
			Hostname string `json:"hostname"`
			Path     string `json:"path"`
		}
		if json.Unmarshal(raw, &rules) != nil {
			writeError(w, http.StatusBadRequest, codeBadInput, "Invalid request")
			return
		}
		if len(rules) == 0 || rules[len(rules)-1].Hostname != "" || rules[len(rules)-1].Path != "" {
			writeError(w, http.StatusBadRequest, codeIngressCatchAll,
				"The last ingress rule must match all URLs (it must not have a hostname or path)")
			return
		}
	}
	t.store(body.Config)
	writeResult(w, renderConfiguration(t))
}

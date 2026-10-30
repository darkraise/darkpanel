package fake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// Device is one tailnet device; the fake answers ID as both the JSON "id" and "nodeId", and
// AddDevice assigns "n<sequence>" when ID is empty.
type Device struct {
	ID                string   `json:"nodeId"`
	Name              string   `json:"name"`
	Hostname          string   `json:"hostname"`
	OS                string   `json:"os"`
	User              string   `json:"user"`
	Addresses         []string `json:"addresses"`
	Tags              []string `json:"tags"`
	Authorized        bool     `json:"authorized"`
	LastSeen          string   `json:"lastSeen,omitempty"`
	Expires           string   `json:"expires,omitempty"`
	KeyExpiryDisabled bool     `json:"keyExpiryDisabled"`
	AdvertisedRoutes  []string `json:"advertisedRoutes"`
	EnabledRoutes     []string `json:"enabledRoutes"`
}

func (d Device) clone() Device {
	d.Addresses = slices.Clone(d.Addresses)
	d.Tags = slices.Clone(d.Tags)
	d.AdvertisedRoutes = slices.Clone(d.AdvertisedRoutes)
	d.EnabledRoutes = slices.Clone(d.EnabledRoutes)
	return d
}

// AddDevice stores a copy of d, replacing a stored device with the same id, and answers its id.
func (s *Server) AddDevice(d Device) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	d = d.clone()
	if d.ID == "" {
		s.seq++
		d.ID = "n" + strconv.Itoa(s.seq)
	}
	if i := s.deviceIndex(d.ID); i >= 0 {
		s.devices[i] = d
	} else {
		s.devices = append(s.devices, d)
	}
	return d.ID
}

// Device answers a copy of the stored device with the given id.
func (s *Server) Device(id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.deviceIndex(id)
	if i < 0 {
		return Device{}, false
	}
	return s.devices[i].clone(), true
}

// deviceIndex is the position of the device with the given id, or -1; s.mu must be held.
func (s *Server) deviceIndex(id string) int {
	return slices.IndexFunc(s.devices, func(d Device) bool { return d.ID == id })
}

func (d Device) render(withRoutes bool) map[string]any {
	m := map[string]any{
		"id": d.ID, "nodeId": d.ID, "name": d.Name, "hostname": d.Hostname, "os": d.OS, "user": d.User,
		"addresses": nonNil(d.Addresses), "tags": nonNil(d.Tags),
		"authorized": d.Authorized, "keyExpiryDisabled": d.KeyExpiryDisabled,
	}
	if d.LastSeen != "" {
		m["lastSeen"] = d.LastSeen
	}
	if d.Expires != "" {
		m["expires"] = d.Expires
	}
	if withRoutes {
		m["advertisedRoutes"] = nonNil(d.AdvertisedRoutes)
		m["enabledRoutes"] = nonNil(d.EnabledRoutes)
	}
	return m
}

func (d Device) routes() map[string]any {
	return map[string]any{"advertisedRoutes": nonNil(d.AdvertisedRoutes), "enabledRoutes": nonNil(d.EnabledRoutes)}
}

// withDevice runs f on the stored device named by the request under the lock, or answers 404.
func (s *Server) withDevice(w http.ResponseWriter, r *http.Request, f func(d *Device)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.deviceIndex(r.PathValue("id"))
	if i < 0 {
		writeMessage(w, http.StatusNotFound, "device not found")
		return
	}
	f(&s.devices[i])
}

func (s *Server) registerDevices() {
	read := []string{"devices:core", "devices:core:read"}
	write := []string{"devices:core"}
	routesRead := []string{"devices:routes", "devices:routes:read"}
	routesWrite := []string{"devices:routes"}

	s.route(http.MethodGet, "/api/v2/tailnet/{t}/devices", read, func(w http.ResponseWriter, r *http.Request) {
		all := r.URL.Query().Get("fields") == "all"
		s.mu.Lock()
		defer s.mu.Unlock()
		out := make([]map[string]any, len(s.devices))
		for i, d := range s.devices {
			out[i] = d.render(all)
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": out})
	})
	s.route(http.MethodGet, "/api/v2/device/{id}", read, func(w http.ResponseWriter, r *http.Request) {
		all := r.URL.Query().Get("fields") == "all"
		s.withDevice(w, r, func(d *Device) { writeJSON(w, http.StatusOK, d.render(all)) })
	})
	s.route(http.MethodPost, "/api/v2/device/{id}/authorized", write, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Authorized *bool `json:"authorized"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Authorized == nil {
			writeMessage(w, http.StatusBadRequest, "authorized must be a boolean")
			return
		}
		s.withDevice(w, r, func(d *Device) {
			d.Authorized = *body.Authorized
			writeJSON(w, http.StatusOK, struct{}{})
		})
	})
	s.route(http.MethodPost, "/api/v2/device/{id}/expire", write, func(w http.ResponseWriter, r *http.Request) {
		s.withDevice(w, r, func(d *Device) {
			d.Expires = time.Now().UTC().Format(time.RFC3339)
			writeJSON(w, http.StatusOK, struct{}{})
		})
	})
	s.route(http.MethodDelete, "/api/v2/device/{id}", write, func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.deviceIndex(r.PathValue("id"))
		if i < 0 {
			writeMessage(w, http.StatusNotFound, "device not found")
			return
		}
		s.devices = slices.Delete(s.devices, i, i+1)
		w.WriteHeader(http.StatusOK)
	})
	s.route(http.MethodGet, "/api/v2/device/{id}/routes", routesRead, func(w http.ResponseWriter, r *http.Request) {
		s.withDevice(w, r, func(d *Device) { writeJSON(w, http.StatusOK, d.routes()) })
	})
	s.route(http.MethodPost, "/api/v2/device/{id}/routes", routesWrite, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Routes *[]string `json:"routes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Routes == nil {
			writeMessage(w, http.StatusBadRequest, "routes must be a list")
			return
		}
		s.withDevice(w, r, func(d *Device) {
			d.EnabledRoutes = slices.Clone(*body.Routes)
			writeJSON(w, http.StatusOK, d.routes())
		})
	})
}

package fake

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

const (
	codeZoneNotFound   = 7003
	codeRecordInvalid  = 9005
	codeRecordExists   = 81058
	codeRecordNotFound = 81044
)

// Record is one DNS record; the fake answers it with the provider's members (id, type, name, content,
// comment, proxied).
type Record struct {
	ID, Type, Name, Content, Comment string
	Proxied                          bool
}

type recordState struct {
	Record
	createdOn time.Time
}

// AddRecord appends a copy of r to the zone's records and answers its id, assigning one when r.ID is empty.
func (s *Server) AddRecord(zoneID string, r Record) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addRecord(zoneID, r).ID
}

// addRecord appends r; the caller holds s.mu.
func (s *Server) addRecord(zoneID string, r Record) *recordState {
	s.seq++
	if r.ID == "" {
		r.ID = fmt.Sprintf("rec%029x", s.seq)
	}
	rec := &recordState{Record: r, createdOn: time.Now().UTC()}
	s.records[zoneID] = append(s.records[zoneID], rec)
	return rec
}

// Records answers copies of the zone's records in creation order.
func (s *Server) Records(zoneID string) []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.records[zoneID]))
	for i, r := range s.records[zoneID] {
		out[i] = r.Record
	}
	return out
}

func (s *Server) registerRecords() {
	const base = "/zones/{z}/dns_records"
	s.route(http.MethodGet, base, PermDNSWrite, s.listRecords)
	s.route(http.MethodPost, base, PermDNSWrite, s.createRecord)
	s.route(http.MethodGet, base+"/{id}", PermDNSWrite, s.getRecord)
	s.route(http.MethodDelete, base+"/{id}", PermDNSWrite, s.deleteRecord)
}

// hasZone reports whether a zone was added; the caller holds s.mu.
func (s *Server) hasZone(id string) bool {
	return slices.ContainsFunc(s.zones, func(z zone) bool { return z.id == id })
}

func renderRecord(zoneID string, r *recordState) map[string]any {
	return map[string]any{
		"id": r.ID, "zone_id": zoneID, "type": r.Type, "name": r.Name, "content": r.Content,
		"comment": r.Comment, "proxied": r.Proxied, "ttl": 1,
		"created_on": stamp(r.createdOn), "modified_on": stamp(r.createdOn),
	}
}

func writeRecordNotFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, codeRecordNotFound, "Record does not exist")
}

// knownZone answers the zone id, or writes the 404 for a zone that was not added and answers
// false; the caller holds s.mu.
func (s *Server) knownZone(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("z")
	if !s.hasZone(id) {
		writeError(w, http.StatusNotFound, codeZoneNotFound, "Zone not found")
		return "", false
	}
	return id, true
}

func (s *Server) listRecords(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	zoneID, ok := s.knownZone(w, r)
	if !ok {
		return
	}
	out := []any{}
	for _, rec := range s.records[zoneID] {
		switch {
		case q.Get("name") != "" && rec.Name != q.Get("name"),
			q.Get("type") != "" && rec.Type != q.Get("type"),
			q.Get("comment.exact") != "" && rec.Comment != q.Get("comment.exact"),
			q.Get("comment.contains") != "" && !strings.Contains(rec.Comment, q.Get("comment.contains")):
			continue
		}
		out = append(out, renderRecord(zoneID, rec))
	}
	writeList(w, out)
}

func (s *Server) createRecord(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	zoneID, ok := s.knownZone(w, r)
	if !ok {
		return
	}
	var body struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		Comment string `json:"comment"`
		Proxied bool   `json:"proxied"`
	}
	if err := decodeBody(r, &body); err != nil || body.Type == "" || body.Name == "" || body.Content == "" {
		writeError(w, http.StatusBadRequest, codeRecordInvalid, "Invalid DNS record")
		return
	}
	for _, rec := range s.records[zoneID] {
		if rec.Type == body.Type && rec.Name == body.Name && rec.Content == body.Content {
			writeError(w, http.StatusBadRequest, codeRecordExists, "An identical record already exists.")
			return
		}
	}
	rec := s.addRecord(zoneID, Record{
		Type: body.Type, Name: body.Name, Content: body.Content, Comment: body.Comment, Proxied: body.Proxied,
	})
	writeResult(w, renderRecord(zoneID, rec))
}

func (s *Server) getRecord(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	zoneID, ok := s.knownZone(w, r)
	if !ok {
		return
	}
	i := s.recordIndex(zoneID, r.PathValue("id"))
	if i < 0 {
		writeRecordNotFound(w)
		return
	}
	writeResult(w, renderRecord(zoneID, s.records[zoneID][i]))
}

func (s *Server) deleteRecord(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	zoneID, ok := s.knownZone(w, r)
	if !ok {
		return
	}
	i := s.recordIndex(zoneID, r.PathValue("id"))
	if i < 0 {
		writeRecordNotFound(w)
		return
	}
	id := s.records[zoneID][i].ID
	s.records[zoneID] = slices.Delete(s.records[zoneID], i, i+1)
	writeResult(w, map[string]any{"id": id})
}

// recordIndex finds a record's position in the zone's records, or -1; the caller holds s.mu.
func (s *Server) recordIndex(zoneID, id string) int {
	return slices.IndexFunc(s.records[zoneID], func(r *recordState) bool { return r.ID == id })
}

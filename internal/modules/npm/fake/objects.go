package fake

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
)

const (
	hostsPath = "/api/nginx/proxy-hosts"
	listsPath = "/api/nginx/access-lists"
	certsPath = "/api/nginx/certificates"

	certificateLifetime = 90 * 24 * time.Hour
	badMeta             = "data/meta must NOT have additional properties"
)

func (s *Server) registerObjects() {
	s.route(http.MethodGet, hostsPath, true, s.listHosts)
	s.route(http.MethodPost, hostsPath, true, s.createHost)
	s.route(http.MethodGet, hostsPath+"/{id}", true, s.getHost)
	s.route(http.MethodPut, hostsPath+"/{id}", true, s.updateHost)
	s.route(http.MethodDelete, hostsPath+"/{id}", true, s.deleteHost)

	s.route(http.MethodGet, listsPath, true, s.listLists)
	s.route(http.MethodPost, listsPath, true, s.createList)
	s.route(http.MethodGet, listsPath+"/{id}", true, s.getList)
	s.route(http.MethodDelete, listsPath+"/{id}", true, s.deleteList)

	s.route(http.MethodGet, certsPath, true, s.listCerts)
	s.route(http.MethodPost, certsPath, true, s.createCert)
	s.route(http.MethodGet, certsPath+"/{id}", true, s.getCert)
	s.route(http.MethodDelete, certsPath+"/{id}", true, s.deleteCert)
}

// ProxyHosts returns copies of the stored proxy hosts in id order; the slice is never nil.
func (s *Server) ProxyHosts() []npmapi.ProxyHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]npmapi.ProxyHost, len(s.hosts))
	for i, h := range s.hosts {
		out[i] = cloneHost(h)
	}
	return out
}

// AccessLists returns copies of the stored access lists in id order; the slice is never nil.
func (s *Server) AccessLists() []npmapi.AccessList {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]npmapi.AccessList, len(s.lists))
	for i, l := range s.lists {
		out[i] = cloneList(l)
	}
	return out
}

// Certificates returns copies of the stored certificates in id order; the slice is never nil.
func (s *Server) Certificates() []npmapi.Certificate {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]npmapi.Certificate, len(s.certs))
	for i, c := range s.certs {
		out[i] = cloneCert(c)
	}
	return out
}

// SeedProxyHost stores h under the next id, ignoring h.ID, without validation, and returns the stored copy. An empty
// CreatedOn is taken from the clock; every other member is stored as given.
func (s *Server) SeedProxyHost(h npmapi.ProxyHost) npmapi.ProxyHost {
	s.mu.Lock()
	defer s.mu.Unlock()
	h = cloneHost(h)
	s.hostSeq++
	h.ID = s.hostSeq
	if h.CreatedOn == "" {
		h.CreatedOn = s.stamp(s.now())
	}
	s.hosts = append(s.hosts, h)
	return cloneHost(h)
}

// SeedAccessList stores l under the next id, ignoring l.ID, without validation, and returns the stored copy. An empty
// CreatedOn is taken from the clock; every other member is stored as given.
func (s *Server) SeedAccessList(l npmapi.AccessList) npmapi.AccessList {
	s.mu.Lock()
	defer s.mu.Unlock()
	l = cloneList(l)
	s.listSeq++
	l.ID = s.listSeq
	if l.CreatedOn == "" {
		l.CreatedOn = s.stamp(s.now())
	}
	s.lists = append(s.lists, l)
	return cloneList(l)
}

// SeedCertificate stores c under the next id, ignoring c.ID, without validation, and returns the stored copy. An empty
// CreatedOn is taken from the clock; every other member, NiceName and ExpiresOn included, is stored as given.
func (s *Server) SeedCertificate(c npmapi.Certificate) npmapi.Certificate {
	s.mu.Lock()
	defer s.mu.Unlock()
	c = cloneCert(c)
	s.certSeq++
	c.ID = s.certSeq
	if c.CreatedOn == "" {
		c.CreatedOn = s.stamp(s.now())
	}
	s.certs = append(s.certs, c)
	return cloneCert(c)
}

func cloneHost(h npmapi.ProxyHost) npmapi.ProxyHost {
	h.DomainNames = slices.Clone(h.DomainNames)
	h.Locations = slices.Clone(h.Locations)
	return h
}

func cloneList(l npmapi.AccessList) npmapi.AccessList {
	l.Clients = slices.Clone(l.Clients)
	return l
}

func cloneCert(c npmapi.Certificate) npmapi.Certificate {
	c.DomainNames = slices.Clone(c.DomainNames)
	return c
}

func idOf(r *http.Request) (string, int) {
	raw := r.PathValue("id")
	id, err := strconv.Atoi(raw)
	if err != nil {
		return raw, 0
	}
	return raw, id
}

func (s *Server) listHosts(*http.Request, []byte) (int, any) {
	return http.StatusOK, s.ProxyHosts()
}

func (s *Server) getHost(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.hostIndex(id); i >= 0 {
		return http.StatusOK, cloneHost(s.hosts[i])
	}
	return notFound(raw)
}

func (s *Server) createHost(_ *http.Request, body []byte) (int, any) {
	var in npmapi.ProxyHostBody
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if msg := s.checkHost(in, 0); msg != "" {
		return refusal(http.StatusBadRequest, msg)
	}
	s.hostSeq++
	now := s.stamp(s.now())
	h := hostOf(in)
	h.ID, h.CreatedOn, h.ModifiedOn = s.hostSeq, now, now
	s.hosts = append(s.hosts, h)
	return http.StatusCreated, cloneHost(h)
}

func (s *Server) updateHost(r *http.Request, body []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.hostIndex(id)
	if i < 0 {
		return notFound(raw)
	}
	var in npmapi.ProxyHostBody
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	if msg := s.checkHost(in, id); msg != "" {
		return refusal(http.StatusBadRequest, msg)
	}
	h := hostOf(in)
	h.ID, h.CreatedOn, h.ModifiedOn = id, s.hosts[i].CreatedOn, s.stamp(s.now())
	s.hosts[i] = h
	return http.StatusOK, cloneHost(h)
}

func (s *Server) deleteHost(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.hostIndex(id)
	if i < 0 {
		return notFound(raw)
	}
	s.hosts = slices.Delete(s.hosts, i, i+1)
	return http.StatusOK, true
}

// hostIndex is the position of the proxy host with id, or -1; the caller holds the lock.
func (s *Server) hostIndex(id int) int {
	return slices.IndexFunc(s.hosts, func(h npmapi.ProxyHost) bool { return h.ID == id })
}

// checkHost returns the refusal text for in, or "" when NPM would accept it. A domain held by the proxy host with id
// except does not conflict; the caller holds the lock.
func (s *Server) checkHost(in npmapi.ProxyHostBody, except int) string {
	switch {
	case len(in.DomainNames) == 0:
		return "data/domain_names must NOT have fewer than 1 items"
	case in.ForwardScheme != "http" && in.ForwardScheme != "https":
		return "data/forward_scheme must be equal to one of the allowed values"
	case in.ForwardHost == "":
		return "data/forward_host must NOT have fewer than 1 characters"
	case in.ForwardPort < 1 || in.ForwardPort > 65535:
		return "data/forward_port must be between 1 and 65535"
	}
	for _, d := range in.DomainNames {
		for _, h := range s.hosts {
			if h.ID != except && slices.ContainsFunc(h.DomainNames, func(held string) bool { return strings.EqualFold(held, d) }) {
				return d + " is already in use"
			}
		}
	}
	return ""
}

func hostOf(in npmapi.ProxyHostBody) npmapi.ProxyHost {
	return cloneHost(npmapi.ProxyHost{
		DomainNames: in.DomainNames, ForwardScheme: in.ForwardScheme, ForwardHost: in.ForwardHost, ForwardPort: in.ForwardPort,
		CertificateID: in.CertificateID, AccessListID: in.AccessListID, SSLForced: in.SSLForced, HSTSEnabled: in.HSTSEnabled,
		AllowWebsocketUpgrade: in.AllowWebsocketUpgrade, Locations: in.Locations, AdvancedConfig: in.AdvancedConfig,
	})
}

// accessListView is an access list as it is answered: its clients only when the request expands them.
type accessListView struct {
	ID         int                    `json:"id"`
	CreatedOn  string                 `json:"created_on"`
	Name       string                 `json:"name"`
	SatisfyAny bool                   `json:"satisfy_any"`
	PassAuth   bool                   `json:"pass_auth"`
	Clients    *[]npmapi.AccessClient `json:"clients,omitempty"`
}

func viewOf(l npmapi.AccessList, expand bool) accessListView {
	v := accessListView{ID: l.ID, CreatedOn: l.CreatedOn, Name: l.Name, SatisfyAny: l.SatisfyAny, PassAuth: l.PassAuth}
	if expand {
		clients := cloneList(l).Clients
		if clients == nil {
			clients = []npmapi.AccessClient{}
		}
		v.Clients = &clients
	}
	return v
}

func expands(r *http.Request) bool {
	return slices.Contains(strings.Split(r.URL.Query().Get("expand"), ","), "clients")
}

func (s *Server) listLists(r *http.Request, _ []byte) (int, any) {
	expand := expands(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]accessListView, len(s.lists))
	for i, l := range s.lists {
		out[i] = viewOf(l, expand)
	}
	return http.StatusOK, out
}

func (s *Server) getList(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.listIndex(id); i >= 0 {
		return http.StatusOK, viewOf(s.lists[i], expands(r))
	}
	return notFound(raw)
}

func (s *Server) createList(_ *http.Request, body []byte) (int, any) {
	var in npmapi.AccessListBody
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	if in.Name == "" {
		return refusal(http.StatusBadRequest, "data/name must NOT have fewer than 1 characters")
	}
	for _, c := range in.Clients {
		if c.Address == "" {
			return refusal(http.StatusBadRequest, "data/clients/address must NOT have fewer than 1 characters")
		}
		if c.Directive != "allow" && c.Directive != "deny" {
			return refusal(http.StatusBadRequest, "data/clients/directive must be equal to one of the allowed values")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listSeq++
	l := npmapi.AccessList{
		ID: s.listSeq, CreatedOn: s.stamp(s.now()), Name: in.Name, SatisfyAny: in.SatisfyAny, PassAuth: in.PassAuth, Clients: slices.Clone(in.Clients),
	}
	s.lists = append(s.lists, l)
	return http.StatusCreated, viewOf(l, true)
}

func (s *Server) deleteList(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.listIndex(id)
	if i < 0 {
		return notFound(raw)
	}
	s.lists = slices.Delete(s.lists, i, i+1)
	return http.StatusOK, true
}

// listIndex is the position of the access list with id, or -1; the caller holds the lock.
func (s *Server) listIndex(id int) int {
	return slices.IndexFunc(s.lists, func(l npmapi.AccessList) bool { return l.ID == id })
}

func (s *Server) listCerts(*http.Request, []byte) (int, any) {
	return http.StatusOK, s.Certificates()
}

func (s *Server) getCert(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if i := s.certIndex(id); i >= 0 {
		return http.StatusOK, cloneCert(s.certs[i])
	}
	return notFound(raw)
}

func (s *Server) createCert(_ *http.Request, body []byte) (int, any) {
	var in struct {
		Provider    string         `json:"provider"`
		DomainNames []string       `json:"domain_names"`
		Meta        map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return refusal(http.StatusBadRequest, "Bad Request")
	}
	if in.Provider != "letsencrypt" {
		return refusal(http.StatusBadRequest, "data/provider must be equal to letsencrypt")
	}
	if len(in.DomainNames) == 0 {
		return refusal(http.StatusBadRequest, "data/domain_names must NOT have fewer than 1 items")
	}
	if _, ok := in.Meta["letsencrypt_email"]; ok {
		return refusal(http.StatusBadRequest, badMeta)
	}
	if _, ok := in.Meta["letsencrypt_agree"]; ok {
		return refusal(http.StatusBadRequest, badMeta)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.certSeq++
	now := s.now()
	c := npmapi.Certificate{
		ID: s.certSeq, CreatedOn: s.stamp(now), NiceName: strings.Join(in.DomainNames, ", "), Provider: "letsencrypt",
		DomainNames: slices.Clone(in.DomainNames), ExpiresOn: s.stamp(now.Add(certificateLifetime)),
	}
	s.certs = append(s.certs, c)
	return http.StatusCreated, cloneCert(c)
}

func (s *Server) deleteCert(r *http.Request, _ []byte) (int, any) {
	raw, id := idOf(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.certIndex(id)
	if i < 0 {
		return notFound(raw)
	}
	s.certs = slices.Delete(s.certs, i, i+1)
	return http.StatusOK, true
}

// certIndex is the position of the certificate with id, or -1; the caller holds the lock.
func (s *Server) certIndex(id int) int {
	return slices.IndexFunc(s.certs, func(c npmapi.Certificate) bool { return c.ID == id })
}

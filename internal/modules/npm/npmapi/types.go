package npmapi

import (
	"encoding/json"
	"fmt"
)

// Health is the answer of NPM's status endpoint.
type Health struct {
	Status string `json:"status"`
	Setup  bool   `json:"setup"`
}

// ProxyHost is a proxy host as NPM answers it.
type ProxyHost struct {
	ID                    int           `json:"id"`
	CreatedOn             string        `json:"created_on"`
	ModifiedOn            string        `json:"modified_on"`
	DomainNames           []string      `json:"domain_names"`
	ForwardScheme         string        `json:"forward_scheme"`
	ForwardHost           string        `json:"forward_host"`
	ForwardPort           int           `json:"forward_port"`
	CertificateID         int           `json:"certificate_id"`
	AccessListID          int           `json:"access_list_id"`
	SSLForced             bool          `json:"ssl_forced"`
	HSTSEnabled           bool          `json:"hsts_enabled"`
	AllowWebsocketUpgrade bool          `json:"allow_websocket_upgrade"`
	Locations             []Location    `json:"locations"`
	AdvancedConfig        string        `json:"advanced_config"`
	Enabled               Flag          `json:"enabled"`
	Meta                  ProxyHostMeta `json:"meta"`
}

// Flag is a boolean of an NPM answer. NPM keeps it in an integer column and answers it as a boolean, so 1 and 0
// are read too.
type Flag bool

// UnmarshalJSON accepts true, false, 1 and 0.
func (f *Flag) UnmarshalJSON(raw []byte) error {
	switch string(raw) {
	case "true", "1":
		*f = true
	case "false", "0":
		*f = false
	default:
		return fmt.Errorf("npmapi: %s is not a boolean flag", raw)
	}
	return nil
}

// MarshalJSON writes the flag as a boolean.
func (f Flag) MarshalJSON() ([]byte, error) {
	return json.Marshal(bool(f))
}

// ProxyHostMeta is the part of a proxy host's meta that says whether nginx took its configuration: NginxOnline is
// false and NginxErr holds nginx's test output when the configuration NPM wrote failed nginx's test.
type ProxyHostMeta struct {
	NginxOnline *bool   `json:"nginx_online,omitempty"`
	NginxErr    *string `json:"nginx_err,omitempty"`
}

// ProxyHostBody is the request body that creates or replaces a proxy host.
type ProxyHostBody struct {
	DomainNames           []string   `json:"domain_names"`
	ForwardScheme         string     `json:"forward_scheme"`
	ForwardHost           string     `json:"forward_host"`
	ForwardPort           int        `json:"forward_port"`
	CertificateID         int        `json:"certificate_id"`
	AccessListID          int        `json:"access_list_id"`
	SSLForced             bool       `json:"ssl_forced"`
	HSTSEnabled           bool       `json:"hsts_enabled"`
	AllowWebsocketUpgrade bool       `json:"allow_websocket_upgrade"`
	Locations             []Location `json:"locations"`
	AdvancedConfig        string     `json:"advanced_config"`
}

// MarshalJSON encodes nil lists as empty arrays.
func (b ProxyHostBody) MarshalJSON() ([]byte, error) {
	type plain ProxyHostBody
	p := plain(b)
	if p.DomainNames == nil {
		p.DomainNames = []string{}
	}
	if p.Locations == nil {
		p.Locations = []Location{}
	}
	return json.Marshal(p)
}

// Location is a custom location of a proxy host.
type Location struct {
	Path           string `json:"path"`
	ForwardScheme  string `json:"forward_scheme"`
	ForwardHost    string `json:"forward_host"`
	ForwardPort    int    `json:"forward_port"`
	AdvancedConfig string `json:"advanced_config"`
}

// AccessList is an access list as NPM answers it; Clients is set only when the read asked for them.
type AccessList struct {
	ID         int            `json:"id"`
	CreatedOn  string         `json:"created_on"`
	Name       string         `json:"name"`
	SatisfyAny bool           `json:"satisfy_any"`
	PassAuth   bool           `json:"pass_auth"`
	Clients    []AccessClient `json:"clients"`
}

// AccessListBody is the request body that creates an access list.
type AccessListBody struct {
	Name       string         `json:"name"`
	SatisfyAny bool           `json:"satisfy_any"`
	PassAuth   bool           `json:"pass_auth"`
	Clients    []AccessClient `json:"clients"`
}

// MarshalJSON encodes a nil client list as an empty array and always adds an empty "items" member, without which NPM
// answers 500 after it has committed the list.
func (b AccessListBody) MarshalJSON() ([]byte, error) {
	type plain AccessListBody
	p := plain(b)
	if p.Clients == nil {
		p.Clients = []AccessClient{}
	}
	return json.Marshal(struct {
		plain
		Items []struct{} `json:"items"`
	}{plain: p, Items: []struct{}{}})
}

// AccessClient is one address rule of an access list.
type AccessClient struct {
	Address   string `json:"address"`
	Directive string `json:"directive"`
}

// Certificate is a certificate as NPM answers it.
type Certificate struct {
	ID          int      `json:"id"`
	CreatedOn   string   `json:"created_on"`
	NiceName    string   `json:"nice_name"`
	Provider    string   `json:"provider"`
	DomainNames []string `json:"domain_names"`
	ExpiresOn   string   `json:"expires_on"`
}

// CertificateBody is the request body that requests a certificate.
type CertificateBody struct {
	Provider    string   `json:"provider"`
	NiceName    string   `json:"nice_name,omitempty"`
	DomainNames []string `json:"domain_names"`
}

// MarshalJSON encodes a nil domain list as an empty array and always names the provider letsencrypt.
func (b CertificateBody) MarshalJSON() ([]byte, error) {
	type plain CertificateBody
	p := plain(b)
	p.Provider = "letsencrypt"
	if p.DomainNames == nil {
		p.DomainNames = []string{}
	}
	return json.Marshal(p)
}

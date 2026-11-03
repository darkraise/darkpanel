package npm

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// proxyhostList is npm.proxyhost.list: the proxy hosts of NPM ordered by id, each marked reserved or unclaimed.
func (n nm) proxyhostList() core.ActionDef {
	return core.DefineRead[ProxyHostListInput, ProxyHostList](core.ActionSpec{
		Name:    ID + ".proxyhost.list",
		Summary: "List the proxy hosts of Nginx Proxy Manager.",
		Version: 1,
		Side:    core.SideAgent,
	}, listProxyHostReader{n: n})
}

// accesslistList is npm.accesslist.list: the access lists of NPM ordered by id, each marked reserved or unclaimed.
func (n nm) accesslistList() core.ActionDef {
	return core.DefineRead[AccessListListInput, AccessListList](core.ActionSpec{
		Name:    ID + ".accesslist.list",
		Summary: "List the access lists of Nginx Proxy Manager.",
		Version: 1,
		Side:    core.SideAgent,
	}, listAccessListReader{n: n})
}

// certificateList is npm.certificate.list: the certificates of NPM ordered by id, each marked reserved or unclaimed.
func (n nm) certificateList() core.ActionDef {
	return core.DefineRead[CertificateListInput, CertificateList](core.ActionSpec{
		Name:    ID + ".certificate.list",
		Summary: "List the certificates of Nginx Proxy Manager.",
		Version: 1,
		Side:    core.SideAgent,
	}, listCertificateReader{n: n})
}

// listProxyHostReader is the handler of npm.proxyhost.list.
type listProxyHostReader struct{ n nm }

// Read lists the proxy hosts.
func (r listProxyHostReader) Read(ctx context.Context, env core.Env, _ ProxyHostListInput) (ProxyHostList, error) {
	api, err := r.n.listClient(ctx, env)
	if err != nil {
		return ProxyHostList{}, err
	}
	items, err := r.n.listProxyHostItems(ctx, env, api)
	if err != nil {
		return ProxyHostList{}, err
	}
	return ProxyHostList{Items: items}, nil
}

// listAccessListReader is the handler of npm.accesslist.list.
type listAccessListReader struct{ n nm }

// Read lists the access lists.
func (r listAccessListReader) Read(ctx context.Context, env core.Env, _ AccessListListInput) (AccessListList, error) {
	api, err := r.n.listClient(ctx, env)
	if err != nil {
		return AccessListList{}, err
	}
	items, err := r.n.listAccessListItems(ctx, env, api)
	if err != nil {
		return AccessListList{}, err
	}
	return AccessListList{Items: items}, nil
}

// listCertificateReader is the handler of npm.certificate.list.
type listCertificateReader struct{ n nm }

// Read lists the certificates.
func (r listCertificateReader) Read(ctx context.Context, env core.Env, _ CertificateListInput) (CertificateList, error) {
	api, err := r.n.listClient(ctx, env)
	if err != nil {
		return CertificateList{}, err
	}
	items, err := r.n.listCertificateItems(ctx, env, api)
	if err != nil {
		return CertificateList{}, err
	}
	return CertificateList{Items: items}, nil
}

// listClient returns a client for the stored admin of the host's instance.
func (n nm) listClient(ctx context.Context, env core.Env) (npmAPI, error) {
	_, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return nil, fmt.Errorf("npm: load the instance: %w", err)
	}
	if !ok {
		return nil, errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host")
	}
	api, err := n.d.api(ctx, env)
	if err != nil {
		return nil, errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not available")
	}
	return api, nil
}

// listMarks answers the reserved and unclaimed marks of the objects of one kind.
type listMarks struct {
	env     core.Env
	kind    string
	watched map[string]bool
}

// listMarks loads the watched identities of kind once.
func (n nm) listMarks(ctx context.Context, env core.Env, kind string) (listMarks, error) {
	w, err := watched(ctx, env.DB.Read(), kind)
	if err != nil {
		return listMarks{}, fmt.Errorf("npm: read the watched objects: %w", err)
	}
	return listMarks{env: env, kind: kind, watched: w}, nil
}

// of returns the owning action of object id, empty when it is not reserved, and whether identity is watched.
func (m listMarks) of(ctx context.Context, id int, identity string) (reserved string, unclaimed bool, err error) {
	row, ok, err := reservedOf(ctx, m.env.DB.Read(), m.kind, id)
	if err != nil {
		return "", false, fmt.Errorf("npm: read the reservations: %w", err)
	}
	if ok {
		reserved = row.Owner
	}
	return reserved, m.watched[identity], nil
}

// listDomainsIdentity is the sorted domain set joined by commas, the identity a creation effect recovers by.
func listDomainsIdentity(domains []string) string {
	s := slices.Clone(domains)
	sort.Strings(s)
	return strings.Join(s, ",")
}

func listUnhealthy(err error) error {
	return errcode.Wrap(errcode.NPMUnhealthy, err, "the Nginx Proxy Manager API is not available")
}

func (n nm) listProxyHostItems(ctx context.Context, env core.Env, api npmAPI) ([]ProxyHostView, error) {
	hosts, err := api.ProxyHosts(ctx)
	if err != nil {
		return nil, listUnhealthy(err)
	}
	marks, err := n.listMarks(ctx, env, "proxy_host")
	if err != nil {
		return nil, err
	}
	sort.Slice(hosts, func(i, j int) bool { return hosts[i].ID < hosts[j].ID })
	items := make([]ProxyHostView, 0, len(hosts))
	for _, h := range hosts {
		reserved, unclaimed, err := marks.of(ctx, h.ID, listDomainsIdentity(h.DomainNames))
		if err != nil {
			return nil, err
		}
		items = append(items, ProxyHostView{
			ID: h.ID, Domains: append([]string{}, h.DomainNames...),
			ForwardScheme: h.ForwardScheme, ForwardHost: h.ForwardHost, ForwardPort: h.ForwardPort,
			CertificateID: h.CertificateID, AccessListID: h.AccessListID,
			SSLForced: h.SSLForced, HSTS: h.HSTSEnabled, Websockets: h.AllowWebsocketUpgrade,
			Reserved: reserved, Unclaimed: unclaimed,
		})
	}
	return items, nil
}

func (n nm) listAccessListItems(ctx context.Context, env core.Env, api npmAPI) ([]AccessListView, error) {
	lists, err := api.AccessLists(ctx)
	if err != nil {
		return nil, listUnhealthy(err)
	}
	marks, err := n.listMarks(ctx, env, "access_list")
	if err != nil {
		return nil, err
	}
	sort.Slice(lists, func(i, j int) bool { return lists[i].ID < lists[j].ID })
	items := make([]AccessListView, 0, len(lists))
	for _, l := range lists {
		reserved, unclaimed, err := marks.of(ctx, l.ID, l.Name)
		if err != nil {
			return nil, err
		}
		clients := make([]AccessClientInput, 0, len(l.Clients))
		for _, c := range l.Clients {
			clients = append(clients, AccessClientInput{Address: c.Address, Directive: c.Directive})
		}
		items = append(items, AccessListView{
			ID: l.ID, Name: l.Name, SatisfyAny: l.SatisfyAny, Clients: clients,
			Reserved: reserved, Unclaimed: unclaimed,
		})
	}
	return items, nil
}

func (n nm) listCertificateItems(ctx context.Context, env core.Env, api npmAPI) ([]CertificateView, error) {
	certs, err := api.Certificates(ctx)
	if err != nil {
		return nil, listUnhealthy(err)
	}
	marks, err := n.listMarks(ctx, env, "certificate")
	if err != nil {
		return nil, err
	}
	sort.Slice(certs, func(i, j int) bool { return certs[i].ID < certs[j].ID })
	items := make([]CertificateView, 0, len(certs))
	for _, c := range certs {
		reserved, unclaimed, err := marks.of(ctx, c.ID, listDomainsIdentity(c.DomainNames))
		if err != nil {
			return nil, err
		}
		items = append(items, CertificateView{
			ID: c.ID, Name: c.NiceName, Domains: append([]string{}, c.DomainNames...), ExpiresOn: c.ExpiresOn,
			Reserved: reserved, Unclaimed: unclaimed,
		})
	}
	return items, nil
}

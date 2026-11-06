package npm

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestSdWireTypesExist names every wire type, so a missing or renamed one
// fails the build.
func TestSdWireTypesExist(t *testing.T) {
	all := []any{
		DeployInput{}, Deployed{}, InstanceStatus{}, AdminExposure{}, ConfirmPublicInput{}, PublicMark{}, RemoveInput{}, Removed{},
		AttachInput{}, Attachment{}, DetachInput{}, ProxyHostInput{}, ProxyHostUpdateInput{}, ProxyHostRemoveInput{}, ProxyHostView{},
		AccessClientInput{}, AccessListInput{}, AccessListRemoveInput{}, AccessListView{}, CertificateInput{}, CertificateRemoveInput{},
		CertificateView{}, ProxyHostList{}, AccessListList{}, CertificateList{}, RevealInput{}, AdminPassword{}, UpgradeInput{}, Upgraded{},
		CheckInput{}, CheckResult{}, PublicInput{}, PublicResult{}, HostInstance{}, InstancesOutput{}, DeployPublicInput{}, ExposeInput{},
		ExposeRemoveInput{}, AdminExposeInput{}, AdminUnexposeInput{}, DeployPublicOutput{}, ExposeOutput{}, ExposeRemoveOutput{},
		AdminExposeOutput{}, AdminUnexposeOutput{}, StatusInput{}, ProxyHostListInput{}, AccessListListInput{}, CertificateListInput{}, InstanceListInput{},
	}
	if len(all) != 50 {
		t.Errorf("%d wire types listed, want 50", len(all))
	}
}

func TestSdWireJSON(t *testing.T) {
	hosts := []string{"a.example.org"}
	clients := []AccessClientInput{{Address: "10.0.0.0/8", Directive: "allow"}}
	proxyHost := ProxyHostInput{Domains: hosts, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 8080}
	full := proxyHost
	full.CertificateID, full.AccessListID, full.SSLForced, full.HSTS, full.Websockets = 3, 4, true, true, true
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"deploy input", DeployInput{AcmeEmail: "ops@darkpanel.dev"}, `{"acmeEmail":"ops@darkpanel.dev"}`},
		{"deployed", Deployed{ContainerID: "c1", Image: "img", AdminEmail: "ops@darkpanel.dev"}, `{"containerId":"c1","image":"img","adminEmail":"ops@darkpanel.dev"}`},
		{"status without an admin exposure", InstanceStatus{Deployed: true, ContainerID: "c1", Image: "i1", PinnedImage: "i2", Running: true, APIReady: true,
			Public: "confirmed", AdminEmail: "ops@darkpanel.dev", UpgradeAvailable: true},
			`{"deployed":true,"containerId":"c1","image":"i1","pinnedImage":"i2","running":true,"apiReady":true,"public":"confirmed",` +
				`"adminEmail":"ops@darkpanel.dev","adminExposure":null,"upgradeAvailable":true}`},
		{"status with an admin exposure", InstanceStatus{Public: "no", AdminExposure: &AdminExposure{Hostname: "npm.example.org", ProxyHostID: 7, CertificateID: 9}},
			`{"deployed":false,"containerId":"","image":"","pinnedImage":"","running":false,"apiReady":false,"public":"no","adminEmail":"",` +
				`"adminExposure":{"hostname":"npm.example.org","proxyHostId":7,"certificateId":9},"upgradeAvailable":false}`},
		{"public mark", PublicMark{Public: "confirmed"}, `{"public":"confirmed"}`},
		{"removed", Removed{ContainerID: "c1"}, `{"containerId":"c1"}`},
		{"attach input", AttachInput{Network: "n", Claimant: "c"}, `{"network":"n","claimant":"c"}`},
		{"attach input with a target and an address", AttachInput{Network: "n", Claimant: "expose:a.example.org", Target: "web", Address: netip.MustParseAddr("172.31.255.2")},
			`{"network":"n","claimant":"expose:a.example.org","target":"web","address":"172.31.255.2"}`},
		{"attachment", Attachment{Network: "n", Claimant: "c", Attached: true}, `{"network":"n","claimant":"c","preexisting":false,"attached":true}`},
		{"detach input for every network", DetachInput{Claimant: "c"}, `{"network":"","claimant":"c"}`},
		{"proxy host input", proxyHost, `{"domains":["a.example.org"],"forwardScheme":"http","forwardHost":"web","forwardPort":8080}`},
		{"proxy host input in full", full,
			`{"domains":["a.example.org"],"forwardScheme":"http","forwardHost":"web","forwardPort":8080,"certificateId":3,"accessListId":4,"sslForced":true,"hsts":true,"websockets":true}`},
		{"proxy host update input", ProxyHostUpdateInput{ID: 5, ProxyHostInput: proxyHost},
			`{"id":5,"domains":["a.example.org"],"forwardScheme":"http","forwardHost":"web","forwardPort":8080}`},
		{"proxy host remove by id", ProxyHostRemoveInput{ID: 5}, `{"id":5}`},
		{"proxy host remove by domain", ProxyHostRemoveInput{Domain: "a.example.org"}, `{"domain":"a.example.org"}`},
		{"proxy host view", ProxyHostView{ID: 5, Domains: hosts, ForwardScheme: "http", ForwardHost: "web", ForwardPort: 80, CertificateID: 3, SSLForced: true, HSTS: true},
			`{"id":5,"domains":["a.example.org"],"forwardScheme":"http","forwardHost":"web","forwardPort":80,"certificateId":3,"accessListId":0,` +
				`"sslForced":true,"hsts":true,"websockets":false,"reserved":"","unclaimed":false}`},
		{"access list input", AccessListInput{SatisfyAny: true, Clients: clients}, `{"satisfyAny":true,"clients":[{"address":"10.0.0.0/8","directive":"allow"}]}`},
		{"access list input without satisfyAny", AccessListInput{Clients: clients}, `{"clients":[{"address":"10.0.0.0/8","directive":"allow"}]}`},
		{"access list remove input", AccessListRemoveInput{ID: 2}, `{"id":2}`},
		{"access list view", AccessListView{ID: 2, Name: "darkpanel-c1-3", Clients: clients, Reserved: "npm.admin.expose"},
			`{"id":2,"name":"darkpanel-c1-3","satisfyAny":false,"clients":[{"address":"10.0.0.0/8","directive":"allow"}],"reserved":"npm.admin.expose","unclaimed":false}`},
		{"certificate input", CertificateInput{Domains: hosts}, `{"domains":["a.example.org"]}`},
		{"certificate remove input", CertificateRemoveInput{ID: 9}, `{"id":9}`},
		{"certificate view", CertificateView{ID: 9, Name: "n", Domains: hosts, ExpiresOn: "2027-01-01T00:00:00Z", Unclaimed: true},
			`{"id":9,"name":"n","domains":["a.example.org"],"expiresOn":"2027-01-01T00:00:00Z","reserved":"","unclaimed":true}`},
		{"empty proxy host list", ProxyHostList{Items: []ProxyHostView{}}, `{"items":[]}`},
		{"empty access list list", AccessListList{Items: []AccessListView{}}, `{"items":[]}`},
		{"empty certificate list", CertificateList{Items: []CertificateView{}}, `{"items":[]}`},
		{"admin password", AdminPassword{Email: "ops@darkpanel.dev", Password: "pw"}, `{"email":"ops@darkpanel.dev","password":"pw"}`},
		{"upgraded", Upgraded{FromImage: "a", ToImage: "b"}, `{"fromImage":"a","toImage":"b"}`},
		{"check input", CheckInput{HostID: "h1", Integration: true}, `{"hostId":"h1","integration":true}`},
		{"check result", CheckResult{Integration: true}, `{"integration":true}`},
		{"public input", PublicInput{HostID: "h1"}, `{"hostId":"h1"}`},
		{"public result", PublicResult{Public: true, Via: "mark"}, `{"public":true,"via":"mark"}`},
		{"instances output", InstancesOutput{
			Instances:  []HostInstance{{HostID: "h1", HostName: "alpha", Status: InstanceStatus{PinnedImage: "i2", Public: "no"}}},
			Unreadable: []string{"h2"},
		},
			`{"instances":[{"hostId":"h1","hostName":"alpha","status":{"deployed":false,"containerId":"","image":"","pinnedImage":"i2","running":false,` +
				`"apiReady":false,"public":"no","adminEmail":"","adminExposure":null,"upgradeAvailable":false}}],"unreadable":["h2"]}`},
		{"instances output without unreadable hosts", InstancesOutput{Instances: []HostInstance{}, Unreadable: []string{}}, `{"instances":[],"unreadable":[]}`},
		{"deploy public input", DeployPublicInput{HostID: "h1", AcmeEmail: "ops@darkpanel.dev", Integration: true},
			`{"hostId":"h1","acmeEmail":"ops@darkpanel.dev","integration":true}`},
		{"expose input", ExposeInput{HostID: "h1", Container: "web", Port: 8080, Network: "app", Domains: hosts},
			`{"hostId":"h1","container":"web","port":8080,"network":"app","domains":["a.example.org"]}`},
		{"expose input in full", ExposeInput{HostID: "h1", Container: "web", Port: 8080, Network: "app", Domains: hosts, Websockets: true, AccessListID: 4},
			`{"hostId":"h1","container":"web","port":8080,"network":"app","domains":["a.example.org"],"websockets":true,"accessListId":4}`},
		{"expose remove input", ExposeRemoveInput{HostID: "h1", Domain: "a.example.org"}, `{"hostId":"h1","domain":"a.example.org"}`},
		{"admin expose input", AdminExposeInput{HostID: "h1", Hostname: "npm.example.org"}, `{"hostId":"h1","hostname":"npm.example.org"}`},
		{"admin expose input with an access list", AdminExposeInput{HostID: "h1", Hostname: "npm.example.org", AccessListID: 4},
			`{"hostId":"h1","hostname":"npm.example.org","accessListId":4}`},
		{"admin unexpose input", AdminUnexposeInput{HostID: "h1", Hostname: "npm.example.org"}, `{"hostId":"h1","hostname":"npm.example.org"}`},
		{"deploy public output", DeployPublicOutput{ContainerID: "c1", PortsOpened: true}, `{"containerId":"c1","portsOpened":true}`},
		{"expose output", ExposeOutput{ProxyHostID: 7, CertificateID: 9}, `{"proxyHostId":7,"certificateId":9}`},
		{"expose remove output", ExposeRemoveOutput{Domain: "a.example.org"}, `{"domain":"a.example.org"}`},
		{"admin expose output", AdminExposeOutput{ProxyHostID: 7, CertificateID: 9}, `{"proxyHostId":7,"certificateId":9}`},
		{"admin unexpose output", AdminUnexposeOutput{Hostname: "npm.example.org"}, `{"hostname":"npm.example.org"}`},
		{"confirm public input", ConfirmPublicInput{}, `{}`},
		{"remove input", RemoveInput{}, `{}`},
		{"reveal input", RevealInput{}, `{}`},
		{"upgrade input", UpgradeInput{}, `{}`},
		{"status input", StatusInput{}, `{}`},
		{"proxy host list input", ProxyHostListInput{}, `{}`},
		{"access list list input", AccessListListInput{}, `{}`},
		{"certificate list input", CertificateListInput{}, `{}`},
		{"instance list input", InstanceListInput{}, `{}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

// TestSdWireSecretTags: the admin password is the only wire field marked secret.
func TestSdWireSecretTags(t *testing.T) {
	f, _ := reflect.TypeOf(AdminPassword{}).FieldByName("Password")
	if f.Tag.Get("secret") != "true" || f.Tag.Get("json") != "password" {
		t.Errorf("AdminPassword.Password tag %q, want json:\"password\" secret:\"true\"", f.Tag)
	}
	e, _ := reflect.TypeOf(AdminPassword{}).FieldByName("Email")
	if e.Tag.Get("secret") != "" {
		t.Errorf("AdminPassword.Email tag %q, want no secret tag", e.Tag)
	}
}

// TestSdWireSchemas: every input and output type reflects into a schema, and
// the admin password's schema marks it secret.
func TestSdWireSchemas(t *testing.T) {
	inputs := []any{
		DeployInput{}, ConfirmPublicInput{}, RemoveInput{}, AttachInput{}, DetachInput{}, ProxyHostInput{}, ProxyHostUpdateInput{},
		ProxyHostRemoveInput{}, AccessListInput{}, AccessListRemoveInput{}, CertificateInput{}, CertificateRemoveInput{}, RevealInput{},
		UpgradeInput{}, CheckInput{}, PublicInput{}, DeployPublicInput{}, ExposeInput{}, ExposeRemoveInput{}, AdminExposeInput{},
		AdminUnexposeInput{}, StatusInput{}, ProxyHostListInput{}, AccessListListInput{}, CertificateListInput{}, InstanceListInput{},
	}
	for _, v := range inputs {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input %T: %v", v, err)
		}
	}
	outputs := []any{
		Deployed{}, InstanceStatus{}, PublicMark{}, Removed{}, Attachment{}, ProxyHostView{}, AccessListView{}, CertificateView{},
		ProxyHostList{}, AccessListList{}, CertificateList{}, AdminPassword{}, Upgraded{}, CheckResult{}, PublicResult{}, InstancesOutput{},
		DeployPublicOutput{}, ExposeOutput{}, ExposeRemoveOutput{}, AdminExposeOutput{}, AdminUnexposeOutput{},
	}
	for _, v := range outputs {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output %T: %v", v, err)
		}
	}
	b, err := schema.Output(reflect.TypeOf(AdminPassword{}))
	if err != nil || !strings.Contains(string(b), `"x-secret":true`) {
		t.Errorf("AdminPassword schema %s (error %v), want the password marked x-secret", b, err)
	}
}

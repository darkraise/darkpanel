package npm

import "github.com/darkraise/darkpanel/internal/modules/firewall/model"

// PortsLabel is the comment of the UFW rules npm.instance.deploy-public v2 adds for ports 80 and 443.
const PortsLabel = "npm"

// The ids of the UFW rules for ports 80 and 443, the same on every host: each is "0" and the first 25 characters of
// the Crockford base32 of sha256("darkpanel/npm/ports/<port>/<family>"), as DERP derives its rule ids. Fixed ids
// make a plan and its apply build the same steps, and a deploy run again finds its rules present.
const (
	PortRule80V4  = "0CGS78T46E9GTMW1GWTK2TV0T5"
	PortRule443V4 = "0GCPW66GP51CA48ZNYH48M71YX"
	PortRule80V6  = "0QSMKDEJA5YXDMQM1MNS2FXN05"
	PortRule443V6 = "0XZS5RBT87XEQW3BMX0WTC6YQW"
)

// PortRules are the rules that admit ports 80 and 443 over TCP from anywhere: 80/v4 and 443/v4, then 80/v6 and 443/v6
// when ipv6. They carry no comment, since firewall.rules.apply's label becomes it.
func PortRules(ipv6 bool) []model.Rule {
	rule := func(id, port string, fam model.Family) model.Rule {
		return model.Rule{ID: id, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP, From: model.Any,
			To: model.Any, ToPort: port, Family: fam}
	}
	rules := []model.Rule{rule(PortRule80V4, "80", model.FamilyV4), rule(PortRule443V4, "443", model.FamilyV4)}
	if ipv6 {
		rules = append(rules, rule(PortRule80V6, "80", model.FamilyV6), rule(PortRule443V6, "443", model.FamilyV6))
	}
	return rules
}

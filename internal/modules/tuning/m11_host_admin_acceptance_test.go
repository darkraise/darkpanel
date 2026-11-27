package tuning_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/tuning"
)

type files struct{ core.FileWriter }

type runner struct{}

func (runner) Run(context.Context, core.Cmd) (core.CmdResult, error) { return core.CmdResult{}, nil }
func (runner) Stream(context.Context, string, []string, func([]byte) error) error {
	return nil
}

var admin = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}

func plan(t *testing.T, values map[string]string) error {
	t.Helper()
	m := tuning.Module(firewall.Module())
	var def core.ActionDef
	for _, a := range m.Manifest().Actions {
		if a.Spec.Name == "tuning.sysctl.apply" {
			def = a
		}
	}
	raw, _ := json.Marshal(map[string]any{"values": values})
	_, err := def.Handler.Plan(context.Background(), core.Env{HostID: "h1", Principal: admin, Exec: runner{}, Files: files{}}, raw)
	return err
}

func TestA56_KeysAndValuesOutsideTheCuratedListAreRefused(t *testing.T) {
	cases := map[string]map[string]string{
		"a key outside the list":                      {"kernel.panic": "5"},
		"another module's key (ip forwarding)":        {"net.ipv4.ip_forward": "1"},
		"another module's key (ipv6 forwarding)":      {"net.ipv6.conf.all.forwarding": "1"},
		"swappiness above 200":                        {"vm.swappiness": "201"},
		"swappiness below 0":                          {"vm.swappiness": "-1"},
		"swappiness not a number":                     {"vm.swappiness": "lots"},
		"vfs_cache_pressure 0":                        {"vm.vfs_cache_pressure": "0"},
		"dirty_ratio 101":                             {"vm.dirty_ratio": "101"},
		"overcommit_memory 3":                         {"vm.overcommit_memory": "3"},
		"max_map_count below 65530":                   {"vm.max_map_count": "65529"},
		"inotify watches below 8192":                  {"fs.inotify.max_user_watches": "8191"},
		"somaxconn above 65535":                       {"net.core.somaxconn": "65536"},
		"tcp_fastopen 4":                              {"net.ipv4.tcp_fastopen": "4"},
		"tcp_tw_reuse 3":                              {"net.ipv4.tcp_tw_reuse": "3"},
		"default_qdisc not in the list":               {"net.core.default_qdisc": "htb"},
		"a port range with low above high":            {"net.ipv4.ip_local_port_range": "60000 1024"},
		"a value with a newline carrying another key": {"vm.swappiness": "10\nkernel.panic = 1"},
	}
	for name, v := range cases {
		err := plan(t, v)
		if err == nil {
			t.Errorf("%s: planned", name)
			continue
		}
		if code := errcode.From(err).Code; code == errcode.Internal {
			t.Errorf("%s: answered %v, an internal error; want a refusal of the input", name, err)
		}
	}
}

func TestA56_InRangeValuesAreNotRefusedAsInput(t *testing.T) {
	for _, v := range []map[string]string{
		{"vm.swappiness": "10"}, {"vm.swappiness": "200"}, {"vm.swappiness": "0"}, {"net.core.default_qdisc": "fq"},
		{"net.ipv4.ip_local_port_range": "1024 60000"}, {"vm.max_map_count": "262144"},
	} {
		func() {
			// Past validation the plan reads the host through the fakes, which hold nothing; a panic there means the input was accepted.
			defer func() { _ = recover() }()
			if err := plan(t, v); err != nil && errcode.From(err).Code == errcode.InvalidInput {
				t.Errorf("%v refused as input: %v", v, err)
			}
		}()
	}
}

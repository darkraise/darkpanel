package conf

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

const (
	confTestUFWConf = "# /etc/ufw/ufw.conf\n#\n\n# Set to yes to start on boot.\nENABLED=yes\n\n# Please use the 'ufw' command to set the loglevel.\nLOGLEVEL=low\n"
	confTestDefault = "# /etc/default/ufw\n#\n\nIPV6=yes\n\nDEFAULT_INPUT_POLICY=\"DROP\"\nDEFAULT_OUTPUT_POLICY=\"ACCEPT\"\n" +
		"DEFAULT_FORWARD_POLICY=\"DROP\"\nDEFAULT_APPLICATION_POLICY=\"SKIP\"\nMANAGE_BUILTINS=no\n\n" +
		"# Location of the sysctl file\nIPT_SYSCTL=/etc/ufw/sysctl.conf\n\nIPT_MODULES=\"\"\n"
	confTestSysctl = "#\n# Configuration file for setting network variables.\n#\n\n" +
		"net/ipv4/conf/all/accept_redirects=0\n#net/ipv4/ip_forward=1\n  net.ipv4.ip_forward = 1 \n" +
		"; a comment\n-net/ipv6/conf/default/forwarding=1\nnet.ipv4.conf.eth0/100.rp_filter=2\nnot a setting\n"
	confTestUser = "*filter\n:ufw-user-input - [0:0]\n### RULES ###\n\n" +
		"### tuple ### allow tcp 22 0.0.0.0/0 any 0.0.0.0/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368\n" +
		"-A ufw-user-input -p tcp --dport 22 -j ACCEPT\n\n" +
		"### tuple ### deny any any 0.0.0.0/0 any 10.0.0.0/8 in\n-A ufw-user-input -s 10.0.0.0/8 -j DROP\n\n### END RULES ###\nCOMMIT\n"
	confTestUser6 = "*filter\n:ufw6-user-input - [0:0]\n### RULES ###\n\n" +
		"### tuple ### allow tcp 22 ::/0 any ::/0 in comment=6461726b70616e656c3a30314a303030303030303030303030303030303030303030303020737368\n" +
		"-A ufw6-user-input -p tcp --dport 22 -j ACCEPT\n\n### END RULES ###\nCOMMIT\n"
)

// confTestFiles is a stock tree: every state file, the sysctl file and
// non-executable init scripts, keyed by path relative to the root.
func confTestFiles() map[string]string {
	return map[string]string{
		"etc/ufw/ufw.conf": confTestUFWConf, "etc/default/ufw": confTestDefault, "etc/ufw/sysctl.conf": confTestSysctl,
		"etc/ufw/user.rules": confTestUser, "etc/ufw/user6.rules": confTestUser6,
		"etc/ufw/before.rules": "*filter\nCOMMIT\n", "etc/ufw/before6.rules": "*filter\nCOMMIT\n",
		"etc/ufw/after.rules": "*filter\nCOMMIT\n", "etc/ufw/after6.rules": "*filter\nCOMMIT\n",
		"etc/ufw/before.init": "#!/bin/sh\n", "etc/ufw/after.init": "#!/bin/sh\n",
	}
}

// confTestTree writes files under a new temporary root; a "" content
// leaves the file out.
func confTestTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		if content == "" {
			continue
		}
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRead(t *testing.T) {
	root := confTestTree(t, confTestFiles())
	c, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled || c.LogLevel != "low" || !c.IPv6 || c.ManageBuiltins || c.DefaultInput != "DROP" ||
		c.DefaultOutput != "ACCEPT" || c.DefaultForward != "DROP" || c.SysctlFile != "/etc/ufw/sysctl.conf" {
		t.Errorf("settings = %+v", c)
	}
	if len(c.Unsupported) != 0 {
		t.Errorf("Unsupported = %q, want none", c.Unsupported)
	}
	wantSysctl := []fwstate.SysctlSetting{{Key: "net/ipv4/conf/all/accept_redirects", Value: "0"},
		{Key: "net/ipv4/ip_forward", Value: "1"}, {Key: "net/ipv6/conf/default/forwarding", Value: "1"},
		{Key: "net/ipv4/conf/eth0.100/rp_filter", Value: "2"}}
	if !reflect.DeepEqual(c.Sysctl, wantSysctl) {
		t.Errorf("Sysctl = %q, want %q", c.Sysctl, wantSysctl)
	}
	if len(c.Rules4) != 2 || len(c.Rules6) != 1 {
		t.Fatalf("rules = %d v4, %d v6; want 2, 1", len(c.Rules4), len(c.Rules6))
	}
	if c.Rules4[1].Src != "10.0.0.0/8" || c.Rules4[1].Action != model.ActionDeny || c.Rules6[0].Family != model.FamilyV6 {
		t.Errorf("rules = %+v %+v", c.Rules4, c.Rules6)
	}
	if rules := model.Merge(c.Rules4, c.Rules6); len(rules) != 2 || rules[0].Family != model.FamilyBoth {
		t.Errorf("merged rules = %+v", rules)
	}
	wantPaths := append(slices.Clone(fwstate.StateFiles), "/etc/ufw/sysctl.conf")
	gotPaths := make([]string, 0, len(c.Files))
	for p, e := range c.Files {
		gotPaths = append(gotPaths, p)
		if e.Path != p || !e.Present || e.Mode != 0o600 || e.UID != os.Getuid() || e.GID != os.Getgid() {
			t.Errorf("Files[%s] = %+v", p, e)
		}
	}
	slices.Sort(gotPaths)
	slices.Sort(wantPaths)
	if !slices.Equal(gotPaths, wantPaths) {
		t.Errorf("Files paths = %q, want %q", gotPaths, wantPaths)
	}
	if string(c.Files["/etc/default/ufw"].Data) != confTestDefault {
		t.Error("Files[/etc/default/ufw] does not hold the file's bytes")
	}
}

func TestReadDefaults(t *testing.T) {
	files := confTestFiles()
	files["etc/ufw/ufw.conf"] = "LOGLEVEL=FULL\n"
	files["etc/default/ufw"] = "IPV6=NO\nDEFAULT_INPUT_POLICY=REJECT\nDEFAULT_OUTPUT_POLICY=\"DROP\"\nDEFAULT_FORWARD_POLICY=\"ACCEPT\"\n"
	files["etc/ufw/user6.rules"] = ""
	files["etc/ufw/before6.rules"] = ""
	files["etc/ufw/sysctl.conf"] = ""
	c, err := Read(confTestTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled || c.LogLevel != "full" || c.IPv6 || c.DefaultInput != "REJECT" || c.DefaultOutput != "DROP" ||
		c.DefaultForward != "ACCEPT" || c.SysctlFile != "" || len(c.Sysctl) != 0 || len(c.Unsupported) != 0 {
		t.Errorf("config = %+v", c)
	}
	if e := c.Files["/etc/ufw/user6.rules"]; e.Present || e.Path != "/etc/ufw/user6.rules" {
		t.Errorf("absent user6.rules = %+v", e)
	}
	if e, ok := c.Files["/etc/ufw/sysctl.conf"]; !ok || e.Present {
		t.Errorf("Files[/etc/ufw/sysctl.conf] = %+v (recorded %v), want an absent entry although IPT_SYSCTL is unset", e, ok)
	}
}

// TestReadSysctlFileWithoutName: UFW's sysctl file is part of the state
// whether or not IPT_SYSCTL names it, present or absent, but its settings
// count only while IPT_SYSCTL names it.
func TestReadSysctlFileWithoutName(t *testing.T) {
	files := confTestFiles()
	files["etc/default/ufw"] = strings.Replace(confTestDefault, "IPT_SYSCTL=/etc/ufw/sysctl.conf\n", "", 1)
	files["etc/ufw/sysctl.conf"] = "net.ipv4.conf.all.forwarding=1\nnet.ipv4.ip_forward=1\n"
	root := confTestTree(t, files)
	c, err := Read(root)
	if err != nil {
		t.Fatal(err)
	}
	if e := c.Files["/etc/ufw/sysctl.conf"]; !e.Present || string(e.Data) != files["etc/ufw/sysctl.conf"] || e.Path != "/etc/ufw/sysctl.conf" {
		t.Errorf("Files[/etc/ufw/sysctl.conf] = %+v, want the file's bytes", e)
	}
	if c.SysctlFile != "" || len(c.Sysctl) != 0 || len(c.Unsupported) != 0 {
		t.Errorf("SysctlFile %q, %d settings, Unsupported %q; want the file ignored while IPT_SYSCTL is unset", c.SysctlFile, len(c.Sysctl), c.Unsupported)
	}
	confTestRemove(t, filepath.Join(root, "etc/ufw/sysctl.conf"))
	if c, err = Read(root); err != nil {
		t.Fatal(err)
	}
	if e, ok := c.Files["/etc/ufw/sysctl.conf"]; !ok || e.Present || e.Path != "/etc/ufw/sysctl.conf" {
		t.Errorf("Files[/etc/ufw/sysctl.conf] = %+v (recorded %v), want an absent entry", e, ok)
	}
}

func TestReadUnsupported(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, root string, files map[string]string)
		want string
	}{
		{"before.init executable", func(t *testing.T, root string, _ map[string]string) {
			confTestChmod(t, filepath.Join(root, "etc/ufw/before.init"), 0o750)
		}, "before.init"},
		{"after.init executable", func(t *testing.T, root string, _ map[string]string) {
			confTestChmod(t, filepath.Join(root, "etc/ufw/after.init"), 0o700)
		}, "after.init"},
		{"manage builtins", confTestSet("etc/default/ufw", "MANAGE_BUILTINS=no", "MANAGE_BUILTINS=yes"), "MANAGE_BUILTINS"},
		{"ipv4 conf forwarding", confTestSet("etc/ufw/sysctl.conf", "#net/ipv4/ip_forward=1", "net.ipv4.conf.all.forwarding=1"), "net/ipv4/conf/all/forwarding"},
		{"route_localnet", confTestSet("etc/ufw/sysctl.conf", "#net/ipv4/ip_forward=1", "net/ipv4/conf/eth0/route_localnet=1"), "route_localnet"},
		{"bridge netfilter", confTestSet("etc/ufw/sysctl.conf", "#net/ipv4/ip_forward=1", "net.bridge.bridge-nf-call-iptables = 1"), "bridge-nf-call-iptables"},
		{"glob key", confTestSet("etc/ufw/sysctl.conf", "#net/ipv4/ip_forward=1", "net.ipv4.conf.*.rp_filter=1"), "glob"},
		{"enabled neither yes nor no", confTestSet("etc/ufw/ufw.conf", "ENABLED=yes", "ENABLED=Yes"), "ENABLED"},
		{"ipv6 neither yes nor no", confTestSet("etc/default/ufw", "IPV6=yes", "IPV6=true"), "IPV6"},
		{"bad loglevel", confTestSet("etc/ufw/ufw.conf", "LOGLEVEL=low", "LOGLEVEL=loud"), "LOGLEVEL"},
		{"missing loglevel", confTestSet("etc/ufw/ufw.conf", "LOGLEVEL=low", ""), "LOGLEVEL"},
		{"lower-case policy", confTestSet("etc/default/ufw", `DEFAULT_INPUT_POLICY="DROP"`, "DEFAULT_INPUT_POLICY=drop"), "DEFAULT_INPUT_POLICY"},
		{"missing policy", confTestSet("etc/default/ufw", `DEFAULT_FORWARD_POLICY="DROP"`, ""), "DEFAULT_FORWARD_POLICY"},
		{"relative sysctl file", confTestSet("etc/default/ufw", "IPT_SYSCTL=/etc/ufw/sysctl.conf", "IPT_SYSCTL=sysctl.conf"), `IPT_SYSCTL names "sysctl.conf"`},
		{"malformed tuple", confTestSet("etc/ufw/user.rules", "### tuple ### deny any any 0.0.0.0/0 any 10.0.0.0/8 in",
			"### tuple ### drop any any 0.0.0.0/0 any 10.0.0.0/8 in"), "user.rules line 8"},
		{"missing user.rules", func(t *testing.T, root string, _ map[string]string) {
			confTestRemove(t, filepath.Join(root, "etc/ufw/user.rules"))
		}, "/etc/ufw/user.rules is missing"},
		{"missing after6.rules", func(t *testing.T, root string, _ map[string]string) {
			confTestRemove(t, filepath.Join(root, "etc/ufw/after6.rules"))
		}, "/etc/ufw/after6.rules is missing"},
		{"two assignments", confTestSet("etc/default/ufw", "MANAGE_BUILTINS=no", "IPV6=yes MANAGE_BUILTINS=yes"), "/etc/default/ufw line 10"},
		{"export", confTestSet("etc/default/ufw", "MANAGE_BUILTINS=no", "export MANAGE_BUILTINS=yes"), "/etc/default/ufw line 10"},
		{"conditional", confTestSet("etc/default/ufw", "MANAGE_BUILTINS=no", "[ -e /x ] && MANAGE_BUILTINS=yes"), "/etc/default/ufw line 10"},
		{"trailing comment", confTestSet("etc/default/ufw", "MANAGE_BUILTINS=no", "MANAGE_BUILTINS=no # c"), "/etc/default/ufw line 10"},
		{"command after assignment", confTestSet("etc/default/ufw", "IPV6=yes", "IPV6=yes foo"), "/etc/default/ufw line 4"},
		{"text after quotes", confTestSet("etc/default/ufw", "IPV6=yes", `IPV6="yes"x`), "/etc/default/ufw line 4"},
		{"lower-case key", confTestSet("etc/default/ufw", "IPV6=yes", "ipv6=yes"), "/etc/default/ufw line 4"},
		{"indented", confTestSet("etc/default/ufw", "IPV6=yes", "  IPV6=yes"), "/etc/default/ufw line 4"},
		{"single quotes", confTestSet("etc/default/ufw", `IPT_MODULES=""`, "IPT_MODULES=''"), "/etc/default/ufw line 15"},
		{"carriage return", confTestSet("etc/ufw/ufw.conf", "ENABLED=yes", "ENABLED=yes\r"), "/etc/ufw/ufw.conf line 5"},
		{"after.init directory", func(t *testing.T, root string, _ map[string]string) {
			p := filepath.Join(root, "etc/ufw/after.init")
			confTestRemove(t, p)
			if err := os.Mkdir(p, 0o750); err != nil {
				t.Fatal(err)
			}
		}, "after.init"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			files := confTestFiles()
			root := confTestTree(t, files)
			c.edit(t, root, files)
			got, err := Read(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Unsupported) != 1 || !strings.Contains(got.Unsupported[0], c.want) {
				t.Fatalf("Unsupported = %q, want one reason naming %q", got.Unsupported, c.want)
			}
		})
	}
}

// confTestSet returns an edit that replaces old with replacement in one
// file of the tree.
func confTestSet(rel, old, replacement string) func(t *testing.T, root string, files map[string]string) {
	return func(t *testing.T, root string, files map[string]string) {
		t.Helper()
		if !strings.Contains(files[rel], old) {
			t.Fatalf("%s does not contain %q", rel, old)
		}
		data := strings.Replace(files[rel], old, replacement, 1)
		if err := os.WriteFile(filepath.Join(root, rel), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func confTestChmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func confTestRemove(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
}

// TestReadOtherSysctlFile checks that a sysctl file other than UFW's default
// is unsupported and never read, while Files still records the default one.
func TestReadOtherSysctlFile(t *testing.T) {
	files := confTestFiles()
	files["etc/default/ufw"] = strings.Replace(confTestDefault, "IPT_SYSCTL=/etc/ufw/sysctl.conf", "IPT_SYSCTL=/etc/sysctl.d/99-ufw.conf", 1)
	files["etc/sysctl.d/99-ufw.conf"] = "net.ipv4.conf.all.forwarding=1\n"
	c, err := Read(confTestTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unsupported) != 1 || !strings.Contains(c.Unsupported[0], `IPT_SYSCTL names "/etc/sysctl.d/99-ufw.conf"`) {
		t.Errorf("Unsupported = %q, want one reason naming the path", c.Unsupported)
	}
	if c.SysctlFile != "/etc/sysctl.d/99-ufw.conf" || len(c.Sysctl) != 0 {
		t.Errorf("SysctlFile = %q, Sysctl = %q; want the named path and no settings", c.SysctlFile, c.Sysctl)
	}
	if _, ok := c.Files["/etc/sysctl.d/99-ufw.conf"]; ok {
		t.Error("Files records the unsupported sysctl file")
	}
	if e := c.Files["/etc/ufw/sysctl.conf"]; !e.Present || string(e.Data) != confTestSysctl {
		t.Errorf("Files[/etc/ufw/sysctl.conf] = %+v, want the default file", e)
	}

	files["etc/default/ufw"] = strings.Replace(confTestDefault, "IPT_SYSCTL=/etc/ufw/sysctl.conf", "IPT_SYSCTL=/etc/ufw/./sysctl.conf", 1)
	c, err = Read(confTestTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unsupported) != 0 || c.SysctlFile != "/etc/ufw/sysctl.conf" || len(c.Sysctl) != 4 {
		t.Errorf("default file through a dot path: Unsupported %q, SysctlFile %q, %d settings", c.Unsupported, c.SysctlFile, len(c.Sysctl))
	}
}

func TestReadIPv6Off(t *testing.T) {
	files := confTestFiles()
	files["etc/default/ufw"] = strings.Replace(confTestDefault, "IPV6=yes", "IPV6=no", 1)
	files["etc/ufw/user6.rules"], files["etc/ufw/before6.rules"], files["etc/ufw/after6.rules"] = "", "", ""
	c, err := Read(confTestTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if c.IPv6 || len(c.Unsupported) != 0 || len(c.Rules6) != 0 {
		t.Fatalf("config = %+v, want IPv6 off and supported", c)
	}
}

func TestReadErrors(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, root string)
	}{
		{"missing ufw.conf", func(t *testing.T, root string) { confTestRemove(t, filepath.Join(root, "etc/ufw/ufw.conf")) }},
		{"missing default", func(t *testing.T, root string) { confTestRemove(t, filepath.Join(root, "etc/default/ufw")) }},
		{"oversize", func(t *testing.T, root string) {
			big := make([]byte, fwstate.MaxFileSize+1)
			if err := os.WriteFile(filepath.Join(root, "etc/ufw/before.rules"), big, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"oversize sysctl file", func(t *testing.T, root string) {
			big := make([]byte, fwstate.MaxFileSize+1)
			if err := os.WriteFile(filepath.Join(root, "etc/ufw/sysctl.conf"), big, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, root string) {
			p := filepath.Join(root, "etc/ufw/after.rules")
			confTestRemove(t, p)
			if err := os.Symlink(filepath.Join(root, "etc/ufw/before.rules"), p); err != nil {
				t.Fatal(err)
			}
		}},
		{"directory", func(t *testing.T, root string) {
			p := filepath.Join(root, "etc/ufw/user6.rules")
			confTestRemove(t, p)
			if err := os.Mkdir(p, 0o750); err != nil {
				t.Fatal(err)
			}
		}},
		{"init script loop", func(t *testing.T, root string) {
			p := filepath.Join(root, "etc/ufw/before.init")
			confTestRemove(t, p)
			if err := os.Symlink("before.init", p); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := confTestTree(t, confTestFiles())
			c.edit(t, root)
			if got, err := Read(root); err == nil {
				t.Fatalf("Read = %+v, want an error", got)
			} else if !strings.HasPrefix(err.Error(), "conf: ") {
				t.Fatalf("error %q lacks the package prefix", err)
			}
		})
	}
}

func TestSysctlKeys(t *testing.T) {
	keys := map[string]string{
		"net.ipv4.ip_forward": "net/ipv4/ip_forward", "net/ipv4/ip_forward": "net/ipv4/ip_forward",
		"net.ipv4.conf.eth0/100.rp_filter": "net/ipv4/conf/eth0.100/rp_filter",
		"net/ipv4/conf/eth0.100/rp_filter": "net/ipv4/conf/eth0.100/rp_filter", "kernel": "kernel",
	}
	for in, want := range keys {
		if got := SysctlKey(in); got != want {
			t.Errorf("SysctlKey(%q) = %q, want %q", in, got, want)
		}
	}
	supported := []string{"net/ipv4/ip_forward", "net/ipv6/conf/all/forwarding", "net/ipv6/conf/eth0.100/accept_ra",
		"net/ipv4/conf/default/rp_filter", "net/ipv4/conf/br-1/accept_source_route"}
	for _, k := range supported {
		if !SupportedSysctl(k) || FilteringSysctl(k) {
			t.Errorf("%s: supported %v, filtering %v; want true, false", k, SupportedSysctl(k), FilteringSysctl(k))
		}
	}
	filtering := []string{"net/ipv4/conf/all/forwarding", "net/ipv4/conf/lo/route_localnet",
		"net/bridge/bridge-nf-call-ip6tables", "net/bridge/bridge-nf-call-arptables"}
	for _, k := range filtering {
		if SupportedSysctl(k) || !FilteringSysctl(k) {
			t.Errorf("%s: supported %v, filtering %v; want false, true", k, SupportedSysctl(k), FilteringSysctl(k))
		}
	}
	for _, k := range []string{"net/ipv4/conf/all/accept_redirects", "net/ipv4/icmp_echo_ignore_all", "net/ipv6/conf/a/b/forwarding"} {
		if SupportedSysctl(k) || FilteringSysctl(k) {
			t.Errorf("%s is classified, want neither", k)
		}
	}
}

// FuzzReadFiles checks that no content of ufw.conf, /etc/default/ufw or
// the sysctl file panics the reader.
func FuzzReadFiles(f *testing.F) {
	f.Add(confTestUFWConf, confTestDefault, confTestSysctl)
	f.Add("ENABLED='yes", "IPV6=\"", "=\n-\n.=.")
	f.Fuzz(func(t *testing.T, ufwConf, def, sysctl string) {
		files := confTestFiles()
		files["etc/ufw/ufw.conf"], files["etc/default/ufw"], files["etc/ufw/sysctl.conf"] = ufwConf+"\n", def+"\n", sysctl+"\n"
		_, _ = Read(confTestTree(t, files))
	})
}

func TestReadQuotedList(t *testing.T) {
	files := confTestFiles()
	files["etc/default/ufw"] = strings.Replace(confTestDefault, `IPT_MODULES=""`, `IPT_MODULES="nf_conntrack_ftp nf_nat_ftp nf_conntrack_netbios_ns"`, 1)
	c, err := Read(confTestTree(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Unsupported) != 0 {
		t.Errorf("Unsupported = %q, want none", c.Unsupported)
	}
}

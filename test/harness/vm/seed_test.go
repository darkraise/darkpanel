//go:build harness

package vm

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestSeedFiles(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
		InetAddrs: []string{"198.18.0.10/15", "2001:db8:d0::10/64"},
		PrivMAC:   "52:54:00:d0:02:10", PrivAddrs: []string{"10.77.0.10/24"}, PrivMcastPort: 23457,
		Provision: "#!/bin/bash\necho '<ok>' && true\n", Key: key,
	}
	s, err := seedFiles(cfg, hostKey)
	if err != nil {
		t.Fatal(err)
	}

	body, ok := bytes.CutPrefix(s.UserData, []byte("#cloud-config\n"))
	if !ok {
		t.Fatalf("user-data does not start with #cloud-config:\n%s", s.UserData)
	}
	if bytes.Contains(body, []byte(`\u003c`)) {
		t.Error("user-data escapes < as \\u003c; the encoder must not escape HTML")
	}
	var user cloudConfig
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatal(err)
	}
	authorized := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key.PublicKey())))
	if user.Hostname != "target" || len(user.Users) != 1 || user.Users[0].Name != User ||
		user.Users[0].Sudo != "ALL=(ALL) NOPASSWD:ALL" || !user.Users[0].LockPasswd ||
		!slices.Equal(user.Users[0].SSHAuthorizedKeys, []string{authorized}) {
		t.Errorf("user-data hostname %q users %+v; want target with %s, the run key and passwordless sudo", user.Hostname, user.Users, User)
	}
	raw, err := ssh.ParseRawPrivateKey([]byte(user.SSHKeys["ed25519_private"]))
	if priv, ok := raw.(*ed25519.PrivateKey); err != nil || !ok || !bytes.Equal(*priv, hostKey) {
		t.Errorf("ssh_keys ed25519_private does not hold the host key (%v)", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := user.SSHKeys["ed25519_public"], strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostSigner.PublicKey()))); got != want {
		t.Errorf("ssh_keys ed25519_public = %q, want %q", got, want)
	}
	if !user.SSHDeleteKeys || user.SSHPwauth {
		t.Errorf("ssh_deletekeys %v ssh_pwauth %v; want the image's host keys deleted and no password login", user.SSHDeleteKeys, user.SSHPwauth)
	}
	wantFile := writeFile{Path: "/usr/local/sbin/harness-provision", Permissions: "0755", Content: cfg.Provision}
	if len(user.WriteFiles) != 1 || user.WriteFiles[0] != wantFile {
		t.Errorf("write_files = %+v, want %+v", user.WriteFiles, wantFile)
	}
	wantRun := []string{"bash", "-c",
		"if /usr/local/sbin/harness-provision >/var/log/harness-provision.log 2>&1; then touch /var/lib/harness-provisioned; fi"}
	if len(user.Runcmd) != 1 || !slices.Equal(user.Runcmd[0], wantRun) {
		t.Errorf("runcmd = %q, want [%q]", user.Runcmd, wantRun)
	}

	var meta map[string]string
	if err := json.Unmarshal(s.MetaData, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["instance-id"] != "target-harness" || meta["local-hostname"] != "target" {
		t.Errorf("meta-data = %v", meta)
	}

	var network netConfig
	if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
		t.Fatal(err)
	}
	mgmt, inet, priv := network.Ethernets["dpmgmt"], network.Ethernets["dpinet"], network.Ethernets["dppriv"]
	if network.Version != 2 || len(network.Ethernets) != 3 ||
		mgmt.Match.MAC != cfg.MgmtMAC || mgmt.SetName != "dpmgmt" || !mgmt.DHCP4 || len(mgmt.Addresses) != 0 ||
		inet.Match.MAC != cfg.InetMAC || inet.SetName != "dpinet" || inet.DHCP4 || inet.DHCP6 || inet.AcceptRA ||
		!slices.Equal(inet.Addresses, cfg.InetAddrs) ||
		priv.Match.MAC != cfg.PrivMAC || priv.SetName != "dppriv" || priv.DHCP4 || priv.DHCP6 || priv.AcceptRA ||
		!slices.Equal(priv.Addresses, cfg.PrivAddrs) {
		t.Errorf("network-config = %s", s.NetworkConfig)
	}
}

// TestSeedFilesKeyNames decodes the seed into generic values, so a wrong
// JSON tag on the seed types, which TestSeedFiles decodes back unchanged,
// shows as a key cloud-init or netplan does not read. Without a private link
// port the network config has no dppriv.
func TestSeedFilesKeyNames(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		priv   bool
		static []string // the NICs with static addresses, besides dpmgmt
	}{
		{"internet only", false, []string{"dpinet"}},
		{"private link", true, []string{"dpinet", "dppriv"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{
				Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
				InetAddrs: []string{"198.18.0.10/15"}, Provision: "#!/bin/bash\n", Key: key,
			}
			if c.priv {
				cfg.PrivMAC, cfg.PrivAddrs, cfg.PrivMcastPort = "52:54:00:d0:02:10", []string{"10.77.0.10/24"}, 23457
			}
			s, err := seedFiles(cfg, hostKey)
			if err != nil {
				t.Fatal(err)
			}
			var user, network any
			if err := json.Unmarshal(bytes.TrimPrefix(s.UserData, []byte("#cloud-config\n")), &user); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
				t.Fatal(err)
			}
			u := wantKeys(t, "user-data", user, "hostname", "users", "ssh_pwauth", "ssh_deletekeys", "ssh_keys", "write_files", "runcmd")
			wantKeys(t, "users[0]", first(t, "users", u["users"]), "name", "sudo", "shell", "lock_passwd", "ssh_authorized_keys")
			wantKeys(t, "ssh_keys", u["ssh_keys"], "ed25519_private", "ed25519_public")
			wantKeys(t, "write_files[0]", first(t, "write_files", u["write_files"]), "path", "permissions", "content")
			n := wantKeys(t, "network-config", network, "version", "ethernets")
			eth := wantKeys(t, "ethernets", n["ethernets"], append([]string{"dpmgmt"}, c.static...)...)
			mgmt := wantKeys(t, "dpmgmt", eth["dpmgmt"], "match", "set-name", "dhcp4", "dhcp6", "accept-ra")
			wantKeys(t, "dpmgmt match", mgmt["match"], "macaddress")
			for _, name := range c.static {
				nic := wantKeys(t, name, eth[name], "match", "set-name", "dhcp4", "dhcp6", "accept-ra", "addresses")
				wantKeys(t, name+" match", nic["match"], "macaddress")
			}
		})
	}
}

// wantKeys checks that v is a JSON object with exactly keys and returns it.
func wantKeys(t *testing.T, what string, v any, keys ...string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s is %T, want an object", what, v)
	}
	got := slices.Sorted(maps.Keys(m))
	slices.Sort(keys)
	if !slices.Equal(got, keys) {
		t.Errorf("%s keys = %v, want %v", what, got, keys)
	}
	return m
}

// first returns the first element of the JSON array v.
func first(t *testing.T, what string, v any) any {
	t.Helper()
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		t.Fatalf("%s is %v, want a non-empty array", what, v)
	}
	return a[0]
}

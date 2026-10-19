//go:build harness

package vm

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"strings"

	"golang.org/x/crypto/ssh"
)

// User is the SSH user cloud-init creates on every VM, with passwordless
// sudo.
const User = "harness"

// McastGroup is the multicast group of the run's "internet" link.
const McastGroup = "230.0.0.1"

// Provisioning files on the VM. The runcmd wrapper writes the marker only
// when the script exits 0, so Boot tells a failed script from one that never
// ran.
const (
	provisionScript = "/usr/local/sbin/harness-provision"
	provisionLog    = "/var/log/harness-provision.log"
	provisionedMark = "/var/lib/harness-provisioned"
)

// Interface names network-config gives the NICs it matches by MAC address,
// because Ubuntu and Debian images name them differently.
const (
	mgmtIface = "dpmgmt"
	inetIface = "dpinet"
)

// Config describes one VM of a run.
type Config struct {
	Name      string // a short name, such as target or scanner: the hostname and the prefix of its files in Dir
	Dir       string // the run directory: overlay, seed, console and QEMU logs
	Image     string // a verified base image from Fetch
	MemoryMiB int
	CPUs      int
	// MgmtMAC is the user-mode management NIC, which has DHCP, outbound
	// access and the SSH port forward; InetMAC is the "internet" NIC on the
	// run's multicast link.
	MgmtMAC, InetMAC string
	// InetAddrs are the internet NIC's static addresses in CIDR form.
	InetAddrs []string
	// McastPort is the UDP port of the run's multicast link, the same for
	// every VM of the run.
	McastPort int
	// Provision is a bash script cloud-init runs once as root; Boot fails
	// unless it exits 0.
	Provision string
	// Key is the run's SSH client key; its public part is authorized for
	// User.
	Key ssh.Signer
}

// NewKey returns a new Ed25519 SSH key.
func NewKey() (ssh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("vm: generate key: %w", err)
	}
	return ssh.NewSignerFromKey(priv)
}

// seed is the content of a NoCloud seed. cloud-init reads all three files as
// YAML, and JSON is YAML, so they are written with encoding/json rather than
// by indenting YAML by hand.
type seed struct {
	UserData, MetaData, NetworkConfig []byte
}

type cloudConfig struct {
	Hostname      string            `json:"hostname"`
	Users         []cloudUser       `json:"users"`
	SSHPwauth     bool              `json:"ssh_pwauth"`
	SSHDeleteKeys bool              `json:"ssh_deletekeys"`
	SSHKeys       map[string]string `json:"ssh_keys"`
	WriteFiles    []writeFile       `json:"write_files"`
	Runcmd        [][]string        `json:"runcmd"`
}

type cloudUser struct {
	Name              string   `json:"name"`
	Sudo              string   `json:"sudo"`
	Shell             string   `json:"shell"`
	LockPasswd        bool     `json:"lock_passwd"`
	SSHAuthorizedKeys []string `json:"ssh_authorized_keys"`
}

type writeFile struct {
	Path        string `json:"path"`
	Permissions string `json:"permissions"`
	Content     string `json:"content"`
}

type netConfig struct {
	Version   int                    `json:"version"`
	Ethernets map[string]netEthernet `json:"ethernets"`
}

type netEthernet struct {
	Match     netMatch `json:"match"`
	SetName   string   `json:"set-name"`
	DHCP4     bool     `json:"dhcp4"`
	DHCP6     bool     `json:"dhcp6"`
	AcceptRA  bool     `json:"accept-ra"`
	Addresses []string `json:"addresses,omitempty"`
}

type netMatch struct {
	MAC string `json:"macaddress"`
}

// seedFiles renders cfg's NoCloud files. hostKey becomes the VM's only SSH
// host key, which Boot then pins.
func seedFiles(cfg Config, hostKey ed25519.PrivateKey) (seed, error) {
	block, err := ssh.MarshalPrivateKey(hostKey, "")
	if err != nil {
		return seed{}, fmt.Errorf("vm %s: host key: %w", cfg.Name, err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		return seed{}, fmt.Errorf("vm %s: host key: %w", cfg.Name, err)
	}
	user, err := marshal(cloudConfig{
		Hostname: cfg.Name,
		Users: []cloudUser{{
			Name: User, Sudo: "ALL=(ALL) NOPASSWD:ALL", Shell: "/bin/bash", LockPasswd: true,
			SSHAuthorizedKeys: []string{authorizedKey(cfg.Key.PublicKey())},
		}},
		SSHDeleteKeys: true,
		SSHKeys: map[string]string{
			"ed25519_private": string(pem.EncodeToMemory(block)),
			"ed25519_public":  authorizedKey(hostSigner.PublicKey()),
		},
		WriteFiles: []writeFile{{Path: provisionScript, Permissions: "0755", Content: cfg.Provision}},
		Runcmd: [][]string{{"bash", "-c",
			"if " + provisionScript + " >" + provisionLog + " 2>&1; then touch " + provisionedMark + "; fi"}},
	})
	if err != nil {
		return seed{}, err
	}
	meta, err := marshal(map[string]string{"instance-id": cfg.Name + "-harness", "local-hostname": cfg.Name})
	if err != nil {
		return seed{}, err
	}
	network, err := marshal(netConfig{Version: 2, Ethernets: map[string]netEthernet{
		mgmtIface: {Match: netMatch{MAC: cfg.MgmtMAC}, SetName: mgmtIface, DHCP4: true},
		inetIface: {Match: netMatch{MAC: cfg.InetMAC}, SetName: inetIface, Addresses: cfg.InetAddrs},
	}})
	if err != nil {
		return seed{}, err
	}
	return seed{UserData: append([]byte("#cloud-config\n"), user...), MetaData: meta, NetworkConfig: network}, nil
}

// authorizedKey is k in authorized_keys form without the trailing newline.
func authorizedKey(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("vm: seed: %w", err)
	}
	return b.Bytes(), nil
}

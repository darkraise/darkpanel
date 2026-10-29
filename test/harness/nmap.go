//go:build harness

package harness

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// OpenPorts returns the open ports nmap's grepable output (-oG -) reports
// for host, as "<port>/<protocol>" in nmap's order. It is an error when the
// output has no port report for host, so a scan that never reached the host
// cannot pass as one that found nothing open.
func OpenPorts(grepable, host string) ([]string, error) {
	open := []string{}
	reported := false
	for _, line := range strings.Split(grepable, "\n") {
		rest, ok := strings.CutPrefix(line, "Host: ")
		if !ok {
			continue
		}
		addr, _, _ := strings.Cut(rest, " ")
		if addr != host {
			continue
		}
		for _, field := range strings.Split(rest, "\t")[1:] {
			switch {
			case strings.HasPrefix(field, "Ignored State: "):
				reported = true
			case strings.HasPrefix(field, "Ports: "):
				reported = true
				for _, p := range strings.Split(strings.TrimPrefix(field, "Ports: "), ", ") {
					// <port>/<state>/<protocol>/<owner>/<service>/<rpc>/<version>/
					f := strings.Split(p, "/")
					if len(f) >= 3 && f[1] == "open" {
						open = append(open, f[0]+"/"+f[2])
					}
				}
			}
		}
	}
	if !reported {
		return nil, fmt.Errorf("harness: nmap reported no ports for %s", host)
	}
	return open, nil
}

// ScanOpts describes one nmap scan of a target.
type ScanOpts struct {
	Family     string     // "-4" or "-6"
	UDP        bool       // -sU in place of -sS
	Ports      []uint16   // -p <list>; empty: every port (-p-)
	SourcePort uint16     // --source-port <n>; 0: nmap's choice
	Source     netip.Addr // -S <addr> -e <iface> when valid
	Iface      string     // the scanner interface for -e
	// Payload is sent in every probe (--data); nil leaves nmap its own UDP
	// payloads, which a service on an unusual port does not answer.
	Payload []byte
	Target  string
}

// PortState is one port entry of nmap's grepable output.
type PortState struct {
	Port  uint16
	Proto string // tcp | udp
	State string // open | closed | filtered | open|filtered | unfiltered
}

// NmapArgs returns the nmap arguments for o: host discovery and DNS off, the
// scan type, the ports, the source options, the timing of the M1 scan and
// grepable output, then the target.
func NmapArgs(o ScanOpts) []string {
	var args []string
	if o.Family != "" {
		args = append(args, o.Family)
	}
	args = append(args, "-Pn", "-n")
	if o.UDP {
		args = append(args, "-sU")
	} else {
		args = append(args, "-sS")
	}
	if len(o.Ports) == 0 {
		args = append(args, "-p-")
	} else {
		ports := make([]string, len(o.Ports))
		for i, p := range o.Ports {
			ports[i] = strconv.FormatUint(uint64(p), 10)
		}
		args = append(args, "-p"+strings.Join(ports, ","))
	}
	if o.SourcePort != 0 {
		args = append(args, "--source-port", strconv.FormatUint(uint64(o.SourcePort), 10))
	}
	if o.Source.IsValid() {
		args = append(args, "-S", o.Source.String())
	}
	if o.Iface != "" {
		args = append(args, "-e", o.Iface)
	}
	if len(o.Payload) > 0 {
		args = append(args, "--data", hex.EncodeToString(o.Payload))
	}
	args = append(args, "--min-rate", "2000", "--max-retries", "2", "--max-rtt-timeout", "500ms", "-oG", "-")
	return append(args, o.Target)
}

// ParsePorts returns every port entry of nmap's grepable output (-oG -), TCP
// and UDP together, in nmap's order and whatever the state. It is an error
// when no host line reports ports or an ignored state, so a scan that never
// reached the host cannot pass as one that found nothing.
func ParsePorts(grepable string) ([]PortState, error) {
	ports := []PortState{}
	reported := false
	for _, line := range strings.Split(grepable, "\n") {
		rest, ok := strings.CutPrefix(line, "Host: ")
		if !ok {
			continue
		}
		for _, field := range strings.Split(rest, "\t")[1:] {
			switch {
			case strings.HasPrefix(field, "Ignored State: "):
				reported = true
			case strings.HasPrefix(field, "Ports: "):
				reported = true
				for _, entry := range strings.Split(strings.TrimPrefix(field, "Ports: "), ", ") {
					p, err := m3dParsePortEntry(entry)
					if err != nil {
						return nil, err
					}
					ports = append(ports, p)
				}
			}
		}
	}
	if !reported {
		return nil, errors.New("harness: nmap reported no ports")
	}
	return ports, nil
}

// m3dParsePortEntry reads <port>/<state>/<protocol>/<owner>/<service>/<rpc>/<version>/.
func m3dParsePortEntry(entry string) (PortState, error) {
	f := strings.Split(entry, "/")
	if len(f) < 3 {
		return PortState{}, fmt.Errorf("harness: nmap port entry %q has fewer than three fields", entry)
	}
	port, err := strconv.ParseUint(f[0], 10, 16)
	if err != nil || port == 0 {
		return PortState{}, fmt.Errorf("harness: nmap port entry %q: port is not a number from 1 to 65535", entry)
	}
	if f[1] == "" {
		return PortState{}, fmt.Errorf("harness: nmap port entry %q has no state", entry)
	}
	if f[2] != "tcp" && f[2] != "udp" {
		return PortState{}, fmt.Errorf("harness: nmap port entry %q: protocol is neither tcp nor udp", entry)
	}
	return PortState{Port: uint16(port), Proto: f[2], State: f[1]}, nil
}

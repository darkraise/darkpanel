//go:build harness

package harness

import (
	"fmt"
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

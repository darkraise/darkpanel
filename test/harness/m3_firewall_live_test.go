//go:build harness

package harness

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func w11a02UFWChains(save string) map[string]string {
	out := make(map[string]string)
	rules := make(map[string][]string)
	for line := range strings.SplitSeq(save, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, ":"):
			fields := strings.Fields(line[1:])
			if len(fields) < 2 {
				continue
			}
			chain, policy := fields[0], fields[1]
			switch {
			case policy != "-":
				out[chain+" policy"] = policy
			case strings.HasPrefix(chain, "ufw-") || strings.HasPrefix(chain, "ufw6-"):
				out[chain] = ""
			}
		case strings.HasPrefix(line, "-A "):
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			chain := fields[1]
			if strings.HasPrefix(chain, "ufw-") || strings.HasPrefix(chain, "ufw6-") {
				rules[chain] = append(rules[chain], line)
			}
		}
	}
	for chain, lines := range rules {
		out[chain] = strings.Join(lines, "\n")
	}
	return out
}

func w11a02PolicyLeaks(log string) []string {
	var out []string
	for line := range strings.SplitSeq(log, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, "ACCEPT") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		fam := fields[0]
		if !slices.Contains(out, fam) {
			out = append(out, fam)
		}
	}
	return out
}

// w11a02PolicyUnsampled returns each of families with no INPUT policy sample
// in log, since an empty poll would otherwise read as no leak.
func w11a02PolicyUnsampled(log string, families ...string) []string {
	var out []string
	for _, fam := range families {
		if !strings.Contains(log, fam+" -P INPUT ") {
			out = append(out, fam)
		}
	}
	return out
}

func m3LiveApply(ctx context.Context, t *testing.T, f *fleet) {
	m3WaitStatus(ctx, t, f.api, f.agentID, time.Minute, m3Healthy)
	t.Cleanup(func() {
		_, _ = f.agent.Run(ctx, "sudo ufw delete allow 8097/tcp; "+
			"sudo ufw route delete allow in on eth0 out on eth0 to 192.0.2.10 port 8097 proto tcp; "+
			"sudo ufw delete deny from 198.51.100.7; "+
			"sudo ufw logging low; "+
			"sudo rm -f /run/w11a-stop /run/w11a-poll.log")
	})
	m3Run(ctx, t, f.agent, "sudo rm -f /run/w11a-stop /run/w11a-poll.log")
	cmds := []string{
		"sudo ufw allow 8097/tcp comment w11a",
		"sudo ufw route allow in on eth0 out on eth0 to 192.0.2.10 port 8097 proto tcp comment w11a",
		"sudo ufw insert 1 deny from 198.51.100.7 comment w11a",
		"sudo ufw logging medium",
		"sudo ufw delete allow 8097/tcp",
		"sudo ufw logging low",
	}
	families := []struct {
		name string
		save string
	}{
		{"v4", "sudo iptables-save -t filter"},
		{"v6", "sudo ip6tables-save -t filter"},
	}
	for _, cmd := range cmds {
		m3Run(ctx, t, f.agent, "sudo rm -f /run/w11a-stop && "+
			`sudo setsid sh -c 'while [ ! -e /run/w11a-stop ]; do echo "v4 $(iptables -S INPUT | head -1)"; echo "v6 $(ip6tables -S INPUT | head -1)"; done >> /run/w11a-poll.log 2>&1 < /dev/null &'`)
		m3Run(ctx, t, f.agent, cmd)
		m3Run(ctx, t, f.agent, "sudo touch /run/w11a-stop && sleep 1")
		live := make(map[string]map[string]string, len(families))
		for _, fam := range families {
			live[fam.name] = w11a02UFWChains(m3Run(ctx, t, f.agent, fam.save))
		}
		m3Run(ctx, t, f.agent, "sudo ufw reload")
		for _, fam := range families {
			reloaded := w11a02UFWChains(m3Run(ctx, t, f.agent, fam.save))
			keys := make([]string, 0, len(live[fam.name])+len(reloaded))
			for k := range live[fam.name] {
				keys = append(keys, k)
			}
			for k := range reloaded {
				if _, ok := live[fam.name][k]; !ok {
					keys = append(keys, k)
				}
			}
			slices.Sort(keys)
			for _, k := range keys {
				if live[fam.name][k] != reloaded[k] {
					t.Errorf("live ufw %s and a reload disagree on %s chain %s", cmd, fam.name, k)
				}
			}
		}
	}
	log := m3Run(ctx, t, f.agent, "sudo cat /run/w11a-poll.log")
	for _, fam := range w11a02PolicyLeaks(log) {
		t.Errorf("live ufw commands left INPUT policy ACCEPT in %s", fam)
	}
	for _, fam := range w11a02PolicyUnsampled(log, "v4", "v6") {
		t.Errorf("the policy poll recorded no INPUT policy of %s", fam)
	}
}

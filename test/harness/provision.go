//go:build harness

package harness

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"

	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// scriptHeader starts every provisioning script. The lock timeout waits out
// the apt runs a cloud image starts on its first boot.
const scriptHeader = `#!/bin/bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt_get() { apt-get -o DPkg::Lock::Timeout=600 -y "$@"; }
`

// fetchFunc downloads a pinned .deb into $pkgdir and checks its SHA-256. A
// pinned file that has left its repository fails with the spec's message
// instead of letting apt pick another version.
const fetchFunc = `fetch() {
  if ! curl -fsL --retry 3 -o "$pkgdir/$1.deb" "$3"; then
    echo "pinned package $1=$2 missing; update the harness pins" >&2
    exit 1
  fi
  got=$(sha256sum "$pkgdir/$1.deb" | cut -d' ' -f1)
  if [ "$got" != "$4" ]; then
    echo "pinned package $1=$2 has SHA-256 $got, want $4; update the harness pins" >&2
    exit 1
  fi
}
`

// pullFunc pulls a pinned image by digest once Docker runs, retrying a
// registry that fails for a moment, and fails with the pins message when it
// cannot. pull_retry_delay exists for the script's own test.
const pullFunc = `pull_image() {
  for _ in 1 2 3; do
    if docker pull --quiet "$2" >/dev/null; then
      return 0
    fi
    sleep "${pull_retry_delay:-5}"
  done
  echo "pinned image $1 ($2) cannot be pulled; update the harness pins" >&2
  exit 1
}
`

// agentRule lets agents reach the controller's agent listener on the
// private link that stands in for the tailnet, and nowhere else. dppriv is
// the name the vm package gives the private NIC; ufw checks only the
// interface name's syntax, so on a target without that NIC the rule matches
// nothing.
var agentRule = "ufw allow in on dppriv to any port " + strconv.Itoa(listen.AgentPort) + " proto tcp\n"

var targetTemplate = template.Must(template.New("target").Parse(scriptHeader + fetchFunc + `apt_get update
apt_get install --no-install-recommends ca-certificates curl sqlite3 ufw
pkgdir=/var/cache/harness-packages
mkdir -p "$pkgdir"
{{range .Packages}}fetch '{{.Name}}' '{{.Version}}' '{{.URL}}' '{{.SHA256}}'
{{end}}apt_get install --no-install-recommends{{range .Packages}} "$pkgdir/{{.Name}}.deb"{{end}}
ufw allow 22/tcp
` + agentRule + `ufw --force enable
systemctl enable --now ufw docker tailscaled
`))

// TargetScript provisions the target VM of o: UFW enabled with 22/tcp
// allowed, and the agent port only on the private link, and ufw.service
// started (Debian's package enables the unit on install without starting it,
// so it would stay inactive until a reboot), sqlite3 for reading the
// databases, and Docker Engine with its Compose plugin and tailscaled from
// o's pinned .deb files.
// tailscaled runs but is not logged in until M5.
func TargetScript(o OS) string {
	var b strings.Builder
	if err := targetTemplate.Execute(&b, o); err != nil {
		panic(err) // the template names only OS and Package fields
	}
	return b.String()
}

// PullScript is a provisioning fragment, appended to TargetScript, that
// pulls cs on the target by digest, as the target fetches its packages
// itself. Docker checks every pulled blob against the digest, so a changed
// image cannot pass as the pinned one; a test then runs each by its Ref.
func PullScript(cs ...Container) string {
	var b strings.Builder
	b.WriteString(pullFunc)
	for _, c := range cs {
		fmt.Fprintf(&b, "pull_image '%s' '%s'\n", c.Name, c.Ref)
	}
	return b.String()
}

// ScannerScript provisions the scanner VM: nmap.
func ScannerScript() string {
	return scriptHeader + "apt_get update\napt_get install --no-install-recommends nmap\n"
}

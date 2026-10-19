//go:build harness

package harness

import (
	"strings"
	"text/template"
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

var targetTemplate = template.Must(template.New("target").Parse(scriptHeader + fetchFunc + `apt_get update
apt_get install --no-install-recommends ca-certificates curl ufw
pkgdir=/var/cache/harness-packages
mkdir -p "$pkgdir"
{{range .Packages}}fetch '{{.Name}}' '{{.Version}}' '{{.URL}}' '{{.SHA256}}'
{{end}}apt_get install --no-install-recommends{{range .Packages}} "$pkgdir/{{.Name}}.deb"{{end}}
ufw allow 22/tcp
ufw --force enable
systemctl enable --now ufw docker tailscaled
`))

// TargetScript provisions the target VM of o: UFW enabled with only 22/tcp
// allowed and ufw.service started (Debian's package enables the unit on
// install without starting it, so it would stay inactive until a reboot),
// and Docker Engine and tailscaled from o's pinned .deb files. tailscaled
// runs but is not logged in until M5.
func TargetScript(o OS) string {
	var b strings.Builder
	if err := targetTemplate.Execute(&b, o); err != nil {
		panic(err) // the template names only OS and Package fields
	}
	return b.String()
}

// ScannerScript provisions the scanner VM: nmap.
func ScannerScript() string {
	return scriptHeader + "apt_get update\napt_get install --no-install-recommends nmap\n"
}

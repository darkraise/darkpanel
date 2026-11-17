# M7 #6: TestTailscaledLive against tailscaled 1.82.0

Recorded run, 2026-11-17, on the development host at commit `3b420962`.
It settles the floor half of M7 register row 6. The spec's Versions row
(tailscaled 1.82 or later) stands.

## Daemon

- Package: `https://pkgs.tailscale.com/stable/tailscale_1.82.0_amd64.tgz`,
  sha256 `c4ad89554f2289c6aefedc261148c5c08c6db78354ae7b3726608b8c8910cc2b`,
  matching the published `.sha256`.
- `tailscale version --daemon`: `1.82.0-t6676b1261-g3ec4bfb9c` (tailscale
  commit `6676b1261e51`, go1.24.1).
- Started as
  `tailscaled --tun=userspace-networking --statedir=<empty dir> --socket=<dir>/tailscaled.sock --port=0`,
  with a fresh state, so the node was logged out (`BackendState` `NeedsLogin`).

## Test

```
DARKPANEL_TAILSCALED_SOCKET=<dir>/tailscaled.sock \
  go test ./internal/platform/tslocal -run '^TestTailscaledLive$' -v -count=1
```

```
=== RUN   TestTailscaledLive
--- PASS: TestTailscaledLive (0.00s)
PASS
ok  	github.com/darkraise/darkpanel/internal/platform/tslocal	0.006s
```

After the run, the daemon's preferences were back to what they were before
the test: `NetfilterMode` 2, `Hostname` empty, `ShieldsUp` false,
`WantRunning` false.

## Difference from the harness

The M5 harness runs the same test on VMs whose tailscaled uses a kernel TUN
device. This run used userspace networking so it would not touch the host's
interfaces or netfilter rules. The test only calls the local API, and the
`NetfilterMode` it carries through `Start` is a stored preference (2 here as
on the VMs), so the networking mode does not change what the test checks.

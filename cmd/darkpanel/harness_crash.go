//go:build harnesscrash

package main

// A harnesscrash build passes selftest but its services exit at once, so
// systemd's restarts exhaust the start limit and OnFailure= rolls it back.
func init() { crashAtStart = true }

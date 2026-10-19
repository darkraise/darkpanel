# Released schema snapshots

`make release-prep VERSION=x.y.z` copies the current snapshots here as
`released/vx.y.z/`. A released snapshot is never edited (ADR 0002): the
controller validates an older agent's input against it, and an agent's schema
hash is looked up among these files.

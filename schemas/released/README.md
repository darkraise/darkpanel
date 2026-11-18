# Released schema snapshots

`make release-prep VERSION=x.y.z` writes `released/vx.y.z/` only for the
current snapshots that no other release of the same major holds byte for
byte, so the first release of a major holds the full set and a release with
no schema change adds nothing. A released snapshot is never edited or deleted
(ADR 0002): the controller validates an older agent's input against it, and
an agent's schema hash is looked up among these files. CI fails if any
released content disappears (`release/snapshots.sh retained`). The exact set
a tag shipped is in git: `git show <tag>:schemas/`.

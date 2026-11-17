# Darkpanel M7 register

**Source:** roadmap
**Covers:** docs/dforge/specs/2026-10-17-darkpanel-design.md

| # | Item | Assigned | Acceptance | State | Note |
|---|---|---|---|---|---|
| 1 | Security review | docs/dforge/specs/2026-10-21-v1-completion-design.md | R73, R74 | verify | 2026-11-17: v1 #37 (R73) and #38 (R74) done in docs/dforge/registers/2026-10-21-v1-completion.md; for the M7 audit |
| 2 | Upgrade and compatibility matrix | docs/dforge/specs/2026-10-21-v1-completion-design.md | R75 | verify | 2026-11-17: v1 #39 (R75); tailscaled floor in row 6 done in docs/dforge/registers/2026-10-21-v1-completion.md; for the M7 audit |
| 3 | Performance budget | docs/dforge/specs/2026-10-21-v1-completion-design.md | R76 | verify | 2026-11-17: v1 #40 (R76) done in docs/dforge/registers/2026-10-21-v1-completion.md; for the M7 audit |
| 4 | Accessibility | docs/dforge/specs/2026-10-21-v1-completion-design.md | R77 | verify | 2026-11-17: v1 #41 (R77) done in docs/dforge/registers/2026-10-21-v1-completion.md; for the M7 audit |
| 5 | Documentation | docs/dforge/specs/2026-10-21-v1-completion-design.md | R78, R79 | verify | 2026-11-17: v1 #42 (R78) and #43 (R79) done in docs/dforge/registers/2026-10-21-v1-completion.md; for the M7 audit |
| 6 | tailscaled 1.82.0 live check carried from the M5 audit 2026-11-16 (v1 #79): TestTailscaledLive has run only against the pinned 1.104.1; the spec's Versions section claims tailscaled 1.82 or later | docs/dforge/notes/m7-tailscaled-1.82.0-live-run.md | TestTailscaledLive passes against tailscaled 1.82.0 (the spec's supported floor) in the harness or a recorded run, or the owner raises the supported floor in the spec | verify | carried from M5 audit part A item 19 by owner ruling 2026-11-16, as part of the R75 compatibility matrix (epic #2); 2026-11-17: TestTailscaledLive passed against tailscaled 1.82.0 (userspace mode, sha-checked package) in a recorded run, docs/dforge/notes/m7-tailscaled-1.82.0-live-run.md; the 1.82 floor stands |

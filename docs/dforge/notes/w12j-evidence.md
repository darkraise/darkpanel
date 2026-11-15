# w12j evidence

Mutation checks for the w12j bounded follow-up (security register rows 237-242) and for the w12i fix tests, which
had no recorded check (row 242). Run on 2026-11-15 on branch `w12j-low-rows` with
`go test ./internal/modules/npm/ -count=1 -run <tests>`. For each check, the fault is put back into the source, the
tests are run, and the source is restored.

| Fix | Mutation | Failing test |
|---|---|---|
| w12i 8982d4cb | an ExecInspect error before the deadline does not hold the exec | TestNginxReloaderKeepsAnExecPendingAfterAnUncertainAnswer |
| w12i 8982d4cb | the worker witness ignores the PPID column | TestNginxReloaderNeedsANewWorkerOfTheMasterAfterTheSignal |
| w12i 8982d4cb | the worker baseline is taken before `nginx -t` | TestNginxReloaderNeedsANewWorkerOfTheMasterAfterTheSignal |
| w12i 7234a9a6 | every npm.reserved failure is remembered, not only one on darkpanel-npm | TestW12gReconcileRetriesAFailureThatIsNotTheOwnNetworkHit (its npm.reserved row, added in 13d3fc7e: the mutation survived before it) |
| w12i bc3ebca5 | joinGuard reads the network before the forward targets | TestW12gUndoOfADetachConnectsByTheInspectedID, TestA1_AttachMakesNoListingOrLookupBetweenConnectAndInspection |
| w12i 6dc6a8de | joinGuard skips the user-defined-bridge rule | TestW12gUndoOfADetachRefusesANetworkThatIsNoLongerABridge |
| w12i 58680120 | the attach's refused connect returns the refusal without inspecting NPM | TestW12gAttachChecksANetworkNPMIsOnWhenTheConnectIsRefused, TestW12jRefusedConnectOutcomes |
| w12j row 242 | a 4xx-refused ExecStart is held as pending | TestNginxReloaderLeavesNothingPendingAfterARefusedStart |
| w12j row 239 | a pending exec not running with no exit code is cleared | TestNginxReloaderKeepsPendingAnExecWithNoExitCode |
| w12j row 239 | a pending exec never expires | TestNginxReloaderDropsAPendingExecAfterItsExpiry |
| w12j row 238 | the undo of a detach connects by network name | TestW12gUndoOfADetachConnectsByTheInspectedID |
| w12j row 237 | the attach adopts a join found after a refused connect | TestW12jRefusedConnectOutcomes (two subtests) |
| w12j row 237 | the undo checks a join found after a refused reconnect on a network with a claim | TestW12jUndoOfADetachWhoseReconnectIsRefusedTakesNPMAsAlreadyThere |
| w12j row 241 | Apply answers done when its lookups failed and it found no hit | TestW12jReconcileApplyRefusesWhenItsLookupsFail |
| w12j row 241 | no throttle | TestW12jReconcileThrottlesAHitWhoseLookupKeepsFailing |
| w12j row 241 | the throttle starts at the first failure | TestW12gReconcileRetriesAnExternalHitWhoseLookupFailed |

The tests that cover the w12j fixes failed before each fix was written (red first). Two of the row-242 cases passed
before any change, because the behaviour they pin already existed: the refused start that leaves nothing pending, and
the two plain-refusal outcomes of TestW12jRefusedConnectOutcomes. The mutations above show that the first of these
catches its fault.

`sqPass` (w12g_security_followup_acceptance_test.go) now runs Verify after Apply. When Verify is made to always
answer nil, only TestW12gReconcileVerifyRequiresNoForwardedAddress fails, so no A9 assertion currently depends on that
Verify.

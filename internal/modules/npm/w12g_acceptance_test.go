package npm

import "testing"

// TestA23_EveryFixHasItsNamedRegressionTestDeclared checks that the tests the register rows cite exist.
func TestA23_EveryFixHasItsNamedRegressionTestDeclared(t *testing.T) {
	for _, name := range []string{
		"TestW12gAttachMakesNoCallBetweenConnectAndInspection", // rows 1, 176, 190
		"TestW12gAttachDisconnectsBeforeAnyLookupOrReload",     // rows 1, 176, 190
		"TestW12gAttachRereadsTheNetworkAfterTheFirstReload",   // row 190
		"TestNginxReloaderFailures",                            // row 188
		"TestW12gReloadRefusals",                               // rows 188, 198
		"TestW12gReconcileDisconnectsNPMFromAForwardedAddress", // row 191
		"TestW12gUpgradeRefusesAnUnansweredStaticForward",      // row 192
		"TestW12gUnansweredForwards",                           // rows 192, 193
	} {
		requireTestDeclared(t, ".", name)
	}
}

package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{InvalidJSON, 400},
		{InvalidInput, 422},
		{TooLarge, 413},
		{UnsupportedMediaType, 415},
		{MethodNotAllowed, 405},
		{AuthRequired, 401},
		{InvalidCredentials, 401},
		{RateLimited, 429},
		{CSRF, 403},
		{Forbidden, 403},
		{NotFound, 404},
		{ActionUnknown, 404},
		{ModuleUnavailable, 409},
		{PlanNotFound, 404},
		{PlanExpired, 410},
		{PlanStateChanged, 409},
		{PlanPrincipalMismatch, 403},
		{PlanConfirmationNeeded, 422},
		{PlanGuardFailed, 409},
		{ChangeNotFound, 404},
		{ChangeNotPending, 409},
		{LockHeld, 423},
		{Internal, 500},
		{Unavailable, 503},
		{ChangeRevertFailed, 500},
		{HostOffline, 503},
		{HostVersionUnsupported, 409},
		{AgentNotEnrolled, 409},
		{EnrollTokenInvalid, 403},
		{EnrollRateLimited, 429},
		{UpdateInProgress, 409},
		{UpdateUnconfirmedChanges, 409},
		{UpdateDowngradeRefused, 409},
		{UpdateNotFound, 404},
		{UpdateNothingToRollBack, 409},
		{RolloutInProgress, 409},
		{UpdateSignatureInvalid, 500},
		{UpdatePrevMismatch, 500},
		{UpdateInterrupted, 500},
		{ChildFailed, 500},
		{NeedsRepair, 409},
		{Recovering, 409},
		{UndoConflict, 409},
		{ChangeBusy, 409},
		{DeadlineExceeded, 504},
		{Uninstalling, 409},
		{StreamLimit, 429},
		{Code("no.such_code"), 500},
	}
	for _, tt := range tests {
		if got := tt.code.Status(); got != tt.want {
			t.Errorf("%s.Status() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

// The values are shared contracts: agents, the CLI and the web UI match on
// them.
func TestFleetCodeValues(t *testing.T) {
	for code, want := range map[Code]string{
		Unavailable:              "unavailable",
		HostOffline:              "host.offline",
		HostVersionUnsupported:   "host.version_unsupported",
		AgentNotEnrolled:         "agent.not_enrolled",
		EnrollTokenInvalid:       "enroll.token_invalid",
		EnrollRateLimited:        "enroll.rate_limited",
		UpdateInProgress:         "update.in_progress",
		UpdateUnconfirmedChanges: "update.unconfirmed_changes",
		UpdateDowngradeRefused:   "update.downgrade_refused",
		UpdateNotFound:           "update.not_found",
		UpdateNothingToRollBack:  "update.nothing_to_roll_back",
		RolloutInProgress:        "rollout.in_progress",
		UpdateSignatureInvalid:   "update.signature_invalid",
		UpdatePrevMismatch:       "update.prev_mismatch",
		UpdateInterrupted:        "update.interrupted",
	} {
		if string(code) != want || !Known(code) {
			t.Errorf("code %q: want value %q registered", code, want)
		}
	}
}

// The lifecycle codes are shared contracts too; the titles are the ones
// problem details and change errors show.
func TestLifecycleCodes(t *testing.T) {
	tests := []struct {
		code         Code
		value, title string
	}{
		{ChildFailed, "core.child_failed", "A composite step failed"},
		{NeedsRepair, "core.needs_repair", "Change needs repair"},
		{Recovering, "core.recovering", "Recovery in progress"},
		{UndoConflict, "core.undo_conflict", "Undo window closed"},
		{ChangeBusy, "core.change_busy", "Change busy"},
		{DeadlineExceeded, "core.deadline_exceeded", "Action deadline exceeded"},
		{Uninstalling, "core.uninstalling", "Host is being uninstalled"},
		{StreamLimit, "core.stream_limit", "Too many streams"},
	}
	for _, tt := range tests {
		if string(tt.code) != tt.value || !Known(tt.code) || tt.code.Title() != tt.title {
			t.Errorf("code %q: known %v, title %q; want value %q registered with title %q",
				tt.code, Known(tt.code), tt.code.Title(), tt.value, tt.title)
		}
	}
}

func TestEveryCodeHasATitle(t *testing.T) {
	for code, i := range registry {
		if i.title == "" {
			t.Errorf("%s has no title", code)
		}
	}
	if Code("no.such_code").Title() != "Internal error" {
		t.Errorf("unknown code title = %q", Code("no.such_code").Title())
	}
}

func TestFrom(t *testing.T) {
	coded := New(PlanExpired, "plan 01J expired")
	if got := From(fmt.Errorf("apply: %w", coded)); got != coded {
		t.Errorf("From(wrapped) = %v, want the original *Error", got)
	}
	cause := errors.New("disk full")
	got := From(cause)
	if got.Code != Internal || !errors.Is(got, cause) || got.Detail != "" {
		t.Errorf("From(plain) = %+v, want Internal wrapping the cause with no detail", got)
	}
}

// The module and core codes of the v1 completion spec (section 11) are
// shared contracts: modules, the CLI and the web UI match on their values,
// and problem details show their titles.
func TestSection11Codes(t *testing.T) {
	tests := []struct {
		code   Code
		value  string
		status int
		title  string
	}{
		{FirewallManualRequired, "firewall.manual_required", 409, "Manual action required"},
		{FirewallUnsupportedIntegration, "firewall.unsupported_integration", 409, "Docker integration unsupported"},
		{FirewallUnsupportedConfiguration, "firewall.unsupported_configuration", 409, "Firewall configuration unsupported"},
		{FirewallPredictionMismatch, "firewall.prediction_mismatch", 500, "Firewall state differs from the prediction"},
		{FirewallDockerUserInUse, "firewall.docker_user_in_use", 409, "DOCKER-USER holds foreign rules"},
		{DockerEngineUnavailable, "docker.engine_unavailable", 503, "Docker Engine unavailable"},
		{DockerInUse, "docker.in_use", 409, "In use"},
		{DockerOwned, "docker.owned", 409, "Owned by a Darkpanel module"},
		{DockerComposeUnavailable, "docker.compose_unavailable", 409, "Compose unavailable or project refused"},
		{TailscaleUnavailable, "tailscale.unavailable", 503, "Tailscale unavailable"},
		{TailscaleRepoConflict, "tailscale.repo_conflict", 409, "Conflicting Tailscale repository"},
		{TailscalePackagePresent, "tailscale.package_present", 409, "Tailscale already installed"},
		{TailscalePackageHeld, "tailscale.package_held", 409, "Tailscale package held"},
		{TailscalePinFailed, "tailscale.pin_failed", 500, "Tailscale pin failed"},
		{TailnetPolicyConflict, "tailnet.policy_conflict", 409, "Policy changed since the plan"},
		{TailnetPolicyInvalid, "tailnet.policy_invalid", 422, "Policy invalid"},
		{CloudNotConnected, "cloud.not_connected", 409, "Account not connected"},
		{CloudAuthFailed, "cloud.auth_failed", 502, "Provider rejected the credentials"},
		{CloudPermissionMissing, "cloud.permission_missing", 502, "Provider permission missing"},
		{CloudRateLimited, "cloud.rate_limited", 503, "Provider rate limit"},
		{CloudUnavailable, "cloud.unavailable", 503, "Provider unavailable"},
		{CloudUncertainOutcome, "cloud.uncertain_outcome", 504, "Provider outcome uncertain"},
		{CloudConflict, "cloud.conflict", 409, "Provider state changed"},
		{CloudCleanupPending, "cloud.cleanup_pending", 409, "Cleanup pending on the account"},
		{NPMUnhealthy, "npm.unhealthy", 503, "Nginx Proxy Manager unhealthy"},
		{NPMNotPublic, "npm.not_public", 409, "Host not publicly reachable"},
		{NPMSetupFailed, "npm.setup_failed", 500, "Nginx Proxy Manager setup failed"},
		{NPMReserved, "npm.reserved", 409, "Reserved by an exposure"},
		{NPMUnsupportedTarget, "npm.unsupported_target", 422, "Unsupported proxy target"},
		{CloudflareReserved, "cloudflare.reserved", 409, "Reserved by an exposure"},
		{CloudflareUnsupportedTarget, "cloudflare.unsupported_target", 422, "Unsupported tunnel target"},
		{CloudflareConnectorMissing, "cloudflare.connector_missing", 409, "No connector for the tunnel on this host"},
		{DERPRegionTaken, "derp.region_taken", 409, "DERP region id taken"},
		{DERPPortInUse, "derp.port_in_use", 409, "Port in use"},
		{DERPVerifyFailed, "derp.verify_failed", 500, "DERP verification failed"},
		{DERPNotReady, "derp.not_ready", 409, "DERP server not ready"},
		{DERPNoFallback, "derp.no_fallback", 409, "No other reachable DERP region"},
		{PanelTOTPIncomplete, "panel.totp_incomplete", 409, "TOTP not enrolled for every user"},
		{PanelAlreadyExposed, "panel.already_exposed", 409, "Panel already exposed"},
		{PanelWrongHost, "panel.wrong_host", 422, "Not the controller's host"},
		{PanelNotExposed, "panel.not_exposed", 404, "Panel not exposed"},
		{Quarantined, "core.quarantined", 409, "Controller in restore quarantine"},
	}
	for _, tt := range tests {
		if string(tt.code) != tt.value || !Known(tt.code) || tt.code.Status() != tt.status || tt.code.Title() != tt.title {
			t.Errorf("code %q: known %v, status %d, title %q; want value %q registered with status %d and title %q",
				tt.code, Known(tt.code), tt.code.Status(), tt.code.Title(), tt.value, tt.status, tt.title)
		}
	}
}

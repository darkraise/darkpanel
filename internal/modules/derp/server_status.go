package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

// serverStatusMaxWhois is the most addresses one derp.server.status read looks up.
const serverStatusMaxWhois = 256

// serverStatus is derp.server.status: the DERP server on this host and the host's place on the tailnet. It has no
// availability check, because it must answer on a host where tailscaled does not.
func (d dp) serverStatus() core.ActionDef {
	return core.DefineRead[ServerStatusInput, ServerStatus](core.ActionSpec{
		Name: ID + ".server.status", Summary: "Report the DERP server on this host and the host's place on the tailnet.",
		Version: 1, Side: core.SideAgent,
	}, serverStatusHandler{d: d})
}

// serverStatusHandler is derp.server.status's handler.
type serverStatusHandler struct{ d dp }

// Read answers the status without running a command or changing anything.
func (h serverStatusHandler) Read(ctx context.Context, env core.Env, in ServerStatusInput) (ServerStatus, error) {
	addrs, err := serverStatusParseWhois(in.Whois)
	if err != nil {
		return ServerStatus{}, err
	}
	st := ServerStatus{AvailableVersion: artifact.DerperVersion, Tags: []string{}, Addresses: []string{}, Visible: []Visibility{}}
	if err := h.readServer(ctx, env, &st); err != nil {
		return ServerStatus{}, err
	}
	if st.LinkViaTailnet, err = h.d.d.linkViaTailnet(); err != nil {
		return ServerStatus{}, fmt.Errorf("derp: read the controller address: %w", err)
	}
	if err := h.readTailnet(ctx, &st); err != nil {
		return ServerStatus{}, err
	}
	for _, addr := range addrs {
		v, err := h.visibility(ctx, addr)
		if err != nil {
			return ServerStatus{}, err
		}
		st.Visible = append(st.Visible, v)
	}
	return st, nil
}

// serverStatusParseWhois refuses a whois list that is too long or holds anything but an address without a zone.
func serverStatusParseWhois(whois []string) ([]netip.Addr, error) {
	if len(whois) > serverStatusMaxWhois {
		msg := "at most " + strconv.Itoa(serverStatusMaxWhois) + " addresses can be looked up at once"
		return nil, &errcode.Error{Code: errcode.InvalidInput, Detail: msg, Fields: []errcode.FieldError{{Path: "/whois", Message: msg}}}
	}
	addrs := make([]netip.Addr, 0, len(whois))
	for i, s := range whois {
		addr, err := netip.ParseAddr(s)
		if err != nil || addr.Zone() != "" {
			msg := "not an IP address"
			return nil, &errcode.Error{Code: errcode.InvalidInput, Detail: msg,
				Fields: []errcode.FieldError{{Path: "/whois/" + strconv.Itoa(i), Message: msg}}}
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// readServer fills st with the installed server, if any.
func (h serverStatusHandler) readServer(ctx context.Context, env core.Env, st *ServerStatus) error {
	row, ok, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil || !ok {
		return err
	}
	st.Installed, st.Mode, st.HostName, st.VerifyClients, st.DerperVersion = true, row.Mode, row.HostName, row.VerifyClients, row.DerperVersion
	unit, err := env.Units.State(ctx, server.UnitName)
	if err != nil {
		return fmt.Errorf("derp: read the state of %s: %w", server.UnitName, err)
	}
	st.UnitActive = unit.ActiveState == "active"
	if crt, _, ok := server.CertFiles(server.Config{Mode: row.Mode, HostName: row.HostName}); ok {
		if st.CertName, st.CertNotAfter, err = h.certificate(row.HostName, crt); err != nil {
			return err
		}
	}
	return nil
}

// certificate is the pin and expiry of host hostName's ip-mode certificate, read from derper's file crt. A file that
// cannot be read or parsed is derp.not_ready: the pin the server row stored may no longer be the one derper serves.
func (h serverStatusHandler) certificate(hostName, crt string) (pin string, notAfter *time.Time, err error) {
	fail := func(err error) (string, *time.Time, error) {
		return "", nil, errcode.Wrap(errcode.DERPNotReady, err, "derper's certificate for "+hostName+" cannot be read")
	}
	b, err := h.d.d.readFile(server.CertDir + "/" + crt)
	if err != nil {
		return fail(err)
	}
	der, after, err := server.ParseCert(b)
	if err != nil {
		return fail(err)
	}
	return server.Pin(der), &after, nil
}

// readTailnet fills st with the node's identity when tailscaled answers and the node is Running; the Status call has a
// bound of its own, availableWait. A tailscaled whose socket cannot be reached leaves the node off the tailnet without
// an error, so a host without tailscale keeps answering. A tailscaled that fails or does not answer in time is
// tailscale.unavailable, and the caller's own cancellation is its error.
func (h serverStatusHandler) readTailnet(ctx context.Context, st *ServerStatus) error {
	tctx, cancel := context.WithTimeout(ctx, availableWait)
	defer cancel()
	ts, err := h.d.d.local().Status(tctx)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if errors.Is(err, tslocal.ErrUnavailable) {
			return nil
		}
		return errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
	}
	if ts.BackendState != "Running" || ts.Self == nil {
		return nil
	}
	st.OnTailnet = true
	st.Tags = append(st.Tags, ts.Self.Tags...)
	slices.Sort(st.Tags)
	ips := ts.Self.TailscaleIPs
	if len(ips) == 0 {
		ips = ts.TailscaleIPs
	}
	for _, v4 := range []bool{true, false} {
		for _, ip := range ips {
			if ip.Is4() == v4 {
				st.Addresses = append(st.Addresses, ip.String())
			}
		}
	}
	return nil
}

// visibility asks tailscaled which node holds addr, within a bound of its own, availableWait. A tailscaled that cannot
// answer is an error, never "not visible".
func (h serverStatusHandler) visibility(ctx context.Context, addr netip.Addr) (Visibility, error) {
	tctx, cancel := context.WithTimeout(ctx, availableWait)
	defer cancel()
	who, err := h.d.d.local().WhoIs(tctx, addr)
	switch {
	case errors.Is(err, tslocal.ErrPeerNotFound):
		return Visibility{Address: addr.String()}, nil
	case err != nil:
		if cerr := ctx.Err(); cerr != nil {
			return Visibility{}, cerr
		}
		return Visibility{}, errcode.Wrap(errcode.TailscaleUnavailable, err, "tailscaled does not answer on its socket")
	case who == nil || who.Node == nil:
		return Visibility{Address: addr.String()}, nil
	}
	return Visibility{Address: addr.String(), Visible: true, Node: strings.TrimSuffix(who.Node.Name, ".")}, nil
}

// readStatus reads host hostID's derp.server.status through its agent, asking it to look up the addresses in whois.
func readStatus(ctx context.Context, env core.Env, hostID string, whois []string) (ServerStatus, error) {
	fail := func(err error) (ServerStatus, error) {
		return ServerStatus{}, fmt.Errorf("derp: read the DERP status of host %s: %w", hostID, err)
	}
	input, err := json.Marshal(ServerStatusInput{Whois: whois})
	if err != nil {
		return fail(err)
	}
	agent, err := env.Agents.Agent(ctx, hostID)
	if err != nil {
		return fail(err)
	}
	raw, err := agent.Read(ctx, env.Principal, core.ActionRef{Name: ID + ".server.status", Version: 1}, input)
	if err != nil {
		return fail(err)
	}
	var st ServerStatus
	if err := json.Unmarshal(raw, &st); err != nil {
		return fail(err)
	}
	return st, nil
}

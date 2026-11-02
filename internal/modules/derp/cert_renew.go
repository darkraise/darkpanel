package derp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// certRenewPoll is how often a renewal reads derper's certificate files while it waits for the new pair.
const certRenewPoll = 500 * time.Millisecond

// certRenew is derp.cert.renew: it sets the ip mode certificate pair aside under .prev names and restarts derper,
// which writes a new self-signed pair. Its undo puts the old pair back.
func (d dp) certRenew() core.ActionDef {
	return core.DefineMutating[CertRenewInput, CertRenewed](core.ActionSpec{
		Name: ID + ".cert.renew", Summary: "Replace the DERP server's self-signed certificate with a new one.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		ApplyTimeout: 45 * time.Second, Available: d.available,
	}, certRenewHandler{d: d})
}

// certRenewHandler is derp.cert.renew's handler.
type certRenewHandler struct{ d dp }

// certRenewCert is a certificate derper serves in ip mode, as the module records it; the zero value is no
// certificate.
type certRenewCert struct {
	pin      string
	notAfter time.Time
}

// certRenewState is the state document of a derp.cert effect. It holds no key material.
type certRenewState struct {
	CertName string `json:"certName"`
	NotAfter string `json:"notAfter"`
}

// certRenewDoc is the state document of c; no certificate has both members empty.
func certRenewDoc(c certRenewCert) core.StateDoc {
	s := certRenewState{CertName: c.pin}
	if c.pin != "" {
		s.NotAfter = c.notAfter.UTC().Format(time.RFC3339)
	}
	b, _ := json.Marshal(s)
	return core.StateDoc{State: b}
}

// certRenewNames is derper's certificate and key file names inside CertDir for the address ip.
func certRenewNames(ip string) (crt, key string) {
	crt, key, _ = server.CertFiles(server.Config{Mode: server.ModeIP, HostName: ip})
	return crt, key
}

// certRenewRead reads and decodes the certificate name in CertDir. An absent file is an error that matches
// fs.ErrNotExist.
func certRenewRead(d dp, name string) (certRenewCert, error) {
	path := server.CertDir + "/" + name
	b, err := d.d.readFile(path)
	if err != nil {
		return certRenewCert{}, fmt.Errorf("derp: read %s: %w", path, err)
	}
	der, notAfter, err := server.ParseCert(b)
	if err != nil {
		return certRenewCert{}, fmt.Errorf("derp: decode %s: %w", path, err)
	}
	return certRenewCert{pin: server.Pin(der), notAfter: notAfter.UTC()}, nil
}

// certRenewCheck refuses a host the renewal cannot run on, and otherwise answers its server row and the
// certificate derper serves now. It only reads.
func certRenewCheck(ctx context.Context, env core.Env, d dp) (localServer, certRenewCert, error) {
	row, found, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return localServer{}, certRenewCert{}, core.Definite(err)
	}
	if !found {
		return localServer{}, certRenewCert{}, core.Definite(errcode.New(errcode.DERPNotReady, "no DERP server is installed on this host"))
	}
	if row.Mode != server.ModeIP {
		return localServer{}, certRenewCert{}, core.Definite(errcode.New(errcode.InvalidInput, "derper renews a Let's Encrypt certificate itself"))
	}
	crt, key := certRenewNames(row.HostName)
	cur, err := certRenewRead(d, crt)
	if err != nil {
		return localServer{}, certRenewCert{}, core.Definite(errcode.New(errcode.DERPNotReady,
			"derper has not written a certificate for "+row.HostName+" yet"))
	}
	// Only the key's presence matters: its bytes are dropped here, never logged or stored.
	if _, err := d.d.readFile(server.CertDir + "/" + key); err != nil {
		return localServer{}, certRenewCert{}, core.Definite(errcode.New(errcode.DERPNotReady,
			"derper's certificate for "+row.HostName+" has no readable key file "+key))
	}
	return row, cur, nil
}

// certRenewAddress is the IP address a derp.cert resource names.
func certRenewAddress(resource string) (string, error) {
	ip, ok := strings.CutPrefix(resource, ID+".cert:")
	if _, err := netip.ParseAddr(ip); !ok || err != nil {
		return "", fmt.Errorf("derp: %q is not a resource derp.cert.renew changes", resource)
	}
	return ip, nil
}

// certRenewRename renames the file from to to inside derper's certificate directory.
func certRenewRename(ctx context.Context, env core.Env, from, to string) error {
	err := env.Files.RenameIn(ctx, ID, server.StateDir, server.CertSubdir+"/"+from, server.CertSubdir+"/"+to)
	if err != nil {
		return fmt.Errorf("derp: rename %s to %s in %s: %w", from, to, server.CertDir, err)
	}
	return nil
}

func (h certRenewHandler) Plan(ctx context.Context, env core.Env, _ CertRenewInput) (core.PlanOutcome, error) {
	_, cur, err := certRenewCheck(ctx, env, h.d)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	until := cur.notAfter.Format(time.RFC3339)
	diff := []core.DiffLine{
		{Op: "-", Text: "certificate " + cur.pin + " (valid until " + until + ")"},
		{Op: "+", Text: "a new self-signed certificate that derper generates when it restarts"},
		{Op: " ", Text: "derper restarts; connections through it drop"},
		{Op: " ", Text: "clients that hold the old pin cannot use the region until the DERP map carries the new one"},
	}
	sum := sha256.Sum256([]byte(cur.pin + "|" + until))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

// Apply journals the renewal before it touches the host. The new pin is unknown until derper writes the new pair,
// so the effect has no After; a failure leaves it intended for the engine to settle through Observe.
func (h certRenewHandler) Apply(ctx context.Context, env core.Env, _ CertRenewInput) (CertRenewed, error) {
	row, old, err := certRenewCheck(ctx, env, h.d)
	if err != nil {
		return CertRenewed{}, err
	}
	resource := ID + ".cert:" + row.HostName
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: resource, Local: true, InverseLocal: true, Before: certRenewDoc(old),
	}, nil)
	if err != nil {
		return CertRenewed{}, err
	}
	crt, key := certRenewNames(row.HostName)
	for _, name := range []string{crt, key} {
		if err := certRenewRename(ctx, env, name, name+".prev"); err != nil {
			return CertRenewed{}, err
		}
	}
	if err := env.Units.Restart(ctx, server.UnitName); err != nil {
		return CertRenewed{}, fmt.Errorf("derp: restart %s: %w", server.UnitName, err)
	}
	cur, err := certRenewAwait(ctx, env, h.d, crt, key, old.pin)
	if err != nil {
		return CertRenewed{}, err
	}
	if err := setLocalCert(ctx, env.DB, cur.pin, cur.notAfter, env.Clock.Now()); err != nil {
		return CertRenewed{}, err
	}
	if err := env.Effects.Made(ctx, seq, resource, certRenewDoc(cur)); err != nil {
		return CertRenewed{}, err
	}
	return CertRenewed{CertName: cur.pin, CertNotAfter: cur.notAfter, PriorCertName: old.pin}, nil
}

// certRenewAwait reads derper's files at once and then every certRenewPoll until the certificate crt decodes with
// a pin other than old and the key file key exists, for at most certWait. A timeout names what the last look found.
func certRenewAwait(ctx context.Context, env core.Env, d dp, crt, key, old string) (certRenewCert, error) {
	deadline := env.Clock.Now().Add(certWait)
	for {
		cur, last := certRenewRead(d, crt)
		switch {
		case last != nil:
		case cur.pin == old:
			last = errors.New("the certificate is still the old one")
		default:
			// Only the key's presence matters: its bytes are dropped here, never logged or stored.
			if _, err := d.d.readFile(server.CertDir + "/" + key); err != nil {
				last = fmt.Errorf("derp: read %s: %w", server.CertDir+"/"+key, err)
				break
			}
			return cur, nil
		}
		if !env.Clock.Now().Before(deadline) {
			return certRenewCert{}, errcode.Wrap(errcode.DERPVerifyFailed, last,
				fmt.Sprintf("derper did not write a new certificate within %s (%s)", certWait, last))
		}
		tick := make(chan struct{})
		timer := env.Clock.AfterFunc(certRenewPoll, func() { close(tick) })
		select {
		case <-tick:
		case <-ctx.Done():
			timer.Stop()
			return certRenewCert{}, fmt.Errorf("derp: wait for derper's new certificate: %w", ctx.Err())
		}
	}
}

// Verify reads only and ignores the output, which recovery passes as the zero value.
func (h certRenewHandler) Verify(ctx context.Context, env core.Env, _ CertRenewInput, _ CertRenewed) error {
	row, found, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !found {
		return errors.New("derp: no DERP server is recorded on this host")
	}
	if row.Mode != server.ModeIP {
		return fmt.Errorf("derp: the recorded server is in %s mode, not %s", row.Mode, server.ModeIP)
	}
	st, err := env.Units.State(ctx, server.UnitName)
	if err != nil {
		return err
	}
	if st.ActiveState != "active" {
		return fmt.Errorf("derp: %s is %s, not active", server.UnitName, st.ActiveState)
	}
	crt, _ := certRenewNames(row.HostName)
	cur, err := certRenewRead(h.d, crt)
	if err != nil {
		return err
	}
	if cur.pin != row.CertName {
		return fmt.Errorf("derp: the stored pin %s differs from the certificate's %s", row.CertName, cur.pin)
	}
	return nil
}

// Observe reads the certificate derper serves for the resource's address. An absent file is the empty document,
// which tells a renewal that set the old pair aside from one that never started.
func (h certRenewHandler) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	ip, err := certRenewAddress(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	crt, _ := certRenewNames(ip)
	cur, err := certRenewRead(h.d, crt)
	if errors.Is(err, fs.ErrNotExist) {
		cur, err = certRenewCert{}, nil
	}
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: certRenewDoc(cur), Matches: 1, Exclusive: true}, nil
}

// Inverse puts the .prev pair back over the new one, restarts derper and stores the old pin again once the restored
// certificate is shown to carry it.
func (h certRenewHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	ip, err := certRenewAddress(e.Resource)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	var before certRenewState
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("derp: decode the certificate before the renewal of %s: %w", ip, err))
	}
	if before.CertName == "" {
		return core.InverseResult{}, core.Definite(fmt.Errorf("derp: the renewal of %s recorded no certificate to restore", ip))
	}
	notAfter, err := time.Parse(time.RFC3339, before.NotAfter)
	if err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("derp: decode the expiry before the renewal of %s: %w", ip, err))
	}
	crt, key := certRenewNames(ip)
	if err := certRenewRename(ctx, env, crt+".prev", crt); err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if err := certRenewRename(ctx, env, key+".prev", key); err != nil {
		return core.InverseResult{}, err
	}
	if err := env.Units.Restart(ctx, server.UnitName); err != nil {
		return core.InverseResult{}, fmt.Errorf("derp: restart %s: %w", server.UnitName, err)
	}
	cur, err := certRenewRead(h.d, crt)
	if err != nil {
		return core.InverseResult{}, err
	}
	if cur.pin != before.CertName {
		return core.InverseResult{}, fmt.Errorf("derp: the restored certificate of %s has pin %s, want %s", ip, cur.pin, before.CertName)
	}
	if err := setLocalCert(ctx, env.DB, before.CertName, notAfter, env.Clock.Now()); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

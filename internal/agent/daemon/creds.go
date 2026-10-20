// Package daemon is darkpanel agent run (spec sections 2 to 5): the agent's
// credential generations, its enrollment and certificate renewal, and the
// daemon that holds the agent lock, serves the agent socket and keeps one
// mTLS session to the controller.
package daemon

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/transport"
)

// The files of one generation and the names in AgentTLSDir.
const (
	keyFile        = "agent.key"
	certFile       = "agent.crt"
	caFile         = "ca.crt"
	controllerFile = "controller.json"
	currentLink    = "current"
	currentTemp    = "current.tmp"
)

// ErrNotEnrolled is LoadCredentials' answer when AgentTLSDir has no current
// generation.
var ErrNotEnrolled = errors.New("daemon: this agent is not enrolled")

var generationPattern = regexp.MustCompile(`^gen-([1-9][0-9]{0,8})$`)

// Endpoint is controller.json: where the agent dials and the URI SAN the
// controller's certificate must carry.
type Endpoint struct {
	Address string `json:"address"`
	SAN     string `json:"san"`
}

// Credentials is one generation: the agent's key and certificate, the agent
// root it trusts and the controller it dials.
type Credentials struct {
	Generation int // 0 until saved
	Key        ed25519.PrivateKey
	Cert       *x509.Certificate
	CA         *x509.Certificate
	Controller Endpoint
}

// HostID is the host id the certificate names.
func (c *Credentials) HostID() string {
	id, _ := transport.HostIDOf(c.Cert)
	return id
}

// TLSCertificate is the certificate and key as a TLS client certificate.
func (c *Credentials) TLSCertificate() tls.Certificate {
	return tls.Certificate{Certificate: [][]byte{c.Cert.Raw}, PrivateKey: c.Key, Leaf: c.Cert}
}

// check refuses credentials the agent could not use: a certificate that is
// not an agent certificate of the key, signed by CA, or a controller.json
// that names no dialable address or no controller SAN. Validity dates are
// the controller's to check.
func (c *Credentials) check() error {
	if len(c.Key) != ed25519.PrivateKeySize || c.Cert == nil || c.CA == nil {
		return errors.New("daemon: credentials need an Ed25519 key, a certificate and the agent root")
	}
	if _, err := transport.HostIDOf(c.Cert); err != nil {
		return fmt.Errorf("daemon: agent certificate: %w", err)
	}
	pub, ok := c.Cert.PublicKey.(ed25519.PublicKey)
	if !ok || !pub.Equal(c.Key.Public()) {
		return errors.New("daemon: the agent certificate is not for the agent key")
	}
	if !slices.Contains(c.Cert.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return errors.New("daemon: the agent certificate lacks the clientAuth usage")
	}
	if err := c.Cert.CheckSignatureFrom(c.CA); err != nil {
		return fmt.Errorf("daemon: the agent certificate is not signed by the agent root: %w", err)
	}
	if _, _, err := net.SplitHostPort(c.Controller.Address); err != nil || c.Controller.Address == "" {
		return fmt.Errorf("daemon: controller address %q is not host:port", c.Controller.Address)
	}
	if !strings.HasPrefix(c.Controller.SAN, transport.ControllerURI("")) || c.Controller.SAN == transport.ControllerURI("") {
		return fmt.Errorf("daemon: %q is not a controller SAN", c.Controller.SAN)
	}
	return nil
}

// LoadCredentials reads the current generation of dir (Paths.AgentTLSDir).
// It resolves current once and reads all four files from that generation, so
// a concurrent SaveGeneration never mixes two generations. Without current it
// answers ErrNotEnrolled.
func LoadCredentials(dir string) (*Credentials, error) {
	target, err := os.Readlink(filepath.Join(dir, currentLink))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, fmt.Errorf("daemon: read the current credentials: %w", err)
	}
	m := generationPattern.FindStringSubmatch(target)
	if m == nil {
		return nil, fmt.Errorf("daemon: %s points to %q, not to a generation", filepath.Join(dir, currentLink), target)
	}
	n, _ := strconv.Atoi(m[1])
	gen := filepath.Join(dir, target)
	read := func(name string) []byte {
		if err != nil {
			return nil
		}
		var data []byte
		data, err = os.ReadFile(filepath.Join(gen, name)) //nolint:gosec // G304: gen matched generationPattern under Paths.AgentTLSDir
		return data
	}
	keyPEM, certPEM, caPEM, endpoint := read(keyFile), read(certFile), read(caFile), read(controllerFile)
	if err != nil {
		return nil, fmt.Errorf("daemon: read generation %d: %w", n, err)
	}
	c := &Credentials{Generation: n}
	if c.Key, err = parseKey(keyPEM); err != nil {
		return nil, fmt.Errorf("daemon: generation %d: %s: %w", n, keyFile, err)
	}
	if c.Cert, err = parseCertificate(certPEM); err != nil {
		return nil, fmt.Errorf("daemon: generation %d: %s: %w", n, certFile, err)
	}
	if c.CA, err = parseCertificate(caPEM); err != nil {
		return nil, fmt.Errorf("daemon: generation %d: %s: %w", n, caFile, err)
	}
	dec := json.NewDecoder(bytes.NewReader(endpoint))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c.Controller); err != nil {
		return nil, fmt.Errorf("daemon: generation %d: %s: %w", n, controllerFile, err)
	}
	if err := c.check(); err != nil {
		return nil, fmt.Errorf("daemon: generation %d: %w", n, err)
	}
	return c, nil
}

func parseKey(data []byte) (ed25519.PrivateKey, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("not one PEM PRIVATE KEY block")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	k, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("not an Ed25519 key")
	}
	return k, nil
}

// parseCertificate reads one PEM CERTIFICATE block.
func parseCertificate(data []byte) (*x509.Certificate, error) {
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("not one PEM CERTIFICATE block")
	}
	return x509.ParseCertificate(block.Bytes)
}

// The steps of SaveGeneration, in order; tests interrupt it before each.
const (
	stepCreate     = "create the generation directory"
	stepKey        = "write " + keyFile
	stepCert       = "write " + certFile
	stepCA         = "write " + caFile
	stepController = "write " + controllerFile
	stepSyncGen    = "sync the generation directory"
	stepLink       = "link " + currentTemp
	stepRename     = "rename " + currentTemp + " over " + currentLink
	stepSyncDir    = "sync the credentials directory"
)

var generationSteps = []string{
	stepCreate, stepKey, stepCert, stepCA, stepController, stepSyncGen, stepLink, stepRename, stepSyncDir,
}

// SaveGeneration writes c as a new generation of dir and makes it current
// (spec section 2): each file is written and fsynced, then the generation
// directory, then current is replaced atomically through current.tmp and
// dir is fsynced. A crash at any point leaves a complete old or a complete
// new generation; a partly written one is never current and is removed by
// PruneGenerations. It returns the new generation's number, which is
// greater than every generation in dir, complete or not.
func SaveGeneration(dir string, c *Credentials) (int, error) {
	return saveGeneration(dir, c, func(string) error { return nil })
}

func saveGeneration(dir string, c *Credentials, before func(step string) error) (int, error) {
	if err := c.check(); err != nil {
		return 0, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(c.Key)
	if err != nil {
		return 0, fmt.Errorf("daemon: encode the agent key: %w", err)
	}
	endpoint, err := json.Marshal(c.Controller)
	if err != nil {
		return 0, fmt.Errorf("daemon: encode %s: %w", controllerFile, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, fmt.Errorf("daemon: create %s: %w", dir, err)
	}
	gens, err := listGenerations(dir)
	if err != nil {
		return 0, err
	}
	n := 1
	if len(gens) > 0 {
		n = slices.Max(gens) + 1
	}
	gen := filepath.Join(dir, generationName(n))
	files := map[string][]byte{
		stepKey:        pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		stepCert:       pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Cert.Raw}),
		stepCA:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.CA.Raw}),
		stepController: endpoint,
	}
	names := map[string]string{stepKey: keyFile, stepCert: certFile, stepCA: caFile, stepController: controllerFile}
	current, temp := filepath.Join(dir, currentLink), filepath.Join(dir, currentTemp)
	for _, step := range generationSteps {
		if err := before(step); err != nil {
			return 0, err
		}
		switch step {
		case stepCreate:
			err = os.Mkdir(gen, 0o700)
		case stepKey, stepCert, stepCA, stepController:
			err = writeSynced(filepath.Join(gen, names[step]), files[step])
		case stepSyncGen:
			err = syncDir(gen)
		case stepLink:
			if err = os.Remove(temp); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
			if err == nil {
				err = os.Symlink(generationName(n), temp)
			}
		case stepRename:
			err = os.Rename(temp, current)
		case stepSyncDir:
			err = syncDir(dir)
		}
		if err != nil {
			return 0, fmt.Errorf("daemon: save generation %d: %s: %w", n, step, err)
		}
	}
	c.Generation = n
	return n, nil
}

// PruneGenerations deletes every generation of dir numbered below keep. The
// daemon calls it after the first successful handshake with generation keep.
// Newer generations and current.tmp stay: darkpanel agent enroll writes them
// without the agent lock while the daemon runs, and SaveGeneration already
// replaces a leftover current.tmp and numbers past every leftover generation.
func PruneGenerations(dir string, keep int) error {
	gens, err := listGenerations(dir)
	if err != nil {
		return err
	}
	var errs []error
	for _, n := range gens {
		if n < keep {
			errs = append(errs, os.RemoveAll(filepath.Join(dir, generationName(n))))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("daemon: prune credential generations: %w", err)
	}
	return nil
}

func generationName(n int) string { return "gen-" + strconv.Itoa(n) }

// listGenerations returns the numbers of the generation directories in
// dir, complete or not.
func listGenerations(dir string) ([]int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("daemon: list %s: %w", dir, err)
	}
	var out []int
	for _, e := range entries {
		if m := generationPattern.FindStringSubmatch(e.Name()); m != nil {
			n, _ := strconv.Atoi(m[1])
			out = append(out, n)
		}
	}
	return out, nil
}

// writeSynced creates path with mode 0600, writes data and fsyncs it.
func writeSynced(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: a file of a new generation under Paths.AgentTLSDir
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: dir is Paths.AgentTLSDir or one of its generations
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

package cli

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/controller/backup"
)

const (
	backupRoutePath = "/api/v1/controller/backup"
	// maxPassphrase is the longest passphrase, in bytes, a backup accepts.
	maxPassphrase = 1024
)

// backupCommands returns backup, restore and a.quarantineCommand(), in that
// order, to be added under "darkpanel controller".
func backupCommands(a *app) []*cobra.Command {
	f := newBackupFlow(a)
	return []*cobra.Command{f.backupCommand(), f.restoreCommand(), a.quarantineCommand()}
}

// backupFlow holds what the backup and restore commands share and the seams
// the tests replace.
type backupFlow struct {
	a         *app
	fileOwner uint32 // the uid a passphrase file must belong to; the zero value is root
	newHost   func(cmd *cobra.Command) (backup.Host, func() error, error)
}

// newBackupFlow returns the flow of the installed controller: passphrase
// files owned by root, and the production restore host.
func newBackupFlow(a *app) *backupFlow {
	return &backupFlow{a: a, newHost: func(cmd *cobra.Command) (backup.Host, func() error, error) {
		p, err := a.opts.SelectPaths(cmd, false)
		if err != nil {
			return backup.Host{}, nil, err
		}
		// The recovery runs as the controller unit's ExecStartPre before
		// every start, so it must not depend on D-Bus.
		recovering, _ := cmd.Flags().GetBool("recover")
		return backup.ProductionHost(cmd.Context(), p, !recovering)
	}}
}

// backupCommand is `controller backup`: it asks the controller over its
// socket for an encrypted backup and writes it to --out.
func (f *backupFlow) backupCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Write an encrypted backup of the controller database and key",
		Args:  cobra.NoArgs,
		RunE:  f.runBackup,
	}
	cmd.Flags().String("out", "", "the backup file to write; it must not exist")
	cmd.Flags().String("passphrase-file", "", "read the passphrase from this file (owned by root, mode 0600) instead of the terminal")
	cmd.Flags().Int("argon2-t", int(backup.DefaultParams.T), "argon2id passes, 1 to 10")
	cmd.Flags().Int("argon2-m", int(backup.DefaultParams.M), "argon2id memory in KiB, 65536 to 1048576")
	cmd.Flags().Int("argon2-p", int(backup.DefaultParams.P), "argon2id parallelism, 1 to 16")
	addConnFlags(cmd, true, false)
	return cmd
}

// restoreCommand is `controller restore`: it restores a backup into the
// installed controller with its services stopped, or with --recover
// finishes or rolls back an interrupted restore. It never talks to the
// controller.
func (f *backupFlow) restoreCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a controller backup, or finish an interrupted restore",
		Args:  cobra.NoArgs,
		RunE:  f.runRestore,
	}
	cmd.Flags().String("in", "", "the backup file to restore")
	cmd.Flags().String("passphrase-file", "", "read the passphrase from this file (owned by root, mode 0600) instead of the terminal")
	cmd.Flags().Bool("recover", false, "finish or roll back an interrupted restore, then exit")
	return cmd
}

func (f *backupFlow) runBackup(cmd *cobra.Command, _ []string) error {
	out, _ := cmd.Flags().GetString("out")
	if out == "" {
		return usagef("--out is required")
	}
	params, err := argon2Params(cmd)
	if err != nil {
		return err
	}
	if err := refuseExisting(out); err != nil {
		return err
	}
	dir := filepath.Dir(out)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("--out directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--out directory: %s is not a directory", dir)
	}
	pass, err := f.passphrase(cmd)
	if err != nil {
		return err
	}
	body, err := json.Marshal(backup.BackupRequest{Passphrase: string(pass), Argon2: params})
	clear(pass)
	if err != nil {
		return fmt.Errorf("encode the request: %w", err)
	}
	defer clear(body)
	c, err := f.a.connect(cmd)
	if err != nil {
		return err
	}
	hc, err := backupHTTPClient(c)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, c.base+backupRoutePath, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s %s: %w", http.MethodPost, backupRoutePath, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", http.MethodPost, backupRoutePath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return fmt.Errorf("%s %s: read the answer: %w", http.MethodPost, backupRoutePath, err)
		}
		return answerError(http.MethodPost, backupRoutePath, resp, data)
	}
	n, err := receiveBackup(resp.Body, out)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Backup written to %s (%d bytes); keep the passphrase, the file cannot be opened without it\n", out, n)
	return nil
}

// argon2Params returns the argon2id parameters the flags give, or nil when
// none was given so the controller applies its default.
func argon2Params(cmd *cobra.Command) (*backup.Params, error) {
	fl := cmd.Flags()
	if !fl.Changed("argon2-t") && !fl.Changed("argon2-m") && !fl.Changed("argon2-p") {
		return nil, nil
	}
	t, _ := fl.GetInt("argon2-t")
	m, _ := fl.GetInt("argon2-m")
	p, _ := fl.GetInt("argon2-p")
	switch {
	case t < 1 || t > 10:
		return nil, usagef("--argon2-t must be 1 to 10, not %d", t)
	case m < 65536 || m > 1048576:
		return nil, usagef("--argon2-m must be 65536 to 1048576 KiB, not %d", m)
	case p < 1 || p > 16:
		return nil, usagef("--argon2-p must be 1 to 16, not %d", p)
	}
	return &backup.Params{T: uint32(t), M: uint32(m), P: uint8(p)}, nil //nolint:gosec // G115: each value is within the bounds checked above
}

// refuseExisting refuses an --out that exists; Lstat makes a dangling
// symbolic link count, so the backup is never written through a link.
func refuseExisting(out string) error {
	if _, err := os.Lstat(out); err == nil {
		return fmt.Errorf("refusing to overwrite %s; remove it or choose another --out", out)
	}
	return nil
}

// receiveBackup copies body to a temporary file beside out and renames it
// to out once it is complete and synced, so out never holds a partial
// backup. It returns the number of bytes written.
func receiveBackup(body io.Reader, out string) (int64, error) {
	dir := filepath.Dir(out)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(out)+".*.partial")
	if err != nil {
		return 0, fmt.Errorf("create the backup file: %w", err)
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	n, err := io.Copy(tmp, body)
	if err != nil {
		return 0, fmt.Errorf("receive the backup: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return 0, fmt.Errorf("write the backup: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, fmt.Errorf("write the backup: %w", err)
	}
	if err := refuseExisting(out); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp.Name(), out); err != nil {
		return 0, fmt.Errorf("write the backup: %w", err)
	}
	renamed = true
	if err := syncDirectory(dir); err != nil {
		return 0, fmt.Errorf("write the backup: %w", err)
	}
	return n, nil
}

// syncDirectory fsyncs dir, so a rename in it survives a crash.
func syncDirectory(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the directory of the --out the operator chose
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// backupHTTPClient returns a client over a clone of c's transport with no
// total timeout.
func backupHTTPClient(c *client) (*http.Client, error) {
	tr, ok := c.http.Transport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("the controller client's transport is a %T, not an *http.Transport", c.http.Transport)
	}
	// A backup streams for as long as the copy takes, so neither a total nor
	// a header deadline may cut it; the command's context is the only limit.
	// The clone keeps the socket client's own timeout for every other command.
	clone := tr.Clone()
	clone.ResponseHeaderTimeout = 0
	return &http.Client{Transport: clone, CheckRedirect: c.http.CheckRedirect}, nil
}

// passphrase reads the passphrase from --passphrase-file or, without it,
// twice from the terminal, and checks its length. The caller clears it.
func (f *backupFlow) passphrase(cmd *cobra.Command) ([]byte, error) {
	var (
		pass []byte
		err  error
	)
	if path, _ := cmd.Flags().GetString("passphrase-file"); path != "" {
		pass, err = readPassphraseFile(path, f.fileOwner)
	} else {
		pass, err = f.typedPassphrase(cmd)
	}
	if err != nil {
		return nil, err
	}
	switch {
	case len(pass) == 0:
		return nil, usagef("the passphrase is empty")
	case len(pass) > maxPassphrase:
		clear(pass)
		return nil, usagef("the passphrase is longer than %d bytes", maxPassphrase)
	}
	return pass, nil
}

// typedPassphrase reads the passphrase twice on the terminal, with the
// prompts on standard error so standard output carries only the result.
func (f *backupFlow) typedPassphrase(cmd *cobra.Command) ([]byte, error) {
	if !f.a.opts.IsTerminal() {
		return nil, usagef("no terminal to read the passphrase from; pass --passphrase-file")
	}
	first, err := f.readSecret(cmd, "Passphrase: ")
	if err != nil {
		return nil, err
	}
	second, err := f.readSecret(cmd, "Repeat passphrase: ")
	if err != nil {
		clear(first)
		return nil, err
	}
	defer clear(second)
	if subtle.ConstantTimeCompare(first, second) != 1 {
		clear(first)
		return nil, usagef("the passphrases do not match")
	}
	return first, nil
}

// readSecret prompts on standard error and reads one line without echo.
func (f *backupFlow) readSecret(cmd *cobra.Command, prompt string) ([]byte, error) {
	w := cmd.ErrOrStderr()
	_, _ = io.WriteString(w, prompt)
	s, err := f.a.opts.ReadSecret()
	_, _ = io.WriteString(w, "\n")
	if err != nil {
		return nil, fmt.Errorf("read the passphrase: %w", err)
	}
	return []byte(s), nil
}

// readPassphraseFile reads a passphrase file: a regular file owned by owner
// with no permission bit outside 0600 and of at most 1025 bytes, so a
// 1024-byte passphrase fits with its newline. One trailing newline is
// removed. Errors name the path, never the content.
func readPassphraseFile(path string, owner uint32) ([]byte, error) {
	// The checks judge the open descriptor rather than the path, so nothing
	// can swap the file between the check and the read; O_NONBLOCK keeps a
	// named pipe from blocking the open before it is refused.
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the operator names the passphrase file
	if errors.Is(err, syscall.ELOOP) {
		return nil, fmt.Errorf("the passphrase file %s is a symbolic link", path)
	}
	if err != nil {
		return nil, fmt.Errorf("read the passphrase file: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("read the passphrase file: %w", err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	switch {
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("the passphrase file %s is not a regular file", path)
	case !ok:
		return nil, fmt.Errorf("the passphrase file %s: cannot read its owner", path)
	case st.Uid != owner:
		return nil, fmt.Errorf("the passphrase file %s must be owned by uid %d", path, owner)
	case st.Mode&0o7777&^0o600 != 0:
		return nil, fmt.Errorf("the passphrase file %s has permission bits outside 0600; run chmod 600 on it", path)
	}
	buf := make([]byte, maxPassphrase+2)
	n, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		clear(buf)
		return nil, fmt.Errorf("read the passphrase file: %w", err)
	}
	if n > maxPassphrase+1 {
		clear(buf)
		return nil, fmt.Errorf("the passphrase file %s is longer than %d bytes", path, maxPassphrase)
	}
	pass, _ := bytes.CutSuffix(buf[:n:n], []byte{'\n'})
	return pass, nil
}

func (f *backupFlow) runRestore(cmd *cobra.Command, _ []string) error {
	fl := cmd.Flags()
	in, _ := fl.GetString("in")
	recovering, _ := fl.GetBool("recover")
	switch {
	case recovering && (fl.Changed("in") || fl.Changed("passphrase-file")):
		return usagef("--recover takes no other flag")
	case !recovering && in == "":
		return usagef("give --in <file> to restore, or --recover to finish an interrupted restore")
	case recovering:
		return f.recover(cmd)
	}
	return f.restore(cmd, in)
}

// recover finishes or rolls back every interrupted restore. It prints
// nothing on success, so the unit's ExecStartPre stays quiet in the
// journal, and fails on any error so the unit does not start the
// controller over a half-finished restore.
func (f *backupFlow) recover(cmd *cobra.Command) error {
	host, closeHost, err := f.newHost(cmd)
	if err != nil {
		return err
	}
	return errors.Join(backup.Recover(cmd.Context(), host), closeHost())
}

// restore restores the backup at in. Restore itself refuses a caller that
// is not root and a controller whose services run.
func (f *backupFlow) restore(cmd *cobra.Command, in string) error {
	file, err := os.Open(in) //nolint:gosec // G304: the backup file the operator named
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	pass, err := f.passphrase(cmd)
	if err != nil {
		return err
	}
	host, closeHost, err := f.newHost(cmd)
	if err != nil {
		clear(pass)
		return err
	}
	res, err := backup.Restore(cmd.Context(), host, file, pass)
	clear(pass)
	if err := errors.Join(err, closeHost()); err != nil {
		return err
	}
	_, _ = io.WriteString(cmd.OutOrStdout(), restoreSummary(res))
	return nil
}

// restoreSummary is what a successful restore prints: when the backup was
// taken and how to leave the restore quarantine.
func restoreSummary(res backup.RestoreResult) string {
	return fmt.Sprintf("Restored the backup taken %s (restore %s).\n"+
		"The controller database starts in restore quarantine. Start darkpanel-controller.service and darkpanel-agent.service, then run:\n"+
		"  darkpanel controller quarantine list\n",
		res.BackupCreatedAt.UTC().Format(time.RFC3339), res.Tx)
}

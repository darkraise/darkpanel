//go:build harness

// Package vm boots QEMU/KVM virtual machines from pinned cloud images for the
// integration harness (ADR 0008, spec section 18) and drives them over SSH.
// Its methods are the fault-injection building blocks later milestones'
// tests use.
package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// Fetch returns the path of the image at url in cacheDir, downloading it
// unless the cache already holds a copy whose SHA-256 is sum. A download is
// checked before it enters the cache, so the cache holds verified images only.
func Fetch(ctx context.Context, url, sum, cacheDir string) (string, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return "", fmt.Errorf("vm: image cache: %w", err)
	}
	path := filepath.Join(cacheDir, sum+".img")
	if got, err := fileSHA256(path); err == nil && got == sum {
		return path, nil
	}
	tmp, err := os.CreateTemp(cacheDir, ".download-*")
	if err != nil {
		return "", fmt.Errorf("vm: image cache: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // a no-op after the rename
	got, err := download(ctx, url, tmp)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("vm: download %s: %w", url, err)
	}
	if got != sum {
		return "", fmt.Errorf("vm: %s has SHA-256 %s, want %s; update the harness pins", url, got, sum)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", fmt.Errorf("vm: image cache: %w", err)
	}
	return path, nil
}

// download writes the body at url to w and returns its SHA-256.
func download(ctx context.Context, url string, w io.Writer) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(w, h), resp.Body); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSHA256 returns the SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // G304: a file in the harness's own image cache
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

package cloud

import (
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// credentialAD is the associated data that binds a sealed account secret
// to its row (ADR 0005), so a ciphertext copied to another row or column
// does not open.
func credentialAD(table, column, id string) (string, error) {
	if table == "" || column == "" || id == "" {
		return "", errors.New("cloud: a sealed credential needs a table, a column and a row id")
	}
	return table + "." + column + "." + id, nil
}

// SealCredential seals an account secret with associated data
// "<table>.<column>.<id>".
func SealCredential(s core.Sealer, table, column, id string, secret []byte) ([]byte, error) {
	ad, err := credentialAD(table, column, id)
	if err != nil {
		return nil, err
	}
	ct, err := s.Seal(ad, secret)
	if err != nil {
		return nil, fmt.Errorf("cloud: seal %s: %w", ad, err)
	}
	return ct, nil
}

// OpenCredential opens what SealCredential sealed for the same table,
// column and id.
func OpenCredential(s core.Sealer, table, column, id string, ciphertext []byte) ([]byte, error) {
	ad, err := credentialAD(table, column, id)
	if err != nil {
		return nil, err
	}
	pt, err := s.Open(ad, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("cloud: open %s: %w", ad, err)
	}
	return pt, nil
}

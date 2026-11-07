package dstate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const v1r14Secret = "placeholder-secret"

// v1r14Want is the document form of the secret under key.
func v1r14Want(key []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(v1r14Secret))
	return "hmac-sha256:" + hex.EncodeToString(m.Sum(nil))
}

func v1r14Labels(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var d struct {
		Labels map[string]string `json:"labels"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d.Labels
}

func v1r14Renderers() map[string]func(key []byte) (json.RawMessage, error) {
	network := &dockerapi.Network{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{"token": v1r14Secret}}
	volume := &dockerapi.Volume{Name: "data", Labels: map[string]string{"token": v1r14Secret}}
	return map[string]func(key []byte) (json.RawMessage, error){
		"network": func(key []byte) (json.RawMessage, error) { return NetworkCreatedKeyed(network, key) },
		"volume":  func(key []byte) (json.RawMessage, error) { return VolumeCreatedKeyed(volume, true, key) },
	}
}

// TestV1r14KeyedLabelFingerprints: a label value is recorded as the HMAC-SHA256 of the value under the given key, so
// the same value gives another document under another key and no value or plain digest is in the document.
func TestV1r14KeyedLabelFingerprints(t *testing.T) {
	keyA, keyB := []byte("placeholder-key-a"), []byte("placeholder-key-b")
	unsalted := sha256.Sum256([]byte(v1r14Secret))
	for name, render := range v1r14Renderers() {
		t.Run(name, func(t *testing.T) {
			raw, err := render(keyA)
			if err != nil {
				t.Fatal(err)
			}
			if got := v1r14Labels(t, raw); !reflect.DeepEqual(got, map[string]string{"token": v1r14Want(keyA)}) {
				t.Errorf("labels %v, want the keyed fingerprint %s", got, v1r14Want(keyA))
			}
			if text := string(raw); strings.Contains(text, v1r14Secret) || strings.Contains(text, hex.EncodeToString(unsalted[:])) {
				t.Errorf("the document %s holds the label value or its unsalted digest", text)
			}
			other, err := render(keyB)
			if err != nil {
				t.Fatal(err)
			}
			if string(other) == string(raw) {
				t.Errorf("two keys render one document %s", raw)
			}
			again, err := render(keyA)
			if err != nil || string(again) != string(raw) {
				t.Errorf("one key rendered %s and then %s (error %v)", raw, again, err)
			}
		})
	}
}

// TestV1r14UnkeyedRenderingRecordsNoLabelValue: without a key a document keeps the label keys and records no
// value, not even a digest, and the two-argument forms behave as an empty key.
func TestV1r14UnkeyedRenderingRecordsNoLabelValue(t *testing.T) {
	unsalted := sha256.Sum256([]byte(v1r14Secret))
	network := &dockerapi.Network{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{"token": v1r14Secret}}
	volume := &dockerapi.Volume{Name: "data", Labels: map[string]string{"token": v1r14Secret}}
	renders := map[string]func() (json.RawMessage, error){
		"NetworkCreated":            func() (json.RawMessage, error) { return NetworkCreated(network) },
		"NetworkCreatedKeyed nil":   func() (json.RawMessage, error) { return NetworkCreatedKeyed(network, nil) },
		"NetworkCreatedKeyed empty": func() (json.RawMessage, error) { return NetworkCreatedKeyed(network, []byte{}) },
		"VolumeCreated":             func() (json.RawMessage, error) { return VolumeCreated(volume, true) },
		"VolumeCreatedKeyed nil":    func() (json.RawMessage, error) { return VolumeCreatedKeyed(volume, true, nil) },
	}
	for name, render := range renders {
		t.Run(name, func(t *testing.T) {
			raw, err := render()
			if err != nil {
				t.Fatal(err)
			}
			if got := v1r14Labels(t, raw); !reflect.DeepEqual(got, map[string]string{"token": ""}) {
				t.Errorf("labels %v, want the key token with no value", got)
			}
			if text := string(raw); strings.Contains(text, v1r14Secret) || strings.Contains(text, hex.EncodeToString(unsalted[:])) {
				t.Errorf("the document %s holds the label value or a digest of it", text)
			}
		})
	}
}

func TestV1r14LabelFingerprintsOfNoLabels(t *testing.T) {
	for _, labels := range []map[string]string{nil, {}} {
		if got := LabelFingerprints([]byte("placeholder-key-a"), labels); got != nil {
			t.Errorf("LabelFingerprints of %v = %v, want nil", labels, got)
		}
	}
}

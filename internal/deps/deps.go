//go:build deps

// Package deps keeps every M1 dependency in go.mod until code imports it.
package deps

// Blank imports only: each line pins one module version for a later part.
import (
	_ "github.com/invopop/jsonschema"            // schema reflection
	_ "github.com/mdp/qrterminal/v3"             // TOTP enrollment QR code
	_ "github.com/oklog/ulid/v2"                 // entity ids
	_ "github.com/santhosh-tekuri/jsonschema/v6" // input validation
	_ "golang.org/x/crypto/argon2"               // password hashing
	_ "golang.org/x/crypto/chacha20poly1305"     // sealer
	_ "golang.org/x/crypto/hkdf"                 // subkeys
	_ "golang.org/x/crypto/ssh"                  // harness SSH client
	_ "golang.org/x/sys/unix"                    // openat2, SO_PEERCRED, IP_FREEBIND
	_ "golang.org/x/term"                        // password prompts
	_ "modernc.org/sqlite"                       // SQLite without CGO
)

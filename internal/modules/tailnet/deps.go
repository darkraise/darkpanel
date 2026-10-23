package tailnet

import (
	// The policy editor parses and rewrites the tailnet policy file with its
	// comments kept (spec 4.2); this import keeps the parser pinned in
	// go.mod, which is frozen once the module skeletons exist.
	_ "github.com/tailscale/hujson"
)

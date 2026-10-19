//go:build harnesskeys

package trustedkeys

// keys trusts only the harness test key, whose private half is committed as
// testdata/harness.key: anyone can sign for it, which is why only a build
// with the harnesskeys tag carries it.
var keys = []Key{
	{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"},
}

const (
	testBuild = true
	// baseURL is the scanner VM, which serves the harness's test releases
	// over plain HTTP; signatures, not the transport, make them trusted.
	baseURL = "http://198.18.0.20:8000"
)

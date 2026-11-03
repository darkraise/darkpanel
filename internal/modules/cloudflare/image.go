package cloudflare

// Image is the cloudflared image the connector runs, pinned by the digest of
// its multi-arch index.
const Image = "cloudflare/cloudflared:2026.10.7@sha256:4955524aeaa379e154023d5a027c250f7b35041a2e7e950cf13f144bc9b9c5ba"

// imageUID is the user and group the image runs as, and so the owner and group
// of the token file.
const imageUID = 65532

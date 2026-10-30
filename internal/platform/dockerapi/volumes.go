package dockerapi

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Volume is a Docker volume.
type Volume struct {
	Name       string
	Driver     string
	Mountpoint string
	CreatedAt  string
	Scope      string
	Labels     map[string]string
	Options    map[string]string
}

// VolumeCreate is a POST /volumes/create request.
type VolumeCreate struct {
	Name       string
	Driver     string            `json:",omitempty"`
	DriverOpts map[string]string `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
}

// ListVolumes lists volumes.
func (c *Client) ListVolumes(ctx context.Context, filters Filters) ([]Volume, error) {
	q := url.Values{}
	if err := filters.set(q); err != nil {
		return nil, err
	}
	var out struct {
		Volumes []Volume
	}
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/volumes", query: q}, &out)
	return out.Volumes, err
}

// InspectVolume inspects a volume by name.
func (c *Client) InspectVolume(ctx context.Context, name string) (Volume, error) {
	if err := requireID("volume", name); err != nil {
		return Volume{}, err
	}
	var out Volume
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/volumes/" + url.PathEscape(name)}, &out)
	return out, err
}

// CreateVolume creates a volume; the daemon returns an existing volume of
// the same name and driver unchanged.
func (c *Client) CreateVolume(ctx context.Context, v VolumeCreate) (Volume, error) {
	var out Volume
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/volumes/create", body: v}, &out)
	return out, err
}

// RemoveVolume removes a volume; the daemon answers 409 while a container
// uses it.
func (c *Client) RemoveVolume(ctx context.Context, name string, force bool) error {
	if err := requireID("volume", name); err != nil {
		return err
	}
	q := url.Values{"force": {strconv.FormatBool(force)}}
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/volumes/" + url.PathEscape(name), query: q}, nil)
	return err
}

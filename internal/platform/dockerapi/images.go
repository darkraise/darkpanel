package dockerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ImageSummary is one entry of GET /images/json.
type ImageSummary struct {
	ID          string `json:"Id"`
	ParentID    string `json:"ParentId"`
	RepoTags    []string
	RepoDigests []string
	Created     int64 // Unix seconds
	Size        int64
	Labels      map[string]string
	Containers  int64 // -1 unless the daemon computed it
	// SharedSize is the bytes this image shares with others, -1 unless
	// ListImagesOptions.SharedSize asked for it; Size minus SharedSize is what
	// removing the image alone reclaims.
	SharedSize int64
}

// ImageInspect is GET /images/{name}/json.
type ImageInspect struct {
	ID           string `json:"Id"`
	RepoTags     []string
	RepoDigests  []string
	Created      string // RFC 3339 with nanoseconds
	Size         int64
	Architecture string
	Os           string
	Config       *ContainerConfig
}

// PullMessage is one progress message of an image pull.
type PullMessage struct {
	Status         string          `json:"status,omitempty"`
	ID             string          `json:"id,omitempty"`
	Progress       string          `json:"progress,omitempty"`
	ProgressDetail *ProgressDetail `json:"progressDetail,omitempty"`
	Error          string          `json:"error,omitempty"`
	ErrorDetail    *ErrorDetail    `json:"errorDetail,omitempty"`
}

// ProgressDetail is a layer's transfer progress in bytes.
type ProgressDetail struct {
	Current int64 `json:"current,omitempty"`
	Total   int64 `json:"total,omitempty"`
}

// ErrorDetail is a failure the daemon reports inside a stream.
type ErrorDetail struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message"`
}

// Err returns the failure the message reports, or nil. A pull that fails
// after it started answers 200 and reports the failure in the stream.
func (m PullMessage) Err() error {
	switch {
	case m.ErrorDetail != nil && m.ErrorDetail.Message != "":
		return fmt.Errorf("dockerapi: pull: %s", m.ErrorDetail.Message)
	case m.Error != "":
		return fmt.Errorf("dockerapi: pull: %s", m.Error)
	}
	return nil
}

// ImageDeleteItem is one entry of DELETE /images/{name}'s answer.
type ImageDeleteItem struct {
	Untagged string `json:",omitempty"`
	Deleted  string `json:",omitempty"`
}

// ListImagesOptions selects the images GET /images/json lists.
type ListImagesOptions struct {
	All        bool // include intermediate images
	SharedSize bool // compute ImageSummary.SharedSize
	Filters    Filters
}

// ListImages lists images.
func (c *Client) ListImages(ctx context.Context, opts ListImagesOptions) ([]ImageSummary, error) {
	q := url.Values{}
	if opts.All {
		q.Set("all", "1")
	}
	if opts.SharedSize {
		q.Set("shared-size", "1")
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	var out []ImageSummary
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/images/json", query: q}, &out)
	return out, err
}

// InspectImage inspects an image by ID or reference.
func (c *Client) InspectImage(ctx context.Context, ref string) (ImageInspect, error) {
	if err := checkImageRef(ref); err != nil {
		return ImageInspect{}, err
	}
	var out ImageInspect
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/images/" + ref + "/json"}, &out)
	return out, err
}

// PullStream reads the progress of an image pull.
type PullStream struct {
	s *Stream[PullMessage]
}

// Next returns the next progress message, or io.EOF once the pull ends. A
// message that reports a failure is returned together with that failure as
// the error, so a pull that failed after the daemon answered 200 cannot be
// mistaken for one that finished.
func (p *PullStream) Next() (PullMessage, error) {
	m, err := p.s.Next()
	if err != nil {
		return m, err
	}
	return m, m.Err()
}

// Close ends the stream.
func (p *PullStream) Close() error { return p.s.Close() }

// PullImage starts pulling ref and returns its progress stream; the caller
// reads it until Next returns io.EOF, and any other error means the pull
// failed. A reference without a tag or digest pulls the tag "latest": the
// daemon would otherwise pull every tag of the repository.
func (c *Client) PullImage(ctx context.Context, ref string) (*PullStream, error) {
	if err := checkImageRef(ref); err != nil {
		return nil, err
	}
	name, tag := splitImageRef(ref)
	q := url.Values{"fromImage": {name}, "tag": {tag}}
	res, err := c.do(ctx, request{method: http.MethodPost, path: "/images/create", query: q})
	if err != nil {
		return nil, err
	}
	return &PullStream{s: newStream[PullMessage](res)}, nil
}

// splitImageRef splits a reference into the fromImage and tag arguments:
// a digest reference stays whole with no tag.
func splitImageRef(ref string) (name, tag string) {
	if strings.Contains(ref, "@") {
		return ref, ""
	}
	slash := strings.LastIndex(ref, "/")
	if colon := strings.LastIndex(ref, ":"); colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// RemoveImageOptions modifies DELETE /images/{name}.
type RemoveImageOptions struct {
	Force   bool
	NoPrune bool // keep untagged parents
}

// RemoveImage removes an image by ID or reference.
func (c *Client) RemoveImage(ctx context.Context, ref string, opts RemoveImageOptions) ([]ImageDeleteItem, error) {
	if err := checkImageRef(ref); err != nil {
		return nil, err
	}
	q := url.Values{"force": {strconv.FormatBool(opts.Force)}, "noprune": {strconv.FormatBool(opts.NoPrune)}}
	var out []ImageDeleteItem
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/images/" + ref, query: q}, &out)
	return out, err
}

// checkImageRef refuses a reference that could leave the path segment: the
// daemon routes image references with their slashes unescaped, so the
// reference cannot be path-escaped. A trailing colon is refused too: it
// splits into an empty tag, which the daemon reads as "pull every tag".
func checkImageRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("dockerapi: invalid image reference: %w", ErrEmptyID)
	}
	if strings.ContainsAny(ref, "?#% \t\r\n") || strings.Contains(ref, "..") || strings.HasSuffix(ref, ":") {
		return errors.New("dockerapi: invalid image reference")
	}
	return nil
}

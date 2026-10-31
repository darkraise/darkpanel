package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// pullApplyTimeout bounds one pull, which downloads every layer it lacks.
const pullApplyTimeout = 30 * time.Minute

func (k dk) imagePull(h *pullHub) core.ActionDef {
	return core.DefineMutating[ImagePullInput, PulledImage](core.ActionSpec{
		Name: ID + ".image.pull", Summary: "Pull an image from its registry.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		LongRunning: true, ApplyTimeout: pullApplyTimeout,
	}, imagePullHandler{k: k, hub: h})
}

// imagePullHandler pulls one image reference and publishes its progress to
// the hub under the change's id. A pulled or re-pointed tag is not reverted,
// so it journals no effect and its undo changes nothing.
type imagePullHandler struct {
	k   dk
	hub *pullHub
}

// pullRefError refuses a reference the Engine client would refuse, so a
// pull never reaches it.
func pullRefError(ref string) error {
	msg := ""
	switch {
	case ref == "":
		msg = "image is required"
	case strings.IndexFunc(ref, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0,
		strings.ContainsAny(ref, "?#%"), strings.Contains(ref, ".."), strings.HasSuffix(ref, ":"):
		msg = "image is not a valid reference"
	default:
		return nil
	}
	return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
		Fields: []errcode.FieldError{{Path: "/image", Message: msg}}})
}

// pullShortID is an image id's first 12 hex digits.
func pullShortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	return id[:min(12, len(id))]
}

func (h imagePullHandler) Plan(ctx context.Context, _ core.Env, in ImagePullInput) (core.PlanOutcome, error) {
	if err := pullRefError(in.Image); err != nil {
		return core.PlanOutcome{}, err
	}
	local := ""
	img, err := h.k.d.docker.InspectImage(ctx, in.Image)
	switch {
	case err == nil:
		local = img.ID
	case dockerapi.IsNotFound(err):
	default:
		return core.PlanOutcome{}, engineError("image", in.Image, err)
	}
	state := "image " + in.Image + " is not present locally"
	if local != "" {
		state = "image " + in.Image + " is local as " + pullShortID(local) + "; a moved tag is re-pointed to the pulled image"
	}
	sum := sha256.Sum256([]byte(in.Image + "\x00" + local))
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: "pull image " + in.Image + " from its registry"},
			{Op: " ", Text: state},
			{Op: " ", Text: "undo changes nothing: a pulled or re-pointed tag is not reverted"},
		},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// Apply pulls with the apply context, whose deadline is the action's
// ApplyTimeout, and ends the change's feed on every return.
func (h imagePullHandler) Apply(ctx context.Context, env core.Env, in ImagePullInput) (PulledImage, error) {
	if env.ChangeID == "" {
		return PulledImage{}, errors.New("docker: an image pull needs a change id")
	}
	defer h.hub.finish(env.ChangeID)
	s, err := h.k.d.docker.PullImage(ctx, in.Image)
	if err != nil {
		return PulledImage{}, engineError("image", in.Image, err)
	}
	defer func() { _ = s.Close() }()
	for {
		m, err := s.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			h.hub.publish(env.ChangeID, PullEvent{Status: m.Status, ID: m.ID, Error: err.Error()})
			return PulledImage{}, fmt.Errorf("docker: pull %s: %w", in.Image, err)
		}
		h.hub.publish(env.ChangeID, pullEventOf(m))
	}
	img, err := h.k.d.docker.InspectImage(ctx, in.Image)
	if err != nil {
		return PulledImage{}, engineError("image", in.Image, err)
	}
	return PulledImage{Image: in.Image, ID: img.ID, Digests: append([]string{}, img.RepoDigests...)}, nil
}

func pullEventOf(m dockerapi.PullMessage) PullEvent {
	e := PullEvent{Status: m.Status, ID: m.ID, Progress: m.Progress}
	if m.ProgressDetail != nil {
		e.Current, e.Total = m.ProgressDetail.Current, m.ProgressDetail.Total
	}
	return e
}

// Verify does not consult the output: recovery passes the zero output.
func (h imagePullHandler) Verify(ctx context.Context, _ core.Env, in ImagePullInput, _ PulledImage) error {
	if _, err := h.k.d.docker.InspectImage(ctx, in.Image); err != nil {
		return engineError("image", in.Image, err)
	}
	return nil
}

func (h imagePullHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse changes nothing: a pulled or re-pointed tag is not reverted.
func (h imagePullHandler) Inverse(context.Context, core.Env, core.Effect) (core.InverseResult, error) {
	return core.InverseResult{}, nil
}

// pullStreamDef replays and follows the progress of one change's pull.
func (k dk) pullStreamDef(h *pullHub) core.StreamDef {
	return core.DefineStream[PullParams, PullEvent]("pull", func(ctx context.Context, _ core.Env, p PullParams, emit func(PullEvent) error) error {
		if p.ChangeID == "" {
			return streamInvalid("/changeId", "changeId is required")
		}
		ch, stop := h.subscribe(ctx, p.ChangeID)
		defer stop()
		for e := range ch {
			if err := emit(e); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		// Checked before the deferred stop, which forgets the lag.
		if h.lagged(ch) {
			return &errcode.Error{Code: errcode.Unavailable, Detail: "the reader fell behind the pull; open the stream again to replay its latest events"}
		}
		return nil
	})
}

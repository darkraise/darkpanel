package tuning

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

const (
	swapLockKey = "swap"
	fstab       = "/etc/fstab"
	fstabBlock  = "swap"
	fstabLine   = platform.SwapFile + " none swap sw 0 0\n"
	swapEffect  = ID + ".swap:" + platform.SwapFile
	// swapHeadroom is the free space a new swap file leaves on the root
	// filesystem.
	swapHeadroom = 1 << 30
)

// swapFilesystems are the root filesystems a swap file made by fallocate
// works on.
var swapFilesystems = []string{"ext4", "xfs"}

// SwapEntry is one swap area of tuning.swap.get.
type SwapEntry struct {
	Path      string `json:"path"`
	Type      string `json:"type" jsonschema_description:"file or partition."`
	Size      int64  `json:"size" jsonschema_description:"Bytes."`
	Used      int64  `json:"used" jsonschema_description:"Bytes."`
	Darkpanel bool   `json:"darkpanel" jsonschema_description:"Darkpanel created it; other swap is read-only."`
}

// SwapState is the answer of tuning.swap.get.
type SwapState struct {
	Swaps           []SwapEntry `json:"swaps"`
	MemoryTotal     int64       `json:"memoryTotal" jsonschema_description:"Bytes."`
	MemoryAvailable int64       `json:"memoryAvailable" jsonschema_description:"Bytes."`
	Filesystem      string      `json:"filesystem" jsonschema_description:"The root filesystem's type."`
	Free            int64       `json:"free" jsonschema_description:"Bytes free on the root filesystem."`
	Unavailable     string      `json:"unavailable,omitempty" jsonschema_description:"Why Darkpanel cannot manage a swap file on this host."`
}

// SwapSizeInput is the input of tuning.swap.create and tuning.swap.resize.
type SwapSizeInput struct {
	SizeMiB int64 `json:"sizeMiB" jsonschema:"minimum=256,maximum=65536" jsonschema_description:"The size of /darkpanel.swap in MiB, at most the free space on / less 1 GiB."`
}

// SwapChanged is the answer of the swap actions.
type SwapChanged struct {
	SizeMiB int64 `json:"sizeMiB" jsonschema_description:"The size of /darkpanel.swap afterwards in MiB; 0 when it is removed."`
}

// swapNow is the swap file as the host holds it.
type swapNow struct {
	size   int64 // bytes; 0 when the file is absent
	active bool
	used   int64
	areas  []platform.SwapArea
	mem    platform.Memory
}

func (m module) readSwap() (swapNow, error) {
	var now swapNow
	size, err := m.d.fileSize(platform.SwapFile)
	switch {
	case err == nil:
		now.size = size
	case !errors.Is(err, fs.ErrNotExist):
		return swapNow{}, fmt.Errorf("tuning: stat %s: %w", platform.SwapFile, err)
	}
	data, err := m.d.readFile("/proc/swaps")
	if err != nil {
		return swapNow{}, fmt.Errorf("tuning: read /proc/swaps: %w", err)
	}
	if now.areas, err = platform.ParseSwaps(data); err != nil {
		return swapNow{}, err
	}
	for _, a := range now.areas {
		if a.Path == platform.SwapFile {
			now.active, now.used = true, a.Used
		}
	}
	if data, err = m.d.readFile("/proc/meminfo"); err != nil {
		return swapNow{}, fmt.Errorf("tuning: read /proc/meminfo: %w", err)
	}
	if now.mem, err = platform.ParseMeminfo(data); err != nil {
		return swapNow{}, err
	}
	return now, nil
}

// rootFilesystem is the type of the filesystem mounted on /, the last
// mount there in /proc/self/mounts.
func (m module) rootFilesystem() (string, error) {
	data, err := m.d.readFile("/proc/self/mounts")
	if err != nil {
		return "", fmt.Errorf("tuning: read /proc/self/mounts: %w", err)
	}
	fsType := ""
	for line := range strings.Lines(string(data)) {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == "/" {
			fsType = f[2]
		}
	}
	if fsType == "" {
		return "", errors.New("tuning: /proc/self/mounts names no filesystem on /")
	}
	return fsType, nil
}

// swapAvailable makes the swap changes unavailable on a root filesystem
// fallocate cannot make a swap file on.
func (m module) swapAvailable(context.Context, core.Env) error {
	fsType, err := m.rootFilesystem()
	if err != nil {
		return err
	}
	if !slices.Contains(swapFilesystems, fsType) {
		return fmt.Errorf("swap files need an ext4 or xfs root filesystem; / is %s", fsType)
	}
	return nil
}

func (m module) swapActions() []core.ActionDef {
	spec := func(verb, summary string) core.ActionSpec {
		return core.ActionSpec{
			Name: ID + ".swap." + verb, Version: 1, Summary: summary, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{swapLockKey}, MinScope: core.ScopeAdmin,
			Available: m.swapAvailable, ApplyTimeout: 45 * time.Second,
		}
	}
	return []core.ActionDef{
		core.DefineRead[struct{}, SwapState](core.ActionSpec{
			Name: ID + ".swap.get", Version: 1, Class: core.ClassRead, Side: core.SideAgent,
			Summary: "List swap areas and memory, and whether Darkpanel can make a swap file here.",
		}, swapGetter{m: m}),
		core.DefineMutating[SwapSizeInput, SwapChanged](spec("create", "Create /darkpanel.swap, turn it on, and add it to /etc/fstab."),
			swapHandler[SwapSizeInput]{m: m, verb: "create"}),
		core.DefineMutating[SwapSizeInput, SwapChanged](spec("resize", "Turn /darkpanel.swap off, rewrite it at a new size, and turn it on."),
			swapHandler[SwapSizeInput]{m: m, verb: "resize"}),
		core.DefineMutating[struct{}, SwapChanged](spec("remove", "Turn /darkpanel.swap off, remove it from /etc/fstab, and delete it."),
			swapHandler[struct{}]{m: m, verb: "remove"}),
	}
}

type swapGetter struct{ m module }

func (g swapGetter) Read(ctx context.Context, env core.Env, _ struct{}) (SwapState, error) {
	now, err := g.m.readSwap()
	if err != nil {
		return SwapState{}, err
	}
	ours, err := env.Files.Owns(ctx, ID, platform.SwapFile)
	if err != nil {
		return SwapState{}, err
	}
	out := SwapState{Swaps: []SwapEntry{}, MemoryTotal: now.mem.Total, MemoryAvailable: now.mem.Available}
	for _, a := range now.areas {
		out.Swaps = append(out.Swaps, SwapEntry{Path: a.Path, Type: a.Type, Size: a.Size, Used: a.Used, Darkpanel: ours && a.Path == platform.SwapFile})
	}
	if out.Filesystem, err = g.m.rootFilesystem(); err != nil {
		return SwapState{}, err
	}
	if out.Free, err = g.m.d.freeSpace("/"); err != nil {
		return SwapState{}, fmt.Errorf("tuning: free space on /: %w", err)
	}
	if err := g.m.swapAvailable(ctx, env); err != nil {
		out.Unavailable = err.Error()
	}
	return out, nil
}

// swapDoc is the state document of the swap file: its size in MiB, null
// when it is absent.
func swapDoc(size int64) core.StateDoc {
	if size == 0 {
		return core.StateDoc{State: json.RawMessage("null")}
	}
	b, _ := json.Marshal(struct {
		SizeMiB int64 `json:"sizeMiB"`
	}{size / platform.MiB})
	return core.StateDoc{State: b}
}

func docSize(d core.StateDoc) (int64, error) {
	var s *struct {
		SizeMiB int64 `json:"sizeMiB"`
	}
	if err := json.Unmarshal(d.State, &s); err != nil {
		return 0, err
	}
	if s == nil {
		return 0, nil
	}
	return s.SizeMiB * platform.MiB, nil
}

func mib(size int64) string { return fmt.Sprintf("%d MiB", size/platform.MiB) }

// swapHandler is one of tuning.swap.create, resize and remove; its effect
// is the swap file's size, which its undo puts back.
type swapHandler[In any] struct {
	m    module
	verb string
}

func (h swapHandler[In]) want(in In) int64 {
	if s, ok := any(in).(SwapSizeInput); ok {
		return s.SizeMiB * platform.MiB
	}
	return 0
}

// plan checks the change against the host: what is there, the sizes, the
// free space and, before swapoff, the memory guard.
func (h swapHandler[In]) plan(ctx context.Context, env core.Env, in In) (swapNow, int64, error) {
	want := h.want(in)
	if h.verb != "remove" && (want < platform.SwapMinSize || want > platform.SwapMaxSize) {
		return swapNow{}, 0, invalid("/sizeMiB", fmt.Sprintf("Use %d to %d MiB", platform.SwapMinSize/platform.MiB, platform.SwapMaxSize/platform.MiB))
	}
	now, err := h.m.readSwap()
	if err != nil {
		return swapNow{}, 0, err
	}
	ours, err := env.Files.Owns(ctx, ID, platform.SwapFile)
	if err != nil {
		return swapNow{}, 0, err
	}
	switch {
	case h.verb == "create" && (now.size > 0 || now.active):
		return swapNow{}, 0, invalid("", platform.SwapFile+" exists already; resize or remove it")
	case h.verb != "create" && now.size == 0:
		return swapNow{}, 0, invalid("", platform.SwapFile+" does not exist; create it")
	case h.verb != "create" && !ours:
		return swapNow{}, 0, invalid("", "Darkpanel did not create "+platform.SwapFile+", so it leaves it alone")
	case h.verb == "resize" && want == now.size:
		return swapNow{}, 0, invalid("/sizeMiB", platform.SwapFile+" is "+mib(want)+" already")
	}
	if want > 0 {
		free, err := h.m.d.freeSpace("/")
		if err != nil {
			return swapNow{}, 0, fmt.Errorf("tuning: free space on /: %w", err)
		}
		if room := free + now.size - swapHeadroom; want > room {
			return swapNow{}, 0, invalid("/sizeMiB", fmt.Sprintf("At most %s: / has %s free and keeps 1 GiB", mib(max(room, 0)), mib(free)))
		}
	}
	if now.active {
		if refusal := platform.SwapoffRefusal(now.used, now.mem); refusal != "" {
			return swapNow{}, 0, core.Definite(errcode.New(errcode.InvalidInput, refusal))
		}
	}
	return now, want, nil
}

func (h swapHandler[In]) Plan(ctx context.Context, env core.Env, in In) (core.PlanOutcome, error) {
	now, want, err := h.plan(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	switch h.verb {
	case "create":
		diff = []core.DiffLine{{Op: "+", Text: "Create " + platform.SwapFile + " of " + mib(want) + " and turn it on"}, {Op: "+", Text: fstab + ": " + strings.TrimSpace(fstabLine)}}
	case "resize":
		diff = []core.DiffLine{{Op: " ", Text: "Resize " + platform.SwapFile + " from " + mib(now.size) + " to " + mib(want) + "; it is off while it is rewritten"}}
	default:
		diff = []core.DiffLine{{Op: "-", Text: "Turn off and delete " + platform.SwapFile + " (" + mib(now.size) + ")"}, {Op: "-", Text: fstab + ": " + strings.TrimSpace(fstabLine)}}
	}
	sum := sha256.Sum256(fmt.Appendf(nil, "%d\n%t\n%d", now.size, now.active, want))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:])}, nil
}

func (h swapHandler[In]) Apply(ctx context.Context, env core.Env, in In) (SwapChanged, error) {
	now, want, err := h.plan(ctx, env, in)
	if err != nil {
		return SwapChanged{}, err
	}
	after := swapDoc(want)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: swapEffect, Local: true, InverseLocal: true, Before: swapDoc(now.size), After: &after,
	}, nil)
	if err != nil {
		return SwapChanged{}, err
	}
	if err := h.m.setSwap(ctx, env, now, want); err != nil {
		return SwapChanged{}, err
	}
	if err := env.Effects.Made(ctx, seq, swapEffect, after); err != nil {
		return SwapChanged{}, err
	}
	return SwapChanged{SizeMiB: want / platform.MiB}, nil
}

func (h swapHandler[In]) Verify(_ context.Context, _ core.Env, in In, _ SwapChanged) error {
	return h.m.verifySwap(h.want(in))
}

// verifySwap checks the swap file is want bytes, on and in fstab, or, for
// 0, gone from all three.
func (m module) verifySwap(want int64) error {
	now, err := m.readSwap()
	if err != nil {
		return err
	}
	switch {
	case now.size != want:
		return fmt.Errorf("tuning: %s is %d bytes, want %d", platform.SwapFile, now.size, want)
	case now.active != (want > 0):
		return fmt.Errorf("tuning: %s is on: %t, want %t", platform.SwapFile, now.active, want > 0)
	}
	data, err := m.d.readFile(fstab)
	if err != nil {
		return fmt.Errorf("tuning: read %s: %w", fstab, err)
	}
	if holds := strings.Contains(string(data), strings.TrimSpace(fstabLine)); holds != (want > 0) {
		return fmt.Errorf("tuning: %s holds the swap line: %t, want %t", fstab, holds, want > 0)
	}
	return nil
}

func (h swapHandler[In]) Observe(context.Context, core.Env, core.Effect) (core.Observation, error) {
	now, err := h.m.readSwap()
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: swapDoc(now.size), Resource: swapEffect, Matches: 1, Exclusive: true}, nil
}

// Inverse puts the swap file back at the size the change found, or
// removes it; the memory guard refusing changes nothing.
func (h swapHandler[In]) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	want, err := docSize(e.Before)
	if err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tuning: read the effect's prior state: %w", err))
	}
	now, err := h.m.readSwap()
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, h.m.setSwap(ctx, env, now, want)
}

func run(ctx context.Context, env core.Env, path string, args []string) error {
	res, err := env.Exec.Run(ctx, core.Cmd{Path: path, Args: args})
	if err != nil {
		return fmt.Errorf("tuning: run %s: %w", path, err)
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("tuning: %s exited %d: %s", path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

// setSwap takes the swap file from now to want bytes: 0 turns it off and
// removes it with its fstab block; otherwise it is turned off if on,
// recreated empty with mode 0600, allocated, formatted and turned on. The
// memory guard is checked again before swapoff and refuses with nothing
// changed.
func (m module) setSwap(ctx context.Context, env core.Env, now swapNow, want int64) error {
	if now.active {
		if refusal := platform.SwapoffRefusal(now.used, now.mem); refusal != "" {
			return core.Definite(errcode.New(errcode.InvalidInput, refusal))
		}
		if err := run(ctx, env, platform.SwapoffPath, platform.SwapFileArgs()); err != nil {
			return err
		}
	}
	if want == 0 {
		if err := env.Files.RemoveBlock(ctx, ID, fstab, fstabBlock); err != nil {
			return err
		}
		return env.Files.Remove(ctx, ID, platform.SwapFile)
	}
	if err := env.Files.WriteFile(ctx, ID, platform.SwapFile, []byte{}, 0o600); err != nil {
		return err
	}
	for _, c := range []struct {
		path string
		args []string
	}{
		{platform.FallocatePath, platform.Fallocate(want)},
		{platform.MkswapPath, platform.SwapFileArgs()},
		{platform.SwaponPath, platform.SwapFileArgs()},
	} {
		if err := run(ctx, env, c.path, c.args); err != nil {
			return err
		}
	}
	return env.Files.ReplaceBlock(ctx, ID, fstab, fstabBlock, []byte(fstabLine))
}

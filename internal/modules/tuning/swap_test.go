package tuning

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/platform"
)

const fstabBase = "UUID=1 / ext4 errors=remount-ro 0 1\n"

// swapRig is the host's swap: /darkpanel.swap, the root filesystem, memory
// and what the ownership store records.
type swapRig struct {
	exists, on  bool
	size, used  int64
	rootFS      string
	free        int64
	available   int64
	partition   bool // a swap partition Darkpanel did not make
	owned       map[string]bool
	failCommand string // a command that exits 1
}

// proc answers the /proc files of the swap half.
func (r *rig) proc(name string) (string, bool) {
	s := r.swap
	switch name {
	case "/proc/swaps":
		out := "Filename\t\t\t\tType\t\tSize\t\tUsed\t\tPriority\n"
		if s.partition {
			out += "/dev/sda2                               partition\t2097148\t\t1024\t\t-2\n"
		}
		if s.on {
			out += fmt.Sprintf("%s                         file\t\t%d\t\t%d\t\t-3\n", platform.SwapFile, s.size>>10-4, s.used>>10)
		}
		return out, true
	case "/proc/meminfo":
		return fmt.Sprintf("MemTotal:        8000000 kB\nMemFree:          100000 kB\nMemAvailable:   %8d kB\n", s.available>>10), true
	case "/proc/self/mounts":
		return "rootfs / rootfs rw 0 0\nsysfs /sys sysfs rw 0 0\n/dev/sda1 / " + s.rootFS + " rw,relatime 0 0\nproc /proc proc rw 0 0\n", true
	}
	return "", false
}

// runner answers the swap commands, refusing any vector the agent's exec
// allowlist would refuse.
type runner struct{ r *rig }

func (x runner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	k := strings.Join(append([]string{cmd.Path}, cmd.Args...), " ")
	allowed := false
	for _, rule := range platform.ProductionRules() {
		if rule.Path == cmd.Path && rule.Match(cmd.Args) == nil {
			allowed = true
		}
	}
	if !allowed {
		x.r.t.Errorf("%q is not in the exec allowlist", k)
		return core.CmdResult{}, fmt.Errorf("refused %q", k)
	}
	x.r.events = append(x.r.events, "run "+k)
	if k == x.r.swap.failCommand {
		return core.CmdResult{ExitCode: 1, Stderr: []byte("failed\n")}, nil
	}
	s := &x.r.swap
	switch cmd.Path {
	case platform.FallocatePath:
		_, _ = fmt.Sscan(cmd.Args[1], &s.size)
	case platform.SwaponPath:
		s.on, s.used = true, 0
	case platform.SwapoffPath:
		s.on = false
	}
	return core.CmdResult{}, nil
}

func (runner) Stream(context.Context, string, []string, func([]byte) error) error {
	return fmt.Errorf("no streams")
}

func (f files) ReplaceBlock(_ context.Context, module, path, blockID string, content []byte) error {
	if module != ID || path != fstab || blockID != fstabBlock {
		return fmt.Errorf("block %s %s %s: not the module's block", module, path, blockID)
	}
	f.r.events = append(f.r.events, "block "+path+" "+strings.TrimSpace(string(content)))
	f.r.files[fstab] = fstabBase + string(content)
	return nil
}

func (f files) RemoveBlock(_ context.Context, module, path, blockID string) error {
	if module != ID || path != fstab || blockID != fstabBlock {
		return fmt.Errorf("unblock %s %s %s: not the module's block", module, path, blockID)
	}
	f.r.events = append(f.r.events, "unblock "+path)
	f.r.files[fstab] = fstabBase
	return nil
}

func (f files) Owns(_ context.Context, module, path string) (bool, error) {
	return module == ID && f.r.swap.owned[path], nil
}

// haveSwap gives the rig a Darkpanel swap file of size MiB, on, with used
// MiB swapped out.
func (r *rig) haveSwap(size, used int64) {
	r.swap.exists, r.swap.on, r.swap.size, r.swap.used = true, true, size*platform.MiB, used*platform.MiB
	r.swap.owned[platform.SwapFile] = true
	r.files[fstab] = fstabBase + fstabLine
}

// commands are the programs the module ran.
func commands(events []string) []string {
	var out []string
	for _, e := range events {
		if c, ok := strings.CutPrefix(e, "run "); ok {
			out = append(out, c)
		}
	}
	return out
}

var (
	swapoffCmd = platform.SwapoffPath + " " + platform.SwapFile
	mkswapCmd  = platform.MkswapPath + " " + platform.SwapFile
	swaponCmd  = platform.SwaponPath + " " + platform.SwapFile
)

func fallocateCmd(size int64) string {
	return fmt.Sprintf("%s -l %d %s", platform.FallocatePath, size, platform.SwapFile)
}

func planErr(t *testing.T, eng interface {
	Plan(context.Context, core.Principal, core.ActionRef, json.RawMessage, core.PlanOptions) (core.PlanView, error)
}, action, input string) error {
	t.Helper()
	_, err := eng.Plan(context.Background(), adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	return err
}

// undo undoes a change and waits until the undo has settled.
func undo(t *testing.T, eng *lifecycle.Engine, changeID string) core.ChangeView {
	t.Helper()
	ctx := context.Background()
	if _, err := eng.Undo(ctx, adminP, changeID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := eng.Change(ctx, changeID)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != core.StateUndoing || time.Now().After(deadline) {
			return v
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCreate(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	eng := engine(t, r)
	pv := plan(t, eng, "tuning.swap.create", `{"sizeMiB":1024}`)
	if got, want := diffText(pv.Diff), []string{"+ Create /darkpanel.swap of 1024 MiB and turn it on", "+ /etc/fstab: /darkpanel.swap none swap sw 0 0"}; !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("create: %+v, %v", cv, err)
	}
	if want := []string{
		"write /darkpanel.swap 0600", "run " + fallocateCmd(1073741824), "run " + mkswapCmd, "run " + swaponCmd,
		"block /etc/fstab /darkpanel.swap none swap sw 0 0",
	}; !slices.Equal(r.events, want) {
		t.Errorf("create did %q, want %q", r.events, want)
	}
	st, err := swapGetter{m: r.module()}.Read(ctx, r.env(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Swaps) != 1 || st.Swaps[0].Path != platform.SwapFile || !st.Swaps[0].Darkpanel || st.Filesystem != "ext4" || st.Unavailable != "" ||
		st.MemoryAvailable != 4<<30 || st.Free != 20<<30 {
		t.Errorf("swap.get %+v", st)
	}

	// An undo the memory guard refuses changes nothing.
	r.swap.used, r.swap.available = 900*platform.MiB, 1<<30
	r.events = nil
	if v := undo(t, eng, cv.ID); v.State == core.StateReverted || slices.Contains(commands(r.events), swapoffCmd) || !r.swap.on || !r.swap.exists {
		t.Errorf("an undo with 900 MiB swapped out of 1 GiB available: %+v, ran %q", v, commands(r.events))
	}
	r.swap.used, r.swap.available = 0, 4<<30

	// The undo turns the file off and removes it with its fstab line.
	r.events = nil
	if cv = undo(t, eng, cv.ID); cv.State != core.StateReverted {
		t.Fatalf("after the undo: %+v", cv)
	}
	if want := []string{"run " + swapoffCmd, "unblock /etc/fstab", "remove /darkpanel.swap"}; !slices.Equal(r.events, want) {
		t.Errorf("the undo did %q, want %q", r.events, want)
	}

	// Sizes outside 256 MiB to 64 GiB, or above the free space less 1 GiB.
	r.swap.free = 100 << 30
	for _, in := range []string{`{"sizeMiB":255}`, `{"sizeMiB":65537}`, `{"sizeMiB":0}`} {
		if err := planErr(t, eng, "tuning.swap.create", in); errcode.From(err).Code != errcode.InvalidInput {
			t.Errorf("create %s: %v", in, err)
		}
	}
	for _, in := range []string{`{"sizeMiB":256}`, `{"sizeMiB":65536}`} {
		if err := planErr(t, eng, "tuning.swap.create", in); err != nil {
			t.Errorf("create %s: %v", in, err)
		}
	}
	r.swap.free = 2 << 30
	if err := planErr(t, eng, "tuning.swap.create", `{"sizeMiB":1025}`); errcode.From(err).Code != errcode.InvalidInput ||
		!strings.Contains(err.Error(), "At most 1024 MiB") {
		t.Errorf("create above the free space less 1 GiB: %v", err)
	}
	if err := planErr(t, eng, "tuning.swap.create", `{"sizeMiB":1024}`); err != nil {
		t.Errorf("create of the free space less 1 GiB: %v", err)
	}
	// A swap file that is there already.
	r.swap.exists, r.swap.size = true, 512*platform.MiB
	if err := planErr(t, eng, "tuning.swap.create", `{"sizeMiB":512}`); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("create over an existing file: %v", err)
	}
}

func TestFilesystem(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.swap.rootFS = "btrfs"
	r.swap.partition = true
	for _, a := range r.module().Manifest().Actions {
		if !strings.HasPrefix(a.Spec.Name, "tuning.swap.") || a.Spec.Class == core.ClassRead {
			continue
		}
		if err := a.Spec.Available(ctx, r.env()); err == nil || !strings.Contains(err.Error(), "btrfs") {
			t.Errorf("%s on btrfs: %v", a.Spec.Name, err)
		}
	}
	st, err := swapGetter{m: r.module()}.Read(ctx, r.env(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Filesystem != "btrfs" || !strings.Contains(st.Unavailable, "btrfs") || len(st.Swaps) != 1 || st.Swaps[0].Darkpanel ||
		st.Swaps[0].Type != "partition" || st.Swaps[0].Size != 2097148<<10 {
		t.Errorf("swap.get on btrfs %+v", st)
	}
	for _, fsType := range []string{"ext4", "xfs"} {
		r.swap.rootFS = fsType
		if err := (r.module()).swapAvailable(ctx, r.env()); err != nil {
			t.Errorf("%s: %v", fsType, err)
		}
	}
}

func TestRemoveGuard(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.haveSwap(1024, 900)
	r.swap.available = 1 << 30
	eng := engine(t, r)
	for action, in := range map[string]string{"tuning.swap.remove": `{}`, "tuning.swap.resize": `{"sizeMiB":2048}`} {
		err := planErr(t, eng, action, in)
		if errcode.From(err).Code != errcode.InvalidInput || !strings.Contains(err.Error(), "holds 900 MiB of swapped memory, more than the 1024 MiB available less 256 MiB") {
			t.Errorf("%s with 900 MiB used and 1 GiB available: %v", action, err)
		}
	}
	if slices.Contains(commands(r.events), swapoffCmd) {
		t.Error("a refused plan ran swapoff")
	}

	r.swap.used = 500 * platform.MiB
	pv := plan(t, eng, "tuning.swap.remove", `{}`)
	if got, want := diffText(pv.Diff), []string{"- Turn off and delete /darkpanel.swap (1024 MiB)", "- /etc/fstab: /darkpanel.swap none swap sw 0 0"}; !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("remove: %+v, %v", cv, err)
	}
	if want := []string{"run " + swapoffCmd, "unblock /etc/fstab", "remove /darkpanel.swap"}; !slices.Equal(r.events, want) {
		t.Errorf("remove did %q, want %q", r.events, want)
	}
	if r.swap.exists || r.swap.on || r.files[fstab] != fstabBase {
		t.Errorf("after the remove: %+v, fstab %q", r.swap, r.files[fstab])
	}

	// Swap Darkpanel did not create is left alone.
	r2 := newRig(t)
	r2.haveSwap(1024, 0)
	delete(r2.swap.owned, platform.SwapFile)
	if err := planErr(t, engine(t, r2), "tuning.swap.remove", `{}`); errcode.From(err).Code != errcode.InvalidInput ||
		!strings.Contains(err.Error(), "Darkpanel did not create") {
		t.Errorf("remove of a file Darkpanel did not create: %v", err)
	}
}

func TestResize(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.haveSwap(1024, 100)
	eng := engine(t, r)
	pv := plan(t, eng, "tuning.swap.resize", `{"sizeMiB":2048}`)
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("resize: %+v, %v", cv, err)
	}
	if got, want := commands(r.events), []string{swapoffCmd, fallocateCmd(2147483648), mkswapCmd, swaponCmd}; !slices.Equal(got, want) {
		t.Errorf("resize ran %q, want %q", got, want)
	}
	if i := slices.Index(r.events, "write /darkpanel.swap 0600"); i < 1 || r.events[i-1] != "run "+swapoffCmd {
		t.Errorf("resize did %q: the file is not rewritten empty after swapoff", r.events)
	}
	if r.swap.size != 2048*platform.MiB || !r.swap.on {
		t.Errorf("after the resize: %+v", r.swap)
	}

	// The undo puts back 1024 MiB the same way.
	r.events = nil
	if cv = undo(t, eng, cv.ID); cv.State != core.StateReverted {
		t.Fatalf("after the undo: %+v", cv)
	}
	if got, want := commands(r.events), []string{swapoffCmd, fallocateCmd(1073741824), mkswapCmd, swaponCmd}; !slices.Equal(got, want) {
		t.Errorf("the undo ran %q, want %q", got, want)
	}
	if r.swap.size != 1024*platform.MiB || !r.swap.on || r.files[fstab] != fstabBase+fstabLine {
		t.Errorf("after the undo: %+v, fstab %q", r.swap, r.files[fstab])
	}

	// The same size, a growth past the free space, and a failed mkswap.
	if err := planErr(t, eng, "tuning.swap.resize", `{"sizeMiB":1024}`); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("resize to the same size: %v", err)
	}
	r.swap.free = 1 << 30
	if err := planErr(t, eng, "tuning.swap.resize", `{"sizeMiB":2048}`); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("resize past the free space: %v", err)
	}
	if err := planErr(t, eng, "tuning.swap.resize", `{"sizeMiB":1024}`); errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("resize within the file's own space: %v", err)
	}
	if err := planErr(t, eng, "tuning.swap.resize", `{"sizeMiB":512}`); err != nil {
		t.Errorf("shrink: %v", err)
	}
	r.swap.free = 20 << 30
	r.swap.failCommand = mkswapCmd
	pv = plan(t, eng, "tuning.swap.resize", `{"sizeMiB":2048}`)
	cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err == nil && cv.State == core.StateCompleted {
		t.Errorf("a resize whose mkswap failed completed: %+v", cv)
	}
	h := swapHandler[SwapSizeInput]{m: r.module(), verb: "resize"}
	if err := h.Verify(ctx, r.env(), SwapSizeInput{SizeMiB: 2048}, SwapChanged{}); err == nil {
		t.Error("verify passed a swap file that is off")
	}
}

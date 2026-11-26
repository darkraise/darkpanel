package tuning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/platform"
)

var adminP = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin, Source: "192.0.2.1"}

// kernelDefaults are /proc/sys as a recent kernel boots, by key path.
var kernelDefaults = map[string]string{
	"vm/swappiness": "60", "vm/vfs_cache_pressure": "100", "vm/dirty_ratio": "20", "vm/dirty_background_ratio": "10",
	"vm/overcommit_memory": "0", "vm/max_map_count": "65530", "fs/file-max": "9223372036854775807",
	"fs/inotify/max_user_watches": "65536", "fs/inotify/max_user_instances": "128", "net/core/somaxconn": "4096",
	"net/core/netdev_max_backlog": "1000", "net/core/default_qdisc": "fq_codel", "net/ipv4/tcp_congestion_control": "cubic",
	"net/ipv4/tcp_fastopen": "1", "net/ipv4/tcp_tw_reuse": "2", "net/ipv4/ip_local_port_range": "32768\t60999",
}

// rig is one host: its files, /proc/sys, and what the module did to them.
type rig struct {
	t      *testing.T
	events []string
	files  map[string]string
	live   map[string]string // /proc/sys by key path, as the kernel prints it
	refuse map[string]bool   // keys the kernel refuses a write to
	clamp  map[string]int    // keys the kernel caps at a limit, setting the limit for a value above it
	others []core.Module
	swap   swapRig
}

func newRig(t *testing.T) *rig {
	r := &rig{t: t, files: map[string]string{availableCongestion: "reno cubic\n"}, live: map[string]string{},
		refuse: map[string]bool{}, clamp: map[string]int{}, others: []core.Module{firewall.Module()},
		swap: swapRig{rootFS: "ext4", free: 20 << 30, available: 4 << 30, owned: map[string]bool{}}}
	r.files[fstab] = fstabBase
	for k, v := range kernelDefaults {
		r.live[k] = v
	}
	return r
}

func (r *rig) module() module {
	return module{
		d: deps{
			readFile: func(name string) ([]byte, error) {
				if b, ok := r.proc(name); ok {
					return []byte(b), nil
				}
				b, ok := r.files[name]
				if !ok {
					return nil, fs.ErrNotExist
				}
				return []byte(b), nil
			},
			glob: func(pattern string) ([]string, error) {
				var out []string
				for name := range r.files {
					if ok, err := path.Match(pattern, name); err != nil {
						return nil, err
					} else if ok {
						out = append(out, name)
					}
				}
				slices.Sort(out)
				return out, nil
			},
			fileSize: func(name string) (int64, error) {
				if name != platform.SwapFile || !r.swap.exists {
					return 0, fs.ErrNotExist
				}
				return r.swap.size, nil
			},
			freeSpace: func(path string) (int64, error) {
				if path != "/" {
					return 0, fmt.Errorf("free space of %s", path)
				}
				return r.swap.free, nil
			},
		},
		owners: func() []ownedKey { return declaredKeys(r.others) },
	}
}

func (r *rig) env() core.Env {
	return core.Env{HostID: "h1", Principal: adminP, Files: files{r: r}, Exec: runner{r}, Log: slog.New(slog.DiscardHandler)}
}

// files is the rig's FileWriter: the module's file and the sysctl keys it
// declares, each write checked against the declared value pattern as the
// platform checks it.
type files struct {
	core.FileWriter
	r *rig
}

func (f files) pattern(module, key string) (*regexp.Regexp, error) {
	for _, op := range f.r.module().Manifest().OwnedPaths {
		if module == ID && op.Kind == core.OwnedSysctl && op.Path == key {
			return regexp.Compile(op.Pattern)
		}
	}
	return nil, fmt.Errorf("module %s does not declare sysctl %s", module, key)
}

func (f files) ReadSysctl(_ context.Context, module, key string) (string, error) {
	if _, err := f.pattern(module, key); err != nil {
		return "", err
	}
	v, ok := f.r.live[key]
	if !ok {
		return "", fmt.Errorf("open sysctl %s: %w", key, fs.ErrNotExist)
	}
	return v, nil
}

func (f files) WriteSysctl(_ context.Context, module, key, value string) error {
	re, err := f.pattern(module, key)
	if err != nil {
		return err
	}
	if !re.MatchString(value) {
		return fmt.Errorf("sysctl %s: value %q does not match %s", key, value, re)
	}
	if f.r.refuse[key] {
		return fmt.Errorf("write sysctl %s: %w", key, errors.New("invalid argument"))
	}
	if limit, ok := f.r.clamp[key]; ok && atoi(value) > limit {
		c := strconv.Itoa(limit)
		f.r.events = append(f.r.events, "sysctl "+key+"="+c)
		f.r.live[key] = c
		return fmt.Errorf("sysctl %s reads back %q after writing %q", key, c, value)
	}
	f.r.events = append(f.r.events, "sysctl "+key+"="+value)
	f.r.live[key] = value
	return nil
}

func (f files) WriteFile(_ context.Context, module, name string, data []byte, mode fs.FileMode) error {
	if module == ID && name == platform.SwapFile && mode == 0o600 && len(data) == 0 {
		f.r.events = append(f.r.events, "write "+name+" 0600")
		f.r.swap.exists, f.r.swap.size, f.r.swap.owned[name] = true, 0, true
		return nil
	}
	if module != ID || name != sysctlFile || mode != 0o644 {
		return fmt.Errorf("write %s %s %v: not the module's file", module, name, mode)
	}
	f.r.events = append(f.r.events, "write "+name)
	f.r.files[name] = string(data)
	return nil
}

func (f files) Remove(_ context.Context, module, name string) error {
	if module == ID && name == platform.SwapFile {
		f.r.events = append(f.r.events, "remove "+name)
		f.r.swap.exists = false
		return nil
	}
	if module != ID || name != sysctlFile {
		return fmt.Errorf("remove %s %s: not the module's file", module, name)
	}
	f.r.events = append(f.r.events, "remove "+name)
	delete(f.r.files, name)
	return nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// engine is the agent's lifecycle engine over the rig.
func engine(t *testing.T, r *rig) *lifecycle.Engine {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now(),
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(r.module())
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{}, Redact: schema.Redact,
		Base: core.Env{HostID: "h1", DB: db, Files: files{r: r}, Exec: runner{r}, Sealer: coretest.Sealer{},
			Clock: coretest.NewFakeClock(time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)), Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

func plan(t *testing.T, eng *lifecycle.Engine, action, input string) core.PlanView {
	t.Helper()
	pv, err := eng.Plan(context.Background(), adminP, core.ActionRef{Name: action, Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		t.Fatalf("plan %s %s: %v", action, input, err)
	}
	return pv
}

func diffText(d []core.DiffLine) []string {
	out := make([]string, len(d))
	for i, l := range d {
		out[i] = l.Op + " " + l.Text
	}
	return out
}

func golden(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/99-zz-darkpanel.conf")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestApply(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.live["net/core/somaxconn"] = "1024"
	eng := engine(t, r)

	pv := plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"10","net.core.somaxconn":"4096"}}`)
	if got, want := diffText(pv.Diff), []string{
		"  Set vm.swappiness from 60 to 10 now", "  Set net.core.somaxconn from 1024 to 4096 now",
		"+ vm.swappiness = 10", "+ net.core.somaxconn = 4096",
	}; !slices.Equal(got, want) {
		t.Errorf("diff %q, want %q", got, want)
	}
	r.events = nil
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	if r.files[sysctlFile] != golden(t) {
		t.Errorf("file\n%s\nwant\n%s", r.files[sysctlFile], golden(t))
	}
	if want := []string{"sysctl vm/swappiness=10", "sysctl net/core/somaxconn=4096", "write " + sysctlFile}; !slices.Equal(r.events, want) {
		t.Errorf("apply did %q, want %q", r.events, want)
	}
	h := sysctlHandler{m: r.module()}
	if err := h.Verify(ctx, r.env(), SysctlInput{Values: map[string]string{"vm.swappiness": "10", "net.core.somaxconn": "4096"}}, SysctlApplied{}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := h.Verify(ctx, r.env(), SysctlInput{Values: map[string]string{"vm.swappiness": "11"}}, SysctlApplied{}); err == nil {
		t.Error("verify passed a value the kernel does not hold")
	}
	r.live["vm/swappiness"] = "12"
	if err := h.Verify(ctx, r.env(), SysctlInput{Values: map[string]string{"vm.swappiness": "10", "net.core.somaxconn": "4096"}}, SysctlApplied{}); err == nil {
		t.Error("verify passed with the file right and a live value changed")
	}
	r.live["vm/swappiness"] = "10"

	// Unconfirmed, the change puts back the live values and removes the file.
	r.events = nil
	if cv, err = eng.Revert(ctx, adminP, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert: %+v, %v", cv, err)
	}
	if _, ok := r.files[sysctlFile]; ok || r.live["vm/swappiness"] != "60" || r.live["net/core/somaxconn"] != "1024" {
		t.Errorf("after the revert: file present %v, swappiness %s, somaxconn %s", ok, r.live["vm/swappiness"], r.live["net/core/somaxconn"])
	}
	if want := []string{"remove " + sysctlFile, "sysctl net/core/somaxconn=1024", "sysctl vm/swappiness=60"}; !slices.Equal(r.events, want) {
		t.Errorf("revert did %q, want %q", r.events, want)
	}

	// Over a file Darkpanel wrote before, the revert puts that file back.
	pv = plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"30","net.ipv4.ip_local_port_range":"30000  40000"}}`)
	if cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{ConfirmNow: true}); err != nil || cv.State != core.StateConfirmed {
		t.Fatalf("apply confirmed now: %+v, %v", cv, err)
	}
	prior := r.files[sysctlFile]
	if !strings.Contains(prior, "net.ipv4.ip_local_port_range = 30000 40000\n") || r.live["net/ipv4/ip_local_port_range"] != "30000\t40000" {
		t.Errorf("port range: file\n%s\nlive %q", prior, r.live["net/ipv4/ip_local_port_range"])
	}
	pv = plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"10","net.core.somaxconn":"4096"}}`)
	if got, want := diffText(pv.Diff), []string{
		"  Set vm.swappiness from 30 to 10 now", "  Set net.core.somaxconn from 1024 to 4096 now",
		"- vm.swappiness = 30", "- net.ipv4.ip_local_port_range = 30000 40000", "+ vm.swappiness = 10", "+ net.core.somaxconn = 4096",
	}; !slices.Equal(got, want) {
		t.Errorf("diff over a file %q, want %q", got, want)
	}
	if cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StatePending {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	if r.live["net/ipv4/ip_local_port_range"] != "30000\t40000" {
		t.Error("a key left out of the file changed its live value")
	}
	if cv, err = eng.Revert(ctx, adminP, cv.ID); err != nil || cv.State != core.StateReverted {
		t.Fatalf("revert: %+v, %v", cv, err)
	}
	if r.files[sysctlFile] != prior || r.live["vm/swappiness"] != "30" || r.live["net/core/somaxconn"] != "1024" {
		t.Errorf("after the revert: file\n%s\nswappiness %s, somaxconn %s", r.files[sysctlFile], r.live["vm/swappiness"], r.live["net/core/somaxconn"])
	}
	_, err = eng.Plan(ctx, adminP, core.ActionRef{Name: "tuning.sysctl.apply", Version: 1},
		json.RawMessage(`{"values":{"vm.swappiness":"30","net.ipv4.ip_local_port_range":"30000 40000"}}`), core.PlanOptions{})
	if errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("a plan of the values held already: %v", err)
	}

	// A value the kernel refuses ends the change: what was set before it
	// is put back, and the file is not written.
	r.refuse["net/core/somaxconn"] = true
	pv = plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"5","net.core.somaxconn":"8192"}}`)
	r.events = nil
	cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err == nil && cv.State != core.StateReverted {
		t.Fatalf("apply of a refused value: %+v", cv)
	}
	if want := []string{"sysctl vm/swappiness=5", "sysctl vm/swappiness=30"}; !slices.Equal(r.events, want) {
		t.Errorf("apply of a refused value did %q, want %q", r.events, want)
	}
	if r.files[sysctlFile] != prior {
		t.Errorf("the file changed after a refused value:\n%s", r.files[sysctlFile])
	}

	// A value the kernel takes as another is put back too.
	delete(r.refuse, "net/core/somaxconn")
	r.clamp["net/core/somaxconn"] = 4000
	pv = plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"5","net.core.somaxconn":"8192"}}`)
	r.events = nil
	cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err == nil && cv.State != core.StateReverted {
		t.Fatalf("apply of a clamped value: %+v", cv)
	}
	if want := []string{"sysctl vm/swappiness=5", "sysctl net/core/somaxconn=4000", "sysctl net/core/somaxconn=1024", "sysctl vm/swappiness=30"}; !slices.Equal(r.events, want) {
		t.Errorf("apply of a clamped value did %q, want %q", r.events, want)
	}
}

func TestRanges(t *testing.T) {
	ctx := context.Background()
	// The keys and ranges of spec section 7, written out here so that a
	// range changed in keys.go fails.
	bounds := []struct{ key, lo, hi string }{
		{"vm.swappiness", "0", "200"}, {"vm.vfs_cache_pressure", "1", "1000"}, {"vm.dirty_ratio", "1", "100"},
		{"vm.dirty_background_ratio", "1", "100"}, {"vm.overcommit_memory", "0", "2"},
		{"vm.max_map_count", "65530", "2147483647"}, {"fs.file-max", "8192", "9223372036854775807"},
		{"fs.inotify.max_user_watches", "8192", "4194304"}, {"fs.inotify.max_user_instances", "128", "65536"},
		{"net.core.somaxconn", "128", "65535"}, {"net.core.netdev_max_backlog", "1000", "262144"},
		{"net.ipv4.tcp_fastopen", "0", "3"}, {"net.ipv4.tcp_tw_reuse", "0", "2"},
	}
	past := map[string][2]string{ // one below the lowest, one above the highest
		"0": {"-1", ""}, "1": {"0", ""}, "128": {"127", ""}, "1000": {"999", ""}, "8192": {"8191", ""}, "65530": {"65529", ""},
	}
	above := map[string]string{
		"200": "201", "1000": "1001", "100": "101", "2": "3", "2147483647": "2147483648", "9223372036854775807": "9223372036854775808",
		"4194304": "4194305", "65536": "65537", "65535": "65536", "262144": "262145", "3": "4",
	}
	type row struct {
		key, value string
		ok         bool
		says       string
	}
	var rows []row
	for _, b := range bounds {
		rows = append(rows, row{b.key, b.lo, true, ""}, row{b.key, b.hi, true, ""},
			row{b.key, past[b.lo][0], false, "Use " + b.lo + " to " + b.hi}, row{b.key, above[b.hi], false, "Use " + b.lo + " to " + b.hi})
	}
	for _, q := range []string{"fq", "fq_codel", "cake", "pfifo_fast"} {
		rows = append(rows, row{"net.core.default_qdisc", q, true, ""})
	}
	rows = append(rows,
		row{"net.core.default_qdisc", "pfifo", false, "Use one of fq, fq_codel, cake, pfifo_fast"},
		row{"net.ipv4.tcp_congestion_control", "reno", true, ""},
		row{"net.ipv4.tcp_congestion_control", "bbr", false, "Use one of reno, cubic"},
		row{"net.ipv4.ip_local_port_range", "1 65535", true, ""},
		row{"net.ipv4.ip_local_port_range", "0 65535", false, "Give the lowest and highest port, 1 to 65535"},
		row{"net.ipv4.ip_local_port_range", "1 65536", false, "Give the lowest and highest port, 1 to 65535"},
		row{"net.ipv4.ip_local_port_range", "40000 30000", false, "The lowest port 40000 must be below the highest 30000"},
		row{"net.ipv4.ip_local_port_range", "30000", false, "Give the lowest and highest port"},
		row{"net.ipv4.ip_local_port_range", "30000 30000", false, "The lowest port 30000 must be below the highest 30000"},
		row{"vm.swappiness", "1e2", false, "Use 0 to 200"},
		row{"vm.panic_on_oom", "1", false, "vm.panic_on_oom is not a kernel parameter Darkpanel tunes"},
		row{"net.ipv4.ip_forward", "1", false, "net.ipv4.ip_forward is managed by the firewall module"},
		row{"net.ipv6.conf.eth0.forwarding", "1", false, "net.ipv6.conf.eth0.forwarding is managed by the firewall module"},
	)
	keys := map[string]bool{}
	for _, row := range rows {
		keys[row.key] = true
		r := newRig(t)
		r.live["vm/swappiness"] = "7"
		_, err := sysctlHandler{m: r.module()}.Plan(ctx, r.env(), SysctlInput{Values: map[string]string{row.key: row.value}})
		if row.ok {
			if err != nil {
				t.Errorf("%s = %q refused: %v", row.key, row.value, err)
			}
			continue
		}
		e := errcode.From(err)
		if err == nil || e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/values/"+row.key || !strings.Contains(e.Fields[0].Message, row.says) {
			t.Errorf("%s = %q: %+v, want invalid input at /values/%s saying %q", row.key, row.value, e, row.key, row.says)
		}
	}
	for _, k := range curated {
		if !keys[k.name] {
			t.Errorf("key %s is not in the table", k.name)
		}
	}
	if len(curated) != 16 {
		t.Errorf("%d curated keys, want the 16 of spec section 7", len(curated))
	}
}

func TestLaterFiles(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	r.files[sysctlFile] = fileHeader + "vm.swappiness = 10\n"
	r.files["/etc/sysctl.d/10-early.conf"] = "vm.swappiness = 1\nnet.core.somaxconn = 512\n"
	r.files["/etc/sysctl.d/99-sysctl.conf"] = "vm.swappiness=2\n"
	r.files["/etc/sysctl.d/99-zzz-local.conf"] = "# local\n-vm.swappiness = 5\n"
	r.files["/usr/lib/sysctl.d/99-zzz-local.conf"] = "net.core.somaxconn = 9\n" // hidden by /etc's file of that name
	r.files["/usr/lib/sysctl.d/zz-vendor.conf"] = "vm.dirty_*ratio = 15\n"
	r.files[ufwSysctlFile] = "net/ipv4/ip_forward=1\nvm/swappiness=0\n"

	st, err := sysctlGetter{m: r.module()}.Read(ctx, r.env(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]SysctlKeyState{}
	for _, k := range st.Keys {
		by[k.Key] = k
	}
	if got, want := by["vm.swappiness"].LaterFiles, []string{"/etc/sysctl.d/99-zzz-local.conf", ufwSysctlFile}; !slices.Equal(got, want) {
		t.Errorf("vm.swappiness later files %q, want %q", got, want)
	}
	if got := by["net.core.somaxconn"].LaterFiles; got != nil {
		t.Errorf("net.core.somaxconn later files %q, want none", got)
	}
	if got, want := by["vm.dirty_ratio"].LaterFiles, []string{"/usr/lib/sysctl.d/zz-vendor.conf"}; !slices.Equal(got, want) {
		t.Errorf("vm.dirty_ratio later files %q, want %q", got, want)
	}
	if s := by["vm.swappiness"]; s.Live != "60" || s.Set != "10" || s.Min != "0" || s.Max != "200" || s.Kind != kindInteger || s.Owner != "" || s.Help == "" {
		t.Errorf("vm.swappiness %+v", s)
	}
	if s := by["net.ipv4.tcp_congestion_control"]; !slices.Equal(s.Choices, []string{"reno", "cubic"}) || s.Live != "cubic" {
		t.Errorf("tcp_congestion_control %+v", s)
	}
	if s := by["net.ipv4.ip_local_port_range"]; s.Live != "32768 60999" || s.Kind != kindPortRange {
		t.Errorf("ip_local_port_range %+v", s)
	}
	for _, key := range []string{"net.ipv4.ip_forward", "net.ipv6.conf.<iface>.forwarding", "net.ipv4.conf.<iface>.rp_filter"} {
		if s, ok := by[key]; !ok || s.Owner != "firewall" || s.Kind != "" || s.Live != "" {
			t.Errorf("firewall key %s: %+v, want it locked with owner firewall", key, s)
		}
	}
	if len(st.Keys) != len(curated)+len(declaredKeys(r.others)) || st.File != sysctlFile {
		t.Errorf("%d keys in %s", len(st.Keys), st.File)
	}

	// A key the kernel lacks is reported, and refused.
	delete(r.live, "net/ipv4/tcp_fastopen")
	st, err = sysctlGetter{m: r.module()}.Read(ctx, r.env(), struct{}{})
	if err != nil || !slices.ContainsFunc(st.Keys, func(k SysctlKeyState) bool {
		return k.Key == "net.ipv4.tcp_fastopen" && k.Unavailable == "This kernel has no net.ipv4.tcp_fastopen."
	}) {
		t.Errorf("a missing key: %+v, %v", st.Keys, err)
	}
	_, err = sysctlHandler{m: r.module()}.Plan(ctx, r.env(), SysctlInput{Values: map[string]string{"net.ipv4.tcp_fastopen": "3"}})
	if errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("a plan of a missing key: %v", err)
	}
}

func TestRepair(t *testing.T) {
	ctx := context.Background()
	r := newRig(t)
	eng := engine(t, r)
	pv := plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"10","net.core.somaxconn":"4096"}}`)
	confirmed, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{ConfirmNow: true})
	if err != nil || confirmed.State != core.StateConfirmed {
		t.Fatalf("apply: %+v, %v", confirmed, err)
	}
	want := r.files[sysctlFile]
	if want != golden(t) {
		t.Errorf("file\n%s\nwant\n%s", want, golden(t))
	}

	// A later change whose revert finds a live value changed by hand
	// leaves that effect unresolved, and the change needs repair.
	pv = plan(t, eng, "tuning.sysctl.apply", `{"values":{"vm.swappiness":"30","vm.dirty_ratio":"40"}}`)
	cv, err := eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StatePending {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	r.live["vm/swappiness"] = "50"
	if cv, err = eng.Revert(ctx, adminP, cv.ID); err != nil || cv.State != core.StateNeedsRepair {
		t.Fatalf("revert over a hand edit: %+v, %v", cv, err)
	}
	if r.live["vm/swappiness"] != "50" || r.live["vm/dirty_ratio"] != "20" || r.files[sysctlFile] != want {
		t.Errorf("the revert: swappiness %s, dirty_ratio %s, file\n%s", r.live["vm/swappiness"], r.live["vm/dirty_ratio"], r.files[sysctlFile])
	}
	effects, err := eng.Effects(ctx, cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range effects {
		if e.State == core.EffectUnresolved {
			if _, err := eng.Resolve(ctx, adminP, cv.ID, e.Seq, core.ResolveRequest{Resolution: "accept"}); err != nil {
				t.Fatalf("accept effect %d: %v", e.Seq, err)
			}
		}
	}
	res, err := eng.Repair(ctx, adminP, cv.ID, core.RepairRequest{})
	if err != nil || res.Plan == nil || res.Plan.Action != "tuning.sysctl.repair" {
		t.Fatalf("repair: %+v, %v", res, err)
	}
	if got, want := diffText(res.Plan.Diff), []string{"  Rewrite from change " + confirmed.ID, "  Set vm.swappiness from 50 to 10 now"}; !slices.Equal(got, want) {
		t.Errorf("repair diff %q, want %q", got, want)
	}
	r.events = nil
	if cv, err = eng.Apply(ctx, adminP, res.Plan.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("apply the repair: %+v, %v", cv, err)
	}
	if want := []string{"sysctl vm/swappiness=10"}; !slices.Equal(r.events, want) {
		t.Errorf("the repair did %q, want %q", r.events, want)
	}

	// With the file edited by hand too, the repair rewrites it.
	r.files[sysctlFile] = "vm.swappiness = 99\n"
	r.live["net/core/somaxconn"] = "128"
	pv = plan(t, eng, "tuning.sysctl.repair", `{}`)
	if got, want := diffText(pv.Diff), []string{
		"  Rewrite from change " + confirmed.ID, "  Set net.core.somaxconn from 128 to 4096 now",
		"- vm.swappiness = 99", "+ vm.swappiness = 10", "+ net.core.somaxconn = 4096",
	}; !slices.Equal(got, want) {
		t.Errorf("repair diff %q, want %q", got, want)
	}
	if cv, err = eng.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("repair: %+v, %v", cv, err)
	}
	if r.files[sysctlFile] != want || r.live["vm/swappiness"] != "10" || r.live["net/core/somaxconn"] != "4096" {
		t.Errorf("after the repair: file\n%s\nswappiness %s, somaxconn %s", r.files[sysctlFile], r.live["vm/swappiness"], r.live["net/core/somaxconn"])
	}
	if pv = plan(t, eng, "tuning.sysctl.repair", `{}`); len(pv.Diff) != 0 {
		t.Errorf("a second repair plans %q", diffText(pv.Diff))
	}

	// With no confirmed change, the repair removes the file.
	r2 := newRig(t)
	r2.files[sysctlFile] = fileHeader + "vm.swappiness = 2\n"
	eng2 := engine(t, r2)
	pv = plan(t, eng2, "tuning.sysctl.repair", `{}`)
	if got := diffText(pv.Diff); !slices.Equal(got, []string{"- Remove " + sysctlFile + ": no settings were confirmed"}) {
		t.Errorf("repair diff with nothing confirmed %q", got)
	}
	if cv, err = eng2.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("repair with nothing confirmed: %+v, %v", cv, err)
	}
	if _, ok := r2.files[sysctlFile]; ok || r2.live["vm/swappiness"] != "60" {
		t.Errorf("after the repair with nothing confirmed: file present %v, swappiness %s", ok, r2.live["vm/swappiness"])
	}

	// The repair writes the confirmed values as the apply wrote them.
	r3 := newRig(t)
	eng3 := engine(t, r3)
	pv = plan(t, eng3, "tuning.sysctl.apply", `{"values":{"net.ipv4.ip_local_port_range":" 30000  40000 "}}`)
	if cv, err = eng3.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{ConfirmNow: true}); err != nil || cv.State != core.StateConfirmed {
		t.Fatalf("apply: %+v, %v", cv, err)
	}
	r3.live["net/ipv4/ip_local_port_range"] = "1024\t65535"
	pv = plan(t, eng3, "tuning.sysctl.repair", `{}`)
	if cv, err = eng3.Apply(ctx, adminP, pv.PlanID, core.ApplyOptions{}); err != nil || cv.State != core.StateCompleted {
		t.Fatalf("repair of a port range: %+v, %v", cv, err)
	}
	if r3.live["net/ipv4/ip_local_port_range"] != "30000\t40000" || !strings.HasSuffix(r3.files[sysctlFile], "net.ipv4.ip_local_port_range = 30000 40000\n") {
		t.Errorf("after the repair: live %q, file\n%s", r3.live["net/ipv4/ip_local_port_range"], r3.files[sysctlFile])
	}

	specs := map[string]core.ActionSpec{}
	for _, a := range r.module().Manifest().Actions {
		specs[a.Spec.Name] = a.Spec
	}
	if s := specs["tuning.sysctl.apply"]; s.Class != core.ClassRevertible || s.RepairedBy != "tuning.sysctl.repair" ||
		!slices.Equal(s.LockKeys, []string{"sysctl"}) || s.MinScope != core.ScopeAdmin {
		t.Errorf("sysctl.apply spec %+v", s)
	}
	var owned []string
	for _, op := range r.module().Manifest().OwnedPaths {
		owned = append(owned, op.Kind+" "+op.Path)
	}
	if !slices.Contains(owned, "file "+sysctlFile) || !slices.Contains(owned, "sysctl vm/swappiness") || len(owned) != len(curated)+3 {
		t.Errorf("owned paths %q", owned)
	}
}

// TestModuleWiring: the real module lists the keys of the modules it is
// given.
func TestModuleWiring(t *testing.T) {
	m, ok := Module(firewall.Module()).(module)
	if !ok || !slices.ContainsFunc(m.owners(), func(o ownedKey) bool { return o == ownedKey{"firewall", "net.ipv4.ip_forward"} }) {
		t.Errorf("owners %+v", m.owners())
	}
	if m, _ := Module().(module); len(m.owners()) != 0 {
		t.Errorf("owners without other modules %+v", m.owners())
	}
}

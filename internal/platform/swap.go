package platform

import (
	"fmt"
	"strconv"
	"strings"
)

// SwapFile is the swap file the tuning module creates.
const SwapFile = "/darkpanel.swap"

// The programs that make, turn on and turn off the swap file.
const (
	FallocatePath = "/usr/bin/fallocate"
	MkswapPath    = "/usr/sbin/mkswap"
	SwaponPath    = "/usr/sbin/swapon"
	SwapoffPath   = "/usr/sbin/swapoff"
)

// The sizes the swap file may take, in bytes.
const (
	MiB         = int64(1) << 20
	SwapMinSize = 256 * MiB
	SwapMaxSize = 64 << 30
)

// SwapReserve is the memory that must stay available after swapoff moves
// the file's pages back into memory.
const SwapReserve = 256 * MiB

// Fallocate allocates size bytes to the swap file.
func Fallocate(size int64) []string {
	return []string{"-l", strconv.FormatInt(size, 10), SwapFile}
}

// SwapFileArgs is the vector of mkswap, swapon and swapoff: the swap file.
func SwapFileArgs() []string { return []string{SwapFile} }

// execMatchFallocate allows a whole number of MiB within the swap file's
// sizes, in decimal without a sign or leading zero.
func execMatchFallocate(args []string) error {
	if len(args) != 3 || args[0] != "-l" || args[2] != SwapFile {
		return errExecVector
	}
	n, err := strconv.ParseInt(args[1], 10, 64)
	if err != nil || strconv.FormatInt(n, 10) != args[1] || n < SwapMinSize || n > SwapMaxSize || n%MiB != 0 {
		return errExecVector
	}
	return nil
}

// SwapArea is one line of /proc/swaps, sizes in bytes.
type SwapArea struct {
	Path, Type string
	Size, Used int64
}

// ParseSwaps reads /proc/swaps, which gives sizes in KiB; a path with a
// space is escaped as \040.
func ParseSwaps(data []byte) ([]SwapArea, error) {
	var out []SwapArea
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) == 0 {
			continue
		}
		if len(f) < 4 {
			return nil, fmt.Errorf("platform: /proc/swaps line %q", line)
		}
		size, err1 := strconv.ParseInt(f[2], 10, 64)
		used, err2 := strconv.ParseInt(f[3], 10, 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("platform: /proc/swaps line %q", line)
		}
		out = append(out, SwapArea{Path: strings.ReplaceAll(f[0], `\040`, " "), Type: f[1], Size: size << 10, Used: used << 10})
	}
	return out, nil
}

// Memory is what /proc/meminfo says of memory, in bytes.
type Memory struct{ Total, Available int64 }

// ParseMeminfo reads MemTotal and MemAvailable from /proc/meminfo.
func ParseMeminfo(data []byte) (Memory, error) {
	var m Memory
	found := 0
	for line := range strings.Lines(string(data)) {
		f := strings.Fields(line)
		if len(f) < 2 || (f[0] != "MemTotal:" && f[0] != "MemAvailable:") {
			continue
		}
		kib, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return Memory{}, fmt.Errorf("platform: /proc/meminfo line %q", strings.TrimSpace(line))
		}
		if f[0] == "MemTotal:" {
			m.Total = kib << 10
		} else {
			m.Available = kib << 10
		}
		found++
	}
	if found != 2 {
		return Memory{}, fmt.Errorf("platform: /proc/meminfo lacks MemTotal or MemAvailable")
	}
	return m, nil
}

// SwapoffRefusal is why turning off a swap area holding used bytes is
// refused, "" when available memory less SwapReserve holds them.
func SwapoffRefusal(used int64, mem Memory) string {
	room := mem.Available - SwapReserve
	if used <= room {
		return ""
	}
	return fmt.Sprintf("%s holds %d MiB of swapped memory, more than the %d MiB available less %d MiB; free memory first",
		SwapFile, used/MiB, mem.Available/MiB, SwapReserve/MiB)
}

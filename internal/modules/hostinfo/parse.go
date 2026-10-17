// Package hostinfo is the host-information module (spec section 16): one
// resource, hostinfo.summary, read from /etc/os-release, /proc and statfs.
package hostinfo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type osRelease struct {
	ID, VersionID, PrettyName string
}

// parseOSRelease reads os-release(5). Missing ID and PRETTY_NAME take the
// defaults that os-release(5) specifies, "linux" and "Linux".
func parseOSRelease(data []byte) osRelease {
	var r osRelease
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		switch key {
		case "ID":
			r.ID = unquote(value)
		case "VERSION_ID":
			r.VersionID = unquote(value)
		case "PRETTY_NAME":
			r.PrettyName = unquote(value)
		}
	}
	if r.ID == "" {
		r.ID = "linux"
	}
	if r.PrettyName == "" {
		r.PrettyName = "Linux"
	}
	return r
}

// unquote strips os-release quoting: single quotes literally, double quotes
// with backslash escapes.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	var b strings.Builder
	inner := v[1 : len(v)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

type cpuInfo struct {
	Model        string
	LogicalCount int
}

// parseCPUInfo counts processor entries and names the CPU from the first
// "model name" line, else the first "Hardware" line, else the first
// "CPU implementer" and "CPU part" lines (arm64 cloud images have no model
// name), else "unknown".
func parseCPUInfo(data []byte) cpuInfo {
	var c cpuInfo
	first := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "processor" {
			c.LogicalCount++
		}
		if _, seen := first[key]; !seen && value != "" {
			first[key] = value
		}
	}
	switch {
	case first["model name"] != "":
		c.Model = first["model name"]
	case first["Hardware"] != "":
		c.Model = first["Hardware"]
	case first["CPU implementer"] != "" && first["CPU part"] != "":
		c.Model = first["CPU implementer"] + "/" + first["CPU part"]
	default:
		c.Model = "unknown"
	}
	return c
}

type memInfo struct {
	TotalBytes, AvailableBytes, SwapTotalBytes, SwapFreeBytes uint64
}

// parseMemInfo reads the four fields the summary needs, in bytes. MemTotal
// and MemAvailable are required, as every supported kernel reports both, and
// neither available memory nor free swap may exceed its total.
func parseMemInfo(data []byte) (memInfo, error) {
	var m memInfo
	fields := map[string]*uint64{
		"MemTotal": &m.TotalBytes, "MemAvailable": &m.AvailableBytes,
		"SwapTotal": &m.SwapTotalBytes, "SwapFree": &m.SwapFreeBytes,
	}
	found := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		dst := fields[key]
		if !ok || dst == nil {
			continue
		}
		num, unit, _ := strings.Cut(strings.TrimSpace(value), " ")
		n, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			return memInfo{}, fmt.Errorf("meminfo %s: %w", key, err)
		}
		if unit == "kB" {
			if n > math.MaxUint64/1024 {
				return memInfo{}, fmt.Errorf("meminfo %s: value out of range", key)
			}
			n *= 1024
		}
		*dst = n
		found[key] = true
	}
	if !found["MemTotal"] || !found["MemAvailable"] {
		return memInfo{}, errors.New("meminfo: MemTotal or MemAvailable missing")
	}
	if m.AvailableBytes > m.TotalBytes || m.SwapFreeBytes > m.SwapTotalBytes {
		return memInfo{}, errors.New("meminfo: available or free exceeds the total")
	}
	return m, nil
}

type loadAvg struct {
	One, Five, Fifteen float64
}

// parseLoadAvg reads the three load averages of /proc/loadavg.
func parseLoadAvg(data []byte) (loadAvg, error) {
	f := strings.Fields(string(data))
	if len(f) < 3 {
		return loadAvg{}, errors.New("loadavg: want at least three fields")
	}
	var v [3]float64
	for i := range v {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return loadAvg{}, fmt.Errorf("loadavg: field %d is not a non-negative number", i+1)
		}
		v[i] = n
	}
	return loadAvg{One: v[0], Five: v[1], Fifteen: v[2]}, nil
}

// parseUptime reads the whole seconds since boot from /proc/uptime.
func parseUptime(data []byte) (int64, error) {
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0, errors.New("uptime: empty")
	}
	s, err := strconv.ParseFloat(f[0], 64)
	// 2^53 seconds is far beyond any real uptime and keeps the conversion exact.
	if err != nil || math.IsNaN(s) || s < 0 || s >= 1<<53 {
		return 0, errors.New("uptime: first field is not a non-negative number of seconds")
	}
	return int64(s), nil
}

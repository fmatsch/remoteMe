//go:build linux

package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

func lines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		out = append(out, s.Text())
	}
	return out
}

func num64(s string) uint64 { v, _ := strconv.ParseUint(s, 10, 64); return v }

func takeSample() sample {
	var s sample
	if l := lines("/proc/stat"); len(l) > 0 {
		f := strings.Fields(l[0])
		for i := 1; i < len(f) && i <= 8; i++ {
			v := num64(f[i])
			s.cpuTotal += v
			if i != 4 && i != 5 { // nicht idle, iowait
				s.cpuBusy += v
			}
		}
	}
	var avail uint64
	for _, l := range lines("/proc/meminfo") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			s.memTotal = num64(f[1]) * 1024
		case "MemAvailable:":
			avail = num64(f[1]) * 1024
		}
	}
	if s.memTotal > avail {
		s.memUsed = s.memTotal - avail
	}
	seen := map[string]bool{}
	for _, l := range lines("/proc/mounts") {
		f := strings.Fields(l)
		if len(f) < 2 || !strings.HasPrefix(f[0], "/dev/") || seen[f[0]] || strings.HasPrefix(f[0], "/dev/loop") {
			continue
		}
		seen[f[0]] = true
		var st syscall.Statfs_t
		if syscall.Statfs(f[1], &st) == nil && st.Blocks > 0 && len(s.disks) < 8 {
			total := st.Blocks * uint64(st.Bsize)
			s.disks = append(s.disks, diskStat{Name: f[1], Total: total, Used: total - st.Bavail*uint64(st.Bsize)})
		}
	}
	for _, l := range lines("/proc/net/dev") {
		name, rest, ok := strings.Cut(l, ":")
		if f := strings.Fields(rest); ok && strings.TrimSpace(name) != "lo" && len(f) >= 9 {
			s.rx += num64(f[0])
			s.tx += num64(f[8])
		}
	}
	return s
}

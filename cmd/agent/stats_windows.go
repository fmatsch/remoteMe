//go:build windows

package main

import (
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pGetSystemTimes       = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")
	pGlobalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")
)

type memoryStatusEx struct {
	length, load                                                        uint32
	totalPhys, availPhys, totalPage, availPage, totalVirt, availVirt, x uint64
}

func takeSample() sample {
	var s sample
	var idle, kernel, user windows.Filetime
	if ok, _, _ := pGetSystemTimes.Call(uintptr(unsafe.Pointer(&idle)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); ok != 0 {
		t := func(f windows.Filetime) uint64 { return uint64(f.HighDateTime)<<32 | uint64(f.LowDateTime) }
		s.cpuTotal = t(kernel) + t(user) // Kernelzeit enthält die Leerlaufzeit
		s.cpuBusy = s.cpuTotal - t(idle)
	}
	m := memoryStatusEx{}
	m.length = uint32(unsafe.Sizeof(m))
	if ok, _, _ := pGlobalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&m))); ok != 0 {
		s.memTotal, s.memUsed = m.totalPhys, m.totalPhys-m.availPhys
	}
	// Nur lokale Festplatten: Netz- und Wechsellaufwerke könnten die Messung blockieren.
	mask, _ := windows.GetLogicalDrives()
	for i := 0; i < 26 && len(s.disks) < 8; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) != windows.DRIVE_FIXED {
			continue
		}
		var free, total, totalFree uint64
		if windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree) == nil && total > 0 {
			s.disks = append(s.disks, diskStat{Name: root[:2], Used: total - totalFree, Total: total})
		}
	}
	ifs, _ := net.Interfaces()
	for _, in := range ifs {
		if in.Flags&net.FlagLoopback != 0 || in.Flags&net.FlagUp == 0 {
			continue
		}
		var row windows.MibIfRow2
		row.InterfaceIndex = uint32(in.Index)
		if windows.GetIfEntry2Ex(windows.MibIfEntryNormal, &row) == nil {
			s.rx += row.InOctets
			s.tx += row.OutOctets
		}
	}
	return s
}

//go:build darwin

package main

/*
#cgo CFLAGS: -mmacosx-version-min=13.0
#include <mach/mach.h>
#include <sys/param.h>
#include <sys/mount.h>
#include <sys/sysctl.h>
#include <sys/socket.h>
#include <ifaddrs.h>
#include <net/if.h>
#include <stdint.h>
#include <stdlib.h>

static void fwCPU(uint64_t *busy, uint64_t *total) {
	host_cpu_load_info_data_t c;
	mach_msg_type_number_t n = HOST_CPU_LOAD_INFO_COUNT;
	if (host_statistics(mach_host_self(), HOST_CPU_LOAD_INFO, (host_info_t)&c, &n) != KERN_SUCCESS) return;
	*busy = (uint64_t)c.cpu_ticks[CPU_STATE_USER] + c.cpu_ticks[CPU_STATE_SYSTEM] + c.cpu_ticks[CPU_STATE_NICE];
	*total = *busy + c.cpu_ticks[CPU_STATE_IDLE];
}
static void fwMem(uint64_t *used, uint64_t *total) {
	size_t len = sizeof(*total);
	sysctlbyname("hw.memsize", total, &len, NULL, 0);
	vm_statistics64_data_t v;
	mach_msg_type_number_t n = HOST_VM_INFO64_COUNT;
	if (host_statistics64(mach_host_self(), HOST_VM_INFO64, (host_info64_t)&v, &n) != KERN_SUCCESS) return;
	// wie "Belegter Speicher" der Aktivitätsanzeige: App-Speicher + reserviert + komprimiert
	uint64_t pages = (uint64_t)v.internal_page_count - v.purgeable_count + v.wire_count + v.compressor_page_count;
	*used = pages * (uint64_t)vm_kernel_page_size;
}
static void fwDisk(const char *path, uint64_t *used, uint64_t *total) {
	struct statfs s;
	if (statfs(path, &s) != 0) return;
	*total = (uint64_t)s.f_blocks * s.f_bsize;
	*used = *total - (uint64_t)s.f_bavail * s.f_bsize;
}
static void fwNet(uint64_t *rx, uint64_t *tx) {
	struct ifaddrs *list, *i;
	if (getifaddrs(&list) != 0) return;
	for (i = list; i; i = i->ifa_next) {
		if (!i->ifa_addr || i->ifa_addr->sa_family != AF_LINK || (i->ifa_flags & IFF_LOOPBACK) || !i->ifa_data) continue;
		struct if_data *d = (struct if_data *)i->ifa_data;
		*rx += d->ifi_ibytes;
		*tx += d->ifi_obytes;
	}
	freeifaddrs(list);
}
*/
import "C"

import "unsafe"

func takeSample() sample {
	var s sample
	var a, b C.uint64_t
	C.fwCPU(&a, &b)
	s.cpuBusy, s.cpuTotal = uint64(a), uint64(b)
	a, b = 0, 0
	C.fwMem(&a, &b)
	s.memUsed, s.memTotal = uint64(a), uint64(b)
	a, b = 0, 0
	root := C.CString("/")
	defer C.free(unsafe.Pointer(root))
	C.fwDisk(root, &a, &b)
	if b > 0 {
		s.disks = []diskStat{{Name: "Macintosh HD", Used: uint64(a), Total: uint64(b)}}
	}
	a, b = 0, 0
	C.fwNet(&a, &b)
	s.rx, s.tx = uint64(a), uint64(b)
	return s
}

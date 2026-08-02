//go:build unix

package main

import "syscall"

// freeDiskPercent reports the free space on the filesystem holding path.
// PLAN.md section 15 treats a nearly full spool as a medium-severity finding:
// once it fills, accepted mail cannot be written and has to be refused.
func freeDiskPercent(path string) (float64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks
	if total == 0 {
		return 0, nil
	}
	// Bavail rather than Bfree: the blocks reserved for root are not usable
	// by this process.
	return float64(stat.Bavail) / float64(total) * 100, nil
}

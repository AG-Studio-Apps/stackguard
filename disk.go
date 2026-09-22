package main

import (
	"fmt"
	"syscall"
)

// diskReading is the free fraction of the filesystem the Docker data root sits
// on, and a sentence naming the numbers.
type diskReading struct {
	fraction float64
	detail   string
}

// checkDisk statfs's the agent's own root. That sounds like the wrong
// filesystem and is the right one: a container's writable layer lives under
// the daemon's data root, so the agent's / is on exactly the filesystem that
// fills up — no bind mount, no privileges, no path to configure.
func checkDisk(path string) (diskReading, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil || st.Blocks == 0 {
		return diskReading{}, false
	}
	bsize := uint64(st.Frsize)
	if bsize == 0 {
		bsize = uint64(st.Bsize)
	}
	total := float64(st.Blocks) * float64(bsize)
	free := float64(st.Bavail) * float64(bsize) // Bavail, not Bfree: root's reserve isn't ours
	if total <= 0 {
		return diskReading{}, false
	}
	fraction := free / total
	percent := int((1 - fraction) * 100)
	return diskReading{
		fraction: fraction,
		detail:   fmt.Sprintf("Docker storage is %d%% full · %s free of %s.", percent, human(free), human(total)),
	}, true
}

func human(bytes float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value, unit := bytes, 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	if value >= 10 || unit == 0 {
		return fmt.Sprintf("%d %s", int(value+0.5), units[unit])
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

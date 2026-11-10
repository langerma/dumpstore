package zfs

import (
	"bufio"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// ScrubScheduleMode indicates which scheduling mechanism is in use.
type ScrubScheduleMode string

const (
	// ScrubModeZfsutils is used on Linux: ZFS_SCRUB_POOLS in /etc/default/zfs,
	// executed by /usr/lib/zfs-linux/scrub on the 2nd Sunday of each month via
	// /etc/cron.d/zfsutils-linux.
	ScrubModeZfsutils ScrubScheduleMode = "zfsutils"

	// ScrubModePeriodic is used on FreeBSD: daily_scrub_zfs_pools in
	// /etc/periodic.conf, executed by /etc/periodic/daily/800.scrub-zfs.
	ScrubModePeriodic ScrubScheduleMode = "periodic"
)

// ScrubSchedule represents a pool that has been explicitly added to the
// platform scrub list.
type ScrubSchedule struct {
	Pool string `json:"pool"`
}

// ScrubScheduleList is the response returned by GET /api/v1/pools/scrub-schedules.
// When Schedules is empty, the platform default applies (all pools are scrubbed).
type ScrubScheduleList struct {
	Mode          ScrubScheduleMode `json:"mode"`
	ThresholdDays int               `json:"threshold_days,omitempty"` // FreeBSD periodic only
	Schedules     []ScrubSchedule   `json:"schedules"`
}

// OSType returns "freebsd" on FreeBSD, "linux" otherwise.
func OSType() string {
	if runtime.GOOS == "freebsd" {
		return "freebsd"
	}
	return "linux"
}

// ScrubSchedules reads the platform-appropriate configuration and returns the
// full schedule list.
//
// Linux:   reads ZFS_SCRUB_POOLS from /etc/default/zfs
// FreeBSD: reads daily_scrub_zfs_pools from /etc/periodic.conf
func ScrubSchedules() (ScrubScheduleList, error) {
	if runtime.GOOS == "freebsd" {
		return readPeriodicConf("/etc/periodic.conf")
	}
	return readZfsDefaults("/etc/default/zfs")
}

// readZfsDefaults reads /etc/default/zfs and extracts ZFS_SCRUB_POOLS.
//
//	ZFS_SCRUB_POOLS="tank backup"   (space-separated pool names)
//
// An empty or absent ZFS_SCRUB_POOLS means the package default applies:
// /usr/lib/zfs-linux/scrub scrubs all pools. In that case Schedules is empty.
func readZfsDefaults(path string) (ScrubScheduleList, error) {
	result := ScrubScheduleList{Mode: ScrubModeZfsutils, Schedules: []ScrubSchedule{}}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result, nil
		}
		return result, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || key != "ZFS_SCRUB_POOLS" {
			continue
		}
		val = strings.Trim(val, `"'`)
		for _, pool := range strings.Fields(val) {
			if pool != "" {
				result.Schedules = append(result.Schedules, ScrubSchedule{Pool: pool})
			}
		}
		break
	}
	return result, scanner.Err()
}

// readPeriodicConf reads /etc/periodic.conf and extracts daily_scrub_zfs_pools
// and daily_scrub_zfs_default_threshold.
//
// An empty or absent daily_scrub_zfs_pools means all pools are scrubbed.
func readPeriodicConf(path string) (ScrubScheduleList, error) {
	result := ScrubScheduleList{Mode: ScrubModePeriodic, Schedules: []ScrubSchedule{}}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			result.ThresholdDays = 35
			return result, nil
		}
		return result, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		val = strings.Trim(val, `"'`)

		switch key {
		case "daily_scrub_zfs_default_threshold":
			if n, err := strconv.Atoi(val); err == nil {
				result.ThresholdDays = n
			}
		case "daily_scrub_zfs_pools":
			for _, pool := range strings.Fields(val) {
				if pool != "" {
					result.Schedules = append(result.Schedules, ScrubSchedule{Pool: pool})
				}
			}
		}
	}

	if result.ThresholdDays == 0 {
		result.ThresholdDays = 35
	}
	return result, scanner.Err()
}

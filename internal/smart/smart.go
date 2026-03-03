// Package smart collects S.M.A.R.T. health data via smartctl.
package smart

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// DriveInfo holds summarized S.M.A.R.T. data for one physical drive.
type DriveInfo struct {
	Device        string `json:"device"`
	Model         string `json:"model"`
	Serial        string `json:"serial"`
	Protocol      string `json:"protocol"`      // "ATA", "SCSI", "NVMe"
	CapacityBytes uint64 `json:"capacity_bytes"` // 0 if unknown
	TempC         int    `json:"temp_c"`         // 0 if unknown
	PowerOnHours  int    `json:"power_on_hours"`
	Passed        bool   `json:"passed"`
	// ATA-specific defect counters (SMART attribute IDs 5, 197, 198)
	ReallocatedSectors  int `json:"reallocated_sectors"`
	PendingSectors      int `json:"pending_sectors"`
	UncorrectableErrors int `json:"uncorrectable_errors"`
	// SCSI
	GrownDefects int `json:"grown_defects"`
	// NVMe
	MediaErrors int `json:"media_errors"`
}

// Result is the payload returned by the API.
type Result struct {
	Available bool        `json:"available"`
	Drives    []DriveInfo `json:"drives"`
}

// Collect scans for drives and returns SMART summaries.
// Returns Result{Available: false} when smartctl is not installed.
func Collect() Result {
	if _, err := exec.LookPath("smartctl"); err != nil {
		return Result{Available: false}
	}
	devices, err := scanDevices()
	if err != nil || len(devices) == 0 {
		return Result{Available: true, Drives: []DriveInfo{}}
	}
	var drives []DriveInfo
	for _, dev := range devices {
		info, err := queryDrive(dev)
		if err != nil {
			continue
		}
		drives = append(drives, info)
	}
	if drives == nil {
		drives = []DriveInfo{}
	}
	return Result{Available: true, Drives: drives}
}

func scanDevices() ([]string, error) {
	out, err := runSmartctl("--scan")
	if err != nil || len(out) == 0 {
		return nil, fmt.Errorf("smartctl --scan failed")
	}
	var scan struct {
		Devices []struct {
			Name string `json:"name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(trimToJSON(out), &scan); err != nil {
		return nil, err
	}
	devs := make([]string, 0, len(scan.Devices))
	for _, d := range scan.Devices {
		devs = append(devs, d.Name)
	}
	return devs, nil
}

// rawDrive is the minimal subset of `smartctl -j -a` output we parse.
type rawDrive struct {
	Device struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
	} `json:"device"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`
	UserCapacity struct {
		Bytes uint64 `json:"bytes"`
	} `json:"user_capacity"`
	Temperature struct {
		Current int `json:"current"`
	} `json:"temperature"`
	PowerOnTime struct {
		Hours int `json:"hours"`
	} `json:"power_on_time"`
	SmartStatus struct {
		Passed bool `json:"passed"`
	} `json:"smart_status"`
	AtaSmartAttributes struct {
		Table []struct {
			ID  int `json:"id"`
			Raw struct {
				Value int64 `json:"value"`
			} `json:"raw"`
		} `json:"table"`
	} `json:"ata_smart_attributes"`
	ScsiGrownDefectList int `json:"scsi_grown_defect_list"`
	NvmeSmartHealth     struct {
		MediaErrors int `json:"media_errors"`
	} `json:"nvme_smart_health_information_log"`
}

func queryDrive(device string) (DriveInfo, error) {
	out, err := runSmartctl("-a", device)
	// smartctl uses exit code as a bitmask of warnings; non-zero is not always fatal.
	// Parse if we got output.
	if len(out) == 0 {
		return DriveInfo{}, fmt.Errorf("no output for %s: %w", device, err)
	}
	var raw rawDrive
	if err := json.Unmarshal(trimToJSON(out), &raw); err != nil {
		return DriveInfo{}, fmt.Errorf("parse %s: %w", device, err)
	}
	info := DriveInfo{
		Device:        raw.Device.Name,
		Model:         raw.ModelName,
		Serial:        raw.SerialNumber,
		Protocol:      raw.Device.Protocol,
		CapacityBytes: raw.UserCapacity.Bytes,
		TempC:         raw.Temperature.Current,
		PowerOnHours:  raw.PowerOnTime.Hours,
		Passed:        raw.SmartStatus.Passed,
		GrownDefects:  raw.ScsiGrownDefectList,
		MediaErrors:   raw.NvmeSmartHealth.MediaErrors,
	}
	for _, attr := range raw.AtaSmartAttributes.Table {
		switch attr.ID {
		case 5:
			info.ReallocatedSectors = int(attr.Raw.Value)
		case 197:
			info.PendingSectors = int(attr.Raw.Value)
		case 198:
			info.UncorrectableErrors = int(attr.Raw.Value)
		}
	}
	return info, nil
}

// runSmartctl calls smartctl with -j plus the given args.
// Always returns stdout if any was produced (smartctl exit codes are informational bitmasks).
func runSmartctl(args ...string) ([]byte, error) {
	all := append([]string{"-j"}, args...)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("smartctl", all...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if stdout.Len() > 0 {
		return stdout.Bytes(), nil
	}
	return nil, fmt.Errorf("smartctl %s: %w: %s", strings.Join(args, " "), err, stderr.String())
}

// trimToJSON skips any leading non-JSON bytes (smartctl occasionally emits warnings first).
func trimToJSON(data []byte) []byte {
	if i := bytes.IndexByte(data, '{'); i >= 0 {
		return data[i:]
	}
	return data
}

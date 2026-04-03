package system

import (
	"os/exec"
	"runtime"
	"strings"
)

// ServiceStatus describes a managed sharing service.
type ServiceStatus struct {
	Name        string `json:"name"`         // logical name: "samba", "nfs", "iscsi"
	DisplayName string `json:"display_name"` // human-readable label
	UnitName    string `json:"unit_name"`    // platform-specific unit/service name
	Active      bool   `json:"active"`
	Enabled     bool   `json:"enabled"`
	State       string `json:"state"` // "active", "inactive", "failed", "unknown"
}

// managedServiceDef maps a logical service name to platform-specific unit names.
type managedServiceDef struct {
	name    string
	display string
	linux   string
	freebsd string
}

var managedServices = []managedServiceDef{
	{"samba", "Samba (SMB)", "smbd", "samba_server"},
	{"nfs", "NFS Server", "nfs-kernel-server", "nfsd"},
	{"iscsi", "iSCSI Target", "iscsid", "ctld"},
}

// ServiceUnitName returns the platform-specific unit name for a logical service
// name (e.g. "samba" → "smbd" on Linux). Returns "" if the name is unknown.
func ServiceUnitName(logical string) string {
	isFreeBSD := runtime.GOOS == "freebsd"
	for _, svc := range managedServices {
		if svc.name == logical {
			if isFreeBSD {
				return svc.freebsd
			}
			return svc.linux
		}
	}
	return ""
}

// ListServices returns the current status of all managed sharing services.
// Failures for individual services are swallowed; the state is set to "unknown".
func ListServices() []ServiceStatus {
	isFreeBSD := runtime.GOOS == "freebsd"
	out := make([]ServiceStatus, 0, len(managedServices))
	for _, svc := range managedServices {
		unit := svc.linux
		if isFreeBSD {
			unit = svc.freebsd
		}
		st := ServiceStatus{
			Name:        svc.name,
			DisplayName: svc.display,
			UnitName:    unit,
			State:       "unknown",
		}
		if isFreeBSD {
			st.Active, st.Enabled, st.State = serviceStatusFreeBSD(unit)
		} else {
			st.Active, st.Enabled, st.State = serviceStatusLinux(unit)
		}
		out = append(out, st)
	}
	return out
}

// serviceStatusLinux queries systemd for the unit's active and enabled states.
func serviceStatusLinux(unit string) (active, enabled bool, state string) {
	state = "unknown"
	if err := exec.Command("systemctl", "is-active", "--quiet", unit).Run(); err == nil {
		active = true
		state = "active"
	} else {
		// is-active exits 3 for inactive, 1 for failed/other — distinguish them.
		if svcExitCode(err) == 3 {
			state = "inactive"
		} else {
			// Check whether it's explicitly failed.
			if exec.Command("systemctl", "is-failed", "--quiet", unit).Run() == nil {
				state = "failed"
			} else {
				state = "inactive"
			}
		}
	}
	if exec.Command("systemctl", "is-enabled", "--quiet", unit).Run() == nil {
		enabled = true
	}
	return
}

// serviceStatusFreeBSD queries rc.d for the service's running and enabled states.
func serviceStatusFreeBSD(unit string) (active, enabled bool, state string) {
	state = "unknown"
	if exec.Command("service", unit, "status").Run() == nil {
		active = true
		state = "active"
	} else {
		state = "inactive"
	}
	// sysrc -n <key> prints the value from rc.conf; "YES" means enabled.
	key := strings.ReplaceAll(unit, "-", "_") + "_enable"
	if out, err := exec.Command("sysrc", "-n", key).Output(); err == nil {
		if strings.TrimSpace(string(out)) == "YES" {
			enabled = true
		}
	}
	return
}

// svcExitCode extracts the process exit code from an *exec.ExitError.
func svcExitCode(err error) int {
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}

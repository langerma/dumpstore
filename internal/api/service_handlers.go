package api

import (
	"fmt"
	"net/http"
	"regexp"
	"runtime"

	"dumpstore/internal/ansible"
	"dumpstore/internal/system"
)

// validServiceName matches the logical service names we accept from clients.
var validServiceName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)

var validServiceActions = map[string]bool{
	"start":   true,
	"stop":    true,
	"restart": true,
	"enable":  true,
	"disable": true,
}

// servicePlaybook returns the OS-appropriate service control playbook path.
func servicePlaybook() string {
	if runtime.GOOS == "freebsd" {
		return "service_control_freebsd.yml"
	}
	return "service_control_linux.yml"
}

// getServices handles GET /api/services
// Returns the status of all managed sharing services (Samba, NFS, iSCSI).
func (h *Handler) getServices(w http.ResponseWriter, r *http.Request) {
	writeJSON(r.Context(), w, system.ListServices())
}

// mutateService handles POST /api/services/{service}/{action}
// Valid actions: start, stop, restart, enable, disable.
func (h *Handler) mutateService(w http.ResponseWriter, r *http.Request) {
	svc := r.PathValue("service")
	action := r.PathValue("action")

	if !validServiceName.MatchString(svc) {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid service name"), nil)
		return
	}
	if !validServiceActions[action] {
		writeError(r.Context(), w, http.StatusBadRequest, fmt.Errorf("invalid action %q", action), nil)
		return
	}

	unitName := system.ServiceUnitName(svc)
	if unitName == "" {
		writeError(r.Context(), w, http.StatusNotFound, fmt.Errorf("unknown service: %s", svc), nil)
		return
	}

	out, err := h.runOp(servicePlaybook(), map[string]string{
		"service_name": unitName,
		"action":       action,
	})
	auditLog(r.Context(), r, "service."+action, svc, err)
	if err != nil {
		var steps []ansible.TaskStep
		if out != nil {
			steps = out.Steps()
		}
		writeError(r.Context(), w, http.StatusInternalServerError, err, steps)
		return
	}
	writeJSON(r.Context(), w, map[string]any{"service": svc, "tasks": out.Steps()})
}

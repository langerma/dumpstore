package api

import (
	"context"
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

// getServices handles GET /api/v1/services
// Returns the status of all managed sharing services (Samba, NFS, iSCSI).
func (h *Handler) getServices(context.Context, struct{}) ([]system.ServiceStatus, error) {
	return system.ListServices(), nil
}

// mutateService handles POST /api/v1/services/{service}/{action}
// Valid actions: start, stop, restart, enable, disable.
func (h *Handler) mutateService(ctx context.Context, in serviceActionParam) (ServiceResult, error) {
	svc, action := in.Service, in.Action

	if !validServiceName.MatchString(svc) {
		return ServiceResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid service name"))
	}
	if !validServiceActions[action] {
		return ServiceResult{}, errStatus(http.StatusBadRequest, fmt.Errorf("invalid action %q", action))
	}

	st, ok := system.LookupService(svc)
	if !ok {
		return ServiceResult{}, errStatus(http.StatusNotFound, fmt.Errorf("unknown service: %s", svc))
	}
	if !st.Installed {
		return ServiceResult{}, errStatus(http.StatusConflict,
			fmt.Errorf("%s is not installed (no %s service on this host)", st.DisplayName, st.UnitName))
	}

	out, err := h.runOp(ctx, servicePlaybook(), map[string]string{
		"service_name": st.UnitName,
		"action":       action,
	})
	auditLog(ctx, "service."+action, svc, err)
	if err != nil {
		return ServiceResult{}, playbookError(err, out)
	}
	return ServiceResult{Service: svc, Tasks: out.Steps()}, nil
}

// ServiceResult is the result of a service action.
type ServiceResult struct {
	Service string             `json:"service"`
	Tasks   []ansible.TaskStep `json:"tasks"`
}

//go:build integration

package integration

import (
	"net/http"
	"testing"
)

type service struct {
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Enabled bool   `json:"enabled"`
	State   string `json:"state"`
}

// TestServices restarts and enables Samba through the service-control
// endpoint. stop/disable are deliberately not exercised — they would leave
// the shared VM without a running smbd for the rest of the suite.
func TestServices(t *testing.T) {
	skipUnlessVMTool(t, "smbd")
	samba := func() service {
		for _, s := range decode[[]service](t, apiOK(t, "GET", "/api/services", nil)) {
			if s.Name == "samba" {
				return s
			}
		}
		t.Fatalf("samba not in /api/services")
		return service{}
	}

	assertTasks(t, apiOK(t, "POST", "/api/services/samba/restart", nil))
	if s := samba(); !s.Active || s.State != "active" {
		t.Fatalf("samba after restart: %+v, want active", s)
	}
	assertTasks(t, apiOK(t, "POST", "/api/services/samba/enable", nil))
	if s := samba(); !s.Enabled {
		t.Fatalf("samba after enable: %+v, want enabled", s)
	}

	apiStatus(t, http.StatusNotFound, "POST", "/api/services/itest-nope/restart", nil)
	apiStatus(t, http.StatusBadRequest, "POST", "/api/services/samba/explode", nil)
	apiStatus(t, http.StatusBadRequest, "POST", "/api/services/Bad_Name/restart", nil)
}

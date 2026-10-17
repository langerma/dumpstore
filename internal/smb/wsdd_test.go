package smb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderWSDD(t *testing.T) {
	cases := []struct {
		workgroup string
		params    string
	}{
		{"WORKGROUP", `WSDD_PARAMS="--workgroup WORKGROUP"`},
		{"HOME-LAB.1", `WSDD_PARAMS="--workgroup HOME-LAB.1"`},
		// Unsafe or over-long names are omitted; wsdd falls back to WORKGROUP.
		{"MY GROUP", `WSDD_PARAMS=""`},
		{`X"; rm -rf /`, `WSDD_PARAMS=""`},
		{"SIXTEENCHARSXXXX", `WSDD_PARAMS=""`},
		{"", `WSDD_PARAMS=""`},
	}
	for _, tc := range cases {
		t.Run(tc.workgroup, func(t *testing.T) {
			cfg := SMBConfig{Global: SMBGlobal{Workgroup: tc.workgroup}}
			dst := filepath.Join(t.TempDir(), "wsdd")
			if err := cfg.RenderWSDDToFile(dst); err != nil {
				t.Fatalf("render: %v", err)
			}
			data, err := os.ReadFile(dst)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "\n"+tc.params+"\n") {
				t.Errorf("rendered file lacks %q:\n%s", tc.params, data)
			}
			if !WSDDManaged(dst) {
				t.Error("rendered file not recognised as managed")
			}
		})
	}
}

func TestWSDDManaged_Unmanaged(t *testing.T) {
	dir := t.TempDir()
	stock := filepath.Join(dir, "wsdd")
	if err := os.WriteFile(stock, []byte("WSDD_PARAMS=\"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if WSDDManaged(stock) {
		t.Error("stock file reported as managed")
	}
	if WSDDManaged(filepath.Join(dir, "missing")) {
		t.Error("missing file reported as managed")
	}
}

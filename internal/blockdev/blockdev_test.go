package blockdev

import (
	"reflect"
	"testing"
)

func TestParseGeomDiskList(t *testing.T) {
	out := `Geom name: ada0
Providers:
1. Name: ada0
   Mediasize: 21474836480 (20G)
   Sectorsize: 512
   Mode: r2w2e3
   descr: VBOX HARDDISK
   ident: VB1234-5678
   rotationrate: unknown
   fwsectors: 63
   fwheads: 16

Geom name: ada1
Providers:
1. Name: ada1
   Mediasize: 10737418240 (10G)
   Sectorsize: 512
   Mode: r0w0e0
   descr: VBOX HARDDISK
`
	devs := parseGeomDiskList(out)
	want := []Device{
		{Name: "ada0", Path: "/dev/ada0", SizeBytes: 21474836480, Model: "VBOX HARDDISK"},
		{Name: "ada1", Path: "/dev/ada1", SizeBytes: 10737418240, Model: "VBOX HARDDISK"},
	}
	if !reflect.DeepEqual(devs, want) {
		t.Errorf("parseGeomDiskList = %+v, want %+v", devs, want)
	}
}

func TestParseGeomDiskListEmpty(t *testing.T) {
	if devs := parseGeomDiskList(""); len(devs) != 0 {
		t.Errorf("expected no devices, got %+v", devs)
	}
}

func TestVdevMatchesDevice(t *testing.T) {
	cases := []struct {
		vdev, dev string
		want      bool
	}{
		{"sdb", "sdb", true},
		{"sdb1", "sdb", true},
		{"sdb12", "sdb", true},
		{"sdba", "sdb", false},
		{"nvme0n1p2", "nvme0n1", true},
		{"nvme0n1", "nvme0n1", true},
		{"nvme0n12", "nvme0n1", true}, // ambiguous but acceptable best-effort
		{"ada0p3", "ada0", true},
		{"ada1", "ada0", false},
		{"sdc", "sdb", false},
		{"sd", "sdb", false},
	}
	for _, c := range cases {
		if got := vdevMatchesDevice(c.vdev, c.dev); got != c.want {
			t.Errorf("vdevMatchesDevice(%q, %q) = %v, want %v", c.vdev, c.dev, got, c.want)
		}
	}
}

func TestMarkInUse(t *testing.T) {
	devs := []Device{
		{Name: "sda", Path: "/dev/sda"},
		{Name: "sdb", Path: "/dev/sdb"},
		{Name: "sdc", Path: "/dev/sdc"},
	}
	// mirror-0 is a grouping vdev and matches nothing; sdb2 is a partition.
	MarkInUse(devs, map[string]string{
		"mirror-0": "tank",
		"sda":      "tank",
		"sdb2":     "tank",
	})
	if devs[0].InUseBy != "tank" {
		t.Errorf("sda InUseBy = %q, want tank", devs[0].InUseBy)
	}
	if devs[1].InUseBy != "tank" {
		t.Errorf("sdb InUseBy = %q, want tank", devs[1].InUseBy)
	}
	if devs[2].InUseBy != "" {
		t.Errorf("sdc InUseBy = %q, want empty", devs[2].InUseBy)
	}
}

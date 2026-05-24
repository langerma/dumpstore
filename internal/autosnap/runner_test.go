package autosnap

import (
	"reflect"
	"testing"

	"dumpstore/internal/scheduler"
	"dumpstore/internal/zfs"
)

func TestEvalBucket(t *testing.T) {
	prop := func(v string) zfs.DatasetProp { return zfs.DatasetProp{Value: v} }

	cases := []struct {
		name      string
		props     zfs.AutoSnapshotProps
		bucket    Bucket
		wantOK    bool
		wantKeep  int
	}{
		{
			name:   "master=true, hourly=24",
			props:  zfs.AutoSnapshotProps{Master: prop("true"), Hourly: prop("24")},
			bucket: BucketHourly, wantOK: true, wantKeep: 24,
		},
		{
			name:   "master=false rejects everything",
			props:  zfs.AutoSnapshotProps{Master: prop("false"), Hourly: prop("24")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "master unset rejects",
			props:  zfs.AutoSnapshotProps{Hourly: prop("24")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "bucket empty rejects",
			props:  zfs.AutoSnapshotProps{Master: prop("true")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "bucket=- rejects (zfs `not set`)",
			props:  zfs.AutoSnapshotProps{Master: prop("true"), Hourly: prop("-")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "bucket=0 rejects",
			props:  zfs.AutoSnapshotProps{Master: prop("true"), Hourly: prop("0")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "bucket=non-numeric rejects",
			props:  zfs.AutoSnapshotProps{Master: prop("true"), Hourly: prop("true")},
			bucket: BucketHourly, wantOK: false,
		},
		{
			name:   "monthly works the same",
			props:  zfs.AutoSnapshotProps{Master: prop("true"), Monthly: prop("12")},
			bucket: BucketMonthly, wantOK: true, wantKeep: 12,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, keep := evalBucket(c.props, c.bucket)
			if ok != c.wantOK || keep != c.wantKeep {
				t.Errorf("evalBucket = (%v, %d), want (%v, %d)", ok, keep, c.wantOK, c.wantKeep)
			}
		})
	}
}

func TestPruneSelection(t *testing.T) {
	in := []string{
		"tank/data@zfs-auto-snap_hourly-2026-05-24-1000",
		"tank/data@zfs-auto-snap_hourly-2026-05-24-1100",
		"tank/data@zfs-auto-snap_hourly-2026-05-24-1200",
		"tank/data@zfs-auto-snap_hourly-2026-05-24-1300",
		"tank/data@zfs-auto-snap_hourly-2026-05-24-1400",
	}
	cases := []struct {
		name string
		keep int
		want []string
	}{
		{"keep all", 5, nil},
		{"keep more than have", 10, nil},
		{"keep 3 destroys oldest 2", 3, []string{
			"tank/data@zfs-auto-snap_hourly-2026-05-24-1000",
			"tank/data@zfs-auto-snap_hourly-2026-05-24-1100",
		}},
		{"keep 0 destroys all", 0, in},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := pruneSelection(in, c.keep)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("pruneSelection = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBucketCronExpressions(t *testing.T) {
	// Smoke: every bucket has a valid cron expression that the shared parser accepts.
	for _, b := range AllBuckets {
		if _, err := scheduler.Parse(b.CronExpression()); err != nil {
			t.Errorf("bucket %s has invalid cron %q: %v", b, b.CronExpression(), err)
		}
	}
}

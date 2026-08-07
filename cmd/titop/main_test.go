package main

import (
	"strings"
	"testing"
	"time"

	"titop/internal/monitor"
	"titop/internal/tidbsql"
)

func TestFormatUptime(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{400 * 24 * time.Hour, "1y"},
		{95 * 24 * time.Hour, "3M"},
		{17 * 24 * time.Hour, "2w"},
		{5*24*time.Hour + 3*time.Hour, "5d"},
		{8*time.Hour + 12*time.Minute, "8h"},
		{6*time.Minute + 20*time.Second, "6m"},
		{42 * time.Second, "42s"},
	}
	for _, tt := range tests {
		if got := formatUptime(tt.duration); got != tt.want {
			t.Errorf("formatUptime(%s)=%q, want %q", tt.duration, got, tt.want)
		}
	}
}

func TestParsePrometheusSpec(t *testing.T) {
	tests := []struct {
		spec, wantCluster, wantEndpoint string
	}{
		{"prod-tidb@http://10.0.0.9:9090", "prod-tidb", "http://10.0.0.9:9090"},
		{"prod-tidb@10.0.0.9:9090", "prod-tidb", "http://10.0.0.9:9090"},
		{"http://10.0.0.9:9090", "unknown", "http://10.0.0.9:9090"},
		{"https://prometheus.example.com", "unknown", "https://prometheus.example.com"},
		{"10.0.0.9:9090", "unknown", "http://10.0.0.9:9090"},
		{" [::1]:9090 ", "unknown", "http://[::1]:9090"},
	}
	for _, tt := range tests {
		cluster, endpoint, err := parsePrometheusSpec(tt.spec)
		if err != nil || cluster != tt.wantCluster || endpoint != tt.wantEndpoint {
			t.Errorf("parsePrometheusSpec(%q) = %q, %q, %v", tt.spec, cluster, endpoint, err)
		}
	}
	for _, invalid := range []string{"", "@http://10.0.0.9:9090", "prod@", "bad cluster@10.0.0.9:9090"} {
		if _, _, err := parsePrometheusSpec(invalid); err == nil {
			t.Errorf("expected %q to fail", invalid)
		}
	}
}

func TestSplitSQLTypes(t *testing.T) {
	rows := []monitor.Activity{{Name: "Select"}, {Name: "Commit"}, {Name: "CreateTable"}, {Name: "Show"}, {Name: "Rollback"}, {Name: "other"}}
	dml, ddl, admin, other := splitSQLTypes(rows)
	if len(dml) != 3 || dml[0].Name != "Select" || dml[1].Name != "Commit" || dml[2].Name != "Rollback" {
		t.Fatalf("unexpected DML/transaction group: %#v", dml)
	}
	if len(ddl) != 1 || ddl[0].Name != "CreateTable" {
		t.Fatalf("unexpected DDL group: %#v", ddl)
	}
	if len(admin) != 1 || admin[0].Name != "Show" {
		t.Fatalf("unexpected admin group: %#v", admin)
	}
	if len(other) != 1 || other[0].Name != "other" {
		t.Fatalf("unexpected other group: %#v", other)
	}
}

func TestHighSessionResources(t *testing.T) {
	if highSessionResources(tidbsql.Session{Memory: 99 * 1024 * 1024, Disk: 1023 * 1024 * 1024}) {
		t.Fatal("values below thresholds must not alert")
	}
	if !highSessionResources(tidbsql.Session{Memory: 100 * 1024 * 1024}) {
		t.Fatal("100 MiB memory must alert")
	}
	if !highSessionResources(tidbsql.Session{Disk: 1024 * 1024 * 1024}) {
		t.Fatal("1 GiB disk must alert")
	}
}

func TestDigestID(t *testing.T) {
	const digest = "d26b4f5c8e0a4e38427d9e980a5f86aa0bf4b8409b5ca31a899c439e4b3c1f21"
	id := digestID(digest)
	if len(id) != 13 {
		t.Fatalf("digestID length = %d, want 13", len(id))
	}
	if id != digestID(digest) {
		t.Fatal("digestID must be stable")
	}
	if id == digestID(digest+"0") {
		t.Fatal("different digests should have different IDs")
	}
	if strings.Trim(id, "0123456789abcdfghjkmnpqrstuvwxyz") != "" {
		t.Fatalf("digestID contains unsupported characters: %q", id)
	}
	if digestID("") != "-" {
		t.Fatal("empty digest must display as a dash")
	}
}

func TestLongQueryCount(t *testing.T) {
	rows := []tidbsql.Session{{Seconds: 9}, {Seconds: 10}, {Seconds: 42}}
	if got := longQueryCount(rows, 10*time.Second); got != 2 {
		t.Fatalf("longQueryCount() = %d, want 2", got)
	}
}

func TestWorkloadColor(t *testing.T) {
	if got := workloadColor(monitor.Snapshot{}, 49999, 50000); got != green {
		t.Fatalf("below threshold color = %q, want green", got)
	}
	if got := workloadColor(monitor.Snapshot{}, 50000, 50000); got != yellow {
		t.Fatalf("high healthy workload color = %q, want yellow", got)
	}
	if got := workloadColor(monitor.Snapshot{P99: 1}, 50000, 50000); got != red+bold {
		t.Fatalf("high slow workload color = %q, want red", got)
	}
	s := monitor.Snapshot{Instances: []monitor.Instance{{Role: "TIDB", CPU: .9}}}
	if got := workloadColor(s, 5000, 5000); got != red+bold {
		t.Fatalf("high CPU workload color = %q, want red", got)
	}
}

func TestSchemaKVColors(t *testing.T) {
	if got := mvccAmplificationColor(1.99); got != green {
		t.Fatalf("low MVCC amplification color = %q, want green", got)
	}
	if got := mvccAmplificationColor(2); got != yellow {
		t.Fatalf("medium MVCC amplification color = %q, want yellow", got)
	}
	if got := mvccAmplificationColor(10); got != red+bold {
		t.Fatalf("high MVCC amplification color = %q, want red", got)
	}
	if got := copPerExecutionColor(100); got != yellow {
		t.Fatalf("medium COP/EXEC color = %q, want yellow", got)
	}
	if got := copPerExecutionColor(1000); got != red+bold {
		t.Fatalf("high COP/EXEC color = %q, want red", got)
	}
	if got := eventRateColor(0, 10); got != green {
		t.Fatalf("zero event rate color = %q, want green", got)
	}
	if got := eventRateColor(1, 10); got != yellow {
		t.Fatalf("positive event rate color = %q, want yellow", got)
	}
	if got := eventRateColor(10, 10); got != red+bold {
		t.Fatalf("high event rate color = %q, want red", got)
	}
}

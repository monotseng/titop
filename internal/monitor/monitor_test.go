package monitor

import (
	"context"
	"strings"
	"testing"

	"titop/internal/prometheus"
)

type nodeQuerier struct{}

func (nodeQuerier) Query(_ context.Context, query string) ([]prometheus.Sample, error) {
	switch {
	case strings.Contains(query, `up{job=~"tidb|.*-tidb"}`) && strings.Contains(query, "by (instance)"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tidb-down:10080"}, Value: 0}}, nil
	case strings.Contains(query, `up{job=~"tikv|.*-tikv"}`) && strings.Contains(query, "by (instance)"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 1}}, nil
	case strings.Contains(query, "process_cpu_seconds_total"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180", "job": "tikv"}, Value: 2.5}}, nil
	case strings.Contains(query, "tikv_storage_command_total") && strings.Contains(query, "batch_get"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 123.25}}, nil
	case strings.Contains(query, "tikv_storage_command_total") && strings.Contains(query, "prewrite"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 45.5}}, nil
	case strings.Contains(query, "tidb_session_transaction_duration_seconds_count"):
		return []prometheus.Sample{{Value: 123.45}}, nil
	case strings.Contains(query, "tidb_tikvclient_request_seconds_count"):
		return []prometheus.Sample{{Metric: map[string]string{"type": "Get"}, Value: 100}}, nil
	case strings.Contains(query, "tidb_tikvclient_request_seconds_sum"):
		return []prometheus.Sample{{Metric: map[string]string{"type": "Get"}, Value: 2}}, nil
	case strings.Contains(query, "tidb_tikvclient_request_seconds_bucket"):
		return []prometheus.Sample{{Metric: map[string]string{"type": "Get"}, Value: 0.08}}, nil
	case strings.Contains(query, `name=~"unified_read_po.*"`):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 1.1}}, nil
	case strings.Contains(query, `name=~"raftstore_.*"`):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 2.2}}, nil
	case strings.Contains(query, `name=~"apply_.*"`):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 3.3}}, nil
	case strings.Contains(query, "grpc-server"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 4.4}}, nil
	case strings.Contains(query, "store-read"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 5.5}}, nil
	case strings.Contains(query, "gc-worker"):
		return []prometheus.Sample{{Metric: map[string]string{"instance": "tikv-up:20180"}, Value: 6.6}}, nil
	}
	return nil, nil
}

func TestPortRoleCannotBeOverwritten(t *testing.T) {
	row := &Instance{Name: "store-1:20180"}
	assignRole(row, "TIDB")
	if row.Role != "TIKV" {
		t.Fatalf("20180 must remain TIKV, got %s", row.Role)
	}
	assignRole(row, "PD")
	if row.Role != "TIKV" {
		t.Fatalf("role was overwritten: %s", row.Role)
	}
}

func TestHostOf(t *testing.T) {
	for input, want := range map[string]string{"10.0.0.1:20180": "10.0.0.1", "[2001:db8::1]:20180": "2001:db8::1", "db-1:9100": "db-1"} {
		if got := hostOf(input); got != want {
			t.Errorf("hostOf(%q)=%q, want %q", input, got, want)
		}
	}
}

func TestMetricVersion(t *testing.T) {
	if got := metricVersion(map[string]string{"release_version": "v8.5.3"}); got != "v8.5.3" {
		t.Fatalf("got %q", got)
	}
	if got := metricVersion(map[string]string{"version": "v8.5.4", "release_version": "old"}); got != "v8.5.4" {
		t.Fatalf("got %q", got)
	}
}

func TestNodesAreRoleAwareAndDownFirst(t *testing.T) {
	s := Collect(context.Background(), nodeQuerier{})
	if len(s.Instances) != 2 {
		t.Fatalf("expected 2 nodes, got %#v", s.Instances)
	}
	if s.Instances[0].Status != "DOWN" || s.Instances[0].Role != "TIDB" {
		t.Fatalf("down TiDB must sort first: %#v", s.Instances)
	}
	if s.Instances[1].Role != "TIKV" || s.Instances[1].CPU != 2.5 {
		t.Fatalf("unexpected TiKV node: %#v", s.Instances[1])
	}
	if !s.Instances[1].LogicalOPSAvailable || s.Instances[1].LogicalReads != 123.25 || s.Instances[1].LogicalWrites != 45.5 {
		t.Fatalf("unexpected TiKV logical OPS: %#v", s.Instances[1])
	}
	if s.Instances[0].LogicalOPSAvailable {
		t.Fatalf("TiDB must not display logical OPS: %#v", s.Instances[0])
	}
	if s.TPS != 123.45 {
		t.Fatalf("unexpected TPS: %v", s.TPS)
	}
	if len(s.Requests) != 1 || s.Requests[0].OPS != 100 || s.Requests[0].Average != 0.02 || s.Requests[0].P99 != 0.08 || s.Requests[0].Load != 2 {
		t.Fatalf("unexpected TiKV request stats: %#v", s.Requests)
	}
	if len(s.TiKVs) != 1 {
		t.Fatalf("expected one TiKV thread row, got %#v", s.TiKVs)
	}
	threads := s.TiKVs[0]
	if threads.UnifiedReadCPU != 1.1 || threads.RaftStoreCPU != 2.2 || threads.AsyncApplyCPU != 3.3 || threads.GRPCPollCPU != 4.4 || threads.StorageReadCPU != 5.5 || threads.GCWorkerCPU != 6.6 {
		t.Fatalf("unexpected TiKV thread CPU values: %#v", threads)
	}
}

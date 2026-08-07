package tidbsql

import (
	"math"
	"testing"
	"time"
)

func TestSchemaTrackerDeltaAndNewWindow(t *testing.T) {
	tracker := NewSchemaTracker()
	start := time.Date(2026, 8, 7, 10, 0, 0, 0, time.UTC)
	window := start.Add(-5 * time.Minute)
	baseline := []SchemaSample{{
		Instance: "tidb-1", Schema: "orders", WindowBegin: window,
		Counters: SchemaCounters{Executions: 100, Errors: 1, Latency: float64(10 * time.Second)},
	}}
	if got := tracker.Update(baseline, start); !got.Baseline || len(got.Rows) != 0 {
		t.Fatalf("first update = %#v, want baseline without rows", got)
	}

	next := []SchemaSample{{
		Instance: "tidb-1", Schema: "orders", WindowBegin: window,
		Counters: SchemaCounters{Executions: 120, Errors: 2, Latency: float64(14 * time.Second), ProcessedKeys: 2000},
	}}
	got := tracker.Update(next, start.Add(5*time.Second))
	if len(got.Rows) != 1 {
		t.Fatalf("delta rows = %d, want 1", len(got.Rows))
	}
	row := got.Rows[0]
	if row.QPS != 4 || row.Interval.Executions != 20 || row.Cumulative.Executions != 20 {
		t.Fatalf("unexpected execution delta: %#v", row)
	}
	if row.AverageLatency != 200*time.Millisecond || math.Abs(row.TimeLoad-.8) > .0001 {
		t.Fatalf("unexpected latency/load: %#v", row)
	}

	// A newly opened summary window contains only work performed since the
	// previous sample, so its full counters are included.
	newWindow := append(next, SchemaSample{
		Instance: "tidb-1", Schema: "orders", WindowBegin: start.Add(6 * time.Second),
		Counters: SchemaCounters{Executions: 5, Latency: float64(time.Second)},
	})
	got = tracker.Update(newWindow, start.Add(10*time.Second))
	if got.Rows[0].Cumulative.Executions != 25 {
		t.Fatalf("cumulative executions = %v, want 25", got.Rows[0].Cumulative.Executions)
	}
}

func TestSchemaTrackerCounterReset(t *testing.T) {
	tracker := NewSchemaTracker()
	at := time.Now()
	window := at.Add(-time.Minute)
	tracker.Update([]SchemaSample{{Instance: "tidb-1", Schema: "test", WindowBegin: window, Counters: SchemaCounters{Executions: 10}}}, at)
	got := tracker.Update([]SchemaSample{{Instance: "tidb-1", Schema: "test", WindowBegin: window, Counters: SchemaCounters{Executions: 1}}}, at.Add(time.Second))
	if !got.Reset || len(got.Rows) != 0 {
		t.Fatalf("reset update = %#v, want reset without a negative delta", got)
	}
}

func TestSchemaTrackerOrdersByTimeLoad(t *testing.T) {
	tracker := NewSchemaTracker()
	at := time.Now()
	window := at.Add(-time.Minute)
	base := []SchemaSample{
		{Instance: "tidb-1", Schema: "fast", WindowBegin: window, Counters: SchemaCounters{Executions: 1, Latency: 1}},
		{Instance: "tidb-1", Schema: "slow", WindowBegin: window, Counters: SchemaCounters{Executions: 1, Latency: 1}},
	}
	tracker.Update(base, at)
	next := []SchemaSample{
		{Instance: "tidb-1", Schema: "fast", WindowBegin: window, Counters: SchemaCounters{Executions: 101, Latency: float64(time.Second)}},
		{Instance: "tidb-1", Schema: "slow", WindowBegin: window, Counters: SchemaCounters{Executions: 2, Latency: float64(2 * time.Second)}},
	}
	got := tracker.Update(next, at.Add(time.Second))
	if len(got.Rows) != 2 || got.Rows[0].Schema != "slow" {
		t.Fatalf("rows = %#v, want slow schema first", got.Rows)
	}
}

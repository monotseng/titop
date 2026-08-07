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
		Counters: SchemaCounters{
			Executions: 100, WriteExecutions: 5, Errors: 1, Latency: float64(10 * time.Second),
			TotalKeys: 1000, ProcessedKeys: 800, CopTasks: 20, Backoffs: 2,
			WriteKeys: 100, WriteSize: 1000, TxnRetries: 1,
		},
	}}
	if got := tracker.Update(baseline, start); !got.Baseline || len(got.Rows) != 0 {
		t.Fatalf("first update = %#v, want baseline without rows", got)
	}

	next := []SchemaSample{{
		Instance: "tidb-1", Schema: "orders", WindowBegin: window,
		Counters: SchemaCounters{
			Executions: 120, WriteExecutions: 10, Errors: 2, Latency: float64(14 * time.Second),
			TotalKeys: 3000, ProcessedKeys: 2000, CopTasks: 50, Backoffs: 7,
			WriteKeys: 300, WriteSize: 5000, TxnRetries: 3,
		},
	}}
	got := tracker.Update(next, start.Add(5*time.Second))
	if len(got.Rows) != 1 {
		t.Fatalf("delta rows = %d, want 1", len(got.Rows))
	}
	row := got.Rows[0]
	if row.QPS != 4 || row.WriteQPS != 1 || row.Interval.Executions != 20 {
		t.Fatalf("unexpected execution delta: %#v", row)
	}
	if row.AverageLatency != 200*time.Millisecond || math.Abs(row.TimeLoad-.8) > .0001 {
		t.Fatalf("unexpected latency/load: %#v", row)
	}
	if row.Interval.TotalKeys != 2000 || row.Interval.CopTasks != 30 || row.Interval.Backoffs != 5 ||
		row.Interval.WriteSize != 4000 || row.Interval.TxnRetries != 2 {
		t.Fatalf("unexpected KV deltas: %#v", row.Interval)
	}

	// A newly opened summary window contains only work performed since the
	// previous sample, so its full counters are included.
	newWindow := append(next, SchemaSample{
		Instance: "tidb-1", Schema: "orders", WindowBegin: start.Add(6 * time.Second),
		Counters: SchemaCounters{Executions: 5, Latency: float64(time.Second)},
	})
	got = tracker.Update(newWindow, start.Add(10*time.Second))
	if got.Rows[0].Interval.Executions != 5 {
		t.Fatalf("new-window executions = %v, want 5", got.Rows[0].Interval.Executions)
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

func TestSchemaTrackerClampsRoundedAverageRegression(t *testing.T) {
	tracker := NewSchemaTracker()
	at := time.Now()
	window := at.Add(-time.Minute)
	tracker.Update([]SchemaSample{{
		Instance: "tidb-1", Schema: "test", WindowBegin: window,
		Counters: SchemaCounters{Executions: 10, TotalKeys: 100},
	}}, at)
	got := tracker.Update([]SchemaSample{{
		Instance: "tidb-1", Schema: "test", WindowBegin: window,
		Counters: SchemaCounters{Executions: 11, TotalKeys: 99},
	}}, at.Add(time.Second))
	if got.Reset || len(got.Rows) != 1 || got.Rows[0].Interval.TotalKeys != 0 {
		t.Fatalf("rounded AVG_* regression = %#v, want a clamped delta without reset", got)
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

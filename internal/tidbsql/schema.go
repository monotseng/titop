package tidbsql

import (
	"sort"
	"time"
)

type SchemaLoad struct {
	Schema               string
	QPS, TimeLoad        float64
	AverageLatency       time.Duration
	Interval, Cumulative SchemaCounters
}

type SchemaLoadSnapshot struct {
	Rows                 []SchemaLoad
	StartedAt, SampledAt time.Time
	Interval             time.Duration
	Baseline, Reset      bool
}

type schemaSampleKey struct {
	instance, schema string
	windowBegin      int64
}

// SchemaTracker converts cumulative TiDB summary counters into per-refresh
// deltas while retaining totals for the lifetime of this TiTop process.
type SchemaTracker struct {
	previous map[schemaSampleKey]SchemaCounters
	totals   map[string]SchemaCounters
	started  time.Time
	last     time.Time
}

func NewSchemaTracker() *SchemaTracker {
	return &SchemaTracker{
		previous: make(map[schemaSampleKey]SchemaCounters),
		totals:   make(map[string]SchemaCounters),
	}
}

func (t *SchemaTracker) LastSample() time.Time { return t.last }

func (t *SchemaTracker) Update(samples []SchemaSample, at time.Time) SchemaLoadSnapshot {
	result := SchemaLoadSnapshot{StartedAt: t.started, SampledAt: at}
	current := make(map[schemaSampleKey]SchemaCounters, len(samples))
	for _, sample := range samples {
		key := schemaSampleKey{sample.Instance, sample.Schema, sample.WindowBegin.UnixNano()}
		current[key] = sample.Counters
	}
	if t.last.IsZero() {
		t.previous, t.started, t.last = current, at, at
		result.StartedAt, result.Baseline = at, true
		return result
	}

	result.Interval = at.Sub(t.last)
	if result.Interval <= 0 {
		result.Interval = time.Second
	}
	deltas := make(map[string]SchemaCounters)
	for key, counters := range current {
		previous, found := t.previous[key]
		var delta SchemaCounters
		if !found {
			delta = counters
		} else if counters.Executions < previous.Executions {
			result.Reset = true
			continue
		} else {
			delta, result.Reset = counterDelta(counters, previous, result.Reset)
		}
		deltas[key.schema] = addCounters(deltas[key.schema], delta)
		t.totals[key.schema] = addCounters(t.totals[key.schema], delta)
	}
	t.previous, t.last = current, at
	result.StartedAt = t.started

	seconds := result.Interval.Seconds()
	for schema, delta := range deltas {
		if delta.Executions <= 0 && delta.Latency <= 0 {
			continue
		}
		row := SchemaLoad{Schema: schema, Interval: delta, Cumulative: t.totals[schema]}
		row.QPS = delta.Executions / seconds
		row.TimeLoad = delta.Latency / float64(time.Second) / seconds
		if delta.Executions > 0 {
			row.AverageLatency = time.Duration(delta.Latency / delta.Executions)
		}
		result.Rows = append(result.Rows, row)
	}
	sort.Slice(result.Rows, func(i, j int) bool {
		if result.Rows[i].TimeLoad == result.Rows[j].TimeLoad {
			return result.Rows[i].QPS > result.Rows[j].QPS
		}
		return result.Rows[i].TimeLoad > result.Rows[j].TimeLoad
	})
	return result
}

func counterDelta(current, previous SchemaCounters, reset bool) (SchemaCounters, bool) {
	values := [8][2]float64{
		{current.Executions, previous.Executions}, {current.Errors, previous.Errors},
		{current.Latency, previous.Latency}, {current.ProcessedKeys, previous.ProcessedKeys},
		{current.WriteKeys, previous.WriteKeys}, {current.AffectedRows, previous.AffectedRows},
		{current.Memory, previous.Memory}, {current.Disk, previous.Disk},
	}
	delta := make([]float64, len(values))
	for i, pair := range values {
		if pair[0] < pair[1] {
			reset = true
			continue
		}
		delta[i] = pair[0] - pair[1]
	}
	return SchemaCounters{delta[0], delta[1], delta[2], delta[3], delta[4], delta[5], delta[6], delta[7]}, reset
}

func addCounters(a, b SchemaCounters) SchemaCounters {
	return SchemaCounters{
		a.Executions + b.Executions, a.Errors + b.Errors, a.Latency + b.Latency,
		a.ProcessedKeys + b.ProcessedKeys, a.WriteKeys + b.WriteKeys, a.AffectedRows + b.AffectedRows,
		a.Memory + b.Memory, a.Disk + b.Disk,
	}
}

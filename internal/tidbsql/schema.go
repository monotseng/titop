package tidbsql

import (
	"sort"
	"time"
)

type SchemaLoad struct {
	Schema                  string
	QPS, WriteQPS, TimeLoad float64
	ErrorRate               float64
	AverageLatency          time.Duration
	Interval                SchemaCounters
}

type SchemaLoadSnapshot struct {
	Rows            []SchemaLoad
	Interval        time.Duration
	Baseline, Reset bool
}

type schemaSampleKey struct {
	instance, schema string
	windowBegin      int64
}

// SchemaTracker converts cumulative TiDB summary counters into per-refresh
// deltas and rates.
type SchemaTracker struct {
	previous map[schemaSampleKey]SchemaCounters
	last     time.Time
}

func NewSchemaTracker() *SchemaTracker {
	return &SchemaTracker{
		previous: make(map[schemaSampleKey]SchemaCounters),
	}
}

func (t *SchemaTracker) LastSample() time.Time { return t.last }

func (t *SchemaTracker) Update(samples []SchemaSample, at time.Time) SchemaLoadSnapshot {
	result := SchemaLoadSnapshot{}
	current := make(map[schemaSampleKey]SchemaCounters, len(samples))
	for _, sample := range samples {
		key := schemaSampleKey{sample.Instance, sample.Schema, sample.WindowBegin.UnixNano()}
		current[key] = sample.Counters
	}
	if t.last.IsZero() {
		t.previous, t.last = current, at
		result.Baseline = true
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
	}
	t.previous, t.last = current, at

	seconds := result.Interval.Seconds()
	for schema, delta := range deltas {
		if delta.Executions <= 0 && delta.Latency <= 0 {
			continue
		}
		row := SchemaLoad{Schema: schema, Interval: delta}
		row.QPS = delta.Executions / seconds
		row.WriteQPS = delta.WriteExecutions / seconds
		row.TimeLoad = delta.Latency / float64(time.Second) / seconds
		if delta.Executions > 0 {
			row.AverageLatency = time.Duration(delta.Latency / delta.Executions)
			row.ErrorRate = delta.Errors / delta.Executions
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
	values := [14][2]float64{
		{current.Executions, previous.Executions}, {current.Errors, previous.Errors},
		{current.WriteExecutions, previous.WriteExecutions}, {current.Latency, previous.Latency},
		{current.TotalKeys, previous.TotalKeys}, {current.ProcessedKeys, previous.ProcessedKeys},
		{current.CopTasks, previous.CopTasks}, {current.Backoffs, previous.Backoffs},
		{current.WriteKeys, previous.WriteKeys}, {current.WriteSize, previous.WriteSize},
		{current.TxnRetries, previous.TxnRetries}, {current.AffectedRows, previous.AffectedRows},
		{current.Memory, previous.Memory}, {current.Disk, previous.Disk},
	}
	delta := make([]float64, len(values))
	for i, pair := range values {
		if pair[0] < pair[1] {
			// EXEC_COUNT, SUM_ERRORS, SUM_LATENCY, SUM_COP_TASK_NUM and
			// SUM_BACKOFF_TIMES are true cumulative counters. Values derived
			// from AVG_* times EXEC_COUNT can move slightly backwards because
			// TiDB exposes rounded averages; clamp those without reporting a
			// summary reset.
			if i == 0 || i == 1 || i == 2 || i == 3 || i == 6 || i == 7 {
				reset = true
			}
			continue
		}
		delta[i] = pair[0] - pair[1]
	}
	return SchemaCounters{
		Executions: delta[0], Errors: delta[1], WriteExecutions: delta[2], Latency: delta[3],
		TotalKeys: delta[4], ProcessedKeys: delta[5], CopTasks: delta[6], Backoffs: delta[7],
		WriteKeys: delta[8], WriteSize: delta[9], TxnRetries: delta[10],
		AffectedRows: delta[11], Memory: delta[12], Disk: delta[13],
	}, reset
}

func addCounters(a, b SchemaCounters) SchemaCounters {
	return SchemaCounters{
		Executions: a.Executions + b.Executions, WriteExecutions: a.WriteExecutions + b.WriteExecutions,
		Errors: a.Errors + b.Errors, Latency: a.Latency + b.Latency,
		TotalKeys: a.TotalKeys + b.TotalKeys, ProcessedKeys: a.ProcessedKeys + b.ProcessedKeys,
		CopTasks: a.CopTasks + b.CopTasks, Backoffs: a.Backoffs + b.Backoffs,
		WriteKeys: a.WriteKeys + b.WriteKeys, WriteSize: a.WriteSize + b.WriteSize,
		TxnRetries:   a.TxnRetries + b.TxnRetries,
		AffectedRows: a.AffectedRows + b.AffectedRows, Memory: a.Memory + b.Memory, Disk: a.Disk + b.Disk,
	}
}

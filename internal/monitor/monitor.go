package monitor

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"titop/internal/prometheus"
)

type Querier interface {
	Query(context.Context, string) ([]prometheus.Sample, error)
}

type Instance struct {
	Name, Role, Status, Version                                                            string
	QPS, Connections, Active, CPU, Memory, HostMemory, Uptime, LogicalReads, LogicalWrites float64
	LogicalOPSAvailable                                                                    bool
}
type TiKVInstance struct {
	Name, Status                                             string
	UnifiedReadCPU, RaftStoreCPU, AsyncApplyCPU, GRPCPollCPU float64
	StorageReadCPU, GCWorkerCPU                              float64
}
type Activity struct {
	Name  string
	Value float64
}
type RequestStat struct {
	Name                    string
	OPS, Average, P99, Load float64
}
type Snapshot struct {
	ClusterName                                   string
	At                                            time.Time
	QPS, TPS, P99, Connections, Active, ErrorRate float64
	TiDBUp, TiKVUp, PDUp                          int
	Instances                                     []Instance
	TiKVs                                         []TiKVInstance
	SQLTypes                                      []Activity
	Requests                                      []RequestStat
	Errors                                        []string
}

type metricDef struct{ name, query string }

var clusterQueries = []metricDef{
	{"qps", `sum(rate(tidb_executor_statement_total[1m]))`},
	{"tps", `sum(rate(tidb_session_transaction_duration_seconds_count{sql_type!="internal"}[1m]))`},
	{"p99", `histogram_quantile(0.99, sum(rate(tidb_server_handle_query_duration_seconds_bucket[5m])) by (le))`},
	{"connections", `sum(tidb_server_connections)`},
	{"active", `sum(tidb_server_tokens)`},
	{"errors", `sum(rate(tidb_server_execute_error_total[1m]))`},
	{"tidb_up", `sum(up{job=~"tidb|.*-tidb"})`},
	{"tikv_up", `sum(up{job=~"tikv|.*-tikv"})`},
	{"pd_up", `sum(up{job=~"pd|.*-pd"})`},
}

var instanceQueries = []metricDef{
	{"status_tidb", `max by (instance) (up{job=~"tidb|.*-tidb"})`},
	{"status_tikv", `max by (instance) (up{job=~"tikv|.*-tikv"})`},
	{"status_pd", `max by (instance) (up{job=~"pd|.*-pd"})`},
	{"qps", `sum by (instance) (rate(tidb_executor_statement_total[1m]))`},
	{"connections", `sum by (instance) (tidb_server_connections)`},
	{"active", `sum by (instance) (tidb_server_tokens)`},
	{"cpu", `sum by (instance, job) (rate(process_cpu_seconds_total{job=~"tidb|tikv|pd|.*-(tidb|tikv|pd)"}[1m]))`},
	{"memory", `sum by (instance, job) (process_resident_memory_bytes{job=~"tidb|tikv|pd|.*-(tidb|tikv|pd)"})`},
	{"uptime", `max by (instance, job) (time() - process_start_time_seconds{job=~"tidb|tikv|pd|.*-(tidb|tikv|pd)"})`},
	{"version_tidb", `max by (instance, version, release_version, build_version, git_version) (tidb_server_info)`},
	{"version_tikv", `max by (instance, version, release_version, build_version, git_version) (tikv_server_info)`},
	{"version_pd", `max by (instance, version, release_version, build_version, git_version) (pd_server_info)`},
	{"logical_reads", `sum by (instance) (rate(tikv_storage_command_total{type=~"get|batch_get|scan|raw_get|raw_batch_get|raw_scan"}[1m]))`},
	{"logical_writes", `sum by (instance) (rate(tikv_storage_command_total{type=~"prewrite|commit|cleanup|rollback|pessimistic_lock|pessimistic_rollback|resolve_lock|raw_put|raw_batch_put|raw_delete|raw_batch_delete|raw_delete_range|delete_range|ingest_sst"}[1m]))`},
}

var tikvQueries = []metricDef{
	{"status", `max by (instance) (up{job=~"tikv|.*-tikv"})`},
	{"unified_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"unified_read_po.*"}[1m])) by (instance)`},
	{"raftstore_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"raftstore_.*"}[1m])) by (instance)`},
	{"apply_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"apply_.*"}[1m])) by (instance)`},
	{"grpc_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"grpc-server-.*|grpc_server_.*|grpc-poll.*"}[1m])) by (instance)`},
	{"storage_read_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"store-read-.*|store_read_.*|storage-read-.*"}[1m])) by (instance)`},
	{"gc_worker_cpu", `sum(rate(tikv_thread_cpu_seconds_total{name=~"gc-worker-.*|gc_worker_.*"}[1m])) by (instance)`},
}

var hostQueries = []metricDef{
	{"host_memory", `max by (instance) (node_memory_MemTotal_bytes)`},
}

var activityQueries = []metricDef{
	{"sql", `sum by (type) (rate(tidb_executor_statement_total[1m]))`},
	{"request_ops", `sum by (type) (rate(tidb_tikvclient_request_seconds_count[1m]))`},
	{"request_load", `sum by (type) (rate(tidb_tikvclient_request_seconds_sum[1m]))`},
	{"request_p99", `histogram_quantile(0.99, sum by (type, le) (rate(tidb_tikvclient_request_seconds_bucket[5m])))`},
}

func Collect(ctx context.Context, q Querier) Snapshot {
	s := Snapshot{At: time.Now()}
	type result struct {
		def     metricDef
		samples []prometheus.Sample
		err     error
		kind    string
	}
	ch := make(chan result, len(clusterQueries)+len(instanceQueries)+len(tikvQueries)+len(hostQueries)+len(activityQueries))
	var wg sync.WaitGroup
	for _, group := range []struct {
		defs []metricDef
		kind string
	}{{clusterQueries, "cluster"}, {instanceQueries, "instance"}, {tikvQueries, "tikv"}, {hostQueries, "host"}, {activityQueries, "activity"}} {
		for _, d := range group.defs {
			wg.Add(1)
			go func(d metricDef, kind string) {
				defer wg.Done()
				v, e := q.Query(ctx, d.query)
				ch <- result{d, v, e, kind}
			}(d, group.kind)
		}
	}
	go func() { wg.Wait(); close(ch) }()
	instances := map[string]*Instance{}
	tikvs := map[string]*TiKVInstance{}
	hostMemory := map[string]float64{}
	requests := map[string]*RequestStat{}
	for r := range ch {
		if r.err != nil {
			s.Errors = append(s.Errors, r.def.name+": "+r.err.Error())
			continue
		}
		if r.kind == "instance" {
			for _, sample := range r.samples {
				name := sample.Metric["instance"]
				if name == "" {
					name = "unknown"
				}
				row := instances[name]
				if row == nil {
					row = &Instance{Name: name}
					instances[name] = row
				}
				switch r.def.name {
				case "status_tidb", "status_tikv", "status_pd":
					if sample.Value >= 1 {
						row.Status = "UP"
					} else {
						row.Status = "DOWN"
					}
					assignRole(row, strings.ToUpper(strings.TrimPrefix(r.def.name, "status_")))
				case "qps":
					row.QPS = sample.Value
				case "connections":
					row.Connections = sample.Value
				case "active":
					row.Active = sample.Value
				case "cpu":
					row.CPU = sample.Value
					assignRole(row, roleFromJob(sample.Metric["job"]))
				case "memory":
					row.Memory = sample.Value
					assignRole(row, roleFromJob(sample.Metric["job"]))
				case "uptime":
					row.Uptime = sample.Value
					assignRole(row, roleFromJob(sample.Metric["job"]))
				case "version_tidb", "version_tikv", "version_pd":
					row.Version = metricVersion(sample.Metric)
					assignRole(row, strings.ToUpper(strings.TrimPrefix(r.def.name, "version_")))
				case "logical_reads":
					row.LogicalReads = sample.Value
					row.LogicalOPSAvailable = true
					assignRole(row, "TIKV")
				case "logical_writes":
					row.LogicalWrites = sample.Value
					row.LogicalOPSAvailable = true
					assignRole(row, "TIKV")
				}
			}
			continue
		}
		if r.kind == "tikv" {
			for _, sample := range r.samples {
				name := sample.Metric["instance"]
				if name == "" {
					name = "unknown"
				}
				row := tikvs[name]
				if row == nil {
					row = &TiKVInstance{Name: name, Status: "UNKNOWN"}
					tikvs[name] = row
				}
				if r.def.name == "status" {
					if sample.Value >= 1 {
						row.Status = "UP"
					} else {
						row.Status = "DOWN"
					}
				} else {
					switch r.def.name {
					case "unified_cpu":
						row.UnifiedReadCPU = sample.Value
					case "raftstore_cpu":
						row.RaftStoreCPU = sample.Value
					case "apply_cpu":
						row.AsyncApplyCPU = sample.Value
					case "grpc_cpu":
						row.GRPCPollCPU = sample.Value
					case "storage_read_cpu":
						row.StorageReadCPU = sample.Value
					case "gc_worker_cpu":
						row.GCWorkerCPU = sample.Value
					}
				}
			}
			continue
		}
		if r.kind == "host" {
			for _, sample := range r.samples {
				hostMemory[hostOf(sample.Metric["instance"])] = sample.Value
			}
			continue
		}
		if r.kind == "activity" {
			for _, sample := range r.samples {
				name := sample.Metric["type"]
				if name == "" {
					name = "unknown"
				}
				if r.def.name == "sql" {
					s.SQLTypes = append(s.SQLTypes, Activity{Name: name, Value: sample.Value})
					continue
				}
				row := requests[name]
				if row == nil {
					row = &RequestStat{Name: name}
					requests[name] = row
				}
				switch r.def.name {
				case "request_ops":
					row.OPS = sample.Value
				case "request_load":
					row.Load = sample.Value
				case "request_p99":
					row.P99 = sample.Value
				}
			}
			continue
		}
		v := 0.0
		if len(r.samples) > 0 {
			v = r.samples[0].Value
		}
		switch r.def.name {
		case "qps":
			s.QPS = v
		case "tps":
			s.TPS = v
		case "p99":
			s.P99 = v
		case "connections":
			s.Connections = v
		case "active":
			s.Active = v
		case "errors":
			s.ErrorRate = v
		case "tidb_up":
			s.TiDBUp = int(v)
		case "tikv_up":
			s.TiKVUp = int(v)
		case "pd_up":
			s.PDUp = int(v)
		}
	}
	for _, row := range instances {
		if row.Status == "" {
			row.Status = "UNKNOWN"
		}
		if row.Role == "" {
			assignRole(row, "UNKNOWN")
		}
		row.HostMemory = hostMemory[hostOf(row.Name)]
		s.Instances = append(s.Instances, *row)
	}
	for _, row := range tikvs {
		s.TiKVs = append(s.TiKVs, *row)
	}
	for _, row := range requests {
		if row.OPS > 0 {
			row.Average = row.Load / row.OPS
		}
		s.Requests = append(s.Requests, *row)
	}
	sort.Slice(s.Instances, func(i, j int) bool {
		if downRank(s.Instances[i].Status) != downRank(s.Instances[j].Status) {
			return downRank(s.Instances[i].Status) < downRank(s.Instances[j].Status)
		}
		if s.Instances[i].CPU != s.Instances[j].CPU {
			return s.Instances[i].CPU > s.Instances[j].CPU
		}
		return s.Instances[i].Name < s.Instances[j].Name
	})
	sort.Slice(s.TiKVs, func(i, j int) bool {
		if downRank(s.TiKVs[i].Status) != downRank(s.TiKVs[j].Status) {
			return downRank(s.TiKVs[i].Status) < downRank(s.TiKVs[j].Status)
		}
		if totalThreadCPU(s.TiKVs[i]) != totalThreadCPU(s.TiKVs[j]) {
			return totalThreadCPU(s.TiKVs[i]) > totalThreadCPU(s.TiKVs[j])
		}
		return s.TiKVs[i].Name < s.TiKVs[j].Name
	})
	sort.Slice(s.SQLTypes, func(i, j int) bool { return s.SQLTypes[i].Value > s.SQLTypes[j].Value })
	sort.Slice(s.Requests, func(i, j int) bool {
		if s.Requests[i].Load != s.Requests[j].Load {
			return s.Requests[i].Load > s.Requests[j].Load
		}
		return s.Requests[i].Name < s.Requests[j].Name
	})
	sort.Strings(s.Errors)
	return s
}

func totalThreadCPU(row TiKVInstance) float64 {
	return row.UnifiedReadCPU + row.RaftStoreCPU + row.AsyncApplyCPU + row.GRPCPollCPU + row.StorageReadCPU + row.GCWorkerCPU
}

func downRank(status string) int {
	if status == "DOWN" {
		return 0
	}
	return 1
}
func roleFromJob(job string) string {
	job = strings.ToLower(job)
	if strings.Contains(job, "tikv") {
		return "TIKV"
	}
	if strings.Contains(job, "pd") {
		return "PD"
	}
	if strings.Contains(job, "tidb") {
		return "TIDB"
	}
	return "UNKNOWN"
}

func roleFromInstance(instance string) string {
	if i := strings.LastIndexByte(instance, ':'); i >= 0 {
		switch instance[i+1:] {
		case "10080":
			return "TIDB"
		case "20180":
			return "TIKV"
		case "2379":
			return "PD"
		}
	}
	return ""
}

func assignRole(row *Instance, candidate string) {
	if fixed := roleFromInstance(row.Name); fixed != "" {
		row.Role = fixed
		return
	}
	if row.Role == "" || row.Role == "UNKNOWN" {
		row.Role = candidate
	}
}

func metricVersion(labels map[string]string) string {
	for _, name := range []string{"version", "release_version", "build_version", "git_version"} {
		if value := strings.TrimSpace(labels[name]); value != "" {
			return value
		}
	}
	return ""
}

func hostOf(instance string) string {
	if host, _, err := net.SplitHostPort(instance); err == nil {
		return strings.Trim(host, "[]")
	}
	if i := strings.LastIndexByte(instance, ':'); i > 0 {
		return strings.Trim(instance[:i], "[]")
	}
	return strings.Trim(instance, "[]")
}

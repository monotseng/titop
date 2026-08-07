# TiTop

**English** | [简体中文](README.md)

TiTop is a lightweight, near-real-time terminal monitor for TiDB clusters, inspired by the workflow of `oratop`. It queries Prometheus directly and can optionally connect to a TiDB SQL endpoint to provide active-session, long-transaction, and cluster TLS diagnostics.

Prometheus is the only required data source. TiDB credentials are optional and are used exclusively for SQL-enhanced diagnostics.

> [!CAUTION]
> **The code for this tool was generated with AI assistance.** AI-generated code can contain defects, incomplete compatibility handling, or undiscovered security risks. Users must review the code, test it thoroughly in a non-production environment, and independently assess whether it is suitable for their systems and production use. Users assume all performance, security, data, and availability risks arising from running TiTop. When connecting to TiDB, use a dedicated least-privilege read-only monitoring account; never use `root` or another highly privileged account.

## Features

- Cluster activity: QPS, TPS, P99 latency, connections, active sessions, errors, and TiDB/TiKV/PD availability.
- Current diagnostics: long-running queries, long transactions, and inter-component TLS status.
- Node view: role, status, version, uptime, QPS, connections, CPU, RSS, host memory, and TiKV logical read/write OPS.
- TiKV request view: OPS, average latency, P99 latency, and accumulated time load per second.
- TiKV thread pools: Unified Read, Raft Store, Async Apply, gRPC Poll, Storage Read, and GC Worker CPU.
- SQL type view: DML/transaction, DDL, administration, and other statements in four columns.
- Active SQL sessions: instance, connection ID, compact `DIGEST_ID`, user, host, database, command, state, elapsed time, recent summary statistics, memory, disk spill, and SQL text.
- Interactive operation: page switching, pause/resume, immediate refresh, plain output, and one-shot snapshots.

## Requirements

- Linux for interactive TTY mode. Other platforms can use `--once` or `--plain`.
- Go 1.23 or newer when building from source.
- Network access to the Prometheus HTTP API.
- Optional network access to TiDB SQL port `4000` when `-u/-p` is enabled.

For SQL-enhanced diagnostics, TiTop takes the hosts of UP TiDB instances returned by Prometheus, replaces their status port with the standard SQL port `4000`, and connects to the first reachable address. Non-standard TiDB SQL ports are not currently supported.

## Quick Start

### Prometheus only

```bash
./titop --prometheus 10.0.0.10:9090
```

The scheme defaults to HTTP when omitted. These commands are equivalent:

```bash
./titop -m 10.0.0.10:9090
./titop -m http://10.0.0.10:9090
```

Optionally prepend a display name for the cluster:

```bash
./titop -m production@http://10.0.0.10:9090
```

When omitted, the cluster name is displayed as `unknown`. The name is display-only; it is not converted into a Prometheus `tidb_cluster` label filter. Use a Prometheus endpoint whose data belongs to the intended cluster.

### SQL-enhanced diagnostics

```bash
./titop -m production@10.0.0.10:9090 -u monitor -p 'password'
```

Environment variables are recommended to keep credentials out of shell history and process arguments:

```bash
export TITOP_PROMETHEUS=production@http://10.0.0.10:9090
export TITOP_MYSQL_USER=monitor
export TITOP_MYSQL_PASSWORD='password'
./titop
```

`-u` and `-p` must be supplied together. The monitoring account must be able to read:

- `INFORMATION_SCHEMA.CLUSTER_PROCESSLIST`
- `INFORMATION_SCHEMA.CLUSTER_STATEMENTS_SUMMARY`
- `INFORMATION_SCHEMA.CLUSTER_TIDB_TRX`
- `INFORMATION_SCHEMA.CLUSTER_CONFIG`

> [!WARNING]
> **Do not run TiTop as `root` or with another highly privileged administrative account.** Create a dedicated read-only monitoring account so that exposed credentials, accidental operations, or a compromised runtime environment cannot put the entire cluster at risk.

Exact privileges vary by TiDB version and cluster security policy. Follow the principle of least privilege and grant only the read access required for the diagnostic system tables listed above. Do not grant `SUPER`, DDL, DML, user-management, or grant-management privileges. Where possible, restrict the account to trusted source addresses, use a unique strong password, and rotate it regularly. After provisioning the account, verify that it can read the required monitoring data but cannot modify application data or cluster configuration.

## Command-line Options

| Option | Default | Description |
| --- | ---: | --- |
| `--prometheus` | `TITOP_PROMETHEUS` | `[cluster@]Prometheus-address`, as a URL or `IP:port` |
| `-m` | same | Shorthand for `--prometheus` |
| `-u` | `TITOP_MYSQL_USER` | TiDB SQL user |
| `-p` | `TITOP_MYSQL_PASSWORD` | TiDB SQL password |
| `--interval` | `5s` | Refresh interval; minimum 1 second |
| `--timeout` | `4s` | Timeout for one Prometheus/SQL collection cycle |
| `--long-query-threshold` | `10s` | Long-running query threshold |
| `--long-txn-threshold` | `1m` | Long transaction threshold |
| `--high-qps` | `50000` | High-QPS threshold |
| `--high-tps` | `5000` | High-TPS threshold |
| `--once` | `false` | Print one snapshot and exit |
| `--plain` | `false` | Do not clear the screen or read interactive keys |
| `--no-color` | `false` | Disable ANSI colors |
| `--version` | - | Print the version and exit |

Set `NO_COLOR=1` to disable colors as well.

## Interactive Keys

| Key | Action |
| --- | --- |
| `i` | All TiDB, TiKV, and PD nodes |
| `k` | TiKV thread-pool CPU |
| `s` | SQL types and active sessions |
| `l` | Schema Load performance view |
| `o` | Toggle the Schema Overview and KV subviews |
| `w` | TiKV request latency/load |
| `p` | Pause or resume automatic refresh |
| `Space` | Refresh immediately |
| `h` / `?` | Toggle help |
| `q` | Quit |

## Views and Metrics

### Cluster Activity

The first line shows cluster throughput, latency, connections, errors, and component availability. `QPS(1m)` and `TPS(1m)` are Prometheus rates smoothed over the latest minute; they use a different window from the short-interval Schema Load QPS shown by its actual `INTERVAL`, so the values are not expected to match exactly. The second line contains SQL-backed diagnostics:

- `LONG QUERY`: non-Sleep rows in `CLUSTER_PROCESSLIST` whose elapsed time reaches the configured threshold.
- `LONG TXN`: rows in `CLUSTER_TIDB_TRX` whose transaction duration reaches the configured threshold.
- `TLS`: inter-component TLS configuration derived from `CLUSTER_CONFIG` for every TiDB, TiKV, and PD instance.

TLS status values:

| Status | Color | Meaning |
| --- | --- | --- |
| `ON` | Green | CA, certificate, and private key are configured on all TiDB, TiKV, and PD instances |
| `OFF` | Red | All relevant settings are empty |
| `INCONSISTENT` | Yellow | Configuration is incomplete or differs between components/instances |
| `UNKNOWN` | Yellow | The query succeeded but returned insufficient configuration data |
| `N/A` | Default | SQL credentials were not supplied, connection failed, or the query failed |

### Cluster Nodes

Nodes are ordered with DOWN instances first, then by descending CPU. TiKV `LREAD/s` and `LWRITE/s` values come from `tikv_storage_command_total`; they represent logical storage command rates, not physical disk IOPS.

CPU is expressed relative to one core, so a multithreaded process can exceed 100%. RSS/HOST% depends on node_exporter host-memory metrics and displays `-` when no host can be matched.

### TiKV Thread-pool CPU

TiTop uses explicit TiDB Dashboard-style PromQL over `tikv_thread_cpu_seconds_total`. Unified Read, for example, uses:

```promql
sum(rate(tikv_thread_cpu_seconds_total{name=~"unified_read_po.*"}[1m])) by (instance)
```

Values represent aggregate CPU-core consumption by the pool, not host CPU utilization: `100%` is approximately one fully occupied core, while `400%` is approximately four cores.

### SQL Types

SQL type QPS comes from `tidb_executor_statement_total` and is grouped into four columns:

- DML / TXN
- DDL
- ADMIN
- OTHER

The SQL page omits the repeated TOP 5 TiKV Request panel to leave more space for SQL diagnostics.

### Active SQL Sessions

With SQL credentials configured, TiTop queries `CLUSTER_PROCESSLIST` and joins `CLUSTER_STATEMENTS_SUMMARY` by instance and digest over the most recent ten minutes. Up to 30 non-Sleep sessions are shown in descending elapsed-time order.

`DIGEST_ID` is a stable 13-character compact identifier derived from the TiDB digest. It provides an Oracle SQL ID-like operator experience, but is not compatible or interchangeable with Oracle SQL ID.

The connection ID is highlighted in red when session memory reaches 100 MiB or disk spill reaches 1 GiB. TiDB can occasionally expose an unsigned-underflow MEM/DISK value; TiTop treats values outside the valid signed range as zero instead of failing the entire page.

### Schema Load

With SQL credentials configured, press `l` to open the Schema Load view. TiTop reads cumulative counters from `CLUSTER_STATEMENTS_SUMMARY` across all TiDB instances and uses `CLUSTER_STATEMENTS_SUMMARY_HISTORY` to bridge summary refresh boundaries. It aggregates by instance, schema, and summary window, calculates interval deltas between consecutive snapshots, and displays the top 20 schemas ordered by `TIME LOAD`. An empty schema remains displayed as `(none)`, meaning that no default schema was selected when the SQL statement ran.

Press `o` to switch between two subviews:

- `SCHEMA LOAD / OVERVIEW` shows overall QPS, write QPS, latency, time load, errors, keys, affected rows, and resource consumption, ordered by `TIME LOAD`.
- `SCHEMA LOAD / KV` shows `TOTAL KEYS/s`, `PROC KEYS/s`, `MVCC AMP`, `COP TASK/s`, `COP/EXEC`, `BACKOFF/s`, `WRITE KEYS/s`, `WRITE SIZE/s`, and `TXN RETRY/s`, ordered by `TOTAL KEYS/s`.

`MVCC AMP` is the interval ratio `TOTAL KEYS / PROC KEYS` and indicates MVCC read amplification. `COP/EXEC` is the average number of Coprocessor tasks generated per statement execution.

KV subview colors use these thresholds: `MVCC AMP` is yellow at `2x` and red at `10x`; `COP/EXEC` is yellow at `100` and red at `1000`; positive `BACKOFF/s` and `TXN RETRY/s` values are yellow and become red at `10/s` and `1/s`, respectively. Other throughput metrics are green by default. These thresholds are visual hints rather than an alerting policy.

- Every metric uses the latest successful sample period shown as `INTERVAL` in the title; no value accumulates for the lifetime of the TiTop process.
- `QPS` is the rate of all statements. `WRITE QPS` is the execution rate of `INSERT`, `UPDATE`, `DELETE`, and `REPLACE`, which is more reliable than an approximate TPS attributed by the default schema of `COMMIT` statements.
- `AVG LAT` is the interval-weighted average latency. `TIME LOAD` is total schema execution time divided by interval duration; `1.00s/s` is approximately one continuously consumed execution-time core.
- `ERR/s` and `ERR%` are the error rate per second and the interval error percentage. `PROC KEYS/s`, `WRITE KEYS/s`, `AFFECT ROWS/s`, `MEM/s`, and `DISK/s` are interval deltas divided by the actual sample duration.
- The first collection establishes a baseline, so load appears after the next refresh. Counter rollback, TiDB restart, or statement-summary reset never produces a negative delta and is reported as `RESET DETECTED`.
- `MEM/s` and `DISK/s` multiply statement-summary average per-execution usage by interval executions and divide by duration. They represent SQL resource-consumption rates, not current resident memory or disk occupancy.

Statement digests can be evicted because of `tidb_stmt_summary_max_stmt_count`, so this view is intended for real-time performance observation rather than audit-grade accounting.

## Colors and Thresholds

- P99 latency: yellow at 200 ms, red at 1 second.
- Node CPU: yellow at 70%, red at 90%.
- QPS/TPS: yellow at the configured high-load threshold; red when high throughput coincides with P99 ≥ 1 second, execution errors, or any TiDB CPU ≥ 90%.
- Long queries/transactions: red when the current count is greater than zero.
- Node status: UP is green, DOWN is red, and unknown is yellow.
- Active-session connection ID: red when memory or disk-spill thresholds are reached.

These colors are visual hints, not a replacement for a production alerting policy such as Prometheus Alertmanager.

## Build and Test

```bash
make test
make vet
make build
```

The native binary is written to `bin/titop`. The repository includes GitHub Actions CI for formatting, tests, `go vet`, and build verification.

Build Linux ARM64 and AMD64 releases:

```bash
make release
```

Outputs:

```text
release/titop-linux-arm64
release/titop-linux-amd64
release/titop                 # ARM64 compatibility copy
```

Override the embedded version:

```bash
make release VERSION=0.9.0
```

## One-shot and Scripted Output

```bash
./titop -m 10.0.0.10:9090 --once --no-color
./titop -m 10.0.0.10:9090 --plain --no-color
```

`--once` exits non-zero when Prometheus queries fail. SQL-enhanced query failures are shown as `N/A` or an error in the relevant view without preventing Prometheus-backed views from working.

## Known Limitations

- Prometheus Basic Auth, Bearer Token, client certificates, and custom CAs are not supported yet.
- SQL-enhanced diagnostics always try TiDB port `4000`; there is no custom SQL port option yet.
- The TiDB SQL client has no TLS/custom-CA options yet.
- The supplied cluster name is display-only and does not add a Prometheus label filter.
- Individual metrics or system tables can differ between TiDB/TiKV versions.
- Wide tables require a wide terminal; long fields are truncated to preserve column alignment.

## Troubleshooting

### Invalid Prometheus URL

Verify the host and port. `IP:port` is automatically prefixed with `http://`; HTTPS must be explicitly specified.

### SQL view reports a connection error

Verify that Prometheus returns at least one UP TiDB instance, the TiTop host can reach port `4000`, credentials and diagnostic privileges are correct, and the cluster does not require SQL TLS that the client has not been configured to use.

### Metrics are zero or WARN is displayed

Run the corresponding PromQL directly in Prometheus and verify that metric names and labels match the deployed TiDB/TiKV version. Independent query failures do not discard the rest of a snapshot.

### Colors or layout look wrong

Use `--no-color` to rule out ANSI rendering issues and increase terminal width. For redirected output, use `--plain --no-color`.

## Project Layout

```text
cmd/titop/             CLI, event loop, and terminal rendering
internal/monitor/      PromQL definitions, concurrent collection, and snapshot aggregation
internal/prometheus/   Prometheus HTTP API client
internal/tidbsql/      TiDB SQL connection and diagnostic queries
internal/terminal/     TTY raw mode and terminal width
```

## Differences from oratop

Oracle ASH and wait events do not map directly to TiDB Prometheus metrics. TiTop uses accumulated TiKV request time as an approximation of wait pressure and, when SQL credentials are supplied, uses TiDB cluster system tables for active SQL and transaction state.

## Roadmap

- Custom TiDB SQL port and TLS options.
- Prometheus Basic Auth, Bearer Token, mTLS, and custom CA support.
- Filtering shared Prometheus instances by the `tidb_cluster` label.
- TiKV hotspot, Region, Raft, disk, and PD scheduling views.
- Interactive sorting/filtering and configurable session-resource thresholds.

## License

TiTop is licensed under the [Apache License 2.0](LICENSE). See [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md) for dependency license information.

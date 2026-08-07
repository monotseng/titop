package tidbsql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"titop/internal/monitor"
)

// Session describes a currently executing statement and its recent digest summary.
type Session struct {
	Instance, User, Host, Database, Command, State, SQL, Digest string
	ID, Seconds, ExecCount, Memory, Disk                        int64
	AverageLatency                                              time.Duration
}

// SchemaSample is a cumulative statement-summary snapshot for one TiDB
// instance, schema, and summary window. Average fields are multiplied by the
// execution count in SQL so every value can be differenced and aggregated.
type SchemaSample struct {
	Instance, Schema string
	WindowBegin      time.Time
	Counters         SchemaCounters
}

type SchemaCounters struct {
	Executions      float64
	WriteExecutions float64
	Errors          float64
	Latency         float64
	TotalKeys       float64
	ProcessedKeys   float64
	CopTasks        float64
	Backoffs        float64
	WriteKeys       float64
	WriteSize       float64
	TxnRetries      float64
	AffectedRows    float64
	Memory          float64
	Disk            float64
}

type Client struct {
	user, password string
	timeout        time.Duration
	db             *sql.DB
	address        string
}

type tlsConfig struct {
	component, instance, key, value string
}

func New(user, password string, timeout time.Duration) *Client {
	return &Client{user: user, password: password, timeout: timeout}
}

func (c *Client) Close() error {
	if c.db != nil {
		return c.db.Close()
	}
	return nil
}

func (c *Client) Address() string { return c.address }

// Connect selects the first reachable TiDB SQL endpoint. Prometheus normally
// reports TiDB's status port, so the host is retained and the standard SQL port
// 4000 is used.
func (c *Client) Connect(ctx context.Context, instances []monitor.Instance) error {
	if c.db != nil {
		if err := c.db.PingContext(ctx); err == nil {
			return nil
		}
		_ = c.db.Close()
		c.db, c.address = nil, ""
	}

	addresses := sqlAddresses(instances)
	if len(addresses) == 0 {
		return fmt.Errorf("Prometheus did not return any UP TiDB instances")
	}
	var failures []string
	for _, address := range addresses {
		cfg := mysql.NewConfig()
		cfg.User = c.user
		cfg.Passwd = c.password
		cfg.Net = "tcp"
		cfg.Addr = address
		cfg.DBName = "information_schema"
		cfg.ParseTime = true
		cfg.Timeout = c.timeout
		cfg.ReadTimeout = c.timeout
		cfg.WriteTimeout = c.timeout
		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err == nil {
			err = db.PingContext(ctx)
		}
		if err == nil {
			c.db, c.address = db, address
			return nil
		}
		if db != nil {
			_ = db.Close()
		}
		failures = append(failures, address+": "+err.Error())
	}
	return fmt.Errorf("cannot connect to a TiDB SQL endpoint: %s", strings.Join(failures, "; "))
}

func sqlAddresses(instances []monitor.Instance) []string {
	seen := make(map[string]bool)
	var addresses []string
	for _, instance := range instances {
		if instance.Role != "TIDB" || instance.Status != "UP" {
			continue
		}
		host := instance.Name
		if parsed, _, err := net.SplitHostPort(instance.Name); err == nil {
			host = parsed
		}
		host = strings.Trim(host, "[]")
		address := net.JoinHostPort(host, "4000")
		if host != "" && !seen[address] {
			seen[address] = true
			addresses = append(addresses, address)
		}
	}
	sort.Strings(addresses)
	return addresses
}

const activeSessionsQuery = `
SELECT p.INSTANCE, p.ID, p.USER, p.HOST, COALESCE(p.DB, ''), p.COMMAND,
       p.TIME, COALESCE(p.STATE, ''), COALESCE(p.INFO, ''), COALESCE(p.DIGEST, ''),
       COALESCE(p.MEM, 0), COALESCE(p.DISK, 0),
       COALESCE(s.EXEC_COUNT, 0), COALESCE(s.AVG_LATENCY, 0)
FROM information_schema.CLUSTER_PROCESSLIST AS p
LEFT JOIN (
    SELECT INSTANCE, DIGEST, SUM(EXEC_COUNT) AS EXEC_COUNT,
           SUM(SUM_LATENCY) / NULLIF(SUM(EXEC_COUNT), 0) AS AVG_LATENCY
    FROM information_schema.CLUSTER_STATEMENTS_SUMMARY
    WHERE SUMMARY_END_TIME >= NOW() - INTERVAL 10 MINUTE
    GROUP BY INSTANCE, DIGEST
) AS s ON s.INSTANCE = p.INSTANCE AND s.DIGEST = p.DIGEST
WHERE p.COMMAND <> 'Sleep' AND p.ID <> CONNECTION_ID()
ORDER BY p.TIME DESC`

func (c *Client) ActiveSessions(ctx context.Context) ([]Session, error) {
	if c.db == nil {
		return nil, fmt.Errorf("TiDB SQL connection is not initialized")
	}
	rows, err := c.db.QueryContext(ctx, activeSessionsQuery)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []Session
	for rows.Next() {
		var row Session
		var averageNS float64
		var memoryRaw, diskRaw sql.RawBytes
		if err := rows.Scan(&row.Instance, &row.ID, &row.User, &row.Host, &row.Database,
			&row.Command, &row.Seconds, &row.State, &row.SQL, &row.Digest, &memoryRaw, &diskRaw,
			&row.ExecCount, &averageNS); err != nil {
			return nil, err
		}
		row.Memory = resourceBytes(memoryRaw)
		row.Disk = resourceBytes(diskRaw)
		row.AverageLatency = time.Duration(averageNS)
		sessions = append(sessions, row)
	}
	return sessions, rows.Err()
}

const schemaSummaryQuery = `
SELECT INSTANCE, COALESCE(NULLIF(SCHEMA_NAME, ''), '(none)'), SUMMARY_BEGIN_TIME,
       SUM(EXEC_COUNT),
       SUM(CASE WHEN UPPER(STMT_TYPE) IN ('INSERT', 'UPDATE', 'DELETE', 'REPLACE')
                THEN EXEC_COUNT ELSE 0 END),
       SUM(SUM_ERRORS), SUM(SUM_LATENCY),
       SUM(AVG_TOTAL_KEYS * EXEC_COUNT), SUM(AVG_PROCESSED_KEYS * EXEC_COUNT),
       SUM(SUM_COP_TASK_NUM), SUM(SUM_BACKOFF_TIMES),
       SUM(AVG_WRITE_KEYS * EXEC_COUNT), SUM(AVG_WRITE_SIZE * EXEC_COUNT),
       SUM(AVG_TXN_RETRY * EXEC_COUNT),
       SUM(AVG_AFFECTED_ROWS * EXEC_COUNT), SUM(AVG_MEM * EXEC_COUNT),
       SUM(AVG_DISK * EXEC_COUNT)
FROM (
    SELECT INSTANCE, SCHEMA_NAME, STMT_TYPE, SUMMARY_BEGIN_TIME, EXEC_COUNT, SUM_ERRORS,
           SUM_LATENCY, AVG_TOTAL_KEYS, AVG_PROCESSED_KEYS, SUM_COP_TASK_NUM,
           SUM_BACKOFF_TIMES, AVG_WRITE_KEYS, AVG_WRITE_SIZE, AVG_TXN_RETRY,
           AVG_AFFECTED_ROWS, AVG_MEM, AVG_DISK
    FROM information_schema.CLUSTER_STATEMENTS_SUMMARY
    UNION ALL
    SELECT INSTANCE, SCHEMA_NAME, STMT_TYPE, SUMMARY_BEGIN_TIME, EXEC_COUNT, SUM_ERRORS,
           SUM_LATENCY, AVG_TOTAL_KEYS, AVG_PROCESSED_KEYS, SUM_COP_TASK_NUM,
           SUM_BACKOFF_TIMES, AVG_WRITE_KEYS, AVG_WRITE_SIZE, AVG_TXN_RETRY,
           AVG_AFFECTED_ROWS, AVG_MEM, AVG_DISK
    FROM information_schema.CLUSTER_STATEMENTS_SUMMARY_HISTORY
    WHERE SUMMARY_END_TIME >= DATE_SUB(NOW(), INTERVAL ? SECOND)
) AS summaries
GROUP BY INSTANCE, COALESCE(NULLIF(SCHEMA_NAME, ''), '(none)'), SUMMARY_BEGIN_TIME`

// SchemaSummary returns current counters plus any summary windows completed
// since the previous successful sample. This prevents a refresh boundary from
// dropping the tail of the previous window.
func (c *Client) SchemaSummary(ctx context.Context, since time.Time) ([]SchemaSample, error) {
	if c.db == nil {
		return nil, fmt.Errorf("TiDB SQL connection is not initialized")
	}
	if since.IsZero() {
		since = time.Now()
	}
	lookback := int64(time.Since(since)/time.Second) + 60
	if lookback < 60 {
		lookback = 60
	}
	rows, err := c.db.QueryContext(ctx, schemaSummaryQuery, lookback)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []SchemaSample
	for rows.Next() {
		var sample SchemaSample
		if err := rows.Scan(&sample.Instance, &sample.Schema, &sample.WindowBegin,
			&sample.Counters.Executions, &sample.Counters.WriteExecutions,
			&sample.Counters.Errors, &sample.Counters.Latency, &sample.Counters.TotalKeys,
			&sample.Counters.ProcessedKeys, &sample.Counters.CopTasks, &sample.Counters.Backoffs,
			&sample.Counters.WriteKeys, &sample.Counters.WriteSize, &sample.Counters.TxnRetries,
			&sample.Counters.AffectedRows,
			&sample.Counters.Memory, &sample.Counters.Disk); err != nil {
			return nil, err
		}
		samples = append(samples, sample)
	}
	return samples, rows.Err()
}

// TiDB can expose a negative tracker value through an unsigned MEM/DISK
// column, which appears near MaxUint64. Treat such underflow values as invalid
// instead of failing the entire processlist scan.
func resourceBytes(raw []byte) int64 {
	value, err := strconv.ParseUint(string(raw), 10, 64)
	if err != nil || value > math.MaxInt64 {
		return 0
	}
	return int64(value)
}

func (c *Client) LongTransactionCount(ctx context.Context, threshold time.Duration) (int, error) {
	if c.db == nil {
		return 0, fmt.Errorf("TiDB SQL connection is not initialized")
	}
	const query = `
SELECT COUNT(*)
FROM information_schema.CLUSTER_TIDB_TRX
WHERE TIMESTAMPDIFF(SECOND, START_TIME, NOW()) >= ?`
	var count int
	if err := c.db.QueryRowContext(ctx, query, int64(threshold/time.Second)).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (c *Client) ClusterTLSStatus(ctx context.Context) (string, error) {
	if c.db == nil {
		return "N/A", fmt.Errorf("TiDB SQL connection is not initialized")
	}
	const query = "SELECT TYPE, INSTANCE, `KEY`, COALESCE(VALUE, '') " +
		"FROM information_schema.CLUSTER_CONFIG " +
		"WHERE (TYPE = 'tidb' AND `KEY` IN ('security.cluster-ssl-ca', 'security.cluster-ssl-cert', 'security.cluster-ssl-key')) " +
		"OR (TYPE = 'tikv' AND `KEY` IN ('security.ca-path', 'security.cert-path', 'security.key-path')) " +
		"OR (TYPE = 'pd' AND `KEY` IN ('security.cacert-path', 'security.cert-path', 'security.key-path'))"
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return "N/A", err
	}
	defer rows.Close()
	var configs []tlsConfig
	for rows.Next() {
		var config tlsConfig
		if err := rows.Scan(&config.component, &config.instance, &config.key, &config.value); err != nil {
			return "N/A", err
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		return "N/A", err
	}
	return clusterTLSStatus(configs), nil
}

func clusterTLSStatus(configs []tlsConfig) string {
	if len(configs) == 0 {
		return "UNKNOWN"
	}
	type state struct{ present, configured int }
	instances := make(map[string]state)
	components := make(map[string]bool)
	for _, config := range configs {
		name := strings.ToLower(config.component)
		components[name] = true
		id := name + "@" + config.instance
		current := instances[id]
		current.present++
		if strings.TrimSpace(config.value) != "" {
			current.configured++
		}
		instances[id] = current
	}
	if !components["tidb"] || !components["tikv"] || !components["pd"] {
		return "INCONSISTENT"
	}
	enabled, disabled := 0, 0
	for _, current := range instances {
		switch {
		case current.present == 3 && current.configured == 3:
			enabled++
		case current.present == 3 && current.configured == 0:
			disabled++
		default:
			return "INCONSISTENT"
		}
	}
	if enabled == len(instances) {
		return "ON"
	}
	if disabled == len(instances) {
		return "OFF"
	}
	return "INCONSISTENT"
}

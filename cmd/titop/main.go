package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"titop/internal/monitor"
	"titop/internal/prometheus"
	"titop/internal/terminal"
	"titop/internal/tidbsql"
)

var version = "dev"

type view int

const (
	instances view = iota
	sqlTypes
	waits
	tikvThreads
	help
)

func main() {
	var prometheusSpec string
	var mysqlUser, mysqlPassword string
	var endpoint, clusterName string
	var highQPS, highTPS float64
	var interval, timeout, longQueryThreshold, longTxnThreshold time.Duration
	var once, plain, noColor, showVersion bool
	flag.StringVar(&prometheusSpec, "prometheus", env("TITOP_PROMETHEUS", ""), "[cluster@]Prometheus address (for example: 127.0.0.1:9090)")
	flag.StringVar(&prometheusSpec, "m", env("TITOP_PROMETHEUS", ""), "shorthand for --prometheus")
	flag.StringVar(&mysqlUser, "u", env("TITOP_MYSQL_USER", ""), "TiDB SQL user (enables active-session SQL view with -p)")
	flag.StringVar(&mysqlPassword, "p", env("TITOP_MYSQL_PASSWORD", ""), "TiDB SQL password (enables active-session SQL view with -u)")
	flag.DurationVar(&interval, "interval", 5*time.Second, "refresh interval")
	flag.DurationVar(&timeout, "timeout", 4*time.Second, "query timeout")
	flag.DurationVar(&longQueryThreshold, "long-query-threshold", 10*time.Second, "running-query duration considered long")
	flag.DurationVar(&longTxnThreshold, "long-txn-threshold", time.Minute, "transaction duration considered long")
	flag.Float64Var(&highQPS, "high-qps", 50000, "QPS threshold for high load")
	flag.Float64Var(&highTPS, "high-tps", 5000, "TPS threshold for high load")
	flag.BoolVar(&once, "once", false, "print one snapshot and exit")
	flag.BoolVar(&plain, "plain", false, "do not clear the terminal or read keys")
	flag.BoolVar(&noColor, "no-color", false, "disable ANSI colors")
	flag.BoolVar(&showVersion, "version", false, "print version")
	flag.Parse()
	if showVersion {
		fmt.Println("titop", version)
		return
	}
	if interval < time.Second {
		fatal("interval must be at least 1s")
	}
	if longQueryThreshold < time.Second || longTxnThreshold < time.Second {
		fatal("long-query-threshold and long-txn-threshold must be at least 1s")
	}
	if highQPS <= 0 || highTPS <= 0 {
		fatal("high-qps and high-tps must be greater than zero")
	}
	userSet, passwordSet := flagWasSet("u") || os.Getenv("TITOP_MYSQL_USER") != "", flagWasSet("p") || os.Getenv("TITOP_MYSQL_PASSWORD") != ""
	if userSet != passwordSet {
		fatal("-u and -p must be provided together to enable the active-session SQL view")
	}
	clusterName, endpoint, err := parsePrometheusSpec(prometheusSpec)
	if err != nil {
		fatal(err.Error())
	}
	promClient, err := prometheus.New(endpoint, timeout)
	if err != nil {
		fatal(err.Error())
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var sqlClient *tidbsql.Client
	if userSet {
		sqlClient = tidbsql.New(mysqlUser, mysqlPassword, timeout)
		defer sqlClient.Close()
	}
	keys := make(chan byte, 1)
	if !once && !plain {
		if info, e := os.Stdin.Stat(); e == nil && info.Mode()&os.ModeCharDevice != 0 {
			if state, e := terminal.MakeRaw(os.Stdin); e == nil {
				defer state.Restore()
				go readKeys(keys)
			}
		}
	}
	current, previous, paused := instances, instances, false
	color := !noColor && !once && !plain && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" && isTerminal(os.Stdout)
	snap := collect(ctx, promClient, timeout, clusterName)
	sqlSnap := collectSQL(ctx, sqlClient, snap.Instances, timeout, longQueryThreshold, longTxnThreshold)
	for {
		draw(endpoint, snap, sqlSnap, sqlClient, current, paused, interval, longQueryThreshold, longTxnThreshold, highQPS, highTPS, plain || once, color)
		if once {
			if len(snap.Errors) > 0 {
				os.Exit(1)
			}
			return
		}
		timer := time.NewTimer(interval)
		refresh := false
		select {
		case <-ctx.Done():
			timer.Stop()
			fmt.Println("\nbye")
			return
		case <-timer.C:
			refresh = !paused
		case key := <-keys:
			timer.Stop()
			switch key {
			case 'q', 'Q':
				return
			case 'h', '?':
				if current == help {
					current = previous
				} else {
					previous, current = current, help
				}
			case 'i', 'I':
				current = instances
			case 's', 'S':
				current = sqlTypes
			case 'w', 'W':
				current = waits
			case 'k', 'K':
				current = tikvThreads
			case 'p', 'P':
				paused = !paused
			case ' ':
				refresh = true
			}
		}
		if refresh {
			snap = collect(ctx, promClient, timeout, clusterName)
			sqlSnap = collectSQL(ctx, sqlClient, snap.Instances, timeout, longQueryThreshold, longTxnThreshold)
		}
	}
}

type sqlSnapshot struct {
	sessions                      []tidbsql.Session
	longQueries, longTransactions int
	sessionErr, transactionErr    error
	tlsStatus                     string
	tlsErr                        error
}

func collectSQL(ctx context.Context, client *tidbsql.Client, instances []monitor.Instance, timeout, longQueryThreshold, longTxnThreshold time.Duration) sqlSnapshot {
	result := sqlSnapshot{tlsStatus: "N/A"}
	if client == nil {
		return result
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := client.Connect(queryCtx, instances); err != nil {
		result.sessionErr, result.transactionErr, result.tlsErr = err, err, err
		return result
	}
	result.sessions, result.sessionErr = client.ActiveSessions(queryCtx)
	if result.sessionErr == nil {
		result.longQueries = longQueryCount(result.sessions, longQueryThreshold)
	}
	result.longTransactions, result.transactionErr = client.LongTransactionCount(queryCtx, longTxnThreshold)
	result.tlsStatus, result.tlsErr = client.ClusterTLSStatus(queryCtx)
	return result
}

func collect(ctx context.Context, client *prometheus.Client, timeout time.Duration, clusterName string) monitor.Snapshot {
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	s := monitor.Collect(queryCtx, client)
	if clusterName != "" {
		s.ClusterName = clusterName
	}
	return s
}
func readKeys(ch chan<- byte) {
	b := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(b); err != nil {
			return
		}
		ch <- b[0]
	}
}

func draw(endpoint string, s monitor.Snapshot, sqlSnap sqlSnapshot, sqlClient *tidbsql.Client, v view, paused bool, interval, longQueryThreshold, longTxnThreshold time.Duration, highQPS, highTPS float64, noClear, color bool) {
	width := displayWidth()
	if !noClear {
		fmt.Print("\033[H\033[2J")
	}
	state := fmt.Sprintf("LIVE/%s", interval)
	if paused {
		state = "PAUSED"
	}
	stateColor := green
	if paused {
		stateColor = yellow
	}
	fmt.Printf("%s %s  TiDB near real-time monitor  [%s]\n", paint(color, bold+cyan, "TiTop"), version, paint(color, stateColor, state))
	cluster := s.ClusterName
	if cluster == "" {
		cluster = "unknown"
	}
	fmt.Printf("CLUSTER %-24.24s PROMETHEUS %-38.38s SNAPSHOT %s\n", cluster, endpoint, s.At.Format("2006-01-02 15:04:05"))
	line(width)
	section(color, "CLUSTER ACTIVITY")
	fmt.Printf(" QPS %s  TPS %s  P99 %s  CONN %s  ACTIVE %s  ERR/s %s  NODES %s\n",
		paint(color, workloadColor(s, s.QPS, highQPS), fmt.Sprintf("%9.2f", s.QPS)),
		paint(color, workloadColor(s, s.TPS, highTPS), fmt.Sprintf("%9.2f", s.TPS)),
		paint(color, latencyColor(s.P99), fmt.Sprintf("%9s", duration(s.P99))),
		paint(color, green, fmt.Sprintf("%7.0f", s.Connections)),
		paint(color, green, fmt.Sprintf("%6.0f", s.Active)),
		paint(color, positiveBad(s.ErrorRate), fmt.Sprintf("%7.2f", s.ErrorRate)),
		paint(color, nodeColor(s), fmt.Sprintf("TiDB:%d TiKV:%d PD:%d", s.TiDBUp, s.TiKVUp, s.PDUp)))
	fmt.Printf(" LONG QUERY (>=%s) %s  LONG TXN (>=%s) %s  CLUSTER TLS %s\n",
		shortThreshold(longQueryThreshold), sqlMetric(sqlClient != nil && sqlSnap.sessionErr == nil, sqlSnap.longQueries, color),
		shortThreshold(longTxnThreshold), sqlMetric(sqlClient != nil && sqlSnap.transactionErr == nil, sqlSnap.longTransactions, color),
		clusterTLSMetric(sqlClient != nil && sqlSnap.tlsErr == nil, sqlSnap.tlsStatus, color))
	line(width)
	if v != sqlTypes {
		renderRequests("TOP 5 TiKV CLIENT REQUEST LOAD", limit(s.Requests, 5), color)
		line(width)
	}
	switch v {
	case help:
		renderHelp(color)
	case sqlTypes:
		renderSQLTypes(s.SQLTypes, color)
		if sqlClient != nil {
			line(width)
			renderActiveSessions(sqlSnap.sessions, sqlSnap.sessionErr, sqlClient.Address(), width, color)
		}
	case waits:
		renderRequests("TiKV CLIENT REQUEST LOAD", s.Requests, color)
	case tikvThreads:
		renderTiKV(s.TiKVs, color)
	default:
		renderInstances(s.Instances, color)
	}
	line(width)
	fmt.Print("Keys: [i]nstances ti[k]v threads [s]ql types [w]aits [p]ause [space] refresh [h]elp [q]uit")
	if len(s.Errors) > 0 {
		fmt.Print(paint(color, red+bold, fmt.Sprintf("  WARN:%d", len(s.Errors))))
	}
	fmt.Println()
}

func renderInstances(rows []monitor.Instance, color bool) {
	section(color, "ALL CLUSTER NODES (DOWN first, then CPU descending)")
	fmt.Printf(" %-19s %6s %8s %10s %9s %7s %7s %9s %9s %9s %9s %9s %9s %12s\n", "INSTANCE", "ROLE", "STATUS", "UPTIME", "QPS", "CONN", "ACTIVE", "CPU", "RSS", "HOST MEM", "RSS/HOST%", "LREAD/s", "LWRITE/s", "VERSION")
	if len(rows) == 0 {
		fmt.Println(" (no TiDB instance metrics returned)")
	}
	for _, r := range rows {
		fmt.Printf(" %-19.19s %6s %s %10s %s %7.0f %7.0f %s %9s %9s %s %9s %9s %12.12s\n", r.Name, r.Role, status(r.Status, color), nodeUptime(r),
			paint(color, green, fmt.Sprintf("%9.2f", r.QPS)), r.Connections, r.Active,
			paint(color, cpuColor(r.CPU), fmt.Sprintf("%8.2f%%", r.CPU*100)), bytes(r.Memory), optionalBytes(r.HostMemory), memoryPercent(r.Memory, r.HostMemory, color),
			logicalOPS(r, r.LogicalReads, color), logicalOPS(r, r.LogicalWrites, color), displayVersion(r.Version))
	}
}

func logicalOPS(r monitor.Instance, value float64, color bool) string {
	if r.Role != "TIKV" || !r.LogicalOPSAvailable {
		return fmt.Sprintf("%9s", "-")
	}
	return paint(color, green, fmt.Sprintf("%9.2f", value))
}
func renderTiKV(rows []monitor.TiKVInstance, color bool) {
	section(color, "ALL TiKV INSTANCES / THREAD POOL CPU")
	fmt.Printf(" %-32s %8s %11s %11s %11s %11s %11s %11s\n",
		"INSTANCE", "STATUS", "UNI READ", "RAFT STORE", "ASYNC APPLY", "GRPC POLL", "STOR READ", "GC WORKER")
	if len(rows) == 0 {
		fmt.Println(" (no TiKV metrics returned)")
	}
	for _, r := range rows {
		fmt.Printf(" %-32.32s %s %s %s %s %s %s %s\n", r.Name, status(r.Status, color),
			threadCPU(r.UnifiedReadCPU, color), threadCPU(r.RaftStoreCPU, color), threadCPU(r.AsyncApplyCPU, color),
			threadCPU(r.GRPCPollCPU, color), threadCPU(r.StorageReadCPU, color), threadCPU(r.GCWorkerCPU, color))
	}
}
func threadCPU(value float64, color bool) string {
	return paint(color, threadCPUColor(value), fmt.Sprintf("%10.2f%%", value*100))
}
func renderSQLTypes(rows []monitor.Activity, color bool) {
	section(color, "TOP SQL TYPES")
	dml, ddl, admin, other := splitSQLTypes(rows)
	fmt.Printf(" %-17s %8s | %-17s %8s | %-17s %8s | %-17s %8s\n", "DML / TXN", "QPS", "DDL", "QPS", "ADMIN", "QPS", "OTHER", "QPS")
	n := len(dml)
	if len(ddl) > n {
		n = len(ddl)
	}
	if len(admin) > n {
		n = len(admin)
	}
	if len(other) > n {
		n = len(other)
	}
	if n == 0 {
		fmt.Println(" (no SQL type metrics returned)")
		return
	}
	for i := 0; i < n && i < 15; i++ {
		dmlName, dmlValue := "", strings.Repeat(" ", 8)
		ddlName, ddlValue := "", strings.Repeat(" ", 8)
		adminName, adminValue := "", strings.Repeat(" ", 8)
		otherName, otherValue := "", strings.Repeat(" ", 8)
		if i < len(dml) {
			dmlName = dml[i].Name
			dmlValue = paint(color, green, fmt.Sprintf("%8.2f", dml[i].Value))
		}
		if i < len(ddl) {
			ddlName = ddl[i].Name
			ddlValue = paint(color, green, fmt.Sprintf("%8.2f", ddl[i].Value))
		}
		if i < len(admin) {
			adminName = admin[i].Name
			adminValue = paint(color, green, fmt.Sprintf("%8.2f", admin[i].Value))
		}
		if i < len(other) {
			otherName = other[i].Name
			otherValue = paint(color, green, fmt.Sprintf("%8.2f", other[i].Value))
		}
		fmt.Printf(" %-17.17s %s | %-17.17s %s | %-17.17s %s | %-17.17s %s\n",
			dmlName, dmlValue, ddlName, ddlValue, adminName, adminValue, otherName, otherValue)
	}
}

func splitSQLTypes(rows []monitor.Activity) (dml, ddl, admin, other []monitor.Activity) {
	for _, row := range rows {
		name := strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(row.Name))
		switch name {
		case "select", "insert", "insertselect", "update", "delete", "replace", "load", "loaddata", "begin", "starttransaction", "commit", "rollback", "savepoint", "prepare", "execute":
			dml = append(dml, row)
		case "ddl", "createdatabase", "dropdatabase", "createtable", "droptable", "altertable", "truncatetable", "createindex", "dropindex", "createsequence", "dropsequence", "altersequence", "createview", "dropview", "rename", "recover", "flashback":
			ddl = append(ddl, row)
		case "show", "grant", "revoke", "createuser", "alteruser", "dropuser", "setpassword", "setrole", "explain", "explainsql", "desc", "desctable", "describe", "set", "use", "analyze", "analyzetable", "flush", "kill", "admin":
			admin = append(admin, row)
		default:
			other = append(other, row)
		}
	}
	return dml, ddl, admin, other
}

func renderActiveSessions(rows []tidbsql.Session, err error, address string, width int, color bool) {
	const fixedWidth = 173
	sqlWidth := width - fixedWidth
	if sqlWidth < 15 {
		sqlWidth = 15
	}
	section(color, "ACTIVE SQL SESSIONS (target "+address+")")
	fmt.Printf(" %-18s %20s %-13s %-16s %-15s %-16s %-8s %-12s %6s %10s %7s %9s %9s %-*s\n",
		"INSTANCE", "ID", "DIGEST_ID", "USER", "HOST", "DB", "COMMAND", "STATE", "TIME", "AVG LAT", "EXEC", "MEM", "DISK", sqlWidth, "SQL")
	if err != nil {
		fmt.Println(paint(color, red, " "+err.Error()))
		return
	}
	if len(rows) == 0 {
		fmt.Println(" (no active SQL sessions)")
		return
	}
	for _, row := range limit(rows, 30) {
		statement := strings.Join(strings.Fields(row.SQL), " ")
		id := fmt.Sprintf("%20.20s", strconv.FormatInt(row.ID, 10))
		if highSessionResources(row) {
			id = paint(color, red+bold, id)
		}
		fmt.Printf(" %-18.18s %s %-13.13s %-16.16s %-15.15s %-16.16s %-8.8s %-12.12s %6.6s %10.10s %7.7s %9.9s %9.9s %-*.*s\n",
			row.Instance, id, digestID(row.Digest), row.User, row.Host, row.Database, row.Command, row.State,
			fmt.Sprintf("%ds", row.Seconds), duration(row.AverageLatency.Seconds()), strconv.FormatInt(row.ExecCount, 10), bytes(float64(row.Memory)), bytes(float64(row.Disk)),
			sqlWidth, sqlWidth, statement)
	}
}

func digestID(digest string) string {
	if digest == "" {
		return "-"
	}
	const alphabet = "0123456789abcdfghjkmnpqrstuvwxyz"
	sum := sha256.Sum256([]byte(digest))
	value := binary.BigEndian.Uint64(sum[:8])
	id := [13]byte{}
	for i := len(id) - 1; i >= 0; i-- {
		id[i] = alphabet[value&31]
		value >>= 5
	}
	return string(id[:])
}

func highSessionResources(row tidbsql.Session) bool {
	const (
		memoryAlert = 100 * 1024 * 1024
		diskAlert   = 1024 * 1024 * 1024
	)
	return row.Memory >= memoryAlert || row.Disk >= diskAlert
}

func longQueryCount(rows []tidbsql.Session, threshold time.Duration) int {
	count := 0
	for _, row := range rows {
		if time.Duration(row.Seconds)*time.Second >= threshold {
			count++
		}
	}
	return count
}

func sqlMetric(available bool, value int, color bool) string {
	if !available {
		return paint(color, yellow, fmt.Sprintf("%5s", "N/A"))
	}
	code := green
	if value > 0 {
		code = red + bold
	}
	return paint(color, code, fmt.Sprintf("%5d", value))
}

func clusterTLSMetric(available bool, status string, color bool) string {
	if !available {
		return fmt.Sprintf("%-12s", "N/A")
	}
	code := yellow
	switch status {
	case "ON":
		code = green
	case "OFF":
		code = red + bold
	}
	return paint(color, code, fmt.Sprintf("%-12s", status))
}

func shortThreshold(value time.Duration) string {
	if value%time.Minute == 0 {
		return fmt.Sprintf("%dm", value/time.Minute)
	}
	return fmt.Sprintf("%ds", value/time.Second)
}
func renderRequests(title string, rows []monitor.RequestStat, color bool) {
	section(color, title)
	fmt.Printf(" %-30s %12s %12s %12s %14s\n", "REQUEST TYPE", "OPS/s", "AVG LAT", "P99 LAT", "TIME LOAD")
	if len(rows) == 0 {
		fmt.Println(" (no TiKV client request metrics returned)")
	}
	for _, r := range rows {
		fmt.Printf(" %-30.30s %s %s %s %s\n", r.Name,
			paint(color, green, fmt.Sprintf("%12.2f", r.OPS)),
			paint(color, latencyColor(r.Average), fmt.Sprintf("%12s", duration(r.Average))),
			paint(color, latencyColor(r.P99), fmt.Sprintf("%12s", duration(r.P99))),
			paint(color, yellow, fmt.Sprintf("%12s/s", duration(r.Load))))
	}
}
func renderHelp(color bool) {
	section(color, "INTERACTIVE HELP")
	fmt.Println(" i  All cluster nodes        s  SQL types / active sessions")
	fmt.Println(" k  TiKV thread-pool CPU     w  TiKV request-time view")
	fmt.Println(" p  Pause/resume automatic refresh")
	fmt.Println(" Space  Refresh immediately  h/?  Toggle this help     q  Quit")
	fmt.Println(" Prometheus queries are independent; missing metrics appear as warnings.")
}
func limit[T any](v []T, n int) []T {
	if len(v) > n {
		return v[:n]
	}
	return v
}
func line(width int) { fmt.Println(strings.Repeat("-", width)) }
func displayWidth() int {
	width := terminal.Width(os.Stdout)
	if width <= 0 {
		width, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if width <= 1 {
		return 120
	}
	return width - 1
}

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
)

func paint(enabled bool, code, value string) string {
	if !enabled {
		return value
	}
	return code + value + reset
}
func section(enabled bool, title string) { fmt.Println(paint(enabled, bold+cyan, title)) }
func positiveBad(v float64) string {
	if v > 0 {
		return red
	}
	return green
}
func workloadColor(s monitor.Snapshot, value, threshold float64) string {
	if value < threshold {
		return green
	}
	if s.P99 >= 1 || s.ErrorRate > 0 || tidbCPUSaturated(s.Instances) {
		return red + bold
	}
	return yellow
}
func tidbCPUSaturated(instances []monitor.Instance) bool {
	for _, instance := range instances {
		if instance.Role == "TIDB" && instance.CPU >= .9 {
			return true
		}
	}
	return false
}
func latencyColor(v float64) string {
	if v >= 1 {
		return red
	}
	if v >= .2 {
		return yellow
	}
	return green
}
func cpuColor(v float64) string {
	if v >= .9 {
		return red
	}
	if v >= .7 {
		return yellow
	}
	return green
}
func threadCPUColor(v float64) string {
	if v >= 8 {
		return red
	}
	if v >= 6 {
		return yellow
	}
	return green
}
func status(value string, color bool) string {
	code := yellow
	if value == "UP" {
		code = green
	} else if value == "DOWN" {
		code = red
	}
	return paint(color, code+bold, fmt.Sprintf("%8s", value))
}
func nodeColor(s monitor.Snapshot) string {
	if s.TiDBUp == 0 || s.TiKVUp == 0 || s.PDUp == 0 {
		return red
	}
	return green
}
func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
func duration(seconds float64) string {
	if seconds <= 0 {
		return "-"
	}
	switch {
	case seconds < 0.000001:
		return fmt.Sprintf("%.2fns", seconds*1e9)
	case seconds < 0.001:
		return fmt.Sprintf("%.2fµs", seconds*1e6)
	case seconds < 1:
		return fmt.Sprintf("%.2fms", seconds*1e3)
	default:
		return fmt.Sprintf("%.2fs", seconds)
	}
}
func bytes(v float64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%.0fB", v)
	}
	div, exp := float64(unit), 0
	for n := v / unit; n >= unit && exp < 4; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.0f%ciB", v/div, "KMGTPE"[exp])
}
func optionalBytes(v float64) string {
	if v <= 0 {
		return "-"
	}
	return bytes(v)
}
func memoryPercent(rss, limit float64, color bool) string {
	if limit <= 0 {
		return fmt.Sprintf("%8s", "-")
	}
	ratio := rss / limit
	code := green
	if ratio >= .9 {
		code = red
	} else if ratio >= .8 {
		code = yellow
	}
	return paint(color, code, fmt.Sprintf("%7.2f%%", ratio*100))
}
func nodeUptime(r monitor.Instance) string {
	if r.Status != "UP" || r.Uptime <= 0 {
		return "-"
	}
	return formatUptime(time.Duration(r.Uptime * float64(time.Second)))
}
func displayVersion(version string) string {
	if version == "" {
		return "-"
	}
	if i := strings.LastIndex(version, "TiDB-"); i >= 0 {
		return version[i+5:]
	}
	return version
}

func formatUptime(d time.Duration) string {
	const (
		day   = 24 * time.Hour
		week  = 7 * day
		month = 30 * day
		year  = 365 * day
	)
	switch {
	case d >= year:
		return fmt.Sprintf("%dy", d/year)
	case d >= month:
		return fmt.Sprintf("%dM", d/month)
	case d >= week:
		return fmt.Sprintf("%dw", d/week)
	case d >= day:
		return fmt.Sprintf("%dd", d/day)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d >= time.Minute:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

func parsePrometheusSpec(spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", "", fmt.Errorf("--prometheus requires an address, for example: 127.0.0.1:9090")
	}

	cluster, endpoint := "unknown", spec
	if before, after, ok := strings.Cut(spec, "@"); ok {
		cluster, endpoint = strings.TrimSpace(before), strings.TrimSpace(after)
		if cluster == "" || endpoint == "" {
			return "", "", fmt.Errorf("invalid --prometheus value %q", spec)
		}
	}
	if strings.ContainsAny(cluster, " \t\r\n") {
		return "", "", fmt.Errorf("cluster name must not contain whitespace")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	return cluster, endpoint, nil
}
func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) { found = found || f.Name == name })
	return found
}
func fatal(message string) { fmt.Fprintln(os.Stderr, "titop:", message); os.Exit(2) }

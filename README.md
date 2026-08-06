# TiTop

[English](README_EN.md) | **简体中文**

TiTop 是一个面向 TiDB 集群的轻量级实时终端监控工具，交互体验参考 `oratop`。它直接查询 Prometheus，并可选连接 TiDB SQL 端口，在一个终端中集中展示集群负载、节点状态、TiKV 请求、线程池 CPU、SQL 类型和活跃会话。

TiTop 适合日常巡检、故障现场观察、压测监控和远程终端使用。Prometheus 是必需数据源；TiDB 数据库账号是可选项，仅在需要活跃会话、长事务和组件间 TLS 状态时使用。

## 功能概览

- 集群活动：QPS、TPS、P99、连接数、活跃数、错误率及 TiDB/TiKV/PD 在线节点数。
- 当前异常：长查询、长事务和 TiDB 组件间 TLS 状态。
- 节点视图：角色、状态、版本、运行时间、QPS、连接、CPU、RSS、主机内存及 TiKV 逻辑读写 OPS。
- TiKV 请求：OPS、平均延迟、P99 延迟和每秒累计时间负载。
- TiKV 线程池：Unified Read、Raft Store、Async Apply、gRPC Poll、Storage Read 和 GC Worker CPU。
- SQL 类型：DML/事务、DDL、管理和其他操作四栏展示。
- 活跃会话：实例、连接 ID、短 `DIGEST_ID`、用户、来源、数据库、Command、State、执行时间、近期摘要统计、内存、磁盘临时空间及 SQL 文本。
- 交互刷新：页面切换、暂停、手动刷新、纯文本和单次快照。

## 环境要求

- Linux：交互式终端依赖 Linux TTY；其他平台可以使用 `--once` 或 `--plain`。
- Go 1.23 或更高版本：仅从源码构建时需要。
- 网络连通性：运行 TiTop 的主机需要访问 Prometheus HTTP API。
- 可选 SQL 连通性：使用 `-u/-p` 时，需要访问 TiDB 标准 SQL 端口 `4000`。

TiTop 当前会从 Prometheus 返回的 UP 状态 TiDB 实例中提取主机地址，将端口替换为 `4000`，然后按地址顺序连接第一个可用实例。因此，使用非标准 TiDB SQL 端口的集群暂不支持 SQL 增强功能。

## 快速开始

### 仅使用 Prometheus

```bash
./titop --prometheus 10.0.0.10:9090
```

省略协议时自动使用 HTTP。以下写法等价：

```bash
./titop -m 10.0.0.10:9090
./titop -m http://10.0.0.10:9090
```

可在地址前指定用于页面展示的集群名：

```bash
./titop -m production@http://10.0.0.10:9090
```

未指定集群名时显示 `unknown`。集群名仅用于展示，不会自动转换为 Prometheus 的 `tidb_cluster` 标签过滤条件；建议每次连接只包含目标集群数据的 Prometheus。

### 启用 SQL 增强功能

```bash
./titop -m production@10.0.0.10:9090 -u monitor -p 'password'
```

为避免密码进入 shell 历史和进程参数，推荐使用环境变量：

```bash
export TITOP_PROMETHEUS=production@http://10.0.0.10:9090
export TITOP_MYSQL_USER=monitor
export TITOP_MYSQL_PASSWORD='password'
./titop
```

`-u` 和 `-p` 必须同时提供。数据库账号需要能够读取以下系统表：

- `INFORMATION_SCHEMA.CLUSTER_PROCESSLIST`
- `INFORMATION_SCHEMA.CLUSTER_STATEMENTS_SUMMARY`
- `INFORMATION_SCHEMA.CLUSTER_TIDB_TRX`
- `INFORMATION_SCHEMA.CLUSTER_CONFIG`

> [!WARNING]
> **不要使用 `root` 用户或其他高权限管理账号运行 TiTop。** 请创建专用的只读监控账号，避免因凭据泄露、误操作或工具运行环境被入侵而危及整个集群。

实际所需权限会因 TiDB 版本和集群安全策略而异。请遵循最小权限原则，仅授予上述诊断系统表所需的读取权限；不要授予 `SUPER`、DDL、DML、用户管理或授权管理权限。建议同时限制该账号允许登录的来源地址，使用独立的强密码并定期轮换。完成授权后，应先验证账号只能读取监控所需信息，不能修改业务数据或集群配置。

## 命令行参数

| 参数 | 默认值 | 说明 |
| --- | ---: | --- |
| `--prometheus` | `TITOP_PROMETHEUS` | `[cluster@]Prometheus地址`，支持 URL 或 `IP:端口` |
| `-m` | 同上 | `--prometheus` 的简写 |
| `-u` | `TITOP_MYSQL_USER` | TiDB SQL 用户名 |
| `-p` | `TITOP_MYSQL_PASSWORD` | TiDB SQL 密码 |
| `--interval` | `5s` | 自动刷新间隔，最小 1 秒 |
| `--timeout` | `4s` | 单轮 Prometheus/SQL 查询超时 |
| `--long-query-threshold` | `10s` | 当前长查询判定阈值 |
| `--long-txn-threshold` | `1m` | 当前长事务判定阈值 |
| `--high-qps` | `50000` | 高 QPS 阈值 |
| `--high-tps` | `5000` | 高 TPS 阈值 |
| `--once` | `false` | 输出一次快照后退出 |
| `--plain` | `false` | 不清屏、不读取交互按键 |
| `--no-color` | `false` | 禁用 ANSI 颜色 |
| `--version` | - | 显示版本并退出 |

也可以设置 `NO_COLOR=1` 禁用颜色。

## 交互按键

| 按键 | 功能 |
| --- | --- |
| `i` | TiDB、TiKV、PD 全部节点 |
| `k` | TiKV 线程池 CPU |
| `s` | SQL 类型和活跃会话 |
| `w` | TiKV 请求耗时 |
| `p` | 暂停或继续自动刷新 |
| `Space` | 立即刷新 |
| `h` / `?` | 打开或关闭帮助 |
| `q` | 退出 |

## 页面说明

### Cluster Activity

第一行展示集群总体吞吐、延迟、连接、错误率和节点数。第二行展示依赖 SQL 连接的诊断状态：

- `LONG QUERY`：`CLUSTER_PROCESSLIST` 中非 Sleep 且运行时间达到阈值的会话数量。
- `LONG TXN`：`CLUSTER_TIDB_TRX` 中持续时间达到阈值的事务数量。
- `CLUSTER TLS`：通过 `CLUSTER_CONFIG` 综合检查 TiDB、TiKV 和 PD 的组件间 TLS 配置。

TLS 状态含义：

| 状态 | 颜色 | 含义 |
| --- | --- | --- |
| `ON` | 绿色 | 所有 TiDB、TiKV、PD 实例的 CA、证书和私钥配置完整 |
| `OFF` | 红色 | 所有相关配置均为空 |
| `INCONSISTENT` | 黄色 | 组件或实例间配置不一致，或证书配置不完整 |
| `UNKNOWN` | 黄色 | 查询成功，但没有返回足够配置项 |
| `N/A` | 默认色 | 未提供 SQL 凭据、连接失败或查询失败 |

### All Cluster Nodes

节点按 DOWN 优先、CPU 降序排列。TiKV 的 `LREAD/s` 和 `LWRITE/s` 来源于 `tikv_storage_command_total`，表示逻辑存储命令速率，不是物理磁盘 IOPS。

CPU 以单核为 100%：多线程进程可能超过 100%。RSS/HOST% 依赖 node_exporter 的主机内存指标；无法匹配主机时显示 `-`。

### TiKV Thread Pool CPU

线程池 CPU 使用 TiDB Dashboard 风格的明确 PromQL，从 `tikv_thread_cpu_seconds_total` 计算一分钟速率并按实例求和。例如 Unified Read：

```promql
sum(rate(tikv_thread_cpu_seconds_total{name=~"unified_read_po.*"}[1m])) by (instance)
```

显示值是线程池累计 CPU 核占用比例，而不是主机 CPU 百分比：`100%` 约等于持续占用一个 CPU 核，`400%` 约等于四个 CPU 核。

### SQL Types

该区域始终从 Prometheus 的 `tidb_executor_statement_total` 获取 SQL 类型 QPS，并按以下四类展示：

- DML / TXN
- DDL
- ADMIN
- OTHER

SQL 页面不会重复展示 TOP 5 TiKV Request，以便为 SQL 信息保留更多空间。

### Active SQL Sessions

提供 SQL 凭据后，TiTop 查询 `CLUSTER_PROCESSLIST`，并以实例和 digest 关联最近十分钟的 `CLUSTER_STATEMENTS_SUMMARY`。页面最多显示 30 个非 Sleep 会话，按执行时间降序排列。

`DIGEST_ID` 是由 TiDB 原始 digest 生成的 13 位稳定短标识，使用体验类似 Oracle SQL ID，但不与 Oracle SQL ID 等价。相同 digest 始终得到相同 `DIGEST_ID`。

会话内存达到 100 MiB，或磁盘临时空间达到 1 GiB 时，会话连接 ID 加粗标红。TiDB 偶尔可能返回无符号下溢的异常 MEM/DISK 值；TiTop 会将超出有效整数范围的值按零处理，避免整页查询失败。

## 颜色和阈值

- P99：达到 200 ms 显示黄色，达到 1 s 显示红色。
- 节点 CPU：达到 70% 显示黄色，达到 90% 显示红色。
- QPS/TPS：达到高负载阈值显示黄色；如果同时发生 P99 ≥ 1 s、执行错误或任一 TiDB CPU ≥ 90%，显示红色。
- 长查询/长事务：当前数量大于零显示红色。
- 节点状态：UP 绿色、DOWN 红色、未知状态黄色。
- Active Session ID：内存或磁盘临时空间超过阈值时显示红色。

这些颜色用于快速观察，不等同于完整告警策略。生产环境仍应使用 Prometheus Alertmanager 等系统配置持续告警。

## 构建

```bash
make test
make vet
make build
```

仓库包含 GitHub Actions CI；push 和 pull request 会自动检查格式、运行测试与 `go vet`，并验证主程序能够构建。

本机构建产物位于 `bin/titop`。

构建 Linux ARM64 和 AMD64 release：

```bash
make release
```

产物：

```text
release/titop-linux-arm64
release/titop-linux-amd64
release/titop                 # ARM64 兼容副本
```

覆盖版本号：

```bash
make release VERSION=0.9.0
```

## 单次输出和脚本使用

```bash
./titop -m 10.0.0.10:9090 --once --no-color
./titop -m 10.0.0.10:9090 --plain --no-color
```

`--once` 在 Prometheus 查询出现错误时以非零状态退出。SQL 增强查询失败会在对应区域显示 `N/A` 或错误信息，但不会阻止 Prometheus 页面继续工作。

## 已知限制

- Prometheus 当前不支持 Basic Auth、Bearer Token、客户端证书或自定义 CA。
- SQL 增强功能固定尝试 TiDB 端口 `4000`，尚无自定义 SQL 端口参数。
- TiDB SQL 客户端当前未提供 TLS/自定义 CA 参数。
- 指定的集群名只用于展示，不会自动添加 Prometheus 标签过滤器。
- 不同 TiDB/TiKV 版本可能缺少个别指标或系统表；独立查询失败不会影响其他 Prometheus 指标。
- 宽表需要较宽终端；字段过长时会按列宽截断。

## 故障排查

### Prometheus URL 无效

确认地址包含正确的主机和端口。`IP:端口` 会自动补充 `http://`，HTTPS 地址必须明确写出 `https://`。

### SQL 页面显示连接错误

确认：

1. Prometheus 返回了 UP 状态的 TiDB 实例。
2. 运行 TiTop 的主机能访问这些实例的 `4000` 端口。
3. 用户名、密码和系统表权限正确。
4. 集群没有要求当前客户端尚未配置的 SQL TLS。

### 指标为零或页面底部出现 WARN

先在 Prometheus 中直接执行相应 PromQL，确认指标名称和标签与当前 TiDB 版本一致。TiTop 会并发执行独立查询；某项失败不会丢弃整张快照。

### 终端颜色或布局异常

使用 `--no-color` 排除 ANSI 颜色影响，并增大终端宽度。重定向输出时推荐同时使用 `--plain --no-color`。

## 项目结构

```text
cmd/titop/             CLI、交互循环和终端渲染
internal/monitor/      PromQL 定义、并发采集和快照聚合
internal/prometheus/   Prometheus HTTP API 客户端
internal/tidbsql/      TiDB SQL 连接和诊断查询
internal/terminal/     TTY 原始模式和终端宽度
```

## 与 oratop 的差异

Oracle 的 ASH 和等待事件无法直接映射到 TiDB Prometheus 指标。TiTop 当前以 TiKV 请求累计耗时作为等待压力的近似视图，并在提供 SQL 凭据时使用 TiDB 集群系统表展示当前活跃 SQL 和事务状态。

## Roadmap

- 自定义 TiDB SQL 端口和 TLS 参数。
- Prometheus Basic Auth、Bearer Token、mTLS 和自定义 CA。
- 按 Prometheus `tidb_cluster` 标签过滤共享监控实例。
- TiKV 热点、Region、Raft、磁盘和 PD 调度视图。
- 交互式排序、过滤和可配置会话资源阈值。

## License

TiTop 采用 [Apache License 2.0](LICENSE) 开源。第三方依赖的许可证信息请参阅 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。

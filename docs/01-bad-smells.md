# 第一部分：cub-panel 坏味道扫描

扫描对象：`/workspace/cub-panel/src`，46 个 `.go` 文件，13,532 行。
行号证据格式 `相对路径:行号`（相对仓库根 `cub-panel/`）。
严重程度：**P0** 数据损坏/资金损失/安全漏洞/服务崩溃；**P1** 维护性或性能问题；**P2** 风格。

> 说明：本清单由机械化扫描（函数长度/嵌套深度脚本 + grep 取证）+ 人工阅读共同产出，
> 所有行号均已与实际文件核对。旧代码整体质量不低（SQL 已参数化、幂等有唯一索引兜底），
> 下列为真实存在的可改进项，不含臆测。

## 1. 重复逻辑（同一代码块 ≥3 处）

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S1-1 | 重复逻辑 | `src/internal/panel/handlers_snapshot.go:40`、`:61`、`:92`、`:118` | P1 | 4 处完全相同的 `s.jsonErr(w, http.StatusForbidden, "当前套餐未开通快照功能")`；抽出 `requireSnapshotQuota(w, inst)` 中间件/守卫函数 |
| S1-2 | 重复逻辑 | `src/internal/panel/handlers_billing.go:186-189`、`:298-301`、`:349-352` | P1 | 3 处 `errors.Is(err, store.ErrInsufficient)` → 文案分支；应由计费域统一返回 `EYVES-201` 并集中映射 HTTP 状态 |
| S1-3 | 重复逻辑 | `src/internal/panel/jobs.go:44-50`、`:66-70`、`:98-100`、`:131-135` | P1 | 4 处 `c, cancel := context.WithTimeout(...); …; cancel()`（部分用 `defer`、部分手工调用）；抽出 `withTimeout(ctx, d, fn)` 辅助 |
| S1-4 | 重复逻辑 | `src/internal/panel/handlers_billing.go:184-204`、`:296-313` | **P0** | 「扣款 → 调外部 → 失败退款」两处复制；且两处的退款调用都丢弃了 error（见 S6-1/S6-2）。应统一为 `billing.Service` 编排（本仓库第六部分已实现） |
| S1-5 | 重复逻辑 | `src/internal/panel/handlers_snapshot.go:315`、`:335` | P2 | 两处 `"仅 KVM 虚拟机支持挂载 ISO"` 校验；抽出 `requireVM(w, inst)` |
| S1-6 | 重复逻辑 | `src/internal/panel/handlers_billing.go:418`、`src/internal/panel/handlers_pay.go:105` | P1 | 充值上限魔法值 `100_000_00` 复制在两处；应为具名常量或配置项（`billing.max_amount_minor`） |

## 2. 函数长度 > 40 行

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S2-1 | 函数长度>40 | `src/internal/panel/server.go:276-429`（`funcMap`，155 行） | P1 | 模板函数表应按域拆分到多个 `Funcs()` 并在 `New` 处合并 |
| S2-2 | 函数长度>40 | `src/internal/agent/provision.go:313-429`（`Create`，118 行） | P1 | 拆为「解析 → 建设备 → 创建 → 等网络 → 置状态」五个 ≤30 行步骤 |
| S2-3 | 函数长度>40 | `src/internal/agent/console.go:39-153`（`handleConsole`，116 行） | P1 | WebSocket 编解码、resize、close 三块分离 |
| S2-4 | 函数长度>40 | `src/internal/store/alloc.go:67-173`（`Allocate`，108 行） | P1 | 抽取「选地址/选端口/写库」三个函数 |
| S2-5 | 函数长度>40 | `src/internal/panel/handlers_admin.go:1033-1133`（`handleAdminInstanceIP`，102 行） | P1 | 拆校验与下发 |
| S2-6 | 函数长度>40 | `src/internal/panel/handlers_admin.go:428-525`（`handleAdminPlanSave`，99 行） | P1 | 表单解析与落库分离 |
| S2-7 | 函数长度>40 | `src/internal/agent/server.go:388-485`（`validateCreate`，99 行） | P1 | 按字段族拆为多个子校验 |
| S2-8 | 函数长度>40 | `src/internal/panel/handlers_user.go:179-275`（`handleRedeemPost`，98 行） | P1 | 兑换码校验/占用/下发分离 |
| S2-9 | 函数长度>40 | `src/internal/panel/handlers_admin.go:1211-1296`（`handleAdminInstancePorts`，87 行） | P2 | 同上 |
| S2-10 | 函数长度>40 | `src/internal/panel/handlers_snapshot.go:208-292`（`runMigration`，86 行） | P1 | 迁移编排应下沉到 cluster 服务 |
| S2-11 | 函数长度>40 | `src/internal/panel/handlers_auth.go:127-204`（`handleRegisterPost`，79 行） | P2 | 校验与写库分离 |
| S2-12 | 函数长度>40 | `src/internal/panel/handlers_billing.go:138-213`（`handleDeployPost`，77 行） | **P0** | 与 S1-4 同源；整段应由计费服务接管 |
| S2-13 | 函数长度>40 | `src/cmd/panel/main.go:40-115`（`main`，77 行） | P2 | 配置装载/依赖装配/启动分离 |
| S2-14 | 函数长度>40 | `src/internal/panel/handlers_billing.go:254-322`（`handleInstanceUpgrade`，70 行） | P1 | 与 S1-4 同源 |
| S2-15 | 函数长度>40 | `src/internal/store/alloc.go:409-474`（`ClaimPorts`，67 行） | P2 | 抽取端口挑选 |

> 完整清单：仓库内共 55 个函数 > 40 行（占函数总数约 12%），上表为严重度最高的 15 个。

## 3. 嵌套深度 > 3 层

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S3-1 | 嵌套深度>3 | `src/internal/agent/provision.go:449-468`（`waitNetwork`，大括号深度 6） | P1 | 三重 for/if 轮询；改早返回 + 独立 `probeOnce` 函数 |
| S3-2 | 嵌套深度>3 | `src/internal/panel/handlers_admin.go:577-644`（`handleAdminImages`，深度 6） | P1 | 嵌套循环拼装镜像视图；抽 `buildImageVM` |
| S3-3 | 嵌套深度>3 | `src/internal/agent/console.go:39-153`（深度 5） | P1 | 与 S2-3 同源 |
| S3-4 | 嵌套深度>3 | `src/internal/panel/handlers_admin.go:1430-1486`（`handleAdminInstanceBatch`，深度 5） | P1 | 批量循环内嵌错误分支；抽 `applyBatchAction` |
| S3-5 | 嵌套深度>3 | `src/internal/panel/jobs.go:79-140`（`meterTraffic`，深度 5） | P1 | 循环 + 多分支 + 内层再循环；抽 `enforceQuota` |
| S3-6 | 嵌套深度>3 | `src/internal/panel/server.go:276-429`（`funcMap`，深度 5） | P2 | 与 S2-1 同源 |
| S3-7 | 嵌套深度>3 | `src/internal/store/store.go:145-170`（`rebind`，深度 5） | P2 | 手写状态机解析 `?`；可读性差，需注释或换实现 |
| S3-8 | 嵌套深度>3 | `src/internal/store/alloc.go:67-173`（`Allocate`，深度 4） | P1 | 与 S2-4 同源 |

## 4. 魔法数字 / 硬编码字符串

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S4-1 | 魔法数字 | `src/internal/store/alloc.go:356`、`:415` | P1 | `lo < 1024 \|\| hi > 65535` 端口边界硬编码；应定义为 `minPort`/`maxPort` 常量并可在 `config.yaml` 覆盖 |
| S4-2 | 魔法数字 | `src/internal/store/models.go:656` | P2 | `1024*1024*1024` 字节换算；用 `const bytesPerGiB` |
| S4-3 | 硬编码字符串 | `src/internal/store/schema.sql:37`、`:38` | P1 | `nat_bridge` 默认 `lxdbr0`、`nat_subnet` 默认 `10.180.0.0/24` 写死在 schema；应只作为配置默认值 |
| S4-4 | 魔法数字 | `src/internal/store/schema.sql:43-45` | P1 | `port_min=20000`、`port_max=60000`、`ports_each=10` 写死在 schema 默认值 |
| S4-5 | 硬编码字符串 | `src/internal/lxd/client.go:514` | P1 | `PATH="/usr/local/sbin:/usr/local/bin:…"` 硬编码；应可配置，且不应假定宿主目录布局 |
| S4-6 | 魔法数字 | `src/internal/store/store.go:53-54` | P2 | SQLite pragma `busy_timeout(10000)`、`synchronous(NORMAL)`、`MaxOpenConns(8)` 硬编码；应读配置 |
| S4-7 | 魔法数字 | `src/internal/panel/jobs.go:14-18` | P1 | 5 个后台任务周期 `2m/5m/5m/1h/1h` 硬编码；应读 `cluster.probe_interval` / `billing.renewal_scan_interval` 等 |
| S4-8 | 魔法数字 | `src/internal/panel/handlers_billing.go:418`、`src/internal/panel/handlers_pay.go:105` | P1 | 充值上限 `100_000_00`（见 S1-6） |
| S4-9 | 魔法数字 | `src/internal/panel/handlers_billing.go:57` | P2 | 金额上限 `1<<40`；应为具名常量，且与 S4-8 合并为同一上限 |
| S4-10 | 硬编码字符串 | `src/internal/shared/proto.go:26` | P2 | `MaxClockSkew = 90 * time.Second`（常量，可接受）；但 40 处 `"元"`/`"分"` 文案与 `"CNY"` 语义分散在 `handlers_billing.go:187`、`:357` 等，币种未建模 |

## 5. 命名不清晰

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S5-1 | 命名不清晰 | `src/internal/store/models.go:716`、`:745`、`:881` | P1 | 3 处 `var nn, nh, ue string`（node name / node host / user email）；改 `nodeName, nodeHost, userEmail` |
| S5-2 | 命名不清晰 | `src/internal/panel/handlers_billing.go:140`、`:261`、`:330`、`:374` | P2 | `ac := userFrom(r)`，`ac` 含义不明；改 `account` |
| S5-3 | 命名不清晰 | `src/internal/panel/handlers_billing.go:364`、`src/internal/panel/handlers_pay.go:91` | P2 | `var b [24]byte` / `var b [8]byte`；改 `keyBytes` / `orderNoBytes` |
| S5-4 | 命名不清晰 | `src/internal/store/store.go:242` | P2 | `s := err.Error()` 遮蔽语义；改 `msg` |
| S5-5 | 命名不清晰 | `src/internal/store/alloc.go:272` | P2 | `u := &usage{…}`；改 `used` |

## 6. 缺少错误处理

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S6-1 | 缺少错误处理 | `src/internal/panel/handlers_billing.go:200-201` | **P0** | 开通失败退款 `_, _ = s.db.AdjustBalance(...)` 完全丢弃错误：退款失败时用户余额已被吞且无人知晓。必须向上报错并告警 |
| S6-2 | 缺少错误处理 | `src/internal/panel/handlers_billing.go:310` | **P0** | 升级失败退款 `_, _ = s.db.AdjustBalance(...)`，同上 |
| S6-3 | 缺少错误处理 | `src/internal/panel/handlers_pay.go:257` | P1 | `_ = s.db.SetOrderTxID(...)` 丢弃错误：用户提交 USDT TxID 失败后管理员看不到，形成资金纠纷 |
| S6-4 | 缺少错误处理 | `src/internal/store/models.go:921` | P1 | `Audit` 内部 `_, _ = d.ExecContext(...)` 吞掉审计写失败，审计链可静默断裂 |
| S6-5 | 缺少错误处理 | `src/internal/store/email_verify.go:39` | P1 | `_, _ = d.ExecContext(... DELETE ...)` 吞掉错误，可能残留旧验证码 |
| S6-6 | 缺少错误处理 | `src/internal/panel/handlers_pay.go:189` | P2 | `_ = r.ParseForm()`；解析失败后 `r.Form` 为空，网关回调必失败但无日志 |
| S6-7 | 缺少错误处理 | `src/internal/panel/handlers_billing.go:314`、`:317` | P1 | `_ = s.db.ResizeInstance(...)`、`_ = s.db.SetInstanceTraffic(...)`：DB 与实例实际规格可能不一致 |
| S6-8 | 缺少错误处理 | `src/internal/lxd/client.go:398`、`:400` | P2 | `raw, _ := io.ReadAll(...)`、`_ = json.Unmarshal(raw, &resp)`：错误响应体解析失败被吞，排障困难 |
| S6-9 | 缺少错误处理 | `src/internal/panel/jobs.go:46`、`:48`、`:71`、`:115`、`:117`、`:136`、`:153`、`:158` | P1 | 后台任务链式 `_ = s.db....`，任务静默失败且无指标；应记 `slog` 并计数 |
| S6-10 | 缺少错误处理 | `src/internal/store/store.go:229` | P2 | `defer func() { _ = raw.Rollback() }()`；常规模式，但回滚失败会被忽略（可接受，建议记 debug 日志） |
| S6-11 | panic 兜底 | `src/internal/panel/security.go:29`、`:51`、`src/internal/panel/handlers_admin.go:819` | P1 | **初版清单此处有误，复核后修正**：实际 3 处 `panic("crypto/rand failed: …")`。`randToken`/`randomPassword` 是登录、CSRF、会话、root 密码的公共依赖，熵源故障时整个面板进程崩溃（P1 而非"无问题"）。应改为返回 `(string, error)` 并向上抛 `EYVES-002` |
| S6-12 | 缺少错误处理 | `src/internal/panel/jobs.go:88` | P1 | `s.provision(...)` 以 detached goroutine 调用且**无返回值契约**：下发失败只能靠 DB 里一行 status 体现，无错误码、无告警、无重试 |

## 7. 全局可变状态

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S7-1 | 全局可变状态 | `src/internal/shared/proto.go:116` | P1 | `var AllowedFeatures = map[string]bool{…}` 导出且可变：任何包都能改写特性白名单（`privileged` 开关），是提权面。应改为不可导出 + 只读访问器，且白名单下沉到被控端最终裁决 |
| S7-2 | 全局可变状态 | `src/internal/store/alloc.go:17` | P1 | `var allocMu sync.Mutex` 包级锁：跨所有 `*DB` 实例串行化分配，隐藏了真实并发语义，且无法按节点分片 |
| S7-3 | 全局可变状态 | `src/internal/shared/version.go:6` | P2 | `var Version = "dev"` 可变；应为只读注入（`-ldflags`）且不导出写入口 |
| S7-4 | 全局可变状态 | `src/internal/panel/security.go:374` | **P1** | **初版清单此处有误，复核后修正**：`var trustProxy bool` 是包级可变开关，却决定 `clientIP()`（`security.go:357`、`:360`）是否信任 `X-Real-IP`/`X-Forwarded-For`。启动后任何同包代码改写它即等于伪造来源 IP，审计与风控全失效。应改为 `Server` 字段或以依赖注入传入 |
| S7-5 | 全局可变状态 | `src/internal/panel/agentclient.go:30`、`:34` | P1 | `var agentHTTP = &http.Client{Timeout: 20 * time.Minute}` 与 `var tlsClients sync.Map`：包级共享 HTTP 客户端 + 全局连接池 map，无法按节点独立配置超时/并发，测试时无法替换 |
| S7-6 | 全局可变状态 | `src/internal/agent/images.go:118`、`src/internal/agent/iso.go:121` | P2 | `var ssHTTP`（30s）、`var isoHTTP`（60m）包级客户端；超时硬编码（与 S4 交叉） |
| S7-7 | 全局可变状态 | 全仓库 | — | 复核修正：包级 `var` 实为 **31 处**（初版称 8 处）。其余为只读值（`regexp.MustCompile` 17 处、`errors.New` 5 处、`embed.FS` 2 处、`websocket.Upgrader` 2 处等），无并发写风险，不计入坏味道 |

## 8. 隐藏依赖（跨模块直接调用未走接口）

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S8-1 | 隐藏依赖 | `src/internal/agent/snapshot.go:28`、`src/internal/agent/storage.go:46`、`src/internal/agent/iso.go:159` | **P0** | agent 直接持有 `*lxd.Client`（具体类型）并逐方法调用，全仓库 30+ 处。**这是"只支持 Incus"的根因**：新增 KVM/OpenVZ 必须改遍所有调用点。必须引入 `Hypervisor` 接口（见第二部分） |
| S8-2 | 隐藏依赖 | `src/internal/panel/handlers_billing.go:76`、`src/internal/panel/handlers_admin.go:54` | P1 | panel 直接依赖 `*store.DB` 具体类型（`s.db.UserByID` 等），无 Repository 接口；无法单测、无法替换存储 |
| S8-3 | 隐藏依赖 | `src/internal/panel/handlers_billing.go:308`（`agentResize`）、`src/internal/panel/agentclient.go:106-150` | P1 | 计费流程直接调用包级函数 `agentResize`/`agentAction`，绕过任何接口，导致「扣款-下发-退款」无法被 mock 测试 |
| S8-4 | 隐藏依赖 | `src/internal/agent/snapshot.go:180` | P2 | 直接构造 `lxd.InstancePut` 类型，把 LXD 线格式泄漏到 agent 业务层 |

## 9. SQL 拼接（非参数化查询）

> 结论：**未发现可利用的 SQL 注入**。全部业务语句使用 `?` 占位并由 `rebind` 适配方言
> （`src/internal/store/store.go:143-171`），用户输入从不进入 SQL 文本。以下为"脆弱拼接"，
> 不是注入，但属于坏味道。

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S9-1 | SQL 字符串改写 | `src/internal/store/store.go:310` | P1 | `stmt = strings.ReplaceAll(stmt, "TEXT NOT NULL DEFAULT", "VARCHAR(1024) NOT NULL DEFAULT")`：对 SQL 做文本替换做方言适配，一旦原文措辞变化即静默错配；应为每方言各写一份迁移语句 |
| S9-2 | SQL 拼接 | `src/internal/store/store.go:192` | P2 | `query+" RETURNING id"` 拼接；无用户输入，可接受，建议加注释约束 |
| S9-3 | SQL 分支拼接 | `src/internal/store/balance.go:90`、`src/internal/store/orders.go:144` | P2 | `q += " WHERE user_id = ?"` 形式：拼接的是语句结构，参数仍走占位，安全；但 `orders.go:144` 的 `onlyPending` 布尔开关应改为显式 `Status` 参数以消除分支 |
| S9-4 | SQL 标识符拼接 | `src/internal/store/models.go:953`、`:968` | P2 | `settings` 表的 `` `key` `` 反引号为 MySQL 专用；跨方言靠 `ReplaceAll`（见 S9-1）兜底，脆弱 |

## 10. 并发安全

| 编号 | 坏味道类型 | 文件:行号 | 严重程度 | 建议处理方式 |
|---|---|---|---|---|
| S10-1 | 无实例级互斥 | `src/internal/panel/jobs.go:79-140` 与 `:56-74` | P1 | `meterTraffic`（超流量停机）与 `reapExpired`（到期停机）可能在同一 tick 对同一实例并发下发 `stop`；无实例级锁，且状态写法都是无条件覆盖，存在状态覆盖竞争 |
| S10-2 | 全局锁掩盖并发 | `src/internal/store/alloc.go:17`、`:67`、`:409` | P1 | `allocMu` 全局串行（见 S7-2）：单次分配持锁跨多表读写，多节点扩容后成为瓶颈 |
| S10-3 | 无锁的状态写入 | `src/internal/panel/jobs.go:71`、`:136` | P1 | `_ = s.db.SetInstanceStatus(...)` 无条件覆盖 status：与用户侧 action 并发时可能把 `running` 覆盖成 `expired` |
| S10-4 | SQLite 写竞争 | `src/internal/store/store.go:53`、`:69` | P2 | 5 个后台 goroutine + 请求处理共 8 连接写单文件 SQLite，仅靠 `busy_timeout(10000)` 兜底；应显式串行化写路径或收敛连接数 |
| S10-5 | 裸 goroutine | `src/internal/panel/handlers_user.go:612`、`src/internal/panel/handlers_admin.go:674`、`:1675`、`src/internal/agent/update.go:75`、`src/cmd/panel/main.go:101`、`src/cmd/agent/main.go:97`、`src/internal/panel/console.go:69`、`src/internal/agent/console.go:114` | **P1** | **初版清单此处有误，复核后修正**：实际 **8 处** `go func()`（初版称 0 处）。其中 4 处用 `context.Background()` 重新起超时（`handlers_user.go:613`、`handlers_admin.go:675`、`:1676`），**丢弃了请求 ctx 的取消/超时/trace_id 传播**；全部 8 处**无 `recover()`**，任一 panics 直接杀进程（叠加 S6-11 的 3 处 panic，是可复现的可用性风险）。修复：统一 `s.background(ctx, timeout, fn)`，内部 `defer recover` + `slog` 记 `trace_id` |
| S10-6 | map 并发读写 | 全仓库 | — | 已确认无包级 map 被并发写；`jobs.go:84` 的 `nodes` map 为函数局部且单 goroutine 访问，安全 |

## 汇总

| 严重程度 | 数量 | 关键项 |
|---|---|---|
| P0 | 5 | S6-1、S6-2（退款错误被吞，资金风险）、S1-4/S2-12（扣款-退款重复逻辑）、S8-1（直接依赖 `*lxd.Client`，无法多 Hypervisor） |
| P1 | 36 | 重复逻辑 5、长函数 10、嵌套 6、魔法值 7、错误处理 7、全局状态 4、隐藏依赖 3、SQL 脆弱 1、并发 3（部分行重叠计数见各行） |
| P2 | 18 | 命名 5、风格与脆弱拼接 13 |

> 复核说明（第二轮）：初版第 6/7/10 节各有一条"全仓库 0 处/8 处"的乐观结论被机械
> 复核推翻（`panic(` 实为 3、包级 `var` 实为 31、裸 `go func()` 实为 8），已按实际行号改写为
> S6-11、S7-4~S7-7、S10-5，并新增 S6-12。所有行号均可用 `sed -n 'Np' <file>` 逐条复现。
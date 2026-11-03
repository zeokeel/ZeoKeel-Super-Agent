# Design review r2 — UI / Workspace / IM / DWS（架构级，只读）

> 角色：conf-arch（设计评审）。**只读**：未改生产代码、未新增依赖、未提交；本文件是唯一交付物。
> 快照：2026-11-04 00:07–00:4x（Asia/Shanghai），HEAD `f4200da`（"workspace-centric panel, native folder picker, IM channel manager + DWS, built-in tools"），工作树 clean。
> 对照：`audit/fixes/ui2-spec.md`、`audit/fixes/im-channel-spec.md`、`internal/{ui,view,runtime,engine,im,scan}`、`cmd/zeokeel`、`internal/view/viewtest`。
> 方法：静态阅读 + 一次黑盒行为探针（用当前树构建的 `zeokeel`、临时 state dir、直接写 kv 的 `setting:source.feishu_enabled`，以 `-headless` 跑 3s 后读 health 行）；无显示器、未开窗口；本机无 `dws`/钉钉/飞书真实凭据，DWS 契约按"外部命令假设"评审。

---

## 0. 结论

**方向正确、行为大体诚实，但有 3 个结构性缺口，其中 2 个会直接让本轮主打功能在真实运行中不可用/卡死：**

1. **配置编码没有单一真相**：设置页开关写 `"true"/"false"`，IM 管理器只认 `"1"`。→ 在 Settings 里打开飞书/钉钉**永远不会启动监听**（黑盒探针已复现），而面板同时显示"已启用"。
2. **命令边界仍是同步 RPC**：UI 帧内直接调用 `Dispatch`/`NewConversation`/`CreateWorkspace`/`SaveSettings`，这些路径包含模型评估（评估器超时 90s）、SQLite 写、IM 停启（每渠道≤5s）、飞书 `Self()` 网络（HTTP 客户端 30s）。接口注释写的"只入队、不等待文件/数据库"与实现相反。
3. **面板与投影的 Ticket 身份契约未定义**：投影的行 ID 是"编号 42"，而详情/会话 ID 是 32 位 domain ID；UI 用行 ID 查详情/跳会话。→ 真实运行时**Ticket 详情永远回退为空/短投影、点会话跳不到工单**；`viewtest` fixture 恰好两边都用 `"42"` 掩盖了它。

计：**P0 ×2，P1 ×6，P2 ×7**。建议按 §6 的 5 个设计改动收敛，不要逐点打补丁。

---

## 1. Findings 总表

| ID | 严重度 | 区域 | file:line | 为什么是错的 | 后果 | 建议 |
| --- | --- | --- | --- | --- | --- | --- |
| D-01 | **P0** | IM 配置 | `internal/runtime/im.go:54,60`；`internal/ui/settings.go:355-367`；`internal/ui/channels.go:162,378-382` | 开关编码两套：UI 写 `FormatBool` → `"true"/"false"`；管理器只把 `"1"` 当启用。没有共享的 settings codec/类型（键值全是 `map[string]string`）。 | **在设置页启用任意 IM 渠道都是 no-op**；面板显示"已启用/未检查"，运行时 health 是 `im:feishu ready/未启用`。旧 flags 用户也没有任何写 `"1"` 的路径（无 `--feishu-enabled`、setup 无 IM 开关），升级后静默不监听。探针见 §2.1。 | 建 settings schema + 单一解码器（`ParseBool` 接受 `1/true`，保存时归一化）；UI/runtime 共用；加跨层编码测试 + flags→启用迁移。 |
| D-02 | **P0** | 命令边界/主线程 | `internal/ui/input.go:42`；`ui/workspace.go:565-568,637-644`；`ui/settings.go:515`；`runtime/runtime.go:752-783`；`runtime/workspaces.go:42-71,128-132`；`runtime/pages.go:514-600`；`runtime/im.go:239-318` | UI 帧内同步调用运行时：`Dispatch/NewConversation` 内联 `EvaluateDue`（评估器超时 90s：`internal/assess/eino.go:31`）；`CreateWorkspace` 内联 `scan.Scan`+DB 写+Publish；`SaveSettings` 内联 store 写 + `ApplyIMSettings`（停渠道≤5s/渠道，且 `Self()` 走网络：`im/feishu/driver.go:135-154`、`transport.go:178` 30s）。接口自述相反：`ui/app.go:47-55`（"must only enqueue… must not wait for a file or a database"）、`ui/input.go:38-40`（"Dispatch is memory-only"）、`ui/settings.go:486-488`。 | 发一句话/存一次来源/新建工作区就可能冻结窗口数秒到 90s；`WM_NOT_RESPONDING`、命令排队、用户重复点击；IM 重启期间的 5s 等待直接落在帧里。 | 引入异步命令通道（submit→pending→result via `postToUI`/snapshot）；`SettingsSaver`/`Commander` 改成不等待语义；所有 I/O+模型调用移到 runtime worker；UI 显示 pending 与拒绝原因。 |
| D-03 | **P1** | 投影↔面板身份 | `runtime/pages.go:288,296`（row.ID=编号，DetailVMs 键=domain ID）；`ui/ticketdetail.go:24-46`；`ui/execution.go:148-160`；`runtime/pages.go:981-999`（Conversation.ID=domain ID）；`view/viewtest/fixtures.go:92-98`、`fixtures_pages.go:457` | 同一个 `string` 身份有两种值：`TicketRow.ID`=Number（"42"），`TicketDetailVM`/`ConversationVM.ID`=32 位 domain ID。UI 以行 ID 查 `DetailVMOf/DetailOf`、以会话 ID 调 `openTicket`（后者只匹配行 ID）。契约只在 fixture 里"碰巧对"（fixture 把 Conversation.ID 写成 "42"）。 | 真实运行：Ticket 详情（Mandate/Inputs/Follows/Artifacts/Attention）**永远不显示**（两条查表都 miss，退回短投影/空）；点会话→执行页报"工单 #<hex> 不在当前投影"、不选中不展开；搜索/详情同源问题。单测因 fixture 全部通过（`ui/workspace_test.go:112-129`）。 | 统一 Ticket 身份：行用 `Ticket.ID`，编号只做显示；`DetailVMOf`/`Details`/会话跳转同键；用一个**真实 projector→snapshot→UI** 的 seam 测试（不用 viewtest）钉住；fixture 改成真实形态。 |
| D-04 | **P1** | DWS 传输契约 | `audit/fixes/im-channel-spec.md:35`；`runtime/dws.go:31-33,58-94`；`runtime/im.go:409-461`；`cmd/zeokeel/sender.go:34-49`；`im/dingtalk/dws.go:586-711,714+` | 规格要求"每渠道一个串行 worker（poll/send/auth 同队列、send 优先）"。串行器 `dwsRunner` 与 `DWSTransport` 确实实现并有测试，但**生产路径完全没接**：`dwsPoller` 直接拿 `DWSClient`，poll 在 `pollLoop`，send 在 outbox 的发送 goroutine，`CheckChannel` 每次 new 一个 `commandDWSClient`（`runtime/im.go:597-601`）→ 三个子进程可并发。 | 登录态/子进程互相竞争（DWS 契约明确要避免）；测试覆盖的是死代码，给出假信心；排查时"规格说串行、线上不串行"。 | 只保留一个 DWS 适配器：把 runner/queue 下沉到 `DWSClient` 或 runtime 的每渠道 worker，poll/send/auth 全走它；删掉未使用的 `DWSTransport`/`dwsRunner` 或将其提升为唯一实现；测试打在活动路径上。 |
| D-05 | **P1** | 渠道生命周期/所有权 | `runtime/im.go:173-198,208-221,239-318,262-271`；`cmd/zeokeel/main.go:301`；`cmd/zeokeel/imwiring.go:39-51`；`cmd/zeokeel/sender.go:34-48` | `ApplyIMSettings` 无每渠道串行/代际控制：`applyChannel` 删旧→等≤5s→建新→插入 map；与 `Stop`/另一次 Apply 并发时可重复启动（旧 goroutine 仍在跑而 map 只留新的）；`Stop` 不清 `m.ctx`，晚到的 `applyChannel` 还能在 shutdown 后起渠道；`main` 忽略 `startConfiguredIM` 返回值，**`IMManager.Stop` 从未被调用**；禁用一个渠道时提前 return，**不注销已注册的 sender**（`sender.ims` 仍保留旧驱动）。 | 双监听/重复处理、shutdown 后残留 goroutine；禁用后 outbox 仍向旧驱动发送（"已禁用"却还在尝试发），不是诚实的"未监听"。 | 渠道状态机 + generation token（只有最新代际可写 map/状态）；每渠道 mutex/命令队列；`Stop` 幂等且从 main 调用；disable/stop 时注销 sender 并让发送返回明确的"渠道未启用"。 |
| D-06 | **P1** | 配置优先级/迁移 | `cmd/zeokeel/imwiring.go:39-51`；`runtime/im.go:51-67,90-108,685-696` | flags 只在 boot overlay 一次（`WithFlags`），之后来源组保存按 **settings-only** 重算（`imAfterSettingsSave`→`IMConfigFromSettings`）→ 用 flag 临时提供凭据的用户，一旦保存**该渠道自身字段**（触发 diff → 重启），新进程用库里的旧/空凭据 → 半配置错误；只在 flag 里配的老安装升级后更是默认 disabled（下表 D-01 的编码问题之外，这里连"能写 enabled"的路径都没有）。同时启用与否只来自 settings（无 enable flag）、默认 disabled，旧版本"给 flag 就监听"的安装升级后静默不监听；DingTalk transport 默认从 Stream 改为 DWS（`im.go:61`），老 flags 用户会先遇到"dws 未安装"。 | 升级=IM 静默停摆；flag 凭据在保存后丢失；DWS/Stream 行为突变。 | 一个配置解析器：persisted overlay（flags 存进 manager）→ 每次 Apply 都叠加；增加 `--feishu-enabled/--dingtalk-enabled`；迁移规则：有旧凭据 flag 且无 enabled 键 → 视为 enabled 并写一次迁移 + 日志；旧 DingTalk 凭据默认 transport 兼容。 |
| D-07 | **P1** | 设置保存一致性/CAS | `runtime/pages.go:531-566,568-582,599` | 先改内存 `r.settings`、先 `settingsRev++`，再写 store；store 冲突/失败时**不回收**内存与 rev（返回的是已经 ++ 的 rev）。下一次保存的 base 与 store 对不上 → 持续报"别的窗口改过"；2s pass 的 `Publish` 还会把未落库值投影出去（UI 接受编辑、显示已保存）。 | 保存失败 → 本次显示成功、重启丢失；CAS 自锁（只能重启/重载页面）；多窗口语义被污染。 | 事务化设置写：在副本上合并→store commit 成功→原子换内存/rev；失败保持原值并返回原 rev；`Publish` 只读已提交快照。 |
| D-08 | **P1** | 入口路径/两阶段 Eval | `runtime/entry.go:18-53`；`runtime/im.go:445-449,491-493`；`cmd/zeokeel/github.go`/`extras.go` 同路径 | `ReceiveTrigger` 在持久化后立即 `EvaluateDue`（模型，90s）并 `Publish`。这被 Source 的 poll 循环直接调用：IM 每批事件的处理时间=模型延迟；轮询/游标推进被模型拖住，单渠道期间无法继续收消息。与"Receive 只入队、2s pass 判定"的 Wave2 两阶段设计及 `entry.go` 自己的注释相矛盾。 | 忙模型时 IM 收件停摆、积压；pollLoop 无法按 2s 节奏；为 D-04 的并发又加一层不可控。 | 入口只 `persist+enqueue` 并返回；由 2s pass/专用 eval worker 判定（有界队列+单飞）；需要"立即判定"的只有面板命令，且也应异步。 |
| D-09 | P2 | 投影成本/上限 | `runtime/runtime.go:143-330`（Publish 重建全部 shard）；`runtime/pages.go:268-308`（ticketsVM 全量 rows+每 Ticket 详情）；`runtime/runtime.go:427+`（stream 遍历全部 Feed）；`runtime/pages.go:953-978`（每 workspace 全 Ticket 扫 + 每人 50 截断） | 每 2s + 每个命令都全量重建投影；Feed/票/详情无总量上限；per-workspace 50 是静默截断（无"更多"）。 | 长跑安装（10k+ triggers/tickets）后每次 Publish 越来越贵，落在 UI 帧与 poll goroutine；侧边栏/执行页无界内存；用户不知道列表被截断。 | 投影分片+增量/节流；Feed 与执行页保留最近 N（可下钻），详情按需投影；截断处显示"仅显示最近 50 条"。 |
| D-10 | P2 | 工作区数据模型 | `runtime/workspaces.go:20-72`；`onboarding.go:234-248`（slug）；`engine/engine.go:143-152`（Register 覆盖）；`ui/workspace.go:316-445` | 只有 create：无 rename/delete/改根；不检查重复 root（两个工作区可指向同一目录）；ID 来自 ASCII slug，非 ASCII 名一律 `"workspace"` → 第二个中文名工作区因 ID 冲突报"已经存在"（错误信息误导）；`Engine.Register` 同 ID 静默覆盖（store 层无唯一约束）。 | 中文用户基本无法建第二个工作区；误建/改名无路可走；同一 root 双工作区并发执行器互踩风险；迁移/清理只能手改 DB。 | 稳定不透明 ID（ULID/UUID）；store 层 name、root 唯一约束；rename/delete（有在跑 Ticket 时拒绝/迁移）；非 ASCII 名不再参与 ID。 |
| D-11 | P2 | 作用域/所有权语义 | `view/workspace.go:52-58`（Selected）；`runtime/pages.go:953-978`（不填 Selected）；`ui/workspace.go:294-345`；`runtime/pages.go:981-999`（CreatedBy） | `Selected` 是死字段（投影不填），无选择时"工作区"页显示"还没有工作区"；侧边栏选择只影响工作区页高亮，**消息/执行页不做 workspace 过滤**（`workspaceSel` 只在 sidebar/workspace.go 出现）；会话所有权只有 `me/agent` 二分：只有 `Source=="local"` 算"我"，同事的 IM DM、甚至操作员自己 IM 发的消息都标"超级Agent 创建的"。 | 树选中与右侧列表语义脱节；"我创建的"在 IM 场景失真；用户无法按工作区看执行列表。 | 投影 `Selected`；无选择默认第一个 workspace/超级Agent；明确 消息/执行 是否全局（若全局则 UI 标注"全部工作区"），或加 workspace 过滤；`CreatedBy` 用 `Trigger.Human/Self/Source` 三态（我/同事/超级Agent）。 |
| D-12 | P2 | outbox/错误分类 | `cmd/zeokeel/sender.go:34-49`；`im/dingtalk/dws.go:222-238,255-301` | `IsPermanent`/`ErrDWSAuthRequired` 实现了但发送路径完全不看：命令缺失、401、参数非法都按可重试错误返回 → outbox 永久退避重试、永不 `gone`；`opError` 把调用方取消（shutdown）标 `Permanent`，若在关闭时丢出会让 outbox 行永久失败（回执丢）。轮询错误能进 health，但"回不出去"只有 backlog。 | 配错凭据/未装 dws 时发送不断重试，日志脏、队列不收敛；关闭瞬间可能丢一条回复。 | sender 映射：永久/认证错误 → `OutcomeGone`/held（面板给可操作提示），可重试才退避；`context.Canceled` 视为可重试（outbox 跨重启）；把 `ErrDWSAuthRequired` 升为渠道 auth 状态。 |
| D-13 | P2 | DWS 契约假设 | `im/dingtalk/dws.go:305-334,398-429,458-484,486-535,537-550`；`audit/fixes/im-channel-spec.md:24-35` | 契约是自定义的，实现有若干脆弱假设：错误状态靠**正则**从 stderr/stdout 猜 3 位码（DingTalk 4/5 位 code 会被截成 400/500 → 误判永久/可重试）；`at` 必须 RFC3339，格式稍变整条丢弃；`page.Skipped` 丢弃坏 item 但 runtime 不展示（只有"轮询正常 N 条"）；marker 是 HTML 注释，钉钉纯文本可能原样显示；transport 切换（dws↔stream）**不重置 cursor**（`runtime/im.go:224-236` 只重启，不清 `im.cursor.<ch>`）；stderr 只截断不脱敏（规格要求去密钥：`im-channel-spec.md:25`）。 | 错误分类错误→重试风暴或错误永久；消息静默丢；用户看到 `<!-- zeokeel:… -->`；切 transport 后可能漏消息；health detail 泄密。 | 与 DWS 侧约定结构化错误（exit code + JSON error code）再解析；`at` 兼容多个格式；health 显示 skipped；marker 用不可见字符或读回校验；transport 变更时清 cursor；detail 做 secret 脱敏。 |
| D-14 | P2 | IM 状态/授权语义 | `runtime/im.go:585-605`；`ui/channels.go:244-286,292-310`；`runtime/pages.go:455-479` | "检查通道"只验证凭据（飞书 token、DWS auth status），不代表长轮询/事件订阅可用；面板 chip 由"设置里的启用意图 + 最近一次检查"合成，不读运行时 sources 的真实监听状态；飞书 transport 选项来自 settingsVM 的 `{"longpoll","ws","webhook"}`，而 `channelTransport` 把 runtime 发布的选项**一律标 `Implemented=true`**（`ui/channels.go:292-310`），与 `view/channels.go:9-11`、规格"未实现项不可选"冲突；渠道挂了/死了没有"重连"动作（只有改动配置才可能重启，且 diff 无变化时 Apply 不动）。 | 面板可能显示"检查通过/已启用"但实际未监听；用户能选一个跑不了的传输；通道永久 error 时需要靠改配置或重启整个 app。 | 向 UI 发布单一渠道状态（desired/actual/auth/lastError/lastPoll），卡片只画这个；未实现选项禁用；加"重连/重启渠道"命令；检查里做一次轻量 poll probe。 |
| D-15 | P2 | 秘密投影 | `runtime/pages.go:455-479`（SettingsVM 带明文 secret）；`view/channels.go:20-23`（注释称"panel never holds the credentials"） | 秘密值随 SettingsVM 进入内存快照、UI 状态与任何后续投影消费方（截图/调试/将来导出），与代码自己的分层承诺相反；`truncateDetail` 不脱敏（D-13）。 | 凭据扩散面变大；一旦有人加"导出设置/截图"功能即成泄漏。 | 投影只给 `configured: true/false` + 掩码；运行时就地读取；保存时单独 secret 通道。 |

---

## 2. P0 证据

### 2.1 D-01：设置页启用 → 运行时"未启用"（黑盒复现）

代码链：
- UI 开关：`internal/ui/settings.go:355-367`（`*ptr = strconv.FormatBool(on)`）、`internal/ui/channels.go:378-382`（`boolText` 返回 `"true"/"false"`）；测试也断言写 `"true"`（`internal/ui/channels_test.go:156`）。
- 运行时解析：`internal/runtime/im.go:54,60` 只认 `== "1"`。
- 面板"启用"判断同样用 `== "true"`（`ui/channels.go:162,175`），而 `channelDetail` 在启用但未检查时显示"已启用；还没有检查过…"（`ui/channels.go` 末段）→ 与运行时相反。

探针（当前树构建，未改代码）：
```sh
go build -o /tmp/zeokeel-r2 ./cmd/zeokeel
# state dir A：kv 写 setting:source.feishu_enabled = "true"（设置页会写的值）
zeokeel-r2 setup --state-dir $A --model mock --base-url http://127.0.0.1:1 --workspace web
sqlite3 $A/zeokeel.db "INSERT OR REPLACE INTO kv(k,v) VALUES('setting:source.feishu_enabled','true');"
zeokeel-r2 -headless --state-dir $A &  sleep 3;  kill %1
# health 行（读同一 db）：
#   im:feishu  ready  未启用        ← 没有监听（半配置本应 error/配置不完整）
#   im:dingtalk ready 未启用
# state dir B：同值写 "1"：
#   im:feishu  error  飞书 配置不完整：缺少 app id、app secret   ← 说明 "1" 才会走到启动判定
```
结论：这是**跨模块编码不一致**，不是单点笔误——`feishu_enabled` 同时被 UI（`"true"`）、runtime（`"1"`）、fixture（`viewtest/fixtures_pages.go:472` 用 `"true"`，demo 看起来正常）三种方式解释。规格 `im-channel-spec.md:14` 只写"switch 默认 false"，没定义编码——缺的是 settings schema/codec，而不是补一行 if。

### 2.2 D-02：主线程阻塞（最快复现路径与最大阻塞时长）

UI 帧内同步调用 → 运行时重活：
- 发送一句话：`ui/input.go:42` → `runtime.Dispatch`（`runtime/runtime.go:752-783`）→ `p.Dispatch`/`eng.Dispatch` + **`EvaluateDue`**（assessor `internal/assess/eino.go:31` 默认 `Timeout=90s`）+ `Publish`（全投影）。注释 `ui/input.go:38-40` 说"Dispatch is memory-only"已不成立。
- 新建会话：`ui/workspace.go:637-644` → `runtime/workspaces.go:101-156`：durable 写 + `EvaluateDue` + `wakeTicket` + `Publish`；点"创建会话"后只要模型慢，窗口就无响应。
- 新建工作区：`ui/workspace.go:565-568` → `runtime/workspaces.go:20-72`：`os.Stat` + `scan.Scan`（`scan.go:31-47`，含 `lastModified` 深度 2/≤2000 文件 + `dirOutline` 每子目录 `dirHint`）+ `p.Register`（SQLite 写）+ `Publish`。
- 保存来源设置：`ui/settings.go:515` → `runtime/pages.go:514-600`：内存改 + `store.SaveSettings` + **`imAfterSettingsSave`**（`runtime/im.go:685-696`）→ 停旧渠道 `select ... 5s`（`im.go:245-252`）×1–2 + `buildDriver` + 可能 `drv.Self(ctx)`（飞书：`im/feishu/driver.go:135-154`，无 self 时 HTTP `BotInfo`，client 30s：`transport.go:178`）→ 最坏 90s 级。
- 接口契约自相矛盾：`ui/app.go:47-55` 明确写"implementation must only enqueue the write and return… must not wait for a file or a database"，`ui/settings.go:486-488` 也写"never waits"，但实现同步写库+重启渠道。

正确做法本仓库已有先例：`CheckChannel`、文件夹选择器都跑在 goroutine 并用 `postToUI`/`Window.Update` 回主线程（`ui/channels.go:34-74`、`ui/workspace.go:14-37`）——把这条规则推广到所有命令即可，而不是给每个按钮再加特例。

---

## 3. P1 细节（摘要）

- **D-03 身份**：`ticketsVM` 行 ID=`number`（`pages.go:288`），详情键=`string(t.ID)`（`:296`）；`DetailVMOf(r.ID)` 用编号查（`execution.go:149`）、`DetailOf(r.ID)` 用编号查（`:160`）；`openTicket` 用行 ID 匹配（`ui/ticketdetail.go:32-46`）但 `openConversation` 传的是 `conversationVM.ID`=domain ID（`ui/workspace.go:304-308`、`pages.go:994`）。真实效果：详情永远短投影/空、点会话报"不在当前执行投影"。`viewtest` 把两边都写成 `"42"`（`fixtures.go:93`、`fixtures_pages.go:457`；UI 测试 `workspace_test.go:112-129`），所以 CI 看不见。**这是"投影契约只在 fixture 中成立"的典型设计缺陷**，修 ID 同时要加真实 projector seam。
- **D-04 DWS 串行**：活动路径 `newDWSPoller`（`runtime/dws.go:31-33`）→ poll（`im.go:420`）、send（`sender.go:38-48`→`dws.go:88`）、auth（`im.go:597` 新 client）三路并发；`dwsRunner`/`DWSTransport`（`im/dingtalk/dws.go:586-711,714+`）无生产调用者（grep 仅测试/定义）——**规格承诺的"单 worker"只存在于未接线的代码**。
- **D-05 生命周期**：`applyChannel` 并发/代际缺失（`im.go:239-318`）；`Stop` 不 clean ctx（`:208-221`）；`main` 丢返回值（`main.go:301`）→ Stop 从不调用；disable 提前 return 不注销 sender（`:262-271`）→ 旧发送路径仍活。
- **D-06 优先级/迁移**：flags 只在 boot overlay（`imwiring.go:49`）；之后的来源保存走 settings-only（`im.go:695`），该渠道字段一变就用库里配置重建（flag 凭据丢失）；启用只来自 settings（`:54,60`）且没有 enable flag —— 旧版本 `startConfiguredIM` 只要 flags 齐全就 `dingtalk.New`（默认 Stream，`internal/im/dingtalk/driver.go:113-117`）自动监听，升级后默认 disabled；默认 transport 又改成 DWS（`im.go:61`）→ 老 flags 用户既静默停摆，打开开关后还会先撞"dws 未安装"。
- **D-07 设置一致性**：先改内存+先 ++rev（`pages.go:553-566`），store 失败不回滚（`:568-582`，返回已 ++ 的 rev）；失败后下次保存 base 对不上 → 持续 CAS 冲突；2s pass `Publish` 会把未落库值展示为已保存（`:599` 只在成功时 Publish，但下一轮 pass 会补）。
- **D-08 两阶段**：`ReceiveTrigger` 内联 `EvaluateDue`+`Publish`（`entry.go:40-50`），被 IM poll 循环直接调用（`im.go:445-449`）；模型 90s 时该渠道停止轮询，与"Receive 只入队、2s pass 判定"的设计承诺相反。

---

## 4. P2 细节（摘要）

- **D-09 投影**：`Publish` 每次重建所有 shard（`runtime.go:143-330`），`ticketsVM` 全量 rows+每 Ticket DetailVM（`pages.go:268-308`），`stream` 遍历整个 Feed（`runtime.go:427+`），workspace 每仓 50 条静默截断（`pages.go:966-968`），无增量/无上限提示。长跑后 UI 帧与 poll goroutine 都被 O(历史) 拖慢。
- **D-10 工作区模型**：create-only（无 rename/delete/改根）；不查重复 root；`slug` 非 ASCII→`"workspace"`（`onboarding.go:234-248`）→ 第二个中文名 ID 冲突，错误文案"已经存在"（`workspaces.go:36-41`）；`engine.Register` 同 ID 覆盖（`engine.go:143-152`）。
- **D-11 作用域/所有权**：`WorkspacePanelVM.Selected` 投影从不填（`pages.go:953-978`；字段 `view/workspace.go:52-58`）→ 未选中时工作区页显示"还没有工作区"；消息/执行页不按 workspace 过滤（`workspaceSel` 仅在 `ui/workspace.go`/sidebar）；`CreatedBy` 仅 `Source=="local"` 算"我"（`pages.go:981-999`），同事 IM DM 和操作员自己 IM 发的都标"超级Agent 创建"。
- **D-12 outbox/错误**：sender 不看 `IsPermanent`/`ErrDWSAuthRequired`（`sender.go:38-48`），未装 dws/401/400 永久错误会无限退避重试而非 gone；`opError` 把 context.Canceled 标 Permanent（`dws.go:265-268`）→ 关闭瞬间可能丢回执。
- **D-13 DWS 契约鲁棒性**：状态码正则从文本猜 3 位（`dws.go:537-550`）会误判 4/5 位 DingTalk code；`at` 严格 RFC3339（`:501-507`）丢格式漂移；`page.Skipped` 不展示；marker HTML 注释在纯文本端可能可见（`:458-484`）；transport 切换不清 cursor（`im.go:224-236`）；`truncateDetail` 不脱敏（`:233-239`，规格要求去密钥）。
- **D-14 状态/授权**：`CheckChannel` 只验凭据（`runtime/dws.go:101-137`、`runtime/im.go:585-605`），token 有效≠长轮询/订阅可用；UI chip 由"设置意图+最近检查"合成，不读 sources 实际状态（`ui/channels.go:244-286`）；未实现传输可选（settingsVM 发布 ws/webhook `pages.go:460-462`，UI 一律标 `Implemented=true` `channels.go:292-310`）；没有"重连渠道"动作，配置无变化时 Apply 不会重启（`im.go:189-198`）。
- **D-15 秘密投影**：SettingsVM 携带明文 secret（`pages.go:463-477`），与 `view/channels.go:20-23` 的"panel never holds the credentials"相反；快照/截图/未来导出都会扩散。

---

## 5. 做对了的（避免过度改造）

- 异步 seam 的样板已经存在且正确：`CheckChannel` 与原生目录选择器都在 goroutine 里跑、经 `postToUI`/`Window.Update` 回主线程（`ui/channels.go:34-74`、`ui/workspace.go:14-37`）；应把它下沉成通用命令通道（见 D-02 的建议）。
- 禁用渠道是诚实的：不监听、来源行 `未启用`、健康 `ready/未启用`（`runtime/im.go:262-271`），没有假装连接；半配置给出可操作错误。
- DWS 子进程卫生：不经过 shell、`exec.CommandContext`、显式 Kill+WaitDelay、30s 兜底（`im/dingtalk/dws.go:303-334`）；marker 键做了危险字符处理；授权 URL 先做 http(s) 校验（`ui/channels.go:121-131`）。
- 工作区扫描有界：目录只读一层、深度 2、≤2000 文件、每文件 4KB（`scan.go:79-107,214-240`），不会扫整棵树。
- 设置层已有 store 级 CAS（`store/settings.go:70-95`），多窗口冲突有真实语义（问题在 runtime 的用法，D-07，而不是 store 本身）。
- 面板在未接线时显示"动作未接线"、空态诚实（本仓库一贯优点，保留）。

---

## 6. 建议的 5 个设计改动（按收益排序）

1. **异步命令边界（先做，收益最大）**：把 `Commander`/`SettingsSaver` 改成"提交→立即返回 pending→结果走投影/回调"；runtime 用一个命令 worker（有界队列、单飞设置写），UI 只画 pending/成功/拒绝。把 `NewConversation`、`CreateWorkspace`、`SaveSettings`（含 IM apply）、`Dispatch` 全部移出帧。**这同时消灭 D-02 与 D-08 的"帧内模型/IO"**，是其余问题的前置。
2. **一个 settings 子系统**：声明式 schema（key/kind/default/validate/normalize；switch 统一 `strconv.ParseBool` 并把 `1/true` 都接受）+ 单一优先级解析器（flags overlay 持久化到 manager，每次 Apply 都叠加；新增 `--feishu-enabled/--dingtalk-enabled`；旧 flags 凭据迁移为 enabled 一次并记日志）+ 事务化保存（副本合并→store commit→swap 内存/rev，失败不动内存）。**修 D-01/D-06/D-07**，并为所有未来设置项止损。
3. **统一 Ticket 身份 + 真实投影 seam 测试**：所有 panel 结构以 `Ticket.ID` 为键（编号只显示）；`ConversationVM`/`TicketRow`/`TicketDetailVM` 用一个 ID 类型/别名；新增"真实 `Publish`→`Snapshot`→UI"集成测试（不用 viewtest fixture）钉住 行↔详情↔会话跳转；修 `openTicket` 与 `DetailVMOf`。**修 D-03**，并让类似身份错位在 CI 暴露。
4. **IM 渠道状态机（desired/actual/auth/error + 串行 worker + 监督）**：每渠道一个状态机+generation、一个串行命令队列（poll/send/auth/check 全走它）、sender 注册随状态启停、失败自动退避重启/`重连`命令、向 UI 发布单一渠道状态（含 lastPoll/lastError/authUrl）。删掉未接线的 `DWSTransport` 或把它变成唯一实现。**修 D-04/D-05/D-12/D-14**。
5. **工作区模型与作用域收口**：稳定不透明 ID + store 级 name/root 唯一 + rename/delete（有在跑 Ticket 时拒绝）+ 投影 `Selected` 并在无选择时默认第一个 + 明确消息/执行页是"全部工作区"还是按选中过滤（若全局，UI 标注）+ 投影/列表最近 N 与"更多"提示 + `CreatedBy` 三态（我/同事/超级Agent）。**修 D-09/D-10/D-11**。

不建议现在做大重构（如换 UI 框架、重写 engine）；上面 5 项都是**边界/契约**层改动，可与现有代码渐进落地。

---

## 7. 评审方法与限制

- 快照 `f4200da`，工作树 clean；只读评审，未改生产代码；本文件之外未写任何文件。
- 黑盒探针只用于 D-01（编码）这一可确定的行为差异；其余为静态阅读（含 `viewtest` 与测试，用于确认"测试为何没抓到"）。
- 未在真实显示器上开窗口（无 GUI 环境），"帧阻塞"结论来自调用链与超时参数（模型 90s、飞书 client 30s、渠道 stop 5s/渠道），不是实测卡顿时长。
- 本机无 `dws`、无钉钉/飞书真实租户：DWS 契约假设（D-13）是风险评审，不是实测结论；真实 DWS 回来后需按 `im-channel-spec.md` §2 逐条对照。
- 评审范围是设计/结构，不是逐行实现审计；D-09 的"成本"未做基准测试，建议后续加一个 10k tickets/100k triggers 的 Publish 基准作为回归门。

---

*conf-arch · 2026-11-04 · 只读设计评审*

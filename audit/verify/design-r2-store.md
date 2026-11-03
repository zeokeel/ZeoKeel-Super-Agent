# 设计复审 R2：持久化 / settings / IM 配置（只读，design-r2-store）

> 范围：`internal/store/{settings,load,kv}`、`internal/runtime/{pages.go SaveSettings, im.go, workspaces.go, dws.go}`、
> `internal/im/dingtalk/dws.go`、`cmd/zeokeel/imwiring.go`、`cmd/zeokeel/main.go`（IM flags）、`internal/ui/settings.go`（编码端）、
> `internal/view/channels.go`、`internal/im/{im.go,addressing.go}`，对照 `audit/fixes/ui2-spec.md` 与 `audit/fixes/im-channel-spec.md`。
> 方式：只读代码 + `git show` 历史对照（提交 `6a96e3b` 为切换前、`f4200da` 为渠道管理切换）。**未改任何代码**，未跑在线服务。
> 结论口径：P0 = 1.0 承诺直接不成立 / 用户可见功能失效；P1 = 数据/安全/升级正确性；P2 = 可维护性、会随功能增长恶化。

---

## 0. 结论与 Top 5

整体判断：**持久化原语（store）是稳的；问题集中在"settings 作为配置系统"这一层——它现在是 `kv` 里的一堆无类型字符串，而 IM 渠道管理、工作区、来源实例都被塞进了同一个平面命名空间。** 三个结构性缺陷在 Wave-2 就应先修，否则每加一个渠道/实例/账号都要再做一次数据迁移。

| Top | ID | 一句话 | 严重度 |
| --- | --- | --- | --- |
| 1 | **D1** | 面板开关写 `"true"`、运行时读 `"1"`：**面板里"启用渠道"这个开关实际永久无效**（双向都不一致：存 `"1"` 时开关显示为关） | **P0** |
| 2 | **D2** | 升级迁移缺失：切换前用户已能在设置页保存 `source.feishu_app_id` 等凭据，切换后没有 `*_enabled` 键 ⇒ 默认 disabled ⇒ **静默变成"未启用"**；flag 也无法启用 | **P0** |
| 3 | **D4** | 生产 DWS 路径绕过了为它写的**串行 runner**：`IMManager` 用裸 `commandDWSClient`，`CheckChannel` 又另起一个裸 client ⇒ 轮询/发送/人工检查可并发跑多个 `dws` 子进程（违反 im-channel-spec §0.4），`DWSTransport` 成为生产死代码 | P1 |
| 4 | **D8** | `SaveSettings` 先改内存再 CAS：冲突/失败时**不回滚内存**，面板显示未落库的配置，`settingsRev` 与磁盘脱节 | P1 |
| 5 | **D10** | 工作区 ID 由显示名 `slug` 派生（`ws_<slug>`）：**纯中文名全部塌缩成 `ws_workspace`**，第二个中文工作区被拒且提示"已经存在"；改名/重名无解 | P1 |

另有一个安全项需要单独点名：**D12** —— `kv` 明文存 app secret / API key，而 Wave-1 新增的只读 `store.Inspect` 把**整个 KV** 返回给 `zeokeel status`（`internal/store/inspect.go:76,96-98`）。任何打印 KV 的调试路径都会把凭据泄到终端/日志。

---

## 1. Findings 表

| ID | 严重度 | 区域 | 位置（file:line） | 为什么是问题 | 后果 | 建议 |
| --- | --- | --- | --- | --- | --- | --- |
| **D1** | **P0** | settings 类型 | `internal/ui/settings.go:357,366,504`；`internal/runtime/im.go:54,60`；`internal/runtime/onboarding.go:37-39`；`cmd/zeokeel/cli.go:263`；`internal/ui/channels_test.go:156` | 布尔值有两套编码，且**跨边界没有归一化**：UI switch 写/读 `"true"/"false"`（`:357` 读、`:366` 写），IM 配置解析只认 `"1"`（`im.go:54,60`）；应用里另有一套 `"1"/"0"`（onboarding/cli）。UI 测试断言保存的是 `"true"`（`channels_test.go:156`），runtime 测试直接喂 `"1"`——**两侧测试各自通过，边界从未被测**。 | 在面板打开"启用飞书/钉钉"→ 存 `"true"` → `IMConfigFromSettings` 判 false → 渠道**永远 disabled**；反过来预置 `"1"` 的库在 UI 显示为**关**（`:357`）。1.0 的"来源页可启用渠道"直接不成立。 | 定义 settings 类型层：`BoolSetting` 统一写 `"1"/"0"`，读用 `parseSettingBool` 兼容 `"true"/"false"/"1"/"0"`；UI 只经 view 层解码；补一条**跨层**测试（UI save → runtime `IMConfigFromSettings` → enabled=true）。 |
| **D2** | **P0** | 升级/迁移 | `git show 6a96e3b:internal/runtime/pages.go:456-464`（切换前设置页已有 `source.feishu_app_id`/`source.dingtalk_app_key`/`im_self`）；`cmd/zeokeel/main.go:63-69`（flag 文案"empty disables the listener"）；`internal/runtime/im.go:54,60,328-378`（默认 disabled）；`internal/runtime/im.go:83-108` + `cmd/zeokeel/imwiring.go:28-33`（flag 只覆盖凭据，不能启用）；`internal/store/settings.go:17`（只有 CAS revision，**没有 settings schema 版本/迁移函数**） | `enabled` 是**存储值**且缺省=disabled；切换前用户的凭据（设置页或 flag）在升级后不会推导出 enabled。切换前 `startIMLoop` 无调用者（`git show 6a96e3b:cmd/zeokeel/extras.go:159`，AUDIT N-1 已指出"未接线"），所以严格说"原本也没监听"，但 README 旧文案与设置页让用户**以为已配置**；升级后既没有 enabled 也没有迁移提示。flag-only 部署更无法启用（flag 列表没有 enable）。 | 静默行为/预期丢失，违背 1.0"升级不丢配置"的诚实性；用户只能自己发现并手动打开开关（而开关又因 D1 无效）。 | ① 加 `setting.__schema`（整数）+ 迁移函数（见 D18）；② `enabled` 采用**三态**：显式 `"1"/"0"` 优先，缺省时**由凭据完整性推导**（`WithFlags` 里 flag 凭据也算"已配置"）；③ 迁移 v1：对没有 `*_enabled` 键但凭据完整的库写显式 `"1"`，并写一次 `source.migrated=1` 供审计；④ 显式 `"0"` 永不被推导覆盖。 |
| **D3** | P1 | IM 身份 | `internal/runtime/im.go:59`（两个渠道共用 `SelfID`）；`im.go:68-70,104-106`（`DingBotID = SelfID` fallback）；`im.go:286-294`（`drv.Self` 失败被忽略，selfID 留空）；`internal/im/addressing.go:24-34`（空 selfID ⇒ Mention 永假）；`im.go:505-513` | 飞书 open_id 与钉钉 staffId/botId 是**不同 ID 空间**，却用一个 `source.im_self` 共享；缺 `dingtalk_bot_id` 时又拿飞书的 self 顶替，比留空更糟（错误值永远匹配不到，且掩盖缺配置）。 | 群聊里 @ 机器人被判"未 addressing"→ gate 丢弃（默认 `AdmitsUnaddressed=false`），而面板显示"已启用/ready"，用户完全不知道为什么没反应。 | 拆键：`feishu_self_id` / `dingtalk_bot_id`（已有），删除或废置 `im_self`；迁移把 `im_self` 复制到对应渠道键并记录来源；`drv.Self` 失败时把渠道健康置 `error`（detail："缺少 bot id，@ 判定不可用"），不要静默降级。 |
| **D4** | P1 | DWS 传输 | `internal/runtime/im.go:400-404`（dws 分支 `newDWSPoller(cfg, commandDWSClient)`）；`internal/runtime/dws.go:31-98`（自建 poller，无 runner）；`internal/im/dingtalk/driver.go:102-107,155-190`（正规路径 `dingtalk.New(Config{Transport:"dws"})`→`newDWSDriver`→`DWSTransport`）；`internal/im/dingtalk/dws.go:586-712`（`dwsRunner` 串行队列）、`:714-790`（seen 去重/Close）；`internal/runtime/im.go:597-601`（`CheckChannel` 另建裸 client，绕过 runner） | 规格 §0.4 要求"每渠道一个串行 worker：poll/send/auth 共用队列，同一时刻最多一个子进程"。实际生产路径**完全没有 runner**：poll loop 与 outbox send 以及"检查通道"按钮是三个互相独立的 `dws` 子进程。`DWSTransport`（含 runner、seen 去重、backoff、cursor 抽象）只在 integration 测试里被构造（`dws_test.go:513,637,700`），生产不可达。 | 并发 `dws` 子进程 → 登录态/token 刷新竞争、CLI 输出交错、面板"检查"可能打断轮询；每次 settings 变更还会新建一个裸 client。**规格里最重要的串行保证没有生效。** | 让 runtime 走 `dingtalk.New(Config{Transport:"dws", DWSClient, SelfID, ...})` 拿 `Driver`（同时获得 runner/去重/健康），删除 `runtime/dws.go` 的平行 poller（约 98 行）；`CheckChannel` 与轮询共享**同一 driver 实例**的 runner（driver 暴露 `CheckAuth(ctx)`）。若坚持自建 poller，则必须让它持有 `DWSTransport`/`dwsRunner` 而不是裸 client。 |
| **D5** | P1 | 领域身份 | `internal/runtime/im.go:505`（`domain.Key(domain.WorkspaceScope(""), source, "trigger", ev.ID)`）；`internal/domain/ids.go:34`（`WorkspaceScope("")` = `"ws:"`）；`im.go:509`（thread key 同 scope）；对照 `internal/sources/github/trigger.go:92`（`Key(scope,"github-comment",repo,number,commentID,version)`） | IM Trigger 用了**畸形 scope**：既不是 `Global`（`"global"`）也不是任何真实工作区（`"ws:"`），`WorkspaceIDOf()` 判 false 侥幸等于 global；thread key 变成 `"ws:|dingtalk|<cid>"`，与面板本地的 `"global|local|…"` 不同命名空间。且 ID 里**没有 conversation id**，只含 `source+ev.ID`。 | 将来任何 scope 校验/按 scope 查询都会踩坑；两个会话若消息 id 相同（DWS/Stream 的 id 是否全局唯一无契约）会被当成重复 Trigger 丢掉；与 GitHub 的 key 组成不一致，重放语义不对称。 | 加 `domain.Global` 语义的构造 helper（或 `ScopeForSource(source)`），不要 `WorkspaceScope("")`；key 带上 `ev.Conv.ID`（`Key(global, source, "trigger", convID, ev.ID)`）。 |
| **D6** | P1 | 时间事实 | `internal/runtime/im.go:514-517`；`internal/im/im.go:60`（`At int64 // unix millis`） | `At: time.UnixMilli(ev.At)` 对 `ev.At==0` 得到 **1970**，`if tr.At.IsZero()` 永远不成立。DWS 解析端会拒绝缺时间（`dws.go:530-536`），但 feishu/stream/fake driver 没有统一校验。 | 1970 的 trigger.at 会排到时间线最前、破坏 `trigger_thread` 的 `<at>|<id>` 游标、让 brief 的 14 天摘要（`brief/digest.go:15`）看不到这条——静默的数据错误。 | 在 `imTrigger` 先判 `ev.At <= 0`，缺时间用 `time.Now()` 并记 `RawEvent` 级告警（或在各 driver 统一校验后拒绝该事件）；加回归测试。 |
| **D7** | P1 | IM 游标 | `internal/runtime/im.go:538-561`（`im.cursor.<channel>`，无 transport/account 维度）；`internal/runtime/kv.go:14-60`（另一套 `source.cursor.<source>`）；`kv.go:9`（注释声称 "an IM cursor is a page token"，实际 IM 从不使用该 API）；`internal/runtime/im.go:433-459`（仅成功后推进） | 同一渠道换 transport（`dws`↔`stream`）或换账号后沿用旧游标——两种消息流的分页 token 语义不同，可能报错或**静默跳过**（从外来游标继续）。同时仓库有两套互不相通的游标持久化 API，kv key 命名 (`im.cursor.*` vs `source.cursor.*`) 也没有单一登记处。 | 切换 transport = 可能丢消息或永远拉不到新消息；多账号/多实例无法表达（见 D20）；运维排查时不知道哪个 key 是"真游标"。 | 统一游标 API/命名：`cursor.<sourceKind>.<instanceId>`（instance 含 channel+transport+account），迁移 `im.cursor.<ch>` 到 `cursor.im.<ch>.<transport>`；删除 `IMManager` 的独立内存 fallback（`Runtime.cursors` 已有）。 |
| **D8** | P1 | settings 事务 | `internal/runtime/pages.go:537-566`（先改 `r.settings`、`r.settingsRev++`）；`:569-580`（随后 `st.SaveSettings`，冲突/失败直接 return，未回滚内存）；`:537`（`beforeSource` 快照同样来自内存） | CAS 的正确性完全依赖"先提交、后生效"，但内存写入发生在提交**之前**，失败路径不恢复。 | 冲突后：面板/投影显示一份**从未落库**的配置（含密钥）；`settingsRev` 比磁盘大 1，后续 base 计算偏移；IM 渠道不会重启（hook 未执行）→ 内存配置与运行中的渠道也不一致；下一次成功保存会把这份"幽灵配置"整体写盘。 | 先生成 `next` 副本，提交成功后再整体替换（copy-on-write），失败时内存不变；`beforeSource` 从当前已提交内存/磁盘读；补一条冲突路径测试断言内存与磁盘一致。 |
| **D9** | P1 | workspace/policy | `internal/runtime/pages.go:887-908`（对每个 workspace `p.Register(w, engine.Policy{Version:"v1", Allowed:allowed})`）；`internal/store/store.go:529`、`internal/store/load.go:22`（policy 存在 workspace 行）；`internal/runtime/workspaces.go:51-52`（新建workspace 只允许 local:operator） | "允许名单"是全局 settings，但落盘时**把每个 workspace 的整份 policy 重写**成只有 `{Version, Allowed}` 两个字面量，`AdmitsUnaddressed`/`AcceptMode`/`ReplyMode`/`Rules`（engine 侧字段）全部被清零；且逐个 workspace 各自一个事务，中途失败会留下混合状态。新建 workspace 又不继承全局名单，直到用户再保存一次名单。 | 保存"允许名单"会**静默重置每个工作区的受理/回复档位**（安全相关配置）；新建工作区里同事的消息被拒，但 UI 显示允许名单里有他；多工作区语义不一致。 | ① 允许名单要么作为独立全局键（不写进 per-workspace policy），要么读-改-写 policy（只替换 `Allowed` 字段）；② 一次事务写全部 workspace（store 提供 `UpdatePolicies(fn)`，单 `Atomic`）；③ 新建 workspace 的默认 policy 从当前全局名单派生。 |
| **D10** | P1 | workspace 身份 | `internal/runtime/workspaces.go:36`（`"ws_"+slug(name)`）；`slug` 实现 `internal/runtime/onboarding.go:234-249`（只保留 `a-z0-9`，非 ASCII 全丢，空则 `"workspace"`）；`:37-40`（查重） | ID 由**可变的显示名**派生：纯中文名 → `slug` 为空 → 全部 `ws_workspace`，第二个中文工作区被拒（提示却是"已经存在"）；"Acme 网页"与"Acme API"都 → `ws_acme`；改名要么换 ID（历史 Trigger/Ticket 的 `workspace_id` 失联）要么名与 ID 永久不一致。 | 中文用户第二个工作区就建不出来；重名/改名的数据迁移风险在以后每次改名时兑现；`Ticket.Workspace`/`Mandate.Workspace`/`Thread` scope 全部受影响。 | 现在就换成**不透明稳定 ID**（`ws_` + 随机 8 位/ULID；或 slug + 冲突时 4 位哈希后缀并持久化），`name` 只做显示且可改；把 slug 保留为别名/关键词。越早改越便宜（目前只有本地库）。 |
| **D11** | P1 | workspace 根目录 | `internal/runtime/workspaces.go:22`（只 `expandHome+TrimSpace`）；`:29-31`（`os.Stat` 后原样保存）；`internal/store/store.go:372-390`（`workspace` 表 ID 唯一，root 无唯一约束）；`:37-41`（只查 ID/名字，不查 root） | root 不做 `Abs`/`EvalSymlinks`/去重：相对路径会按进程 cwd 解析（GUI/launchd/systemd 的 cwd 不同），两个工作区可指向同一目录。 | 重启后 scan/worker cwd 指向错误目录 → 事实为空、执行写错地方；两个工作区同时改同一仓库，artifact/PR 归属混乱。 | create 时 `filepath.Abs` + `EvalSymlinks`，`root` 加 UNIQUE（或应用层查重）；迁移把现有 root 规范化为绝对路径。 |
| **D12** | P1 | 密钥 | `internal/store/settings.go:83-92`（密钥与普通设置同表同事务明文写 kv）；`README.md:8`（自认"API Key 明文存库"）；`internal/runtime/pages.go:464,476`（secret 真实值进入 SettingsVM）；`internal/runtime/onboarding.go:78`；`internal/ui/settings.go:381-383`（只做显示掩码）；**`internal/store/inspect.go:76,96-98`（Wave-1 新增的 `Inspect` 返回整个 KV）** | 明文落库可接受（本地单用户 + 0600），但**投影层**三个问题更大：① secret 真值随 Snapshot 进入 UI 内存/test fixture；② `Inspect.KV` 含 `setting:source.*_app_secret`、`setting:onboard.api_key`，任何 `status`/调试打印会泄到终端与日志；③ settings 无"字段敏感"元数据，靠每个调用点自觉。 | 凭据进入终端日志/错误上报/截图；`zeokeel status --json` 这类输出会带密钥。 | 短期：`Inspect` 返回**允许列表**的 KV 或对 `setting:` 做 redaction（`secret` 字段按 view 的 `SettingSecret` 名单打码）；`SettingsVM` 对 secret 字段返回掩码 + "未修改则原样保留"哨兵（见 D8 的 copy-on-write 里保留旧值）；长期：`secret:` 独立表 + OS keychain seam（README 已列为未做）。 |
| **D13** | P1 | DWS 脱敏 | `internal/im/dingtalk/dws.go:255-263`（`opError` 把 stderr/stdout 原文放进 detail）；`:233-239`（只截断不脱敏） | 规格 §2 明确要求"stderr 进入 health detail（截断，**去密钥**）"；现在未实现脱敏。detail 会被写进 `health` 表（`internal/effects`/driver health）并被 `Inspect`/面板/日志读到。 | CLI 在 auth/token 错误时可能回显凭据片段 → 落库 + 面板 + 状态输出三重泄漏。 | 在 `opError` 统一 redaction：剔除 `token/secret/key/authorization` 模式与 URL query；单测覆盖。 |
| **D14** | P2 | 渠道生命周期 | `internal/runtime/im.go:239-252`（`applyChannel` 只 cancel+等 done），`:207-221`（`Stop` 同）；`internal/im/dingtalk/dws.go:780-790`（`DWSTransport.Close`）、`internal/im/dingtalk/stream.go:87-89`（`StreamCloser`） | 停止渠道时从不调用驱动的 `Close()`；driver 的 stream/SDK 连接与 runner goroutine 只在进程退出时消失。 | 每次保存 source 组/切换 transport 泄漏一个 runner/连接；长跑后 goroutine/句柄增长；DWS 旧 runner 仍可能执行排队中的 send。 | `applyChannel`/`Stop` 在等 `done` 后 `if c, ok := drv.(interface{ Close() error }); ok { _ = c.Close() }`。 |
| **D15** | P2 | outbox/DWS 幂等 | `internal/runtime/dws.go:88-93`（`messageId` 被丢弃）；`internal/im/dingtalk/dws.go:434-455`（`--idempotency-key` + marker）；`internal/runtime/dws.go:96-98`（无历史读回） | DWS 返回的远端 `messageId` 不落库，outbox 回执只有状态；marker 无读回路径，无法用于"发送前去重"。超时后重试的安全性完全押在 DWS 是否遵守 idempotency-key（规格未验证）。 | 若 DWS 忽略 key：一次超时重试 → 用户看到重复消息；审计无法回答"这条回执对应远端哪条消息"。 | `outbox` 回执加 `remote_id` 列（store 已有 receipt 表，加列兼容）；`dwsPoller.Send` 回填；文档写明 idempotency 契约与"不保证"的边界。 |
| **D16** | P2 | IM 批次语义 | `internal/runtime/im.go:441-458`（`failed=true → continue`，cursor 不推进，无退避/上限；失败仅 warn） | 一条永久失败的事件会让整批永远重试（2s 一次），游标**永久卡死**，该渠道从此"看起来在轮询但什么都进不来"；日志刷屏。 | 需要人工重启/清库才能恢复。 | 失败计数 + 指数退避；超过 N 次把该事件标记 skipped 并写入审计（store 表或 health detail），cursor 越过它；至少限制批内失败重试次数。 |
| **D17** | P2 | settings 健壮性 | `internal/store/settings.go:44-57`（`"no rows"` 字符串匹配；`ParseUint` 失败返回 `(0,nil)` 吞错）；`:70-95`（整快照写、只拒绝空 key，未知 key 永远保留、永不修剪）；`internal/runtime/pages.go:569`（每次保存都写全量 settings） | 设置键无 schema/类型/白名单：拼错的键永久留在库里；损坏的 revision 静默归零；全量重写让"删掉一个键"不可表达。 | 配置随版本无限膨胀；CAS 在异常数据上行为不确定；难以写可靠的 settings 迁移。 | 建立 settings key 注册表（`key → type/default/owner/secret`），保存时校验/归一化，支持删除（显式 set 空并标记 deleted）；`SettingsRevision` 用 `errors.Is(sql.ErrNoRows)` + 解析失败报错。 |
| **D18** | P2 | settings 版本 | `internal/store/settings.go:17`（只有 `setting.__revision`）；全仓无 `setting.__schema`/迁移函数 | D2 的根因：设置布局会随 IM/workspace/source 继续演进，没有版本号就无法写有序迁移。 | 每次布局变更只能"读时猜"，D2 类静默行为回归会重复发生。 | 加 `setting.__schema` + `migrations` 列表（与 store schema 迁移同款，`internal/store/migrate.go` 已有可复用模式），启动时在 `Open`/`LoadSettings` 内跑。 |
| **D19** | P2 | workspace 事实 | `internal/runtime/workspaces.go:42-49`（创建时 scan 一次写入 brief JSON）；`internal/runtime/extras.go:206-237`（只有显式 Refresh）；`internal/domain/workspace.go:68-73`（7 天 Stale 只打印警告） | `Facts`（语言/build/test/remote/最近活动）只创建时扫一次，之后靠人工/启动刷新；brief 是 Router 上下文，会持续用陈旧事实路由。 | 路由/简报基于旧事实（仓库改名/换栈后仍按旧语言/命令）。 | 启动时后台异步刷新 + 定时（如 24h）刷新；写入 `Facts.ObservedAt` 并让面板显示"上次扫描"；把 scan 失败（root 不存在）升级为来源/工作区健康问题而不是静默空事实。 |
| **D20** | P2 | 配置布局 | `internal/runtime/pages.go:456-482`（`source.<key>` 单值平面键；GitHub 也是同组单值键）；`README.md:143`；对照 `internal/runtime/kv.go:14`（游标有 instance 维度） | settings 无法表达"N 个来源实例/账号"：一个 `source.feishu_app_id` 只能是飞书一号账号；GitHub 同理（`source.github_repo` 单值，flags 复制到 settings `main.go:150-161`）。游标有 instance 维度、配置没有。 | 加第二个仓库/IM 账号时必须做 settings 键迁移 + UI 重写；如果先发 1.0 再改，迁移面大且不可逆（用户已存单值键）。 | 在 1.0 内引入 instance 维度：`source.<instance>.<key>` + `source.instances` 或独立 `source_instance` 表（id/kind/config JSON/created_at）；迁移把现有单值键包成默认实例；IM 渠道编号从"两个硬编码渠道"改为实例列表。 |
| **D21** | P2 | workspace 生命周期 | `internal/runtime/workspaces.go`（只有 CreateWorkspace/NewConversation）；schema `workspace` 无 `deleted_at`/`archived` 列 | 无删除/改名/停用：误建的工作区永久占据路由候选与面板；删除若以后直接 DELETE 会破坏已结束 Ticket 的 `workspace_id` 引用。 | 面板越来越脏；后续删除功能会变成"要不要级联删历史"的两难。 | 现在只加 `deleted_at`（tombstone），删除=停止路由+面板隐藏，历史保留；重命名走 D10 的稳定 ID。 |
| **D22** | P2 | 保存阻塞 | `internal/runtime/im.go:189-198` → `:239-252`（同步停旧渠道：每渠道最多等 5s）；`internal/runtime/pages.go:587-590`（在 SaveSettings 返回前调用） | 保存设置会在 UI 线程同步停/起渠道，最坏 2×5s；期间面板卡住，且保存失败（渠道 build 失败）不会回滚已停的旧渠道。 | 用户点保存 → 界面冻结数秒；配错凭据后旧渠道已被停，只剩 error 状态。 | 保存只写盘并投递"应用配置"事件，渠道切换在后台串行 worker 完成（配 `applying` 状态）；失败时保留旧渠道或至少明确显示"已停用，原因是…"。 |
| **D23** | P2 | 配置完整性 | `internal/runtime/im.go:113-116`（`dingConfigured()` 因 `DingTransport=="dws"` 恒为 true） | 表示"已配置"的函数永远真，任何依赖它的完整性判断都失真（目前只用于语义，尚无调用点，但会误导后续）。 | 后续有人用它做"半配置检测"会漏判。 | 改为与 `channelReadiness` 一致的判定，或删掉未用的 helper。 |

---

## 2. 逐题回答

### Q1. Settings 模型：`enabled` 是派生还是存储？迁移应该怎么做？

**现状**：`enabled` 是**存储值**（`settings["source.<ch>_enabled"]`），缺省 = disabled；但读写编码不一致（D1），且没有迁移（D2）。凭据（app id/secret、flag、`dws_cmd`）是另一组存储值，与 enabled 无逻辑关联。密钥与普通设置同表明文（D12）。

**建议模型**（明确回答"derived or stored"）：
1. **显式值优先、缺省派生**的三态：`enabled ∈ {"1","0", unset}`；`unset` 时 `enabled = credentialsComplete(ch)`（flag 提供的凭据也算 complete，`WithFlags` 里置一个 `implicitEnable=true`）。用户关掉即写 `"0"`，永不被派生覆盖——这同时解决 D1（统一编码）与 D2（flag/旧配置用户）。
2. **settings schema 版本** `setting.__schema`（D18），迁移 v1 做三件事：把旧布尔写法归一（`"true"→"1"`）；无 enable 键但凭据完整 → 写 `"1"`；`im_self` → `feishu_self_id`/`dingtalk_bot_id`（D3）。
3. **类型层**：settings key 注册表（类型/默认/owner/secret）驱动 view 与校验（D17），UI 不再手写 `"true"`。
4. **密钥**：短期在 `view`/`Inspect` 做 redaction + "未修改保留旧值"（D12）；长期 `secret:` 表/keychain seam。

### Q2. Workspace 持久化

- **ID 由名字派生**是最大的"会固化"错误（D10）：中文名塌缩、改名无路、重名拒绝提示误导。**建议 1.0 内改为不透明稳定 ID**（现只有本地库，迁移只是重写 `workspace.id` 外键引用，成本远低于以后）。
- **rename/delete 缺失**（D21）：加 `deleted_at` tombstone 与"改名不改 ID"。
- **duplicate root / 相对路径**（D11）：`Abs+EvalSymlinks+UNIQUE`。
- **workspaces 表 vs policy JSON**：policy 作为独立列可以，但必须**单一写入者+读改写**（D9）；"允许名单"全局化，不要把全局设置写进每行 policy。
- **policy 默认 local:operator**（`workspaces.go:51-52`）：新工作区应继承当前全局名单；否则同事消息被拒而 UI 说允许。
- **scan facts**（D19）：创建时扫 + 启动/定时刷新；失败进健康。
- **多工作区不变量**：IDs scoped（`domain.WorkspaceScope`）本身正确，但 IM 用了 `"ws:"`（D5）破坏了 scope 语义；workspace 删除/改名/替换后，Trigger/Ticket 的 `workspace_id` 指向必须靠稳定 ID 保证。
- **restart load ordering**：`Load (workspaces→policies→triggers→threads→decisions→tickets→outbox)`（`internal/engine/persist.go:69-177`）在 IM 启动前完成（`cmd/zeokeel/durable.go:96` → `cmd/zeokeel/main.go:301`）✓；但 IM 游标/设置的加载与渠道启动之间**没有**"配置应用完成"屏障（D22）。

### Q3. IM 游标

- 推进时机正确：整批交付成功后才写（`im.go:451-458`），失败保持（`:451-453`）。
- 但有三处设计债：**键不加 transport/account 维度**（D7）；**两套游标机制**（`im.cursor.*` vs `source.cursor.*`，`kv.go:10` 注释与实现矛盾）；**无实例概念**（D20）。
- **restart dedupe**：依赖 Trigger ID 内容派生；但 ID 不含 conversation（D5）→ 跨会话同 id 碰撞风险；且 scope 是 `"ws:"`。修好 D5 后 at-least-once 语义才成立。
- **failing-batch**：无上限重试、游标永久卡死（D16）。
- **relation to engine Trigger IDs**：cursor 与 Trigger 写入不在同一事务（at-least-once 是刻意选择，可接受）；但文档要写清"重复投递由内容 ID 去重，故 ID 必须覆盖 (source, conversation, message)"。

### Q4. DWS 持久化/交互

- **outbox key 幂等**：`--idempotency-key` 传的是 outbox 业务 key（`dws.go:434-455`）。但远端 `messageId` 被丢弃（`runtime/dws.go:88-93`），回执不记录远端 id（D15）；marker 只写不读（`runtime/dws.go:96-98` 无历史读回）→ 无法用于发送前去重。
- **cursor**：DWS transport 自带 cursor（`dws.go:797-808,931+`）但生产路径没用它（D4），runtime 用自己 `Poller.Poll` 的 cursor——功能等价但两套实现。
- **"用户调用 dws"（auth/check）不共享状态**：`CheckChannel` 新建裸 client（`im.go:597-601`），与 poll/send 完全独立（D4）；规格 §0.4 的"同一队列"未实现。
- **tx/locking around ApplyIMSettings**：`SaveSettings` 在 store 事务提交**之后**调用 `imAfterSettingsSave`（`pages.go:587-590`），不持锁、不长期占用写事务 ✓；但同步阻塞 UI（D22），且 `before` 取自可能已被污染的内存快照（D8）。`setCursor` 用单条 `st.Set`（`im.go:558`），没有与 Trigger 事务耦合——符合 at-least-once 设计，但要在文档中明确。

### Q5. 在"固化"前我会改的状态布局

1. **settings 成为有 schema 的配置系统**：`setting.__schema`、key 注册表（类型/default/secret/owner）、归一化的 bool/enum、per-key 或 per-group 的 CAS；保存走 copy-on-write（D1/D8/D17/D18）。
2. **实例维度**：`source.<instance>.<key>` + source instance 表（kind/config/cursor/health），IM 渠道、GitHub 仓库都是实例；游标统一 `cursor.<instance>`（D7/D20）。
3. **secret 独立通道**：`secret:` 命名空间/keychain seam，`Inspect`/`SettingsVM` 默认 redaction（D12/D13）。
4. **workspace 稳定 ID + root 唯一 + tombstone**，policy 读改写、允许名单全局化（D9/D10/D11/D21）。
5. **IM 入口统一**：runtime 使用 driver 的 `DWSTransport`（含串行 runner/去重/健康），`CheckChannel` 与轮询共享同一 driver；`imTrigger` 用 `Global` scope + conversation 进 ID；渠道停止调用 `Close`（D4/D5/D14）。
6. **明确的 at-least-once 文档**：cursor 提交/Trigger 提交/远端发送三处各自的重放语义，写进 store/`im` 包注释与 README。

---

## 3. 与 spec 的偏差摘要（供 Lead 决定处置）

| spec 要求 | 代码现状 | 相关 finding |
| --- | --- | --- |
| 渠道可单独启用/停用（im-channel-spec §0.1、§1） | 开关编码错误，实际不可用 | D1 |
| 默认 `feishu_enabled=false` 但需迁移旧配置（§1 隐含） | 无迁移；flags 无法启用 | D2 |
| 每渠道一个串行 worker，poll/send/auth 共用队列（§0.4、§2） | 生产路径无 runner；检查按钮并发 | D4 |
| cursor 仅在本批全部成功后持久化（§2） | 已实现；但键无实例维度、失败批次无上限 | D7、D16 |
| stderr 进 health detail（截断、去密钥）（§2） | 只截断，未脱敏 | D13 |
| secret 字段由 UI 掩码（ui2-spec §2 契约） | 仅显示掩码；真值进 VM/Inspect | D12 |
| "未启用=未监听、健康 ok"（§1） | 实现 ✓（`applyChannel:262-272`） | — |
| IM 消息必触发明确结果（ui2-spec §0.5） | 有 `noDispatchMessage` 兜底 ✓；但 1970/身份问题会让消息先被 gate 丢 | D3、D6 |

---

## 4. 只读声明

- 本报告写于 `audit/verify/design-r2-store.md`，**未修改任何源文件**（`git status` 对源文件无新增变更）。
- 方法：逐文件阅读 + `git show <commit>` 对照切换前行为；所有结论均带 `file:line`。未运行 `go test`/服务以保持只读（无必要：均为静态可达性问题）。
- 标注为"推测"的点：DWS/Stream 的 message id 是否全局唯一（影响 D5 的实际碰撞概率）——规格未定义，需要 DWS 端确认。

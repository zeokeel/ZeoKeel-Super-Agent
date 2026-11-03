# fix-store handoff — 用户“修复全部”轮（store 范围）

> 范围：`internal/store/**`（未改 runtime/ui/agent/im/cmd；未改 go.mod；CGO=0）。
> 本轮针对 `audit/verify/design-r2-store.md` 的 **P1-9（密钥经 Inspect 泄露）** 与三项存储前置能力，
> 以及 `audit/design-review-r2.md` 的 store 相关行。
> 只读审计见 `audit/verify/design-r2-store.md`（我上一轮产出）。

---

## 1. 完成的条目

| 条目 | 状态 | 说明 |
| --- | --- | --- |
| **P1-9：Inspect 返回全部 KV（含 secret）** | **fixed** | `store.Inspect`/`HealthView`/`HealthEntries` 现在对 secret-named key 返回值替换为 `store.RedactedValue`（`"***"`，key 仍可见以区分“已配置/未配置”），并对 health detail 做凭据模式脱敏；writer 侧 `Settings()`/`Get()` 保持原值（runtime 需要真实凭据）。见 §3 契约。 |
| 存储前置（Lead item 2）：SuperAgent 聊天持久化 | **fixed** | 新增专用有界 chat API（`PutChat/AppendChat/Chat/ClearChat/ChatIDs`，`ChatMessage`）：每 chat 一行 kv，≤200 条 / ≤256KB，超出丢最旧、单条超大按 rune 截断；同 ID 追加为幂等替换；时间戳规范化 UTC。 |
| 存储前置（Lead item 3）：settings schema 地基 | **fixed（store 侧）** | `SettingsSchemaVersion=1` + `SettingsSchema()`/`SetSettingsSchema()`（`setting.__schema`）；`Settings()` 不再暴露任何 `__` 开头的 store bookkeeping；`SaveSettings` 忽略调用方回传的 `__` 键（保护 CAS 计数器，不改变既有语义）。另补 CAS/事务测试：冲突/校验失败后数据库**完全不变**。 |
| 存储前置（Lead item 4）：workspace policy 保留式更新 | **fixed** | 新增纯函数 `PatchPolicyAllowed(policyJSON, allowed)` 与 `UpdateWorkspacePolicyAllowed(ws, allowed)`、`UpdateAllWorkspacePoliciesAllowed(allowed)`（单事务、全有或全无）；只替换 `Allowed` 字段，`Version/AcceptMode/ReplyMode/AdmitsUnaddressed/Rules` 等原样保留（design-r2-store D9 的 store 侧修复）。 |
| Lead item 5：status/Inspect 调用方检查 + CLI 边界脱敏测试 | **fixed（store 侧）** | 调用方清单：`internal/runtime/status.go:170`（读到的是 Health/载荷，KV 只按非 secret 键取值：`runtime.manually_stopped`、`setup.complete`、`setting:onboard.*`、`setting:execution.concurrency`、`schema`）；`cmd/zeokeel/ops_test.go:160` 只用 `SchemaVersion`。新增测试锁定这些 key 不被误脱敏，并断言整个 `HealthView` 序列化后不含任何 secret 字面量。**未改 status 代码**（scope 外），脱敏在 store 边界完成，status 打印什么都不会再带密钥。 |
| item 5 附带：`status` 打印 KV 的场景 | **covered** | 即便将来 status 打印整个 `view.KV`，值已被替换为 `"***"`；`HealthEntry.Detail` 已脱敏。 |

## 2. 改动文件（全部在本人 scope）

新增：
- `internal/store/redact.go` — `IsSecretKey`、`RedactedValue`、`redactText`（health detail 脱敏）
- `internal/store/chat.go` — 有界聊天持久化 API
- `internal/store/policy.go` — policy 保留式 patch/update
- 测试：`internal/store/redact_test.go`、`chat_test.go`、`policy_test.go`
- 本文件

修改：
- `internal/store/inspect.go` — KV 脱敏、health detail 脱敏、`HealthView.KV` 契约注释
- `internal/store/settings.go` — `__schema` 常量/读写 helper、`Settings()` 过滤 `__` bookkeeping、`SaveSettings` 忽略 `__` 键、`SettingsRevision` 用 `errors.Is(sql.ErrNoRows)`
- `internal/store/settings_test.go` — 新增 CAS/回滚/bookkeeping/schema 测试

## 3. 给 Wave-2 runtime 的契约（API 名 + 语义）

```go
// 1) 只读状态（脱敏保证）
view, err := store.Inspect(dbPath)   // HealthView
// view.KV[secret-ish key] == store.RedactedValue ("***")；非 secret 键原值
// view.Health[i].Detail 已按 key=value/Bearer 模式脱敏
store.IsSecretKey(key) bool          // UI/status 可复用同一判定做字段掩码
s.HealthEntries()                    // writer 侧健康读，Detail 同样已脱敏

// 2) SuperAgent 聊天（runtime 拥有语义/投影）
type store.ChatMessage struct{ ID, Role, Text string; At time.Time }
s.AppendChat(chatID string, m ChatMessage) ([]ChatMessage, error) // 同 ID 幂等替换；返回有界历史
s.PutChat(chatID string, msgs []ChatMessage) error                // 整段替换（投影重建）
s.Chat(chatID string) ([]ChatMessage, error)                      // 未知 chat => nil,nil
s.ClearChat(chatID string) error
s.ChatIDs() ([]string, error)                                     // 用于会话列表投影
// 边界：200 条 / 256KB；超限丢最旧；单条超大截断（合法 UTF-8）

// 3) settings schema（runtime 负责迁移，store 只存/读）
const store.SettingsSchemaVersion = 1
s.SettingsSchema() (version int, known bool, err error) // known=false 表示从未迁移
s.SetSettingsSchema(v int) error
// Settings() 不再返回 "__*" bookkeeping；SaveSettings 忽略 "__*" 键（不会覆盖 CAS 计数器）

// 4) 允许名单保存（替代“整份 Policy 覆盖”）
store.PatchPolicyAllowed(policyJSON string, allowed map[string]map[string]bool) (string, error)
s.UpdateWorkspacePolicyAllowed(ws domain.WorkspaceID, allowed map[string]map[string]bool) (bool, error)
s.UpdateAllWorkspacePoliciesAllowed(allowed map[string]map[string]bool) (int, error) // 单事务、全有或全无
```

**runtime 侧建议接线（scope 外，留给 fix-runtime/nih-runtime）**：
1. `pages.go::applyAllowlist`（当前 `pages.go:887-908`）改为调用 `UpdateAllWorkspacePoliciesAllowed(allowed)`，**不要再** `p.Register(w, engine.Policy{Version,Allowed})`（那会清掉每区 accept/reply/rules）。
2. 启动时读 `SettingsSchema()`：未知或 `< SettingsSchemaVersion` → 跑 settings 迁移（如 `"true"→"1"`、`im_self→feishu_self_id/dingtalk_bot_id`、无 enable 但凭据完整→写 `"1"`）→ `SetSettingsSchema(1)`。
3. 面板聊天持久化用 `AppendChat`/`Chat`/`ChatIDs`（不要塞进 settings）。
4. status 若打印 `view.KV`，secret 位显示 `"***"` 是预期；不要再从 writer 侧 `Settings()` 取 KV 打印。

## 4. 新增/修改的测试与结果（命令 + 实际输出）

```
$ CGO_ENABLED=0 go test ./internal/store/ -count=1
ok  github.com/zeokeel/super-agent/internal/store  1.30s
$ CGO_ENABLED=0 go test ./internal/store/... ./internal/effects/... ./internal/outbox/... \
    ./internal/schedule/... ./internal/goal/... ./internal/domain/... -count=1
ok  store 1.29s | effects 0.25s | outbox 0.17s | schedule 0.11s | goal 0.16s | domain 0.004s
$ CGO_ENABLED=1 go test -race ./internal/store/ -count=1
ok  github.com/zeokeel/super-agent/internal/store  15.4s
$ gofmt -l internal/store   → 空
$ CGO_ENABLED=0 go vet ./internal/store/ → 通过
$ ./scripts/check-nocgo.sh ./internal/store/ → PASS（上一轮已验证，本轮无新依赖）
$ go test ./cmd/zeokeel/ -run 'TestBackup|TestArchive|TestStatus' → ok（Inspect 调用方无回归）
```

新增测试要点：
- `TestInspectNeverReturnsSecrets`：库里同时放 5 类 secret（`onboard.api_key`、`source.*_app_secret`、`github_token`、`execution.private_key`）+ 非 secret 事实；断言 `Inspect.KV[secret]=="***"`、整个 `HealthView` JSON 不含任何 secret 字面量、非 secret 键值原样、health detail 中的 token 被脱敏、而 `Settings()`/`Get()` 仍返回真值。
- `TestStatusFactsSurviveRedaction`：锁定 `runtime.manually_stopped`/`setup.complete`/`setting:onboard.*`/`setting:execution.concurrency`/`schema` 不被误脱敏（status 读取面）。
- `TestHealthEntriesRedactsDetails`：writer 侧健康读也脱敏。
- `TestIsSecretKey` / `TestRedactText`：正负例（`token expired` 这类散文不误伤；`Bearer …`、`name=value` 命中）。
- chat：`TestChatRoundTripAndBound`（210 条 → 保留最新 200，时间规范化）、`TestAppendChatIsIdempotentByID`、`TestPutChatClearChatAndChatIDs`、`TestChatIsNotASetting`、`TestChatBoundsOversizedMessage`（200k 汉字 → 行 ≤256KB、合法 UTF-8、非空）、`TestChatValidation`（空/控制字符 id、空 role、坏 JSON 行报错）。
- policy：`TestUpdateWorkspacePolicyAllowedPreservesOtherFields`、`TestUpdateAllWorkspacePoliciesAllowedPreservesFields`、`TestUpdateAllWorkspacePoliciesAllowedIsAtomic`（一条坏 policy → 全部回滚；修好后成功）、`TestPatchPolicyAllowedShapes`（""/{} /null、大小写字段、非对象拒绝）。
- settings：`TestConflictLeavesDatabaseUnchanged`、`TestSaveSettingsValidationRollsBack`、`TestSettingsBookkeepingKeysAreProtected`、`TestSettingsSchemaStamp`。

## 5. 行为变化与兼容性风险

1. **`Inspect().KV` 对 secret-named key 的值从原文变为 `"***"`**。已 grep 全部调用方：`internal/runtime/status.go` 只按非 secret 键取值；`cmd/zeokeel/ops_test.go` 只用 `SchemaVersion`；store 内 `verify_core_test.go` 只查 `schema`。若外部脚本依赖 `Inspect` 读 secret（不应存在），行为变化是**有意的**。
2. **`HealthEntries()` 的 `Detail` 现在会脱敏**；effects 测试只断言非空与 state，未受影响。
3. **`Settings()` 不再返回 `__` 开头的 kv 键**（之前只过滤 `__revision`，现在过滤整个 bookkeeping 命名空间；`__schema` 为新增键）。`SaveSettings` 对 `__` 键**静默忽略**（旧调用方传 `__revision` 也不会报错）。
4. **新增 kv 命名空间**：`chat.<id>`（与 `setting:`/`source.cursor.`/`schema` 无冲突）；`setting.__schema`。均为纯增量，无迁移。
5. 无 schema 变更（未加表），无需 v4 migration。
6. 未改 `SettingsRevision` 的解析失败语义（仍回退 0），避免影响 runtime 现有行为；只在“无行”判断上改用 `errors.Is`。

## 6. 跨 scope 待办（给 fix-runtime / nih-runtime）

1. `internal/runtime/pages.go:887-908` `applyAllowlist`：改用 `store.UpdateAllWorkspacePoliciesAllowed`（当前会清掉每区 AcceptMode/ReplyMode/Rules，且是 N 个独立事务）。
2. `internal/runtime` 启动迁移钩子：读 `store.SettingsSchema()` 并实现 D1/D2/D3 迁移（布尔归一 + enabled 三态推导 + `im_self` 拆分），完成后 `SetSettingsSchema(1)`。**store 未改 setting 语义**（Lead 要求）。
3. 聊天投影：runtime 用 `store.Chat/ChatIDs/AppendChat` 持久化最近对话；不要再写 settings。
4. status：可放心打印 `view.KV`（secret 为 `"***"`），但**不要**用 writer 侧 `Settings()` 打印。
5. 密钥静态加密/钥匙串（design-r2-store D12 长期项）不在本轮；如需，另立任务。

## 7. 未完成 / 被阻塞

- 无本人 scope 内未完成项；store 包 build/test/vet/gofmt/race/nocgo 全绿。
- **无法跑 `go test ./internal/runtime/`**：该包当前被另一位 agent 的在途编辑破坏（`internal/runtime/onboarding.go:192-193: undefined values/rev`），与本轮改动无关；`cmd/zeokeel` 相关测试已单独跑通。
- 全仓 `go build ./...` 在最后检查时曾通过；若再红，请以各 owner 的 in-flight 编辑为准（本 scope 构建独立通过）。

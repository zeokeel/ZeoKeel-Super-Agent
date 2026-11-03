# UI / Super-Agent / Tools 规格（2026-11-03 第二轮）

## 0. 用户要求
1. 侧边栏结构改成：顶部单独入口 **超级Agent**（总揽页）；下面 **工作区** 区域（可新建工作区；每个工作区展开显示会话；会话区分「我创建的」与「超级Agent创建的」）；底部放 消息/来源/触发/评估/执行/设置。类似 DSH 界面。
2. 全局字体 +2 号。
3. 显示当前运行平台（Linux/Windows/macOS + arch）与当前时区。
4. 检查框架自带工具并注入 Agent：eino 的 plantask（task_create/get/update/list，即 todo/plan）、agentsmd（AGENTS.md 注入）；补齐 web_fetch / web_search。
5. 修复"发消息只显示未受理、不派发"：操作员从面板发的任何消息都必须有明确结果（派发工单，或超级Agent 直接答复），不得只留「未受理」。

## 1. 所有权
- UI：internal/ui/**、internal/view/**（侧边栏、字体、工作区/会话页、系统信息展示）。
- Runtime：internal/runtime/**、cmd/**（工作区/会话投影与命令、面板答复、系统/时区事实、工具开关接线）。
- Agent：internal/agent/**（plantask/agentsmd 中间件、web_fetch/web_search 工具）。
- 文档：Lead/后续 docs pass。

## 2. view 契约（UI 定义，runtime 填充）
```go
const SecWorkspace Section = "workspace" // 工作区页
// Section 列表保留已有常量；新增 SecWorkspace。

type WorkspacePanelVM struct {
    Workspaces []WorkspaceEntryVM
    Selected   string // workspace id, "" = super agent
    UpdatedAt  time.Time
}
type WorkspaceEntryVM struct {
    ID, Name, Root, Purpose, Kind string
    Conversations []ConversationVM
    LastAt time.Time
}
type ConversationVM struct {
    ID       string // ticket id
    Number   int
    Title    string
    Workspace string
    CreatedBy string // "me" | "agent"
    Source    string // local/dingtalk/github/feishu/schedule
    Stage     string // display label
    StageID   string
    Attention bool
    LastAt    time.Time
}
// Snapshot:
func (s *Snapshot) PublishWorkspaces(vm *WorkspacePanelVM)
func (s *Snapshot) Workspaces() *WorkspacePanelVM
// OverviewVM 增加：
type SystemInfo struct { OS, Arch, Timezone string; UTCOffset string }
// OverviewVM.System SystemInfo

// UI optional interfaces:
type WorkspaceCreator interface { CreateWorkspace(name, root, purpose, kind string) error }
type ConversationCreator interface { NewConversation(workspaceID, text string) error }
```
- Conversation 由 Ticket 映射：`CreatedBy` = origin Trigger.Source=="local" ? "me" : "agent"；排序按 LastAt desc；只列该 workspace 的 Ticket（含 ended，最多最近 50）。
- Runtime 必须发布 WorkspacePanelVM 与 System；UI 无数据时显示诚实空态。

## 3. UI 结构
- 侧边栏（宽约 220）：顶部大项「超级Agent」（SecOverview，label 超级Agent，计数=NeedsPerson）；「工作区」标题 + 工作区行（展开箭头；展开显示会话行 + 「＋ 新建会话」）+「＋ 新建工作区」；底部「监控」组（消息/来源/触发/评估/执行，执行带 running 计数）+「设置」。
- 超级Agent 页：现有总览内容（待办/健康/最近/用量）不变；输入条保留（全局问答/派活）。
- 工作区页（SecWorkspace）：标题+用途+根目录；会话列表（我创建/超级Agent 创建分组或 badge）；点击会话跳到执行页并选中该 Ticket；「＋ 新建会话」弹窗（预填该工作区）。
- 新建工作区弹窗：名称/根目录/用途/类型(repo/data/docs/mixed) → WorkspaceCreator。
- 字体 +2：theme.FontSize 13→15；所有 `.FontSize(N)` 文本 +2（图标尺寸不强制）；状态栏/侧边栏/表单同步。
- 系统信息：侧边栏底部一行 `OS/arch · 时区 (UTC±h)`；设置-部署组也显示这两项。
- 保留现有快捷键/测试语义；更新 ui/view 测试。

## 4. Runtime
- `CreateWorkspace(name,root,purpose,kind)`：校验+scan 事实+Persist Register（默认 policy 允许 operator）→ Publish。
- `NewConversation(workspaceID,text)`：`engine.Dispatch{DispatchInput{Text,Workspace}}`（durable 用 Persistence），立即 EvaluateDue + wake；Publish；返回 error。
- `WorkspacePanel()`：按上述契约投影；`OverviewVM.System` 用 `runtime.GOOS/GOARCH`、`time.Local.String()`、UTC offset；`deployFacts` 加「操作系统」「时区」。
- 未受理修复：stream() 对 `t.Source=="local"` 且无 Ticket/Proposal 的 Decision，追加 `LineReply` 文本 `超级Agent：...`；若用户问工作区（关键词 工作区/workspace/哪些/有什么）列出所有工作区名+用途；否则说明「没有派发，因为…；你可以选择工作区新建会话，或直接说明要做什么」。保持 Decision 行但把「未受理」改为「已答复」/「未派发」并附原因码。Dispatch 的 composer 错误也给出可操作提示。
- 工具开关：agent.Config 增加 `PlanTasks`、`AgentsMD`、`WebTools`（默认 true），runtime 创建 Runner 时打开；prompt 里提及 task 工具与 web 工具。

## 5. Agent 工具
- plantask：eino `adk/middlewares/plantask`，Backend 用 workspace filesystem backend，BaseDir=ticket dir；提供 task_create/get/update/list 工具；测试工具出现在 agent 工具列表、能创建/读取任务。
- agentsmd：eino `adk/middlewares/agentsmd`，Backend 读 workspace root 的 `AGENTS.md`（+ 可配文件），注入 system/模型输入；缺失文件不报错；测试注入内容出现。
- web_fetch：GET http(s)，10s 超时，2MB 上限，HTML→纯文本（去 script/style/tag），返回标题+正文截断；拒绝 localhost/私网/非 http(s)；测试 httptest。
- web_search：默认 DuckDuckGo HTML 端点（可配 `WebSearchURL`，测试用 httptest），解析结果标题/URL/摘要，返回前 5 条；失败明确错误；测试 parser。
- 所有工具名/描述与 DSH 语义一致，注入到每个 Worker（子智能体按白名单继承）；更新 prompt 工具清单。

## 6. 验收
- `go build ./...`、`go test ./internal/{ui,view,runtime,agent}/... -count=1`、`-race` 相关包通过。
- 手测/单测：侧边栏结构、新建工作区、工作区会话列表、新建会话、全局字体、OS/时区、问"有哪些工作区"得到答复而不是未受理、plantask/agentsmd/web 工具可见可用。
- 现有 UI/引擎语义不回归；不改 go.mod 除非 Lead 批准。

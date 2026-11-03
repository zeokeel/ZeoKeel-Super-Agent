> **历史评审说明**：本报告基于 HEAD `f4200da`（2026-11-04 修复波之前）阅读；修复波后行号/实现已变化，
> 现状以 README、design §13、design/im-channels.md 为准。本文保留下令评审当时的发现与建议，不再逐行更新 file:line。

# 设计评审：Round 2/3 UX / 产品（独立、只读）

- 日期：2026-11-04（评审时区 Asia/Shanghai）
- 审阅对象：`README.md`、`design/zeokeel-go-mygo-design.md` §6/§13、`design/im-channels.md`、`audit/fixes/ui2-spec.md`，
  以及 `internal/ui/{workspace,channels,chrome,app,input,stream,settings}.go`、`internal/view/{vm,pages,channels}.go`、
  `internal/runtime/{workspaces,im,worker}.go`、`internal/runtime/{runtime,pages}.go`（按需）。
- 性质：**只读的 UX/产品判断**。没有改任何代码；没有在本机开窗口（无显示器），没有做真人可用性测试或性能测量。
  所有"会卡/会慢"的判断来自同步调用链阅读，不是秒表数据；"看不到"的判断来自渲染代码阅读，不是截图。

## 结论摘要

新结构**方向对了**：DSH 式“超级Agent + 工作区树 + 底部监控”比七页侧栏更接近用户心智，
原生目录选择器、渠道级 IM 管理、禁用=未监听等诚实设计值得保留。但当前版本有 **4 个 High 级问题**会让新用户
在最初十分钟内得出“这个面板没生效/没反应”的结论：

1. **IM 渠道开关根本打不开渠道**：面板开关写 `"true"`，runtime 只认 `"1"`（详见 F-01）。
2. **“超级Agent”不是一个聊天**：全局输入框只做派活（无工作命中时用关键词模板回答），且回答只出现在「消息」页，
   默认的超级Agent 总览页既不显示对话也不显示刚派出的工单（F-02）。
3. **同一条消息同时得到红色错误和“超级Agent：”回答**，输入框还保留原文（F-03）。
4. **UI 主线程承担 DB/模型调用/渠道重启**：输入、新建会话、保存设置都可能卡住窗口，违反项目自己的 §6.1 铁律（F-04）。

另有 9 个 Medium：工作区上下文不贯穿、会话/工单/任务词汇混用、工作区创建后不能管理、kind 的后果无解释、
“全局字号”实为一次性改数字、IM 传输选项与 runtime 能力相反、onboarding 的“来源”步骤没有效果（选中 GitHub/IM 后实际不接线）、
新工具能力对用户不可见、设置保存的“热更新”承诺不成立。Top 5 改动见文末。

## 严重度定义

- **High**：目标用户会在关键任务（接 IM、发消息、首次配置）上直接失败或得到错误结论；发布前应修。
- **Medium**：不阻塞，但长期造成误解、重复劳动或概念债；下一轮必修。
- **Low**：文案/一致性/边角体验问题，可排期。

## 发现

| ID | 严重度 | 领域 | 判断（为什么，带证据） | 后果 | 建议 |
| --- | --- | --- | --- | --- | --- |
| F-01 | **High** | IM 设置 / 诚实状态 | 面板开关写布尔字符串：`internal/ui/settings.go:365-367`（`strconv.FormatBool`→`"true"/"false"`），渠道卡也按 `== "true"` 读（`internal/ui/channels.go:162,377-381`）；但 runtime 读 `== "1"`（`internal/runtime/im.go:54,60`）。没有任何迁移/归一化（store 只存 kv）。 | 用户在设置里打开“启用飞书/钉钉”、保存，面板显示“已启用；还没有检查过”，**渠道实际永不启动**；检查按钮会按“未启用”的配置工作或直接给出误导结果。这是“设置页保存了但什么都没发生”的最坏形态。 | 统一布尔编码并只保留一个真源（建议 runtime 只认 `true/1` 之一，UI 用同一个 helper；或在 `SaveSettings` 归一化）。补一条“settings VM → IMConfigFromSettings → IMManager 启动”的集成测试（目前测试都直接构造 `IMConfig`，见 `internal/runtime/im_test.go:126`，绕过了这个洞）。渠道卡状态改为读 runtime 投影，不读未保存的 edit。 |
| F-02 | **High** | 信息架构 / 超级Agent 交互 | 规格与 README 都承诺“全局问答”（`audit/fixes/ui2-spec.md:59`“输入条保留（全局问答/派活）”、`README.md:28`“任何问题都可以先问它”），但实现只有派活。全局输入条出现在每一页（`internal/ui/app.go:372-377`），但提交只走 `Commander.Dispatch`（`internal/ui/input.go:37-48`）→ Router；无工作命中时不调用模型，只用关键词模板回答（`internal/runtime/workspaces.go:171-214`、`runtime.go:456-482`）。默认页 Super-Agent 总览只画审批/待办/健康/用量（`internal/ui/overview.go:15-45`），**不渲染消息流**（流在 `internal/ui/stream.go:24` 的「消息」页）。 | “超级Agent”这个名字 + 输入框“说点什么，或派一件事…”承诺了一个聊天/问答入口（DSH 心智），实际是“派活盒”：非工作问题得到一段“没有派发工单”的模板；即使成功派活，输入框被清空，总览页上也没有任何可见结果（工单/回答在别的页）。用户会认为没发出去或坏了。 | 先拍板产品：**(A)** 真的做全局聊天（模型回答、有限历史、就地显示；无工作时明确“没派工单”），或 **(B)** 改名/改文案为“派活”，去掉聊天暗示。无论哪种：把最近一次“已答复/派发 #N”内联显示在输入框上方（所有页面），并让总览页显示与输入直接相关的结果，而不是要求用户换页找答案。 |
| F-03 | **High** | 交互反馈 / 错误语义 | 无工作命中时 runtime 返回 error（`internal/runtime/runtime.go:790-796`），UI 把任何 error 画成输入框下的红字（`internal/ui/input.go:42-44`），并保留草稿；同时同一消息在流里被渲染成「已答复」+「超级Agent：…」回答（`internal/runtime/runtime.go:458-473`、`internal/ui/stream.go:129-200`）。NeedsPerson 的追问也走 error（`runtime.go:775-780`）。 | 同一条消息出现“红色错误 + 已经回答了”两套结论；用户不知道是失败还是成功，可能重复发送；红色被日常“非错误”消耗后，真正的失败不再醒目。 | 区分“有结论”与“失败”：已回答/需人回答应返回成功，用中性内联回答呈现；红色只留给真失败（网络/数据库/无权限）。成功或已答复后清空草稿；错误可关闭且带下一步。 |
| F-04 | **High** | 主线程 / 诚实承诺 | UI 契约写“实现只能入队，不能等文件/数据库”（`internal/ui/app.go:51-56`），`input.go:35-36` 注释也说“Dispatch 是纯内存，不会卡帧”。实际链：`Dispatch` → `p.Dispatch`（DB tx）+ 立即 `EvaluateDue`（`internal/runtime/runtime.go:752-796`）→ `assessor.Assess` 模型调用（`internal/engine/evaluate.go:38-39,49-54`）；`NewConversation` 同链（`internal/runtime/workspaces.go:101-150`）。`SaveSettings` 同步写 store（`internal/runtime/pages.go:568-590`），并在 source 组同步重启 IM：`imAfterSettingsSave`→`ApplyIMSettings`→`applyChannel`，旧渠道 `Stop` 最多等 5s/渠道（`internal/runtime/im.go:189,208-222,239-270`）。 | 弱网/大库时，发送一条消息、新建会话或保存来源设置会让窗口冻结到模型/DB/渠道停止返回；用户看到的是“点了没反应”，而设计文档/代码注释承诺相反。这正是 §6.1 三条铁律要防的事。 | 把这三类写操作移到 runtime 的后台命令队列：UI 只提交并显示“发送中/保存中/正在重启渠道”，用快照回传结果；模型调用永不进主线程。加一个“handler 里禁止同步 DB/模型”的静态测试（类似 `go tool mygo vet` 的 harness）。 |
| F-05 | **Medium** | 信息架构 / 上下文 | 工作区选择只影响「工作区」页与侧栏高亮（`internal/ui/app.go:178`、`workspace.go:294-312`）；「消息/执行/来源」始终全局（`internal/ui/execution.go:20` 明确“全部任务”）。会话行点击跳到全局「执行」页并选中该 Ticket（`workspace.go:280,423`）。 | 侧栏树在视觉上像"当前工作区上下文"，但一旦离开工作区页，所有页面都是全局的；同一条 Ticket 有 3 个入口（消息流、执行列表、工作区会话），用户无法建立“我在哪/我在看谁”的模型。 | 二选一：**(A)** 选中工作区后，「执行/消息」默认按工作区过滤，标题显示“工作区：X（全部）”可清除；**(B)** 取消工作区页的会话列表，工作区只做筛选器，保留唯一的「执行」列表。无论哪种，会话行点击后侧栏要保留工作区/会话的选中态。 |
| F-06 | **Medium** | 概念 / 词汇 | "会话 = Ticket" 只在工作区页副标题出现一次（`internal/ui/workspace.go:322`），代码注释也只在 `internal/view/workspace.go:17`。界面同时用：会话、工单、任务、消息、`#N`、执行；消息页把 Ticket 的 Thread 叫“线程”。 | 新用户无法回答“会话和工单是不是一回事？删掉会话会删工作吗？”；不同页面像不同产品。 | 在名词表（⌘/）加一条“会话（Conversation）= 工单（Ticket）”，全 UI 统一首选词（建议列表处“会话”，详情处“工单 #N”且加注释/链接）；不要把工单编号只画成裸 `#N`。 |
| F-07 | **Medium** | 工作区创建 / 管理 | 创建表单只有一次机会：无重命名/删除/改根目录/改用途（`internal/ui/workspace.go:453-530` 只有创建；runtime 无 Delete/Rename）。重复检查只看 id/name（`internal/runtime/workspaces.go:36-41`），**同一根目录可以建两个工作区**。根目录接受相对路径（`internal/runtime/workspaces.go:22-30`，`expandHome`+`os.Stat` 后原样存储）→ 进程 cwd 变化（launchd/systemd）后工作区失效。kind（repo/data/docs/mixed）会影响回复与 Proposal 文案（`internal/engine/evaluate.go:547-565,591-595`），但表单对类型零解释（`workspace.go:479-481`）。 | 打错名字/路径后只能删库；同一仓库两个容器会让同一活分叉；相对路径在服务模式下静默坏掉；用户随手选 kind，却在“同事请求是否自动回复”上产生看不见的差别。 | 加工作区管理（重命名/改根目录/删除+确认，删除前说明不会删磁盘）；根目录强制绝对路径（可自动 `filepath.Abs` 并提示）；同根目录二次确认或合并提示；kind 加一句后果说明或收进“高级”。 |
| F-08 | **Medium** | 工作区 / 可解释性 | 创建时 `scan.Scan(root)` 收集语言/构建/测试/AGENTS.md 摘要（`internal/runtime/workspaces.go:42`、`internal/scan/scan.go:31-46`），但工作区页只显示 name/purpose/root/kind（`internal/ui/workspace.go:345-375`），别处也不展示这些 facts。 | 用户看不到 Agent 对工作区的“认知”（会什么语言、怎么跑测试、AGENTS.md 是什么），也就无法判断 Router 为什么把活派到这里；出问题时没有可核对的“简报”。 | 工作区页加“Agent 看到的事实”区块（语言/构建/测试/默认分支/AGENTS.md 标题 + 最近扫描时间）和“重新扫描”；给 AGENTS.md 一个来源路径（安全/信任相关，见 F-14）。 |
| F-09 | **Medium** | 字体 / 可访问性 | “全局 +2”是一次性把 theme base 改成 15（`internal/ui/tokens.go:49`）并手改大量字面量（如 sidebar 16/17、标题 20、辅助 10-13，见 `internal/ui/chrome.go`、`workspace.go`）；没有用户可调字号、没有跟随系统字体缩放；侧栏固定 232px（`chrome.go:65`）且工作区名 `SingleLine`，长中文名在 232px/16px 下会被截断；窗口没有最小尺寸约束。 | 高 DPI/小窗口/长名字场景下布局拥挤或截断；需要更大字的用户没有出口；"全局字体"只是一次视觉调整，不是设置项。 | 用一组 token（base/body/caption/heading）替代散落数字；加字号设置（或至少跟随系统缩放）；给侧栏/标题加 ellipsis + tooltip，设窗口最小尺寸，补一张小窗口/长名字的回归截图测试。 |
| F-10 | **Low** | 系统信息 | OS/arch+时区放在侧栏 footer（`internal/ui/chrome.go:92-99`，10px）+ 设置›部署（`internal/runtime/pages.go:763-764`）。但页面上所有时间（`Ago`、日志、定时）没有绑定/标注时区；`time.Local.String()` 在 TZ 未设置时可能是 `Local` 而非真实 zone 名（`runtime.go:172-175`）。 | 用户不知道这个信息该拿来干什么；真正需要时区语义的地方（“明天 9 点”“下次轮询”）反而没有时区。 | 在涉及时间的控件旁显示 tz（或“本机时区”链接），如“下次轮询 14:00（Asia/Shanghai）”；设置里保留事实即可，footer 可缩成 hover/tooltip；对 `Local` 显示“系统本地时区（未命名）”。 |
| F-11 | **Medium** | IM 传输选择 | 传输选项与 runtime 能力相反：设置字段发布了 `ws/webhook`（`internal/runtime/pages.go:460-464`），UI 对已发布 options 一律当 `Implemented=true`（`internal/ui/channels.go:291-305`），于是可选择运行时明确报错“尚未实现”（`internal/runtime/im.go:328-349`）；反过来钉钉 `stream` 在 `internal/view/channels.go:56` 被标 `Implemented:false`（“本版本未接入”），但 runtime 已能构建官方 Stream 驱动（`internal/runtime/im.go:394-398`），Help 也写 stream 可用（`pages.go:469-471`）。 | 用户被允许选择一定会失败的传输；真正能用的 stream 又灰掉——两条路都让“配置 IM”看起来不可靠。 | 只发布并允许**已实现**的选项（Feishu: longpoll；DingTalk: dws+stream）；未实现选项不发布/灰掉并说明原因；补一张 transport×runtime 能力矩阵测试，避免两边再次漂移。 |
| F-12 | **Medium** | 首次配置 / IM 可接入性 | 向导“从哪里接活”步骤（`internal/runtime/onboarding.go:84-94`）保存 `onboard.source_kind/source_repo` 后没有任何消费者：IM 的 enable/凭据没写（`feishu_enabled` 等只在设置页写），GitHub repo 也没写进 `source.github_repo`（只有启动时读，`cmd/zeokeel/main.go:158-161`）。选完来源、完成向导后实际什么也没接。 | 首启用户按向导选了 GitHub 仓库/飞书，期待“能从聊天接活”，实际 Source 未启动；这是 onboarding 的承诺失效，且用户很难自己发现。 | 二选一：向导**真正接线**（选 GitHub 就写 repo 并在完成时启动监听/提示重启；选 IM 就收凭据并启用），或向导明确写“这里只是记录，稍后在设置›来源启用并填凭据”，且完成后在总览显示“来源未启用”的待办。 |
| F-13 | **Low** | IM 状态 / 诚实 | 渠道卡“已启用”来自未保存的 edit（`internal/ui/channels.go:162`），不是 runtime 投影；禁用渠道 health 被标 `ready`（“未启用”，`internal/runtime/im.go:268-273`）；启用/保存后不会自动检查，用户需再点一次“检查通道”。 | 用户可能在保存前就看到“已启用”；source 页的绿色健康可能被读成“连接正常”；多一步才能知道到底通没通。 | 保存后自动跑一次 CheckChannel 并把结果回填；渠道卡状态只用服务端快照；禁用渠道用中性 chip 并在 source 页明确“未启用≠故障”。 |
| F-14 | **Medium** | 工具 / 注意力 / 安全 | 新能力对用户不可见：plantask/AGENTS.md/web 默认对每个 Worker 全开（`internal/runtime/worker.go:480-482`），设置页没有开关或事实（grep 无 tools 组），面板也没有 plan/task 视图（`internal/ui` 无 task 相关渲染）；仓库根 `AGENTS.md` 静默进入模型输入（`internal/agent/builtin.go:96-102`），可作为提示注入源；web_fetch/web_search 默认允许出站。 | 用户不知道 Agent 能上网、能读 AGENTS.md、有计划列表；仓库里的 AGENTS.md 可以悄悄改变行为，缺少知情与关闭手段；出了问题无法从面板定位。 | 在设置›执行（或工作区页）加“Agent 能力”事实/开关（web、AGENTS.md、plan），默认值写清楚；工单详情/消息流显示当前 task 列表与“注入了 AGENTS.md（路径/大小）”；web 默认建议 opt-in 或至少首次使用提示。 |
| F-15 | **Low** | 命名 / 双入口 | 「来源」既是监控页（`internal/view/vm.go:443`）又是设置组名（`internal/runtime/pages.go:450`）；source 页右侧有“设置›来源”的提示（`internal/ui/source.go:43`）缓解，但仍需用户理解两个同名地点的分工。 | 新用户可能先在来源页找“启用飞书”，找不到开关。 | 监控页改名“来源状态/连接”，设置组叫“来源与通道”；或在来源页直接放每个渠道的启用/检查（复用渠道卡），减少跳转。 |
| F-16 | **Low** | 文案 / 心智模型 | 全局状态栏写“一条连续的上下文 · 满了自动压缩”（`internal/ui/chrome.go:141`），但实际是每 Ticket 一个 Worker 会话/上下文（`internal/runtime/worker.go` 的 `ticketWorkdir`、session dir）；总览没有任何“连续上下文”载体。 | 用户以为整个应用共享一段对话，进而误解为什么不同会话不知道彼此；与“工作区/会话”模型冲突。 | 改成“每个会话有自己的上下文；满了自动压缩”；或当打开某 Ticket 时显示该 Ticket 的上下文状态。 |
| F-17 | **Low** | 空态 / 恢复 | 工作区/渠道/来源投影为 nil 时只有“等待运行时发布…”，没有状态/重试入口（如 `internal/ui/workspace.go:160`、`channels.go:62` 的未接线文案虽诚实但无恢复动作）。 | 投影失败时用户困在等待页，不知道是应用坏了还是还没加载。 | 空态旁给“查看状态/日志”（`status` 页或按钮）和“重试”；区分“加载中/未接线/失败”。 |
| F-18 | **Medium** | 设置保存语义 | 每个设置组保存条都写“保存后写入 … 并热更新”（`internal/ui/settings.go:400`、`prompt.go:161`），但 GitHub repo/login 只在启动时读（`cmd/zeokeel/main.go:156-161`），改完需重启；IM 是同步重启渠道；people 立即生效；prompt 是热更新。同一句承诺覆盖四种不同语义。 | 用户改了 GitHub 仓库以为已生效，实际要重启；或改 IM 后界面冻结（F-04）却没有任何“正在重启”提示。 | 按组写清生效方式：“立即生效 / 下次启动生效 / 会重启该渠道（几秒）/ 热更新”；保存后显示后台应用的进度与结果，而不是笼统“热更新”。 |

## 逐项检查结论（对应任务 1–8）

1. **IA**：方向对，但“工作区”只是页面而非上下文；三个入口（消息/执行/工作区）展示同一批 Ticket；会话=工单概念只在个别文案里解释（F-05/F-06/F-15）。
2. **全局输入**：不是聊天，是“派活 + 关键词兜底”；非工作问题只有工作区清单/通用下一步模板；答案藏在「消息」页；成功派活没有就地反馈；红错与已答复并存（F-02/F-03）。必须拍板“聊天还是派活”。
3. **工作区创建**：原生选择器流程简短、取消/错误状态处理正确（`workspace.go:19-83` 异步、有 seam，值得保留）；但字段偏多且 kind 无解释、无管理、根目录可为相对路径、同根可重复（F-07/F-08）。
4. **字体 +2**：不是可调/可访问的“全局字号”，是固定 15 + 大量字面量；小窗口/长名字/高 DPI 有截断风险，且无最小窗口尺寸（F-09）。
5. **OS/时区**：事实真实、位置低调，但与用户真正需要时区的地方（时间/定时）脱节，`Local` 未命名时语义弱（F-10）。
6. **IM**：结构（每渠道卡片/检查/授权/禁用=未监听）合理，但**开关编码 bug 让渠道无法从面板启用**（F-01）；传输选项与能力相反（F-11）；DWS 对非技术用户门槛高、向导“来源”步骤无效（F-12）；保存语义不清且可能冻 UI（F-04/F-18）。
7. **工具**：能力是真实新增的，但对用户完全不可见；AGENTS.md 注入和 web 默认开启缺少知情/开关（F-14）。
8. **诚实/注意力**：没有假在线/假计数（好），但新增了“面板已启用但 runtime 认为关”（F-01）、“热更新”泛化（F-18）、“红色错误+已回答”（F-03）、“顶部聊天承诺”（F-02）四类不诚实/矛盾信号；主线程阻塞（F-04）也违反 §6.1。

## Top 5 UX 改动（建议优先级）

1. **让 IM 真正能连上**（F-01 + F-11 + F-12 + F-13）：统一布尔编码/真源；只展示可运行的 transport；保存后自动检查并显示“正在重启渠道→结果”；向导的“来源”要么接线要么改成“稍后配置”的明确文案；补集成测试。
2. **消灭主线程阻塞**（F-04/F-18）：输入、新建会话、设置保存全部改为后台命令 + 可见进度（发送中/保存中/渠道重启中）；UI 只提交与渲染快照；把“不能同步调 DB/模型”变成测试约束。
3. **给全局输入一个明确身份与就地反馈**（F-02/F-03）：拍板“超级Agent 聊天”还是“派活”；若是聊天就写模型回答与有限历史；无论哪种，输入框上方固定显示最近结果（回答/`#N 已派发`/需你批准），非失败不要红字，回答后清空草稿。
4. **把“工作区”变成真正的上下文**（F-05/F-06/F-07/F-08）：选中工作区后监控页默认过滤（可切“全部”），或去掉工作区页的会话重复列表；名词表写清会话=工单；补工作区管理（改名/改根/删除+确认）、绝对路径/同根校验、kind 后果说明与 facts 展示。
5. **可信度与能力可见**（F-14 + F-09/F-10）：设置里露出“Agent 能力”（web/AGENTS.md/plan）与事实，工单/消息流显示 task 列表与 AGENTS.md 来源；字号做成 token/可调并处理侧栏截断；时区出现在真正涉及时间的控件旁。

## 值得保留的做法（不是客套）

- 禁用渠道=未启动、状态写“未启用”，健康不会假装在线（`internal/runtime/im.go:268-273`、`internal/ui/channels.go:244-290`）。
- 设置不做乐观更新：保存前显示服务端值、冲突提示（`internal/ui/settings.go:443-486`）。
- 原生目录选择器在 goroutine 里跑、有测试 seam、取消不报错（`internal/ui/workspace.go:19-83`）。
- 无工作立即回答而不是沉默（虽然交互形态要改，见 F-02/F-03）。
- 未接线动作会明说“动作未接线”，按钮禁用而不是假装成功（`channels.go:62`、`workspace.go` 的 wired 分支）。

## 限制与开放问题

- 无显示器/未做真人测试：F-09 的截断、F-04 的实际卡顿秒数均为静态推断；建议接 GUI/性能实测后再定级。
- 待拍板：超级Agent 是“聊天”还是“派活”（决定 F-02/F-03 修法）；工作区是否需要成为全局过滤上下文（决定 F-05）。
- 本报告只读：未改代码、未改文档；相关代码问题请交由对应 owner（F-01/F-04/F-11 的 runtime+ui；F-12 onboarding；F-14 agent/runtime）。

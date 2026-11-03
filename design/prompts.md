# 提示词设计（对照 dsh 的评估与改造）

这份文档记录一次系统的提示词评审：对照 dsh 的做法，评估我们原来的提示词，并说明改造后的结构。

## 一、评估：我们原来的问题

| 维度 | dsh | 改造前的 ZeoKeel |
| --- | --- | --- |
| 组织方式 | 有序 section 组装（identity / persona-prefix / plan policy / team policy / 每个工具一节 / 交付物引用 / 结构化输出 / web surface），带变量插值 | 单个常量字符串，散落在各包里 |
| 部署定制 | 有 `deployment:persona-prefix/suffix` 插槽，preset 可整体替换 | 无；只能整体覆盖 |
| 工具描述 | 参数级，解释语义与失败行为（"Run in the background and return a job id…"） | 一行泛泛而谈（"Run a shell command…"） |
| 完成契约 | 明确的结构化输出与交付物引用 | 没有；模型写完一段话就结束，工单悬着 |
| 升级/求助 | 有审批与沙箱策略节 | 没有；真实模型只能把问题写在正文里 |
| 测试 | — | 无任何断言，改一句话没人知道坏了 |
| 语言 | 提示词与本地化分离 | 中英混用且不固定 |

## 二、改造后的结构

新增 `internal/prompts`：有序 section + `{{variable}}` 插值。

```go
type Section struct{ Name string; Order int; Body string }
type Set struct{ sections []Section }
func (s *Set) Render(vars map[string]string) string
func Expand(body string, vars map[string]string) string
```

### Worker（执行器）分节

| section | 作用 | 来自哪个失败模式 |
| --- | --- | --- |
| identity | 无人实时对话，要么干完，要么说清缺什么 | — |
| workspace | 工作目录已经是工作区根；不要自建同名子目录 | 真实模型建了 `webapp/` 子目录 |
| tools | 先看再改，不猜文件/API/目录 | — |
| verification | 最小改动 + 实际跑过的验证；没验证要说没验证 | — |
| completion | `present()` 每个交付物 → `report(ready)` | 模型只回一段话，工单悬着 |
| escalation | 模糊/需要人决策 → `ask_user_question` → `report(blocked)` | 真实模型把问题写进正文并报 ready |
| long_tasks | 几小时的任务在里程碑处 `report(progress)` | 用户明确说任务会跑十几小时 |
| safety | 不出工作区、不外泄密钥、破坏性操作要批准 | — |
| language | 用请求的语言回答 | — |

### Router（研判）分节

identity / candidates / decision / accept_contract / reason_codes / output。
`accept_contract` 与 `reason_codes` 是硬约束：少一条整批作废，码不在表里面板显示不出来。

### 子智能体

explorer（只读）与 verifier（只跑命令）各有独立 section：身份、允许的动作、输出格式（≤150 词、结论先行、证据带路径）。

## 三、工具描述重写

每个工具描述从"做什么"改成"何时用 + 语义 + 失败怎么表现"：

| 工具 | 改造前 | 改造后 |
| --- | --- | --- |
| `bash` | Run a shell command… | 说明 cwd 是工作区根、非零退出码是**结果不是失败**、长输出会截断并给落盘路径 |
| `write` | Write a text file… | 路径相对工作区、越界会作为结果返回、父目录自动建 |
| `edit` | Replace one exact occurrence… | 必须先读文件、old_text 必须唯一 |
| `report` | Report this ticket's state… | 何时调用（完成/里程碑/无法继续）、summary 会直接进回执 |
| `present` | Present a deliverable… | 每个交付物一次，先 present 再 report |

## 四、补上缺失的能力：`ask_user_question`

原先执行器只有 `report` / `present`，**没有"正式提问"的通道**。真实模型于是把问题写在正文里，然后报告 ready——没人被明确询问。

现在：`ask_user_question(question, options)` → `Attention` → 工单进入"等你回答"、出现在总览待办，回执也会说明在等什么。

## 四点五、评审中另外发现的两个"假承诺"

| 位置 | 假承诺 | 修复 |
| --- | --- | --- |
| 提示词编辑器第三层（评估规则说明） | 能编辑、会保存，但从未进入研判提示 | `assess.Config.RulesNote` 注入为「团队规则说明」节 |
| 技能注入块 | 写着"可按名字用 skill 工具加载"，但没有这个工具 | 新增 `load_skill` 工具（仅在配置了技能库时出现） |

共同点：**提示词里承诺的能力必须真的存在，否则模型会尝试一扇不存在的门**。

## 五、提示词编辑器与变量

- 编辑器显示 `WorkerTemplate()`，保留 `{{workspace}}` / `{{purpose}}` 占位符
- 运行时先读覆盖值再 `Expand`，所以用户改过的模板仍然按工作区展开
- 有测试保证：改过的提示词能到达模型、占位符被展开、未知变量保持可见（`{{who}}` 不会被静默抹掉）

## 六、测试策略

不测"提示词全文"（改一个词就红），只测**必须保住的不变量**：

- Worker：工作区变量已展开、`present()`、`report(ready)`、`ask_user_question`、验证纪律、随请求语言
- Router：四种 kind、accept 五要素、原因码表、单 JSON 输出
- 子智能体：只读/只验证的边界、输出格式
- 组装：section 顺序、覆盖、删除、未知变量

## 七、效果

真实模型（qwen3.8-flash-next，1M 上下文）验收：

| | 改造前 | 改造后 |
| --- | --- | --- |
| 研判 | 12.6–16.5s | **8.8s** |
| 执行 | 24.6s | **12.6s** |

更快是因为提示词更短、更少歧义（旧 Worker 提示词用英文写了一段"工作目录即根"，新版本按 section 精炼），模型少了犹豫与重复探测。

## 八、大模型缓存（KV cache / prompt cache）利用检查

### 原理
OpenAI 兼容端点的前缀缓存按**序列化后的前缀字节**命中：system 提示、工具列表、历史消息
只要与上一次请求逐字节一致，后面新增的部分才需要重新计算。所以"是否利用缓存"不是配置项，
而是**请求是否稳定**的推论。

### 我们做了什么才让它稳住
| 会破坏缓存的做法 | 我们的做法 |
| --- | --- |
| 系统提示里塞时间戳/随机 id/每轮变化的内容 | 系统提示只由工作区名与用途决定，同一 Ticket 内恒定 |
| 工具列表顺序来自 map | 工具是切片，顺序确定；MCP 工具按 server 顺序追加 |
| 每轮重写历史（加标记、改格式） | 历史只追加；只有压缩（summarization）会重写，且是显式事件 |
| 把任务相关的内容放在系统提示最前面 | 技能块追加在系统提示**末尾**，与任务无关的前缀保持稳定 |

### 可观测：采集并显示缓存命中
- eino 的 `PromptTokenDetails.CachedTokens` → `agent.Usage.CachedTokens` → 事件 → 面板
- 消息页每次调用显示「缓存命中 99%（3220/3247）」
- 总览运行卡片显示本次运行的加权命中率

### 实测（qwen3.8-flash-next，真实端点）
```
calls=5  prompt=11559  cached=9341 (81%)  completion=531   13.56s
面板逐轮：0% → 99% → 94% → 99% → 99%
```
首轮必然 0%（没有前缀可复用），之后接近上限。**81% 的总体值被首轮摊薄**，这不是缺陷。

### 结构性测试（不依赖真实模型）
`internal/agent/cache_test.go`：
- 一次运行内的多次请求：system 逐字节相同、工具列表与顺序相同、历史是上一条请求的前缀
- 恢复会话后：前缀不变，只追加

### 还能更好的地方
- 跨 Ticket 复用：系统提示里含工作区名与用途，所以同一工作区的多个 Ticket 共享前缀（已经成立）
- 不同工作区之间不共享（这是有意的：提示词要写明"你在哪个工作区"）
- 压缩会一次性让缓存失效，但换来后续更小的上下文，净值仍为正

## 九、Router 与工作区：它能建吗？它怎么知道近况？

### Router 不能创建工作区（有意为之）

工作区的创建是**人的动作**（首次向导、设置页、`tools/seedstate`）。理由与"提议≠接受"同源：
Router 一旦能自建工作区，就可以把任何消息塞进一个自己刚编出来的目标里，而"派错地方"
比"多问一句"贵得多（design §4.3）。

但 Router 是**第一个发现缺工作区的人**。所以它被要求用 `kind=ask` 说清楚缺什么：

> 这看起来是哪类工作区（repository / data / docs 之一）、它该负责什么、哪些关键词应该路由给它。

操作员据此决定要不要建。人建完之后补上 Keyword，下一次就能自动路由。

### 每个工作区给 Router 一段 ~200 字近况

`internal/brief.Digest` 由**持久数据实时推导**（不缓存、不落库，避免"缓存过期恰好出现在最需要它的时候"）：

| 来源 | 贡献 |
| --- | --- |
| 近 14 天该工作区的工单 | 「近两周 3 个工作项（已完成 1）：登录超时 …」 |
| 交付物 | 「产出 1 个交付物」 |
| 扫描到的目录大纲 | 「目录：src(ts · 120 个文件), docs, scripts」 |
| 最近活动时间 | 「最近活动 2 小时前」 |

上限 200 runes（按 rune 截断，不会切出坏 UTF-8），确定性输出——同一批工单永远得到同一段文字，
这让提示词 diff 可读、缓存前缀稳定。

### 内容大纲从哪来：真正的扫描器

`internal/scan` 读工作区（只读、有上限）：

- `.git/config`/`HEAD` → 远端与默认分支（不调用 git，启动路径不依赖外部进程）
- `go.mod` / `package.json`+`tsconfig.json` / `pyproject.toml` / `Cargo.toml` → 语言与包管理器
- 由语言推导 build/test/lint 命令（Go: `go test ./...` 等）
- 顶层目录大纲：每个目录的**主导扩展名 + 文件数**（2 层、最多 500 个文件）
- `AGENTS.md` / `README.md` 首个标题 → 一句话定位
- 最近修改时间 → 活动度

**扫描只读不写**（有测试断言目录树未变），启动时扫一次，也可按工作区刷新。

### 分工

| 谁写 | 内容 |
| --- | --- |
| 人 | 目的、范围、不归它管、关键词、约束 |
| 机器（扫描） | 远端、分支、语言、命令、目录大纲、AGENTS.md 摘要、活动时间 |
| 机器（推导） | 近两周工单与交付物摘要（digest） |

三者在 `brief.RenderWith` 里合成一段给 Router 看，人写的部分永远不会被机器覆盖。

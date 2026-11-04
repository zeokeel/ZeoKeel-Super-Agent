# ZeoKeel Super Agent 1.0.0-rc.3 — 发布说明

**状态：发布候选（2026-11-04）。** `VERSION=1.0.0-rc.3`（由发布负责人 bump），tag `v1.0.0-rc.3`。
完整清单见 [RELEASE.md](RELEASE.md)，变更见 [CHANGELOG.md](CHANGELOG.md)，设计状态见 [design/zeokeel-go-mygo-design.md](design/zeokeel-go-mygo-design.md) §13。

## 最重要的变化：对齐 DSH 主链

- **明确请求直接接单**：admitted 来源（飞书/钉钉/GitHub）的**明确**请求由 Eval 直接创建 Ticket，
  不再“每条 IM 消息先等批准”。重复投递仍只产生一个 Ticket。
- **推断请求看 accept mode**：`off` 忽略、`observe` 只记录、`auto` 必须匹配规则；
- **旧式审批只是 opt-in**：只有 Policy 显式 `RequireApproval` 且请求是推断时，才产生 `DecPropose`；
  收件箱把它标为「旧式请求（需要批准）」，批准才建单/拒绝无损可撤销。`AutoApproveIM` 已移除。

## 项目级浏览器 + MCP

- 项目页新增 **概览 / 设置 / 统计**：浏览器开关、Chrome/Edge（只检测默认安装路径）、无头、CDP 端口 0=自动、
  profile 目录只读展示；每个项目独立 profile（`<stateDir>/workspaces/<ws>/browser-profile`）与唯一端口；
  启动/停止/状态可见；技能可全局默认开、按项目关闭；统计显示会话/任务/进行中/等待/完成/失败与工具可用性。
- 启动时同时拉起 `npx -y chrome-devtools-mcp@latest --browserUrl http://127.0.0.1:<port>`；
  工具名 `mcp__browser__*`，**只注入该项目的 Worker**；浏览器子进程环境剥离凭证变量。
- 需要 Chrome/Edge + Node.js/npx；缺依赖/端口冲突/启动失败会给出可操作错误，不会假装有浏览器工具。

## 工具

- 新增 `read_image`（**只读元数据/尺寸/格式，模型端点 text-only，不附像素**）、`job_list`（job_output/list/kill 三件套）、
  `subagent` 名称与 DSH 对齐。
- Worker 域工具 `ack` / `handback` / `thread_unrelated` / `request_status`。
- **SuperAgent 聊天不带工具**（它不是 orchestrator，只回答文本）。
- **仍缺** `orchestrator_read`（Worker 分页读 Ticket/Trigger/Thread 资源）与 DSH main Session 的 orchestrator
  工具集（主 Session 是确定性 assessor，无 tool loop）——这是有意缺席/已知缺口，见 `audit/tool-gap.md`。

## 诚实限制

- **没有真正在原项目执行**：Agent 只在执行副本里工作，成果用「应用到项目」显式回写（带 `.bak-<timestamp>` 备份）。
- **系统钥匙串未实现**：API Key 以明文存 SQLite（文件 0600）；读取侧已 redaction，worker 环境已剥离凭证。
- **多窗口未实现**；**自动更新只检查不下载/不安装**；真实飞书/DWS 租户 E2E 未在本机验证。
- `read_image` 无视觉；`orchestrator_read`/DSH main orchestrator 工具缺失；浏览器需 Chrome/Edge+node/npx。
- 打包仍只有 tar.gz / 未签名 `.app` zip / Windows zip + `install.ps1`；无 `.dmg`/deb/PKGBUILD/NSIS。

## 验证

负责人 bump VERSION 后运行：

```sh
sh scripts/verify-fixes.sh
sh scripts/release-check.sh
```

通过后打 tag `v1.0.0-rc.3` 并发布。本文档不预称测试全绿；旧 rc 快照不作为 rc.3 的证据。

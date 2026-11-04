# ZeoKeel Super Agent 1.0.0-rc.4 — 发布说明

**状态：发布候选（2026-11-04）。** `VERSION=1.0.0-rc.4`，tag `v1.0.0-rc.4`。
完整清单见 [RELEASE.md](RELEASE.md)，变更见 [CHANGELOG.md](CHANGELOG.md)，设计状态见 [design/zeokeel-go-mygo-design.md](design/zeokeel-go-mygo-design.md) §13。

## 这版最重要的变化：去掉历史包袱

- **旧式审批/提案机制彻底删除**：Decision 只有 `accept` / `stop` / `ignore` / `unavailable`；
  没有 Approve / Reject / UndoReject / proposal / `RequireApproval`，也没有任何审批 UI/文案。
  旧数据库里的 `propose` 行载入时转为 `ignore` 并记 `legacyImport`；旧 `requireApproval` 被忽略。
- **admitted 明确请求直接接单**：IM/GitHub 的明确请求由 Eval 直接创建 Ticket；推断请求按 accept mode
  `off` / `observe` / `auto`（auto 需要匹配规则）。
- **收件箱 attention-only**：只显示任务问题/受阻/失败，不再有批准/拒绝/撤销。

## 浏览器 MCP 前置检查

- 新增 `browser.Prerequisites`：检测 Node.js / npx 及版本。
- 缺失时设置/项目页显示醒目警告、当前系统安装命令（brew / winget / apt 等）、nodejs.org 链接与「重新检查」；
  「启动浏览器」被禁用并说明原因。**不会自动安装，也不会假装浏览器在运行。**

## Worker `read_resource`

- DSH `orchestrator_read` 的等价物：按 offset/limit 分页读**本 Ticket 的** ticket / inputs / trigger / thread，
  只读、归属与路径校验、文本 redaction（默认 20、上限 200）。
- Worker prompt 已列出该工具；**SuperAgent 仍然不带任何工具**（它是聊天，不是 orchestrator）。

## 工具/状态（rc.4 汇总）

`read_image`（元数据-only，无像素；png/jpeg/gif 维度，webp 只识别格式）、`job_list`、`subagent` 命名、
Worker 域工具 `ack/handback/thread_unrelated/request_status`、`read_resource`、按项目的 `mcp__browser__*`。
DSH main Session 的 orchestrator 工具集仍有意缺席（主 Session 是确定性 assessor，无 tool loop）。

## 诚实限制

- **没有真正在原项目执行**：Agent 只在执行副本里工作，成果用「应用到项目」显式回写（带 `.bak-<timestamp>` 备份）。
- **系统钥匙串未实现**（intentionally）：API Key 以明文存 SQLite（文件 0600）；读取侧已 redaction。
- 多窗口未实现；自动更新只检查不下载/不安装；真实飞书/DWS 租户 E2E 未在本机验证。
- `read_image` 无像素（需要 vision endpoint 才有真正视觉）；DSH main orchestrator 工具集缺席。
- 浏览器需要 Chrome/Edge + Node.js/npx；打包仍只有 tar.gz / 未签名 `.app` zip / Windows zip + `install.ps1`，
  无 `.dmg`/deb/PKGBUILD/NSIS/签名公证。

## 验证

负责人 bump VERSION 后运行：

```sh
sh scripts/verify-fixes.sh
sh scripts/release-check.sh
```

通过后打 tag `v1.0.0-rc.4` 并发布。本文档不预称测试全绿；旧 rc 快照不作为 rc.4 的证据。

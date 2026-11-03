# ZeoKeel Super Agent 1.0.0-rc.2 — 发布说明

**状态：发布候选（2026-11-04）。** `VERSION=1.0.0-rc.2`（由发布负责人 bump），tag `v1.0.0-rc.2`。
完整清单见 [RELEASE.md](RELEASE.md)，变更见 [CHANGELOG.md](CHANGELOG.md)，交互契约见 [design/zeokeel-go-mygo-design.md](design/zeokeel-go-mygo-design.md)。

## 这版最重要的变化

- **聊天终于像聊天**：回答流式到达（带打字光标，可「停止生成」），支持 Markdown（标题/粗斜体/行内代码/
  围栏代码块/列表/http(s) 链接）；最终文本只追加一次，不会重复。
- **计划看得见**：从 `.zeokeel/plan/*.json` 投影计划/待办卡，执行详情和对话里都能看到步骤、状态与“3/5 完成”。
- **成果能回项目**：执行详情「应用到项目」把记录过的交付物从执行副本复制到项目根；越界路径拒绝，
  同名文件先备份 `.bak-<时间戳>`，确认框列出目标/文件/覆盖警告。
- **拒绝可以反悔**：拒绝无损，10 秒内可撤销回到待批（幂等；同一 Thread 有更新决策则不可撤销；重启安全）。
- **更像桌面应用**：原生 App/文件/编辑/窗口菜单（新建对话 ⌘N、设置 ⌘,、打开主窗口、标准编辑 roles）；
  系统 TextScale/ReduceMotion/HighContrast/Accent 生效；设置›关于有版本/许可证与「检查更新」（只查不下载）。

## 保留的诚实限制

- **没有真正在原项目里执行**：Agent 只在执行副本里工作，需要显式「应用到项目」回写（带备份/覆盖确认）。
- **系统钥匙串未实现**：API Key 以明文存 SQLite（文件 0600）；读取侧已 redaction，worker 环境已剥离凭证变量。
- **多窗口未实现**；自动更新只检查版本，不下载/不安装。
- **真实飞书/DWS 租户 E2E 未在本机验证**；连接向导只验证凭据/订阅。
- 打包仍只有 tar.gz / 未签名 `.app` zip / Windows zip + `install.ps1`；无 `.dmg`/deb/PKGBUILD/NSIS。

## 验证

全量闸门由负责人在 bump VERSION 后运行：

```sh
sh scripts/verify-fixes.sh
sh scripts/release-check.sh
```

通过后打 tag `v1.0.0-rc.2` 并发布。本文档不预称测试全绿；rc.1 的历史快照不作为 rc.2 的证据。

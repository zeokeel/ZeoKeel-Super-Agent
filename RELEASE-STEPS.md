# 从 rc 到 1.0.0：发布动作清单

**先读这一段（2026-11-04 最终代码）：** rc.1 代表“候选版可构建”，不代表产品完成；全量闸门由 Lead 合并前统一跑。
`RELEASE.md` F 组仍有阻塞项（**打包无 .dmg/deb/安装器**、**系统钥匙串未做**、设置页规则/admission/Source 实例不可编辑、
飞书 webhook/IM 媒体下载、`steer/inject` 等），它们全部关闭前，本文的发布动作**不能**把 rc 变成 1.0.0。
IM 入站已改为审批优先（每条消息先待批，Approve 才建单）。

本文件剩下的部分是“具备硬件/凭据才能做”的发布动作。每完成一项勾掉；
F 组清空且文档最终 pass 通过后，才把 `VERSION` 改成 `1.0.0` 并重跑：

```sh
sh scripts/verify-fixes.sh
sh scripts/release-check.sh
```

## 1. macOS 打包（需要一台 Mac）

```sh
# 在一台 macOS 上
sh scripts/package.sh          # 产出 dist/zeokeel-1.0.0-rc.1-macos-arm64.zip
cd dist/darwin

# 签名（需要 Developer ID Application 证书）
codesign --deep --force --options runtime \
  --sign "Developer ID Application: <你的名字> (<TEAMID>)" ZeoKeel.app
codesign --verify --deep --strict --verbose=2 ZeoKeel.app

# 公证（需要 apple-id 与 app-specific password）
ditto -c -k --keepParent ZeoKeel.app ZeoKeel.zip
xcrun notarytool submit ZeoKeel.zip --apple-id <id> --team-id <TEAMID> \
  --password <app-specific-password> --wait
xcrun stapler staple ZeoKeel.app

# 做 .dmg（hdiutil 只有 macOS 有）
hdiutil create -volname "ZeoKeel" -srcfolder ZeoKeel.app -ov -format UDZO \
  zeokeel-1.0.0-macos-arm64.dmg
```

- [ ] 签名通过 `codesign --verify`
- [ ] 公证通过并 `stapler staple` 成功
- [ ] `.dmg` 产出，双击能打开
- [ ] 在一台**没装过 Go** 的 Mac 上装一次、开一次、完成向导

## 2. Windows 安装验证（需要一台 Windows）

```
powershell -ExecutionPolicy Bypass -File install.ps1
```

- [ ] `%LOCALAPPDATA%\ZeoKeel\zeokeel.exe` 存在
- [ ] 开始菜单出现 ZeoKeel
- [ ] 首次启动进向导，能保存
- [ ] `uninstall.ps1` 能清干净（数据目录保留是有意的）

## 3a. 真实模型端到端（最快的一条）

有一条随时能跑的验收，用环境变量传凭据，**不会进仓库**：

```sh
export ZEOKEEL_LIVE_BASE_URL=http://<host>:8080/v1
export ZEOKEEL_LIVE_API_KEY=<key>
export ZEOKEEL_LIVE_MODEL=<model id>

go test ./internal/runtime/ -run Live -v
```

**只在本地跑**：CI 明确 `-skip Live`，也不注入 `ZEOKEEL_LIVE_*`。真实端点和密钥永远不进仓库、不进 CI。

它验证两件事：

- `TestLiveEvalPicksTheWorkspace`：真实模型读消息、在候选工作区里选对、生成可校验的 Decision
- `TestLiveWorkerDelivers`：真实模型调真实 shell 工具、产出 `hello.txt`、工单 ready、回执请求方

没设 `ZEOKEEL_LIVE_BASE_URL` 时这两个测试自动 SKIP，不会让 `go test ./...` 变红。

## 3. 真实凭据端到端（需要你的账号）

至少打通一条，两条更好：

**GitHub**
```sh
./zeokeel -repo owner/name -gh-login <agent账号> -model ... -assess-model ...
# 在 issue 里 @agent 提一个请求，观察：Trigger → 待批单 → 批准 → 工单 → 交付物 → 评论回写
```
- [ ] 评论被识别为同事请求（生成待批单，不直接开工）
- [ ] 批准后工单执行，产出交付物
- [ ] 回执评论出现在原 issue，且带幂等 marker
- [ ] 重复投递不产生第二条评论

**飞书 / 钉钉（入口已接线；真实凭据端到端未验证）**
- [ ] 应用凭据填进设置›来源（或 `-feishu-*`/`-dingtalk-*` flag），来源显示"已配置/连接中/ready"
- [ ] 未配置时不监听、不显示"在线"（负例也要验）
- [ ] 群里 @agent 一句话 → 面板「来源」页看到轮询计数
- [ ] 你自己账号发的消息被识别为 Self（无条件执行），同事的被识别为待批单

## 4. 收尾

先做一次真实运维冒烟（Wave 2 起可执行）：

```sh
./zeokeel setup --state-dir /tmp/zk --model <m> --base-url <u> --api-key <k>   --workspace demo --root /tmp/demo --people github:<you>
./zeokeel status --state-dir /tmp/zk --json    # 期望 exit 0（或 3 表示还缺 setup，按输出处理）
./zeokeel backup --state-dir /tmp/zk --out /tmp/zk-backup.db
./zeokeel stop --state-dir /tmp/zk --no-signal   # 写 STOPPED；status 应返回 0（manually_stopped=true）
./zeokeel -headless -state-dir /tmp/zk &       # 然后 kill -TERM；日志里应看到 exitCode 0/3（启动会清 STOPPED）
```

- [ ] `status/check/setup/backup/archive/-headless` 冒烟全部符合退出码语义
- [ ] `VERSION` 改为 `1.0.0`
- [ ] `CHANGELOG.md` 的 `[1.0.0] — 未发布` 改成发布日期
- [ ] `sh scripts/release-check.sh` 输出 `0 失败`
- [ ] `make package` 用正式版本号重新出包
- [ ] 打 tag：`git tag -a v1.0.0 -m "ZeoKeel Super Agent 1.0.0" && git push --tags`

## 已知限制（写进发布说明；本版为 2026-11-04 最终代码事实）

- macOS 包在完成第 1 步前是未签名的，首次打开需右键 → 打开；`.dmg`/deb/PKGBUILD/自动更新未实现。
- Windows 只有 zip + `install.ps1`，没有 MSI/NSIS 安装器；CI 只交叉编译 + 查体积，不出包。
- **IM 渠道可启停但入站=审批优先**：飞书新装默认官方长连接（ws，推荐）、旧配置迁移 pin longpoll（fallback）；钉钉默认 DWS 个人 OAuth CLI
  （长驻 NDJSON，无 poll/cursor），官方 Stream 为 bot 模式可选 transport；每条消息（含操作员）先成为待批准项，
  Approve 才建单，Reject 无痕。飞书 webhook/媒体下载未实现；真实凭据/DWS 租户 E2E 未验证。
  发布说明里不得写“IM 全平台稳定/自动接活”。
- 设置›来源可配置 IM 三态启停/transport/凭据/检查/授权；但**没有 Source 实例表**（GitHub 仍 `-repo/-gh-login`，
  改设置需重启）；评估组只读（规则/admission/accept mode 不可编辑）。
- 提示词：研判提示热更新 ✓、Worker 下一次运行生效 ✓；历史/base-hash/Default updated 已接线（每层最近 20 版），
  面板还没有历史列表页。
- 设置：UI 开关 canonical 1/0；启动一次性迁移旧 bool/IM enabled/transport/im_self；`ui.font_scale` 可在设置›部署调（0.9/1.0/1.1）。
- **系统钥匙串未实现**：API Key 明文存 SQLite（文件 0600）；worker shell 已剥离凭证变量，读取侧已 redaction。
  备份数据库时仍按密钥对待。
- `zeokeel stop` 写 `<stateDir>/STOPPED` 手动停标记（`status` 在“手动停且未运行”时返回 0），可选 `--pid` 发 SIGTERM；
  `--respect-stop` 没有独立 flag，语义由 stop 标记 + 巡检脚本承担。
- **最终代码 2026-11-04**：全量闸门由 Lead 在合并前跑 `sh scripts/verify-fixes.sh` + `release-check.sh`；
  本文不预称全绿。此前 2026-11-03 快照全绿只作历史参考。
- 没有开机自启、休眠唤醒、自动更新（systemd/launchd 样例可用，但开机自启靠系统配置）。
- workflow 只有 `-pipeline` 的持久层在用；agent 工具侧没有 workflow 编排。
- `design/prototype/` 只是视觉参考（已钉 `mygo v0.7.4` 可渲染），不要回迁代码到 `internal/ui`。

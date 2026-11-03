# fix-docs-ci handoff — Wave 1（docs / CI / tooling）

日期：2026-11-03　角色：fix-docs-ci　scope：`README.md`、`CHANGELOG.md`、`RELEASE.md`、`RELEASE-STEPS.md`、`design/**`、`.github/**`、`scripts/**`、`packaging/**`、`Makefile`、`VERSION`、`.gitignore`。
未改 `internal/**`、`cmd/**`、`go.mod/go.sum`、`audit/AUDIT.md`、分片报告；未 commit；未 `go mod tidy`。

## 完成的条目（引用 audit 编号）

- **N-4（P0）CI 与零 cgo 冲突**：`.github/workflows/ci.yml` 重写。
  - `check` job 只跑一个步骤：`sh scripts/verify-fixes.sh`（本地/CI 同一个闸门，不再有隐藏差异）。
  - `-race` 步骤只在**该步骤内**用 `CGO_ENABLED=1`（脚本第 5 步），产品 build/test 全部 `CGO_ENABLED=0`；注释与 `design/ops-runbook.md`、`Makefile`、`RELEASE.md` 都解释了这条例外。
  - window job 修复：先跑 `-demo`（退出 0），再用 `timeout 60 ... ./cmd/zeokeel` 起真实窗口，退出码 124 视为"存活到超时"（不再挂死 CI）；陈旧 M0 注释删除。
  - 新增 `scripts/verify-fixes.sh`（work item 6）：零 cgo build / test / vet / check-nocgo / `CGO_ENABLED=1 -race`；`SKIP_RACE=1` 供无 gcc 环境，发布闸门禁止使用。
  - `scripts/release-check.sh` 重写：工程/测试部分直接调用 `scripts/verify-fixes.sh`（唯一来源），发布专属检查（VERSION、产物、文档、验收测试、SKIP 列表）保留；产物名改为 `$VERSION`（避免 1.0.0 与 rc.1 文件名漂移）；IM 接线决策列入 SKIP。
- **文档真实性（AUDIT §6 / 本切片 004-006 的 overclaim）**：
  - `README.md` 重写：新增"当前状态"表（已实现/未接线/未实现）、rc.1 不等于 1.0.0、运维现状、密钥明文说明、完整"已知限制"。
  - `CHANGELOG.md`：`[1.0.0-rc.1]` 段改为如实说明（F 组 12 项阻塞 + "Wave 1 文档校正"清单）；IM 驱动标注"未接线"；崩溃恢复/`Runner.Resume`/提示词热更新/诚实计数/打包/测试数逐条校正；1.0.0 "定义"改为"目标（未全部满足）"。
  - `RELEASE.md` 重写：A–G 组；只勾选已验证项；新增 F 组 12 项阻塞清单（对应 AUDIT §7）；G 组明确 rc.1 语义。
  - `RELEASE-STEPS.md`：开头加"rc.1 未完成"说明；"已知限制"改为 Wave 1 事实（IM 未接线、恢复范围、Attention、status/logs/keychain、workflow、prototype 仅参考）。
- **设计文档校正（work item 3）** `design/zeokeel-go-mygo-design.md`：
  - §11（现编号 §13，消除与风险登记册的重号）整表重写：每行 ✅ 均带 `file:line`，其余 ❌/🟡 写原因；新增 §1.2/附录 A 能力清单一行为 ❌；"明确未做"扩充。
  - §4.1 类型映射改为"目标 vs 现状"（Source/Thread/Eval/UserError/Input/Follows 标 ❌/🟡 + 证据）。
  - §6.7 生命周期表改为三列（原型机制/目标/现状）：单实例 ✅（含"锁在 store 恢复之后才拿"缺陷）、托盘+关窗 ✅、自启/watchdog ❌、status ❌（health 表无读者）、休眠唤醒 ❌、托盘重启 ❌；**删除旧的 0/1/2 写法**。
  - §7.3 归档改为"计划，尚未实现"，并给出 Wave 2 验收；§7.1 表下加"现状校正"（SchemaVersion=2、v3 marker 旁路、无 trigger_thread/assignment 表、health 无读者）。
  - §8.1 模式/CLI 目标 vs 现状（`-headless` 是 demo；status/check/login/setup/backup/archive 均未实现）。
  - §8.2 首次配置目标 vs 现状（向导四步已实现；无 gh auth 校验、无 prepare、无钥匙串、**设置不落库**）。
  - §8.3 打包校正（tar.gz / 未签名 .app zip / Windows zip+install.ps1 有；.dmg/NSIS/deb/PKGBUILD/updater 无；CI 不出包）。
  - §8.4 可观测性→**§8.5** 目标 vs 现状；**0/1/2/3 定义只保留一处**（006:113-124 语义），解决旧 §6.7(0/1/2) vs §8.4(0/1/2/3) 矛盾。
  - ADR-12 凭据行改为"目标钥匙串 / 现状明文 SQLite 0600"；§6.5 深色主题标现状未接；§6 intro/附录 B 更新 prototype 描述（16 界面、钉 v0.7.4、仅参考）。
- **`design/rejected-concepts.md` 新增**（design §1.5 承诺）：A 组原型否决概念（001:39/50、002:143/145、000:19/20、006:25/33 等，含引文）+ B 组本项目主动否决（webview、DSH 宿主复刻、legacy migration、全量 i18n、多租户、UUID、提示词授权、第二派活通道、robfig、backoff 替换、prototype 回迁、sleep/offline/cross-device 等）及理由/现状。
- **`design/prototype/` 可复现（work item 4）**：`replace github.com/egoist/mygo => /tmp/mygo` 删除，改 `require v0.7.4`；go.sum 补 v0.7.4 校验行。已验证（无网络）：`cd design/prototype && CGO_ENABLED=0 GOPROXY=off go build ./... && go vet ./...` 通过，`go run . -out /tmp/shots` 渲染 16 张 PNG。README 标注"视觉参考，勿回迁 internal/ui"。
- **部署脚手架（work item 5）**：新增 `packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist`（均标"需要 Wave-2 CLI/服务模式"，`Restart=on-failure`/`KeepAlive=false` 并解释 2/3 不该重启）、`scripts/watchdog-check.sh`（006:133-141 语义，CLI 缺失时**明确说未实现并 exit 0**，不假装监督）、`design/ops-runbook.md`（status 退出码表 0/1/2/3、JSON 形状、日志路径、backup/archive 命令、今天的手工备份、排障表；每节标现状）。
- **门禁文档（work item 6）**：`Makefile` 新增 `verify` 目标（= verify-fixes.sh），`check` 改为 `check: verify`（兼容），`release: verify package` 后再跑 release-check；`RELEASE.md`/README 以 verify-fixes 为唯一机械闸门。
- **审计索引**：`audit/AUDIT.md` 是 Lead 所有，未改（契约规则 5）。建议 Lead 在 §8 索引加一行 `audit/fixes/fix-docs-ci.md`。

## 改动文件（全部在本 scope）

- 改：`.github/workflows/ci.yml`、`.gitignore`、`scripts/release-check.sh`、`Makefile`、`README.md`、`CHANGELOG.md`、`RELEASE.md`、`RELEASE-STEPS.md`、`design/zeokeel-go-mygo-design.md`（§4.1/§6 intro/§6.5/§6.7/§7.1/§7.3/§8.1/§8.2/§8.3/§8.5/§13 原§11/附录 B/ADR-12）、`design/prototype/go.mod`、`design/prototype/go.sum`、`design/prototype/README.md`
- 增：`scripts/verify-fixes.sh`、`scripts/watchdog-check.sh`、`design/rejected-concepts.md`、`design/ops-runbook.md`、`packaging/systemd/zeokeel.service`、`packaging/launchd/com.zeokeel.agent.plist`、`audit/fixes/fix-docs-ci.md`
- 改（小）：`.gitignore` 增加 `/design/prototype/zeokeel-prototype`（prototype 的构建产物，避免误入库）
- 未改：`VERSION`（保持 `1.0.0-rc.1`，G 组语义要求不提前改 1.0.0）

## 新增/修改的测试与结果

- `sh -n scripts/verify-fixes.sh`、`sh -n scripts/release-check.sh`、`sh -n scripts/watchdog-check.sh`：PASS。
- `python3 yaml.safe_load(.github/workflows/ci.yml)`：PASS；`packaging/launchd/com.zeokeel.agent.plist` XML 解析：PASS。
- `ZEOKEEL_BIN=/nonexistent sh scripts/watchdog-check.sh` → 打印 "no zeokeel binary found" 且 exit 0（不假装监督）。
- `cd design/prototype && CGO_ENABLED=0 GOPROXY=off go build ./... && go vet ./...` → PASS；`go run . -out /tmp/shots` → 16 PNG。
- `sh scripts/verify-fixes.sh`：**闸门机制已验证**（5 个 step 各自独立计 PASS/FAIL，失败会继续跑完并汇总）。
  Wave 1 期间本机实测两次典型状态：
  1. 当树可编译时：前 4 步（零 cgo build / test / vet / check-nocgo）PASS，`-race` FAIL —— 失败为**其他 scope 的问题**（见下）。
  2. 当其他 agent 正在编辑 `internal/**` 时：build/test/vet/nocgo/race 连锁 FAIL，`scripts/package.sh` 也因同一编译错误失败
     （实测错误：`internal/sources/github` 的 `decodeItems`/`commentPageSize`/`RawComment redeclared`/`s.pollBranchCI undefined`），
     这是**并发 Wave 1 编辑的瞬时状态**，不是本 scope 的脚本问题。
  3. 第 4 次（树可编译）：build/vet/check-nocgo PASS；test 与 race FAIL ——
     `cmd/zeokeel` 的 `TestEndToEndFromChatToReply`、`internal/runtime` 的 `TestSettingsConflictAcrossWindows` /
     `TestLongRunCompactsAndOffloads` / `TestUnwritableStateDirectoryFailsTheTicket` / `TestModelFailureMarksTheTicketAndReplies`
     （后两个同时被 -race 抓到数据竞争）。全部在 `internal/**`/`cmd/**`，属 fix-runtime / fix-core 的并发修复范围。
- `sh scripts/release-check.sh`（在第 4 次尝试、树可编译时）：**12 通过 / 1 失败 / 7 跳过**。
  通过：VERSION（rc 语义提示）、无 TODO/FIXME、**三个产物**（linux tar.gz 11MB / macos zip 11MB / windows zip 12MB，
  文件名带 `$VERSION`）、6 个必需文档、验收测试存在。唯一失败是 `verify-fixes.sh`（跨 scope 的 test/race 红，见下）。
  也就是说：**新闸门不会假绿**——树不绿时它诚实报红；产物/文档部分已经真实可用。
- `sh scripts/package.sh`：**成功**产出 `dist/zeokeel-1.0.0-rc.1-{linux-x86_64.tar.gz,macos-arm64.zip,windows-x86_64.zip}`
  （dist/ 已在 .gitignore，未入库）；解包后 linux 可执行 33MB、darwin 31MB，均 < 40MB 预算。

## 行为变化与兼容性风险

1. **CI 从"结构性永远红"变为"真实红/绿"**：`-race` 现在真的运行（CGO=1 单步）。在跨 scope 的 race/测试问题修完前，CI `check` 会红——这是正确信号；若 Lead 需要临时绿，可把该 step 改 advisory，但不要删除（N-4 的意义就是让它可见）。
2. **release-check.sh 变慢**（现在跑完整 verify-fixes，含 race），但本地与 CI 一致，避免假绿。
3. **`make check` 语义变化**：从 `nocgo vet test` 变为 `verify`（多了 build 与 race）；`make test` 加了 `-skip 'Live'`（live 测试本来也会自跳过，行为等价）。
4. 文档大改：只改描述，不改产品行为；`VERSION` 仍是 rc.1。
5. `design/prototype` 现在可离线构建（钉版本），但它仍**不在**根 module 测试范围，CI 不跑它。

## 跨 scope 待办（file:line + 期望改动，按契约 §1 交给对应 owner）

1. **fix-runtime / fix-core：`-race` 暴露的真实数据竞争 + 失败用例**（Wave 1 期间 `scripts/verify-fixes.sh` 实测）：
   - `internal/runtime/disk_test.go:72-78`：测试 goroutine 里 `Engine.Report` 写 Ticket，测试主线程同时 `Ticket.LastReport()` 读 → `WARNING: DATA RACE`（`internal/engine/engine.go:850` vs `internal/domain/ticket.go:305`）。期望：测试通过 `Engine.Ticket()` 快照读取，或 runtime 提供同步读取 API；生产路径同样要确认无跨 goroutine 无锁读写。
   - `internal/runtime/faults_test.go:81`：同型竞争（`worker.go:307` 的 report vs 测试读）。
   - 普通模式也失败的：`TestSettingsConflictAcrossWindows`（settings 跨窗口 rev 语义）、`TestLongRunCompactsAndOffloads`、`cmd/zeokeel` 的 `TestEndToEndFromChatToReply` —— Wave 1 收尾时应由 owner 跑 `go test ./internal/runtime ./cmd/zeokeel -count=1` 复现。
   - 期望 owner：fix-runtime（测试/读取路径、settings rev）+ fix-core（`Engine.Report` 与 Ticket 快照语义）。
2. **fix-integrations：`-race` 下 `internal/im/dingtalk` 两个测试曾失败**：`TestHTTPRetriesOn429`（`im: http 429`）与 `TestHTTPRetryExhaustion`（`attempts = 1, want 3`）；第 4 次实测**已不复现**（可能已被 fix-integrations 修掉），保留为观察项：期望重试策略在 -race 下确定（注入 clock/backoff），不要删测试。
3. **fix-runtime（Wave 2）**：`cmd/zeokeel` 加 `-headless` 真服务模式与 `status` 子命令（0/1/2/3）；文件日志；`backup`/`archive`。本文档与 `design/ops-runbook.md` 的契约已按此写好，实现时逐条对齐；实现后把 `design/ops-runbook.md` 每节"现状"改为"已实现"。
4. **fix-core（Wave 2）**：schema v3 折进 `migrations[]` 且 `SchemaVersion=3`（AUDIT N-13）；`archive` 的最小实现。
5. **Lead**：`audit/AUDIT.md` §8 索引补 `audit/fixes/fix-docs-ci.md`；`audit/fixes/STATUS.md` 汇总 N-4 与本清单的关闭状态。

## 未完成 / 被阻塞

- **`-race` 全绿**被跨 scope 修复阻塞（上述 1/2）；本 scope 的文档/CI/脚本已就绪，闸门按设计会如实报红。
- **`make package` / release-check 的产物项**被同一个树红阻塞；Wave 1 结束、树绿后重跑应能通过（本 scope 改的是文档与打包脚本的版本名，不改 Go 代码）。
- **最终文档 pass**：Wave 2 后需要再校一次 README/CHANGELOG/design §13（Wave 1 只覆盖了已知高报；Wave 2 会新增/改变能力）。
- **Wave 1 期间仓库并发编辑**：本交接里的命令结果有时序性；Lead 在 Wave 1 收尾时应重跑
  `sh scripts/verify-fixes.sh && sh scripts/package.sh && sh scripts/release-check.sh` 作为权威结果。

## 给 Wave 2 之后的"最终文档 pass 检查清单"

- [ ] `scripts/verify-fixes.sh` 全绿（含 `-race`），CI `check` 绿；`-race` 仍是唯一 cgo 步骤。
- [ ] `README.md` "当前状态"表逐行对代码复核：IM 是否接线/删除；恢复范围；Attention 闭环；pause/stop/retry；status/logs/backup；keychain；计数诚实性。
- [ ] `RELEASE.md` F 组 12 项逐条关闭（fixed / documented-deviation / removed），与 `audit/fixes/STATUS.md` 对齐；只勾选有测试或命令输出的项。
- [ ] `CHANGELOG.md` 1.0.0 节：把 rc.1 的"Wave 1 校正"合并进最终描述；测试数/文件数用当时实测值。
- [ ] `design/zeokeel-go-mygo-design.md` §11 每个 ✅ 指向仍存在的 `file:line`（代码变动会失效）；§4.1 目标 vs 现状、§6.7、§7.3、§8.1-8.5、ADR-12 全部改为"已实现/明确不做"，删除 Wave 1 的"待修"标注。
- [ ] `design/ops-runbook.md` 去掉每个"需要 Wave-2 CLI (pending)"；`packaging/systemd|launchd` 与真实 `status` 语义对齐后再声称可用。
- [ ] `design/rejected-concepts.md` 新增概念（如 Wave 2 引入的）逐条登记；任何"推翻拒绝"必须有 ADR。
- [ ] `design/prototype/go.mod` 仍钉 `mygo v0.7.4`；`go build/vet` 与 `-out shots` 可复现；不把原型代码回迁。
- [ ] `RELEASE-STEPS.md`：把"rc.1 未完成"改为 1.0.0 发布说明；`.dmg`/签名/真机步骤保持未完成状态直到真的做过。
- [ ] `VERSION` 只在 F 组清空后改 `1.0.0`；`scripts/release-check.sh` 输出 0 失败。

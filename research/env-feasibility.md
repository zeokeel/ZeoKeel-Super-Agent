# Go + mygo 原生 UI 环境可行性实测报告

- 实测日期：2026-11-03（Asia/Shanghai）
- 机器：Ubuntu 24.04.5 LTS，96 核 / 32 GB，`/dev/dri/card0-2` + `renderD128/129`，**无 DISPLAY / WAYLAND_DISPLAY**
- Go：`go1.27.3 linux/amd64`（`/usr/local/go/bin/go`，不在 PATH）
- mygo 源码：`/tmp/mygo`，commit `982590e`（`go.mod` 要求 `go 1.27.3`；模块 `github.com/egoist/mygo`，本地版本 `v0.0.0-20261103011102-982590ef860f`，比 proxy 上的 `v0.7.4` 新）。**全程未改 `/tmp/mygo` 源码**，`git status` 干净。
- 工作目录：`/data/ZeoKeel-Super-Agent/spike/`；只读 research/ 与 _ref/。

## 1. 结论摘要（TL;DR）

1. **GOPROXY 可用**：`GOPROXY=https://goproxy.cn` 可用，且默认 `GOSUMDB=sum.golang.org` 也能通过镜像的 `/sumdb/` 代理校验，**不需要 `GOSUMDB=off`**。`goproxy.io` 也可用；`proxy.golang.org` 30s i/o timeout；`GOPROXY=direct` 能走（git 拉取）但很慢。
2. **原生 UI 能编译**：`CGO_ENABLED=0 go build` 成功，stripped 二进制 9.5 MB（未 strip 约 14.5 MB）。注意产物并非纯静态——purego 的 fakecgo 用 `cgo_import_dynamic` 动态链接 `libc.so.6`/`libdl.so.2`，所以目标机必须有 glibc。
3. **无头服务器能“跑起来”，但必须有虚拟 X**：裸跑直接失败 `mygo: cannot open display (is DISPLAY or WAYLAND_DISPLAY set?)`（exit 1）；套 `xvfb-run` 后窗口正常创建、正常绘制，**截图证明 UI 完整渲染**。GPU 不可用时 mygo 自动落 CPU 渲染器，首帧仅 5–10 ms；`MYGO_GPU=1` 走 Mesa llvmpipe 软件 OpenGL 也能画，但首帧约 1.5 s，无头环境没必要。
4. **headless view 单测跑通**：`ui.NewTester` 不建窗口、不需要 DISPLAY/GTK，`点击按钮 → 状态 +1 → 断言文本 "1"` 的用例 0.02 s 通过；`CGO_ENABLED=0` 下 mygo 的 `./ui` 全量 941 个测试 77 s 全绿。
5. **web 目标能编译**：`mygo build -platform web` 4.2 s 产出完整静态站点（`index.html` + `wasm_exec.js` + 11.5 MB wasm），本机 python http.server 验证 200 + `application/wasm`。**但本机没装浏览器**（只有 node），所以只验证到“编译 + 托管”，未做浏览器内运行验证。
6. **额外收获**：用 `plugins/automation`（unix socket JSON 协议）在 xvfb 下驱动真实窗口，`tree → act press → tree` 断言计数 `0→1`，并 capture 360×260 PNG 成功；mygo 仓库自带的 `internal/e2e` 真实窗口测试在 `xvfb-run + dbus-run-session` 下 6.1 s 通过。这条路线可直接用于 CI 的端到端测试。

## 2. 验证总表（命令 → 结果 → 结论）

| 验证项 | 命令（本机实测） | 结果 | 结论 |
| --- | --- | --- | --- |
| 默认 GOPROXY | `GOPROXY=https://proxy.golang.org,direct go mod download github.com/google/uuid@v1.6.0`（新 GOMODCACHE） | `dial tcp 108.177.98.141:443: i/o timeout`（30 s） | ❌ 不可用 |
| goproxy.cn | `GOPROXY=https://goproxy.cn go mod download github.com/google/uuid@v1.6.0` | 0.13 s 成功，日志显示 `goproxy.cn/sumdb/sum.golang.org/supported: 200` | ✅ 首选，且 sumdb 默认可用 |
| goproxy.io | `GOPROXY=https://goproxy.io go mod download github.com/google/uuid@v1.6.0` | 3.1 s 成功 | ✅ 备用 |
| direct | `GOPROXY=direct GOSUMDB=off go mod download github.com/google/uuid@v1.6.0` | 71.6 s 成功 | ⚠️ 可用但慢，仅作兜底 |
| 拉 mygo 模块 | `GOPROXY=https://goproxy.cn go get github.com/egoist/mygo@latest` | 3.4 s，拿到 `v0.7.4` + 依赖 | ✅ 模块下载链路通 |
| 原生 UI 编译 CGO=0 | `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" .`（counter 应用，replace→/tmp/mygo） | 成功，9,924,871 B；默认未 strip 14,533,972 B | ✅ cgo-free 可编译 |
| 原生 UI 编译 CGO=1 | `CGO_ENABLED=1 go build` | 成功，14,146,312 B | ✅ 但没必要 |
| 链接方式 | `file` / `readelf -d` | `dynamically linked`，NEEDED：`libdl.so.2 libpthread.so.0 libc.so.6` | ⚠️ 需 glibc，非静态二进制 |
| 无显示器直接跑 | `./hello-native`（DISPLAY 未设） | `mygo: cannot open display (is DISPLAY or WAYLAND_DISPLAY set?)`，exit 1 | ❌ 真窗口不能裸跑 |
| xvfb 直接跑 | `xvfb-run -a -s "-screen 0 900x700x24" ./hello-native` | 窗口打开，`frame 1 took 5.5 ms ... drawn in memory` | ✅ 可行 |
| xvfb 截图 | `import -window root shots/default.png`（应用启动 5 s 后） | 900×700 PNG，1 个 360×260 白底窗口，计数器 UI 完整 | ✅ 画面真实可见 |
| 强制 CPU 渲染 | `MYGO_GPU=0 MYGO_FRAME_STATS=all ./hello-native` | 首帧 9.0 ms，`drawn in memory`，截图与默认一致 | ✅ 无头首选 |
| 强制软件 GL | `MYGO_GPU=1 LIBGL_ALWAYS_SOFTWARE=1 GALLIUM_DRIVER=llvmpipe` | 首帧 1480.8 ms，`drawn on the GPU ...`（llvmpipe），截图同样正确 | ⚠️ 能画但慢，无头不必 |
| headless view 单测 | `CGO_ENABLED=0 go test -v ./...`（spike/hello，含 click→状态→断言） | `--- PASS: TestClickChangesState (0.02s)`，texts 从 `"0"` 变 `"1"` | ✅ CI 可直接跑 |
| mygo `./ui` 全量测试 | `CGO_ENABLED=0 go test ./ui -count=1 -timeout 600s`（DISPLAY 未设） | `ok github.com/egoist/mygo/ui 76.971s`，941 个 Test* 全绿 | ✅ 无需显示 |
| 仓库真实窗口 e2e | `MYGO_E2E=1 xvfb-run -a dbus-run-session -- go test -run TestContentWindow ./internal/e2e -count=1` | `ok ... 6.114s` | ✅ 官方推荐姿势可复现 |
| e2e 无 xvfb | 同上但去掉 `xvfb-run` | `mygo: cannot open display ...`，FAIL 0.028s | ❌ 必须虚拟显示 |
| e2e 未开开关 | 不加 `MYGO_E2E=1` | `skipping e2e tests; set MYGO_E2E=1 to run them in a desktop session` | ℹ️ 无环境依赖的 job 安全跳过 |
| automation 驱动真窗口 | xvfb + `MYGO_AUTOMATION=/tmp/x.sock` + `e2e_client.py` | `windows/tree/act/capture` 均成功，点击 increment 后 tree 文本 `0→1`，PNG 360×260 | ✅ 可做 CI 端到端 |
| go vet | `CGO_ENABLED=0 go vet . ./ui`；`go vet ./cmd/mygo` | exit 0，无输出 | ✅ |
| mygo vet（UI 生命周期检查） | `go run github.com/egoist/mygo/cmd/mygo vet .`（webapp） | exit 0 | ✅ |
| web 目标编译 | `mygo build -platform web`（webapp） | 4.2 s；`dist/web`：wasm 11,506,805 B、index.html 2,561 B、wasm_exec.js 16,992 B | ✅ 无需 bun/前端 |
| web 静态托管 | `python3 -m http.server` + curl | `/` 200 text/html；wasm 200 `application/wasm`，11,506,805 B | ✅ 可直接部署静态站 |
| 浏览器内运行 | 无 chromium/firefox（仅 node/python3） | 未验证 | ⚠️ 本机缺浏览器，编译层已通 |
| arm64 交叉编译测试 | `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./internal/e2e` | 13.8 s 产出 27 MB arm64 测试二进制 | ✅ 可交叉编译，容器需 glibc |
| mygo doctor | `go run ./cmd/mygo doctor` | go ✓、gtk ✓、webkit ✓、bun ✗（可选）、makensis/osslsigncode ✗（可选） | ✅ 本机环境足够 |

## 3. 可用的 GOPROXY（可直接抄）

```sh
# 首选（本机实测最快，sumdb 校验默认即可工作）
export PATH=$PATH:/usr/local/go/bin
export GOPROXY=https://goproxy.cn,direct
# 备用镜像（goproxy.cn 缺包时多挂一个）
# export GOPROXY=https://goproxy.cn,https://goproxy.io,direct
```

- 本机 `go env GOPROXY` 原值是 `https://proxy.golang.org,direct` → 会 30 s 超时，必须改。
- 不建议 `GOSUMDB=off`：实测 `goproxy.cn` 默认就代理 `sum.golang.org` 的 tile 校验，保留供应链校验。
- `direct`（走 git）能通但 71 s/小模块，只能作为镜像缺包时的兜底；`curl https://github.com` 返回 000 但 `git`/`go get direct` 实际可通。
- 已有模块缓存：`/root/go/pkg/mod`（默认 GOMODCACHE）。

## 4. 模块 / 编译方式（推荐 spike/hello 做法）

最小项目（`spike/hello/go.mod`）：

```go
module example.com/hello

go 1.27.3

require github.com/egoist/mygo v0.0.0

replace github.com/egoist/mygo => /tmp/mygo
```

- `go mod tidy` + `CGO_ENABLED=0 go build` 即可；无需 cgo、无需 GTK 头文件（GTK 是运行时 dlopen 的）。
- 编译时间（依赖已下载）：单应用约 9–12 s；`-trimpath -ldflags="-s -w"` 后 9.5 MB。
- **部署注意**：由于 purego fakecgo 的 `cgo_import_dynamic`，`CGO_ENABLED=0` 产物仍动态链接 glibc，不能进 `scratch`/Alpine(musl) 容器；Ubuntu 24.04/Debian 容器天然满足。
- 运行依赖：`libgtk-3.so.0`、`libepoxy.so.0`、`libgdk-3.so.0`、`libglib-2.0.so.0`、`libX11.so.6`、`libGL.so.1`、`libEGL.so.1`（Mesa）；webview 才需要 `libwebkit2gtk-4.1`。

## 5. 无头服务器上“真窗口”到底能不能跑

**结论：裸机不行，套 xvfb 就行；这是“CI 可跑”而非“无头进程内可跑”。**

- 直接运行：GTK 初始化即失败，`exit 1`，stderr：`mygo: cannot open display (is DISPLAY or WAYLAND_DISPLAY set?)`。这不是 OpenGL 问题，是根本没有 X/Wayland server；`/dev/dri` 在这个阶段救不了。
- `xvfb-run -a`（本机已预装 xvfb/xauth/dbus-x11，**无需 apt install**）后：窗口创建、事件循环、绘制全部正常。`MYGO_FRAME_STATS=all` 显示首帧 5–10 ms；渲染器日志：`native UI draws without the GPU: OpenGL draws on the CPU (llvmpipe (LLVM 20.1.2, 256 bits))`，随后帧走内存渲染器（`drawn in memory`）。
- `MYGO_GPU=1` 会强制走 GtkGLArea + Mesa llvmpipe，能正确渲染（截图一致）但首帧 1.48 s，软件 GL 没意义；无头/CI 保持默认或显式 `MYGO_GPU=0`。
- `LIBGL_ALWAYS_SOFTWARE=1` + `GALLIUM_DRIVER=llvmpipe` 组合同上，非必需（有 Mesa 软件驱动即可）。
- 唯一噪音：`dbind-WARNING ... org.a11y.Bus`（AT-SPI 辅助功能总线缺失），不影响运行；CI 里可用 `dbus-run-session` 包住或忽略。

### 桌面要求（开发者机器 / 无头 CI 依赖）

- 桌面 Linux（真机跑）：GTK 3 + Mesa（`libgtk-3-0t64`(24.04)/`libgtk-3-0`、`libepoxy0`、`libgl1`、`libgl1-mesa-dri`、`libglx-mesa0`、`libegl-mesa0`）、X11 或 Wayland 桌面会话、字体（`fonts-dejavu-core`）；用 webview/前端时加 `libwebkit2gtk-4.1-0`（或 4.0）；托盘加 `libayatana-appindicator3-1`。mygo 官方 `docs/README.md` 写明：**原生 UI 只需 GTK 3**，webview 才需要 WebKitGTK 4.1。
- 无头 CI 额外：`xvfb xauth dbus-x11`（本机已全有；mygo 自己的 `.github/workflows/ci.yml` 用的就是这一组 +
  `libgl1-mesa-dri libglx-mesa0 libegl-mesa0`，并以 `xvfb-run -a dbus-run-session -- go test ./internal/e2e` 运行）。
- macOS/Windows 开发者桌面：原生后端分别是 Metal / D3D11，无需额外包；这也是最终用户桌面目标。

## 6. headless view 单测（CI 主力，已完成“点击→状态→断言”）

`ui.NewTester` 在内存里渲染、按文本/Label 查找元素、模拟点击/键盘，不建窗口、不加载 GTK、不需要 DISPLAY。
实测最小用例（`spike/hello/spike_click_test.go`，基于 counter 示例的 `view`）：

```go
func TestClickChangesState(t *testing.T) {
	s := &counter{}
	tt := ui.NewTester(s.view, 360, 260)   // 无窗口
	if !tt.HasText("0") { t.Fatalf("texts: %q", tt.Texts()) }
	if err := tt.Click("+"); err != nil { t.Fatal(err) }   // 按可见文本点
	if s.n != 1 || !tt.HasText("1") { t.Errorf("n=%d texts=%q", s.n, tt.Texts()) }
	if err := tt.Click("Reset"); err != nil { t.Fatal(err) }
	tt.Key(0, ui.KeyUp)                    // 键盘快捷键也能测
}
```

实测输出（`env -u DISPLAY -u WAYLAND_DISPLAY CGO_ENABLED=0 go test -v`）：

```text
=== RUN   TestClickChangesState
    spike_click_test.go:40: final texts: ["1" "Decrement" "−" "Reset" "Increment" "+" "↑ and ↓ count too"]
--- PASS: TestClickChangesState (0.02s)
PASS
ok  	example.com/hello	0.895s
```

补充：`./ui` 全量 `CGO_ENABLED=0 go test ./ui` 77 s 通过（941 个 Test*）；`go vet . ./ui`、`cmd/mygo` 的 vet、`mygo vet`（UI 生命周期检查）均为 exit 0。真实窗口/输入法/菜单/截图等留给 xvfb 下的 e2e。

## 7. 真实窗口 E2E（automation socket，已验证）

`plugins/automation` 提供 newline-delimited JSON over unix socket：

```text
{"id":1,"method":"windows"} → 1 个 "Counter" 窗口，kind=content，360×260
{"id":2,"method":"tree","params":{"window":1}} → text "0"、button key=increment/decrement/reset
{"id":3,"method":"act","params":{"window":1,"key":"increment","action":"press"}}
{"id":4,"method":"tree"} → text "1"     ← 状态确实变了
{"id":5,"method":"capture"} → 360×260 PNG（shots/e2e.png，图内大数字为 1）
```

构建：`CGO_ENABLED=0 go build -tags e2e`（`e2e.go` 里 `import _ ".../plugins/automation"`，用 build tag 隔离发布版），运行：`MYGO_AUTOMATION=/tmp/x.sock xvfb-run ... ./hello-e2e`。适合做 nightly E2E / 关键交互冒烟，不适合每次 PR 全量跑。

## 8. web 目标（无头部署的备选路线）

- `mygo build -platform web`（项目根需 `mygo.json`；本机 webapp 用最小配置 `{name,slug,identifier,version,main}`）→ 4.2 s 产出 `dist/web/`：
  - `counter.<hash>.wasm` 11,506,805 B（gzip 约 3.1 MB）
  - `index.html` 2,561 B（含 loading spinner、错误提示）
  - `wasm_exec.js` 16,992 B
- 纯 `GOOS=js GOARCH=wasm go build` 也可（12,079,446 B），但官方 CLI 会生成带 hash 文件名/加载页/icon 的完整站点。
- 托管实测：`python3 -m http.server` → `/` 200 text/html、wasm 200 `application/wasm`（部署 Nginx 时确保 `.wasm` MIME）。
- 限制：本机无浏览器，未验证浏览器内 WebGL2/2D 回退的实际渲染；部署侧无 GPU 服务器也不影响（浏览器客户端渲染）。适合“无头服务器只做静态托管”的部署形态。

## 9. CI 策略建议

可以在**本机（无桌面、无 X）**直接跑：

1. `CGO_ENABLED=0 go build`（linux/amd64 及交叉编译）；`go vet ./...`、`mygo vet`；
2. `CGO_ENABLED=0 go test ./ui/...` 等 headless view 单测（941 个测试 77 s，无 DISPLAY）；
3. `GOOS=js GOARCH=wasm go build` / `mygo build -platform web`，产物上传 artifact；
4. `GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go test -c ./internal/e2e` 交叉编译（13.8 s，27 MB）。

**必须**加虚拟显示 / 容器才能跑：

- 真实窗口渲染、截图、automation E2E、输入法/菜单/剪贴板/焦点等：`xvfb-run -a dbus-run-session -- ...`，镜像需 Ubuntu 24.04 + `libgtk-3-0t64 libepoxy0 libgl1 libgl1-mesa-dri libglx-mesa0 libegl-mesa0 xvfb xauth dbus-x11`（可选 `libwebkit2gtk-4.1-0`、`libayatana-appindicator3-1`）；`MYGO_GPU=1` 的软件 GL 路径很慢，PR 默认只跑 CPU 渲染，GL 单独 job。
- 打包 macOS/Windows 安装包、开发者桌面交互体验：必须 macOS/Windows runner 或对应容器，本机（Linux 无头）做不了。

**分层建议**：PR job = headless view 单测 + build + vet + web 构建；nightly/merge job = xvfb + `MYGO_E2E=1 go test ./internal/e2e`（+ 1 个 `TestContentWindow` GL 冒烟）；发布 job = 各平台 packaging。容器务必带 glibc（purego 动态链接），不要用 Alpine 基础镜像。

## 10. 实验产物（均在 `/data/ZeoKeel-Super-Agent/spike/`）

| 路径 | 内容 |
| --- | --- |
| `modtest/` | GOPROXY 最小验证 module（`go get github.com/egoist/mygo@latest`） |
| `hello/` | 最小 counter 应用 + `replace => /tmp/mygo`：`main.go`、`main_test.go`（官方示例测试）、`spike_click_test.go`（点击→状态→断言）、`e2e.go`（build tag e2e 引入 automation）、`cmd_build.sh`、`go.mod/go.sum` |
| `hello-native` | CGO_ENABLED=0 stripped 二进制（9.5 MB） |
| `hello-native-cgo` / `hello-static` | CGO=1 / `-buildmode=exe` 对照产物 |
| `counter-native` | 直接编译 `/tmp/mygo/examples/counter-native` 的产物（13.9 MB，未 strip） |
| `hello-e2e` | 带 automation 插件的 E2E 构建（9.7 MB） |
| `run_xvfb.sh` + `shots/` | xvfb 启动脚本；`default.png`/`cpu.png`/`gl.png` 三种渲染模式截图（UI 完整），`app.log` 帧统计 |
| `run_e2e.sh`, `e2e_client.py`, `shots/e2e.png`, `shots/e2e-app.log` | 真实窗口 automation 驱动 + 点击后 PNG（显示 "1"） |
| `webapp/` + `webapp/dist/web/` | `mygo build -platform web` 项目与产物（wasm 11.5 MB、index.html、wasm_exec.js） |
| `app.wasm` | 纯 `GOOS=js GOARCH=wasm go build` 产物（12,079,446 B） |
| `e2e-linux-arm64.test` | 交叉编译的 arm64 e2e 测试二进制（27 MB） |

## 11. 未验证 / 风险

- 浏览器内运行（WebGL2/2D canvas）未实测：本机无浏览器；如需验证，装 chromium + 无头截图，或交给有浏览器的环境。
- 本机没有真实 GPU 渲染（Xvfb 下只有 llvmpipe 软件 GL）；真实 AMD/Intel GPU 下 OpenGL 路径未测，但 CPU 渲染器已证明可用，CI 不依赖 GPU。
- 未跑 mygo 全量 `go test ./...`（任务要求只跑关心包）：仅 `./ui` 全量 + `internal/e2e` 子集 + vet，结论对“原生 UI 可行性”已充分。
- 无头服务器若既不能安装 xvfb 又要跑真窗口，则**不可行**；此时只能走 headless view 单测 + web 目标两条无显示路线。

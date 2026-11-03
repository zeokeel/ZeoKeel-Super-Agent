# 零 cgo 硬约束验证报告（Go + mygo + eino + openai-go + modernc/sqlite + MCP SDK）

- 实测日期：2026-11-03（Asia/Shanghai）；机器：Ubuntu 24.04.5，96 核 / 32 GB；Go 1.27.3 linux/amd64
- GOPROXY 实测可用：`https://goproxy.cn,direct`（模块下载 7.7 s 完成全部依赖）
- 实验目录：`/data/ZeoKeel-Super-Agent/spike-nocgo/`（独立 module，未改动 `/tmp/mygo` 与已有 research 文件）
- 探针程序：一个 `CGO_ENABLED=0` 二进制，同时调用 5 类依赖，真实执行而非空导入（mygo 窗口 + `ui.NewTester`、eino `adk` 两轮 ReAct、openai-go/v3 直连同一 mock、modernc sqlite 建库读写、MCP go-sdk 内存 server/client 真实 CallTool）。

## 0. 结论摘要

1. **组合可以纯 `CGO_ENABLED=0` 编译并实际跑通**：headless 5/5 检查通过，`xvfb-run` 下 6/6（含真实 mygo 窗口）通过；stripped 二进制 **48,267,527 B**（约 46 MiB）。
2. **传递依赖零 cgo**：`CGO_ENABLED=0 go list -deps ... .CgoFiles` 输出为**空**；用 `CGO_ENABLED=1` 反查只出现标准库的可选 cgo 路径（net/os/user/runtime/cgo）和 purego 的 cgo 回退文件，没有第三方依赖**必须** cgo。6 个选定模块自身真实 `import "C"` 文件数为 0。
3. **交叉编译 6 个目标全部成功**：linux/darwin/windows × amd64/arm64，零失败（体积 45–50 MB）。
4. **零 cgo ≠ 静态二进制**：`ldd` 显示 `libdl.so.2 / libpthread.so.0 / libc.so.6`，原因是 mygo 依赖的 purego fakecgo 用 `//go:cgo_import_dynamic` 动态导入 libc；Alpine/scratch 均无法运行（exit 255, `no such file or directory`），**debian:12-slim 可运行**。
5. **CI 守卫可行且必要**：`CGO_ENABLED=0 go build` 单独会漏掉带 `!cgo` 桩的 cgo 依赖（已用 mattn/go-sqlite3 复现），守卫必须加 `CGO_ENABLED=1` 的第三方 cgo 审计；附可直接抄的脚本与负例验证。

## 1. 验证总表（检查项 | 命令 | 结果 | 结论）

| 检查项 | 命令 | 结果 | 结论 |
| --- | --- | --- | --- |
| 零 cgo 编译 | `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o nocgo-probe .` | 成功，48,267,527 B；首次 32 s，增量 5.7 s | ✅ |
| headless 实跑 | `env -u DISPLAY -u WAYLAND_DISPLAY CGO_ENABLED=0 ./nocgo-probe` | 5 checks, 0 failed；`go run .` 5.6 s 同样通过 | ✅ |
| xvfb 实跑（含窗口） | `xvfb-run -a env CGO_ENABLED=0 ./nocgo-probe` | 6 checks, 0 failed（`ok mygo native window`） | ✅ |
| 传递依赖 cgo 审计 | `CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}...{{end}}' ./...` | 输出 0 行 | ✅ 构建图内无 cgo |
| cgo 反查归因 | `CGO_ENABLED=1` 同命令 + `{{.Module}}` | 4 个包：stdlib 可选 cgo + purego 回退 | ✅ 无必需 cgo |
| 逐模块源码检查 | 各模块目录 `import "C"` 统计 | mygo/eino/eino-ext×2/openai-go/mcp/sqlite 全部 0 | ✅ |
| modernc sqlite | `go list -f 'CgoFiles/CFiles/HFiles' modernc.org/sqlite` | 全为 `[]`；`.c` 仅在 testdata/vfs 未编译；`sqlite_version()`=3.54.1 | ✅ 纯 Go |
| Go 版本要求 | 各模块 `go list -m -f ...GoVersion` | mygo 1.27.3 / eino 1.18 / eino-ext 1.18、1.23 / openai-go 1.26 / mcp 1.25 / sqlite 1.26 | ✅ 1.27.3 满足 |
| 版本冲突 / replace | `go mod tidy` 前后 diff、`go mod edit -json` | tidy 稳定、`go mod verify` 通过；唯一 replace 是 mygo→/tmp/mygo（本地新源码，非冲突） | ✅ |
| 模块规模 | `go list -m all \| wc -l`、`go mod graph \| wc -l` | 151 个模块 / 541 行依赖图 / go.sum 257 行 / 二进制内嵌 55 模块 | ℹ️ |
| 交叉编译 | 6 个 `GOOS/GOARCH` + `CGO_ENABLED=0 go build` | 6/6 成功，37–41 s，无错误 | ✅ |
| 动态链接 | `ldd nocgo-probe`、`readelf -d` | NEEDED：libdl.so.2、libpthread.so.0、libc.so.6 | ⚠️ 非静态 |
| Alpine 运行 | `docker run ... alpine:3.20 /nocgo-probe` | exit 255，`exec /nocgo-probe: no such file or directory` | ❌ 不可用 |
| scratch 运行 | `FROM scratch` + COPY 后运行 | exit 255，同上（缺 glibc loader） | ❌ 不可用 |
| debian slim 运行 | `docker run ... debian:12-slim /nocgo-probe` | headless 5 checks 全过 | ✅ 可用基础镜像 |
| CI 守卫（正例） | `./scripts/check-nocgo.sh` | PASS（build + 双重审计 + vet） | ✅ |
| CI 守卫（负例） | 引入 `mattn/go-sqlite3` 后跑审计 | `CGO_ENABLED=0 go build` 仍通过，但审计抓住 `github.com/mattn/go-sqlite3` | ✅ 守卫有效 |
| 容器内 GUI | debian-slim + apt 安装 GTK/xvfb | apt 在本网络环境长时间无进展，已终止 | ⚠️ 未验证（宿主 xvfb 已证明） |

## 2. 版本与依赖

| 模块 | 版本 | go.mod go 要求 | 模块内真实 cgo 文件 |
| --- | --- | --- | --- |
| github.com/egoist/mygo | v0.0.0（replace→/tmp/mygo，commit 982590e） | **1.27.3** | 0（`plugins/sqlite/native/sqlite.c` 是 zig 编译、purego dlopen 的插件，非 cgo，未被引入） |
| github.com/cloudwego/eino | v0.10.0-alpha.35 | 1.18 | 0 |
| github.com/cloudwego/eino-ext/components/model/openai | v0.1.13 | 1.18 | 0 |
| github.com/cloudwego/eino-ext/components/tool/mcp | v0.0.9 | 1.23 | 0 |
| github.com/openai/openai-go/v3 | v3.91.1 | 1.26 | 0 |
| github.com/modelcontextprotocol/go-sdk | v1.8.0 | 1.25 | 0 |
| modernc.org/sqlite（+libc） | v1.61.1（libc v1.79.0） | 1.26 | 0 |

- 唯一 replace 是 `github.com/egoist/mygo => /tmp/mygo`（本地源码比 proxy 上的 v0.7.4 新；生产可改为固定 commit/发布版，不构成版本冲突）。`go mod tidy` 后 go.mod/go.sum 均无变化，`go mod verify` = `all modules verified`；`go` 指令必须写 `1.27.3`（由 mygo 决定），工具链低于此版本无法构建。

## 3. 依赖 cgo 审计（原始命令与原始输出）

（1）需求中给出的命令，`CGO_ENABLED=0`：

```text
$ CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}' ./... | grep -v '^$'
（无输出，0 行）
```

等价于：**进入最终二进制的每个包在 CGO=0 下都没有 cgo 文件**。

（2）`CGO_ENABLED=1` 反查（看“如果允许 cgo 会用到哪些 cgo 文件”，并归因到模块）：

```text
$ CGO_ENABLED=1 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}{{with .Module}}|{{.Path}}{{end}}{{end}}' ./...
runtime/cgo
net
github.com/ebitengine/purego/internal/cgo|github.com/ebitengine/purego
os/user
```

- `runtime/cgo`、`net`、`os/user` 是**标准库的可选 cgo 路径**，仅在 CGO=1 时编译，CGO=0 时走纯 Go 实现；`purego/internal/cgo` 是 purego 的 cgo 回退（build tag `cgo`），CGO=0 时被排除、改用纯 Go 的 fakecgo。**没有任何第三方业务依赖出现在这里**。

（3）逐模块源码审计（`import "C"` 精确匹配，排除注释）：

```text
$ for m in <各模块目录>; do grep -rlE '^\s*import "C"|^\s*"C"\s*$' $m --include='*.go'; done
mygo:            real-cgo-files=[]
eino:            real-cgo-files=[]
eino-ext openai: real-cgo-files=[]
eino-ext mcp:    real-cgo-files=[]
openai-go/v3:    real-cgo-files=[]
mcp go-sdk:      real-cgo-files=[]
modernc sqlite:  real-cgo-files=[]
purego(间接):     [internal/cgo/dlfcn_cgo_unix.go internal/cgo/syscall_cgo_unix.go ...]（均 build-tag cgo）
```

（4）`modernc.org/sqlite` 纯 Go 的证据：

```text
$ CGO_ENABLED=1 go list -f 'CgoFiles={{.CgoFiles}} CFiles={{.CFiles}} HFiles={{.HFiles}}' modernc.org/sqlite
CgoFiles=[] CFiles=[] HFiles=[]
$ ldd nocgo-probe | grep -i sqlite
（无输出——没有链接 libsqlite3）
```

模块里 29 个 `.c/.h` 全部位于 `testdata/tcl` 与 `vfs/c`，不参与编译；实测 `select sqlite_version()` 返回 3.54.1，建表/插入/查询在 CGO=0 二进制内正常。**“modernc.org/sqlite 需要 cgo”是误传。**

（5）反例说明守卫为何不能只看 build：`mattn/go-sqlite3` 带 `static_mock.go`（`//go:build !cgo`），所以 `CGO_ENABLED=0 go build` **会通过**，但该驱动实际不可用；只有 `CGO_ENABLED=1` 的 `CgoFiles` 审计能抓住它（实测输出 `github.com/mattn/go-sqlite3|github.com/mattn/go-sqlite3`）。

## 4. 实测运行结果（摘录）

```text
nocgo probe: go=go1.27.3 linux/amd64 CGO_ENABLED=0
     sqlite: modernc 3.54.1, round-trip k=hello v=world            -> ok
     openai-go: "Hello! This is a streamed answer from the mock..." -> ok
     adk: 3 events, 2 assistant msgs, 2 model rounds, 1 tool call,
          final="TOOL_RESULT_SEEN[{...HELLO FROM MOCK...}]"        -> ok
     mcp: in-memory client/server CallTool -> "HELLO MCP"          -> ok
     tester: click/click/KeyUp -> n=3                              -> ok
skip/ok mygo native window  (无 DISPLAY 时 skip；xvfb-run 时创建真窗口 800ms 后退出)
summary: 6 checks, 0 failed / ALL CGO-FREE CHECKS PASSED
```

## 5. 交叉编译矩阵（`CGO_ENABLED=0`，全部成功）

| 目标 | 体积（字节） | 结果 |
| --- | --- | --- |
| linux/amd64 | 48,267,527 | ✅ 且已实际运行 |
| linux/arm64 | 45,023,495 | ✅ 仅编译 |
| darwin/amd64 | 49,638,208 | ✅ 仅编译 |
| darwin/arm64 | 46,628,098 | ✅ 仅编译 |
| windows/amd64 | 49,609,728 | ✅ 仅编译 |
| windows/arm64 | 45,656,576 | ✅ 仅编译 |

只有 linux/amd64 做了运行验证；其余目标为编译验证（46–50 MB，均为 CGO=0）。

## 6. 零 cgo ≠ 静态：分发建议

```text
$ ldd nocgo-probe
libdl.so.2 => /usr/lib/x86_64-linux-gnu/libdl.so.2
libpthread.so.0 => /usr/lib/x86_64-linux-gnu/libpthread.so.0
libc.so.6 => /usr/lib/x86_64-linux-gnu/libc.so.6
$ docker run --rm -v $PWD/nocgo-probe:/nocgo-probe:ro alpine:3.20 /nocgo-probe
exec /nocgo-probe: no such file or directory   (exit 255)
```

- 原因不是 cgo，而是 mygo→purego 的 fakecgo 在 CGO=0 下用 `//go:cgo_import_dynamic` 绑定 `libc.so.6`/`libdl.so.2` 以调用 `dlopen`；purego 无“关闭该动态导入”的构建标签，因此 **Linux 上做不到纯静态**（Windows/macOS 本来就是动态链接系统库，不受影响）。结论：**不能进 scratch/Alpine(musl)/distroless**，必须 glibc 基础镜像；实测 **debian:12-slim** 直接运行 headless 探针 5/5 通过。
- 建议基础镜像：`debian:12-slim` 或 `ubuntu:24.04`。运行时按用途加包：
  - 有窗口/桌面：`libgtk-3-0 libepoxy0 libgl1 libgl1-mesa-dri libglx-mesa0 libegl-mesa0 fonts-dejavu-core`（Ubuntu 24.04 为 `libgtk-3-0t64`）；无头 CI + 真窗口测试再加 `xvfb xauth dbus-x11`（与 mygo 仓库 CI 包列表一致）；不用 webview 时不需要 WebKitGTK，用 `embed-webview`/web 前端才需要 `libwebkit2gtk-4.1-0`。
- 体积提醒：46 MiB 主要来自 eino/sonic、sqlite、openai-go；如分发紧张，可裁剪未用的 eino-ext 组件与 MCP 适配器，但当前组合已是“按需引入”的合理体积。

## 7. 可直接抄的 CI 零 cgo 守卫

`spike-nocgo/scripts/check-nocgo.sh`（已在本次环境跑通；`verify-guard-catches-cgo.sh` 为负例验证）：

```sh
#!/bin/sh
set -eu
# 硬约束：任何环境下 cgo 都不允许开启
export CGO_ENABLED=0
[ "$(go env CGO_ENABLED)" = "0" ] || { echo "CGO_ENABLED != 0" >&2; exit 1; }

pkgs="${1:-./...}"

echo "== 1. CGO_ENABLED=0 go build"
go build $pkgs

echo "== 2. 构建图内不得出现 cgo 文件"
cgo=$(CGO_ENABLED=0 go list -deps -f '{{if .CgoFiles}}{{.ImportPath}}: {{.CgoFiles}}{{end}}' $pkgs \
      | grep -v '^$' || true)
[ -z "$cgo" ] || { echo "FAIL: cgo files in build graph:" >&2; echo "$cgo" >&2; exit 1; }

echo "== 3. 第三方依赖不得含 cgo 文件（含 !cgo 桩的依赖也能抓住）"
third=$(CGO_ENABLED=1 go list -deps \
      -f '{{if .CgoFiles}}{{.ImportPath}}{{with .Module}}|{{.Path}}{{end}}{{end}}' $pkgs \
      | grep '|' | grep -v '|github.com/ebitengine/purego$' || true)
[ -z "$third" ] || { echo "FAIL: third-party cgo packages:" >&2; echo "$third" >&2; exit 1; }

echo "== 4. go vet"
go vet $pkgs
echo "check-nocgo: PASS (zero cgo)"
```

要点：
1. `CGO_ENABLED=0` 写死并断言（防止 CI 环境变量覆盖）；
2. `go build ./...` 保证“无纯 Go 回退的 cgo 依赖”直接失败；
3. 第 2 步断言最终构建图无 cgo 文件（本需求要求的命令）；
4. 第 3 步的 `CGO_ENABLED=1` 审计是必要补充：能抓住带 `!cgo` 桩、CGO=0 下“能编译但不能用”的依赖（mattn/go-sqlite3 实测）；`purego` 的 cgo 回退加入白名单（CGO=0 不会被编译）。建议 CI 再加一步交叉编译矩阵（`linux/arm64, darwin/amd64, darwin/arm64, windows/amd64, windows/arm64`）作回归。

## 8. 实验产物（`/data/ZeoKeel-Super-Agent/spike-nocgo/`）

| 路径 | 内容 |
| --- | --- |
| `main.go` | 探针：5 类依赖真实调用 + mygo 窗口/`ui.NewTester` |
| `go.mod` / `go.sum` | module `example.com/nocgo`，go 1.27.3，唯一 replace mygo→/tmp/mygo |
| `internal/mockllm/mockllm.go` | 从 spike-sdk 原样复制的 OpenAI 兼容 mock（跨模块 internal 不能直接 import） |
| `nocgo-probe` | CGO=0 stripped 二进制，48,267,527 B |
| `scripts/check-nocgo.sh` | 零 cgo 守卫（可直接用于 CI） |
| `scripts/verify-guard-catches-cgo.sh` | 负例：证明守卫能抓住 cgo 依赖 |
| `audit/` 下各 txt | `cgo0-deps.txt`（空）、`cgo1-deps.txt`/`cgo1-attribution.txt`、`per-module-source-grep.txt`、`ldd.txt`、`counts.txt`、`go-versions.txt`、`cross-builds.txt`、两次 `probe-run-*.txt`：全部为原始命令输出 |
| `/tmp/nocgo-builds/`（工作区外） | 6 个交叉编译产物（临时） |

## 9. 未验证 / 风险

- 其他 5 个交叉编译目标未做运行验证（仅编译）；Windows/macOS 上 mygo 的 purego 后端需实际机器冒烟。容器内 GUI（debian-slim + apt 装 GTK/xvfb）因本网络 apt 长时间无进展未完成；宿主 `xvfb-run` 已证明本组依赖可同进程跑真窗口，CI 容器按 mygo 官方包列表安装即可。
- 日志噪音：`sonic/ast only supports go1.17~1.26 ... fallback to encoding/json`（bytedance/sonic 对 Go 1.27 的版本判断，自动回退纯 Go 实现，非 cgo、不影响功能，eino 路径性能可能略降，建议跟踪升级）。

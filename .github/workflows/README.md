# CI

四个 job，只有第一个是合并闸门：

| job | 作用 | 失败是否阻断 |
| --- | --- | --- |
| `check` | 零 cgo 守卫 + build + vet + `go test -race` | ✅ 阻断 |
| `cross` | 六平台交叉编译（cgo 依赖会在这里炸，而不是发布时） | ✅ 阻断 |
| `size` | release 体积 < 40 MB（M0 预算） | ✅ 阻断 |
| `window` | Xvfb 下开真窗口 | ❌ 仅提示（X 环境易抖动，见设计 §9.2） |

`GOPROXY` 在墙内改成 `https://goproxy.cn,direct`。Go 版本必须钉 `1.27.3`——mygo 的下限。

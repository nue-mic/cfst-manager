# 测速引擎来源声明 (Engine Attribution)

本目录 (`internal/engine`) 下的测速核心算法（TCP/HTTP 延迟测速、下载测速、IP 段解析、
延迟/丢包过滤与排序）**移植并改造自** 开源项目：

> **XIU2/CloudflareSpeedTest** — https://github.com/XIU2/CloudflareSpeedTest
> 许可证：GNU General Public License v3.0 (GPL-3.0)

为适配长期运行的 Web 守护进程，本项目对原始代码做了以下**结构性改造**（算法逻辑保持等价）：

1. **去全局化**：原项目使用包级全局变量（`task.Routines`、`utils.Output` 等）传参，
   改为显式的 `Config` 结构体，使引擎可重入、参数隔离。
2. **进度回调**：原项目通过 `github.com/cheggaaa/pb/v3` 在 stdout 打印进度条，
   改为 `ProgressFunc` 回调，便于经 SSE 推送到 Web 前端。
3. **错误返回**：原项目在 IP 解析失败等场景调用 `log.Fatal` 直接终止进程，
   改为返回 `error`，避免一次坏输入打挂整个守护进程。
4. **上下文取消**：新增 `context.Context` 支持，使 Web 端可随时中止正在进行的测速。

因 CFST 采用 GPL-3.0，本派生项目 **cfst-manager 整体同样以 GPL-3.0 发布**，并在此保留署名。
原始算法版权归 XIU2 及 CloudflareSpeedTest 贡献者所有。

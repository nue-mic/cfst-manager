# CLAUDE.md — cfst-manager 项目指南

把 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)（CFST，命令行 CDN 优选 IP 工具）
改造成 frpc-manager 式的 **Go 后端 + 内嵌 Web 图形界面** 管理服务，并对外提供与
**wetest.vip / hostmonit 逐字段兼容**的优选 IP API。

## 模块与构建

- 模块路径：`github.com/mia-clark/cfst-manager`，守护进程 `cfstmgrd`。
- 构建：`make build`（输出 `bin/cfstmgrd`）；运行 `make run`；`go vet ./...`；`go test ./...`。
- Go 1.25+。依赖极简：`go-chi/chi`（路由）、`VividCortex/ewma`（下载测速 EWMA）。前端内嵌于 `web/dist`，无构建链。

## 架构（数据流）

```
HTTP → internal/api（chi 路由 + 中间件 + SSE + 对外兼容 API）
         ├─ internal/runner（单任务串行、ctx 取消、进度/状态推送）
         │    └─ internal/engine（测速核心：移植自 CFST）
         ├─ internal/store（meta.json + runs/*.json 持久化）
         └─ internal/eventbus（pub/sub → SSE）
```

## 关键约束（勿破坏）

1. **engine 是 CFST 的 GPL-3.0 派生**（见 `internal/engine/NOTICE.md`），故整个项目为 GPL-3.0。
   重构原则：去包级全局变量（改 `Config`）、进度走 `ProgressFunc` 回调、支持 `context` 取消、
   错误用 `error` 返回（**绝不 `log.Fatal`**，否则打挂守护进程）。
2. **对外 API 必须与 wetest.vip 逐字节兼容**（见 `docs/API.zh-CN.md` 与 `internal/api/public.go`）。
   响应用显式结构体（`ipResp`/`licResp`）锁字段顺序；`info` 为三网 `{CM,CU,CT}` 对象；
   成功判据 `code==200`。改动前先核对实测契约。
3. **单任务模型**：engine 资源密集，runner 用 mutex 保证同一时刻仅一个测速任务。
4. **管理 API** 一律 Bearer 鉴权；**对外 API** 用 license key（或公开模式）。两套鉴权互不混用。

## 约定

- 全中文注释与文档（与上游团队习惯一致）。
- 持久化用原子写（`writeJSONAtomic`：临时文件 + rename）。
- 新增对外端点时同步更新 `docs/API.zh-CN.md` 与前端「API 文档」页。
- Windows 开发：shell 用 git bash；测试服务用临时 `CFST_DATA_DIR` 避免污染。

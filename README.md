# ⚡ CFST Manager — CDN 优选 IP 测速 Web 控制台

> 把命令行工具 [XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)（CFST）改造成 **图形化 Web 管理服务**：
> 浏览器里点击即可开始优选，所有命令行参数图形化配置，优选结果持久化保存，
> 并对外提供与 **[微测网 wetest.vip](https://www.wetest.vip/) / hostmonit 逐字段兼容**的优选 IP API —— 现有客户端可零改动无缝迁移。

单文件 Go 二进制（内嵌前端），开箱即用；也提供 Docker 一键部署。

---

## ✨ 核心特性

| 能力 | 说明 |
| :-- | :-- |
| 🖱️ **全图形化测速** | CFST 的全部命令行参数（`-n -t -dn -dt -tp -url -httping -cfcolo -tl -tll -tlr -sl -dd -allip` …）都在网页表单里配置，点击「开始测速」即可 |
| 📡 **实时进度** | 通过 SSE 实时推送延迟/下载测速进度、可用 IP 计数与运行日志 |
| 💾 **历史记录持久化** | 每次优选自动保存，可查看明细、导出（CSV/TXT/JSON）、删除、重新发布 |
| 🌐 **对外优选 IP API** | 内置与 wetest.vip / hostmonit **完全一致**的接口，三网（CM/CU/CT）分类返回，供第三方（如 cf2dns）调用 |
| 🔑 **授权密钥管理** | 为对外 API 颁发/吊销访问密钥；或开启「公开模式」接受任意 key（无缝迁移） |
| 🧩 **多 CDN Profile** | 内置 cloudflare / cloudfront / edgeone，各自独立 IP 源、结果与对外端点；CFST 可测任意 CDN 的 IP 段 |
| 📄 **IP 源管理** | 内置 Cloudflare 官方 IP 段，支持在线编辑/新增自定义 IP 源 |
| 🔒 **管理端鉴权** | 管理 API/控制台用 Bearer 令牌保护；令牌可自动生成 |
| 📦 **零依赖部署** | 前端已内嵌进二进制，无需 Node/数据库；单文件或 Docker 即可运行 |

---

## 🚀 快速开始

### 方式一：Docker（推荐）

```bash
cd deploy
cp .env.example .env        # 按需修改，至少建议设置 CFST_API_TOKEN
docker compose up -d
```

打开 `http://<服务器IP>:18123`，用 `CFST_API_TOKEN` 登录。
若未设置令牌，执行 `docker logs cfst-manager` 查看自动生成的令牌。

### 方式二：直接运行二进制

```bash
# 从源码编译（需 Go 1.25+）
make build
# 运行（首启会自动生成管理令牌并打印）
CFST_API_TOKEN=你的令牌 ./bin/cfstmgrd serve
```

访问 `http://127.0.0.1:18123`。

---

## ⚙️ 环境变量

| 变量 | 默认 | 说明 |
| :-- | :-- | :-- |
| `CFST_HTTP_ADDR` | `:18123` | 监听地址，支持裸端口（如 `18123`） |
| `CFST_API_TOKEN` | （自动生成） | 管理端 Bearer 令牌。留空则从 `<data>/admin_token` 读取或随机生成并打印 |
| `CFST_DATA_DIR` | `data` | 数据目录（历史记录、Profile、设置、IP 源） |
| `CFST_CORS_ORIGINS` | `*` | 允许跨域来源，逗号分隔或 `*` |
| `CFST_LOG_LEVEL` | `info` | `trace\|debug\|info\|warn\|error` |
| `CFST_PUBLIC_OPEN` | `false` | 公开模式：对外优选 IP API 接受任意 key（强制覆盖 UI 设置） |

---

## 🔗 对外 API：与 wetest.vip / hostmonit 无缝兼容

把原本指向 `www.wetest.vip` 或 `api.hostmonit.com` 的请求改指向本服务即可，**响应结构逐字段一致**。
详见 [docs/API.zh-CN.md](docs/API.zh-CN.md)。

```bash
# 获取 Cloudflare 优选 IP（三网分类）
curl "http://<本服务>/api/cf2dns/get_cloudflare_ip?key=YOUR_KEY&type=v4"

# hostmonit 兼容（cf2dns 默认调用方式）
curl -X POST "http://<本服务>/get_optimization_ip" \
  -H "Content-Type: application/json" -d '{"key":"YOUR_KEY","type":"v4"}'
```

成功响应（与 wetest 完全一致）：

```json
{"status":true,"code":200,"msg":"请求成功",
 "info":{"CM":[{"ip":"104.16.x.x","colo":"SJC","latency":150.5}],"CU":[...],"CT":[...]}}
```

> **无缝迁移三步**：① 部署本服务并跑一次优选 → ② 在「设置」开启**公开模式**（或在「授权密钥」登记旧 key）→ ③ 把客户端/DNS 指向本服务地址。完成。

---

## 🧭 使用流程

1. **测速优选**：选 Profile（如 cloudflare）→ 选 IP 源或手动输入 IP 段 → 调参 → 开始测速 → 实时看进度。
2. **自动发布**：测速完成默认自动发布为该 Profile 的对外数据（可在「设置」关闭）。
3. **对外提供**：第三方用 `get_<profile>_ip` 接口拉取优选 IP；用「授权密钥」控制访问。
4. **历史回溯**：在「历史记录」查看/导出/手动发布任意一次结果。

---

## 🏗️ 架构

```
cmd/cfstmgrd        守护进程入口（serve/health/version）
internal/
  engine            ⭐ 测速核心（移植自 CFST：去全局化 + 进度回调 + ctx 取消 + 错误返回）
  runner            任务管理器（单任务串行、可取消、进度推送）
  store             持久化（历史/Profile/License/IP源/设置）
  eventbus          进程内 pub/sub（SSE 事件源）
  api               REST API + SSE + 对外兼容 API + 中间件
  appcfg            环境变量配置
web/dist            内嵌前端（原生 SPA，无构建依赖）
deploy              Dockerfile / docker-compose / .env.example
```

技术栈：Go + go-chi（后端） · 原生 HTML/CSS/JS SPA（前端，内嵌） · SSE（实时进度）。

---

## 📜 许可证与致谢

本项目测速核心移植并改造自 **[XIU2/CloudflareSpeedTest](https://github.com/XIU2/CloudflareSpeedTest)**（GPL-3.0），
详见 [internal/engine/NOTICE.md](internal/engine/NOTICE.md)。因此本项目整体以 **GPL-3.0** 发布。

对外 API 兼容目标为 [微测网 wetest.vip](https://www.wetest.vip/)（仅接口结构兼容，与其无隶属关系）。

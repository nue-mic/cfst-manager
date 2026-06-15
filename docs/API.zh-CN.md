# CFST Manager API 文档

分两类：**对外优选 IP API**（公开，兼容 wetest.vip/hostmonit）与 **管理 API**（Bearer 鉴权）。

---

## 一、对外优选 IP API（公开）

与 [微测网 wetest.vip](https://www.wetest.vip/) 及 `api.hostmonit.com` **逐字段兼容**，现有客户端无需改动即可迁移。
鉴权方式：请求需携带 `key`（优选 IP 接口）或 `license`（授权接口）参数。
若服务端开启「公开模式」（`CFST_PUBLIC_OPEN=true` 或 UI 设置），则任意非空 key 均放行。

### 1.1 获取优选 IP

```
GET|POST /api/cf2dns/get_cloudflare_ip
GET|POST /api/cf2dns/get_cloudfront_ip
GET|POST /api/cf2dns/get_edgeone_ip
```

| 参数 | 必填 | 说明 |
| :-- | :-- | :-- |
| `key` | 是 | 授权密钥 |
| `type` | 是 | `v4`（IPv4）或 `v6`（IPv6） |

请求示例：

```bash
# GET
curl "http://<本服务>/api/cf2dns/get_cloudflare_ip?key=YOUR_KEY&type=v4"
# POST (x-www-form-urlencoded)
curl -X POST "http://<本服务>/api/cf2dns/get_cloudflare_ip" \
  -H "Content-Type: application/x-www-form-urlencoded" -d "key=YOUR_KEY&type=v4"
```

**成功响应**（`code==200` 为成功判据）：

```json
{
  "status": true,
  "code": 200,
  "msg": "请求成功",
  "info": {
    "CM": [{"ip": "104.16.123.45", "colo": "SJC", "latency": 150.5}],
    "CU": [{"ip": "172.64.80.1",  "colo": "LAX", "latency": 180.2}],
    "CT": [{"ip": "104.17.0.1",   "colo": "HKG", "latency": 90.1}]
  }
}
```

- `info` 按三网分类：`CM`=移动 `CU`=联通 `CT`=电信；每元素含 `ip`、`colo`（数据中心机场码）、`latency`（毫秒）。
- 单探测点默认三线返回同一份优选结果；可在「CDN Profile」高级用法中按线路绑定不同记录。
- 每线返回数量受「设置 → 对外 API 单次返回 IP 数上限」限制（默认 10）。

**错误响应**（与 wetest 原始字节一致）：

```json
// 缺少参数
{"status":false,"code":500,"msg":"未提交key、type参数","info":""}
// key 无效（非公开模式）
{"status":true,"code":500,"msg":"KEY不存在","info":{"CM":[],"CU":[],"CT":[]}}
```

### 1.2 hostmonit 兼容端点

```
POST /get_optimization_ip      # JSON body: {"key":"...","type":"v4"}
```

cf2dns 等客户端默认调用此端点；响应同 1.1（默认取 cloudflare Profile 数据）。

```bash
curl -X POST "http://<本服务>/get_optimization_ip" \
  -H "Content-Type: application/json" -d '{"key":"YOUR_KEY","type":"v4"}'
```

### 1.3 获取 License 授权信息

```
GET|POST /api/cf2dns/get_cloudflare_license      # 参数名为 license
```

| 参数 | 必填 | 说明 |
| :-- | :-- | :-- |
| `license` | 是 | 授权密钥（亦兼容传 `key`） |

```json
// 成功
{"status":true,"code":200,"count":99999999,"token":"YOUR_KEY","info":"Request success!"}
// 失败
{"status":false,"code":500,"msg":"License未找到","info":""}
```

`count` 为积分余额，`token` 为密钥回显。

---

## 二、管理 API（Bearer 鉴权）

所有 `/api/v1/*` 接口需请求头 `Authorization: Bearer <CFST_API_TOKEN>`；
SSE 等无法设置请求头的场景可用 `?access_token=<令牌>` 查询参数。

统一响应信封：`{"ok":true,"data":...}` 或 `{"ok":false,"error":"..."}`。

### 测速控制

| 方法 | 路径 | 说明 |
| :-- | :-- | :-- |
| POST | `/api/v1/speedtest/start` | 开始测速（body 为引擎参数 + `profile`/`note`/`ip_source`/`ip_text`） |
| POST | `/api/v1/speedtest/stop` | 中止当前测速 |
| GET | `/api/v1/speedtest/status` | 当前运行状态 |

`start` 请求体字段（均可选，缺省取默认值）：

```jsonc
{
  "profile": "cloudflare",       // 目标 CDN Profile
  "ip_source": "ip.txt",         // IP 源文件名；与 ip_text 二选一
  "ip_text": "1.1.1.1,104.16.0.0/24", // 直接指定 IP 段（优先）
  "note": "备注",
  "routines": 200, "ping_times": 4, "tcp_port": 443,
  "httping": false, "httping_status_code": 0, "httping_cf_colo": "",
  "test_count": 10, "download_time": 10, "url": "https://...", "min_speed": 0, "disable": false,
  "max_delay": 9999, "min_delay": 0, "max_loss_rate": 1, "test_all": false
}
```

### 历史记录

| 方法 | 路径 | 说明 |
| :-- | :-- | :-- |
| GET | `/api/v1/runs` | 历史摘要列表（`?profile=` 过滤） |
| GET | `/api/v1/runs/{id}` | 完整记录 |
| DELETE | `/api/v1/runs/{id}` | 删除 |
| GET | `/api/v1/runs/{id}/export?format=csv\|txt\|json&n=` | 导出 |
| POST | `/api/v1/runs/{id}/publish` | 发布到 Profile（body `{profile,ip_version}`） |

### CDN Profile / 授权密钥 / IP 源 / 设置 / 事件

| 方法 | 路径 | 说明 |
| :-- | :-- | :-- |
| GET/PUT | `/api/v1/profiles`、`/api/v1/profiles/{name}` | Profile 查询/编辑 |
| GET/POST/PUT/DELETE | `/api/v1/licenses[/{key}]` | 授权密钥管理 |
| GET/PUT/DELETE | `/api/v1/ipsources[/{name}]` | IP 源管理 |
| GET/PUT | `/api/v1/settings` | 全局设置 |
| GET | `/api/v1/events?access_token=` | SSE 实时事件流 |
| GET | `/api/v1/health` | 健康检查（无需鉴权） |

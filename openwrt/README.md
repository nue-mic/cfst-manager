# cfstmgrd · OpenWrt 打包（单个 all ipk）

把 CDN 优选 IP 测速管理器 `cfstmgrd` 打成 OpenWrt 的**一个** ipk 包：`Architecture: all`，**一个包到处装**，不分 CPU 架构。装上即由 **procd** 守护，配置走 **UCI**（`/etc/config/cfstmgrd`），可改端口/登录令牌、开机自启、一键启停、彻底装卸。

> 本目录自包含：ipk 生成逻辑、随包脚本、服务/配置文件全在这里。发布时由 CI（[.github/workflows/release.yml](../.github/workflows/release.yml) 的 `goreleaser` job）调 [build-ipk.sh](build-ipk.sh) 生成单个 `luci-app-cfstmgrd_<版本>-1_all.ipk` 并随 GitHub Release 一并上传。

---

## 一个 all 包怎么做到「到处装」

`cfstmgrd` 是按 CPU 编译的 Go 二进制，本来需要每个架构一个包。本方案用**「壳子包 + 安装时自取二进制」**把它收敛成一个 `all` 包：

```
all ipk（仅 ~15KB，不含二进制）
├── LuCI web 壳子（控制器 + 视图 + ACL + uci-defaults）  ← 网页里操作一切
├── /etc/init.d/cfstmgrd        procd 服务脚本
├── /etc/config/cfstmgrd        UCI 配置（端口/令牌/公开模式/…）
├── /usr/sbin/cfstmgrd-fetch    二进制拉取器（架构检测 + 自建源下载）
└── /usr/lib/cfstmgrd/VERSION   随包版本号

opkg install 时 → 只装壳子（不下载二进制）→ enable 服务
用户开 LuCI（服务 → CFST 优选IP）→ 点「下载/更新核心」按钮 →
  cfstmgrd-fetch：
  ① uname -m + 字节序 识别本机 CPU → 映射到 goreleaser 资产架构
  ② 拉 cfstmgrd_<版本>_linux_<架构>.tar.gz，下载优先级：
       ⒈ 自建 gh-raw 源（首选）   {base}/cfst-mgr-releases/v<版本>/<file>
       ⒉ 公共 GitHub 代理（兜底）  {proxy}https://github.com/.../releases/download/...
       ⒊ GitHub 直连（最后兜底）
  ③ 解出二进制装到 /usr/bin/cfstmgrd
→ 在 LuCI 里配端口/令牌、启动、点「打开管理后台」做优选 IP 测速
```

**好处**：一个包覆盖所有架构；彻底甩掉 opkg 架构串映射；连 mips64le 也能装。装包瞬间完成（不阻塞下载），核心由网页按需下载。

**唯一代价**：下载核心时需能联网（优先走自建源）。依赖 `luci-base` / `luci-compat`。

`cfstmgrd-fetch` 支持的 CPU（`uname -m` → 拉取的二进制）：x86_64、aarch64、armv7/armv6、i386、mips/mipsel（按字节序）、mips64/mips64le、riscv64、loongarch64。

---

## ⚠️ 适配范围（体积约束）

`cfstmgrd` 二进制约 7–9MB（内嵌前端，无 frp 之类重依赖，比 frpc 小很多）。OpenWrt 可写空间有限：

| 设备 | 能否装 |
|---|---|
| 8/16MB NOR flash 家用路由（无外置存储） | ⚠️ 可能紧张（`cfstmgrd-fetch` 会预检空间，需约 12MB） |
| 任意设备 + USB/SD 做 [extroot](https://openwrt.org/docs/guide-user/additional-software/extroot_configuration) | ✅ |
| 128MB+ NAND 机型（MT7621/798x、小米/Redmi 等） | ✅ |
| x86 软路由 | ✅ 推荐 |

`cfstmgrd-fetch` 下载前会 `df` 预检 `/usr/bin` 所在分区（需约 12MB），不足则中止并提示配置 extroot。

---

## 安装与使用（全程网页操作）

```sh
# 上传 luci-app-cfstmgrd_<版本>-1_all.ipk 到路由器后：
opkg install luci-app-cfstmgrd_<版本>-1_all.ipk
```

无需挑架构——任何设备装同一个 `all` 包。装完后**全程在网页里操作**：

1. 打开路由器后台 **LuCI → 服务(Services) → CFST 优选IP**
2. 点 **「下载 / 更新核心」**（自动识别 CPU、优先走自建源拉二进制并安装；可填 `latest` 或指定版本）
3. 填 **端口 / 登录令牌 / 公开模式**，点「保存并重启生效」
4. 点 **「启动」**，再点 **「打开管理后台」** 进 cfstmgrd 自带界面做测速优选/定时任务/对外 API

> 也可纯命令行：`cfstmgrd-fetch latest`（下载核心）、`uci set cfstmgrd.main.token=...`、`/etc/init.d/cfstmgrd start`。

> **OpenWrt 25.12+（默认 apk）**：本包是 ipk，面向 opkg（OpenWrt ≤24.10）。25.12 改用 apk(APKv3)，不能直接 `apk add` 此 ipk。见文末「OpenWrt 25.12 / apk」。

---

## 配置（端口 / 登录令牌 / 公开模式）

配置在 UCI `/etc/config/cfstmgrd` 的 `main` 节，改完 commit + restart 生效：

```sh
uci set cfstmgrd.main.http_addr=':9000'
uci set cfstmgrd.main.token='你的强随机令牌'
uci set cfstmgrd.main.public_open='1'     # 对外 API 接受任意 key（无缝迁移旧客户端）
uci commit cfstmgrd
/etc/init.d/cfstmgrd restart
```

| UCI 选项 | 默认 | 说明 |
|---|---|---|
| `enabled` | `1` | 0=禁用，不启动（网页启停按钮写这里） |
| `boot_autostart` | `0` | 1=开机强制自启（即使点过停止） |
| `http_addr` | `:18123` | 监听地址 `:端口` 或 `ip:端口` |
| `token` | 空 | 登录令牌；**留空则首次启动自动生成强随机令牌** |
| `data_dir` | `/usr/lib/cfstmgrd` | 数据根目录，**必须持久化路径**（勿用 /tmp、/var） |
| `log_level` | `info` | trace/debug/info/warn/error |
| `docs_enabled` | `1` | 是否开放 `/api/docs` |
| `cors_origins` | `*` | CORS 白名单 |
| `public_open` | `0` | 对外优选 IP API 公开模式：1=接受任意 key（无缝迁移 wetest/hostmonit 客户端） |
| `version` | （注释） | `cfstmgrd-fetch` 拉取的版本，留空=随包版本，填 `latest`=拉最新 |
| `download_proxy` | （注释） | 指定单一公共代理，跳过内置公共代理列表 |
| `no_proxy` | `0` | 1=跳过自建源+公共代理，直连 GitHub |
| `release_proxy_bases` | （注释） | 覆盖内置自建 gh-raw 源域名列表（逗号分隔） |
| `install_proxy_key` | `cfst-mgr-releases` | 覆盖自建源资源键（一般无需改） |

> 查看自动生成的令牌：`uci get cfstmgrd.main.token`

---

## 服务管理 / 升级 / 卸载

```sh
/etc/init.d/cfstmgrd start|stop|restart|enable|disable
logread -e cfstmgrd -f            # 实时日志

# 升级二进制（保留配置/数据）：
cfstmgrd-fetch latest            # 查最新版（经自建源/GitHub API）并安装
cfstmgrd-fetch 1.1.0             # 或拉指定版本；不带参数=随包 VERSION 记录的版本
# 或重装新版 all ipk（postinst 不自动下载，需在网页点「下载/更新核心」）：
opkg install luci-app-cfstmgrd_<新版本>-1_all.ipk

# 卸载（停服 + 删壳子；自取的 /usr/bin/cfstmgrd 与 /usr/lib/cfstmgrd 由 postrm 清理）
opkg remove luci-app-cfstmgrd
# 连配置也清掉：
opkg remove luci-app-cfstmgrd && rm -f /etc/config/cfstmgrd
```

> 升级保留配置：`/etc/config/cfstmgrd` 是 conffile，opkg 升级时保留你改过的 token/端口，新版默认配置写到 `/etc/config/cfstmgrd-opkg`。

---

## 本地 / CI 构建

依赖 `nfpm`：`go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest`

```sh
VERSION=1.1.0 ./openwrt/build-ipk.sh --out dist-ipk
# 或
./openwrt/build-ipk.sh --version 1.1.0
# 本地也可 make ipk VERSION=1.1.0
```

参数：`--version`（必填，须与 GitHub Release 一致，决定 `cfstmgrd-fetch` 默认拉哪个版本）、`--release`（默认 1）、`--out`（默认 dist-ipk）。产出单个 `luci-app-cfstmgrd_<版本>-1_all.ipk`。

> Windows/git-bash 也能跑（内置 `cygpath` 适配）；CI 在 Linux 上无此转换。

**发布自动化（已融进 goreleaser，无独立 job）**：CI 的 `goreleaser` job 在跑 goreleaser 之前先用 `build-ipk.sh` 把 all 包打到 `openwrt-dist/`，再由 goreleaser 的 `release.extra_files`（见 [.goreleaser.yml](../.goreleaser.yml)）与各架构二进制/tar 包**一并上传到同一个 Release**。

---

## 目录文件清单

```
openwrt/
├── README.md                       本文档
├── nfpm.yaml                       nfpm 打包模板（__占位符__ 由 build-ipk.sh 渲染）
├── build-ipk.sh                    单 all 包生成脚本
├── files/
│   ├── etc/
│   │   ├── init.d/cfstmgrd          procd 服务脚本（读 UCI → 注入 CFST_* 环境变量）
│   │   └── config/cfstmgrd          UCI 默认配置
│   └── usr/sbin/cfstmgrd-fetch      按 CPU 联网拉二进制（自建源首选 + 公共代理兜底 + 空间预检）
├── luci-app-cfstmgrd/              LuCI web 壳子
│   ├── luasrc/controller/cfstmgr.lua   JSON 动作：info/save/download/control
│   ├── luasrc/view/cfstmgr/main.htm    页面：状态 + 下载核心 + 配置 + 启停 + 开后台
│   └── root/
│       ├── usr/share/rpcd/acl.d/luci-app-cfstmgrd.json  ACL
│       └── etc/uci-defaults/40_luci-cfstmgrd           刷新 LuCI 菜单缓存
└── scripts/
    ├── postinst.sh                 只装壳子（不下载）+ enable + 刷新 LuCI 菜单 + 引导
    ├── prerm.sh                    卸载/升级前 stop+disable
    └── postrm.sh                   真正卸载时清理下载的二进制（升级时跳过）
```

---

## OpenWrt 25.12 / apk（APKv3）说明

OpenWrt 25.12 默认包管理器换成 **apk**（APKv3 + 签名），与 ipk 格式不同、不能直接 `apk add`。nfpm 产的 “apk” 是 Alpine 风味 APKv2，同样不适用于 OpenWrt 的 apk。原生支持 25.12 需走 OpenWrt SDK（APKv3 + 签名），本目录未实现。

25.12 用户过渡办法：直接下 `cfstmgrd` 的 `tar.gz` 裸二进制装到 `/usr/bin`，再把本目录 `files/etc/init.d/cfstmgrd`、`files/etc/config/cfstmgrd` 手动落位（完全绕开包管理器，等价于 `cfstmgrd-fetch` 干的事）。

---

> 给后续 LuCI 的稳定契约：服务名 `cfstmgrd`，init `/etc/init.d/cfstmgrd`，UCI `cfstmgrd.main.{http_addr,token,data_dir,public_open,version,download_proxy,…}`，拉取器 `/usr/sbin/cfstmgrd-fetch`。改这些名字会破坏后续 LuCI，务必保持兼容。

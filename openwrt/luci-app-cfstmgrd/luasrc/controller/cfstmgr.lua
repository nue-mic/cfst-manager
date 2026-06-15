-- =============================================================================
-- luci-app-cfstmgrd — cfstmgrd 的 OpenWrt LuCI「web 壳子」控制器
--
--   在路由器后台网页里：配端口/令牌、下载/更新核心二进制、启停服务、看版本状态、
--   一键打开 cfstmgrd 自带的 CDN 优选 IP 测速后台（:端口）。
--
--   复用已装好的 /usr/sbin/cfstmgrd-fetch（架构检测 + 自建源优先下载 + 安装）、
--   /etc/config/cfstmgrd（UCI）、/etc/init.d/cfstmgrd（procd）。
-- =============================================================================
module("luci.controller.cfstmgr", package.seeall)

local sys  = require "luci.sys"
local http = require "luci.http"
local util = require "luci.util"
local fs   = require "nixio.fs"
local uci  = require "luci.model.uci".cursor()

local BIN    = "/usr/bin/cfstmgrd"
local INIT   = "/etc/init.d/cfstmgrd"
local FETCH  = "/usr/sbin/cfstmgrd-fetch"
local DL_LOG = "/tmp/cfstmgrd-fetch.log"
local DL_LOCK = "/tmp/cfstmgrd-fetch.running"

function index()
	if not fs.access("/etc/config/cfstmgrd") then return end
	entry({"admin", "services", "cfstmgr"},
		template("cfstmgr/main"), _("CFST 优选IP"), 60).dependent = true
	entry({"admin", "services", "cfstmgr", "info"},            call("action_info"))
	entry({"admin", "services", "cfstmgr", "save"},            call("action_save"))
	entry({"admin", "services", "cfstmgr", "download"},        call("action_download"))
	entry({"admin", "services", "cfstmgr", "download_status"}, call("action_download_status"))
	entry({"admin", "services", "cfstmgr", "logs"},            call("action_logs"))
	entry({"admin", "services", "cfstmgr", "control"},         call("action_control")).leaf = true
end

local function u(k, d)
	local v = uci:get("cfstmgrd", "main", k)
	if v == nil or v == "" then return d end
	return v
end

local function is_running()
	return sys.call("pidof cfstmgrd >/dev/null 2>&1") == 0
end

-- 运行信息：架构、是否已装二进制、版本、运行状态、当前配置
function action_info()
	local has = fs.access(BIN) and true or false
	local ver = ""
	if has then ver = util.trim(sys.exec(util.shellquote(BIN) .. " version 2>/dev/null")) end
	http.prepare_content("application/json")
	http.write_json({
		arch        = util.trim(sys.exec("uname -m 2>/dev/null")),
		has_binary  = has,
		version     = ver,
		running     = is_running(),
		downloading = fs.access(DL_LOCK) and true or false,
		cfg = {
			enabled        = u("enabled", "1"),
			boot_autostart = u("boot_autostart", "0"),
			http_addr      = u("http_addr", ":18123"),
			token          = u("token", ""),
			data_dir       = u("data_dir", "/usr/lib/cfstmgrd"),
			log_level      = u("log_level", "info"),
			public_open    = u("public_open", "0"),
			version        = u("version", ""),
		},
	})
end

-- 保存配置（端口/令牌/数据目录/日志级别/公开模式/拉取版本）
function action_save()
	local function setv(opt, val, allow_empty)
		if val == nil then return end
		if val == "" and not allow_empty then return end
		uci:set("cfstmgrd", "main", opt, val)
	end
	-- 确保 section 存在
	if not uci:get("cfstmgrd", "main") then
		uci:set("cfstmgrd", "main", "cfstmgrd")
	end
	local http_addr = http.formvalue("http_addr")
	if http_addr ~= nil and http_addr ~= "" then
		-- 归一化：仅填端口（纯数字）时自动补冒号 -> :端口
		local addr = util.trim(http_addr)
		local port_only = addr:match("^(%d+)$")
		if port_only ~= nil then
			local p = tonumber(port_only)
			if not p or p < 1 or p > 65535 then
				http.prepare_content("application/json")
				http.write_json({ ok = false, error = "端口需为 1-65535 的数字：" .. http_addr })
				return
			end
			addr = ":" .. port_only
		end
		uci:set("cfstmgrd", "main", "http_addr", addr)
	end
	setv("token",     http.formvalue("token"),     false)
	setv("data_dir",  http.formvalue("data_dir"),  false)
	setv("log_level", http.formvalue("log_level"), false)
	setv("version",   http.formvalue("version"),   true)
	local po = http.formvalue("public_open")
	if po == "1" or po == "0" then uci:set("cfstmgrd", "main", "public_open", po) end
	local en = http.formvalue("enabled")
	if en == "1" or en == "0" then uci:set("cfstmgrd", "main", "enabled", en) end
	-- 开机强制自启开关。勾选时顺带把持久运行状态置 1。
	local ba = http.formvalue("boot_autostart")
	if ba == "1" or ba == "0" then
		uci:set("cfstmgrd", "main", "boot_autostart", ba)
		if ba == "1" then uci:set("cfstmgrd", "main", "enabled", "1") end
	end
	uci:commit("cfstmgrd")
	http.prepare_content("application/json")
	http.write_json({ ok = true })
end

-- 异步下载/更新核心：后台跑 cfstmgrd-fetch，日志写 DL_LOG，锁文件标识进行中
function action_download()
	http.prepare_content("application/json")
	if fs.access(DL_LOCK) then
		http.write_json({ ok = true, status = "in_progress" })
		return
	end
	local version = http.formvalue("version") or ""
	local verarg = ""
	if version ~= "" then verarg = " " .. util.shellquote(version) end
	local cmd = string.format(
		"( touch %s; %s%s > %s 2>&1; rm -f %s ) >/dev/null 2>&1 &",
		util.shellquote(DL_LOCK), util.shellquote(FETCH), verarg,
		util.shellquote(DL_LOG), util.shellquote(DL_LOCK))
	sys.call(cmd)
	http.write_json({ ok = true, status = "started" })
end

-- 下载进度/结果：是否仍在跑、日志尾部、是否已装好、版本
function action_download_status()
	http.prepare_content("application/json")
	local has = fs.access(BIN) and true or false
	local ver = ""
	if has then ver = util.trim(sys.exec(util.shellquote(BIN) .. " version 2>/dev/null")) end
	local logtail = ""
	if fs.access(DL_LOG) then
		logtail = sys.exec("tail -n 200 " .. util.shellquote(DL_LOG) .. " 2>/dev/null")
	end
	http.write_json({
		running    = fs.access(DL_LOCK) and true or false,
		has_binary = has,
		version    = ver,
		log        = logtail,
	})
end

-- 运行日志：系统日志(logread)里 cfstmgrd 相关行。
function action_logs()
	http.prepare_content("application/json")
	local lines = tonumber(http.formvalue("lines") or "") or 300
	if lines < 50 then lines = 50 end
	if lines > 1000 then lines = 1000 end
	local log = sys.exec("logread 2>/dev/null | grep -iE 'cfstmgr' | tail -n " .. lines)
	local st = util.trim(sys.exec(util.shellquote(INIT) .. " status 2>&1"))
	http.write_json({
		log     = log or "",
		status  = st,
		running = is_running(),
	})
end

-- 把「持久运行状态」写进 UCI option enabled（跨重启、跨升级保留）。
local function set_run_state(v) -- v: "1" | "0"
	uci:set("cfstmgrd", "main", "enabled", v)
	uci:commit("cfstmgrd")
end

-- 服务控制：start/stop/restart/enable/disable
function action_control(act)
	http.prepare_content("application/json")
	local allow = { start = true, stop = true, restart = true, enable = true, disable = true }
	if not allow[act] then
		http.write_json({ ok = false, error = "invalid action" })
		return
	end
	local rc
	if act == "start" or act == "restart" then
		-- 先置 enabled=1（否则 start_service 会因「停止状态」拒绝启动），再执行。
		set_run_state("1")
		rc = sys.call(INIT .. " " .. act .. " >/dev/null 2>&1")
	elseif act == "stop" then
		rc = sys.call(INIT .. " stop >/dev/null 2>&1")
		-- 默认把停止状态持久化；开启开机强制自启则保留 enabled=1。
		if u("boot_autostart", "0") ~= "1" then
			set_run_state("0")
		end
	else
		rc = sys.call(INIT .. " " .. act .. " >/dev/null 2>&1")
	end
	http.write_json({ ok = (rc == 0), running = is_running() })
end

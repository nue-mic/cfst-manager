/* ============================================================
 * CFST Manager — CDN 优选 IP 控制台（原生 SPA，无构建依赖）
 * ============================================================ */
(() => {
'use strict';

// ---------- 全局状态 ----------
const State = {
  token: localStorage.getItem('cfst_token') || '',
  theme: localStorage.getItem('cfst_theme') || 'light',
  page: location.hash.replace('#', '') || 'dashboard',
  version: 'dev',
  live: { running: false, active: null, progress: null, queue: [], logs: [], lastFinish: null, hijackHits: 0, hijackDismissed: false },
  sse: null,
};

// ---------- 工具 ----------
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const fmtTime = (t) => { if (!t) return '-'; const d = new Date(t); return isNaN(d) ? '-' : d.toLocaleString('zh-CN', { hour12: false }); };
const fmtNum = (n, d = 2) => (n == null ? '-' : Number(n).toFixed(d));

function toast(msg, kind = 'info', ms = 3200) {
  const host = $('#toast-host');
  const t = document.createElement('div');
  t.className = `toast ${kind}`;
  t.textContent = msg;
  host.appendChild(t);
  setTimeout(() => { t.style.opacity = '0'; t.style.transition = 'opacity .3s'; setTimeout(() => t.remove(), 300); }, ms);
}

function copy(text) {
  navigator.clipboard?.writeText(text).then(() => toast('已复制到剪贴板', 'ok')).catch(() => toast('复制失败', 'err'));
}

// ---------- API 客户端 ----------
async function api(method, path, body) {
  const opts = { method, headers: { 'Authorization': 'Bearer ' + State.token } };
  if (body !== undefined) { opts.headers['Content-Type'] = 'application/json'; opts.body = JSON.stringify(body); }
  const res = await fetch(path, opts);
  if (res.status === 401) { logout(); throw new Error('未授权'); }
  let json = {};
  try { json = await res.json(); } catch (_) {}
  if (!res.ok || json.ok === false) throw new Error(json.error || ('请求失败 ' + res.status));
  return json.data;
}

// ---------- 鉴权 ----------
function logout() {
  State.token = '';
  localStorage.removeItem('cfst_token');
  if (State.sse) { State.sse.close(); State.sse = null; }
  renderLogin();
}

async function tryLogin(token) {
  const res = await fetch('/api/v1/version', { headers: { 'Authorization': 'Bearer ' + token } });
  if (!res.ok) throw new Error('令牌无效');
  const j = await res.json();
  State.token = token;
  localStorage.setItem('cfst_token', token);
  State.version = j.data?.version || 'dev';
  return true;
}

// ---------- SSE ----------
function connectSSE() {
  if (State.sse) State.sse.close();
  const es = new EventSource('/api/v1/events?access_token=' + encodeURIComponent(State.token));
  State.sse = es;
  const handle = (type, e) => {
    let data = {};
    try { data = JSON.parse(e.data); } catch (_) { return; }
    onEvent(type, data.data !== undefined ? data.data : data);
  };
  ['status', 'run.queued', 'run.started', 'run.progress', 'run.finished', 'run.failed', 'run.stopped', 'queue.changed', 'log'].forEach(t => {
    es.addEventListener(t, (e) => handle(t, e));
  });
  es.onerror = () => { /* 浏览器自动重连 */ };
}

// 日志面板环形缓冲容量。调试模式下日志可能很密集，留足回看空间又不至于撑爆浏览器内存。
const LOG_MAX = 1000;

function pushLog(msg, level, tone) {
  State.live.logs.push({ ts: new Date().toLocaleTimeString('zh-CN', { hour12: false }), msg, level: level || '', tone: tone || '' });
  if (State.live.logs.length > LOG_MAX) State.live.logs.shift();
}

// 单条日志行 HTML。level=debug 标记调试行；tone=err/warn 对应上游终端的红/黄着色。
function logLineHTML(l) {
  const cls = ['line', l.level, l.tone].filter(Boolean).join(' ');
  return `<div class="${cls}"><span class="ts">${l.ts}</span>${esc(l.msg)}</div>`;
}

// 增量追加一行到日志面板（避免调试刷屏时整块重渲染导致卡顿）。仅在仪表盘可见时操作 DOM。
function appendLogLine() {
  if (State.page !== 'dashboard') return;
  const log = $('#live-log');
  if (!log) return;
  const l = State.live.logs[State.live.logs.length - 1];
  if (!l) return;
  log.insertAdjacentHTML('beforeend', logLineHTML(l));
  while (log.childElementCount > LOG_MAX) log.removeChild(log.firstChild); // DOM 与环形缓冲容量保持一致
  log.scrollTop = log.scrollHeight;
}

// 透明代理劫持特征：下载/握手出现 TLS 证书不符等（Go 报错含 x509 / certificate is valid for /
// failed to verify certificate，或 tls: internal error）。连接被中间人改道到代理节点时大量出现。
const HIJACK_THRESHOLD = 3; // 单次测速内累计这么多次即判定为「疑似被劫持」
function isProxyHijackError(msg) {
  return /x509|certificate is valid for|failed to verify certificate|certificate subject name|tls: internal error|remote error: tls/i.test(msg || '');
}

// 重置本次测速的劫持检测状态（新测速开始时调用）。
function resetHijackDetection() {
  State.live.hijackHits = 0;
  State.live.hijackDismissed = false;
  renderHijackBanner();
}

// 在日志面板顶部渲染/清除「疑似被劫持」横幅。幂等：已显示则不重复渲染。
function renderHijackBanner() {
  const el = $('#log-hijack-banner');
  if (!el) return;
  const show = State.live.hijackHits >= HIJACK_THRESHOLD && !State.live.hijackDismissed;
  if (!show) { el.innerHTML = ''; return; }
  if (el.childElementCount) return; // 已显示，避免每条报错都重渲染
  el.innerHTML = `<div class="hijack-banner">
    <span class="ico">⚠️</span>
    <div><b>测速连接疑似被透明代理劫持</b>　下载/握手大量出现 <b>TLS 证书不符</b>（拿到的证书并非 speed.cloudflare.com）。
    本次测速结果<b>不可信</b>。请在<b>上游代理</b>为 Cloudflare 配置直连：把本机源 IP 加入代理「绕过/直连」名单，
    或加规则 <code>DOMAIN-SUFFIX,cloudflare.com,DIRECT</code> 与 Cloudflare 网段
    <code>IP-CIDR,104.16.0.0/13,DIRECT,no-resolve</code>（172.64.0.0/13、162.158.0.0/15 …）。</div>
    <span class="x" title="忽略本次提示">×</span>
  </div>`;
  const x = $('.x', el);
  if (x) x.onclick = () => { State.live.hijackDismissed = true; el.innerHTML = ''; };
}

function applyStatus(d) {
  State.live.running = !!d.running;
  State.live.active = d.active || null;
  State.live.progress = d.progress || null;
  State.live.queue = d.queue || [];
}

function onEvent(type, d) {
  switch (type) {
    case 'status':
    case 'queue.changed':
      applyStatus(d);
      break;
    case 'run.queued':
      pushLog(`已入队 · ${d.trigger || ''}（前面还有 ${d.ahead} 个）`);
      break;
    case 'run.started':
      State.live.running = true; State.live.active = d; State.live.progress = null; State.live.lastFinish = null;
      resetHijackDetection();
      pushLog(`测速开始 · ${d.trigger || ''} · profile=${d.profile || '-'}`);
      break;
    case 'run.progress':
      State.live.progress = d;
      break;
    case 'run.finished':
      State.live.running = false; State.live.lastFinish = d;
      pushLog(`测速完成 · 命中 ${d.count} 个 IP`);
      toast(`测速完成，命中 ${d.count} 个 IP`, 'ok');
      if (State.page === 'history' || State.page === 'schedules') renderPage();
      break;
    case 'run.failed':
      State.live.running = false; State.live.lastFinish = d;
      pushLog(`测速失败 · ${d.error || ''}`);
      toast('测速失败: ' + (d.error || ''), 'err', 5000);
      if (State.page === 'schedules') renderPage();
      break;
    case 'run.stopped':
      State.live.running = false; State.live.lastFinish = d;
      pushLog('测速已中止');
      toast('测速已中止', 'warn');
      break;
    case 'log':
      if (d.msg) {
        pushLog(d.msg, d.level, d.tone);
        appendLogLine();
        if (d.level === 'debug' && isProxyHijackError(d.msg)) { State.live.hijackHits++; renderHijackBanner(); }
      }
      return; // 日志行已增量渲染，跳过整块重渲染（调试刷屏时显著降卡顿）
  }
  if (State.page === 'dashboard') updateLiveUI();
}

// ============================================================
// 渲染
// ============================================================
const NAV = [
  { id: 'dashboard', ico: '⚡', label: '测速优选' },
  { id: 'schedules', ico: '⏰', label: '定时任务' },
  { id: 'history', ico: '🕑', label: '历史记录' },
  { id: 'profiles', ico: '🌐', label: 'CDN Profile' },
  { id: 'licenses', ico: '🔑', label: '授权密钥' },
  { id: 'ipsources', ico: '📄', label: 'IP 源' },
  { id: 'settings', ico: '⚙️', label: '设置' },
  { id: 'apidocs', ico: '📡', label: 'API 文档' },
];

function renderLogin() {
  document.documentElement.setAttribute('data-theme', State.theme);
  $('#app').innerHTML = `
    <div class="login-wrap"><div class="card login-card">
      <div class="logo-big">⚡</div>
      <h2>CFST Manager</h2>
      <p>CDN 优选 IP 测速控制台 · 请输入管理令牌登录</p>
      <div class="field">
        <label>管理令牌 (CFST_API_TOKEN)</label>
        <input type="password" id="login-token" placeholder="粘贴管理令牌…" autofocus>
        <div class="desc">首次启动若未设置 CFST_API_TOKEN，令牌会在服务端日志中自动生成并打印。</div>
      </div>
      <button class="btn btn-primary" id="login-btn" style="width:100%">登 录</button>
    </div></div>`;
  const submit = async () => {
    const tk = $('#login-token').value.trim();
    if (!tk) return toast('请输入令牌', 'warn');
    try { await tryLogin(tk); boot(); } catch (e) { toast('登录失败：' + e.message, 'err'); }
  };
  $('#login-btn').onclick = submit;
  $('#login-token').onkeydown = (e) => { if (e.key === 'Enter') submit(); };
}

function renderShell() {
  document.documentElement.setAttribute('data-theme', State.theme);
  $('#app').innerHTML = `
    <div class="layout">
      <aside class="sidebar">
        <div class="brand">
          <div class="logo">⚡</div>
          <div><div class="name">CFST Manager</div><div class="ver">${esc(State.version)}</div></div>
        </div>
        ${NAV.map(n => `<div class="nav-item" data-nav="${n.id}"><span class="ico">${n.ico}</span>${n.label}</div>`).join('')}
        <div class="spacer"></div>
        <div class="nav-item" id="theme-toggle"><span class="ico">🌓</span>切换主题</div>
        <div class="nav-item" id="logout-btn"><span class="ico">🚪</span>退出登录</div>
        <div class="side-foot">基于 XIU2/CloudflareSpeedTest · GPL-3.0</div>
      </aside>
      <main class="main" id="main"></main>
    </div>`;
  $$('[data-nav]').forEach(el => el.onclick = () => go(el.dataset.nav));
  $('#theme-toggle').onclick = () => {
    State.theme = State.theme === 'dark' ? 'light' : 'dark';
    localStorage.setItem('cfst_theme', State.theme);
    document.documentElement.setAttribute('data-theme', State.theme);
  };
  $('#logout-btn').onclick = logout;
  renderPage();
}

function go(page) { State.page = page; location.hash = page; renderPage(); }

function setActiveNav() { $$('[data-nav]').forEach(el => el.classList.toggle('active', el.dataset.nav === State.page)); }

async function renderPage() {
  setActiveNav();
  const main = $('#main');
  if (!main) return;
  const pages = { dashboard: pageDashboard, schedules: pageSchedules, history: pageHistory, profiles: pageProfiles, licenses: pageLicenses, ipsources: pageIPSources, settings: pageSettings, apidocs: pageApiDocs };
  const fn = pages[State.page] || pageDashboard;
  main.innerHTML = `<div class="empty"><div class="big">⏳</div>加载中…</div>`;
  try { await fn(main); } catch (e) { main.innerHTML = `<div class="empty"><div class="big">⚠️</div>${esc(e.message)}</div>`; }
}

function topbar(title, sub, actions = '') {
  return `<div class="topbar"><div><h1>${title}</h1>${sub ? `<div class="sub">${sub}</div>` : ''}</div><div class="topbar-actions">${actions}</div></div>`;
}

// ============================================================
// 页面：测速优选
// ============================================================
const ENGINE_DEFAULTS = { routines: 200, ping_times: 4, tcp_port: 443, httping: false, httping_status_code: 0, httping_cf_colo: '', test_count: 10, download_time: 10, url: 'https://speed.cloudflare.com/__down?bytes=50000000', min_speed: 0, disable: false, max_delay: 9999, min_delay: 0, max_loss_rate: 1, test_all: false, debug: false };

// 下载测速地址预设。测速地址须托管在被优选的 CDN 上（优选 Cloudflare 就用 Cloudflare 地址），
// 这样强制连候选 IP 才能测出该边缘真实速度。多备几个：大文件被限流时可换小的/换域名。
// speed.cloudflare.com 是 Cloudflare 官方测速基础设施，最稳；公共地址都可能限额，最稳是自建(CF Workers)。
const SPEED_URL_PRESETS = [
  { v: 'https://speed.cloudflare.com/__down?bytes=50000000',  t: 'Cloudflare 官方 · 50MB（推荐·最稳·实测全过）' },
  { v: 'https://speed.cloudflare.com/__down?bytes=100000000', t: 'Cloudflare 官方 · 100MB（高带宽·国内线路可能 403）' },
  { v: 'https://speed.cloudflare.com/__down?bytes=200000000', t: 'Cloudflare 官方 · 200MB（高带宽·国内线路可能 403）' },
  { v: 'https://speed.cloudflare.com/__down?bytes=10000000',  t: 'Cloudflare 官方 · 10MB（低带宽/路由器·测不准）' },
  { v: 'https://cdnjs.cloudflare.com/ajax/libs/three.js/r128/three.min.js', t: 'cdnjs（Cloudflare 备用·小文件/仅连通性）' },
  { v: 'https://d7uri8nf7uskq.cloudfront.net/tools/list-cloudfront-ips', t: 'AWS CloudFront 官方（供 CloudFront 优选）' },
];
// 生成「预设下拉」HTML：选中即把值填入 targetId 输入框（保留手动自定义）。
function urlPresetSelect(targetId) {
  return `<select onchange="if(this.value){var el=document.getElementById('${targetId}');el.value=this.value;el.dispatchEvent(new Event('change'))}">
    <option value="">— 选择测速地址预设填入 —</option>
    ${SPEED_URL_PRESETS.map(p => `<option value="${esc(p.v)}">${esc(p.t)}</option>`).join('')}
  </select>`;
}

async function pageDashboard(main) {
  let profiles = [], sources = [], settings = {};
  try { [profiles, sources, settings] = await Promise.all([api('GET', '/api/v1/profiles'), api('GET', '/api/v1/ipsources'), api('GET', '/api/v1/settings')]); } catch (_) {}
  const def = Object.assign({}, ENGINE_DEFAULTS, settings.default_config || {});

  main.innerHTML = topbar('测速优选', '配置参数并开始优选 — 所有命令行参数均可在此图形化设置') + `
    <div class="grid grid-2">
      <div class="card">
        <div class="card-title">📋 测速配置</div>
        <div class="field">
          <label>目标 CDN Profile</label>
          <select id="f-profile">${profiles.map(p => `<option value="${esc(p.name)}">${esc(p.title || p.name)}</option>`).join('')}</select>
        </div>
        <div class="field">
          <label>IP 来源</label>
          <div class="row" style="margin-bottom:8px">
            <label class="radio"><input type="radio" name="ipmode" value="file" checked>选择 IP 源文件</label>
            <label class="radio"><input type="radio" name="ipmode" value="text">手动输入 IP 段</label>
          </div>
          <select id="f-ipsource">${sources.map(s => `<option value="${esc(s.name)}">${esc(s.name)} (${s.lines} 段${s.builtin ? ' · 内置' : ''})</option>`).join('')}</select>
          <textarea id="f-iptext" placeholder="例如：1.1.1.1, 104.16.0.0/24, 2606:4700::/32" style="display:none"></textarea>
        </div>
        <div class="row">
          <div class="field"><label>测速模式</label>
            <select id="f-httping"><option value="false">TCPing（默认）</option><option value="true">HTTPing</option></select>
          </div>
          <div class="field"><label>测速端口 -tp</label><input type="number" id="f-tcp_port" value="${def.tcp_port}"></div>
        </div>
        <details>
          <summary style="cursor:pointer;color:var(--text-2);margin:6px 0 12px">⚙️ 高级参数</summary>
          <div class="row">
            <div class="field"><label>延迟测速线程 -n</label><input type="number" id="f-routines" value="${def.routines}"></div>
            <div class="field"><label>延迟测速次数 -t</label><input type="number" id="f-ping_times" value="${def.ping_times}"></div>
          </div>
          <div class="row">
            <div class="field"><label>下载测速数量 -dn</label><input type="number" id="f-test_count" value="${def.test_count}"></div>
            <div class="field"><label>下载测速时间(秒) -dt</label><input type="number" id="f-download_time" value="${def.download_time}"></div>
          </div>
          <div class="field"><label>测速地址 -url</label>
            <div style="margin-bottom:8px">${urlPresetSelect('f-url')}</div>
            <input type="text" id="f-url" value="${esc(def.url)}">
            <div class="desc">下拉选预设自动填入，也可手动填自建地址。地址须托管在被优选的 CDN 上（优选 Cloudflare 用 Cloudflare 地址）。多备几个：限流/失效时可换。</div>
          </div>
          <div class="row">
            <div class="field"><label>下载速度下限(MB/s) -sl</label><input type="number" step="0.1" id="f-min_speed" value="${def.min_speed}"></div>
            <div class="field"><label>丢包率上限 -tlr</label><input type="number" step="0.01" id="f-max_loss_rate" value="${def.max_loss_rate}"></div>
          </div>
          <div class="row">
            <div class="field"><label>平均延迟上限(ms) -tl</label><input type="number" id="f-max_delay" value="${def.max_delay}"></div>
            <div class="field"><label>平均延迟下限(ms) -tll</label><input type="number" id="f-min_delay" value="${def.min_delay}"></div>
          </div>
          <div class="row">
            <div class="field"><label>HTTPing 有效状态码</label><input type="number" id="f-httping_status_code" value="${def.httping_status_code}"></div>
            <div class="field"><label>匹配地区 -cfcolo</label><input type="text" id="f-httping_cf_colo" placeholder="HKG,LAX,SJC" value="${esc(def.httping_cf_colo)}"></div>
          </div>
          <div class="row">
            <label class="switch"><input type="checkbox" id="f-disable" ${def.disable ? 'checked' : ''}><span class="track"></span>禁用下载测速 -dd</label>
            <label class="switch"><input type="checkbox" id="f-test_all" ${def.test_all ? 'checked' : ''}><span class="track"></span>测速全部 IP -allip</label>
          </div>
          <div class="row">
            <label class="switch"><input type="checkbox" id="f-debug" ${def.debug ? 'checked' : ''}><span class="track"></span>调试输出 -debug</label>
          </div>
          <div class="desc">开启后，出现非预期情况时右侧「运行日志」会打印详细诊断（逐字移植自上游 -debug）。注意：上游 -debug 仅在 <b>HTTPing 延迟测速</b> 与 <b>下载测速</b> 阶段产生输出，默认的 TCPing 模式下只有下载阶段会有调试日志。</div>
        </details>
        <div class="field"><label>备注（可选）</label><input type="text" id="f-note" placeholder="给这次测速加个标签…"></div>
        <div class="flex">
          <button class="btn btn-primary" id="btn-start">▶ 开始测速</button>
          <button class="btn btn-danger" id="btn-stop">■ 停止</button>
        </div>
      </div>

      <div class="card">
        <div class="card-title">📡 实时状态 <span id="live-badge"></span></div>
        <div id="live-progress"></div>
        <div id="live-queue"></div>
        <div class="card-title" style="margin-top:18px">📜 运行日志</div>
        <div id="log-hijack-banner"></div>
        <div class="logbox" id="live-log"></div>
      </div>
    </div>

    <div class="card" id="result-card" style="display:none">
      <div class="card-title">🏆 最新优选结果 <span class="hint" id="result-hint"></span></div>
      <div class="table-wrap"><table id="result-table"></table></div>
    </div>`;

  // IP 来源切换
  $$('input[name=ipmode]').forEach(r => r.onchange = () => {
    const file = $('input[name=ipmode]:checked').value === 'file';
    $('#f-ipsource').style.display = file ? '' : 'none';
    $('#f-iptext').style.display = file ? 'none' : '';
  });

  $('#btn-start').onclick = startTest;
  $('#btn-stop').onclick = async () => { try { await api('POST', '/api/v1/speedtest/stop'); } catch (e) { toast(e.message, 'warn'); } };
  updateLiveUI();
}

function collectConfig() {
  const v = (id) => $('#' + id)?.value;
  const num = (id) => Number(v(id));
  const cfg = {
    routines: num('f-routines'), ping_times: num('f-ping_times'), tcp_port: num('f-tcp_port'),
    httping: v('f-httping') === 'true', httping_status_code: num('f-httping_status_code'),
    httping_cf_colo: v('f-httping_cf_colo') || '',
    test_count: num('f-test_count'), download_time: num('f-download_time'), url: v('f-url') || '',
    min_speed: num('f-min_speed'), disable: $('#f-disable').checked,
    max_delay: num('f-max_delay'), min_delay: num('f-min_delay'), max_loss_rate: num('f-max_loss_rate'),
    test_all: $('#f-test_all').checked, debug: $('#f-debug').checked,
  };
  const payload = Object.assign({}, cfg, { profile: v('f-profile'), note: v('f-note') || '' });
  if ($('input[name=ipmode]:checked').value === 'text') payload.ip_text = $('#f-iptext').value.trim();
  else payload.ip_source = $('#f-ipsource').value;
  return payload;
}

async function startTest() {
  const payload = collectConfig();
  if (payload.ip_text !== undefined && !payload.ip_text) return toast('请输入要测速的 IP 段', 'warn');
  try {
    State.live.logs = [];
    resetHijackDetection();
    const res = await api('POST', '/api/v1/speedtest/start', payload);
    if (res && res.ahead > 0) toast(`已加入队列，前面还有 ${res.ahead} 个任务`, 'info');
    else toast('测速已开始', 'ok');
  } catch (e) { toast('启动失败：' + e.message, 'err'); }
}

function updateLiveUI() {
  const badge = $('#live-badge'); if (!badge) return;
  const L = State.live;
  const qn = (L.queue || []).length;
  badge.innerHTML = L.running
    ? `<span class="badge ok"><span class="pulse"></span> 测速中</span>${qn ? ` <span class="badge muted">队列 ${qn}</span>` : ''}`
    : (qn ? `<span class="badge warn">队列 ${qn}</span>` : `<span class="badge muted">空闲</span>`);
  const start = $('#btn-start'), stop = $('#btn-stop');
  if (start) { start.disabled = false; start.textContent = L.running ? '▶ 加入队列' : '▶ 开始测速'; }
  if (stop) stop.disabled = !L.running;

  const prog = $('#live-progress');
  if (prog) {
    if (L.progress && (L.running || L.progress.total)) {
      const p = L.progress;
      const pct = p.total ? Math.min(100, Math.round(p.current / p.total * 100)) : 0;
      const stage = p.stage === 'download' ? '下载测速' : '延迟测速';
      const who = L.active && L.active.trigger ? ` <span class="tag">${esc(L.active.trigger)}</span>` : '';
      prog.innerHTML = `
        <div class="flex" style="margin-bottom:6px;font-size:12px;color:var(--text-2)">当前任务${who}</div>
        <div class="progress"><i style="width:${pct}%"></i></div>
        <div class="prog-meta"><span>${stage} · ${pct}%</span><span>${p.current}/${p.total} · 可用 ${p.available}</span></div>`;
    } else if (L.active && L.running) {
      prog.innerHTML = `<div class="muted">准备中…（加载 IP 段）</div>`;
    } else {
      prog.innerHTML = `<div class="muted">暂无进行中的任务。配置参数后点击「开始测速」。</div>`;
    }
  }

  const queue = $('#live-queue');
  if (queue) {
    if (qn > 0) {
      queue.innerHTML = `<div style="margin-top:12px"><div style="font-size:12px;color:var(--text-2);margin-bottom:6px">⏳ 排队中（${qn}）</div>` +
        L.queue.map((j, i) => `<div class="flex" style="padding:5px 0;border-bottom:1px solid var(--border);font-size:12.5px">
          <span class="rank">${i + 1}</span>
          <span>${esc(j.trigger || '手动')}</span>
          <span class="muted">${esc(j.profile || '')}</span>
          <button class="btn btn-sm btn-ghost right" data-cancelq="${esc(j.run_id)}">取消</button>
        </div>`).join('') + `</div>`;
      $$('[data-cancelq]', queue).forEach(b => b.onclick = async () => {
        try { await api('DELETE', '/api/v1/speedtest/queue/' + encodeURIComponent(b.dataset.cancelq)); } catch (e) { toast(e.message, 'warn'); }
      });
    } else {
      queue.innerHTML = '';
    }
  }

  const log = $('#live-log');
  if (log) { log.innerHTML = L.logs.map(logLineHTML).join(''); log.scrollTop = log.scrollHeight; }
  renderHijackBanner();

  const card = $('#result-card');
  if (card && L.lastFinish && L.lastFinish.top && L.lastFinish.top.length) {
    card.style.display = '';
    $('#result-hint').textContent = `共 ${L.lastFinish.count} 个 · 展示前 ${L.lastFinish.top.length}`;
    $('#result-table').innerHTML = resultTableHTML(L.lastFinish.top);
  }
}

function resultTableHTML(rows) {
  return `<thead><tr><th>#</th><th>IP 地址</th><th>丢包率</th><th>平均延迟</th><th>下载速度</th><th>地区</th></tr></thead><tbody>${
    rows.map((r, i) => `<tr>
      <td class="rank ${i === 0 ? 'top' : ''}">${i + 1}</td>
      <td class="mono">${esc(r.ip)}</td>
      <td>${fmtNum(r.loss_rate * 100, 0)}%</td>
      <td>${fmtNum(r.delay_ms, 1)} ms</td>
      <td>${fmtNum(r.speed_mbps)} MB/s</td>
      <td>${esc(r.colo || '-')}</td></tr>`).join('')}</tbody>`;
}

// ============================================================
// 页面：定时任务
// ============================================================
const SPEC_PRESETS = [
  { v: '@every 30m', t: '每 30 分钟' },
  { v: '@every 1h', t: '每小时' },
  { v: '@every 6h', t: '每 6 小时' },
  { v: '0 */6 * * *', t: '每 6 小时整点 (0/6/12/18:00)' },
  { v: '0 3 * * *', t: '每天 03:00' },
  { v: '0 4 * * 1', t: '每周一 04:00' },
];

async function pageSchedules(main) {
  const [data, profiles, sources] = await Promise.all([
    api('GET', '/api/v1/schedules'),
    api('GET', '/api/v1/profiles'),
    api('GET', '/api/v1/ipsources'),
  ]);
  const schedules = data.schedules || [];
  const nextRuns = data.next_runs || {};
  main.innerHTML = topbar('定时任务', '按计划自动测速并发布。所有任务共用一个串行队列，一次只跑一个，其余排队等待。',
    `<button class="btn btn-primary" id="add-sch">+ 新建定时任务</button>`) + `
    <div class="card mb0">${schedules.length === 0 ? emptyHTML('暂无定时任务', '点击右上角新建，例如「每 6 小时优选一次」') : `
      <div class="table-wrap"><table>
        <thead><tr><th>名称</th><th>计划</th><th>下次运行</th><th>Profile</th><th>发布</th><th>最近结果</th><th>状态</th><th>操作</th></tr></thead>
        <tbody>${schedules.map(s => rowSchedule(s, nextRuns[s.id])).join('')}</tbody>
      </table></div>`}</div>`;
  $('#add-sch').onclick = () => editSchedule(null, profiles, sources);
  $$('[data-sc-edit]').forEach(b => b.onclick = () => editSchedule(schedules.find(x => x.id === b.dataset.scEdit), profiles, sources));
  $$('[data-sc-run]').forEach(b => b.onclick = async () => { try { await api('POST', `/api/v1/schedules/${b.dataset.scRun}/run`); toast('已触发（进入队列）', 'ok'); renderPage(); } catch (e) { toast(e.message, 'warn'); } });
  $$('[data-sc-del]').forEach(b => b.onclick = async () => { if (confirm('删除该定时任务？')) { try { await api('DELETE', '/api/v1/schedules/' + b.dataset.scDel); toast('已删除', 'ok'); renderPage(); } catch (e) { toast(e.message, 'err'); } } });
  $$('[data-sc-toggle]').forEach(b => b.onclick = async () => { try { await api('POST', `/api/v1/schedules/${b.dataset.scToggle}/toggle`, { enabled: b.dataset.en !== 'true' }); renderPage(); } catch (e) { toast(e.message, 'err'); } });
}

function rowSchedule(s, next) {
  const last = s.last_status ? statusBadge(s.last_status) : '<span class="muted">—</span>';
  return `<tr>
    <td><b>${esc(s.name)}</b>${s.note ? `<br><span class="tag">${esc(s.note)}</span>` : ''}</td>
    <td><span class="mono">${esc(s.spec)}</span></td>
    <td>${s.enabled ? (next ? fmtTime(next) : '<span class="muted">计算中…</span>') : '<span class="muted">已停用</span>'}</td>
    <td><span class="tag">${esc(s.profile)}</span></td>
    <td>${s.publish ? '<span class="badge ok">是</span>' : '<span class="badge muted">否</span>'}</td>
    <td>${last}${s.last_run_at ? `<br><span class="muted" style="font-size:11px">${fmtTime(s.last_run_at)}</span>` : ''}</td>
    <td>${s.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge muted">停用</span>'}</td>
    <td><div class="flex">
      <button class="btn btn-sm" data-sc-run="${s.id}">立即运行</button>
      <button class="btn btn-sm" data-sc-edit="${s.id}">编辑</button>
      <button class="btn btn-sm" data-sc-toggle="${s.id}" data-en="${s.enabled}">${s.enabled ? '停用' : '启用'}</button>
      <button class="btn btn-sm btn-danger" data-sc-del="${s.id}">删除</button>
    </div></td></tr>`;
}

function editSchedule(sc, profiles, sources) {
  const isNew = !sc;
  const c = Object.assign({}, ENGINE_DEFAULTS, (sc && sc.config) || {});
  const useText = !!(sc && sc.ip_text);
  modal(isNew ? '新建定时任务' : `编辑定时任务 · ${esc(sc.name)}`, `
    <div class="row">
      <div class="field"><label>任务名称</label><input type="text" id="sc-name" value="${esc(sc ? sc.name : '')}" placeholder="例如：每日优选"></div>
      <div class="field"><label>目标 Profile</label><select id="sc-profile">${profiles.map(p => `<option value="${esc(p.name)}" ${sc && sc.profile === p.name ? 'selected' : ''}>${esc(p.title || p.name)}</option>`).join('')}</select></div>
    </div>
    <div class="field">
      <label>运行计划（cron 表达式 / 描述符）</label>
      <div class="row" style="margin-bottom:8px">
        <select id="sc-preset"><option value="">— 选择预设填入 —</option>${SPEC_PRESETS.map(p => `<option value="${p.v}">${p.t} (${p.v})</option>`).join('')}</select>
      </div>
      <input type="text" id="sc-spec" value="${esc(sc ? sc.spec : '@every 6h')}" placeholder="如 @every 6h 或 0 */6 * * *">
      <div class="desc">支持标准 5 段 cron（分 时 日 月 周）或描述符 @every 30m / @hourly / @daily。</div>
    </div>
    <div class="field">
      <label>IP 来源</label>
      <div class="row" style="margin-bottom:8px">
        <label class="radio"><input type="radio" name="sc-ipmode" value="file" ${useText ? '' : 'checked'}>选择 IP 源文件</label>
        <label class="radio"><input type="radio" name="sc-ipmode" value="text" ${useText ? 'checked' : ''}>手动输入 IP 段</label>
      </div>
      <select id="sc-ipsource" style="${useText ? 'display:none' : ''}">${sources.map(s => `<option value="${esc(s.name)}" ${sc && sc.ip_source === s.name ? 'selected' : ''}>${esc(s.name)} (${s.lines} 段)</option>`).join('')}</select>
      <textarea id="sc-iptext" style="${useText ? '' : 'display:none'}" placeholder="1.1.1.1, 104.16.0.0/24">${esc(sc ? sc.ip_text : '')}</textarea>
    </div>
    <div class="row">
      <div class="field"><label>测速模式</label><select id="sc-httping"><option value="false" ${c.httping ? '' : 'selected'}>TCPing</option><option value="true" ${c.httping ? 'selected' : ''}>HTTPing</option></select></div>
      <div class="field"><label>测速端口</label><input type="number" id="sc-tcp_port" value="${c.tcp_port}"></div>
    </div>
    <details><summary style="cursor:pointer;color:var(--text-2);margin:6px 0 12px">⚙️ 高级参数</summary>
      <div class="row">
        <div class="field"><label>延迟线程 -n</label><input type="number" id="sc-routines" value="${c.routines}"></div>
        <div class="field"><label>延迟次数 -t</label><input type="number" id="sc-ping_times" value="${c.ping_times}"></div>
      </div>
      <div class="row">
        <div class="field"><label>下载数量 -dn</label><input type="number" id="sc-test_count" value="${c.test_count}"></div>
        <div class="field"><label>下载时间(秒) -dt</label><input type="number" id="sc-download_time" value="${c.download_time}"></div>
      </div>
      <div class="field"><label>测速地址 -url</label>
        <div style="margin-bottom:8px">${urlPresetSelect('sc-url')}</div>
        <input type="text" id="sc-url" value="${esc(c.url)}">
      </div>
      <div class="row">
        <div class="field"><label>速度下限 MB/s -sl</label><input type="number" step="0.1" id="sc-min_speed" value="${c.min_speed}"></div>
        <div class="field"><label>延迟上限 ms -tl</label><input type="number" id="sc-max_delay" value="${c.max_delay}"></div>
      </div>
      <div class="row">
        <div class="field"><label>丢包率上限 -tlr</label><input type="number" step="0.01" id="sc-max_loss_rate" value="${c.max_loss_rate}"></div>
        <div class="field"><label>匹配地区 -cfcolo</label><input type="text" id="sc-httping_cf_colo" value="${esc(c.httping_cf_colo)}"></div>
      </div>
      <div class="row">
        <label class="switch"><input type="checkbox" id="sc-disable" ${c.disable ? 'checked' : ''}><span class="track"></span>禁用下载测速</label>
        <label class="switch"><input type="checkbox" id="sc-test_all" ${c.test_all ? 'checked' : ''}><span class="track"></span>测速全部 IP</label>
      </div>
      <div class="row">
        <label class="switch"><input type="checkbox" id="sc-debug" ${c.debug ? 'checked' : ''}><span class="track"></span>调试输出 -debug</label>
      </div>
    </details>
    <div class="row">
      <label class="switch"><input type="checkbox" id="sc-publish" ${sc ? (sc.publish ? 'checked' : '') : 'checked'}><span class="track"></span>完成后发布到 Profile</label>
      <label class="switch"><input type="checkbox" id="sc-enabled" ${sc ? (sc.enabled ? 'checked' : '') : 'checked'}><span class="track"></span>启用</label>
    </div>
    <div class="field"><label>备注</label><input type="text" id="sc-note" value="${esc(sc ? sc.note : '')}"></div>
    <button class="btn btn-primary" id="sc-save">保存</button>`);

  $('#sc-preset').onchange = (e) => { if (e.target.value) $('#sc-spec').value = e.target.value; };
  $$('input[name=sc-ipmode]').forEach(r => r.onchange = () => {
    const file = $('input[name=sc-ipmode]:checked').value === 'file';
    $('#sc-ipsource').style.display = file ? '' : 'none';
    $('#sc-iptext').style.display = file ? 'none' : '';
  });
  $('#sc-save').onclick = async () => {
    const v = (id) => $('#' + id).value;
    const num = (id) => Number(v(id));
    const body = {
      name: v('sc-name').trim(), spec: v('sc-spec').trim(), profile: v('sc-profile'),
      note: v('sc-note'), publish: $('#sc-publish').checked, enabled: $('#sc-enabled').checked,
      config: {
        routines: num('sc-routines'), ping_times: num('sc-ping_times'), tcp_port: num('sc-tcp_port'),
        httping: v('sc-httping') === 'true', httping_cf_colo: v('sc-httping_cf_colo') || '', httping_status_code: 0,
        test_count: num('sc-test_count'), download_time: num('sc-download_time'), url: v('sc-url') || '',
        min_speed: num('sc-min_speed'), disable: $('#sc-disable').checked,
        max_delay: num('sc-max_delay'), min_delay: 0, max_loss_rate: num('sc-max_loss_rate'),
        test_all: $('#sc-test_all').checked, debug: $('#sc-debug').checked,
      },
    };
    if ($('input[name=sc-ipmode]:checked').value === 'text') { body.ip_text = $('#sc-iptext').value.trim(); body.ip_source = ''; }
    else { body.ip_source = $('#sc-ipsource').value; body.ip_text = ''; }
    if (!body.name) return toast('请输入任务名称', 'warn');
    if (!body.spec) return toast('请输入运行计划', 'warn');
    if (body.ip_text === '' && body.ip_source === '') return toast('请选择 IP 源或输入 IP 段', 'warn');
    try {
      if (isNew) await api('POST', '/api/v1/schedules', body);
      else await api('PUT', '/api/v1/schedules/' + sc.id, body);
      toast('已保存', 'ok'); closeModal(); renderPage();
    } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：历史记录
// ============================================================
async function pageHistory(main) {
  const runs = await api('GET', '/api/v1/runs');
  main.innerHTML = topbar('历史记录', `共 ${runs.length} 条测速记录`) + `
    <div class="card mb0">${runs.length === 0 ? emptyHTML('暂无历史记录', '去「测速优选」开始第一次优选') : `
      <div class="table-wrap"><table>
        <thead><tr><th>时间</th><th>Profile</th><th>版本</th><th>状态</th><th>命中</th><th>最优 IP</th><th>最优速度</th><th>操作</th></tr></thead>
        <tbody>${runs.map(rowRun).join('')}</tbody>
      </table></div>`}</div>`;
  $$('[data-act]').forEach(b => b.onclick = () => runAction(b.dataset.act, b.dataset.id));
}

function statusBadge(s) {
  const m = { finished: ['ok', '完成'], failed: ['err', '失败'], stopped: ['warn', '中止'] };
  const [k, t] = m[s] || ['muted', s];
  return `<span class="badge ${k}">${t}</span>`;
}

function rowRun(r) {
  return `<tr>
    <td>${fmtTime(r.created_at)}${r.note ? `<br><span class="tag">${esc(r.note)}</span>` : ''}</td>
    <td>${esc(r.profile || '-')}</td>
    <td>${esc(r.ip_version || '-')}</td>
    <td>${statusBadge(r.status)}</td>
    <td>${r.count}</td>
    <td class="mono">${esc(r.best_ip || '-')}</td>
    <td>${r.best_mbps ? fmtNum(r.best_mbps) + ' MB/s' : '-'}</td>
    <td><div class="flex">
      <button class="btn btn-sm" data-act="view" data-id="${r.id}">查看</button>
      <button class="btn btn-sm" data-act="publish" data-id="${r.id}">发布</button>
      <button class="btn btn-sm" data-act="export" data-id="${r.id}">导出</button>
      <button class="btn btn-sm btn-danger" data-act="del" data-id="${r.id}">删除</button>
    </div></td></tr>`;
}

async function runAction(act, id) {
  if (act === 'del') {
    if (!confirm('确定删除该测速记录？')) return;
    try { await api('DELETE', '/api/v1/runs/' + id); toast('已删除', 'ok'); renderPage(); } catch (e) { toast(e.message, 'err'); }
  } else if (act === 'view') {
    try { const run = await api('GET', '/api/v1/runs/' + id); showRunModal(run); } catch (e) { toast(e.message, 'err'); }
  } else if (act === 'export') {
    showExportModal(id);
  } else if (act === 'publish') {
    showPublishModal(id);
  }
}

function showRunModal(run) {
  const rows = run.results || [];
  modal(`测速详情 · ${esc(run.id)}`, `
    <div class="grid grid-4" style="margin-bottom:16px">
      <div class="kpi"><span class="v">${rows.length}</span><span class="l">命中 IP 数</span></div>
      <div class="kpi"><span class="v">${esc(run.ip_version || '-')}</span><span class="l">IP 版本</span></div>
      <div class="kpi"><span class="v">${esc(run.profile || '-')}</span><span class="l">Profile</span></div>
      <div class="kpi"><span class="v">${statusBadge(run.status)}</span><span class="l">状态</span></div>
    </div>
    ${run.error ? `<div class="badge err" style="margin-bottom:12px">错误：${esc(run.error)}</div>` : ''}
    <div class="table-wrap" style="max-height:50vh;overflow:auto"><table>${resultTableHTML(rows.slice(0, 200))}</table></div>
    ${rows.length > 200 ? `<div class="muted" style="margin-top:8px">仅展示前 200 条，完整结果请导出。</div>` : ''}`);
}

function showExportModal(id) {
  const base = `/api/v1/runs/${id}/export`;
  const tk = encodeURIComponent(State.token);
  modal('导出结果', `
    <p class="muted">选择导出格式（含管理令牌的下载链接）：</p>
    <div class="flex" style="flex-wrap:wrap">
      <a class="btn" href="${base}?format=csv&access_token=${tk}" target="_blank">⬇ CSV（同 CFST result.csv）</a>
      <a class="btn" href="${base}?format=txt&access_token=${tk}" target="_blank">⬇ TXT（纯 IP 列表）</a>
      <a class="btn" href="${base}?format=json&access_token=${tk}" target="_blank">⬇ JSON</a>
    </div>`);
}

async function showPublishModal(id) {
  const profiles = await api('GET', '/api/v1/profiles');
  modal('发布到 Profile', `
    <p class="muted">把该测速结果发布为对外 API 的优选数据源（按 IP 版本写入对应槽位）。</p>
    <div class="field"><label>目标 Profile</label><select id="pub-profile">${profiles.map(p => `<option value="${esc(p.name)}">${esc(p.title || p.name)}</option>`).join('')}</select></div>
    <div class="field"><label>IP 版本</label><select id="pub-ver"><option value="">自动（按记录检测）</option><option value="v4">v4</option><option value="v6">v6</option><option value="mixed">v4+v6</option></select></div>
    <button class="btn btn-primary" id="pub-go">确认发布</button>`);
  $('#pub-go').onclick = async () => {
    try {
      await api('POST', `/api/v1/runs/${id}/publish`, { profile: $('#pub-profile').value, ip_version: $('#pub-ver').value });
      toast('已发布', 'ok'); closeModal();
    } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：CDN Profile
// ============================================================
async function pageProfiles(main) {
  const [profiles, runs] = await Promise.all([api('GET', '/api/v1/profiles'), api('GET', '/api/v1/runs')]);
  main.innerHTML = topbar('CDN Profile', '每个 Profile 对应一个对外 API 端点（get_&lt;name&gt;_ip），可指定其发布的优选数据') + `
    <div class="card mb0"><div class="table-wrap"><table>
      <thead><tr><th>名称</th><th>标题</th><th>已发布 v4</th><th>已发布 v6</th><th>对外端点</th><th>操作</th></tr></thead>
      <tbody>${profiles.map(p => `<tr>
        <td><span class="tag">${esc(p.name)}</span></td>
        <td>${esc(p.title || '-')}</td>
        <td class="mono">${esc(p.published_v4_run_id || '—')}</td>
        <td class="mono">${esc(p.published_v6_run_id || '—')}</td>
        <td class="mono">/api/cf2dns/get_${esc(p.name)}_ip</td>
        <td><button class="btn btn-sm" data-edit="${esc(p.name)}">编辑</button></td></tr>`).join('')}</tbody>
    </table></div></div>`;
  $$('[data-edit]').forEach(b => b.onclick = () => editProfile(b.dataset.edit, profiles, runs));
}

function editProfile(name, profiles, runs) {
  const p = profiles.find(x => x.name === name) || { name };
  const opt = (sel) => `<option value="">— 不发布 —</option>` + runs.map(r => `<option value="${r.id}" ${sel === r.id ? 'selected' : ''}>${fmtTime(r.created_at)} · ${r.ip_version} · ${r.count}IP · ${esc(r.best_ip || '')}</option>`).join('');
  modal(`编辑 Profile · ${esc(name)}`, `
    <div class="field"><label>标题</label><input type="text" id="pf-title" value="${esc(p.title || '')}"></div>
    <div class="row">
      <div class="field"><label>发布的 v4 记录</label><select id="pf-v4">${opt(p.published_v4_run_id)}</select></div>
      <div class="field"><label>发布的 v6 记录</label><select id="pf-v6">${opt(p.published_v6_run_id)}</select></div>
    </div>
    <div class="desc muted" style="margin-bottom:14px">提示：三网线路（CM/CU/CT）默认统一使用上面发布的记录。单一探测点无法分别测量三网延迟，如有多探测点数据可在 API 中按线路绑定不同记录（高级用法）。</div>
    <button class="btn btn-primary" id="pf-save">保存</button>`);
  $('#pf-save').onclick = async () => {
    try {
      await api('PUT', '/api/v1/profiles/' + name, {
        name, title: $('#pf-title').value,
        published_v4_run_id: $('#pf-v4').value, published_v6_run_id: $('#pf-v6').value,
      });
      toast('已保存', 'ok'); closeModal(); renderPage();
    } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：授权密钥
// ============================================================
async function pageLicenses(main) {
  const lics = await api('GET', '/api/v1/licenses');
  main.innerHTML = topbar('授权密钥', '对外 API 的访问密钥（客户端通过 ?key= 调用）。开放模式下任意 key 均可访问。',
    `<button class="btn btn-primary" id="add-lic">+ 新建密钥</button>`) + `
    <div class="card mb0">${lics.length === 0 ? emptyHTML('暂无密钥', '点击右上角新建，或在「设置」中开启公开模式') : `
      <div class="table-wrap"><table>
        <thead><tr><th>密钥</th><th>备注</th><th>状态</th><th>余额</th><th>创建时间</th><th>操作</th></tr></thead>
        <tbody>${lics.map(l => `<tr>
          <td class="mono">${esc(l.key)} <button class="btn btn-sm btn-ghost" data-copy="${esc(l.key)}">复制</button></td>
          <td>${esc(l.note || '-')}</td>
          <td>${l.enabled ? '<span class="badge ok">启用</span>' : '<span class="badge muted">停用</span>'}</td>
          <td>${l.count}</td>
          <td>${fmtTime(l.created_at)}</td>
          <td><div class="flex">
            <button class="btn btn-sm" data-toggle="${esc(l.key)}" data-en="${l.enabled}">${l.enabled ? '停用' : '启用'}</button>
            <button class="btn btn-sm btn-danger" data-del="${esc(l.key)}">删除</button>
          </div></td></tr>`).join('')}</tbody>
      </table></div>`}</div>`;
  $('#add-lic').onclick = addLicense;
  $$('[data-copy]').forEach(b => b.onclick = () => copy(b.dataset.copy));
  $$('[data-del]').forEach(b => b.onclick = async () => { if (confirm('删除该密钥？')) { try { await api('DELETE', '/api/v1/licenses/' + encodeURIComponent(b.dataset.del)); toast('已删除', 'ok'); renderPage(); } catch (e) { toast(e.message, 'err'); } } });
  $$('[data-toggle]').forEach(b => b.onclick = async () => {
    try { await api('PUT', '/api/v1/licenses/' + encodeURIComponent(b.dataset.toggle), { note: '', enabled: b.dataset.en !== 'true', count: 0 }); renderPage(); } catch (e) { toast(e.message, 'err'); }
  });
}

function addLicense() {
  modal('新建授权密钥', `
    <div class="field"><label>密钥（留空自动生成）</label><input type="text" id="lic-key" placeholder="留空将自动生成"></div>
    <div class="field"><label>备注</label><input type="text" id="lic-note" placeholder="例如：给某客户端"></div>
    <div class="field"><label>余额 count（默认 99999999）</label><input type="number" id="lic-count" placeholder="99999999"></div>
    <button class="btn btn-primary" id="lic-go">创建</button>`);
  $('#lic-go').onclick = async () => {
    try {
      const c = Number($('#lic-count').value) || 0;
      const lic = await api('POST', '/api/v1/licenses', { key: $('#lic-key').value.trim(), note: $('#lic-note').value, count: c });
      toast('已创建：' + lic.key, 'ok'); closeModal(); renderPage();
    } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：IP 源
// ============================================================
async function pageIPSources(main) {
  const list = await api('GET', '/api/v1/ipsources');
  main.innerHTML = topbar('IP 源管理', '待测速的 IP 段数据文件。内置 ip.txt / ipv6.txt 来自 Cloudflare 官方 IP 段。',
    `<button class="btn btn-primary" id="add-src">+ 新建 IP 源</button>`) + `
    <div class="card mb0"><div class="table-wrap"><table>
      <thead><tr><th>文件名</th><th>IP 段数</th><th>大小</th><th>类型</th><th>操作</th></tr></thead>
      <tbody>${list.map(s => `<tr>
        <td class="mono">${esc(s.name)}</td><td>${s.lines}</td><td>${(s.size / 1024).toFixed(1)} KB</td>
        <td>${s.builtin ? '<span class="badge info">内置</span>' : '<span class="badge muted">自定义</span>'}</td>
        <td><div class="flex">
          <button class="btn btn-sm" data-edit="${esc(s.name)}">编辑</button>
          ${s.builtin ? '' : `<button class="btn btn-sm btn-danger" data-del="${esc(s.name)}">删除</button>`}
        </div></td></tr>`).join('')}</tbody>
    </table></div></div>`;
  $('#add-src').onclick = () => editSource('', '');
  $$('[data-edit]').forEach(b => b.onclick = async () => { const d = await api('GET', '/api/v1/ipsources/' + encodeURIComponent(b.dataset.edit)); editSource(d.name, d.content); });
  $$('[data-del]').forEach(b => b.onclick = async () => { if (confirm('删除该 IP 源？')) { try { await api('DELETE', '/api/v1/ipsources/' + encodeURIComponent(b.dataset.del)); toast('已删除', 'ok'); renderPage(); } catch (e) { toast(e.message, 'err'); } } });
}

function editSource(name, content) {
  modal(name ? `编辑 IP 源 · ${esc(name)}` : '新建 IP 源', `
    <div class="field"><label>文件名</label><input type="text" id="src-name" value="${esc(name)}" ${name ? 'readonly' : ''} placeholder="例如：my-ip.txt"></div>
    <div class="field"><label>内容（每行一个 IP 或 IP 段）</label><textarea id="src-content" style="min-height:300px">${esc(content)}</textarea></div>
    <button class="btn btn-primary" id="src-save">保存</button>`);
  $('#src-save').onclick = async () => {
    const n = $('#src-name').value.trim();
    if (!n) return toast('请输入文件名', 'warn');
    try { await api('PUT', '/api/v1/ipsources/' + encodeURIComponent(n), { content: $('#src-content').value }); toast('已保存', 'ok'); closeModal(); renderPage(); } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：设置
// ============================================================
async function pageSettings(main) {
  const s = await api('GET', '/api/v1/settings');
  main.innerHTML = topbar('设置', '对外 API 与测速默认行为') + `
    <div class="card">
      <div class="card-title">📡 对外 API</div>
      <div class="field"><label class="switch"><input type="checkbox" id="s-open" ${s.public_open ? 'checked' : ''}><span class="track"></span> 公开模式（接受任意 key，便于无缝迁移）</label>
        <div class="desc">开启后，任何携带 key 的请求都可访问对外优选 IP API，无需预先在「授权密钥」中登记。</div></div>
      <div class="field"><label class="switch"><input type="checkbox" id="s-auto" ${s.auto_publish ? 'checked' : ''}><span class="track"></span> 自动发布（测速完成即更新对应 Profile）</label></div>
      <div class="field"><label>对外 API 单次返回 IP 数上限</label><input type="number" id="s-max" value="${s.public_result_max || 10}" style="max-width:200px"></div>
      <button class="btn btn-primary" id="s-save">保存设置</button>
    </div>`;
  $('#s-save').onclick = async () => {
    try {
      await api('PUT', '/api/v1/settings', {
        public_open: $('#s-open').checked, auto_publish: $('#s-auto').checked,
        public_result_max: Number($('#s-max').value) || 10, default_config: s.default_config || {},
      });
      toast('设置已保存', 'ok');
    } catch (e) { toast(e.message, 'err'); }
  };
}

// ============================================================
// 页面：API 文档
// ============================================================
async function pageApiDocs(main) {
  const origin = location.origin;
  const block = (title, code) => `<div class="card"><div class="card-title">${title}</div><div class="code-block">${esc(code)}<button class="btn btn-sm copy-btn" data-copy="${esc(code)}">复制</button></div></div>`;
  main.innerHTML = topbar('对外 API 文档', '本服务的对外接口与 wetest.vip / hostmonit 逐字段兼容，现有客户端可零改动迁移') + `
    <div class="card">
      <div class="card-title">🔗 无缝迁移说明</div>
      <p class="muted mb0">把原本指向 <code>www.wetest.vip</code> 或 <code>api.hostmonit.com</code> 的请求改指向本服务地址 <code>${esc(origin)}</code> 即可。响应结构（<code>status/code/msg/info</code> 与三网 <code>CM/CU/CT</code> 分类）完全一致。要让旧客户端的已有 key 直接可用，请在「设置」中开启<b>公开模式</b>，或在「授权密钥」中登记其 key。</p>
    </div>
    ${block('获取 CloudFlare 优选 IP（GET）', `curl "${origin}/api/cf2dns/get_cloudflare_ip?key=YOUR_KEY&type=v4"`)}
    ${block('获取优选 IP（hostmonit 兼容 · JSON POST）', `curl -X POST "${origin}/get_optimization_ip" \\\n  -H "Content-Type: application/json" \\\n  -d '{"key":"YOUR_KEY","type":"v4"}'`)}
    ${block('获取 License 授权信息', `curl "${origin}/api/cf2dns/get_cloudflare_license?license=YOUR_KEY"`)}
    <div class="card">
      <div class="card-title">📦 成功响应示例（优选 IP）</div>
      <div class="code-block">${esc(JSON.stringify({ status: true, code: 200, msg: '请求成功', info: { CM: [{ ip: '104.16.x.x', colo: 'SJC', latency: 150.5 }], CU: [], CT: [] } }, null, 2))}</div>
    </div>
    <div class="card mb0">
      <div class="card-title">📑 可用端点</div>
      <div class="table-wrap"><table>
        <thead><tr><th>端点</th><th>方法</th><th>参数</th><th>说明</th></tr></thead>
        <tbody>
          <tr><td class="mono">/api/cf2dns/get_cloudflare_ip</td><td>GET/POST</td><td>key, type</td><td>Cloudflare 优选 IP</td></tr>
          <tr><td class="mono">/api/cf2dns/get_cloudfront_ip</td><td>GET/POST</td><td>key, type</td><td>CloudFront 优选 IP</td></tr>
          <tr><td class="mono">/api/cf2dns/get_edgeone_ip</td><td>GET/POST</td><td>key, type</td><td>EdgeOne 优选 IP</td></tr>
          <tr><td class="mono">/api/cf2dns/get_cloudflare_license</td><td>GET/POST</td><td>license</td><td>授权信息</td></tr>
          <tr><td class="mono">/get_optimization_ip</td><td>POST</td><td>key, type (JSON)</td><td>hostmonit 兼容</td></tr>
        </tbody>
      </table></div>
    </div>`;
  $$('[data-copy]').forEach(b => b.onclick = () => copy(b.dataset.copy));
}

// ============================================================
// 通用组件
// ============================================================
function emptyHTML(title, sub) { return `<div class="empty"><div class="big">📭</div><div>${esc(title)}</div><div class="muted" style="margin-top:6px">${esc(sub || '')}</div></div>`; }

function modal(title, bodyHTML) {
  closeModal();
  const mask = document.createElement('div');
  mask.className = 'modal-mask'; mask.id = 'modal-mask';
  mask.innerHTML = `<div class="modal"><div class="modal-head"><h3>${title}</h3><span class="x" id="modal-x">×</span></div><div class="modal-body">${bodyHTML}</div></div>`;
  document.body.appendChild(mask);
  $('#modal-x').onclick = closeModal;
  mask.onclick = (e) => { if (e.target === mask) closeModal(); };
  $$('[data-copy]', mask).forEach(b => b.onclick = () => copy(b.dataset.copy));
}
function closeModal() { const m = $('#modal-mask'); if (m) m.remove(); }

// ============================================================
// 启动
// ============================================================
function boot() {
  renderShell();
  connectSSE();
  // 周期性同步状态（兜底，防 SSE 丢失）
  setInterval(async () => {
    if (!State.token) return;
    try { const st = await api('GET', '/api/v1/speedtest/status'); applyStatus(st); if (State.page === 'dashboard') updateLiveUI(); } catch (_) {}
  }, 15000);
}

window.addEventListener('hashchange', () => { const p = location.hash.replace('#', ''); if (p && p !== State.page) { State.page = p; renderPage(); } });

async function init() {
  if (!State.token) { renderLogin(); return; }
  try { const j = await fetch('/api/v1/version', { headers: { 'Authorization': 'Bearer ' + State.token } }); if (!j.ok) throw 0; State.version = (await j.json()).data?.version || 'dev'; boot(); }
  catch (_) { logout(); }
}
init();
})();

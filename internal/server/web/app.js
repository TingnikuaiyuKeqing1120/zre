/* ZRE 前端逻辑：无框架，单文件。所有数据插值都经过 esc()。 */
"use strict";

/* ---------- 小工具 ---------- */
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, c => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;"
  }[c]));
}
const fmtInt = n => (n > 0 ? Number(n).toLocaleString("en-US") : "");
const fmtSize = b => (b < 1024 ? b + " B" : (b / 1024).toFixed(1) + " KB");
const fmtTime = iso => {
  const d = new Date(iso);
  const p = n => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
};

function toast(msg, kind = "info", ms = 3500) {
  const el = document.createElement("div");
  el.className = `toast ${kind}`;
  el.textContent = msg;
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), ms);
}

async function copyText(text, tip = "已复制") {
  try {
    await navigator.clipboard.writeText(text);
    toast(tip, "ok", 1800);
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    document.body.appendChild(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
    toast(tip, "ok", 1800);
  }
}

/* ---------- API ---------- */
async function api(path, body) {
  const opt = body === undefined ? {} : {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  };
  let res;
  try {
    res = await fetch(path, opt);
  } catch {
    throw new Error("无法连接 ZRE 服务");
  }
  let data = null;
  try { data = await res.json(); } catch { /* 非 JSON 响应 */ }
  if (!res.ok || !data || data.ok === false) {
    const err = new Error((data && data.error) || `请求失败 (HTTP ${res.status})`);
    err.conflict = !!(data && data.conflict);
    err.data = data;
    throw err;
  }
  return data;
}

/* ---------- 全局状态 ---------- */
let state = null;
const sel = { providerId: null, modelId: null };
let expandedId = null;
let query = "";

function findProvider(id) {
  return (state && state.providers.find(p => p.id === id)) || null;
}
function findModel(pid, mid) {
  const p = findProvider(pid);
  return p ? (p.models.find(m => m.id === mid) || null) : null;
}

function apply(resp) {
  state = resp.state;
  const p = findProvider(sel.providerId);
  if (!p) {
    sel.providerId = null;
    sel.modelId = null;
  } else if (sel.modelId && !p.models.find(m => m.id === sel.modelId)) {
    sel.modelId = null;
  }
  if (sel.providerId) expandedId = sel.providerId;
  renderAll();
  (resp.warnings || []).forEach(w => toast(w, "warn", 5000));
}

async function refreshState() {
  apply(await api("/api/state"));
}

/* ---------- 渲染 ---------- */
function renderAll() { renderSidebar(); renderMain(); renderStatus(); }

function reasonBadge(m) {
  if (!m.hasReasoning) return `<span class="rb" title="未配置推理">—</span>`;
  if (m.reasoningEnabled) return `<span class="rb on" title="思考已启用，默认档位 ${esc(m.defaultVariant)}">⚙ ${esc(m.defaultVariant || "on")}</span>`;
  return `<span class="rb off" title="思考已关闭">✕ 关</span>`;
}

function renderSidebar() {
  const list = $("#prov-list");
  if (!state.providers.length) {
    list.innerHTML = `<div class="snap-empty">还没有任何提供商<br><br>
      <button class="btn accent" data-action="create-provider">＋ 新增提供商</button></div>`;
    return;
  }
  const q = query.trim().toLowerCase();
  const provs = state.providers.filter(p => {
    if (!q) return true;
    if (p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q)) return true;
    return p.models.some(m => m.id.toLowerCase().includes(q) || (m.name || "").toLowerCase().includes(q));
  });
  if (!provs.length) {
    list.innerHTML = `<div class="snap-empty">没有匹配「${esc(query)}」的结果</div>`;
    return;
  }
  list.innerHTML = provs.map(p => {
    const matchModel = m => !q || m.id.toLowerCase().includes(q) || (m.name || "").toLowerCase().includes(q);
    const provMatch = q && (p.name.toLowerCase().includes(q) || p.id.toLowerCase().includes(q));
    const isOpen = expandedId === p.id || (!!q && !provMatch && p.models.some(matchModel));
    const models = p.models.filter(matchModel);
    const badges =
      (p.id.startsWith("builtin:") ? `<span class="badge gray">内置</span>` : "") +
      `<span class="badge kind">${esc(p.kind || "?")}</span>` +
      (p.enabledSet && !p.enabled ? `<span class="badge red">已禁用</span>` : "");
    return `
    <div class="prov ${isOpen ? "expanded" : ""} ${sel.providerId === p.id ? "selected" : ""}" data-id="${esc(p.id)}">
      <div class="prov-row" data-action="sel-provider" data-id="${esc(p.id)}" title="${esc(p.id)}">
        <span class="chev" data-action="toggle-expand" data-id="${esc(p.id)}">▶</span>
        <span class="p-main">
          <span class="p-name">${esc(p.name || p.id)}</span>
          <span class="p-badges">${badges}</span>
        </span>
        <span class="p-count">${p.models.length}</span>
      </div>
      <div class="models">
        ${models.map(m => `
          <div class="model-row ${sel.providerId === p.id && sel.modelId === m.id ? "selected" : ""}"
               data-action="sel-model" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" title="${esc(m.id)}">
            <span class="m-id">${esc(m.id)}</span>
            ${reasonBadge(m)}
          </div>`).join("")}
        ${models.length === 0 ? `<div class="snap-empty" style="padding:6px 0">无模型</div>` : ""}
      </div>
    </div>`;
  }).join("");
}

/* ---- 主区：提供商视图 ---- */
function kindOptions(current) {
  const base = ["anthropic", "openai-compatible", "openai"];
  const list = !current || base.includes(current) ? base : [current, ...base];
  return list.map(k => `<option value="${esc(k)}" ${k === current ? "selected" : ""}>${esc(k)}${base.includes(k) ? "" : "（自定义）"}</option>`).join("");
}

function providerView(p) {
  const enabledSel = !p.enabledSet ? "unset" : (p.enabled ? "true" : "false");
  return `
  <div class="crumb"><a data-action="home">全部提供商</a><span class="sep">/</span>${esc(p.name || p.id)}</div>
  <div class="title-row">
    <h2>${esc(p.name || p.id)}</h2>
    ${p.id.startsWith("builtin:") ? `<span class="badge gray">内置提供商</span>` : `<span class="badge gray">自定义</span>`}
    <span class="badge kind">${esc(p.kind || "?")}</span>
    ${p.enabledSet && !p.enabled ? `<span class="badge red">已禁用</span>` : ""}
    ${p.systemDisabledReason ? `<span class="badge red" title="${esc(p.systemDisabledReason)}">系统停用: ${esc(p.systemDisabledReason)}</span>` : ""}
    <span class="id-chip" title="点击复制 ID" data-action="copy" data-copy="${esc(p.id)}" style="cursor:pointer">${esc(p.id)}</span>
  </div>

  <div class="card">
    <h3>提供商设置</h3>
    <div class="form-grid">
      <label>名称</label>
      <div class="val"><input data-pfield="name" data-pid="${esc(p.id)}" value="${esc(p.name)}"></div>
      <label>类型 (kind)</label>
      <div class="val"><select data-pfield="kind" data-pid="${esc(p.id)}">${kindOptions(p.kind)}</select></div>
      <label>Base URL</label>
      <div class="val"><input data-pfield="baseURL" data-pid="${esc(p.id)}" value="${esc(p.baseURL)}" placeholder="https://…" class="mono" style="font-size:12px"></div>
      <label>API Key</label>
      <div class="val">
        <input id="apikey-input" type="password" data-pfield="apiKey" data-pid="${esc(p.id)}" value="${esc(p.apiKey)}" class="mono" style="font-size:12px">
        <button class="btn icon" data-action="toggle-key" title="显示 / 隐藏">👁</button>
        <button class="btn icon" data-action="copy" data-copy="${esc(p.apiKey)}" title="复制">⧉</button>
      </div>
      <label>启用状态</label>
      <div class="val">
        <select data-pfield="enabled" data-pid="${esc(p.id)}">
          <option value="unset" ${enabledSel === "unset" ? "selected" : ""}>未设置（默认启用）</option>
          <option value="true" ${enabledSel === "true" ? "selected" : ""}>enabled: true（启用）</option>
          <option value="false" ${enabledSel === "false" ? "selected" : ""}>enabled: false（禁用）</option>
        </select>
      </div>
    </div>
    <div style="margin-top:12px;display:flex;gap:6px;flex-wrap:wrap">
      ${p.apiKeyRequired ? `<span class="badge green">此提供商需要 API Key</span>` : ""}
      ${p.source ? `<span class="badge gray">source: ${esc(p.source)}</span>` : ""}
    </div>
  </div>

  <div class="card">
    <div class="mhead">
      <h3 style="margin:0">模型 <span class="cnt">${p.models.length}</span></h3>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="btn" data-action="model-reorder" data-pid="${esc(p.id)}" ${p.models.length < 2 ? "disabled" : ""} title="调整模型顺序（拖动或 ↑↓）">⇅ 排序</button>
        ${state.catalogReady ? `<button class="btn" data-action="autoreason-all" data-pid="${esc(p.id)}" title="按官方模型目录为每个模型匹配思考档位（覆盖现有 reasoning 配置，思考开关保持不变）
目录来源：${esc((state.catalogSources || []).join("；"))}">🎯 全部目录匹配</button>
        <button class="btn icon" data-action="catalog-rescan" title="重新扫描官方模型目录（zcode 更新目录文件后点击刷新）">⟳</button>` : ""}
        <button class="btn accent" data-action="create-model" data-pid="${esc(p.id)}">＋ 添加模型</button>
        <button class="btn" data-action="fetch-models" data-pid="${esc(p.id)}" title="调用该提供商的 /models 接口拉取可用模型，批量勾选添加（按官方目录自动预填配置）">🔗 从 API 拉取</button>
      </div>
    </div>
    <div class="mtable" style="margin-top:10px">
      ${p.models.map(m => `
        <div class="mrow" data-action="sel-model" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">
          <span class="m-id" title="${esc(m.id)}">${esc(m.id)}</span>
          <span class="m-name">${esc(m.name || "")}</span>
          <span class="m-limits">${m.hasLimit ? `${fmtInt(m.context)} / ${fmtInt(m.output)}` : "—"}</span>
          ${reasonBadge(m)}
          <span class="m-actions">
            <button class="btn" data-action="sel-model" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">编辑</button>
            <button class="btn danger" data-action="del-model" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">删除</button>
          </span>
        </div>`).join("")}
      ${p.models.length === 0 ? `<div class="snap-empty">该提供商还没有模型</div>` : ""}
    </div>
  </div>

  <div class="card danger-card">
    <h3>危险操作</h3>
    <div style="display:flex;gap:8px">
      <button class="btn" data-action="rename-provider-id" data-pid="${esc(p.id)}">重命名提供商 ID</button>
      <button class="btn danger" data-action="del-provider" data-pid="${esc(p.id)}">删除提供商</button>
    </div>
    <div class="field-hint" style="margin-top:10px">删除后需点击「保存」才会写入磁盘；保存前会自动备份。</div>
  </div>`;
}

/* ---- 主区：模型视图 ---- */
function modChips(items, which, pid, mid) {
  return (items || []).map((v, i) => `
    <span class="chip">${esc(v)}
      <button class="x" data-action="chip-mod-del" data-which="${which}" data-idx="${i}"
              data-pid="${esc(pid)}" data-mid="${esc(mid)}" title="移除">✕</button>
    </span>`).join("");
}

function modelView(p, m) {
  const hasR = m.hasReasoning;
  const variants = m.variants || [];
  const QUICK_VARIANTS = ["low", "medium", "high", "xhigh", "max", "off", "enabled", "none"];
  const QUICK_IN = ["text", "image", "video", "audio"];
  const QUICK_OUT = ["text"];

  const variantChips = variants.map((v, i) => `
    <span class="chip ${m.defaultVariant === v ? "def" : ""}">
      <button class="mv" data-action="chip-variant-up" data-idx="${i}" title="上移">↑</button>
      <button class="mv" data-action="chip-variant-down" data-idx="${i}" title="下移">↓</button>
      ${esc(v)}${m.defaultVariant === v ? `<span class="deftag">默认</span>` : ""}
      <button class="x" data-action="chip-variant-del" data-idx="${i}" title="移除">✕</button>
    </span>`).join("");

  return `
  <div class="crumb">
    <a data-action="home">全部提供商</a><span class="sep">/</span>
    <a data-action="back-provider" data-pid="${esc(p.id)}">${esc(p.name || p.id)}</a><span class="sep">/</span>${esc(m.id)}
  </div>
  <div class="title-row">
    <h2 class="mono" style="font-size:16px">${esc(m.id)}</h2>
    ${m.name ? `<span class="badge gray">${esc(m.name)}</span>` : ""}
    ${reasonBadge(m)}
    ${m.priority ? `<span class="badge gray" title="zcode.priority">优先级 ${m.priority}</span>` : ""}
    <span class="id-chip" title="点击复制模型 ID" data-action="copy" data-copy="${esc(m.id)}" style="cursor:pointer">${esc(m.id)}</span>
  </div>

  <div class="card">
    <h3>基本信息</h3>
    <div class="form-grid">
      <label>显示名称</label>
      <div class="val"><input data-mfield="name" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" value="${esc(m.name)}" placeholder="可选，仅用于展示"></div>
    </div>
    <div style="margin-top:12px;display:flex;gap:8px">
      <button class="btn" data-action="rename-model-id" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">重命名模型 ID</button>
      <button class="btn danger" data-action="del-model" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">删除模型</button>
    </div>
  </div>

  <div class="card">
    <h3>思考配置 (reasoning)</h3>
    ${hasR ? `
      <label class="switch">
        <input type="checkbox" id="reasoning-enabled" ${m.reasoningEnabled ? "checked" : ""}>
        <span class="track"></span>
        <span>启用思考</span>
      </label>
      <div class="subcard">
        <div class="form-grid" style="grid-template-columns:110px 1fr">
          <label>思考档位</label>
          <div class="val" style="flex-direction:column;align-items:flex-start;gap:8px">
            <div class="chips-row">${variantChips || `<span class="hint" style="margin:0">暂无档位，点击下方快捷档位添加</span>`}</div>
            <div class="chips-row">
              ${QUICK_VARIANTS.map(v => `<button class="chip-mini" data-action="quick-variant" data-v="${v}" ${variants.includes(v) ? "disabled" : ""}>＋${v}</button>`).join("")}
              <span class="inline-add">
                <input id="variant-input" placeholder="自定义档位…">
                <button class="btn" data-action="add-variant">添加</button>
              </span>
            </div>
          </div>
          <label>默认档位</label>
          <div class="val">
            <select id="default-variant" ${variants.length === 0 ? "disabled" : ""}>
              ${variants.map(v => `<option value="${esc(v)}" ${m.defaultVariant === v ? "selected" : ""}>${esc(v)}</option>`).join("")}
              ${variants.length === 0 ? `<option value="">（先添加档位）</option>` : ""}
            </select>
          </div>
        </div>
        <div class="field-hint">档位写入 reasoning.variants；默认档位写入 reasoning.defaultVariant。注意：配置值只是 UI 声明，实际支持的档位由模型 API 决定。</div>
        <div class="tpl-row">
          <label>档位模板</label>
          <select id="tpl-select">
            ${(state.templates || []).map(tp => `<option value="${esc(tp.name)}">${esc(tp.name)}（${esc((tp.variants || []).join("-"))}，默认 ${esc(tp.defaultVariant || "?")}）</option>`).join("")}
            ${(state.templates || []).length === 0 ? `<option value="">（暂无模板）</option>` : ""}
          </select>
          <button class="btn" data-action="apply-template">应用</button>
          <button class="btn" data-action="save-template">存为模板</button>
          <button class="btn" data-action="manage-templates">管理</button>
        </div>
      </div>
      <div style="margin-top:14px;display:flex;gap:8px;flex-wrap:wrap;align-items:center">
        ${state.catalogReady ? `<button class="btn accent" data-action="autoreason" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" title="按官方模型目录匹配该模型的档位
目录来源：${esc((state.catalogSources || []).join("；"))}">🎯 自动匹配官方档位</button>
        <button class="btn icon" data-action="catalog-rescan" title="重新扫描官方模型目录（zcode 更新目录文件后点击刷新）">⟳</button>` : ""}
        <button class="btn danger" data-action="remove-reasoning" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">移除整个推理配置</button>
      </div>
    ` : `
      <p class="hint" style="margin-top:0">该模型尚未配置推理（reasoning）。添加后可控制思考开关与思考强度档位。</p>
      <div style="display:flex;gap:8px;flex-wrap:wrap">
        <button class="btn accent" data-action="add-reasoning" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}">＋ 添加推理配置</button>
        ${state.catalogReady ? `<button class="btn" data-action="autoreason" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" title="按官方模型目录匹配该模型的档位">🎯 自动匹配官方档位</button>` : ""}
      </div>
    `}
  </div>

  <div class="card">
    <h3>上下文限制 (limit)</h3>
    <div class="form-grid" style="grid-template-columns:110px 1fr 110px 1fr">
      <label>上下文窗口</label>
      <div class="val"><input type="number" min="0" step="1" id="limit-ctx" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" value="${m.hasLimit && m.context > 0 ? m.context : ""}" placeholder="未设置"></div>
      <label>最大输出</label>
      <div class="val"><input type="number" min="0" step="1" id="limit-out" data-pid="${esc(p.id)}" data-mid="${esc(m.id)}" value="${m.hasLimit && m.output > 0 ? m.output : ""}" placeholder="未设置"></div>
    </div>
    <div class="field-hint">两项都清空将移除 limit 配置；修改在失焦后生效（仍需「保存」写入磁盘）。</div>
  </div>

  <div class="card">
    <h3>模态 (modalities)</h3>
    <div class="form-grid" style="grid-template-columns:110px 1fr">
      <label>输入</label>
      <div class="val" style="flex-direction:column;align-items:flex-start;gap:8px">
        <div class="chips-row">${modChips(m.inputModalities, "input", p.id, m.id) || `<span class="hint" style="margin:0">未设置</span>`}</div>
        <div class="chips-row">
          ${QUICK_IN.map(v => `<button class="chip-mini" data-action="quick-mod" data-which="input" data-v="${v}" ${(m.inputModalities || []).includes(v) ? "disabled" : ""}>＋${v}</button>`).join("")}
          <span class="inline-add"><input id="mod-input-input" data-which="input" placeholder="自定义…"><button class="btn" data-action="add-mod" data-which="input">添加</button></span>
        </div>
      </div>
      <label>输出</label>
      <div class="val" style="flex-direction:column;align-items:flex-start;gap:8px">
        <div class="chips-row">${modChips(m.outputModalities, "output", p.id, m.id) || `<span class="hint" style="margin:0">未设置</span>`}</div>
        <div class="chips-row">
          ${QUICK_OUT.map(v => `<button class="chip-mini" data-action="quick-mod" data-which="output" data-v="${v}" ${(m.outputModalities || []).includes(v) ? "disabled" : ""}>＋${v}</button>`).join("")}
          <span class="inline-add"><input id="mod-input-output" data-which="output" placeholder="自定义…"><button class="btn" data-action="add-mod" data-which="output">添加</button></span>
        </div>
      </div>
    </div>
    <div class="field-hint">输入与输出均清空将移除 modalities 配置。</div>
  </div>`;
}

function welcomeView() {
  const s = state.stats;
  return `
  <div class="welcome">
    <h2>ZRE — ZCode 模型配置编辑器</h2>
    <p>从左侧选择一个提供商或模型开始编辑；所有修改先在内存中进行，点「保存」才写入磁盘。</p>
    <div class="tips">
      <ul>
        <li>当前共 <b>${s.providers}</b> 个提供商、<b>${s.models}</b> 个模型${s.disabledProvider ? `（${s.disabledProvider} 个已禁用）` : ""}${s.reasoningModels ? `、${s.reasoningModels} 个启用思考` : ""}。</li>
        <li>保存前会<b>自动备份</b>到 <span class="mono">~/.zcode/v2/backups/</span>。</li>
        <li>「快照」可随时保存 / 恢复完整配置副本（<span class="mono">~/.zcode/v2/snapshots/</span>）。</li>
        <li>「思考档位模板」可把常用档位组合存成模板一键应用；${state.catalogReady ? `官方模型目录已加载 <b>${state.catalogCount}</b> 个带档位模型，可按模型 ID / 系列+版本自动匹配档位。` : "若加载了官方模型目录（models_catalog*.json，可用 --catalog 指定），还能按模型 ID 自动匹配官方档位。"}</li>
        <li>侧栏底部「⇅ 调整顺序」和提供商页的「⇅ 排序」可调整提供商 / 模型的显示顺序。</li>
        <li><b>保存后需重启 zcode 才会生效</b>（zcode 启动时读取配置）。</li>
      </ul>
    </div>
  </div>`;
}

function renderMain() {
  const main = $("#main");
  const p = findProvider(sel.providerId);
  if (!p) { main.innerHTML = welcomeView(); return; }
  if (sel.modelId) {
    const m = findModel(sel.providerId, sel.modelId);
    if (m) { main.innerHTML = modelView(p, m); return; }
  }
  main.innerHTML = providerView(p);
}

function renderStatus() {
  $("#st-path").textContent = state.configPath;
  const s = state.stats;
  $("#st-stats").textContent = `${s.providers} 个提供商 · ${s.models} 个模型 · ${s.reasoningModels} 个启用思考`;
  const dirty = $("#st-dirty");
  const saveBtn = $("#btn-save");
  if (state.dirty) {
    dirty.textContent = "● 有未保存更改";
    dirty.className = "st-item dirty";
    saveBtn.classList.add("dirty-dot");
  } else {
    dirty.textContent = state.lastSavedAt ? `✓ 已保存 ${fmtTime(state.lastSavedAt)}` : "✓ 与磁盘一致";
    dirty.className = "st-item clean";
    saveBtn.classList.remove("dirty-dot");
  }
}

/* ---------- 修改操作 ---------- */
async function mutate(path, body, okTip) {
  try {
    const resp = await api(path, body);
    apply(resp);
    if (okTip) toast(okTip, "ok", 2000);
    return resp;
  } catch (e) {
    if (e.conflict) await conflictDialog(e.message);
    else toast(e.message, "err", 6000);
    throw e;
  }
}

function updateProvider(pid, fields, tip) {
  return mutate("/api/provider/update", { id: pid, ...fields }, tip);
}

async function updateReasoning(p, m, patch = {}) {
  const cur = {
    enabled: m.hasReasoning ? m.reasoningEnabled : true,
    variants: m.hasReasoning ? (m.variants || []) : [],
    defaultVariant: m.defaultVariant || "",
  };
  const next = { ...cur, ...patch };
  await mutate("/api/model/update", {
    providerId: p.id,
    id: m.id,
    update: {
      reasoning: {
        enabled: next.enabled,
        variants: next.variants,
        defaultVariant: next.defaultVariant,
      },
    },
  });
}

function updateLimit(p, m, ctx, out) {
  return mutate("/api/model/update", {
    providerId: p.id,
    id: m.id,
    update: { limit: { context: ctx || 0, output: out || 0 } },
  });
}

function updateModalities(p, m, which, list) {
  const input = which === "input" ? list : (m.inputModalities || []);
  const output = which === "output" ? list : (m.outputModalities || []);
  return mutate("/api/model/update", {
    providerId: p.id,
    id: m.id,
    update: { modalities: { input, output } },
  });
}

/* ---------- 保存 / 重载 ---------- */
async function save(force = false) {
  try {
    const resp = await api("/api/save", { force });
    apply(resp);
    if (resp.extra && resp.extra.noChange) {
      toast("没有需要保存的更改", "info", 2000);
      return;
    }
    let msg = "已保存 ✓ 重启 zcode 后生效";
    if (resp.extra && resp.extra.backupPath) msg += `\n备份: ${resp.extra.backupPath}`;
    toast(msg, "ok", 6000);
  } catch (e) {
    if (e.conflict) { await conflictDialog(e.message); return; }
    toast(e.message, "err", 6000);
  }
}

async function reloadAll() {
  if (state.dirty) {
    const yes = await confirmDialog("重新加载", "有<b>未保存</b>的修改，重新加载将丢弃这些修改。确定继续？", { okLabel: "丢弃并重新加载", danger: true });
    if (!yes) return;
  }
  try {
    apply(await api("/api/reload", {}));
    toast("已从磁盘重新加载", "ok", 2000);
  } catch (e) { toast(e.message, "err"); }
}

/* ---------- 对话框 ---------- */
function openDialog(dlg) { dlg.showModal(); return dlg; }

function choiceDialog(title, html, buttons) {
  return new Promise(resolve => {
    const dlg = $("#dlg-generic");
    const body = $("#dlg-generic-body");
    body.innerHTML = `
      <div class="dlg-head"><h3>${esc(title)}</h3></div>
      <div class="hint" style="font-size:12.5px">${html}</div>
      <div class="dlg-actions">
        ${buttons.map((b, i) => `<button class="btn ${b.danger ? "danger" : ""} ${b.primary ? "primary" : ""}" data-choice="${i}">${esc(b.label)}</button>`).join("")}
      </div>`;
    body.querySelectorAll("[data-choice]").forEach(btn =>
      btn.addEventListener("click", () => {
        dlg.close();
        resolve(buttons[+btn.dataset.choice].value);
      })
    );
    dlg.addEventListener("close", () => resolve(null), { once: true });
    openDialog(dlg);
  });
}

function confirmDialog(title, html, opts) {
  const { okLabel = "确认", danger = false } = opts || {};
  return choiceDialog(title, html, [
    { label: okLabel, value: true, danger, primary: !danger },
    { label: "取消", value: null },
  ]).then(v => v === true);
}

function formDialog(title, rows, okLabel = "确定", extraFooter = "") {
  return new Promise(resolve => {
    const dlg = $("#dlg-generic");
    const body = $("#dlg-generic-body");
    body.innerHTML = `
      <div class="dlg-head"><h3>${esc(title)}</h3><button type="button" class="btn icon" data-cancel>✕</button></div>
      <form class="dlg-form">
        ${rows.map(r => `
          <div class="frow">
            <label>${esc(r.label)}${r.required ? " *" : ""}</label>
            ${r.options
              ? `<select name="${r.key}">${r.options.map(o => `<option value="${esc(o.v)}" ${o.v === r.value ? "selected" : ""}>${esc(o.t)}</option>`).join("")}</select>`
              : `<input name="${r.key}" type="${r.type || "text"}" value="${esc(r.value ?? "")}" placeholder="${esc(r.placeholder || "")}" ${r.required ? "required" : ""} ${r.mono ? 'class="mono"' : ""}>`}
            ${r.hint ? `<span class="hint" style="margin:0">${r.hint}</span>` : ""}
          </div>`).join("")}
        <div class="dlg-actions">
          <button type="button" class="btn" data-cancel>取消</button>
          <button type="submit" class="btn primary">${esc(okLabel)}</button>
        </div>
      </form>${extraFooter || ""}`;
    const form = body.querySelector("form");
    const cancel = () => { dlg.close(); resolve(null); };
    body.querySelectorAll("[data-cancel]").forEach(b => b.addEventListener("click", cancel));
    form.addEventListener("submit", e => {
      e.preventDefault();
      const out = {};
      rows.forEach(r => { out[r.key] = form.elements[r.key].value.trim(); });
      for (const r of rows) {
        if (r.required && !out[r.key]) { toast(`${r.label} 不能为空`, "warn"); return; }
      }
      dlg.close();
      resolve(out);
    });
    dlg.addEventListener("close", () => resolve(null), { once: true });
    openDialog(dlg);
    const first = form.querySelector("input,select");
    if (first) first.focus();
  });
}

async function conflictDialog(msg) {
  const choice = await choiceDialog(
    "检测到外部修改",
    esc(msg) + `<br><br>磁盘上的 config.json 在本工具之外被修改过（可能 zcode 刚写入了新配置）。`,
    [
      { label: "用当前编辑内容覆盖磁盘", value: "overwrite", danger: true },
      { label: "丢弃我的修改，重新加载", value: "reload" },
      { label: "取消", value: "cancel" },
    ]
  );
  if (choice === "overwrite") {
    try {
      const resp = await api("/api/save", { force: true });
      apply(resp);
      toast("已强制保存 ✓ 重启 zcode 后生效", "ok");
    } catch (e) { toast(e.message, "err"); }
  } else if (choice === "reload") {
    try {
      apply(await api("/api/reload", {}));
      toast("已重新加载磁盘配置", "ok");
    } catch (e) { toast(e.message, "err"); }
  }
}

/* ---------- 快照 ---------- */
async function openSnapshots() {
  try { await refreshState(); } catch {}
  renderSnapList();
  openDialog($("#dlg-snapshots"));
}

function renderSnapList() {
  const list = $("#snap-list");
  const snaps = (state && state.snapshots) || [];
  if (!snaps.length) {
    list.innerHTML = `<div class="snap-empty">暂无快照</div>`;
    return;
  }
  list.innerHTML = snaps.map(s => `
    <div class="snap-row">
      <span class="s-name mono">${esc(s.name)}</span>
      <span class="s-meta">${fmtTime(s.modTime)} · ${fmtSize(s.size)}</span>
      <button class="btn" data-action="snap-restore" data-name="${esc(s.name)}">恢复</button>
      <button class="btn danger" data-action="snap-delete" data-name="${esc(s.name)}">删除</button>
    </div>`).join("");
}

async function createSnapshot() {
  const name = $("#snap-name").value.trim();
  if (!name) { toast("请输入快照名称", "warn"); return; }
  const doSave = async allowOverwrite => {
    await api("/api/snapshot/save", { name, allowOverwrite });
    toast(`快照「${name}」已保存`, "ok");
    $("#snap-name").value = "";
    await refreshState();
    renderSnapList();
  };
  try {
    await doSave(false);
  } catch (e) {
    if (await confirmDialog("覆盖快照", `快照「<b>${esc(name)}</b>」已存在，覆盖它？`, { okLabel: "覆盖" })) {
      try { await doSave(true); } catch (e2) { toast(e2.message, "err"); }
    }
  }
}

async function restoreSnapshot(name) {
  const yes = await confirmDialog(
    "恢复快照",
    `将用快照「<b>${esc(name)}</b>」覆盖当前 <span class="mono">config.json</span>。<br>恢复前会自动备份当前配置；未保存的编辑将丢失。`,
    { okLabel: "恢复", danger: true }
  );
  if (!yes) return;
  try {
    const resp = await api("/api/snapshot/restore", { name });
    apply(resp);
    const bp = resp.extra && resp.extra.backupPath;
    toast(`已恢复快照「${name}」${bp ? `\n恢复前备份: ${bp}` : ""}\n重启 zcode 后生效`, "ok", 6000);
  } catch (e) { toast(e.message, "err"); }
}

/* ---------- JSON 查看 ---------- */
function hlJSON(text) {
  const e = String(text).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return e.replace(/("(?:\\.|[^"\\])*")(\s*:)?|\b(true|false|null)\b|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
    (m, str, colon, kw, num) => {
      if (str) return colon ? `<span class="j-key">${str}</span>${colon}` : `<span class="j-str">${str}</span>`;
      if (kw) return `<span class="j-kw">${kw}</span>`;
      if (num) return `<span class="j-num">${num}</span>`;
      return m;
    });
}

async function openRaw() {
  try {
    const resp = await api("/api/raw");
    $("#raw-view").innerHTML = hlJSON(resp.extra.text);
    openDialog($("#dlg-raw"));
  } catch (e) { toast(e.message, "err"); }
}

/* ---------- 事件委托：点击 ---------- */
document.addEventListener("click", async e => {
  const fetchLink = e.target.closest("[data-fetch-link]");
  if (fetchLink) {
    $("#dlg-generic").close();
    fetchModelsDialog(fetchLink.dataset.fetchLink);
    return;
  }
  const t = e.target.closest("[data-action]");
  if (!t) return;
  const act = t.dataset.action;
  const pid = t.dataset.pid || t.dataset.id;
  const mid = t.dataset.mid;

  try {
    switch (act) {
      case "sel-provider":
        // 再次点击已展开的提供商 → 折叠；否则选中并展开
        if (sel.providerId === t.dataset.id && expandedId === t.dataset.id) {
          expandedId = null;
        } else {
          expandedId = t.dataset.id;
        }
        sel.providerId = t.dataset.id;
        sel.modelId = null;
        renderAll();
        break;
      case "toggle-expand":
        expandedId = expandedId === t.dataset.id ? null : t.dataset.id;
        renderSidebar();
        break;
      case "sel-model":
        sel.providerId = t.dataset.pid;
        sel.modelId = t.dataset.mid;
        expandedId = t.dataset.pid;
        renderAll();
        break;
      case "back-provider":
        sel.modelId = null;
        renderAll();
        break;
      case "home":
        sel.providerId = null;
        sel.modelId = null;
        expandedId = null;
        renderAll();
        break;
      case "create-provider":
        await addProviderDialog();
        break;
      case "create-model":
        await addModelDialog(t.dataset.pid);
        break;
      case "fetch-models":
        fetchModelsDialog(t.dataset.pid);
        break;
      case "model-reorder":
        openModelReorder(t.dataset.pid);
        break;
      case "rename-provider-id":
        await renameProviderDialog(t.dataset.pid);
        break;
      case "rename-model-id":
        await renameModelDialog(t.dataset.pid, t.dataset.mid);
        break;
      case "del-provider":
        await delProvider(t.dataset.pid);
        break;
      case "del-model":
        await delModel(t.dataset.pid, t.dataset.mid);
        break;
      case "add-reasoning": {
        const p = findProvider(pid), m = findModel(pid, mid);
        if (p && m) await updateReasoning(p, m, { enabled: true, variants: ["low", "medium", "high"], defaultVariant: "high" });
        break;
      }
      case "remove-reasoning": {
        const p = findProvider(pid), m = findModel(pid, mid);
        if (!p || !m) break;
        if (await confirmDialog("移除推理配置", `将删除模型 <b>${esc(m.id)}</b> 的整个 reasoning 配置块。`, { okLabel: "移除", danger: true })) {
          await mutate("/api/model/update", { providerId: p.id, id: m.id, update: { removeReasoning: true } });
        }
        break;
      }
      case "apply-template": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        const name = $("#tpl-select") ? $("#tpl-select").value : "";
        const tpl = (state.templates || []).find(x => x.name === name);
        if (!p || !m || !tpl) break;
        await updateReasoning(p, m, { variants: tpl.variants || [], defaultVariant: tpl.defaultVariant || "" });
        toast(`已应用模板「${tpl.name}」`, "ok", 2200);
        break;
      }
      case "save-template": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const variants = m.variants || [];
        if (!variants.length) { toast("当前没有档位，先添加档位再保存为模板", "warn"); break; }
        const def = variants.includes(m.defaultVariant) ? m.defaultVariant : variants[0];
        const suggested = `${variants.join("-")}，默认${def}`;
        const vals = await formDialog("保存为思考档位模板", [
          { key: "name", label: "模板名称", required: true, value: suggested, hint: "下次可在「档位模板」里一键应用" },
        ], "保存");
        if (!vals) break;
        const doSave = ow => api("/api/template/save", {
          name: vals.name, variants, defaultVariant: def, allowOverwrite: ow,
        });
        try {
          await doSave(false);
          await refreshState();
          toast(`模板「${vals.name}」已保存`, "ok");
        } catch (err) {
          if (await confirmDialog("覆盖模板", `模板「<b>${esc(vals.name)}</b>」已存在，覆盖？`, { okLabel: "覆盖" })) {
            try { await doSave(true); await refreshState(); toast(`模板「${vals.name}」已覆盖`, "ok"); }
            catch (e2) { toast(e2.message, "err"); }
          }
        }
        break;
      }
      case "manage-templates":
        openSettings("tpl");
        break;
      case "tpl-delete": {
        if (await confirmDialog("删除模板", `删除模板「<b>${esc(t.dataset.name)}</b>」？`, { okLabel: "删除", danger: true })) {
          try {
            apply(await api("/api/template/delete", { name: t.dataset.name }));
            toast("模板已删除", "ok", 1800);
          } catch (e2) { toast(e2.message, "err"); }
        }
        break;
      }
      case "autoreason": {
        const apid = t.dataset.pid || sel.providerId, amid = t.dataset.mid || sel.modelId;
        if (!apid || !amid) break;
        try {
          const resp = await api("/api/model/autoreason", { providerId: apid, id: amid });
          apply(resp);
          if (resp.extra && resp.extra.matched) {
            const mm = findModel(apid, amid);
            toast(`已按官方目录匹配「${resp.extra.source}」${resp.extra.fuzzy ? "（模糊匹配，请核对）" : ""}\n档位：${(mm ? mm.variants : []).join(" / ")}，默认 ${mm ? mm.defaultVariant : ""}`, "ok", 6000);
          } else {
            toast("官方目录中未找到该模型的匹配项，档位未修改", "warn", 5000);
          }
        } catch (e2) { toast(e2.message, "err"); }
        break;
      }
      case "catalog-rescan": {
        try {
          const resp = await api("/api/catalog/rescan", {});
          apply(resp);
          toast(`官方目录已刷新：${resp.extra.count} 个带档位的模型`, "ok");
        } catch (e2) { toast(e2.message, "err"); }
        break;
      }
      case "autoreason-all": {
        const apid = t.dataset.pid;
        if (!apid) break;
        const yes = await confirmDialog("全部模型目录匹配",
          "将按官方模型目录为该提供商的每个模型匹配思考档位并覆盖现有 reasoning 档位配置。<br>思考开关保持不变，未匹配到目录的模型不动。继续？",
          { okLabel: "开始匹配" });
        if (!yes) break;
        try {
          const resp = await api("/api/provider/autoreason-all", { providerId: apid });
          apply(resp);
          const ex = resp.extra || {};
          toast(`匹配完成：${ex.matched || 0}/${ex.total || 0} 个模型${ex.hinted ? `（${ex.hinted} 个为系列/模糊匹配，建议核对）` : ""}`, "ok", 6000);
        } catch (e2) { toast(e2.message, "err"); }
        break;
      }
      case "quick-variant": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (p && m) await updateReasoning(p, m, { variants: [...(m.variants || []), t.dataset.v] });
        break;
      }
      case "add-variant": {
        const input = $("#variant-input");
        const v = input ? input.value.trim() : "";
        if (!v) break;
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (p && m) await updateReasoning(p, m, { variants: [...(m.variants || []), v] });
        break;
      }
      case "chip-variant-del": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const variants = (m.variants || []).filter((_, i) => i !== +t.dataset.idx);
        await updateReasoning(p, m, { variants });
        break;
      }
      case "chip-variant-up":
      case "chip-variant-down": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const i = +t.dataset.idx;
        const j = act === "chip-variant-up" ? i - 1 : i + 1;
        const variants = [...(m.variants || [])];
        if (j < 0 || j >= variants.length) break;
        [variants[i], variants[j]] = [variants[j], variants[i]];
        await updateReasoning(p, m, { variants });
        break;
      }
      case "quick-mod": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const which = t.dataset.which;
        const cur = which === "input" ? (m.inputModalities || []) : (m.outputModalities || []);
        await updateModalities(p, m, which, [...cur, t.dataset.v]);
        break;
      }
      case "add-mod": {
        const which = t.dataset.which;
        const input = $("#mod-input-" + which);
        const v = input ? input.value.trim() : "";
        if (!v) break;
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const cur = which === "input" ? (m.inputModalities || []) : (m.outputModalities || []);
        await updateModalities(p, m, which, [...cur, v]);
        break;
      }
      case "chip-mod-del": {
        const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
        if (!p || !m) break;
        const which = t.dataset.which;
        const cur = which === "input" ? (m.inputModalities || []) : (m.outputModalities || []);
        await updateModalities(p, m, which, cur.filter((_, i) => i !== +t.dataset.idx));
        break;
      }
      case "toggle-key": {
        const inp = $("#apikey-input");
        if (inp) inp.type = inp.type === "password" ? "text" : "password";
        break;
      }
      case "copy":
        await copyText(t.dataset.copy);
        break;
      case "snap-restore":
        await restoreSnapshot(t.dataset.name);
        break;
      case "snap-delete": {
        if (await confirmDialog("删除快照", `删除快照「<b>${esc(t.dataset.name)}</b>」？此操作不可撤销。`, { okLabel: "删除", danger: true })) {
          try {
            apply(await api("/api/snapshot/delete", { name: t.dataset.name }));
            renderSnapList();
            toast("快照已删除", "ok", 2000);
          } catch (e2) { toast(e2.message, "err"); }
        }
        break;
      }
    }
  } catch { /* mutate 内部已提示 */ }
});

/* ---------- 事件委托：字段修改 ---------- */
document.addEventListener("change", async e => {
  const t = e.target;
  try {
    if (t.dataset.pfield) {
      const body = { id: t.dataset.pid };
      if (t.dataset.pfield === "enabled") body.enabled = t.value;
      else body[t.dataset.pfield] = t.value;
      await updateProvider(t.dataset.pid, body);
      return;
    }
    if (t.dataset.mfield === "name") {
      await mutate("/api/model/update", {
        providerId: t.dataset.pid, id: t.dataset.mid, update: { name: t.value },
      });
      return;
    }
    if (t.id === "reasoning-enabled") {
      const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
      if (p && m) await updateReasoning(p, m, { enabled: t.checked });
      return;
    }
    if (t.id === "default-variant") {
      const p = findProvider(sel.providerId), m = findModel(sel.providerId, sel.modelId);
      if (p && m) await updateReasoning(p, m, { defaultVariant: t.value });
      return;
    }
    if (t.id === "limit-ctx" || t.id === "limit-out") {
      const pid = t.dataset.pid, mid = t.dataset.mid;
      const ctx = parseInt($("#limit-ctx").value, 10) || 0;
      const out = parseInt($("#limit-out").value, 10) || 0;
      const p = findProvider(pid), m = findModel(pid, mid);
      if (p && m) await updateLimit(p, m, ctx, out);
      return;
    }
  } catch { /* mutate 内部已提示 */ }
});

/* ---------- 快捷键 ---------- */
document.addEventListener("keydown", e => {
  if (e.key === "Enter" && (e.target.id === "variant-input" || (e.target.id || "").startsWith("mod-input-"))) {
    e.preventDefault();
    if (e.target.id === "variant-input") {
      const btn = $('#main [data-action="add-variant"]');
      if (btn) btn.click();
    } else {
      const which = e.target.dataset.which;
      const btn = $(`#main [data-action="add-mod"][data-which="${which}"]`);
      if (btn) btn.click();
    }
  }
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
    e.preventDefault();
    save();
  }
});

/* ---------- 增删对话框 ---------- */
function newestProviderId() {
  const ps = (state && state.providers) || [];
  return ps.length ? ps[ps.length - 1].id : null;
}

async function addProviderDialog() {
  const vals = await formDialog("新增模型提供商", [
    { key: "name", label: "名称", required: true, placeholder: "如：My Relay" },
    { key: "id", label: "提供商 ID", placeholder: "留空自动生成 UUID", hint: "将作为 provider 映射的键；不能以 builtin: 开头", mono: true },
    {
      key: "kind", label: "类型 (kind)", value: "openai-compatible",
      options: [
        { v: "openai-compatible", t: "openai-compatible（OpenAI 兼容接口）" },
        { v: "anthropic", t: "anthropic" },
        { v: "openai", t: "openai" },
      ],
    },
    { key: "baseURL", label: "Base URL", placeholder: "https://api.example.com/v1", mono: true },
    { key: "apiKey", label: "API Key", type: "password", placeholder: "可留空稍后填写" },
  ], "创建");
  if (!vals) return;
  await mutate("/api/provider/add", vals, "提供商已创建（记得保存）");
  sel.providerId = vals.id || newestProviderId();
  sel.modelId = null;
  expandedId = sel.providerId;
  renderAll();
}

async function fetchModelsDialog(pid) {
  const p = findProvider(pid);
  try {
    var resp = await api("/api/provider/fetch-models", { providerId: pid });
  } catch (e) { toast(e.message, "err", 8000); return; }
  const models = (resp.extra && resp.extra.models) || [];
  if (!models.length) { toast("接口未返回任何模型", "warn"); return; }
  const existing = new Set((p ? p.models : []).map(m => m.id));
  const body = $("#dlg-generic-body");
  body.innerHTML = `
    <div class="dlg-head"><h3>从 API 拉取模型 — ${esc(p ? (p.name || p.id) : pid)}</h3><button type="button" class="btn icon" data-fetch-close>✕</button></div>
    <p class="hint">接口返回 <b>${models.length}</b> 个模型；已按官方目录预填名称 / 上下文 / 模态（可直接改）。勾选后批量添加，已存在的 ID 自动跳过。</p>
    <div class="set-row" style="margin-bottom:8px">
      <button type="button" class="btn" id="fetch-sel-all">全选</button>
      <button type="button" class="btn" id="fetch-sel-none">全不选</button>
      <span class="hint" style="margin:0">已勾选 <b id="fetch-count">0</b> 个</span>
    </div>
    <div id="fetch-list">
      ${models.map(m => {
        const exists = existing.has(m.id);
        return `
        <div class="fetch-row">
          <label class="fetch-main">
            <input type="checkbox" class="fetch-ck" data-id="${esc(m.id)}" ${exists ? "disabled" : "checked"}>
            <span class="mono" style="font-size:12.5px" title="${esc(m.id)}">${esc(m.id)}</span>
            ${exists ? `<span class="badge gray">已存在</span>` : ""}
            ${m.catalogMatch ? `<span class="badge green" title="配置预填自：${esc(m.catalogMatch)}">目录</span>` : ""}
          </label>
          <input class="fetch-name mono" placeholder="显示名称" value="${esc(m.name && m.name !== m.id ? m.name : "")}" title="显示名称">
          <input class="fetch-ctx" type="number" min="0" placeholder="上下文" value="${m.context > 0 ? m.context : ""}" title="上下文窗口">
          <input class="fetch-out" type="number" min="0" placeholder="最大输出" value="${m.output > 0 ? m.output : ""}" title="最大输出">
          <input class="fetch-mod mono" value="${esc((m.inputModalities || ["text"]).join(","))}" title="输入模态（逗号分隔）" data-out="${esc((m.outputModalities || ["text"]).join(","))}">
        </div>`;
      }).join("")}
    </div>
    <div class="dlg-actions">
      <button type="button" class="btn" data-fetch-close>取消</button>
      <button type="button" class="btn primary" id="fetch-add">添加所选</button>
    </div>`;

  body.querySelector("[data-fetch-close]").addEventListener("click", () => $("#dlg-generic").close());
  const updateCount = () => { $("#fetch-count").textContent = String(body.querySelectorAll(".fetch-ck:checked").length); };
  body.querySelectorAll(".fetch-ck").forEach(c => c.addEventListener("change", updateCount));
  updateCount();
  $("#fetch-sel-all").addEventListener("click", () => { body.querySelectorAll(".fetch-ck:not(:disabled)").forEach(c => { c.checked = true; }); updateCount(); });
  $("#fetch-sel-none").addEventListener("click", () => { body.querySelectorAll(".fetch-ck").forEach(c => { c.checked = false; }); updateCount(); });

  $("#fetch-add").addEventListener("click", async () => {
    let added = 0, failed = 0;
    for (const row of body.querySelectorAll(".fetch-row")) {
      const ck = row.querySelector(".fetch-ck");
      if (!ck.checked || ck.disabled) continue;
      const id = ck.dataset.id;
      const name = row.querySelector(".fetch-name").value.trim();
      const ctx = parseInt(row.querySelector(".fetch-ctx").value, 10) || 0;
      const out = parseInt(row.querySelector(".fetch-out").value, 10) || 0;
      const mod = row.querySelector(".fetch-mod").value.split(/[,，]+/).map(x => x.trim()).filter(Boolean);
      const outMod = (row.querySelector(".fetch-mod").dataset.out || "text").split(",").map(x => x.trim()).filter(Boolean);
      try {
        await api("/api/model/add", { providerId: pid, model: { id, name } });
        const update = {};
        if (name) update.name = name;
        if (ctx > 0 || out > 0) update.limit = { context: ctx, output: out };
        if (mod.length || outMod.length) update.modalities = { input: mod, output: outMod };
        if (Object.keys(update).length) {
          await api("/api/model/update", { providerId: pid, id, update });
        }
        added++;
      } catch (e) {
        failed++;
        toast(`添加 ${id} 失败：${e.message}`, "err", 6000);
      }
    }
    await refreshState();
    sel.providerId = pid; sel.modelId = null; expandedId = pid;
    renderAll();
    $("#dlg-generic").close();
    toast(`已添加 ${added} 个模型${failed ? `，失败 ${failed} 个` : ""}（记得保存）`, failed ? "warn" : "ok", 6000);
  });

  openDialog($("#dlg-generic"));
}

async function addModelDialog(pid) {
  const p = findProvider(pid);
  const extra = `<div style="margin-top:10px;text-align:center">
    <a style="color:var(--accent);cursor:pointer;font-size:12px" data-fetch-link="${esc(pid)}">或从 API 拉取可用模型列表（批量添加）→</a>
  </div>`;
  const vals = await formDialog(`添加模型 — ${p ? (p.name || p.id) : pid}`, [
    { key: "id", label: "模型 ID", required: true, placeholder: "如：glm-5.3", hint: "请求时使用的 model 名称，也是 provider.models 的键", mono: true },
    { key: "name", label: "显示名称", placeholder: "可选" },
  ], "添加", extra);

  if (!vals) return;
  await mutate("/api/model/add", { providerId: pid, model: { id: vals.id, name: vals.name } }, "模型已添加（记得保存）");
  sel.providerId = pid;
  sel.modelId = vals.id;
  expandedId = pid;
  renderAll();
}

async function renameProviderDialog(pid) {
  const vals = await formDialog("重命名提供商 ID", [
    { key: "newId", label: "新 ID", required: true, value: pid, hint: "显示顺序中的对应项会同步更新", mono: true },
  ], "重命名");
  if (!vals || vals.newId === pid) return;
  await mutate("/api/provider/rename-id", { id: pid, newId: vals.newId }, "已重命名");
  sel.providerId = vals.newId;
  sel.modelId = null;
  renderAll();
}

async function renameModelDialog(pid, mid) {
  const vals = await formDialog("重命名模型 ID", [
    { key: "newId", label: "新 ID", required: true, value: mid, hint: "修改后请求该模型需使用新名称", mono: true },
  ], "重命名");
  if (!vals || vals.newId === mid) return;
  await mutate("/api/model/rename-id", { providerId: pid, id: mid, newId: vals.newId }, "已重命名");
  sel.providerId = pid;
  sel.modelId = vals.newId;
  renderAll();
}

async function delProvider(pid) {
  const p = findProvider(pid);
  if (!p) return;
  const yes = await confirmDialog(
    "删除提供商",
    `将删除提供商「<b>${esc(p.name || p.id)}</b>」及其 <b>${p.models.length}</b> 个模型。<br>此修改在保存后才会写入磁盘（保存前会自动备份）。`,
    { okLabel: "删除", danger: true }
  );
  if (!yes) return;
  await mutate("/api/provider/delete", { id: pid });
  sel.providerId = null;
  sel.modelId = null;
  renderAll();
}

async function delModel(pid, mid) {
  const m = findModel(pid, mid);
  if (!m) return;
  const yes = await confirmDialog("删除模型", `将删除模型「<b>${esc(mid)}</b>」。此修改在保存后写入磁盘。`, { okLabel: "删除", danger: true });
  if (!yes) return;
  await mutate("/api/model/delete", { providerId: pid, id: mid });
  if (sel.modelId === mid) sel.modelId = null;
  renderAll();
}

/* ---------- 设置弹窗 / 界面缩放 ---------- */
const SCALE_KEY = "zre-ui-scale";

function autoScale() {
  const h = window.innerHeight || 900;
  return Math.min(1.6, Math.max(1, Math.round((h / 900) * 20) / 20));
}
function currentScale() {
  const v = localStorage.getItem(SCALE_KEY) || "auto";
  if (v === "auto") return autoScale();
  const n = parseFloat(v);
  return isNaN(n) ? autoScale() : n;
}
function applyScale() {
  document.body.style.zoom = String(currentScale());
}
window.addEventListener("resize", () => {
  if ((localStorage.getItem(SCALE_KEY) || "auto") === "auto") applyScale();
});

function openSettings(focusSection) {
  const body = $("#dlg-generic-body");
  const s = state.settings || {};
  const scaleVal = localStorage.getItem(SCALE_KEY) || "auto";
  const settingsPath = (state.configPath || "").replace(/[^\\/]+$/, "") + "zre-settings.json";
  const scaleOpts = ["auto", "0.8", "0.9", "1", "1.1", "1.25", "1.4", "1.6"];
  const scaleLabel = v => (v === "auto" ? "自动（按窗口高度）" : Math.round(parseFloat(v) * 100) + "%");
  const tpls = state.templates || [];
  const userPaths = s.catalogPaths || [];

  body.innerHTML = `
    <div class="dlg-head"><h3>设置</h3><button type="button" class="btn icon" data-set-close>✕</button></div>
    <p class="hint">除缩放外，设置保存在 <span class="mono">${esc(settingsPath)}</span>（不影响 zcode 配置）。</p>

    <div class="set-sec" id="sec-scale">
      <h4>界面缩放</h4>
      <div class="set-row">
        <select id="set-scale">
          ${scaleOpts.map(v => `<option value="${v}" ${scaleVal === v ? "selected" : ""}>${scaleLabel(v)}</option>`).join("")}
        </select>
        <span class="hint" style="margin:0">当前生效 ${Math.round(currentScale() * 100)}%；"自动"按窗口高度推断；也可以直接用浏览器 Ctrl+滚轮</span>
      </div>
    </div>

    <div class="set-sec">
      <h4>生命周期</h4>
      <label class="set-check">
        <input type="checkbox" id="set-autoquit" ${s.autoQuitOnPageClose ? "checked" : ""}>
        <span>关闭所有网页后自动退出（15 秒无心跳即退出；刷新页面不受影响；默认关闭）</span>
      </label>
    </div>

    <div class="set-sec" id="sec-tpl">
      <h4>思考档位模板
        <span class="hint" style="margin:0 0 0 10px;display:inline">常见档位：none / off / minimal / low / medium / high / xhigh / enabled / max</span>
      </h4>
      <div id="set-tpl-list">
        ${tpls.map(t => `
          <div class="snap-row">
            <span class="s-name">${esc(t.name)}</span>
            <span class="s-meta mono">${esc((t.variants || []).join(" / "))} · 默认 ${esc(t.defaultVariant || "")}</span>
            <button class="btn" data-set-tpl-edit="${esc(t.name)}">编辑</button>
            <button class="btn danger" data-set-tpl-del="${esc(t.name)}">删除</button>
          </div>`).join("")}
        ${tpls.length === 0 ? `<div class="snap-empty">暂无模板</div>` : ""}
      </div>
      <div class="set-row" style="margin-top:10px">
        <input id="set-tpl-name" placeholder="模板名称" style="width:180px">
        <input id="set-tpl-vars" placeholder="档位，逗号分隔：low,high,max" class="mono" style="flex:1;min-width:200px">
        <button class="btn accent" id="set-tpl-add">保存模板</button>
      </div>
      <div class="hint" style="margin:4px 0 0">默认档位取列表第一项；同名模板将被覆盖。模型编辑页也可把当前档位一键"存为模板"。</div>
    </div>

    <div class="set-sec">
      <h4>自动嗅探目录（官方模型目录）</h4>
      ${
        state.catalogFromCLI
          ? `<div class="hint">目录来源当前由 <span class="mono">--catalog</span> 启动参数锁定，页面修改不可用。</div>`
          : `<textarea id="set-cat-paths" rows="3" placeholder="每行一个路径（models_catalog*.json 文件或所在目录）；留空 = 自动探测常见安装位置">${esc(userPaths.join("\n"))}</textarea>
        <div class="set-row" style="margin-top:10px">
          <button class="btn accent" id="set-cat-save">保存并重扫</button>
          <button class="btn" id="set-cat-rescan">⟳ 重新扫描</button>
          <span class="hint" style="margin:0">${state.catalogReady ? `当前已加载 <b>${state.catalogCount}</b> 个带档位模型` : "未加载任何目录"}</span>
        </div>
        <div class="hint" style="margin:6px 0 0">当前来源：${esc((state.catalogSources || []).join("；") || "无")}</div>`
      }
    </div>`;

  body.querySelector("[data-set-close]").addEventListener("click", () => $("#dlg-generic").close());

  $("#set-scale").addEventListener("change", e => {
    localStorage.setItem(SCALE_KEY, e.target.value);
    applyScale();
    toast(`界面缩放已设为 ${e.target.value === "auto" ? "自动" : Math.round(parseFloat(e.target.value) * 100) + "%"}`, "ok", 2000);
  });

  $("#set-autoquit").addEventListener("change", async e => {
    try {
      const resp = await api("/api/settings/update", { autoQuitOnPageClose: e.target.checked });
      apply(resp);
      toast(e.target.checked ? "已开启：关闭所有网页 15 秒后自动退出" : "已关闭自动退出", "ok");
    } catch (e2) {
      toast(e2.message, "err");
      e.target.checked = !e.target.checked;
    }
  });

  body.querySelectorAll("[data-set-tpl-edit]").forEach(b => b.addEventListener("click", () => {
    const t = (state.templates || []).find(x => x.name === b.dataset.setTplEdit);
    if (!t) return;
    $("#set-tpl-name").value = t.name;
    $("#set-tpl-vars").value = (t.variants || []).join(",");
    $("#set-tpl-name").focus();
    toast(`已载入模板「${t.name}」，修改后点"保存模板"覆盖`, "info", 2500);
  }));

  $("#set-tpl-add").addEventListener("click", async () => {
    const name = $("#set-tpl-name").value.trim();
    const variants = $("#set-tpl-vars").value.split(/[,，;；]+/).map(x => x.trim()).filter(Boolean);
    if (!name || !variants.length) { toast("请填写模板名称和档位", "warn"); return; }
    try {
      const resp = await api("/api/template/save", { name, variants, defaultVariant: variants[0], allowOverwrite: true });
      apply(resp);
      toast(`模板「${name}」已保存`, "ok");
      openSettings("tpl");
    } catch (e2) { toast(e2.message, "err"); }
  });

  body.querySelectorAll("[data-set-tpl-del]").forEach(b => b.addEventListener("click", async () => {
    if (!(await confirmDialog("删除模板", `删除模板「<b>${esc(b.dataset.setTplDel)}</b>」？`, { okLabel: "删除", danger: true }))) return;
    try {
      apply(await api("/api/template/delete", { name: b.dataset.setTplDel }));
      toast("模板已删除", "ok", 1800);
      openSettings("tpl");
    } catch (e2) { toast(e2.message, "err"); }
  }));

  const catSave = $("#set-cat-save");
  if (catSave) catSave.addEventListener("click", async () => {
    const paths = $("#set-cat-paths").value.split(/\r?\n/).map(x => x.trim()).filter(Boolean);
    try {
      const resp = await api("/api/settings/update", { catalogPaths: paths });
      apply(resp);
      (resp.warnings || []).forEach(w => toast(w, "warn", 5000));
      toast("嗅探目录已保存", "ok");
      openSettings();
    } catch (e2) { toast(e2.message, "err"); }
  });

  const catRescan = $("#set-cat-rescan");
  if (catRescan) catRescan.addEventListener("click", async () => {
    try {
      const resp = await api("/api/catalog/rescan", {});
      apply(resp);
      toast(`官方目录已刷新：${resp.extra.count} 个带档位的模型`, "ok");
    } catch (e2) { toast(e2.message, "err"); }
  });

  openDialog($("#dlg-generic"));
  if (focusSection === "tpl") {
    const sec = body.querySelector("#sec-tpl");
    if (sec) sec.scrollIntoView({ block: "start" });
  }
}

/* ---------- 顺序调整弹窗（拖动 + ↑↓） ---------- */

// 通用列表重排弹窗：items=[{id,label,sub}]，完成后 onCommit(新顺序 id 数组)。
function openReorderDialog({ title, hint, items, onCommit }) {
  const body = $("#dlg-generic-body");
  let order = items.map(x => ({ ...x }));
  let changed = false;

  const render = () => {
    body.innerHTML = `
      <div class="dlg-head"><h3>${esc(title)}</h3><button type="button" class="btn icon" data-ro-close>✕</button></div>
      ${hint ? `<p class="hint">${hint}</p>` : ""}
      <div id="ro-list">
        ${order.map((it, i) => `
          <div class="ro-row" draggable="true" data-ro-i="${i}">
            <span class="grip" data-ro-grip title="拖动调整顺序">⋮⋮</span>
            <span class="ro-label" title="${esc(it.label)}">${esc(it.label)}</span>
            ${it.sub ? `<span class="s-meta mono">${esc(it.sub)}</span>` : ""}
            <span class="ro-btns">
              <button type="button" class="btn icon" data-ro-up ${i === 0 ? "disabled" : ""} title="上移">↑</button>
              <button type="button" class="btn icon" data-ro-down ${i === order.length - 1 ? "disabled" : ""} title="下移">↓</button>
            </span>
          </div>`).join("")}
      </div>
      <div class="dlg-actions">
        <button type="button" class="btn" data-ro-cancel>取消</button>
        <button type="button" class="btn primary" data-ro-done>完成${changed ? "（已调整）" : ""}</button>
      </div>`;

    body.querySelector("[data-ro-close]").onclick = () => $("#dlg-generic").close();
    body.querySelector("[data-ro-cancel]").onclick = () => $("#dlg-generic").close();
    body.querySelector("[data-ro-done]").onclick = () => {
      $("#dlg-generic").close();
      if (changed) onCommit(order.map(x => x.id));
    };
    body.querySelectorAll("[data-ro-up]").forEach((b, i) => b.onclick = () => move(i, i - 1));
    body.querySelectorAll("[data-ro-down]").forEach((b, i) => b.onclick = () => move(i, i + 1));

    const list = body.querySelector("#ro-list");
    let armed = false;
    list.addEventListener("mousedown", e => { armed = !!e.target.closest("[data-ro-grip]"); });
    list.querySelectorAll(".ro-row").forEach(row => {
      const i = +row.dataset.roI;
      row.addEventListener("dragstart", e => {
        if (!armed) { e.preventDefault(); return; }
        row.dataset.roDrag = "1";
        row.classList.add("dragging");
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", String(i));
      });
      row.addEventListener("dragover", e => {
        e.preventDefault();
        list.querySelectorAll(".ro-row").forEach(r => r.classList.remove("ro-drop"));
        if (+row.dataset.roI !== i) row.classList.add("ro-drop");
      });
      row.addEventListener("drop", e => {
        e.preventDefault();
        const j = +row.dataset.roI;
        if (j !== i) { const [x] = order.splice(i, 1); order.splice(j, 0, x); changed = true; }
      });
      row.addEventListener("dragend", () => {
        row.classList.remove("dragging");
        list.querySelectorAll(".ro-row").forEach(r => r.classList.remove("ro-drop"));
        if (changed) render();
      });
    });
  };

  const move = (i, j) => {
    if (j < 0 || j >= order.length || i === j) return;
    [order[i], order[j]] = [order[j], order[i]];
    changed = true;
    render();
  };

  render();
  openDialog($("#dlg-generic"));
}

function openProviderReorder() {
  openReorderDialog({
    title: "调整提供商显示顺序",
    hint: "拖动 ⋮⋮ 或点击 ↑↓ 调整；顺序会同步到 zcode 的提供商列表。点「完成」应用后记得保存。",
    items: state.providers.map(p => ({ id: p.id, label: p.name || p.id, sub: `${p.models.length} 个模型` })),
    onCommit: async ids => {
      try {
        apply(await api("/api/provider/reorder", { ids }));
        toast("提供商顺序已更新（记得保存）", "ok", 2500);
      } catch (e) { toast(e.message, "err"); }
    },
  });
}

function openModelReorder(pid) {
  const p = findProvider(pid);
  if (!p || p.models.length < 2) { toast("至少需要两个模型才能排序", "warn"); return; }
  openReorderDialog({
    title: `调整模型顺序 — ${p.name || p.id}`,
    hint: "拖动 ⋮⋮ 或点击 ↑↓ 调整；models 的键序即 zcode 中的显示顺序。点「完成」应用后记得保存。",
    items: p.models.map(m => ({
      id: m.id,
      label: m.id,
      sub: [m.name, m.hasReasoning && m.enabled ? `默认 ${m.defaultVariant || "on"}` : ""].filter(Boolean).join(" · "),
    })),
    onCommit: async ids => {
      try {
        apply(await api("/api/model/reorder", { providerId: pid, ids }));
        toast("模型顺序已更新（记得保存）", "ok", 2500);
      } catch (e) { toast(e.message, "err"); }
    },
  });
}

/* ---------- 顶栏与初始化 ---------- */
$("#search").addEventListener("input", e => { query = e.target.value; renderSidebar(); });
$("#btn-save").addEventListener("click", () => save());
$("#btn-reload").addEventListener("click", reloadAll);
$("#btn-snapshots").addEventListener("click", openSnapshots);
$("#btn-settings").addEventListener("click", () => openSettings());
$("#btn-quit").addEventListener("click", async () => {
  const yes = await confirmDialog("退出 ZRE",
    "将结束 ZRE 进程（托盘图标一并移除）。<br>未保存的修改会丢失，磁盘上的 config.json 不受影响。确定退出？",
    { okLabel: "退出", danger: true });
  if (!yes) return;
  try {
    await api("/api/quit", {});
    toast("ZRE 已退出，可以关闭此页面", "info", 10000);
  } catch (e2) { toast(e2.message, "err"); }
});
$("#btn-raw").addEventListener("click", openRaw);
$("#btn-add-provider").addEventListener("click", addProviderDialog);
$("#btn-snap-create").addEventListener("click", createSnapshot);
$("#snap-name").addEventListener("keydown", e => { if (e.key === "Enter") createSnapshot(); });
$("#btn-raw-copy").addEventListener("click", () => copyText($("#raw-view").textContent, "已复制 JSON"));
$("#st-path").addEventListener("click", () => copyText(state.configPath, "已复制配置路径"));
$("#btn-side-reorder").addEventListener("click", openProviderReorder);
$$("dialog [data-close]").forEach(b =>
  b.addEventListener("click", () => b.closest("dialog").close())
);

window.addEventListener("beforeunload", e => {
  if (state && state.dirty) { e.preventDefault(); e.returnValue = ""; }
});

// 页面心跳：开启"关闭网页自动退出"后，服务端按最后一次心跳计时
setInterval(() => {
  if (state) fetch("/api/heartbeat", { method: "POST" }).catch(() => {});
}, 2000);

applyScale();
refreshState().catch(e => toast(e.message, "err", 8000));

'use strict';
(() => {
  // ------------------------------------------------------------------ utils
  const $ = (s, el = document) => el.querySelector(s);
  const $$ = (s, el = document) => [...el.querySelectorAll(s)];
  const PROPS = new Set(['value', 'checked', 'selected', 'disabled', 'indeterminate']);
  function h(tag, attrs, ...kids) {
    const el = document.createElement(tag);
    const props = [];
    if (attrs) for (const [k, v] of Object.entries(attrs)) {
      if (v == null || v === false) continue;
      if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
      else if (k === 'class') el.className = v;
      else if (k === 'html') el.innerHTML = v;
      else if (k === 'style' && typeof v === 'object') for (const [sk, sv] of Object.entries(v)) el.style.setProperty(sk, sv);
      else if (PROPS.has(k)) props.push([k, v]);
      else el.setAttribute(k, v === true ? '' : v);
    }
    for (const c of kids.flat(Infinity)) if (c != null && c !== false) el.append(c.nodeType ? c : String(c));
    for (const [k, v] of props) el[k] = v;
    return el;
  }
  const esc = s => String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const NATIVE = !!(window.__devknife && window.__devknife.native);
  const TOKEN = NATIVE ? window.__devknife.token : '';
  // Access key for the local API, so other local users and apps can't read
  // your history. The desktop app injects it; in a browser it arrives once in
  // the link `devknife ui` prints (?k=…), is kept in this origin's storage
  // (per port, unlike cookies) and removed from the address bar.
  const KEY = (() => {
    let k = '';
    try {
      const u = new URL(location.href);
      k = u.searchParams.get('k') || '';
      if (k) {
        u.searchParams.delete('k');
        history.replaceState(null, '', u.pathname + u.search + u.hash);
      }
      if (NATIVE && window.__devknife.key) return window.__devknife.key;
      if (k) localStorage.setItem('devknife-key', k);
      else k = localStorage.getItem('devknife-key') || '';
    } catch { }
    return k;
  })();
  const apiHeaders = json => Object.assign({ 'X-Devknife-Key': KEY }, json ? { 'Content-Type': 'application/json' } : {});
  // UI state lives in a file in the user's config dir (via /api/prefs), so it
  // survives restarts of the desktop app, which gets a new port each launch.
  // Only changed keys are sent (PATCH): two windows don't overwrite each
  // other's settings, and a session whose load failed can't wipe the file.
  let PREFS = {}, prefsTimer = null, prefsLoaded = false, saveFailed = false;
  const dirty = new Set();
  // Saves go out one at a time, so an older value can't land after a newer one.
  let prefsChain = Promise.resolve();
  const flushPrefs = () => {
    clearTimeout(prefsTimer);
    prefsTimer = null;
    return (prefsChain = prefsChain.then(sendPrefs));
  };
  const sendPrefs = () => {
    if (!prefsLoaded || !dirty.size) return;
    const keys = [...dirty], set = {}, del = [];
    dirty.clear();
    for (const k of keys) if (Object.prototype.hasOwnProperty.call(PREFS, k)) set[k] = PREFS[k]; else del.push(k);
    const body = JSON.stringify({ set, del });
    return fetch('api/prefs', { method: 'PATCH', headers: apiHeaders(true), body, keepalive: body.length < 60000 })
      .then(async r => {
        if (!r.ok) throw new Error((await r.json().catch(() => ({}))).error || 'HTTP ' + r.status);
        if (saveFailed) { saveFailed = false; toast('Settings saved'); }
      })
      .catch(e => {
        for (const k of keys) if (!dirty.has(k)) dirty.add(k); // sent again with the next change
        if (!saveFailed) { saveFailed = true; toast('Settings not saved: ' + e.message, 5000); }
      });
  };
  // The desktop app can quit at any moment (⌘Q, closing the window) without a
  // pagehide event, so it sends changes right away; it finishes in-flight
  // saves before exiting. Typed input is still debounced by saveState.
  const schedulePrefs = () => { clearTimeout(prefsTimer); prefsTimer = setTimeout(flushPrefs, NATIVE ? 0 : 400); };
  const store = {
    get(k, d) { return Object.prototype.hasOwnProperty.call(PREFS, k) ? PREFS[k] : d; },
    set(k, v) { PREFS[k] = v; dirty.add(k); schedulePrefs(); },
    del(k) { delete PREFS[k]; dirty.add(k); schedulePrefs(); },
  };
  const isMac = NATIVE ? window.__devknife.os === 'darwin' : /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  const MOD = isMac ? '⌘' : 'Ctrl+';
  const fmtBytes = n => n < 1024 ? n + ' B' : n < 1048576 ? (n / 1024).toFixed(1) + ' KB' : (n / 1048576).toFixed(2) + ' MB';
  const utf8Len = s => new TextEncoder().encode(s).length;
  const debounce = (fn, ms) => {
    let t = null, args;
    const d = (...a) => { args = a; clearTimeout(t); t = setTimeout(() => { t = null; fn(...args); }, ms); };
    d.flush = () => { if (t) { clearTimeout(t); t = null; fn(...args); } }; // run a pending call now
    return d;
  };
  const countLines = s => { let n = 1, i = -1; while ((i = s.indexOf('\n', i + 1)) !== -1) n++; return n; };
  // The desktop window has its own title bar; keep it in sync with the page.
  function setTitle(s) {
    document.title = s;
    if (NATIVE && window.dkSetTitle) window.dkSetTitle(TOKEN, s).catch(() => { });
  }

  const ICON = {
    copy: '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h8"/>',
    clip: '<rect x="8" y="2" width="8" height="4" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/>',
    file: '<path d="M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M14 3v6h6"/>',
    x: '<path d="M18 6 6 18M6 6l12 12"/>',
    flask: '<path d="M9 3h6M10 3v6l-5 9a2 2 0 0 0 1.7 3h10.6a2 2 0 0 0 1.7-3l-5-9V3"/>',
    star: '<path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1 6.2L12 17.3 6.5 20.2l1-6.2L3 9.6l6.2-.9z"/>',
    send: '<path d="M5 12h14M13 6l6 6-6 6"/>',
    back: '<path d="M19 12H5M11 18l-6-6 6-6"/>',
    save: '<path d="M12 3v12M7 10l5 5 5-5M5 21h14"/>',
    link: '<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1"/><path d="M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>',
    history: '<path d="M3 12a9 9 0 1 0 3-6.7L3 8"/><path d="M3 3v5h5M12 7v5l3 2"/>',
    term: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="m7 9 3 3-3 3M13 15h4"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8z"/>',
    auto: '<circle cx="12" cy="12" r="9"/><path d="M12 3v18a9 9 0 0 0 0-18z" fill="currentColor"/>',
    menu: '<path d="M4 6h16M4 12h16M4 18h16"/>',
    refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4L21 8"/><path d="M21 3v5h-5"/>',
    wrap: '<path d="M4 6h16M4 12h13a3 3 0 0 1 0 6h-4M15 16l-2 2 2 2M4 18h5"/>',
    swap: '<path d="M7 4 3 8l4 4M3 8h14M17 20l4-4-4-4M21 16H7"/>',
    up: '<path d="m6 15 6-6 6 6"/>',
    down: '<path d="m6 9 6 6 6-6"/>',
    side: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
    chev: '<path d="m6 9 6 6 6-6"/>',
    paperclip: '<path d="m21 11-8.6 8.6a5 5 0 0 1-7-7L14 4a3.3 3.3 0 0 1 4.7 4.7l-8.6 8.6a1.7 1.7 0 0 1-2.4-2.4L15.5 7"/>',
    alert: '<path d="M10.3 3.9 1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/><path d="M12 9v4M12 17h.01"/>',
    check: '<path d="M20 6 9 17l-5-5"/>',
    xcircle: '<circle cx="12" cy="12" r="9"/><path d="m15 9-6 6M9 9l6 6"/>',
    circle: '<circle cx="12" cy="12" r="9"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    edit: '<path d="M4 20h4L19 9l-4-4L4 16z"/><path d="m13.5 6.5 4 4"/>',
    trash: '<path d="M4 7h16M10 11v6M14 11v6M6 7l1 13h10l1-13M9 7V4h6v3"/>',
  };
  const icon = (n, fill) => h('span', { html: `<svg viewBox="0 0 24 24" fill="${fill ? 'currentColor' : 'none'}" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${ICON[n]}</svg>`, style: { display: 'contents' } });
  const btn = (ic, label, onclick, extra = {}) => h('button', { class: 'btn ' + (extra.class || ''), title: extra.title || label, 'aria-label': label, onclick, type: 'button', disabled: extra.disabled }, ic && icon(ic), h('span', { class: 'lbl' }, label));
  const iconBtn = (ic, label, onclick, cls = '') => h('button', { class: 'icon-btn ' + cls, title: label, 'aria-label': label, onclick, type: 'button' }, icon(ic, ic === 'star' && cls.includes('on')));

  let toastTimer;
  function toast(msg, ms = 1600) {
    const t = $('#toast');
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove('show'), ms);
  }

  async function api(path, body, signal) {
    const init = { headers: apiHeaders(body !== undefined), signal };
    if (body !== undefined) { init.method = 'POST'; init.body = JSON.stringify(body); }
    const r = await fetch(path, init);
    if (r.status === 401) throw new Error('the access key is missing or wrong; reopen devknife from the link in your terminal');
    if (!r.ok && r.headers.get('content-type')?.indexOf('json') < 0) throw new Error(await r.text());
    return r.json();
  }
  // Detection hits, or [] when the server is busy or unreachable.
  const detectAPI = async input => { const r = await api('api/detect', { input }); return Array.isArray(r) ? r : []; };

  async function copyText(s, what = 'Copied') {
    try {
      if (NATIVE) await window.dkClipWrite(TOKEN, s);
      else await navigator.clipboard.writeText(s);
    } catch {
      const ta = h('textarea', { style: { position: 'fixed', opacity: '0' } });
      ta.value = s;
      document.body.append(ta);
      ta.select();
      document.execCommand('copy');
      ta.remove();
    }
    toast(what);
  }

  async function readClipboard() {
    try { return NATIVE ? await window.dkClipRead(TOKEN) : await navigator.clipboard.readText(); } catch { toast(`Clipboard blocked — press ${MOD}V instead`); return null; }
  }

  // ------------------------------------------------------------------ state
  const S = {
    tools: [], byKey: {}, cats: [], version: '',
    tool: null, input: '', opts: {}, data: null, dataName: '', dataURL: '',
    result: null, seq: 0, outTab: 0, running: false,
    favorites: store.get('fav', []), recent: store.get('recent', []),
    history: store.get('history', []), keepHistory: store.get('keepHistory', true),
    wrap: store.get('wrap', false), diffLayout: store.get('diffLayout', 'split'), diffExpand: false,
    theme: store.get('theme', 'auto'), cursor: { ln: 1, col: 1 }, suggest: null,
    folded: store.get('sideFold', []), lastGood: null,
  };
  const defaults = t => Object.fromEntries((t.options || []).map(o => [o.key, o.default]));
  // Secrets (signing keys, HMAC keys) are never written to disk, history or
  // links; they live in memory for the session only.
  const sensitiveKeys = t => new Set((t.options || []).filter(o => o.sensitive).map(o => o.key));
  const keptOpts = (t, o) => { const s = sensitiveKeys(t); return Object.fromEntries(Object.entries(o || {}).filter(([k]) => !s.has(k))); };
  const SECRETS = {}; // tool id -> its sensitive option values, this session only
  // Hidden options are set by the tool's own view (e.g. command placeholders).
  const textOpts = t => (t.options || []).filter(o => o.type === 'text' && !o.hidden);
  const barOpts = t => (t.options || []).filter(o => o.type !== 'text' && !o.hidden);

  // ------------------------------------------------------------------ theme
  function applyTheme() {
    if (S.theme === 'auto') delete document.documentElement.dataset.theme;
    else document.documentElement.dataset.theme = S.theme;
    const b = $('#theme-btn');
    b.replaceChildren(icon(S.theme === 'dark' ? 'moon' : S.theme === 'light' ? 'sun' : 'auto'));
    b.title = 'Theme: ' + S.theme;
  }
  function cycleTheme() {
    S.theme = { auto: 'light', light: 'dark', dark: 'auto' }[S.theme];
    store.set('theme', S.theme);
    applyTheme();
    toast('Theme: ' + S.theme);
  }

  // ------------------------------------------------------------------ fuzzy
  function fuzzy(q, s) {
    if (!q) return 1;
    s = s.toLowerCase();
    const i = s.indexOf(q);
    if (i === 0) return 100 - s.length * 0.05;
    if (i > 0) return (/[\s\-_/.]/.test(s[i - 1]) ? 85 : 60) - i * 0.1;
    let si = 0, score = 0, prev = -2;
    for (const c of q) {
      const j = s.indexOf(c, si);
      if (j < 0) return 0;
      score += (j === prev + 1 ? 3 : 1) + (j === 0 || /[\s\-_/.]/.test(s[j - 1]) ? 3 : 0);
      prev = j; si = j + 1;
    }
    return Math.min(55, score * 2);
  }
  function toolScore(t, q) {
    q = q.trim().toLowerCase();
    if (!q) return 1;
    let best = Math.max(fuzzy(q, t.name) * 1.05, fuzzy(q, t.id), ...(t.aliases || []).map(a => fuzzy(q, a)), ...(t.keywords || []).map(k => fuzzy(q, k) * 0.75));
    if (best < 30 && q.length > 2 && t.description.toLowerCase().includes(q)) best = 30;
    return best;
  }
  const searchTools = q => S.tools.map(t => [t, toolScore(t, q)]).filter(x => x[1] > 0).sort((a, b) => b[1] - a[1]).map(x => x[0]);

  // ------------------------------------------------------------------ highlight
  function hl(code, re, fn) {
    let out = '', last = 0, m;
    re.lastIndex = 0;
    while ((m = re.exec(code))) {
      if (m[0] === '') { re.lastIndex++; continue; }
      out += esc(code.slice(last, m.index)) + fn(m);
      last = re.lastIndex;
    }
    return out + esc(code.slice(last));
  }
  const sp = (c, s) => `<span class="${c}">${esc(s)}</span>`;
  const reJSON = /("(?:[^"\\\n]|\\.)*")(\s*:)?|\b(true|false|null)\b|(-?\b\d+(?:\.\d+)?(?:[eE][+-]?\d+)?\b)/g;
  const reYAML = /(#.*$)|^(\s*(?:- )*)([^\s#:'"\-][^:#\n]*?|"[^"\n]*"|'[^'\n]*')(:)(?=\s|$)|("(?:[^"\\\n]|\\.)*"|'[^'\n]*')|\b(true|false|null|~)\b|(-?\b\d+(?:\.\d+)?\b)/gm;
  const reMarkup = /(<!--[\s\S]*?-->|\{\/\*[\s\S]*?\*\/\})|(<%[\s\S]*?%>)|(<!\[CDATA\[[\s\S]*?\]\]>)|(<[!?/]?[A-Za-z][^<>]*>|<\/?>)/g;
  const reAttr = /("[^"]*"|'[^']*'|\{[^}]*\})|([^\s=/>]+)/g;
  const reCSS = /(\/\*[\s\S]*?\*\/|(?<![:\w(])\/\/[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*')|(@[\w-]+)|([\w-]+)(\s*:)(?![^{};]*\{)|(#[0-9a-fA-F]{3,8}\b)|((?<![\w-])-?\d*\.?\d+(?:px|em|rem|%|vh|vw|vmin|vmax|s|ms|deg|fr|ch)?)|([$][\w-]+)/g;
  const SQLKW = 'select|from|where|and|or|not|in|is|null|as|on|join|left|right|inner|outer|full|cross|group|by|order|having|limit|offset|insert|into|values|update|set|delete|create|table|alter|drop|index|view|distinct|union|all|case|when|then|else|end|between|like|ilike|exists|asc|desc|with|returning|primary|key|foreign|references|default|true|false|over|partition|using|if|begin|commit|rollback|declare|grant|revoke|top|fetch|next|rows|only|unique|check|constraint|cascade';
  const reSQL = new RegExp(`(--[^\\n]*|\\/\\*[\\s\\S]*?\\*\\/)|('(?:[^']|'')*')|("(?:[^"]|"")*"|\`[^\`]*\`|\\[[^\\]\\n]*\\])|\\b(${SQLKW})\\b|(\\b\\d+(?:\\.\\d+)?\\b)|([:@$]\\w+|\\?)`, 'gi');
  const KW = new Set(('abstract async await break case catch class const continue def default defer delete do elif else enum export extends false final finally fn for from func function go if impl import in instanceof interface is lambda let match mod mut new nil none not null of or package pass private protected pub public raise return self static struct super switch this throw throws true try type typeof undefined use val var void when where while with yield None True False and as chan map range select fallthrough goto internal override data object sealed record namespace using readonly get set init guard extension protocol end require then unless elsif module begin rescue ensure crate dyn ref where loop unsafe trait').split(' '));
  const reCodeBase = String.raw`("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|` + '`(?:[^`\\\\]|\\\\.)*`' + String.raw`)|(\b0x[0-9a-fA-F]+\b|\b\d[\d_]*(?:\.\d+)?(?:[eE][+-]?\d+)?\b)|([A-Za-z_$][\w$]*)(\s*\()?|(@\w+)`;
  const reCode = new RegExp(String.raw`(\/\/[^\n]*|\/\*[\s\S]*?\*\/)|` + reCodeBase, 'g');
  const reCodeHash = new RegExp(String.raw`(#[^\n]*)|` + reCodeBase, 'g');
  const codeFn = m => m[1] ? sp('t-com', m[1]) : m[2] ? sp('t-str', m[2]) : m[3] ? sp('t-num', m[3]) : m[4] ? (KW.has(m[4]) ? sp('t-kw', m[4]) + esc(m[5] || '') : m[5] ? sp('t-fn', m[4]) + esc(m[5]) : /^[A-Z]/.test(m[4]) ? sp('t-key', m[4]) : esc(m[4])) : sp('t-attr', m[6]);

  function highlight(code, lang) {
    lang = (lang || '').toLowerCase();
    if (!code || code.length > 400000) return esc(code || '');
    switch (lang) {
      case 'json': case 'jsonschema': case 'schema':
        return hl(code, reJSON, m => m[1] ? (m[2] ? sp('t-key', m[1]) + esc(m[2]) : sp('t-str', m[1])) : m[3] ? sp('t-kw', m[3]) : sp('t-num', m[4]));
      case 'yaml': case 'yml':
        return hl(code, reYAML, m => m[1] ? sp('t-com', m[1]) : m[3] ? esc(m[2]) + sp('t-key', m[3]) + esc(m[4]) : m[5] ? sp('t-str', m[5]) : m[6] ? sp('t-kw', m[6]) : sp('t-num', m[7]));
      case 'html': case 'xml': case 'svg': case 'jsx': case 'tsx': case 'erb': case 'vue':
        return hl(code, reMarkup, m => {
          if (m[1]) return sp('t-com', m[1]);
          if (m[2]) return sp('t-fn', m[2]);
          if (m[3]) return sp('t-str', m[3]);
          const t = /^(<[!?/]?)([\w:.-]*)([\s\S]*?)(\/?>)$/.exec(m[4]);
          if (!t) return esc(m[4]);
          return esc(t[1]) + sp('t-tag', t[2]) + hl(t[3], reAttr, a => a[1] ? sp('t-str', a[1]) : sp('t-attr', a[2])) + esc(t[4]);
        });
      case 'css': case 'scss': case 'less': case 'sass':
        return hl(code, reCSS, m => m[1] ? sp('t-com', m[1]) : m[2] ? sp('t-str', m[2]) : m[3] ? sp('t-kw', m[3]) : m[4] ? sp('t-key', m[4]) + esc(m[5]) : m[6] ? sp('t-num', m[6]) : m[7] ? sp('t-num', m[7]) : sp('t-attr', m[8]));
      case 'sql':
        return hl(code, reSQL, m => m[1] ? sp('t-com', m[1]) : m[2] ? sp('t-str', m[2]) : m[3] ? sp('t-key', m[3]) : m[4] ? sp('t-kw', m[4]) : m[5] ? sp('t-num', m[5]) : sp('t-attr', m[6]));
      case 'diff': case 'patch':
        return code.split('\n').map(l => l.startsWith('+++') || l.startsWith('---') ? `<b>${esc(l)}</b>` : l[0] === '+' ? sp('t-ins', l) : l[0] === '-' ? sp('t-del', l) : l.startsWith('@@') ? sp('t-hunk', l) : esc(l)).join('\n');
      case 'worddiff':
        return hl(code, /\[-([\s\S]*?)-\]|\{\+([\s\S]*?)\+\}/g, m => m[1] !== undefined ? `<del class="dw">${esc(m[1])}</del>` : `<ins class="dw">${esc(m[2])}</ins>`);
      case 'python': case 'ruby': case 'bash': case 'sh': case 'shell': case 'httpie': case 'toml': case 'ini':
        return hl(code, reCodeHash, codeFn);
      case '': case 'text': case 'csv': case 'tsv': case 'markdown':
        return esc(code);
      default:
        return hl(code, reCode, codeFn);
    }
  }

  // ------------------------------------------------------------------ sidebar
  function sideItem(t) {
    const on = !!S.tool && S.tool.id === t.id;
    return h('a', { class: 'side-item' + (on ? ' active' : ''), href: '#/' + t.id, 'data-id': t.id, title: t.description, 'aria-current': on ? 'page' : null },
      h('span', { class: 'name' }, t.name), t.extra && h('span', { class: 'extra-dot', title: 'devknife extra' }));
  }
  const slug = s => s.toLowerCase().replace(/[^a-z0-9]+/g, '-');
  function sideGroup(label, tools, foldable) {
    if (!foldable) return h('div', { class: 'side-group' }, h('h2', null, h('span', { class: 'group-label' }, label)), tools.map(sideItem));
    // A folded category still shows the tool that is open, so the selection never disappears.
    const folded = S.folded.includes(label);
    const shown = folded ? tools.filter(t => S.tool && t.id === S.tool.id) : tools;
    const id = 'grp-' + slug(label);
    return h('div', { class: 'side-group' + (folded ? ' folded' : '') },
      h('h2', null, h('button', {
        type: 'button', class: 'group-btn', 'data-fold': label, 'aria-expanded': String(!folded), 'aria-controls': id,
        title: (folded ? 'Show ' : 'Hide ') + label, onclick: () => toggleFold(label),
      }, h('span', null, label), h('span', { class: 'count' }, tools.length), h('span', { class: 'chev' }, icon('chev')))),
      h('div', { id, role: 'group', 'aria-label': label }, shown.map(sideItem)));
  }
  function toggleFold(label) {
    S.folded = S.folded.includes(label) ? S.folded.filter(x => x !== label) : [...S.folded, label];
    store.set('sideFold', S.folded);
    renderSidebar();
    const b = $$('#tool-list .group-btn').find(x => x.dataset.fold === label);
    if (b) b.focus();
  }
  function renderSidebar() {
    const nav = $('#tool-list');
    const q = $('#side-filter').value.trim();
    nav.replaceChildren();
    if (q) {
      const hits = searchTools(q);
      nav.append(sideGroup(hits.length ? 'Results' : 'No matching tools', hits, false));
      return;
    }
    const fav = S.favorites.map(id => S.byKey[id]).filter(Boolean);
    if (fav.length) nav.append(sideGroup('Favorites', fav, false));
    // Recent is for jumping back, so the tool that is already open is left out.
    const rec = S.recent.map(id => S.byKey[id]).filter(t => t && !S.favorites.includes(t.id) && !(S.tool && t.id === S.tool.id)).slice(0, 4);
    if (rec.length) nav.append(sideGroup('Recent', rec, false));
    for (const c of S.cats) nav.append(sideGroup(c, S.tools.filter(t => t.category === c), true));
  }
  function markActive() {
    $$('.side-item').forEach(a => {
      const on = !!S.tool && a.dataset.id === S.tool.id;
      a.classList.toggle('active', on);
      if (on) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
    });
    const act = $('.side-item.active');
    if (act) act.scrollIntoView({ block: 'nearest' });
  }
  // ↑/↓ walk the sidebar like a native source list.
  function sideArrows(e) {
    if ((e.key !== 'ArrowDown' && e.key !== 'ArrowUp') || e.altKey || e.metaKey || e.ctrlKey) return;
    const items = $$('#tool-list .side-item, #tool-list .group-btn');
    const i = items.indexOf(document.activeElement);
    if (i < 0) return;
    e.preventDefault();
    const next = items[i + (e.key === 'ArrowDown' ? 1 : -1)];
    if (next) next.focus();
    else if (e.key === 'ArrowUp') $('#side-filter').focus();
  }
  const orderedIds = () => S.cats.flatMap(c => S.tools.filter(t => t.category === c).map(t => t.id));
  function stepTool(d) {
    const ids = orderedIds();
    const i = S.tool ? ids.indexOf(S.tool.id) : -1;
    go(ids[(i + d + ids.length) % ids.length]);
  }
  function closeSide() { $('#app').classList.remove('side-open'); $('#scrim').hidden = true; }
  function toggleSide() {
    if (window.matchMedia('(max-width: 900px)').matches) {
      const open = $('#app').classList.toggle('side-open');
      $('#scrim').hidden = !open;
    } else {
      $('#app').classList.toggle('side-collapsed');
      store.set('sideCollapsed', $('#app').classList.contains('side-collapsed'));
    }
  }

  // ------------------------------------------------------------------ routing
  function go(id, preset) {
    if (preset) S.pendingPreset = preset;
    const target = id ? '#/' + id : '#/';
    if (location.hash === target) route(); else location.hash = target;
  }
  function parseHash() {
    const raw = location.hash.replace(/^#\/?/, '');
    const [path, qs] = raw.split('?');
    return { id: decodeURIComponent(path || ''), params: new URLSearchParams(qs || '') };
  }
  function route() {
    const { id, params } = parseHash();
    const t = id && S.byKey[id.toLowerCase()];
    closeSide();
    // The desktop app reopens where you left off (it has no URL bar to carry the route).
    if (NATIVE && store.get('lastTool', '') !== (t ? t.id : '')) store.set('lastTool', t ? t.id : '');
    if (!t) { S.tool = null; stopTicker(); renderHome(); markActive(); return; }
    let preset = S.pendingPreset || null;
    S.pendingPreset = null;
    if ([...params.keys()].length) {
      preset = preset || {};
      preset.opts = Object.assign({}, preset.opts);
      for (const [k, v] of params) {
        if (k === 'in') { try { preset.input = b64urlDecode(v); } catch { } }
        else preset.opts[k] = v;
      }
    }
    openTool(t, preset);
    if (t.id !== id) history.replaceState(null, '', '#/' + t.id);
  }
  const b64urlEncode = s => btoa(String.fromCharCode(...new TextEncoder().encode(s))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  const b64urlDecode = s => new TextDecoder().decode(Uint8Array.from(atob(s.replace(/-/g, '+').replace(/_/g, '/')), c => c.charCodeAt(0)));

  function syncURL() {
    if (!S.tool) return;
    const d = defaults(S.tool), p = new URLSearchParams();
    for (const o of barOpts(S.tool)) if (S.opts[o.key] !== d[o.key] && !o.sensitive) p.set(o.key, S.opts[o.key]);
    const qs = p.toString();
    history.replaceState(null, '', '#/' + S.tool.id + (qs ? '?' + qs : ''));
  }

  // ------------------------------------------------------------------ tool state
  // Inputs kept across restarts are bounded: per tool, and in total (the
  // least recently used tools lose theirs first).
  const MAX_SAVED_INPUT = 100000, MAX_SAVED_INPUTS = 2000000;
  const saveState = debounce(() => {
    if (!S.tool) return;
    const t = S.tool;
    SECRETS[t.id] = Object.fromEntries([...sensitiveKeys(t)].map(k => [k, S.opts[k]]));
    const keep = S.keepHistory && !t.sensitiveInput && S.input.length <= MAX_SAVED_INPUT;
    store.set('s.' + t.id, { input: keep ? S.input : '', opts: keptOpts(t, S.opts) });
    if (keep) trimSavedInputs(t.id);
  }, 250);
  function trimSavedInputs(current) {
    const ids = Object.keys(PREFS).filter(k => k.startsWith('s.')).map(k => k.slice(2));
    let total = ids.reduce((n, id) => n + ((PREFS['s.' + id] || {}).input || '').length, 0);
    if (total <= MAX_SAVED_INPUTS) return;
    const age = id => { const i = S.recent.indexOf(id); return i < 0 ? Infinity : i; };
    for (const id of ids.filter(id => id !== current).sort((a, b) => age(b) - age(a))) {
      const st = PREFS['s.' + id];
      if (!st || !st.input) continue;
      total -= st.input.length;
      store.set('s.' + id, Object.assign({}, st, { input: '' }));
      if (total <= MAX_SAVED_INPUTS) break;
    }
  }

  function sanitizeOpts(t, o) {
    const out = defaults(t);
    for (const opt of t.options || []) {
      const v = o && o[opt.key];
      if (v == null) continue;
      const sv = String(v);
      if (opt.type === 'select') {
        const hit = opt.choices.find(c => c.toLowerCase() === sv.toLowerCase());
        if (hit) out[opt.key] = hit;
      } else if (opt.type === 'bool') out[opt.key] = sv === 'true' || sv === '1' ? 'true' : 'false';
      else out[opt.key] = sv;
    }
    return out;
  }

  function openTool(t, preset) {
    const same = S.tool && S.tool.id === t.id;
    if (!same) {
      stopTicker();
      const saved = store.get('s.' + t.id, null);
      S.tool = t;
      S.input = saved ? saved.input || '' : '';
      S.opts = sanitizeOpts(t, Object.assign({}, saved && saved.opts, SECRETS[t.id]));
      S.data = null; S.dataName = ''; S.dataURL = '';
      S.result = null; S.outTab = 0; S.suggest = null; S.diffExpand = false; S.lastGood = null;
      S.recent = [t.id, ...S.recent.filter(x => x !== t.id)].slice(0, 8);
      store.set('recent', S.recent);
    }
    if (preset) {
      if (preset.input != null) S.input = preset.input;
      if (preset.opts) S.opts = sanitizeOpts(t, Object.assign({}, S.opts, preset.opts));
      if (preset.data) { S.data = preset.data; S.dataName = preset.dataName || 'file'; S.dataURL = preset.dataURL || ''; if (preset.input == null) S.input = ''; }
      saveState();
    }
    setTitle(t.name + ' — devknife');
    renderTool();
    if (!same) renderSidebar();
    markActive();
    syncURL();
    run(true);
    if (!window.matchMedia('(max-width: 900px)').matches) {
      const ed = $('#main textarea.editor');
      if (ed && (!same || preset)) ed.focus({ preventScroll: true });
    }
  }

  // ------------------------------------------------------------------ running
  let runTimer, ticker = null, runCtl = null;
  function stopTicker() { if (ticker) { clearInterval(ticker); ticker = null; } }
  function scheduleRun() {
    clearTimeout(runTimer);
    const n = S.input.length;
    runTimer = setTimeout(run, n > 500000 ? 600 : n > 50000 ? 250 : 90);
  }
  async function run(immediate) {
    clearTimeout(runTimer);
    const t = S.tool;
    if (!t) return;
    const seq = ++S.seq;
    // Abort the superseded request: the server then drops it if it hasn't
    // started, instead of computing a result nobody will see.
    if (runCtl) { runCtl.abort(); runCtl = null; }
    if (t.input === 'text' && !S.input && !S.data) {
      S.running = false;
      S.result = null;
      S.lastGood = null;
      renderOutput();
      renderStatus();
      if (mainGutter) mainGutter();
      return;
    }
    S.running = true;
    const body = { input: S.input, opts: S.opts };
    if (S.data) body.data = S.data;
    const ctl = runCtl = new AbortController();
    let res;
    try {
      res = await api('api/run/' + encodeURIComponent(t.id), body, ctl.signal);
    } catch (e) {
      if (ctl.signal.aborted) return;
      res = { error: 'devknife is not responding: ' + e.message };
    }
    if (runCtl === ctl) runCtl = null;
    if (seq !== S.seq || S.tool !== t) return;
    res._input = body.input; // what match offsets (regex view) refer to
    S.running = false;
    S.result = res;
    // Remember the last good result so a half-typed input doesn't blank the output.
    if (!res.error && !S.data) S.lastGood = res;
    renderOutput();
    renderStatus();
    if (mainGutter) mainGutter();
    if (!res.error) recordHistory();
    // live clock for "now"
    if (t.id === 'time' && !S.input.trim()) {
      if (!ticker) ticker = setInterval(() => { if (S.tool && S.tool.id === 'time' && !S.input.trim() && !document.hidden) run(); else stopTicker(); }, 1000);
    } else stopTicker();
    void immediate;
  }

  // ------------------------------------------------------------------ history
  const recordHistory = debounce(() => {
    if (!S.keepHistory || !S.tool || !S.result || S.result.error) return;
    const t = S.tool;
    if (t.input === 'none' || t.sensitiveInput) return;
    const input = S.input;
    if (!input.trim() || input.length > 20000) return;
    const top = S.history[0];
    const entry = { id: t.id, input, opts: keptOpts(t, S.opts), ts: Date.now() };
    if (top && top.id === t.id && (top.input === input || input.startsWith(top.input) || top.input.startsWith(input))) S.history[0] = entry;
    else S.history.unshift(entry);
    S.history = S.history.filter((e, i) => i === 0 || !(e.id === entry.id && e.input === entry.input)).slice(0, 80);
    store.set('history', S.history);
  }, 1500);

  function showHistory(onlyCurrent = !!S.tool) {
    const list = S.history.filter(e => !onlyCurrent || (S.tool && e.id === S.tool.id));
    const body = h('div');
    const filter = h('label', { class: 'toggle' }, h('input', { type: 'checkbox', checked: onlyCurrent, onchange: e => showHistory(e.target.checked) }), S.tool ? `Only ${S.tool.name}` : 'Only current tool');
    const keep = h('label', { class: 'toggle', title: 'Keep each tool\'s last input and a history of inputs in devknife\'s settings file. Secrets (signing keys, HMAC keys, passwords) are never saved.' },
      h('input', { type: 'checkbox', checked: S.keepHistory, onchange: e => setKeepHistory(e.target.checked) }), 'Remember inputs and history on this device');
    body.append(h('div', { style: { display: 'flex', gap: '16px', 'flex-wrap': 'wrap', 'align-items': 'center', padding: '6px 0' } }, S.tool && filter, keep,
      h('button', { class: 'btn', style: { 'margin-left': 'auto' }, onclick: () => { S.history = []; store.set('history', []); showHistory(onlyCurrent); toast('History cleared'); } }, 'Clear all')));
    if (!list.length) body.append(h('p', { class: 'muted' }, 'Nothing yet. Inputs you work on are kept here (locally, never uploaded).'));
    for (const e of list.slice(0, 60)) {
      const t = S.byKey[e.id];
      if (!t) continue;
      body.append(h('button', { class: 'hist-item', onclick: () => { closeDialog(); go(e.id, { input: e.input, opts: e.opts }); } },
        h('div', { class: 'ht' }, h('b', null, t.name), h('span', null, new Date(e.ts).toLocaleString())),
        h('code', null, e.input.slice(0, 400))));
    }
    openDialog('History', body);
  }

  function setKeepHistory(on) {
    S.keepHistory = on;
    store.set('keepHistory', on);
    if (on) { saveState(); return; }
    // Forget the inputs that reopening a tool would restore.
    for (const k of Object.keys(PREFS)) if (k.startsWith('s.') && PREFS[k] && PREFS[k].input) store.set(k, Object.assign({}, PREFS[k], { input: '' }));
    toast('Inputs are no longer saved. Use “Clear all” to delete past history.', 3500);
  }

  // ------------------------------------------------------------------ dialogs
  let lastFocus = null;
  function openDialog(title, body) {
    if ($('#dialog').hidden) lastFocus = document.activeElement;
    $('#dialog-title').textContent = title;
    $('#dialog-body').replaceChildren(body);
    $('#dialog').hidden = false;
    $('#dialog [data-close]').focus();
  }
  // Keep Tab inside an open modal, cycling through its controls.
  function trapTab(e, root) {
    const f = $$('button, input, select, textarea, a[href], [tabindex]:not([tabindex="-1"])', root).filter(x => !x.disabled && x.getClientRects().length);
    if (!f.length) { e.preventDefault(); return; }
    const i = f.indexOf(document.activeElement);
    const n = e.shiftKey ? (i <= 0 ? f.length - 1 : i - 1) : (i < 0 || i === f.length - 1 ? 0 : i + 1);
    e.preventDefault();
    f[n].focus();
  }
  function closeDialog() {
    $('#dialog').hidden = true;
    if (lastFocus && lastFocus.focus) lastFocus.focus();
  }
  function showShortcuts() {
    const rows = [
      ['Command palette — tools & actions', `${MOD}K`],
      ['Run now / regenerate', `${MOD}↵`],
      ['Copy output', `${MOD}⇧C`],
      ['Use output as input', `${MOD}⇧U`],
      ['Send output to another tool', `${MOD}⇧↵`],
      ['Smart paste from clipboard (auto-detect tool)', `${MOD}⇧V`],
      ['Save output to file', `${MOD}S`],
      ['Toggle favorite', `${MOD}D`],
      ['Toggle sidebar', `${MOD}B`],
      ['Previous / next tool', `⌥↑ ⌥↓`],
      ['Filter tools', '/'],
      ['History', `${MOD}Y`],
      ['This help', '?'],
    ];
    openDialog('Keyboard shortcuts', h('div', null,
      h('table', { class: 'keys' }, rows.map(([a, k]) => h('tr', null, h('td', null, a), h('td', null, k.split(' ').map(x => h('kbd', null, x)))))),
      h('p', { class: 'muted', style: { 'margin-top': '14px' } }, 'Tip: paste anywhere outside a text box and devknife opens the right tool for whatever is on your clipboard.')));
  }
  function cliCommand() {
    const t = S.tool, d = defaults(t);
    const parts = ['devknife', t.id];
    for (const o of t.options || []) {
      const v = S.opts[o.key];
      if (v === d[o.key] || o.type === 'text') continue;
      const flag = '--' + o.key.replace(/_/g, '-');
      if (o.type === 'bool') parts.push(v === 'true' ? flag : '--no-' + o.key.replace(/_/g, '-'));
      else parts.push(flag, /^[\w.,:/@%+=-]+$/.test(v) ? v : `'${v.replace(/'/g, `'\\''`)}'`);
    }
    return parts.join(' ');
  }
  function showCLI() {
    const t = S.tool;
    const base = cliCommand();
    const pb = isMac ? 'pbpaste' : 'xclip -o -sel clip';
    const lines = t.input === 'none' ? [base, base + ' --copy'] : [
      `${pb} | ${base}`,
      `${base} -f input.txt`,
      `${base} --paste --copy        # clipboard in, clipboard out`,
      `${base} --json                # full result as JSON`,
    ];
    const pre = h('pre', { class: 'code', style: { background: 'var(--panel-2)', 'border-radius': '8px', 'white-space': 'pre-wrap' } }, lines.join('\n'));
    openDialog('Use from the terminal', h('div', null,
      h('p', { class: 'muted' }, 'Same tool, same options — straight from your shell. Input can come from arguments, stdin, files or the clipboard.'),
      pre,
      h('div', { style: { display: 'flex', gap: '8px', 'margin-top': '10px' } }, btn('copy', 'Copy command', () => copyText(base, 'Command copied'), { class: 'primary' })),
      h('p', { class: 'muted', style: { 'margin-top': '16px' } }, 'AI agents: run ', h('code', null, 'devknife mcp'), ' to expose every tool over the Model Context Protocol.')));
  }

  // ------------------------------------------------------------------ palette
  const P = { mode: 'open', items: [], sel: 0, suggested: [] };
  function actions() {
    const a = [
      { name: 'Home — smart paste', key: '', run: () => go('') },
      { name: 'Smart paste from clipboard', key: `${MOD}⇧V`, run: smartPasteFromClipboard },
      { name: 'Toggle theme (auto / light / dark)', key: '', run: cycleTheme },
      { name: 'Toggle sidebar', key: `${MOD}B`, run: toggleSide },
      { name: 'Keyboard shortcuts', key: '?', run: showShortcuts },
      { name: 'History', key: `${MOD}Y`, run: () => showHistory() },
    ];
    if (S.tool) a.push(
      { name: 'Copy output', key: `${MOD}⇧C`, run: copyOutput },
      { name: 'Use output as input', key: `${MOD}⇧U`, run: useAsInput },
      { name: 'Send output to…', key: `${MOD}⇧↵`, run: () => setTimeout(() => openPalette('send'), 0) },
      { name: 'Load sample input', key: '', run: loadSample },
      { name: 'Clear input', key: '', run: clearInput },
      { name: (S.favorites.includes(S.tool.id) ? 'Remove from' : 'Add to') + ' favorites', key: `${MOD}D`, run: toggleFav },
      !NATIVE && { name: 'Copy link to this tool & input', key: '', run: shareLink },
      { name: 'Use from the terminal (CLI command)', key: '', run: showCLI },
    );
    return a.filter(Boolean);
  }
  function openPalette(mode = 'open') {
    if (mode === 'send' && !outputText()) { toast('No output to send'); return; }
    P.mode = mode;
    P.suggested = [];
    if (!$('#palette').hidden) { /* reopen in new mode */ } else lastFocus = document.activeElement;
    $('#palette').hidden = false;
    const inp = $('#palette-input');
    inp.value = '';
    inp.placeholder = mode === 'send' ? 'Send output to which tool?' : 'Search tools and actions…';
    inp.focus();
    renderPalette();
    if (mode === 'send') {
      detectAPI(outputText().slice(0, 200000)).then(hits => {
        if (P.mode !== 'send' || $('#palette').hidden) return;
        P.suggested = hits.filter(x => x.score >= 40 && (!S.tool || x.id !== S.tool.id));
        renderPalette();
      }).catch(() => { });
    }
  }
  function closePalette() {
    $('#palette').hidden = true;
    if (lastFocus && lastFocus.focus && document.contains(lastFocus)) lastFocus.focus();
  }
  function renderPalette() {
    const q = $('#palette-input').value.trim().toLowerCase();
    const list = $('#palette-list');
    list.replaceChildren();
    P.items = [];
    const add = (sec, items) => {
      if (!items.length) return;
      list.append(h('div', { class: 'p-sec' }, sec));
      for (const it of items) {
        const i = P.items.length;
        P.items.push(it);
        list.append(h('div', { class: 'p-item', role: 'option', id: 'p-opt-' + i, 'aria-selected': 'false', 'data-i': i, onmousemove: () => selPalette(i, false), onclick: () => pickPalette(i) },
          h('span', { class: 'pn' }, it.name), h('span', { class: 'pd' }, it.desc || ''), it.key && h('span', { class: 'pk' }, it.key)));
      }
    };
    const toolItem = (t, opts, note) => ({ name: t.name, desc: note || t.description, key: t.id, run: () => P.mode === 'send' ? go(t.id, { input: outputText(), opts }) : go(t.id, opts ? { opts } : undefined) });
    if (P.mode === 'send') {
      if (!q) add('Suggested for this output', P.suggested.map(x => toolItem(S.byKey[x.id], x.opts, x.note)));
      add(q ? 'Tools' : 'All tools', searchTools(q).map(t => toolItem(t)));
    } else if (!q) {
      add('Recent', S.recent.filter(id => !S.tool || id !== S.tool.id).slice(0, 5).map(id => S.byKey[id]).filter(Boolean).map(t => toolItem(t)));
      add('Actions', actions().slice(0, S.tool ? 9 : 6).map(a => ({ name: a.name, key: a.key, run: a.run })));
      add('All tools', S.tools.map(t => toolItem(t)));
    } else {
      const tools = searchTools(q).slice(0, 12).map(t => toolItem(t));
      const acts = actions().map(a => [a, fuzzy(q, a.name)]).filter(x => x[1] > 20).sort((a, b) => b[1] - a[1]).map(x => ({ name: x[0].name, key: x[0].key, run: x[0].run }));
      add('Tools', tools);
      add('Actions', acts);
      if (!tools.length && !acts.length) list.append(h('div', { class: 'p-sec' }, 'No results'));
    }
    selPalette(0, true);
  }
  function selPalette(i, scroll = true) {
    P.sel = Math.max(0, Math.min(i, P.items.length - 1));
    $$('.p-item').forEach(el => {
      const on = +el.dataset.i === P.sel;
      el.classList.toggle('sel', on);
      el.setAttribute('aria-selected', String(on));
    });
    const inp = $('#palette-input');
    if (P.items.length) inp.setAttribute('aria-activedescendant', 'p-opt-' + P.sel); else inp.removeAttribute('aria-activedescendant');
    if (scroll) { const el = $(`.p-item[data-i="${P.sel}"]`); if (el) el.scrollIntoView({ block: 'nearest' }); }
  }
  function pickPalette(i) {
    const it = P.items[i];
    if (!it) return;
    $('#palette').hidden = true;
    it.run();
  }

  // ------------------------------------------------------------------ actions
  function outputText() {
    const r = S.result;
    if (!r || r.error) return '';
    if (r.view === 'commands') return commandsText(r);
    if (r.output) return r.output;
    if (r.fields && r.fields.length) return r.fields.filter(f => f.value).map(f => `${f.label}: ${f.value}`).join('\n');
    return '';
  }
  function copyOutput() { const o = outputText(); if (o) copyText(o, 'Output copied'); else toast('Nothing to copy'); }
  function setInput(v, focus) {
    S.input = v;
    const ed = $('#main textarea.editor');
    if (ed) { ed.value = v; if (focus) ed.focus(); }
    S.data = null; S.dataName = ''; S.dataURL = '';
    if (mainGutter) mainGutter();
    renderFileChip();
    saveState();
    scheduleRun();
    renderStatus();
  }
  function useAsInput() {
    if (!S.tool || S.tool.input === 'none') return;
    const o = S.result && S.result.output;
    if (!o) { toast('No output'); return; }
    setInput(o, true);
    toast('Output moved to input');
  }
  function loadSample() {
    const t = S.tool;
    if (!t) return;
    const tpl = (t.templates || [])[0];
    const opts = {};
    for (const o of textOpts(t)) if (!S.opts[o.key] && tpl && tpl.opts && tpl.opts[o.key]) opts[o.key] = tpl.opts[o.key];
    if (Object.keys(opts).length) S.opts = Object.assign({}, S.opts, opts);
    if (t.input === 'none') { run(); return; }
    renderTool();
    setInput(t.sample || '', true);
  }
  function clearInput() {
    if (!S.tool) return;
    setInput('', true);
  }
  function applyTemplate(tpl) {
    const t = S.tool;
    S.opts = sanitizeOpts(t, Object.assign({}, S.opts, tpl.opts || {}));
    if (tpl.input) S.input = tpl.input;
    S.data = null;
    saveState();
    renderTool();
    syncURL();
    run();
  }
  function toggleFav() {
    if (!S.tool) return;
    const id = S.tool.id;
    const on = !S.favorites.includes(id);
    S.favorites = on ? [...S.favorites, id] : S.favorites.filter(x => x !== id);
    store.set('fav', S.favorites);
    renderSidebar();
    markActive();
    const b = $('#fav-btn');
    if (b) b.replaceWith(favButton());
    toast(on ? 'Added to favorites' : 'Removed from favorites');
  }
  function shareLink() {
    if (!S.tool) return;
    const d = defaults(S.tool), p = new URLSearchParams();
    for (const o of S.tool.options || []) if (S.opts[o.key] !== d[o.key] && !o.sensitive && String(S.opts[o.key]).length < 2000) p.set(o.key, S.opts[o.key]);
    if (S.input && S.input.length <= 6000 && !S.tool.sensitiveInput) p.set('in', b64urlEncode(S.input));
    const qs = p.toString();
    copyText(location.origin + location.pathname + '#/' + S.tool.id + (qs ? '?' + qs : ''), 'Link copied');
  }
  const EXT = { json: 'json', jsonschema: 'json', html: 'html', xml: 'xml', css: 'css', scss: 'scss', less: 'less', javascript: 'js', js: 'js', typescript: 'ts', go: 'go', rust: 'rs', python: 'py', swift: 'swift', kotlin: 'kt', java: 'java', csharp: 'cs', ruby: 'rb', yaml: 'yaml', sql: 'sql', csv: 'csv', diff: 'diff', jsx: 'jsx', erb: 'erb', bash: 'sh', httpie: 'sh' };
  async function download(name, blob) {
    if (NATIVE) {
      const buf = new Uint8Array(await blob.arrayBuffer());
      let bin = '';
      for (let i = 0; i < buf.length; i += 0x8000) bin += String.fromCharCode.apply(null, buf.subarray(i, i + 0x8000));
      try {
        const path = await window.dkSave(TOKEN, name, btoa(bin));
        toast('Saved to ' + path.replace(/^\/Users\/[^/]+|^\/home\/[^/]+/, '~'));
        window.dkReveal(TOKEN, path).catch(() => { });
      } catch (e) { toast('Save failed: ' + e); }
      return;
    }
    const a = h('a', { href: URL.createObjectURL(blob), download: name });
    document.body.append(a);
    a.click();
    setTimeout(() => { URL.revokeObjectURL(a.href); a.remove(); }, 1000);
  }
  function dataURLToBlob(u) {
    const [meta, b64] = u.split(',');
    const mime = meta.slice(5).replace(';base64', '');
    const bin = atob(b64);
    const arr = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
    return new Blob([arr], { type: mime });
  }
  function saveOutput() {
    const r = S.result;
    if (!r || r.error) { toast('Nothing to save'); return; }
    if (r.image && (r.view === 'image')) {
      const blob = dataURLToBlob(r.image);
      download(`${S.tool.id}.${(blob.type.split('/')[1] || 'bin').replace('svg+xml', 'svg')}`, blob);
      return;
    }
    const o = outputText();
    if (!o) { toast('Nothing to save'); return; }
    download(`${S.tool.id}-output.${EXT[(r.lang || '').toLowerCase()] || 'txt'}`, new Blob([o], { type: 'text/plain;charset=utf-8' }));
  }
  async function copyImage() {
    const r = S.result;
    if (!r || !r.image) return;
    try {
      const blob = dataURLToBlob(r.image);
      if (blob.type !== 'image/png' || !window.ClipboardItem) throw new Error('unsupported');
      await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })]);
      toast('Image copied');
    } catch {
      copyText(r.image, 'Data URL copied');
    }
  }

  // smart paste: detect the content and open the best tool
  async function smartPaste(text, source) {
    if (!text || !text.trim()) return;
    let hits = [];
    try { hits = await detectAPI(text.slice(0, 500000)); } catch { }
    const top = hits[0];
    if (S.tool) {
      const here = hits.find(x => x.id === S.tool.id);
      if (here || !top || top.score < 50) {
        setInput(text, true);
        if (here && here.opts) applyDetectedOpts(here);
        return;
      }
    }
    if (!top || top.score < 30) {
      go('inspect', { input: text });
      toast('Opened in String Inspector');
      return;
    }
    go(top.id, { input: text, opts: top.opts });
    toast(`${source || 'Detected'}: ${top.note} → ${top.name}`);
  }
  async function smartPasteFromClipboard() {
    const t = await readClipboard();
    if (t != null) smartPaste(t, 'Clipboard');
  }
  function applyDetectedOpts(hit) {
    let changed = [];
    for (const [k, v] of Object.entries(hit.opts || {})) {
      if (S.opts[k] !== undefined && S.opts[k] !== v) { S.opts[k] = v; changed.push(v); }
    }
    if (changed.length) {
      saveState();
      renderOpts();
      syncURL();
      scheduleRun();
      toast('Switched to ' + changed.join(', '));
    }
  }

  // ------------------------------------------------------------------ files
  // The API takes 48 MB per request; base64 makes binary files a third bigger.
  const MAX_BINARY = 36 * 1048576, MAX_TEXT = 45 * 1048576;
  const tooBig = (file, limit) => {
    if (file.size <= limit) return false;
    toast(`${file.name} is too large (${fmtBytes(file.size)}; ${fmtBytes(limit)} max here). Use the terminal: devknife ${S.tool ? S.tool.id : '<tool>'} -f FILE`, 5000);
    return true;
  };
  function readFile(file) {
    const t = S.tool;
    const isImg = file.type.startsWith('image/');
    const wantsBinary = t && (t.input === 'image');
    if (tooBig(file, isImg || wantsBinary ? MAX_BINARY : MAX_TEXT)) return;
    if (!t || (isImg && !wantsBinary)) {
      if (isImg) {
        const r = new FileReader();
        r.onload = () => { const u = r.result; go('base64-image', { data: u.split(',')[1], dataName: file.name, dataURL: u }); };
        r.readAsDataURL(file);
        return;
      }
    }
    if (wantsBinary) {
      const r = new FileReader();
      r.onload = () => {
        const u = r.result;
        S.data = u.split(',')[1] || '';
        S.dataName = file.name + ' · ' + fmtBytes(file.size);
        S.dataURL = isImg ? u : '';
        S.input = '';
        const ed = $('#main textarea.editor');
        if (ed) ed.value = '';
        if (mainGutter) mainGutter();
        if (t.id === 'qr' && isImg && S.opts.mode !== 'read') { S.opts.mode = 'read'; renderOpts(); syncURL(); }
        renderFileChip();
        run();
      };
      r.readAsDataURL(file);
      return;
    }
    const r = new FileReader();
    r.onload = () => { setInput(String(r.result)); toast('Loaded ' + file.name); };
    r.readAsText(file);
  }
  function pickFile() {
    const inp = h('input', { type: 'file', style: { display: 'none' }, onchange: () => { if (inp.files[0]) readFile(inp.files[0]); inp.remove(); } });
    document.body.append(inp);
    inp.click();
  }
  function renderFileChip() {
    const slot = $('#file-slot');
    if (!slot) return;
    slot.replaceChildren();
    if (!S.data) return;
    slot.append(h('div', { class: 'file-chip' }, S.dataURL && h('img', { src: S.dataURL, alt: '' }), h('span', null, h('span', { class: 'clipico' }, icon('paperclip')), S.dataName),
      h('button', { type: 'button', class: 'btn x', onclick: () => { S.data = null; S.dataName = ''; S.dataURL = ''; renderFileChip(); run(); } }, icon('x'), 'Remove')));
  }

  // ------------------------------------------------------------------ render: tool
  function favButton() {
    const on = S.tool && S.favorites.includes(S.tool.id);
    const b = iconBtn('star', on ? `Remove from favorites (${MOD}D)` : `Add to favorites (${MOD}D)`, toggleFav, on ? 'on' : '');
    b.id = 'fav-btn';
    return b;
  }

  function segKeys(e) {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step || e.altKey || e.metaKey || e.ctrlKey) return;
    const bs = $$('button', e.currentTarget);
    const i = bs.indexOf(document.activeElement);
    if (i < 0) return;
    e.preventDefault();
    e.stopPropagation();
    const b = bs[(i + step + bs.length) % bs.length];
    b.focus();
    b.click();
  }

  function renderOpts() {
    const bar = $('#opts');
    if (!bar) return;
    const t = S.tool;
    bar.replaceChildren();
    const opts = barOpts(t);
    for (const o of opts) {
      const id = 'opt-' + o.key;
      const set = v => { S.opts[o.key] = v; saveState(); syncURL(); scheduleRun(); };
      let ctl;
      if (o.type === 'bool') {
        bar.append(h('div', { class: 'opt', title: o.help || '' }, h('label', { class: 'toggle' },
          h('input', { type: 'checkbox', id, checked: S.opts[o.key] === 'true', onchange: e => set(e.target.checked ? 'true' : 'false') }), o.label)));
        continue;
      }
      if (o.type === 'select' && o.choices.length <= 5 && o.choices.join('').length <= 34) {
        // Radio-group pattern: one tab stop, arrow keys move the selection.
        const cur = o.choices.includes(S.opts[o.key]) ? S.opts[o.key] : o.choices[0];
        ctl = h('div', { class: 'seg', role: 'radiogroup', 'aria-label': o.label, onkeydown: segKeys }, o.choices.map(c => h('button', {
          type: 'button', class: S.opts[o.key] === c ? 'on' : '', role: 'radio', 'aria-checked': String(S.opts[o.key] === c), tabindex: c === cur ? '0' : '-1',
          onclick: e => {
            set(c);
            $$('button', e.currentTarget.parentNode).forEach(b => {
              const on = b === e.currentTarget;
              b.classList.toggle('on', on);
              b.setAttribute('aria-checked', String(on));
              b.tabIndex = on ? 0 : -1;
            });
          },
        }, c)));
      } else if (o.type === 'select') {
        ctl = h('select', { id, onchange: e => set(e.target.value), value: S.opts[o.key] }, o.choices.map(c => h('option', { value: c }, c)));
      } else {
        const grow = ['pattern', 'path', 'replacement', 'name', 'namespace', 'custom', 'hmac_key', 'hash', 'search'].includes(o.key);
        ctl = h('input', {
          id, type: o.type === 'number' ? 'number' : 'text', class: 'mono', value: S.opts[o.key] || '', placeholder: o.placeholder || '',
          spellcheck: 'false', autocomplete: 'off', oninput: e => set(e.target.value),
        });
        bar.append(h('div', { class: 'opt' + (grow ? ' grow' : ''), title: o.help || '' }, h('label', { for: id }, o.label), ctl));
        continue;
      }
      bar.append(h('div', { class: 'opt', title: o.help || '' }, h('label', { for: id }, o.label), ctl));
    }
    if ((t.templates || []).length) {
      bar.append(h('div', { class: 'opt', style: { 'margin-left': 'auto' } }, h('select', {
        class: 'tpl', 'aria-label': 'Presets', onchange: e => { const tp = t.templates[+e.target.value]; e.target.value = ''; if (tp) applyTemplate(tp); },
      }, h('option', { value: '' }, 'Presets…'), t.templates.map((tp, i) => h('option', { value: String(i) }, tp.name)))));
    }
    bar.hidden = !bar.children.length;
  }

  function renderTool() {
    const t = S.tool;
    const main = $('#main');
    const hasInput = t.input !== 'none';
    const flexText = textOpts(t).find(o => o.key === 'other');
    const head = h('header', { class: 'tool-head' },
      iconBtn('menu', 'Tools', toggleSide, 'menu-btn'),
      h('h1', null, t.name), h('span', { class: 'desc', title: t.description }, t.description),
      h('div', { class: 'head-actions' },
        iconBtn('history', `History (${MOD}Y)`, () => showHistory()),
        !NATIVE && iconBtn('link', 'Copy link to this tool & input', shareLink),
        iconBtn('term', 'Use from the terminal', showCLI),
        favButton()));
    const opts = h('div', { class: 'opts', id: 'opts' });

    let inPane = null;
    if (hasInput) {
      const ed = h('textarea', {
        class: 'editor' + (S.wrap ? ' wrap' : ''), spellcheck: 'false', autocomplete: 'off', autocapitalize: 'off', 'aria-label': 'Input',
        placeholder: (t.placeholder || 'Paste or type input') + `\n\nTip: drop a file here · ${MOD}K for all tools`, value: S.input,
        oninput: e => { S.input = e.target.value; if (S.data) { S.data = null; renderFileChip(); } saveState(); scheduleRun(); renderStatus(); },
        onkeyup: updateCursor, onclick: updateCursor, onselect: updateCursor,
        onpaste: e => {
          const f = e.clipboardData && e.clipboardData.files && e.clipboardData.files[0];
          if (f) { e.preventDefault(); readFile(f); return; }
          setTimeout(() => suggestFor(S.input), 0);
        },
      });
      const wrap = h('div', { class: 'editor-wrap' + (S.wrap ? ' wrapped' : '') }, ed);
      mainGutter = attachGutter(ed, wrap, () => (S.result && S.result.error && S.result.errorLine && !S.data ? S.result.errorLine : 0));
      const subs = textOpts(t).map(o => {
        const ta = h('textarea', {
          class: 'editor' + (S.wrap ? ' wrap' : ''), spellcheck: 'false', 'aria-label': o.label, placeholder: o.placeholder || o.help || '', value: S.opts[o.key] || '',
          oninput: e => { S.opts[o.key] = e.target.value; saveState(); scheduleRun(); },
        });
        const subWrap = h('div', { class: 'editor-wrap' + (S.wrap ? ' wrapped' : '') }, ta);
        // Line numbers only where the field is a full second document (diff's right side), not for keys or secrets.
        const gutter = o.key === 'other' ? attachGutter(ta, subWrap) : () => { };
        const load = v => { ta.value = v; S.opts[o.key] = v; gutter(); saveState(); run(); };
        return h('div', { class: 'sub-editor' + (o.key === 'other' ? ' flex' : '') },
          h('div', { class: 'pane-bar' }, h('span', { class: 'title' }, o.label),
            o.key === 'other' && btn('swap', 'Swap', () => { const a = S.input; S.input = S.opts.other || ''; S.opts.other = a; saveState(); renderTool(); run(); }),
            btn('clip', 'Clipboard', async () => { const c = await readClipboard(); if (c != null) load(c); }),
            btn('file', 'File', () => {
              const inp = h('input', { type: 'file', style: { display: 'none' }, onchange: () => { const f = inp.files[0]; if (!f) return; const r = new FileReader(); r.onload = () => load(String(r.result)); r.readAsText(f); inp.remove(); } });
              document.body.append(inp); inp.click();
            })),
          subWrap);
      });
      inPane = h('section', { class: 'pane', 'aria-label': 'Input' },
        h('div', { class: 'pane-bar' },
          h('span', { class: 'title' }, flexText ? (t.placeholder || 'Original') : 'Input'),
          btn('clip', 'Clipboard', async () => { const c = await readClipboard(); if (c != null) { setInput(c); suggestFor(c); } }, { title: 'Paste from clipboard' }),
          btn('flask', 'Sample', loadSample, { title: 'Load sample input' }),
          btn('file', 'File', pickFile, { title: 'Load a file' }),
          btn('x', 'Clear', clearInput)),
        h('div', { id: 'suggest-slot' }), h('div', { id: 'file-slot' }),
        wrap, subs);
      bindDrop(inPane, wrap);
    }

    const library = t.id === 'commands'; // a list to browse: nothing to regenerate or wrap
    const outBar = h('div', { class: 'pane-bar' },
      h('span', { class: 'title' }, library ? 'Commands' : 'Output', h('span', { class: 'tabs', id: 'out-tabs' })),
      !hasInput && !library && btn('refresh', 'Regenerate', () => run(), { class: 'primary', title: `Regenerate (${MOD}↵)` }),
      !library && iconBtn('wrap', 'Toggle line wrap', () => {
        S.wrap = !S.wrap;
        store.set('wrap', S.wrap);
        $$('#main .editor, #main pre.code').forEach(e => e.classList.toggle('wrap', S.wrap));
        $$('#main .editor-wrap').forEach(e => e.classList.toggle('wrapped', S.wrap));
        toast(S.wrap ? 'Line wrap on' : 'Line wrap off');
      }),
      btn('copy', library ? 'Copy all' : 'Copy', copyOutput, { title: library ? `Copy the listed commands as a script (${MOD}⇧C)` : `Copy output (${MOD}⇧C)` }),
      hasInput && btn('back', 'Use as input', useAsInput, { title: `Use output as input (${MOD}⇧U)` }),
      btn('send', 'Send to…', () => openPalette('send'), { title: `Send output to another tool (${MOD}⇧↵)` }),
      btn('save', 'Save', saveOutput, { title: `Save to file (${MOD}S)` }));
    // No aria-live here: the output re-renders on every keystroke. Errors announce themselves (role=alert).
    const outPane = h('section', { class: 'pane', 'aria-label': 'Output' }, outBar, h('div', { class: 'out', id: 'out' }));
    const panes = h('div', { class: 'panes' + (hasInput ? '' : ' no-input') }, inPane, outPane);
    const status = h('footer', { class: 'status', id: 'status' });
    main.replaceChildren(head, opts, panes, status);
    renderOpts();
    renderFileChip();
    renderOutput();
    renderStatus();
  }

  // Line numbers beside a textarea. With an error line, that number turns red and
  // a band marks the line in the text. Hidden while soft-wrap is on (numbers would drift).
  let mainGutter = null;
  function attachGutter(ta, wrap, errLine = () => 0) {
    const g = h('pre', { class: 'gutter', 'aria-hidden': 'true' });
    const band = h('div', { class: 'err-band', hidden: true });
    wrap.prepend(g, band);
    let lines = -1, err = -1;
    const sync = () => {
      g.scrollTop = ta.scrollTop;
      if (band.hidden) return;
      const lh = parseFloat(getComputedStyle(ta).lineHeight) || 20;
      band.style.height = lh + 'px';
      band.style.top = (parseFloat(getComputedStyle(ta).paddingTop) || 12) + (err - 1) * lh - ta.scrollTop + 'px';
    };
    const update = () => {
      const n = countLines(ta.value), e = errLine();
      if (n !== lines || e !== err) {
        lines = n; err = e;
        g.hidden = n > 100000;
        if (!g.hidden) {
          let out = '';
          for (let i = 1; i <= n; i++) out += (i > 1 ? '\n' : '') + (i === e ? `<span class="err">${i}</span>` : i);
          g.innerHTML = out;
        }
        band.hidden = !(e > 0 && e <= n);
      }
      sync();
    };
    ta.addEventListener('scroll', sync, { passive: true });
    ta.addEventListener('input', update);
    update();
    return update;
  }

  function bindDrop(zone, wrap) {
    let depth = 0;
    const hint = h('div', { class: 'drop-hint', hidden: true }, S.tool.input === 'image' ? 'Drop a file' : 'Drop a file to load it');
    wrap.append(hint);
    zone.addEventListener('dragenter', e => { if (e.dataTransfer.types.includes('Files')) { depth++; hint.hidden = false; e.preventDefault(); } });
    zone.addEventListener('dragover', e => { if (e.dataTransfer.types.includes('Files')) e.preventDefault(); });
    zone.addEventListener('dragleave', () => { if (--depth <= 0) { depth = 0; hint.hidden = true; } });
    zone.addEventListener('drop', e => {
      depth = 0; hint.hidden = true;
      const f = e.dataTransfer.files[0];
      if (f) { e.preventDefault(); e.stopPropagation(); readFile(f); }
    });
  }

  function updateCursor(e) {
    const ta = e.target;
    const before = ta.value.slice(0, ta.selectionStart);
    const ln = before.split('\n').length;
    S.cursor = { ln, col: ta.selectionStart - before.lastIndexOf('\n'), sel: ta.selectionEnd - ta.selectionStart };
    renderStatus();
  }
  function gotoLine(line, col) {
    const ta = $('#main textarea.editor');
    if (!ta) return;
    const lines = ta.value.split('\n');
    let pos = 0;
    for (let i = 0; i < Math.min(line - 1, lines.length - 1); i++) pos += lines[i].length + 1;
    pos += Math.max(0, Math.min((col || 1) - 1, (lines[line - 1] || '').length));
    ta.focus();
    ta.setSelectionRange(pos, Math.min(pos + 1, ta.value.length));
    const lh = parseFloat(getComputedStyle(ta).lineHeight) || 20;
    ta.scrollTop = Math.max(0, (line - 4) * lh);
  }

  // suggestion banner after pasting into a tool's input
  const suggestFor = debounce(async text => {
    if (!S.tool || !text || !text.trim()) return;
    const tool = S.tool;
    let hits = [];
    try { hits = await detectAPI(text.slice(0, 300000)); } catch { return; }
    if (S.tool !== tool) return;
    const here = hits.find(x => x.id === tool.id);
    if (here) { applyDetectedOpts(here); }
    const top = hits[0];
    const slot = $('#suggest-slot');
    if (!slot) return;
    slot.replaceChildren();
    if (!top || top.id === tool.id || top.score < 75 || (here && here.score >= 50)) return;
    const openIt = () => go(top.id, { input: S.input, opts: top.opts });
    slot.append(h('div', { class: 'suggest' }, h('span', null, `Looks like ${top.note.replace(/^./, c => c.toLowerCase())}.`),
      h('button', { class: 'btn primary', onclick: openIt }, `Open in ${top.name}`),
      h('button', { class: 'btn', onclick: () => slot.replaceChildren() }, 'Dismiss')));
  }, 120);

  // ------------------------------------------------------------------ render: output
  const VIEW = {
    jwt: { hideOutput: true, render: viewJWT },
    regex: { render: viewRegex },
    diff: { tabs: ['Visual', 'Text'], hideFields: true, hideOutput: true, render: viewDiff },
    preview: { tabs: ['Preview', 'HTML'], hideOutput: true, hideFields: true, render: viewPreview },
    color: { render: viewColor, outputTitle: 'Code' },
    image: { render: viewImage, wrapOutput: true },
    // The view shows its own "no match" state, which also counts your commands.
    commands: { render: viewCommands, hideOutput: true, hideFields: true, hideWarnings: true },
  };

  function renderTabs(meta) {
    const el = $('#out-tabs');
    if (!el) return;
    el.replaceChildren();
    if (!meta || !meta.tabs) return;
    meta.tabs.forEach((name, i) => el.append(h('button', { type: 'button', class: S.outTab === i ? 'on' : '', onclick: () => { S.outTab = i; renderOutput(); } }, name)));
  }

  function renderOutput() {
    const out = $('#out');
    if (!out) return;
    const scroll = out.scrollTop;
    out.replaceChildren();
    const r = S.result, t = S.tool;
    if (!r) {
      renderTabs(null);
      if (t.input === 'text' && !S.input) {
        out.append(h('div', { class: 'empty' },
          h('div', { class: 'big' }, 'Paste, type or drop something'),
          h('div', null, t.description),
          h('div', { class: 'row' }, btn('clip', 'Paste clipboard', async () => { const c = await readClipboard(); if (c != null) setInput(c, true); }), btn('flask', 'Try the sample', loadSample))));
      }
      return;
    }
    if (r.error) {
      renderTabs(null);
      const [first, ...rest] = r.error.split('\n');
      const n = h('div', { class: 'notice err', role: 'alert' }, icon('xcircle'), h('div', { style: { flex: '1', 'min-width': '0' } }, first, rest.length ? h('pre', null, rest.join('\n')) : null));
      if (r.errorLine) n.append(h('button', { type: 'button', class: 'btn', onclick: () => gotoLine(r.errorLine, r.errorCol) }, `Go to line ${r.errorLine}`));
      out.append(n);
      // Keep the previous result visible (dimmed) so the pane doesn't flash empty mid-edit.
      const lg = S.lastGood;
      if (lg && lg.output && !lg.view && !S.data) {
        out.append(h('div', { class: 'section-title' }, 'Last valid output'),
          h('pre', { class: 'code stale' + (S.wrap ? ' wrap' : ''), 'aria-label': 'Last valid output', html: highlight(lg.output, lg.lang) }));
      }
      out.scrollTop = scroll;
      return;
    }
    const meta = r.view && VIEW[r.view];
    if (!(meta && meta.hideWarnings)) for (const w of r.warnings || []) out.append(h('div', { class: 'notice warn' }, icon('alert'), h('span', null, w)));
    renderTabs(meta);
    const textTab = meta && meta.tabs && S.outTab === 1;
    let hideOutput = false, hideFields = false;
    if (meta && !textTab) {
      const res = meta.render(r, out) || {};
      hideOutput = res.hideOutput ?? meta.hideOutput;
      hideFields = res.hideFields ?? meta.hideFields;
    }
    if (!hideFields && r.fields && r.fields.length) out.append(fieldsTable(r.fields));
    if (!hideOutput && r.output) {
      const showTitle = (r.fields && r.fields.length && !hideFields) || (meta && !textTab);
      if (showTitle) out.append(h('div', { class: 'section-title' }, (meta && meta.outputTitle) || 'Output', h('button', { class: 'btn', onclick: copyOutput }, icon('copy'), 'Copy')));
      const lang = r.view === 'diff' && S.opts.mode !== 'lines' ? 'worddiff' : r.lang;
      out.append(h('pre', { class: 'code' + (S.wrap || (meta && meta.wrapOutput) ? ' wrap' : ''), html: highlight(r.output, lang) }));
    }
    if (!out.children.length) out.append(h('div', { class: 'empty' }, h('div', { class: 'big' }, 'Empty result')));
    out.scrollTop = scroll;
  }

  function fieldsTable(fields) {
    return h('table', { class: 'fields' }, h('tbody', null, fields.filter(f => f.value !== '').map(f => h('tr', null,
      h('td', { class: 'k' }, f.label),
      h('td', { class: 'v' }, f.color && h('span', { class: 'swatch', style: { '--sw': `linear-gradient(${f.color}, ${f.color})` } }), f.value),
      h('td', { class: 'c' }, h('button', { class: 'icon-btn copy-mini', title: 'Copy ' + f.label, 'aria-label': 'Copy ' + f.label, onclick: () => copyText(f.value, f.label + ' copied') }, icon('copy')))))));
  }

  function viewJWT(r, out) {
    const d = r.data;
    if (!d || !d.parts) return { hideOutput: false };
    const [a, b, c] = d.parts;
    out.append(h('div', { class: 'jwt-token', html: `<span class="jwt-h">${esc(a || '')}</span><span class="jwt-dot">.</span><span class="jwt-p">${esc(b || '')}</span><span class="jwt-dot">.</span><span class="jwt-s">${esc(c || '')}</span>` }));
    // Only 'verified' gets the green check; 'unsigned' (alg: none) is a warning, never a pass.
    const cls = d.status === 'verified' ? 'ok' : d.status === 'invalid' ? 'err' : 'warn';
    const mark = d.status === 'verified' ? 'check' : d.status === 'invalid' ? 'xcircle' : d.status === 'unsigned' ? 'alert' : 'circle';
    out.append(h('div', { class: 'notice ' + cls }, icon(mark), h('span', null, d.message || d.status)));
    out.append(h('div', { class: 'jwt-cols' },
      h('div', null, h('h4', { class: 'jwt-h' }, 'Header'), h('pre', { class: 'code', html: highlight(d.header || '', 'json') })),
      h('div', null, h('h4', { class: 'jwt-p' }, 'Payload'), h('pre', { class: 'code', html: highlight(d.payload || '', 'json') }))));
    return {};
  }

  function viewRegex(r, out) {
    const d = r.data || {};
    const ms = d.matches || [];
    const text = r._input ?? S.input; // offsets refer to the input that was run, not newer typing
    out.append(h('div', { class: 'section-title' }, ms.length ? `${ms.length}${ms.length >= 10000 ? '+' : ''} match${ms.length === 1 ? '' : 'es'}` : 'No matches'));
    if (text) {
      let html = '', pos = 0;
      ms.slice(0, 3000).forEach((m, i) => {
        if (m.s < pos || m.e <= m.s) return;
        html += esc(text.slice(pos, m.s)) + `<mark class="m${i % 2}" data-i="${i}">${esc(text.slice(m.s, m.e))}</mark>`;
        pos = m.e;
      });
      html += esc(text.slice(pos));
      out.append(h('div', { class: 'rx-text', html }));
    }
    if (ms.length) {
      const rows = ms.slice(0, 500).map((m, i) => h('tr', {
        onclick: () => {
          $$('mark.cur', out).forEach(x => x.classList.remove('cur'));
          const mk = $(`mark[data-i="${i}"]`, out);
          if (mk) { mk.classList.add('cur'); mk.scrollIntoView({ block: 'nearest' }); }
          const ta = $('#main textarea.editor');
          if (ta) { ta.focus(); ta.setSelectionRange(m.s, m.e); }
        },
      }, h('td', { class: 'n' }, i + 1), h('td', null, m.text === '' ? h('i', { class: 'muted' }, '(empty)') : m.text,
        (m.groups || []).length ? h('div', null, m.groups.map((g, gi) => h('span', { class: 'grp' }, h('b', null, (g.name || '$' + (gi + 1)) + ' '), g.s < 0 ? '—' : g.text))) : null),
        h('td', { class: 'n' }, `${m.s}–${m.e}`)));
      out.append(h('table', { class: 'matches' }, h('thead', null, h('tr', null, h('th', null, '#'), h('th', null, 'Match / groups'), h('th', null, 'Range'))), h('tbody', null, rows)));
    }
    return { hideOutput: S.opts.mode === 'matches' };
  }

  function splitLines(s) {
    const a = s.split('\n');
    if (a.length > 1 && a[a.length - 1] === '') a.pop();
    return a;
  }
  function pairHL(a, b) {
    let p = 0;
    while (p < a.length && p < b.length && a[p] === b[p]) p++;
    let s = 0;
    while (s < a.length - p && s < b.length - p && a[a.length - 1 - s] === b[b.length - 1 - s]) s++;
    const mid = (x, tag) => x.slice(p, x.length - s) ? `<${tag} class="dw">${esc(x.slice(p, x.length - s))}</${tag}>` : '';
    return [esc(a.slice(0, p)) + mid(a, 'del') + esc(a.slice(a.length - s)), esc(b.slice(0, p)) + mid(b, 'ins') + esc(b.slice(b.length - s))];
  }
  function viewDiff(r, out) {
    const d = r.data || {};
    const ops = d.ops || [];
    const field = l => ((r.fields || []).find(f => f.label === l) || {}).value || '';
    if (ops.every(o => o.op === 0)) {
      out.append(h('div', { class: 'notice ok' }, icon('check'), h('span', null, 'The texts are identical', S.opts.ignore_ws === 'true' || S.opts.ignore_case === 'true' ? ' (with the current ignore options)' : '')));
      return {};
    }
    out.append(h('div', { class: 'diff-stat' }, h('span', { class: 'a' }, '+' + field('Additions')), h('span', { class: 'd' }, '−' + field('Deletions')), h('span', { class: 'muted' }, field('Similarity') + ' similar')));
    const nav = h('div', { class: 'diff-nav' });
    out.append(nav);
    const body = h('div');
    out.append(body);
    if (d.mode === 'lines') {
      const rows = [];
      let la = 1, lb = 1;
      for (let i = 0; i < ops.length; i++) {
        const o = ops[i], lines = splitLines(o.text);
        if (o.op === 0) lines.forEach(l => rows.push({ a: l, b: l, na: la++, nb: lb++, eq: true }));
        else if (o.op === -1) {
          const nx = ops[i + 1];
          const ins = nx && nx.op === 1 ? splitLines(nx.text) : [];
          if (ins.length) i++;
          const n = Math.max(lines.length, ins.length);
          for (let k = 0; k < n; k++) rows.push({ a: lines[k], b: ins[k], na: k < lines.length ? la++ : null, nb: k < ins.length ? lb++ : null, start: k === 0 });
        } else lines.forEach((l, k) => rows.push({ a: undefined, b: l, na: null, nb: lb++, start: k === 0 }));
      }
      // fold long unchanged runs
      const CTX = 3, shown = [];
      for (let i = 0; i < rows.length;) {
        if (!rows[i].eq) { shown.push(rows[i++]); continue; }
        let j = i;
        while (j < rows.length && rows[j].eq) j++;
        const run = rows.slice(i, j);
        const head = i === 0 ? 0 : CTX, tail = j === rows.length ? 0 : CTX;
        if (!S.diffExpand && run.length > head + tail + 2) {
          shown.push(...run.slice(0, head), { fold: run.length - head - tail }, ...run.slice(run.length - tail));
        } else shown.push(...run);
        i = j;
      }
      const split = S.diffLayout === 'split';
      const tb = h('tbody');
      for (const row of shown) {
        if (row.fold) {
          tb.append(h('tr', { style: { cursor: 'pointer' }, onclick: () => { S.diffExpand = true; renderOutput(); } }, h('td', { colspan: split ? 4 : 3, class: 'muted', style: { background: 'var(--panel-2)', padding: '2px 12px', 'font-family': 'var(--sans)' } }, `⋯ ${row.fold} unchanged lines — click to expand`)));
          continue;
        }
        let ha = row.a !== undefined ? esc(row.a) : '', hb = row.b !== undefined ? esc(row.b) : '';
        if (!row.eq && row.a !== undefined && row.b !== undefined) [ha, hb] = pairHL(row.a, row.b);
        if (split) {
          tb.append(h('tr', { class: row.start ? 'chg' : '' },
            h('td', { class: 'ln' }, row.na ?? ''), h('td', { class: row.eq ? '' : row.a === undefined ? 'blank' : 'del', html: ha }),
            h('td', { class: 'ln sep' }, row.nb ?? ''), h('td', { class: row.eq ? '' : row.b === undefined ? 'blank' : 'ins', html: hb })));
        } else if (row.eq) {
          tb.append(h('tr', null, h('td', { class: 'ln' }, row.na), h('td', { class: 'ln' }, row.nb), h('td', { html: ' ' + ha })));
        } else {
          if (row.a !== undefined) tb.append(h('tr', { class: row.start ? 'chg' : '' }, h('td', { class: 'ln' }, row.na), h('td', { class: 'ln' }), h('td', { class: 'del', html: '-' + ha })));
          if (row.b !== undefined) tb.append(h('tr', { class: row.start && row.a === undefined ? 'chg' : '' }, h('td', { class: 'ln' }), h('td', { class: 'ln' }, row.nb), h('td', { class: 'ins', html: '+' + hb })));
        }
      }
      const cols = split ? [h('col', { style: { width: '44px' } }), h('col'), h('col', { style: { width: '44px' } }), h('col')] : [h('col', { style: { width: '44px' } }), h('col', { style: { width: '44px' } }), h('col')];
      body.append(h('table', { class: 'sbs' }, h('colgroup', null, cols), tb));
    } else {
      body.append(h('div', { class: 'inline-diff', html: ops.map(o => o.op === 0 ? esc(o.text) : o.op < 0 ? `<del class="dw chg">${esc(o.text)}</del>` : `<ins class="dw chg">${esc(o.text)}</ins>`).join('') }));
    }
    const chgs = $$('.chg', body);
    let cur = -1;
    const label = h('span', null, `${chgs.length} change${chgs.length === 1 ? '' : 's'}`);
    const jump = dlt => {
      if (!chgs.length) return;
      if (cur >= 0) chgs[cur].classList.remove('cur');
      cur = (cur + dlt + chgs.length) % chgs.length;
      chgs[cur].classList.add('cur');
      chgs[cur].scrollIntoView({ block: 'center', behavior: 'smooth' });
      label.textContent = `Change ${cur + 1} of ${chgs.length}`;
    };
    nav.append(iconBtn('up', 'Previous change', () => jump(-1)), iconBtn('down', 'Next change', () => jump(1)), label);
    if (d.mode === 'lines') nav.append(h('div', { class: 'seg', style: { 'margin-left': '8px' } },
      ['split', 'unified'].map(m => h('button', { type: 'button', class: S.diffLayout === m ? 'on' : '', onclick: () => { S.diffLayout = m; store.set('diffLayout', m); renderOutput(); } }, m === 'split' ? 'Side by side' : 'Inline'))));
    nav.append(h('div', { class: 'legend' }, h('span', { class: 'd' }, 'Removed'), h('span', { class: 'a' }, 'Added')));
    return {};
  }

  function viewPreview(r, out) {
    if (!r.previewURL) return { hideOutput: false };
    const scripts = r.data && r.data.scripts;
    out.append(h('iframe', { class: 'preview-frame', src: r.previewURL, title: 'Preview', sandbox: 'allow-popups allow-modals' + (scripts ? ' allow-scripts allow-forms' : ''), referrerpolicy: 'no-referrer' }));
    return {};
  }

  function viewColor(r, out) {
    const d = r.data || {};
    const css = d.css || '#000';
    const field = l => ((r.fields || []).find(f => f.label === l) || {}).value || '';
    const hex6 = (d.hex || '#000000').slice(0, 7);
    const picker = h('input', { type: 'color', value: hex6.toLowerCase(), 'aria-label': 'Pick a color', oninput: e => setInput(e.target.value.toUpperCase()) });
    out.append(h('div', { class: 'color-hero' },
      h('div', { class: 'color-big', style: { '--c': css }, title: 'Click to pick a color' }, picker),
      h('div', null,
        h('div', { class: 'contrast-row' },
          h('div', { class: 'contrast', style: { background: css, color: '#fff' } }, h('b', null, 'Aa'), h('small', null, 'White text · ' + field('Contrast on white'))),
          h('div', { class: 'contrast', style: { background: css, color: '#000' } }, h('b', null, 'Aa'), h('small', null, 'Black text · ' + field('Contrast on black')))),
        h('div', { class: 'color-hint' }, 'Click the swatch to pick a color. Nearest name: ', field('Nearest named color')))));
    return {};
  }

  function viewImage(r, out) {
    if (!r.image) return {};
    const img = h('img', { src: r.image, alt: 'Result image', class: S.tool.id === 'qr' ? 'pixel' : '' });
    const cap = h('span', { class: 'muted' });
    img.addEventListener('load', () => { cap.textContent = `${img.naturalWidth} × ${img.naturalHeight}px`; });
    out.append(h('div', { class: 'img-view' }, h('div', { class: 'frame' }, img), cap,
      h('div', { class: 'row' }, btn('save', 'Save image', saveOutput), btn('copy', 'Copy image', copyImage))));
    return { hideOutput: S.tool.id === 'qr' };
  }

  // ------------------------------------------------------------------ shell commands
  // Built-in commands come from the tool (r.data.items); your own live in
  // prefs ('snippets'). Placeholders look like {{port:3000}}. A value you type
  // fills every command that uses the same name, and is kept in the tool's
  // hidden "set" option in the CLI's --set format (fillCmd and
  // parseCmdValues in commands.go do the same on the Go side).
  const PARAM_RE = /\{\{([a-z][a-z0-9_]*)(?::([^{}]*))?\}\}/g;
  function paramDefaults(tmpl) {
    const d = new Map();
    for (const m of tmpl.matchAll(PARAM_RE)) if (!d.get(m[1])) d.set(m[1], m[2] || '');
    return d;
  }
  function parseValues(s) {
    const vals = {};
    for (const m of String(s || '').matchAll(/([a-z][a-z0-9_]*)=("((?:[^"\\]|\\.)*)"|'([^']*)'|(\S*))/g)) {
      vals[m[1]] = m[3] != null ? m[3].replace(/\\(.)/g, '$1') : m[4] != null ? m[4] : m[5];
    }
    return vals;
  }
  const formatValues = vals => Object.entries(vals).filter(([, v]) => v !== '')
    .map(([k, v]) => k + '=' + (/^[^\s"'\\]+$/.test(v) ? v : '"' + v.replace(/[\\"]/g, '\\$&') + '"')).join(' ');
  const cmdValues = () => parseValues(S.opts.set);
  function fillCommand(tmpl, vals, missing) {
    const d = paramDefaults(tmpl);
    return tmpl.replace(PARAM_RE, (all, name) => {
      const v = vals[name] || d.get(name) || '';
      if (!v && missing) missing.add(name);
      return v || `{{${name}}}`;
    });
  }
  // Your commands that match the search and category, shaped like built-ins.
  function myCommands() {
    const words = (S.opts.search || '').toLowerCase().split(/\s+/).filter(Boolean);
    const cat = (S.opts.category || 'all').toLowerCase();
    return store.get('snippets', []).filter(c => c && c.title && c.cmd)
      .filter(c => cat === 'all' || (c.cat || '').toLowerCase() === cat)
      .filter(c => { const hay = [c.title, c.desc, c.cmd, c.cat].join(' ').toLowerCase(); return words.every(w => hay.includes(w)); })
      .map(c => ({ id: c.id, title: c.title, desc: c.desc || '', category: c.cat || '', commands: [{ cmd: c.cmd }], own: c }));
  }
  // The list as a shell script (Copy, Save, Send to…), like the CLI prints it.
  function commandsText(r) {
    const vals = cmdValues();
    const items = [...myCommands(), ...((r.data && r.data.items) || [])];
    const q = (S.opts.search || '').trim().toLowerCase();
    if (items.length === 1 && items[0].id === q && items[0].commands.length === 1) return fillCommand(items[0].commands[0].cmd, vals) + '\n';
    return items.map(it => [
      '# ' + it.title + (it.danger ? ' (careful)' : ''),
      it.desc && '#   ' + it.desc,
      ...it.commands.flatMap(v => [v.os && '# ' + v.os + ':', fillCommand(v.cmd, vals)]),
    ].filter(Boolean).join('\n') + '\n').join('\n');
  }
  function cmdPre(tmpl, vals) {
    const d = paramDefaults(tmpl);
    const pre = h('pre', { class: 'cmd' });
    let last = 0;
    for (const m of tmpl.matchAll(PARAM_RE)) {
      pre.append(tmpl.slice(last, m.index));
      const v = vals[m[1]] || d.get(m[1]);
      pre.append(h('span', { class: 'cmd-param' + (v ? '' : ' unset'), 'data-p': m[1], 'data-d': d.get(m[1]) || '', title: m[1].replace(/_/g, ' ') }, v || m[1]));
      last = m.index + m[0].length;
    }
    pre.append(tmpl.slice(last));
    return pre;
  }
  function setCmdValue(name, value) {
    const vals = cmdValues();
    vals[name] = value;
    S.opts.set = formatValues(vals);
    saveState();
    // Update in place: re-rendering would take the focus from the field you type in.
    const out = $('#out');
    if (!out) return;
    for (const sp of $$('.cmd-param', out)) {
      if (sp.dataset.p !== name) continue;
      const v = value || sp.dataset.d;
      sp.textContent = v || name;
      sp.classList.toggle('unset', !v);
    }
    for (const inp of $$('input.cmd-in', out)) if (inp.dataset.p === name && inp !== document.activeElement) inp.value = value;
  }
  function cmdCard(it) {
    const vals = cmdValues();
    const params = new Map();
    for (const v of it.commands) for (const [k, def] of paramDefaults(v.cmd)) if (!params.get(k)) params.set(k, def);
    let card;
    const copy = v => {
      const missing = new Set();
      const text = fillCommand(v.cmd, cmdValues(), missing);
      if (missing.size) {
        toast('Fill in ' + [...missing].map(n => n.replace(/_/g, ' ')).join(', ') + ' first');
        const inp = $$('input.cmd-in', card).find(i => missing.has(i.dataset.p));
        if (inp) inp.focus();
        return;
      }
      copyText(text, 'Command copied');
    };
    let actions;
    if (it.own) {
      let armed = null;
      const del = btn('trash', 'Delete', e => {
        const b = e.currentTarget;
        if (!armed) { // no confirm(): WebViews don't always show JS dialogs
          b.classList.add('danger');
          $('.lbl', b).textContent = 'Delete?';
          armed = setTimeout(() => { armed = null; b.classList.remove('danger'); $('.lbl', b).textContent = 'Delete'; }, 3000);
          return;
        }
        clearTimeout(armed);
        store.set('snippets', store.get('snippets', []).filter(c => c.id !== it.own.id));
        renderOutput();
        toast('Command deleted');
      }, { title: 'Delete this command' });
      actions = [btn('edit', 'Edit', () => editCommand(it.own)), del];
    } else {
      actions = [btn('plus', 'Customize', () => editCommand(null, it), { title: 'Save an editable copy to your commands' })];
    }
    card = h('article', { class: 'cmd-card' + (it.danger ? ' danger' : ''), 'aria-label': it.title },
      h('div', { class: 'cmd-head' },
        h('h3', null, it.title),
        it.danger && h('span', { class: 'badge warn', title: 'Kills, deletes or discards something. Read it before you run it.' }, 'careful'),
        it.own && h('span', { class: 'badge' }, 'yours'),
        h('span', { class: 'cmd-actions' }, actions)),
      it.desc && h('p', { class: 'cmd-desc' }, it.desc),
      it.commands.map(v => h('div', { class: 'cmd-row' },
        v.os && h('span', { class: 'cmd-os' }, v.os),
        cmdPre(v.cmd, vals),
        btn('copy', 'Copy', () => copy(v), { class: 'primary', title: 'Copy this command' }))),
      params.size ? h('div', { class: 'cmd-params' }, [...params].map(([name, def]) => h('label', null, name.replace(/_/g, ' '),
        h('input', {
          type: 'text', class: 'cmd-in mono', 'data-p': name, value: vals[name] || '', placeholder: def || name,
          spellcheck: 'false', autocomplete: 'off', autocapitalize: 'off',
          oninput: e => setCmdValue(name, e.target.value),
          onkeydown: e => { if (e.key === 'Enter') { e.preventDefault(); copy(it.commands[0]); } },
        })))) : null);
    return card;
  }
  function viewCommands(r, out) {
    const d = r.data || {};
    const items = d.items || [];
    const mine = myCommands();
    const q = (S.opts.search || '').trim();
    const n = items.length + mine.length;
    const list = h('div', { class: 'cmd-list' },
      n > 0 && h('div', { class: 'cmd-top' },
        h('span', { class: 'muted' }, n ? `${n} command${n === 1 ? '' : 's'}${d.os && d.os !== 'all' ? ' for ' + d.os : ''} · placeholders are highlighted; fill them in below a command` : ''),
        btn('plus', 'New command', () => editCommand(null, null, q), { title: 'Save a command of your own' })));
    if (mine.length) list.append(h('h2', { class: 'cmd-group' }, 'Your commands'), ...mine.map(cmdCard));
    if (!n) {
      list.append(h('div', { class: 'empty' },
        h('div', { class: 'big' }, q ? `No command matches “${q}”` : 'No commands here'),
        h('div', null, 'Try fewer words, another category, or OS “all”. Or save it as your own.'),
        h('div', { class: 'row' }, btn('plus', 'New command', () => editCommand(null, null, q)))));
    }
    let group = mine.length && q && items.length ? null : '';
    for (const it of items) {
      if (group === null) { group = 'x'; list.append(h('h2', { class: 'cmd-group' }, 'Built-in')); }
      else if (!q && it.category !== group) { group = it.category; list.append(h('h2', { class: 'cmd-group' }, group)); }
      list.append(cmdCard(it));
    }
    out.append(list);
    return {};
  }
  // New command, edit one of yours (own), or start from a built-in (from).
  function editCommand(own, from, title) {
    const cats = ((S.tool.options || []).find(o => o.key === 'category') || { choices: [] }).choices.filter(c => c !== 'all');
    const src = own || (from
      ? { title: from.title, desc: from.desc, cmd: (from.commands[0] || {}).cmd || '', cat: from.category }
      : { title: title || '', desc: '', cmd: '', cat: cats.includes(S.opts.category) ? S.opts.category : '' });
    const fTitle = h('input', { type: 'text', value: src.title, maxlength: '120', autocomplete: 'off' });
    const fCmd = h('textarea', { class: 'mono', rows: '4', spellcheck: 'false', autocapitalize: 'off', value: src.cmd });
    const fDesc = h('input', { type: 'text', value: src.desc || '', maxlength: '300', autocomplete: 'off' });
    const fCat = h('select', null, h('option', { value: '' }, 'None'), cats.map(c => h('option', { value: c, selected: c === src.cat }, c)));
    const err = h('div', { class: 'form-err', role: 'alert' });
    const save = () => {
      const t = fTitle.value.trim(), c = fCmd.value.trim();
      if (!t || !c) { err.textContent = 'A title and a command are required.'; (t ? fCmd : fTitle).focus(); return; }
      const list = store.get('snippets', []).slice();
      const entry = { id: own ? own.id : 'u' + Date.now().toString(36), title: t, cmd: c, desc: fDesc.value.trim(), cat: fCat.value };
      const i = own ? list.findIndex(x => x.id === own.id) : -1;
      if (i >= 0) list[i] = entry; else list.unshift(entry);
      store.set('snippets', list);
      closeDialog();
      if (!myCommands().some(x => x.id === entry.id)) {
        // the search or category would hide it: show everything instead
        S.opts.search = '';
        S.opts.category = 'all';
        renderOpts();
        saveState();
        syncURL();
        run();
      } else renderOutput();
      toast(own ? 'Command saved' : 'Command added to your commands');
    };
    const form = h('form', { class: 'cmd-form', onsubmit: e => { e.preventDefault(); save(); } },
      h('label', null, 'Title', fTitle),
      h('label', null, 'Command', fCmd),
      h('p', { class: 'hint muted' }, 'Mark values to fill in as {{name}} or {{name:default}}, for example: kill -9 {{pid}}'),
      h('label', null, 'Description (optional)', fDesc),
      h('label', null, 'Category', fCat),
      err,
      h('div', { class: 'row' },
        h('button', { type: 'submit', class: 'btn primary' }, own ? 'Save' : 'Add command'),
        h('button', { type: 'button', class: 'btn', onclick: closeDialog }, 'Cancel')));
    openDialog(own ? 'Edit command' : 'New command', form);
    (from ? fCmd : fTitle).focus();
  }

  // ------------------------------------------------------------------ status
  function renderStatus() {
    const el = $('#status');
    if (!el || !S.tool) return;
    const r = S.result;
    const parts = [];
    if (S.running && !r) parts.push(h('span', null, 'Running…'));
    else if (r && r.error) parts.push(h('span', { class: 'bad' }, icon('xcircle'), r.errorLine ? `Error on line ${r.errorLine}` : 'Error'));
    else if (r) parts.push(h('span', { class: 'ok', title: 'Run time' }, icon('check'), `${r.ms < 1 ? '<1' : Math.round(r.ms)} ms`));
    if (S.tool.input !== 'none') {
      if (S.data) parts.push(h('span', null, S.dataName));
      else if (S.input) {
        const lines = countLines(S.input);
        parts.push(h('span', null, `${S.input.length.toLocaleString()} chars · ${lines.toLocaleString()} line${lines === 1 ? '' : 's'} · ${fmtBytes(utf8Len(S.input))}`));
        parts.push(h('span', null, `Ln ${S.cursor.ln}, Col ${S.cursor.col}${S.cursor.sel ? ` (${S.cursor.sel} selected)` : ''}`));
      }
    }
    if (r && !r.error && r.output) {
      let s = `Output ${fmtBytes(utf8Len(r.output))}`;
      if (S.input && !S.data && S.tool.category === 'Format & Minify') {
        const a = utf8Len(S.input), b = utf8Len(r.output);
        if (a) s += ` (${b <= a ? '−' : '+'}${Math.abs(Math.round((1 - b / a) * 100))}%)`;
      }
      parts.push(h('span', null, s));
    }
    el.replaceChildren(...parts, h('span', { class: 'right' }, h('span', { class: 'hint' }, `${MOD}K tools · ${MOD}↵ run · ? shortcuts`)));
  }

  // ------------------------------------------------------------------ home
  function renderHome() {
    setTitle('devknife');
    mainGutter = null;
    const main = $('#main');
    const hits = h('div', { class: 'smart-hits', id: 'smart-hits' });
    const ta = h('textarea', {
      id: 'smart-input', spellcheck: 'false', 'aria-label': 'Paste anything',
      placeholder: `Paste anything — JSON, a JWT, a Unix timestamp, a cron expression, a cURL command, Base64, a UUID, a color, SQL, HTML…\n\n${MOD}↵ opens the best match · drop a file anywhere`,
    });
    let topHit = null;
    const detect = debounce(async () => {
      const v = ta.value;
      hits.replaceChildren();
      topHit = null;
      if (!v.trim()) return;
      let res = [];
      try { res = await detectAPI(v.slice(0, 500000)); } catch { return; }
      if (ta.value !== v) return;
      res = res.filter(x => x.score >= 20);
      if (!res.length) res = [{ id: 'inspect', name: 'String Inspector', score: 10, note: 'Inspect characters and counts' }];
      topHit = res[0];
      res.forEach((x, i) => hits.append(h('button', { class: 'hit' + (i === 0 ? ' top' : ''), type: 'button', onclick: () => go(x.id, { input: v, opts: x.opts }) },
        h('span', { class: 'hn' }, x.name, h('span', { class: 'hs' }, i === 0 ? `${MOD}↵` : x.score + '%')),
        h('span', { class: 'note' }, x.note),
        h('span', { class: 'bar' }, h('i', { style: { width: x.score + '%' } })))));
    }, 120);
    ta.addEventListener('input', detect);
    ta.addEventListener('keydown', e => {
      if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && topHit) { e.preventDefault(); e.stopPropagation(); go(topHit.id, { input: ta.value, opts: topHit.opts }); }
    });
    const cats = h('div', { class: 'home-cats' }, S.cats.map(c => h('section', null, h('h2', null, c),
      h('div', { class: 'grid' }, S.tools.filter(t => t.category === c).map(t => h('a', { class: 'tool-card', href: '#/' + t.id },
        h('b', null, t.name, t.extra && h('span', { class: 'badge' }, 'EXTRA')), h('span', null, t.description)))))));
    main.replaceChildren(
      h('header', { class: 'tool-head' }, iconBtn('menu', 'Tools', toggleSide, 'menu-btn'), h('span', { class: 'head-title' }, 'devknife'), h('span', { class: 'desc' }, `${S.tools.length} offline developer tools`),
        h('div', { class: 'head-actions' }, iconBtn('history', `History (${MOD}Y)`, () => showHistory(false)))),
      h('div', { class: 'home' }, h('div', { class: 'home-inner' },
        h('h1', null, 'What are you working with?'),
        h('p', { class: 'lead' }, 'Paste it below and devknife picks the right tool. Everything runs locally — nothing leaves your machine.'),
        h('div', { class: 'smart' }, ta, hits),
        h('div', { class: 'tips' },
          h('span', null, h('kbd', null, MOD + 'K'), 'find any tool'),
          h('span', null, h('kbd', null, MOD + '⇧V'), 'smart paste from anywhere'),
          h('span', null, h('kbd', null, '⌥↑'), h('kbd', null, '⌥↓'), 'switch tools'),
          h('span', null, h('kbd', null, '?'), 'all shortcuts')),
        cats,
        h('div', { class: 'cli-box' }, h('h2', null, 'Also in your terminal'),
          h('pre', { html: [
            '<b>pbpaste | devknife</b>                   # auto-detect and run the right tool',
            '<b>devknife json -f data.json --sort</b>   # format with sorted keys',
            '<b>devknife jwt -p</b>                      # decode the JWT on your clipboard',
            '<b>devknife time now + 2d</b>               # timestamp math',
            '<b>devknife curl python -p --copy</b>       # cURL → Python, clipboard in & out',
            '<b>devknife mcp</b>                         # every tool for AI agents (MCP)',
          ].join('\n') }))))
    );
    renderSidebar();
    if (!window.matchMedia('(max-width: 900px)').matches) ta.focus();
  }

  // ------------------------------------------------------------------ global keys
  const typing = el => el && (el.tagName === 'TEXTAREA' || el.tagName === 'INPUT' || el.tagName === 'SELECT' || el.isContentEditable);
  function onKey(e) {
    const mod = e.metaKey || e.ctrlKey;
    const k = e.key.toLowerCase();
    if (!$('#palette').hidden) {
      if (e.key === 'Escape') { e.preventDefault(); closePalette(); }
      else if (e.key === 'ArrowDown' || (e.ctrlKey && k === 'n')) { e.preventDefault(); selPalette(P.sel + 1); }
      else if (e.key === 'ArrowUp' || (e.ctrlKey && k === 'p')) { e.preventDefault(); selPalette(P.sel - 1); }
      else if (e.key === 'Enter') { e.preventDefault(); pickPalette(P.sel); }
      else if (mod && k === 'k') { e.preventDefault(); closePalette(); }
      else if (e.key === 'Tab') { e.preventDefault(); $('#palette-input').focus(); }
      return;
    }
    if (!$('#dialog').hidden) {
      if (e.key === 'Escape') { e.preventDefault(); closeDialog(); }
      else if (e.key === 'Tab') trapTab(e, $('#dialog .dialog-box'));
      return;
    }
    if (mod && k === 'k') { e.preventDefault(); openPalette('open'); return; }
    if (mod && e.shiftKey && e.key === 'Enter') { e.preventDefault(); openPalette('send'); return; }
    if (mod && e.key === 'Enter') { e.preventDefault(); if (S.tool) run(true); return; }
    if (mod && e.shiftKey && k === 'c' && S.tool) { e.preventDefault(); copyOutput(); return; }
    if (mod && e.shiftKey && k === 'u') { e.preventDefault(); useAsInput(); return; }
    if (mod && e.shiftKey && k === 'v' && !typing(document.activeElement)) { e.preventDefault(); smartPasteFromClipboard(); return; }
    if (mod && !e.shiftKey && k === 's') { e.preventDefault(); if (S.tool) saveOutput(); return; }
    if (mod && !e.shiftKey && k === 'd') { e.preventDefault(); toggleFav(); return; }
    if (mod && !e.shiftKey && k === 'b') { e.preventDefault(); toggleSide(); return; }
    if (mod && !e.shiftKey && k === 'y') { e.preventDefault(); showHistory(); return; }
    if (e.altKey && (e.key === 'ArrowDown' || e.key === 'ArrowUp') && !(typing(document.activeElement) && document.activeElement.tagName === 'TEXTAREA')) { e.preventDefault(); stepTool(e.key === 'ArrowDown' ? 1 : -1); return; }
    if (e.key === 'Escape') {
      if ($('#app').classList.contains('side-open')) closeSide();
      else if (typing(document.activeElement)) document.activeElement.blur();
      return;
    }
    if (typing(document.activeElement) || mod || e.altKey) return;
    if (e.key === '?') { e.preventDefault(); showShortcuts(); }
    else if (e.key === '/') { e.preventDefault(); if ($('#app').classList.contains('side-collapsed')) toggleSide(); $('#side-filter').focus(); }
  }

  // ------------------------------------------------------------------ boot
  function showLocked() {
    $('#main').replaceChildren(h('div', { class: 'empty' },
      h('div', { class: 'big' }, 'This page needs its access key'),
      h('div', null, 'Open devknife from the link that ', h('code', null, 'devknife ui'), ' prints in your terminal (it ends in ?k=…), or use the desktop app.')));
  }
  // Older versions saved secrets and passwords; remove them from the file.
  function purgeSavedSecrets() {
    for (const t of S.tools) {
      const k = 's.' + t.id, st = PREFS[k];
      if (!st) continue;
      const sens = sensitiveKeys(t);
      if (Object.keys(st.opts || {}).some(o => sens.has(o)) || (t.sensitiveInput && st.input)) store.set(k, { input: t.sensitiveInput ? '' : st.input, opts: keptOpts(t, st.opts) });
    }
    const hist = S.history.filter(e => !(S.byKey[e.id] && S.byKey[e.id].sensitiveInput)).map(e => S.byKey[e.id] ? Object.assign({}, e, { opts: keptOpts(S.byKey[e.id], e.opts) }) : e);
    if (JSON.stringify(hist) !== JSON.stringify(S.history)) { S.history = hist; store.set('history', hist); }
  }
  async function boot() {
    try {
      const r = await fetch('api/prefs', { headers: apiHeaders() });
      if (r.status === 401) { showLocked(); return; }
      if (!r.ok) throw new Error('HTTP ' + r.status);
      PREFS = await r.json();
      prefsLoaded = true;
    } catch {
      PREFS = {};
      setTimeout(() => toast('Couldn\'t load your settings, so changes in this window won\'t be saved', 5000), 500);
    }
    if (!PREFS || typeof PREFS !== 'object' || Array.isArray(PREFS)) PREFS = {};
    Object.assign(S, {
      favorites: store.get('fav', []), recent: store.get('recent', []), history: store.get('history', []),
      keepHistory: store.get('keepHistory', true), wrap: store.get('wrap', false), diffLayout: store.get('diffLayout', 'split'), theme: store.get('theme', 'auto'),
      folded: store.get('sideFold', []),
    });
    if (!Array.isArray(S.folded)) S.folded = [];
    // Save pending changes when the page goes away or into the background
    // (closing the desktop window may not fire pagehide at all).
    const flushAll = () => { saveState.flush(); recordHistory.flush(); if (dirty.size) flushPrefs(); };
    window.addEventListener('pagehide', flushAll);
    document.addEventListener('visibilitychange', () => { if (document.hidden) flushAll(); });
    if (NATIVE) document.documentElement.classList.add('native');
    applyTheme();
    if (store.get('sideCollapsed', false)) $('#app').classList.add('side-collapsed');
    let meta;
    try { meta = await api('api/tools'); } catch (e) {
      $('#main').replaceChildren(h('div', { class: 'empty' }, h('div', { class: 'big' }, 'Cannot reach devknife'), String(e)));
      return;
    }
    S.tools = meta.tools;
    S.cats = meta.categories;
    S.version = meta.version;
    for (const t of S.tools) {
      S.byKey[t.id] = t;
      for (const a of t.aliases || []) S.byKey[a] = t;
    }
    if (prefsLoaded) purgeSavedSecrets();
    $('#version').textContent = /^v?\d/.test(S.version) ? 'v' + S.version.replace(/^v/, '') : S.version;
    $('#theme-btn').addEventListener('click', cycleTheme);
    $('#help-btn').addEventListener('click', showShortcuts);
    $('#scrim').addEventListener('click', closeSide);
    const filter = $('#side-filter');
    filter.addEventListener('input', () => { renderSidebar(); markActive(); });
    filter.addEventListener('keydown', e => {
      if (e.key === 'Enter') { const first = $('#tool-list .side-item'); if (first) { go(first.dataset.id); filter.value = ''; renderSidebar(); } }
      if (e.key === 'Escape') { filter.value = ''; renderSidebar(); filter.blur(); }
      if (e.key === 'ArrowDown' && !e.altKey) { const first = $('#tool-list .side-item, #tool-list .group-btn'); if (first) { e.preventDefault(); first.focus(); } }
    });
    $('#tool-list').addEventListener('keydown', sideArrows);
    $('#skip-btn').addEventListener('click', () => { const t = $('#main textarea, #main input:not([type=hidden])'); (t || $('#main')).focus(); });
    $('#tool-list').addEventListener('click', e => { const a = e.target.closest('.side-item'); if (a && $('#side-filter').value) { $('#side-filter').value = ''; setTimeout(renderSidebar, 0); } });
    $('#palette-input').addEventListener('input', renderPalette);
    $('#palette').addEventListener('mousedown', e => { if (e.target.id === 'palette') closePalette(); });
    $('#dialog').addEventListener('mousedown', e => { if (e.target.id === 'dialog') closeDialog(); });
    $('#dialog [data-close]').addEventListener('click', closeDialog);
    window.addEventListener('keydown', onKey);
    window.addEventListener('hashchange', route);
    // paste anywhere outside an editor → smart paste
    document.addEventListener('paste', e => {
      if (typing(e.target) || !$('#palette').hidden || !$('#dialog').hidden) return;
      const f = e.clipboardData.files && e.clipboardData.files[0];
      if (f) { e.preventDefault(); readFile(f); return; }
      const text = e.clipboardData.getData('text/plain');
      if (text) { e.preventDefault(); if (!S.tool) { const ta = $('#smart-input'); if (ta) { ta.value = text; ta.dispatchEvent(new Event('input')); ta.focus(); return; } } smartPaste(text, 'Pasted'); }
    });
    // drop anywhere
    document.addEventListener('dragover', e => { if (e.dataTransfer.types.includes('Files')) e.preventDefault(); });
    document.addEventListener('drop', e => {
      const f = e.dataTransfer.files[0];
      if (!f) return;
      e.preventDefault();
      if (S.tool) { readFile(f); return; }
      if (f.type.startsWith('image/')) { readFile(f); return; }
      if (tooBig(f, MAX_TEXT)) return;
      const r = new FileReader();
      r.onload = () => smartPaste(String(r.result), f.name);
      r.readAsText(f);
    });
    renderSidebar();
    if (NATIVE && !parseHash().id) {
      const last = store.get('lastTool', '');
      if (last && S.byKey[last]) history.replaceState(null, '', '#/' + last);
    }
    route();
  }
  boot();
})();

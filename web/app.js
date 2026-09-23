'use strict';

/* AnimalGuide — интерфейс. Состояние прогона живёт на сервере, здесь —
   только то, что показано: выбранный диалог (он же в адресе страницы),
   выбранный ход журнала и идущий ход с его потоком событий. */

const app = {
  meta: null,
  convs: [],
  conv: null,          // диалог целиком (Detail)
  selected: null,      // ход, чей журнал открыт
  tab: 'events',
  live: null,          // идущий ход: {view, events, updates}
  stream: null,        // EventSource идущего хода
  panels: {},          // панели механизмов по имени хука: function(extra, conv) → html
  windows: {},         // окна: имя → {title, render()}
};
window.app = app;

/* ---------- мелочи ---------- */

function esc(s) {
  return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
}
function md(text) {
  const safe = esc(text || '');
  try { return window.marked ? window.marked.parse(safe, { breaks: true }) : safe.replace(/\n/g, '<br>'); }
  catch (e) { return safe; }
}
function $(id) { return document.getElementById(id); }
function usd(c) {
  if (!c || !c.known) return '—';
  return '$' + (c.usd < 0.01 ? c.usd.toFixed(5) : c.usd.toFixed(4));
}
function num(n) { return (n || 0).toLocaleString('ru-RU'); }
function when(t) { return t ? new Date(t).toLocaleString('ru-RU', { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' }) : ''; }
function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return n + ' ' + one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 10 || m100 >= 20)) return n + ' ' + few;
  return n + ' ' + many;
}
let toastTimer = null;
function toast(msg, bad) {
  const el = $('toast');
  el.textContent = msg;
  el.className = 'toast' + (bad ? ' bad' : '');
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, bad ? 7000 : 3500);
}

async function api(method, path, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch (e) { data = { error: text }; }
  if (!res.ok) throw new Error(data.error || ('HTTP ' + res.status));
  return data;
}

/* ---------- загрузка ---------- */

async function boot() {
  try {
    app.meta = await api('GET', '/api/meta');
  } catch (e) {
    toast('Сервер не отвечает: ' + e.message, true);
    return;
  }
  await refreshList();
  const id = hashId();
  if (id && app.convs.some(c => c.id === id)) {
    await loadConv(id);
  } else if (app.convs.length) {
    await loadConv(app.convs[0].id);
  } else {
    render();
  }
}

function hashId() {
  const m = /c=([0-9a-f]+)/.exec(location.hash);
  return m ? m[1] : '';
}

async function refreshList() {
  try {
    app.convs = (await api('GET', '/api/conversations')).conversations || [];
  } catch (e) { toast(e.message, true); }
}

async function loadConv(id, keepSelection) {
  try {
    const d = (await api('GET', '/api/conversations/' + id)).conversation;
    app.conv = d;
    if (location.hash !== '#c=' + id) history.replaceState(null, '', '#c=' + id);
    if (!keepSelection || !d.turnList.some(t => t.id === app.selected)) {
      app.selected = d.turnList.length ? d.turnList[d.turnList.length - 1].id : null;
    }
    if (d.active && (!app.live || app.live.view.id !== d.active.id)) follow(d.active.id);
  } catch (e) {
    toast(e.message, true);
  }
  render();
}

/* ---------- отрисовка ---------- */

function render() {
  renderDialogs();
  renderReadings();
  renderBranches();
  renderContext();
  renderMechanisms();
  renderExtPanels();
  renderFeed();
  renderJournal();
  const busy = !!app.live;
  $('send-button').disabled = busy;
  $('composer-hint').textContent = busy ? 'справочник отвечает…' : '';
}

function renderDialogs() {
  const sel = $('dialog-select');
  const cur = app.conv ? app.conv.id : '';
  let html = '';
  if (!app.convs.length) html = '<option value="">— диалогов нет —</option>';
  for (const c of app.convs) {
    const title = c.title || 'без названия';
    const lane = c.lane ? ' · ' + c.lane : '';
    html += `<option value="${esc(c.id)}"${c.id === cur ? ' selected' : ''}>${esc(title)}${esc(lane)} (${c.turns})</option>`;
  }
  sel.innerHTML = html;
}

function renderReadings() {
  const m = app.meta || {};
  const c = app.conv;
  const items = [['Модель', m.model || '—']];
  if (c) {
    items.push(['Ходов', num(c.turns)]);
    items.push(['Цена диалога', usd(sumCost(c.totals.cost, c.meter.cost))]);
    items.push(['Собеседник', (c.owners || []).join(', ') || '—']);
  }
  items.push(['Сервер запущен', when(m.serverStarted)]);
  $('readings').innerHTML = items.map(([k, v]) => `<div><dt>${esc(k)}</dt><dd>${esc(v)}</dd></div>`).join('');
}

function sumCost(a, b) {
  if (!a || !a.known) return b;
  if (!b || !b.known) return a;
  return { usd: a.usd + b.usd, known: true };
}

function renderBranches() {
  const c = app.conv;
  if (!c) { $('branches').innerHTML = '<span class="hint">нет диалога</span>'; return; }
  const byParent = {};
  for (const b of c.branchTree) (byParent[b.parent || ''] = byParent[b.parent || ''] || []).push(b);
  const walk = (parent, depth) => (byParent[parent] || []).map(b =>
    `<div class="branch${b.active ? ' active' : ''}" style="padding-left:${depth * 14}px">
       <button type="button" class="branch-name" data-action="switchBranch" data-arg="${esc(b.id)}" title="Перейти в ветку">${depth ? '↳ ' : ''}${esc(b.name)}</button>
       <span class="hint">${plural(b.turns, 'ход', 'хода', 'ходов')}</span>
     </div>` + walk(b.id, depth + 1)).join('');
  let html = walk('', 0);
  html += `<div class="cps"><button type="button" class="small" data-action="mark" title="Поставить точку сохранения на конце текущей ветки">+ точка</button>`;
  for (const cp of c.checkpoints) {
    html += `<button type="button" class="small cp" data-action="fork" data-arg="${esc(cp.id)}" title="Новая ветка от точки «${esc(cp.name)}»: всё до неё — общее, после — своё">⑂ ${esc(cp.name)}</button>`;
  }
  html += '</div>';
  $('branches').innerHTML = html;
}

const blockColors = {
  system: '#8a8f86', charter: '#a3412e', profile: '#6a4c93', 'memory.long': '#2c5a85', 'collection.state': '#a5701a',
  'memory.work': '#3f8a9c', facts: '#2f6b4f', tools: '#b8a88a', history: '#d4c29c', user: '#1f2a24',
};

function lastContext() {
  const c = app.conv;
  if (!c) return null;
  for (let i = c.turnList.length - 1; i >= 0; i--) {
    const ctx = c.turnList[i].context;
    if (ctx && ctx.estimate && ctx.estimate.total) return ctx;
  }
  return null;
}

function renderContext() {
  const ctx = lastContext();
  if (!ctx) { $('context').innerHTML = '<span class="hint">появится после первого хода с моделью</span>'; return; }
  const e = ctx.estimate;
  const parts = [['system', 'системный промпт', e.system]];
  const mechs = (app.meta && app.meta.mechanisms) || [];
  for (const m of mechs) if (e.blocks && e.blocks[m.name]) parts.push([m.name, m.title, e.blocks[m.name]]);
  parts.push(['tools', 'инструменты', e.tools], ['history', 'окно истории', e.history], ['user', 'реплика', e.user]);
  const total = e.total || 1;
  let bar = '', legend = '';
  for (const [key, title, n] of parts) {
    if (!n) continue;
    const color = blockColors[key] || '#999';
    bar += `<span style="width:${(n / total * 100).toFixed(2)}%;background:${color}" title="${esc(title)}: ${num(n)}"></span>`;
    legend += `<span><i style="background:${color}"></i>${esc(title)} ${num(n)}</span>`;
  }
  const constant = e.constant || 0;
  const over = constant > 6000;
  let fact = '';
  if (ctx.firstPrompt) fact = ` · факт первого запроса ${num(ctx.firstPrompt)}, из кэша ${num(ctx.cacheHit)}`;
  $('context').innerHTML = `<div class="hint">≈ ${num(total)} токенов${fact}</div>
    <div class="scale">${bar}</div><div class="legend">${legend}</div>
    <div class="budget${over ? ' over' : ''}">постоянная часть ≈ ${num(constant)} из 6 000 (бюджет раздела 10)</div>`;
}

function renderMechanisms() {
  const c = app.conv;
  const list = c ? c.mechanisms : ((app.meta && app.meta.mechanisms) || []);
  const on = list.filter(m => m.on).length;
  $('mech-count').textContent = `включено ${on} из ${list.length}` + (c ? '' : ' (для новых диалогов)');
  $('mechanisms').innerHTML = list.map(m => {
    const cost = [];
    if (m.cost.tokens) cost.push('≈' + m.cost.tokens + ' токенов');
    if (m.cost.requests) cost.push(m.cost.requests + ' запроса на ход');
    const tip = `${m.about}\nВыключен: ${m.fallback}\nЦена: ${cost.join(', ') || 'без токенов'}${m.cost.note ? ' — ' + m.cost.note : ''}\nВид: ${(m.kind || []).join(', ')}`;
    return `<button type="button" class="mech ${m.on ? 'on' : 'off'}" data-action="toggleMechanism" data-arg="${esc(m.name)}" data-on="${m.on ? '1' : ''}"
      title="${esc(tip)}"${c ? '' : ' disabled'}><span class="dot"></span>${esc(m.title)}</button>`;
  }).join('');
}

function renderExtPanels() {
  const box = $('ext-panels');
  const c = app.conv;
  let html = '';
  if (c && c.extras) {
    for (const [name, fn] of Object.entries(app.panels)) {
      if (c.extras[name] === undefined) continue;
      try { html += fn(c.extras[name], c) || ''; } catch (e) { html += `<section class="panel"><h2>${esc(name)}</h2><span class="hint">${esc(e.message)}</span></section>`; }
    }
  }
  box.innerHTML = html;
}

/* ---------- лента ---------- */

function renderFeed() {
  const c = app.conv;
  const feed = $('feed');
  if (!c || (!c.turnList.length && !app.live)) {
    feed.innerHTML = `<div class="empty-feed"><h2>Спросите про животное</h2>
      <p>Карточка собирается только из источников — русской Википедии и GBIF. Разделы раскрываются по клику, дерево кликабельное.</p>
      <div class="examples">
        ${['рысь', 'манул', 'шурундук пятнистый', 'Сравни рысь и манула', 'Расскажи про ежа: где живёт и чем питается?']
          .map(x => `<button type="button" data-action="example" data-arg="${esc(x)}">${esc(x)}</button>`).join('')}
      </div></div>`;
    return;
  }
  const started = app.meta ? new Date(app.meta.serverStarted) : null;
  const shown = new Set();   // карточки, уже показанные выше в ленте
  const state = cardsState();
  let html = '', seamDone = false;
  c.turnList.forEach((t, i) => {
    if (started && !seamDone && i > 0 && new Date(t.started) > started && new Date(c.turnList[i - 1].started) < started) {
      html += '<div class="seam">сервер перезапущен — разговор продолжается с того же места</div>';
      seamDone = true;
    }
    if (i > 0 && t.collection && t.collection !== c.turnList[i - 1].collection) {
      html += '<div class="seam">новая подборка</div>';
    }
    html += turnHTML(t, state, shown);
  });
  if (app.live) html += liveHTML(state, shown);
  const atBottom = window.innerHeight + window.scrollY >= document.body.scrollHeight - 80;
  feed.innerHTML = html;
  if (atBottom) window.scrollTo(0, document.body.scrollHeight);
}

// cardsState — карточки текущей ветки вместе с тем, что уже пришло в идущем
// ходе: карточка по мере сборки (ФТ-14).
function cardsState() {
  const c = app.conv;
  const st = c ? JSON.parse(JSON.stringify(c.cards)) : { cards: [], comparisons: [], notFound: [] };
  if (!app.live) return st;
  for (const u of app.live.updates) {
    if (u.kind === 'card') {
      const i = st.cards.findIndex(x => x.id === u.data.id);
      if (i >= 0) st.cards[i] = mergeCard(st.cards[i], u.data); else st.cards.push(u.data);
    } else if (u.kind === 'section') {
      const card = st.cards.find(x => x.id === u.data.cardId);
      if (card) {
        const i = card.sections.findIndex(s => s.key === u.data.section.key);
        if (i >= 0) card.sections[i] = u.data.section;
      }
    } else if (u.kind === 'neighbors') {
      const card = st.cards.find(x => x.id === u.data.cardId);
      if (card) {
        card.neighbors = (card.neighbors || []).filter(n => n.nodeKey !== u.data.neighbors.nodeKey);
        card.neighbors.push(u.data.neighbors);
      }
    } else if (u.kind === 'comparison') {
      st.comparisons.push(u.data);
    }
  }
  return st;
}

function mergeCard(old, fresh) {
  const out = Object.assign({}, fresh);
  out.sections = fresh.sections.map(s => {
    const had = (old.sections || []).find(x => x.key === s.key);
    return had && (had.status === 'read' || had.status === 'none') && s.status === 'unread' ? had : s;
  });
  if ((!out.tree || !out.tree.length) && old.tree) out.tree = old.tree;
  return out;
}

const kindTitles = { section: 'раздел', node: 'узел дерева', open: 'карточка', compare: 'сравнение' };

function turnHTML(t, state, shown) {
  const click = t.kind && t.kind !== 'message';
  let html = `<div class="turn${t.id === app.selected ? ' selected' : ''}" id="turn-${esc(t.id)}">`;
  html += `<div class="bubble user${click ? ' click' : ''}">${click ? '⤷ ' : ''}${esc(t.user)}</div>`;
  html += deltasHTML(t.cards || [], state, shown, t.id);
  if (t.status === 'failed') {
    html += `<div class="bubble reply error">Ход не удался: ${esc(t.error)}</div>`;
  } else if (t.reply && !(t.route === 'card' && (t.cards || []).some(d => d.kind === 'card'))) {
    html += `<div class="bubble reply">${md(t.reply)}</div>`;
  }
  html += chipsHTML(t);
  html += `<div class="turn-meta">
    <button type="button" class="link" data-action="selectTurn" data-arg="${esc(t.id)}">журнал хода</button>
    <span>${when(t.started)}</span><span>${routeTitle(t.route)}</span>
    <span>${plural(t.totals.llmCalls, 'запрос', 'запроса', 'запросов')} к модели</span>
    <span>${usd(t.totals.cost)}</span><span>${(t.totals.seconds || 0).toFixed(1)} с</span>
    ${mechDiff(t)}
  </div></div>`;
  return html;
}

function routeTitle(r) {
  return ({ card: 'карточка', section: 'раздел по клику', node: 'узел дерева', compare: 'сравнение', lead: 'ведущий диалога', collection: 'подборка' })[r] || '';
}

// mechDiff — ход прошёл не с тем набором механизмов, что просили (откат
// пути до источников): видно прямо в ленте.
function mechDiff(t) {
  const req = t.requested || {}, eff = t.effective || {};
  const diff = Object.keys(req).filter(k => !!req[k] !== !!eff[k]);
  return diff.length ? `<span class="chip warn" title="Механизм откатился на ходу — см. журнал">откат: ${esc(diff.join(', '))}</span>` : '';
}

// chipsHTML — правки механизмов под ответом (память, профиль, подборка,
// страж): их кладут хуки в extras хода, а рисуют зарегистрированные
// обработчики.
function chipsHTML(t) {
  const chips = [];
  for (const [name, fn] of Object.entries(app.chips)) {
    if (t.extras && t.extras[name] !== undefined) {
      try { chips.push(...(fn(t.extras[name], t) || [])); } catch (e) { /* чужие данные не роняют ленту */ }
    }
  }
  return chips.length ? `<div class="chips">${chips.join('')}</div>` : '';
}
app.chips = {};

function deltasHTML(deltas, state, shown, turnId) {
  let html = '';
  for (const d of deltas) {
    if (d.kind === 'card' && d.card) {
      if (shown.has(d.card.id)) { html += `<div class="hint">карточка «${esc(d.card.name)}» — выше в ленте</div>`; continue; }
      shown.add(d.card.id);
      const cur = state.cards.find(c => c.id === d.card.id) || d.card;
      html += cardHTML(cur);
    } else if (d.kind === 'notfound' && d.notFound) {
      html += `<div class="notfound"><b>Сведений о «${esc(d.notFound.query)}» нет.</b> ${esc(d.notFound.reason)}${d.notFound.gate ? ' <span class="chip">отказал привратник — дорогие шаги не делались</span>' : ''}</div>`;
    } else if (d.kind === 'comparison' && d.comparison) {
      const idx = state.comparisons.findIndex(x => x.a.id === d.comparison.a.id && x.b.id === d.comparison.b.id);
      html += compareHTML(d.comparison, idx);
    } else if (d.kind === 'neighbors' && d.neighbors) {
      const card = state.cards.find(c => c.id === d.cardId);
      if (card && !shown.has(card.id)) { shown.add(card.id); html += cardHTML(card); }
    }
  }
  return html;
}

function whyBtn(why, label) {
  if (!why) return '';
  const tip = `Почему так: ${why.tool}${why.sourceTitle ? ' — ' + why.sourceTitle : ''}. Нажмите — откроется событие журнала с этим вызовом.`;
  return `<button type="button" class="why" data-action="why" data-turn="${esc(why.turn || '')}" data-call="${esc(why.callId || '')}" title="${esc(tip)}">${label || '?'}</button>`;
}

function cardHTML(c) {
  const rank = c.rankRu || (c.rank || '').toLowerCase();
  let html = `<article class="acard${c.unverified ? ' unverified' : ''}" data-card="${esc(c.id)}">
    <div class="acard-head"><h3>${esc(c.name)}</h3>
      ${latinHidden() ? `<span class="hint" title="${esc(c.latin)}">латынь скрыта профилем</span>` : `<span class="latin">${esc(c.latin)}</span>`}${whyBtn(c.latinWhy)}
      ${rank ? `<span class="rank">${esc(rank)}</span>` : ''}
    </div>
    <p class="summary">${esc(c.summary)} ${whyBtn(c.summaryWhy)}</p>`;
  if (c.tree && c.tree.length) {
    html += '<div class="tree">' + c.tree.map((n, i) => {
      const self = n.key === c.taxonKey;
      const title = (n.nameRu ? n.nameRu : n.name);
      const btn = self
        ? `<button type="button" class="self" disabled title="${esc(n.name)}">${esc(title)}</button>`
        : `<button type="button" data-action="node" data-card="${esc(c.id)}" data-key="${n.key}" data-name="${esc(n.nameRu || n.name)}" title="${esc((n.rankRu || n.rank) + ': ' + n.name)} — показать, кто ещё входит">${esc(title)}</button>`;
      return (i ? '<span class="arrow">›</span>' : '') + btn;
    }).join('') + ' ' + whyBtn(c.treeWhy) + '</div>';
  }
  for (const n of c.neighbors || []) {
    html += `<div class="neighbors">В «${esc(n.nodeName)}» по GBIF (${plural(n.total, 'таксон', 'таксона', 'таксонов')}) ${whyBtn(n.why)}:
      <div class="list">${n.children.map(ch => `<button type="button" class="small" data-action="open" data-arg="${esc(ch.nameRu || ch.name)}"
        title="${esc((ch.rankRu || ch.rank) + ': ' + ch.name)} — открыть карточку">${esc(ch.nameRu ? ch.nameRu + ' (' + ch.name + ')' : ch.name)}</button>`).join('')}</div></div>`;
  }
  html += '<div class="sections">';
  for (const s of c.sections || []) {
    const st = `<span class="s-status s-${esc(s.status)}">${esc(statusTitle(s.status))}</span>`;
    if (s.status === 'read' || s.status === 'none') {
      html += `<details class="section"><summary><span class="s-title">${esc(s.title)}</span>${st} ${whyBtn(s.why)}</summary>
        <div class="s-body">${s.status === 'read' ? md(s.text) : esc(s.reason)}${s.heading ? `<div class="heading">раздел статьи: «${esc(s.heading)}»</div>` : ''}</div></details>`;
    } else if (s.status === 'reading') {
      html += `<div class="section"><div class="section-head"><span class="s-title">${esc(s.title)}</span>${st}<span class="thinking">специалист читает</span></div></div>`;
    } else {
      html += `<div class="section"><div class="section-head"><span class="s-title">${esc(s.title)}</span>${st}
        <button type="button" class="small" data-action="section" data-card="${esc(c.id)}" data-topic="${esc(s.key)}"${app.live ? ' disabled' : ''}>прочитать</button>
        ${s.reason ? `<span class="hint">${esc(s.reason)}</span>` : ''}</div></div>`;
    }
  }
  html += '</div>';
  if (c.notes && c.notes.length) html += '<ul class="notes">' + c.notes.map(n => `<li>${esc(n)}</li>`).join('') + '</ul>';
  html += `<div class="sources">Источники: ${(c.sources || []).map(s => `<a href="${esc(s.url)}" target="_blank" rel="noopener">${esc(s.title)}</a>`).join(' · ')}
    <button type="button" class="small" data-action="export" data-kind="card" data-arg="${esc(c.id)}">↓ markdown</button>
    <button type="button" class="small" data-action="compareWith" data-arg="${esc(c.name)}" title="Сравнение уйдёт в отдельную ветку">сравнить с…</button>
    <button type="button" class="small" data-action="bookmark" data-arg="${esc(c.name)}" title="Закладка — в долговременную память собеседника">☆ в закладки</button></div>`;
  return html + '</article>';
}

function statusTitle(s) {
  return ({ unread: 'не прочитан', reading: 'читается', read: 'прочитан', none: 'сведений нет' })[s] || s;
}

function compareHTML(cmp, idx) {
  const cell = x => `<td class="${x.confirmed ? '' : 'nodata'}">${esc(x.text)} ${x.confirmed ? whyBtn(x.why) : ''}</td>`;
  return `<div class="compare"><b>Сравнение: ${esc(cmp.a.name)} и ${esc(cmp.b.name)}</b>
    ${idx >= 0 ? `<button type="button" class="small" data-action="export" data-kind="comparison" data-arg="${idx}">↓ markdown</button>` : ''}
    <table class="cmp"><tr><th></th><th>${esc(cmp.a.name)}</th><th>${esc(cmp.b.name)}</th></tr>
    ${cmp.rows.map(r => `<tr><th>${esc(r.aspect)}</th>${cell(r.a)}${cell(r.b)}</tr>`).join('')}</table>
    ${cmp.notes && cmp.notes.length ? '<ul class="notes">' + cmp.notes.map(n => `<li>${esc(n)}</li>`).join('') + '</ul>' : ''}</div>`;
}

function liveHTML(state, shown) {
  const l = app.live;
  const v = l.view;
  let html = `<div class="turn${v.id === app.selected ? ' selected' : ''}" id="turn-${esc(v.id)}">`;
  html += `<div class="bubble user${v.kind !== 'message' ? ' click' : ''}">${esc(v.user || kindTitles[v.kind] || '')}</div>`;
  const deltas = [];
  for (const u of l.updates) {
    if (u.kind === 'card') deltas.push({ kind: 'card', card: u.data });
    if (u.kind === 'notfound') deltas.push({ kind: 'notfound', notFound: u.data });
    if (u.kind === 'comparison') deltas.push({ kind: 'comparison', comparison: u.data });
  }
  html += deltasHTML(deltas, state, shown, v.id);
  if (v.status === 'running') {
    const last = l.events.length ? l.events[l.events.length - 1].title : 'начинаю';
    html += `<div class="bubble reply"><span class="thinking">${esc(last)}</span></div>`;
  } else if (v.error) {
    html += `<div class="bubble reply error">${esc(v.error)}</div>`;
  } else if (v.reply) {
    html += `<div class="bubble reply">${md(v.reply)}</div>`;
  }
  html += `<div class="turn-meta"><span>${plural(v.totals.llmCalls, 'запрос', 'запроса', 'запросов')} к модели</span><span>${usd(v.totals.cost)}</span></div></div>`;
  return html;
}

/* ---------- журнал ---------- */

function selectedTurn() {
  if (app.live && app.selected === app.live.view.id) return { live: true, id: app.live.view.id, events: app.live.events };
  const c = app.conv;
  if (!c) return null;
  const t = c.turnList.find(x => x.id === app.selected);
  return t ? { id: t.id, events: t.events || [], turn: t } : null;
}

function renderJournal() {
  $('tab-events').classList.toggle('active', app.tab === 'events');
  $('tab-prompts').classList.toggle('active', app.tab === 'prompts');
  const sel = selectedTurn();
  const body = $('journal-body');
  if (!sel) { body.innerHTML = '<p class="hint">Журнал хода появится здесь.</p>'; $('journal-turn').textContent = ''; return; }
  $('journal-turn').textContent = (sel.live ? 'идёт ход · ' : '') + plural(sel.events.length, 'событие', 'события', 'событий');
  if (app.tab === 'prompts') { body.innerHTML = promptsHTML(sel.events); return; }
  const open = new Set([...body.querySelectorAll('.ev.open')].map(e => e.dataset.seq));
  body.innerHTML = sel.events.filter(e => e.kind !== 'prompt').map(e => {
    const cost = e.cost && e.cost.known ? usd(e.cost) : '';
    const tk = e.tokens ? `≈${num(e.tokens.estimated)}${e.tokens.actual ? ' / ' + num(e.tokens.actual) : ''}` : '';
    const meta = [tk, cost, e.seconds ? e.seconds.toFixed(1) + 'с' : ''].filter(Boolean).join(' · ');
    let detail = e.detail || '';
    if (e.hits) detail += (detail ? '\n\n' : '') + e.hits.map(h => '• ' + h.pattern + ': ' + h.fragment).join('\n');
    if (e.data && !detail) detail = JSON.stringify(e.data, null, 2);
    return `<div class="ev ev-kind-${esc(e.kind)}${open.has(String(e.seq)) ? ' open' : ''}" data-seq="${e.seq}" data-call="${esc(e.callId || '')}">
      <div class="ev-head" data-action="toggleEvent" data-arg="${e.seq}">
        <span class="ev-seq">${e.seq}</span><span class="ev-agent">${esc(e.agent)}</span>
        <span class="ev-title">${esc(e.title)}</span>${e.via === 'mcp' ? '<span class="ev-via">MCP</span>' : ''}
        <span class="ev-cost">${esc(meta)}</span></div>
      ${detail ? `<div class="ev-detail">${esc(detail)}</div>` : ''}</div>`;
  }).join('') || '<p class="hint">событий нет</p>';
}

function promptsHTML(events) {
  const prompts = events.filter(e => e.kind === 'prompt');
  if (!prompts.length) return '<p class="hint">В этом ходе модель не вызывалась — всё сделал код (клик по узлу дерева, сохранённый раздел).</p>';
  return prompts.map(e => {
    let p;
    try { p = JSON.parse(e.detail); } catch (x) { return `<pre>${esc(e.detail)}</pre>`; }
    let html = `<div class="prompt-block"><h4><span>${esc(p.agent)}: системный промпт</span><span>≈${num(p.estimate.system)}</span></h4><pre>${esc(p.system)}</pre></div>`;
    for (const b of p.blocks || []) {
      html += `<div class="prompt-block"><h4><span>блок «${esc(b.title || b.feature)}» (${esc(b.feature)})</span><span>≈${num(b.tokens)}</span></h4><pre>${esc(b.text)}</pre></div>`;
    }
    html += `<div class="prompt-block"><h4><span>окно истории: ${plural(p.history, 'сообщение', 'сообщения', 'сообщений')}</span><span>≈${num(p.estimate.history)}</span></h4></div>`;
    html += `<div class="prompt-block"><h4><span>реплика</span><span>≈${num(p.estimate.user)}</span></h4><pre>${esc(p.user)}</pre></div>`;
    html += `<div class="prompt-block"><h4><span>инструменты (отпечаток ${esc(p.fingerprint || '—')})</span><span>≈${num(p.estimate.tools)}</span></h4><pre>${esc((p.tools || []).map(t =>
      (t.final ? '■ ' : '• ') + t.name + (t.via === 'mcp' ? ' [MCP]' : '') + ' — ' + t.description).join('\n'))}</pre></div>`;
    return html;
  }).join('<hr>');
}

/* ---------- поток идущего хода ---------- */

function follow(turnId) {
  if (app.stream) app.stream.close();
  app.live = { view: { id: turnId, status: 'running', totals: { llmCalls: 0, cost: {} }, kind: 'message' }, events: [], updates: [] };
  app.selected = turnId;
  const es = new EventSource('/api/turns/' + turnId + '/events');
  app.stream = es;
  let pending = false;
  const redraw = () => {
    if (pending) return;
    pending = true;
    requestAnimationFrame(() => { pending = false; renderFeed(); renderJournal(); });
  };
  es.addEventListener('snapshot', ev => {
    const snap = JSON.parse(ev.data);
    app.live.view = snap.view;
    app.live.events = snap.events || [];
    app.live.updates = snap.updates || [];
    render();
  });
  es.addEventListener('log', ev => { app.live.events.push(JSON.parse(ev.data)); redraw(); });
  es.addEventListener('state', ev => { app.live.view = JSON.parse(ev.data); redraw(); });
  es.addEventListener('update', ev => { app.live.updates.push(JSON.parse(ev.data)); redraw(); });
  es.addEventListener('done', ev => {
    es.close();
    const v = JSON.parse(ev.data);
    const convId = v.conversationId;
    app.stream = null;
    app.live = null;
    if (v.status === 'failed') toast('Ход не удался: ' + v.error, true);
    refreshList().then(() => loadConv(convId));
  });
  es.onerror = () => {
    // Сервер перезапущен или ход уже записан: перечитываем диалог.
    es.close();
    if (app.stream === es) {
      app.stream = null;
      app.live = null;
      if (app.conv) loadConv(app.conv.id);
    }
  };
}

/* ---------- действия ---------- */

async function sendTurn(body) {
  if (app.live) { toast('Справочник ещё отвечает на предыдущее сообщение'); return; }
  try {
    let out;
    if (!app.conv) {
      out = await api('POST', '/api/conversations', body);
    } else {
      out = await api('POST', '/api/conversations/' + app.conv.id + '/turns', body);
    }
    if (!app.conv || app.conv.id !== out.conversationId) {
      await refreshList();
      const d = (await api('GET', '/api/conversations/' + out.conversationId)).conversation;
      app.conv = d;
      history.replaceState(null, '', '#c=' + d.id);
    }
    follow(out.turn.id);
    app.live.view = out.turn;
    render();
  } catch (e) { toast(e.message, true); }
}

const actions = {
  sendComposer() {
    const ta = $('composer-text');
    const text = ta.value.trim();
    if (!text) return;
    ta.value = '';
    sendTurn({ text });
  },
  example(x) { sendTurn({ text: x }); },
  section(_, el) { sendTurn({ kind: 'section', cardId: el.dataset.card, topic: el.dataset.topic }); },
  node(_, el) { sendTurn({ kind: 'node', cardId: el.dataset.card, nodeKey: Number(el.dataset.key), nodeName: el.dataset.name }); },
  open(name) { sendTurn({ kind: 'open', name, text: 'Открой карточку: ' + name }); },
  compareWith(name) {
    const other = prompt('С кем сравнить «' + name + '»? Сравнение уйдёт в отдельную ветку.');
    if (other && other.trim()) sendTurn({ kind: 'compare', a: name, b: other.trim(), text: 'Сравни: ' + name + ' и ' + other.trim() });
  },
  async newDialog() {
    try {
      const d = (await api('POST', '/api/conversations', { empty: true })).conversation;
      await refreshList();
      app.live = null;
      await loadConv(d.id);
      $('composer-text').focus();
    } catch (e) { toast(e.message, true); }
  },
  async pickDialog(id) { if (id) { app.live = null; if (app.stream) app.stream.close(); await loadConv(id); } },
  async switchBranch(id) { await convAction('switch', { branch: id }); },
  async mark() {
    const name = prompt('Название точки сохранения (можно пусто):', '');
    if (name === null) return;
    await convAction('checkpoints', { name });
  },
  async fork(cp) {
    const name = prompt('Название новой ветки:', '');
    if (name === null) return;
    await convAction('branches', { checkpoint: cp, name });
  },
  async toggleMechanism(name, el) { await convAction('features', { name, on: !el.dataset.on }); },
  selectTurn(id) { app.selected = id; renderFeed(); renderJournal(); },
  tab(t) { app.tab = t; renderJournal(); },
  toggleEvent(seq, el) { el.closest('.ev').classList.toggle('open'); },
  why(_, el) { showWhy(el.dataset.turn, el.dataset.call); },
  export(key, el) {
    if (!app.conv) return;
    location.href = `/api/conversations/${app.conv.id}/export?kind=${encodeURIComponent(el.dataset.kind)}&id=${encodeURIComponent(key)}`;
  },
  openWindow(name) { openWindow(name); },
  closeWindow() { $('window').close(); },
};
app.actions = actions;

async function convAction(action, body) {
  if (!app.conv) return;
  try {
    app.conv = (await api('POST', '/api/conversations/' + app.conv.id + '/' + action, body)).conversation;
    app.selected = app.conv.turnList.length ? app.conv.turnList[app.conv.turnList.length - 1].id : null;
    render();
  } catch (e) { toast(e.message, true); }
}

// showWhy — «почему так» (ФТ-12): журнал хода, откуда пришёл факт, и
// событие с этим вызовом инструмента.
function showWhy(turnId, callId) {
  if (turnId) app.selected = turnId;
  app.tab = 'events';
  renderFeed();
  renderJournal();
  if (!callId) return;
  const body = $('journal-body');
  const evs = [...body.querySelectorAll('.ev')].filter(e => e.dataset.call === callId);
  const target = evs.find(e => e.classList.contains('ev-kind-tool.result')) || evs[0];
  if (!target) { toast('Событие этого вызова в журнале не найдено'); return; }
  evs.forEach(e => e.classList.add('open'));
  target.classList.add('flash');
  target.scrollIntoView({ block: 'center' });
  setTimeout(() => target.classList.remove('flash'), 1600);
}

/* ---------- окна ---------- */

function openWindow(name) {
  if (name === 'windows') {
      const list = Object.entries(app.windows);
    $('window-title').textContent = 'Окна';
    $('window-body').innerHTML = `<div class="wlist">${list.map(([k, w]) =>
      `<button type="button" data-action="openWindow" data-arg="${esc(k)}">${esc(w.title)}</button>`).join('')}
      ${list.length ? '' : '<p class="hint">Окна появятся вместе с механизмами.</p>'}</div>`;
  } else {
    const w = app.windows[name];
    if (!w) return;
    $('window-title').textContent = w.title;
    $('window-body').innerHTML = '<p class="hint">загружаю…</p>';
    Promise.resolve(w.render()).then(html => { $('window-body').innerHTML = html; })
      .catch(e => { $('window-body').innerHTML = `<p class="hint">${esc(e.message)}</p>`; });
  }
  const dlg = $('window');
  if (!dlg.open) dlg.showModal();
}

app.windows.file = {
  title: 'Файл диалога',
  async render() {
    if (!app.conv) return '<p class="hint">нет диалога</p>';
    const f = await api('GET', '/api/conversations/' + app.conv.id + '/raw');
    return `<p class="hint">${esc(f.path)}</p><pre>${esc(f.json)}</pre>`;
  },
};

/* ---------- человек: профиль, память, карточка фактов ---------- */

function personView() { return app.conv && app.conv.extras ? app.conv.extras.persona : null; }

// latinHidden — профиль просит скрывать латынь (роль «ребёнок»): карточка
// показывает её только по наведению, а не в заголовке.
function latinHidden() {
  const p = personView();
  return !!(p && p.profile && p.profile.values && p.profile.values.latin && p.profile.values.latin.value === 'hide');
}

function fieldLabel(key, value) {
  const f = ((app.meta && app.meta.profileFields) || []).find(x => x.key === key);
  if (!f) return value;
  const o = f.options.find(x => x.value === value);
  return o ? o.title : value;
}

app.panels.persona = (v, conv) => {
  const fields = (app.meta && app.meta.profileFields) || [];
  const values = (v.profile && v.profile.values) || {};
  const on = name => conv.features && conv.features[name];
  let prof = fields.filter(f => values[f.key]).map(f =>
    `<span class="chip ok" title="${esc(f.title)}${values[f.key].quote ? ' — по словам: «' + esc(values[f.key].quote) + '»' : ''}">${esc(fieldLabel(f.key, values[f.key].value))}</span>`).join('');
  if (!prof) prof = '<span class="hint">анкета пуста — заполнится из разговора или руками</span>';
  if (!on('profile')) prof = '<span class="hint">профиль выключен в этом диалоге</span>';
  const limits = ((v.profile && v.profile.limits) || []).map(l => `<span class="chip warn">не: ${esc(l.text)}</span>`).join('');
  const entries = card => (card && card.entries) || [];
  const long = entries(v.long).map(e => `<div><b>${esc(e.key)}</b>: ${esc(e.value)}</div>`).join('') || '<span class="hint">пусто</span>';
  const work = v.work && v.work.id ? (entries(v.work).map(e => `<div><b>${esc(e.key)}</b>: ${esc(e.value)}</div>`).join('') || '<span class="hint">пусто</span>') : '';
  return `<section class="panel" id="panel-person"><h2>Собеседник «${esc(v.owner)}»
      <button type="button" class="small" data-action="openWindow" data-arg="people">анкета</button></h2>
      <div class="chips">${prof}${limits}</div>
      ${v.error ? `<div class="hint">${esc(v.error)}</div>` : ''}</section>
    <section class="panel" id="panel-memory"><h2>Память <button type="button" class="small" data-action="openWindow" data-arg="memory">целиком</button></h2>
      <div class="hint">долговременная${on('memory.long') ? '' : ' — выключена'} · ${esc(v.paths.long || '')}</div>${long}
      ${work ? `<div class="hint" style="margin-top:4px">рабочая: подборка «${esc(v.work.title || v.work.id)}»</div>${work}` : ''}
      ${conv.facts && conv.facts.entries && conv.facts.entries.length ? `<div class="hint" style="margin-top:4px">карточка фактов ветки</div>` +
        conv.facts.entries.map(e => `<div><b>${esc(e.key)}</b>: ${esc(e.value)}</div>`).join('') : ''}</section>`;
};

const layerTitle = { long: 'долговременная', work: 'рабочая' };
app.chips.memory = changes => (changes || []).map(c => {
  const cls = c.op === 'skip' ? 'warn' : '';
  const what = c.op === 'delete' ? `забыто «${c.key}»` : c.op === 'move' ? `«${c.key}» → ${layerTitle[c.layer]}` :
    c.op === 'skip' ? `не записано «${c.key}»` : `${layerTitle[c.layer] || c.layer}: ${c.key} = ${c.value}`;
  return `<span class="chip ${cls}" title="${esc(c.reason || 'память')}">🧠 ${esc(what)}</span>`;
});
app.chips.profile = changes => (changes || []).map(c => {
  const cls = c.op === 'reject' ? 'bad' : c.op === 'once' ? 'warn' : 'ok';
  const text = c.op === 'once' ? `разово: ${c.label}` : c.op === 'reject' ? `отклонено: ${c.title || c.value}` :
    c.op === 'limit' ? `ограничение: ${c.value}` : `профиль: ${c.title} — ${c.label}`;
  const tip = [c.reason, c.quote ? 'цитата: «' + c.quote + '»' : ''].filter(Boolean).join('\n');
  return `<span class="chip ${cls}" title="${esc(tip)}">👤 ${esc(text)}</span>`;
});
app.chips.facts = changes => (changes || []).map(c =>
  `<span class="chip ${c.op === 'skip' ? 'warn' : ''}" title="${esc(c.reason || 'карточка фактов ветки')}">📌 ${esc(c.op === 'delete' ? 'забыто ' + c.key : c.key + ': ' + (c.value || ''))}</span>`);
app.chips.checks = checks => {
  const def = (checks || []).filter(c => !c.na);
  if (!def.length) return [];
  const ok = def.filter(c => c.ok).length;
  const tip = checks.map(c => `${c.title}: ${c.na ? 'не определить' : c.ok ? 'соблюдено' : 'нарушено'} — ${c.got}`).join('\n');
  return [`<span class="chip ${ok === def.length ? 'ok' : 'bad'}" title="${esc(tip)}">профиль соблюдён ${ok} из ${def.length}</span>`];
};
app.chips.read = names => [`<span class="chip" title="Долговременная память: «уже читал»">📖 уже читал: ${esc((names || []).join(', '))}</span>`];

app.windows.people = {
  title: 'Картотека профилей',
  async render() {
    const out = await api('GET', '/api/people');
    const fields = app.meta.profileFields || [];
    const presets = app.meta.profilePresets || [];
    const current = personView() ? personView().owner : '';
    let html = '<p class="hint">Анкета — не пожелание, а правила: у каждого значения готовая строка промпта. Разовая просьба в разговоре анкету не меняет, повторённая — закрепляется.</p>';
    for (const p of out.people || []) {
      html += `<h3 style="margin:10px 0 4px">${esc(p.title || p.id)} ${p.id === current ? '<span class="chip ok">этот диалог</span>' : ''}
        <span class="hint">${esc(p.paths.profile)}</span></h3><table class="grid"><tr><th>поле</th><th>значение</th></tr>`;
      for (const f of fields) {
        const cur = p.profile.values[f.key] ? p.profile.values[f.key].value : '';
        html += `<tr><td>${esc(f.title)}${f.checked ? ' <span class="hint">(проверяется)</span>' : ''}</td><td>
          <select data-change="setProfileField" data-person="${esc(p.id)}" data-field="${esc(f.key)}">
          <option value="">— не задано —</option>${f.options.map(o => `<option value="${esc(o.value)}"${o.value === cur ? ' selected' : ''}>${esc(o.title)}</option>`).join('')}
          </select></td></tr>`;
      }
      html += `</table><div class="chips" style="margin-top:4px">${(p.profile.limits || []).map(l =>
        `<span class="chip warn">не: ${esc(l.text)} <button type="button" class="link" data-action="unlimit" data-person="${esc(p.id)}" data-arg="${esc(l.text)}">×</button></span>`).join('')}
        <button type="button" class="small" data-action="addLimit" data-arg="${esc(p.id)}">+ ограничение</button>
        ${presets.map(pr => `<button type="button" class="small" data-action="applyPreset" data-person="${esc(p.id)}" data-arg="${esc(pr.id)}">заготовка «${esc(pr.title)}»</button>`).join('')}
        </div>`;
    }
    if (!(out.people || []).length) html += '<p class="hint">Пока никого нет.</p>';
    return html;
  },
};

app.windows.memory = {
  title: 'Память целиком',
  async render() {
    const out = await api('GET', '/api/memory');
    const table = (card, layer) => `<h3 style="margin:10px 0 4px">${esc(layerTitle[layer])}: ${esc(card.title || card.id)} <span class="hint">версия ${card.version}</span></h3>
      <table class="grid"><tr><th>ключ</th><th>значение</th><th>ход</th><th>откуда</th><th></th></tr>${card.entries.map(e =>
        `<tr><td>${esc(e.key)}</td><td>${esc(e.value)}</td><td>${e.turn}</td><td>${esc(e.source || '')}</td>
         <td><button type="button" class="small" data-action="forgetMemory" data-layer="${layer}" data-id="${esc(card.id)}" data-arg="${esc(e.key)}">забыть</button></td></tr>`).join('')}</table>
      <button type="button" class="small" data-action="putMemory" data-layer="${layer}" data-arg="${esc(card.id)}">+ запись</button>`;
    let html = '<p class="hint">Три слоя по адресам: краткосрочная — в файле диалога, рабочая — по подборке, долговременная — по человеку. Один ключ живёт в одном слое.</p>';
    for (const c of out.long || []) html += table(c, 'long');
    for (const c of out.work || []) html += table(c, 'work');
    if (!(out.long || []).length && !(out.work || []).length) html += '<p class="hint">Память пуста.</p>';
    return html;
  },
};

async function afterWindowEdit(name) {
  try { $('window-body').innerHTML = await app.windows[name].render(); } catch (e) { toast(e.message, true); }
  if (app.conv) loadConv(app.conv.id, true);
}

Object.assign(actions, {
  async setProfileField(value, el) {
    try {
      await api('POST', `/api/people/${el.dataset.person}/profile`, value ? { op: 'set', field: el.dataset.field, value } : { op: 'clear', field: el.dataset.field });
      toast('Анкета записана');
      await afterWindowEdit('people');
    } catch (e) { toast(e.message, true); }
  },
  async addLimit(person) {
    const text = prompt('Чего справочнику не делать никогда (например, «не рассказывай про охоту»)?');
    if (!text) return;
    try { await api('POST', `/api/people/${person}/profile`, { op: 'limit', text }); await afterWindowEdit('people'); } catch (e) { toast(e.message, true); }
  },
  async unlimit(text, el) {
    try { await api('POST', `/api/people/${el.dataset.person}/profile`, { op: 'unlimit', text }); await afterWindowEdit('people'); } catch (e) { toast(e.message, true); }
  },
  async applyPreset(preset, el) {
    try { await api('POST', `/api/people/${el.dataset.person}/profile`, { op: 'preset', preset }); await afterWindowEdit('people'); } catch (e) { toast(e.message, true); }
  },
  async forgetMemory(key, el) {
    try { await api('POST', `/api/memory/${el.dataset.layer}/${el.dataset.id}`, { op: 'forget', key }); await afterWindowEdit('memory'); } catch (e) { toast(e.message, true); }
  },
  async putMemory(id, el) {
    const key = prompt('Ключ записи:');
    if (!key) return;
    const value = prompt('Значение:');
    if (!value) return;
    try { await api('POST', `/api/memory/${el.dataset.layer}/${id}`, { op: 'put', key, value }); await afterWindowEdit('memory'); } catch (e) { toast(e.message, true); }
  },
  async bookmark(name) {
    const p = personView();
    if (!p || !p.owner) { toast('У диалога нет собеседника'); return; }
    try { await api('POST', `/api/people/${p.owner}/bookmark`, { name }); toast('В закладках: ' + name); loadConv(app.conv.id, true); } catch (e) { toast(e.message, true); }
  },
});

/* ---------- подборка: этапы, виды, права этапа ---------- */

const stageTitle = { planning: 'план', collecting: 'сбор', validation: 'сверка', done: 'принята' };
const stageOrder = ['planning', 'collecting', 'validation', 'done'];
const itemMark = { pending: '○', done: '●', rework: '↺' };

function stagesHTML(st) {
  const at = stageOrder.indexOf(st.stage);
  return `<div class="stages">${stageOrder.map((s, i) =>
    `<span class="stage-step ${i < at ? 'past' : i === at ? 'now' : ''}">${esc(stageTitle[s])}</span>`).join('<span class="stage-arrow">→</span>')}
    ${st.paused ? '<span class="chip warn">на паузе</span>' : ''}</div>`;
}

app.panels.collection = v => {
  const st = v.state || {};
  const items = (st.items || []).map(it =>
    `<div class="item ${it.n === st.current && st.stage === 'collecting' ? 'current' : ''}" title="${esc(it.result || '')}">
      ${itemMark[it.status] || '○'} ${it.n}. ${esc(it.name)}${it.output ? ` <button type="button" class="link" data-action="open" data-arg="${esc(it.output.card.name || it.name)}">карточка</button>` : ''}</div>`).join('');
  const g = v.grant || {};
  const locked = (g.locked || []).map(l =>
    `<span class="chip warn" title="${esc(l.why)}${l.when ? '\nСтанет можно: ' + esc(l.when) : ''}">🔒 ${esc(l.tool)}</span>`).join('');
  const tools = (g.tools || []).map(t => `<span class="chip ok">${esc(t)}</span>`).join('');
  const report = st.report ? `<div class="hint">сверка: ${esc(st.report.summary || '')} — ${st.report.checks.filter(c => c.ok).length} из ${st.report.checks.length} сошлось</div>` : '';
  return `<section class="panel wide" id="panel-collection"><h2>Подборка «${esc(st.title || '')}»
      <button type="button" class="small" data-action="openWindow" data-arg="collections">все подборки</button>
      <a class="small" href="/api/collections/${esc(st.id)}/export">выгрузить</a></h2>
    ${v.enabled ? '' : '<div class="hint">состояние подборки выключено в этом диалоге — модель его не видит</div>'}
    ${stagesHTML(st)}
    <div class="hint">ждём: ${esc((v.expected || {}).text || '')}${st.goal ? ' · цель: ' + esc(st.goal) : ''}</div>
    <div class="items">${items || '<span class="hint">плана ещё нет</span>'}</div>${report}
    <div class="chips" title="${v.gated ? 'Права этапа: модели даны только эти инструменты' : 'Права этапа выключены: все инструменты сразу, правила — словами'}">
      ${v.gated ? '' : '<span class="chip bad">права этапа выключены</span>'}${tools}${locked}</div>
    <div class="hint">${esc(v.path || '')}${v.error ? ' · ' + esc(v.error) : ''}</div></section>`;
};

app.chips.collection = r => {
  const out = [];
  for (const c of r.changes || []) {
    const text = c.rejected ? `отклонено: ${c.event} — ${c.reason}` :
      c.from.stage === c.to.stage && !!c.from.paused === !!c.to.paused ? `подборка: ${c.event}` :
      `подборка: ${stageTitle[c.from.stage]}${c.from.paused ? ' (пауза)' : ''} → ${stageTitle[c.to.stage]}${c.to.paused ? ' (пауза)' : ''}`;
    out.push(`<span class="chip ${c.rejected ? 'warn' : 'ok'}" title="${esc([c.note, c.quote ? 'цитата: «' + c.quote + '»' : ''].filter(Boolean).join('\n'))}">📋 ${esc(text)}</span>`);
  }
  for (const w of r.works || []) out.push(`<span class="chip">📋 ${esc(w)}</span>`);
  for (const d of r.denials || []) {
    out.push(`<span class="chip bad" title="${esc(`Почему: ${d.reason}\nДоступно: ${d.available || '—'}\nЧто сделать: ${d.hint || '—'}`)}">🔒 нельзя: ${esc(d.what)}</span>`);
  }
  return out;
};

app.windows.collections = {
  title: 'Подборки',
  async render() {
    const out = await api('GET', '/api/collections');
    const list = out.collections || [];
    let html = '<p class="hint">Подборка живёт в своём файле, а не в диалоге: её можно продолжить в любом диалоге — этап, виды и отказы сохранятся.</p>';
    if (list.length) {
      html += `<table class="grid"><tr><th>подборка</th><th>этап</th><th>видов</th><th>ждём</th><th></th></tr>${list.map(c =>
        `<tr><td>${esc(c.title)}<div class="hint">${esc(c.path)}</div></td><td>${esc(stageTitle[c.stage] || c.stage)}${c.paused ? ' · пауза' : ''}</td>
         <td>${c.done} из ${c.items}</td><td>${esc(c.expected.text)}</td>
         <td><button type="button" class="small" data-action="continueCollection" data-arg="${esc(c.id)}"${app.conv ? '' : ' disabled'}>продолжить здесь</button>
             <a class="small" href="/api/collections/${esc(c.id)}/export">выгрузить</a>
             <button type="button" class="small" data-action="collectionFile" data-arg="${esc(c.id)}">файл</button></td></tr>`).join('')}</table>`;
    } else {
      html += '<p class="hint">Подборок нет. Начните с реплики «Собери подборку: хищники тайги, пять видов».</p>';
    }
    for (const p of out.problems || []) html += `<p class="hint">⚠ ${esc(p)}</p>`;
    return html;
  },
};

Object.assign(actions, {
  async continueCollection(id) {
    if (!app.conv) return;
    try {
      await api('POST', `/api/collections/${id}/continue`, { conversation: app.conv.id });
      actions.closeWindow();
      toast('Подборка подключена к диалогу');
      loadConv(app.conv.id, true);
    } catch (e) { toast(e.message, true); }
  },
  async collectionFile(id) {
    try {
      const f = await api('GET', '/api/collections/' + id);
      $('window-body').innerHTML = `<p class="hint">${esc(f.path)}</p><pre>${esc(f.json)}</pre>`;
    } catch (e) { toast(e.message, true); }
  },
});

/* ---------- свод и страж ---------- */

const ruleKindTitle = { sources: 'достоверность', safety: 'советы и безопасность', tone: 'тон' };
const actionTitle = { add: 'добавить', amend: 'изменить', retire: 'снять' };

function ruleTip(inv) {
  return [inv.rule, inv.because ? 'Почему: ' + inv.because : '', inv.instead ? 'Вместо: ' + inv.instead : '',
    (inv.except || []).length ? 'Не нарушает: ' + inv.except.join('; ') : ''].filter(Boolean).join('\n');
}

app.panels.charter = v => {
  const items = (v.items || []).map(inv => inv.status === 'retired'
    ? `<span class="chip rule-retired" title="${esc('Снято. ' + ruleTip(inv))}">${esc(inv.id)} ${esc(inv.title)}</span>`
    : `<span class="chip ok" title="${esc(ruleTip(inv))}">${esc(inv.id)} ${esc(inv.title)}</span>`).join('');
  const pending = (v.pending || []).map(a =>
    `<span class="chip warn" title="${esc('Основание: ' + a.reason + (a.cost ? '\nЦена: ' + a.cost : '') + '\nПринимает человек своими словами, не раньше следующего хода.')}">⏳ ${esc(actionTitle[a.action] || a.action)}: ${esc(a.proposed.title || a.item_id)}</span>`).join('');
  const flags = [];
  if (!v.charter) flags.push('<span class="chip bad" title="Те же правила уходят абзацем системного промпта: без сверки, стража и процедуры">свод выключен — правила словами</span>');
  if (!v.guard) flags.push('<span class="chip bad" title="Ответ уходит человеку без проверки по своду">страж выключен</span>');
  return `<section class="panel wide" id="panel-charter"><h2>Свод «${esc(v.title || '')}» <span class="hint">редакция ${v.version}</span>
      <button type="button" class="small" data-action="openWindow" data-arg="charter">свод</button></h2>
    <div class="chips">${flags.join('')}${items}${pending}</div>
    <div class="hint">${esc(v.path || '')}${v.error ? ' · ' + esc(v.error) : ''}</div></section>`;
};

app.chips.charter = r => {
  const out = [];
  if (!r.enabled) out.push('<span class="chip warn" title="Механизм charter выключен: правила ушли абзацем системного промпта">📜 свод выключен — правила словами</span>');
  for (const c of r.checks || []) {
    out.push(`<span class="chip ${(c.suspect || []).length ? 'warn' : ''}" title="${esc('Сверка: ' + c.answer)}">📜 сверка${(c.suspect || []).length ? ': похоже на ' + esc(c.suspect.join(', ')) : ' — чисто'}</span>`);
  }
  for (const a of r.amendments || []) {
    const text = a.rejected ? `${a.event} не принято — ${a.reason}` : `${a.summary}`;
    const tip = [a.amendment ? 'поправка ' + a.amendment : '', a.quote ? 'цитата: «' + a.quote + '»' : '', 'редакция ' + a.version].filter(Boolean).join('\n');
    out.push(`<span class="chip ${a.rejected ? 'warn' : 'ok'}" title="${esc(tip)}">📜 ${esc(text)}</span>`);
  }
  const g = r.guard || {};
  const rev = g.review || {};
  switch (g.status) {
    case 'refused': {
      const why = (rev.broken || []).map(v => `${v.invariant}: ${v.why || ''}${v.fragment ? ' — «' + v.fragment + '»' : ''}`).join('\n');
      out.push(`<span class="chip bad" title="${esc(why + '\n\nНе дошло до человека:\n' + (g.original || ''))}">🛡 страж: нарушен ${esc((rev.broken || []).map(v => v.invariant).join(', '))} — ответ заменён</span>`);
      break;
    }
    case 'passed':
      out.push(`<span class="chip ok" title="${esc((rev.screened || []).map(h => `${h.invariant} «${h.marker}»: ${h.fragment}${h.cleared ? ' — снято: ' + h.why : ''}`).join('\n'))}">🛡 страж: проверено, нарушений нет</span>`);
      break;
    case 'unchecked':
      out.push(`<span class="chip warn" title="${esc(rev.error || '')}">🛡 страж: ответ не проверен</span>`);
      break;
    case 'off':
      out.push('<span class="chip warn" title="Механизм guard выключен">🛡 страж выключен</span>');
      break;
  }
  return out;
};

app.windows.charter = {
  title: 'Свод',
  async render() {
    const out = await api('GET', '/api/charter');
    const c = out.charter || {};
    let html = `<p class="hint">Правила справочника, которые просьба не отменяет. Изменить свод можно только поправкой в разговоре: справочник предлагает, человек принимает своими словами и не раньше следующего хода. ${esc(out.summary || '')}.</p>
      <table class="grid"><tr><th>№</th><th>вид</th><th>правило</th><th>почему · вместо</th><th>статус</th></tr>${(c.items || []).map(inv =>
        `<tr class="${inv.status === 'retired' ? 'rule-retired' : ''}"><td>${esc(inv.id)}</td><td>${esc(ruleKindTitle[inv.kind] || inv.kind)}</td>
         <td><b>${esc(inv.title)}</b><div>${esc(inv.rule)}</div>${(inv.except || []).length ? `<div class="hint">не нарушает: ${esc(inv.except.join('; '))}</div>` : ''}</td>
         <td>${esc(inv.because || '')}${inv.instead ? `<div class="hint">вместо: ${esc(inv.instead)}</div>` : ''}</td>
         <td>${inv.status === 'retired' ? 'снято' : 'действует'}</td></tr>`).join('')}</table>`;
    if ((c.pending || []).length) {
      html += `<h3 style="margin:10px 0 4px">Открытые поправки</h3><table class="grid"><tr><th>поправка</th><th>что</th><th>основание</th><th>цена</th></tr>${c.pending.map(a =>
        `<tr><td>${esc(a.id)}</td><td>${esc(actionTitle[a.action] || a.action)}: ${esc(a.proposed.title || a.item_id)}${a.action !== 'retire' ? `<div class="hint">${esc(a.proposed.rule || '')}</div>` : ''}</td>
         <td>${esc(a.reason)}</td><td>${esc(a.cost || '')}</td></tr>`).join('')}</table>`;
    }
    if ((c.log || []).length) {
      html += `<h3 style="margin:10px 0 4px">Журнал изменений</h3><table class="grid"><tr><th>когда</th><th>ред.</th><th>что</th><th>слова человека</th></tr>${c.log.slice().reverse().map(l =>
        `<tr><td>${esc(when(l.at))}</td><td>${l.version}</td><td>${esc(actionTitle[l.action] || l.action)}: ${esc(l.title)}<div class="hint">${esc(l.summary)}</div></td><td>${l.quote ? '«' + esc(l.quote) + '»' : ''}</td></tr>`).join('')}</table>`;
    }
    html += `<details><summary>Блок свода в запросе</summary><pre>${esc(out.block)}</pre></details>
      <details><summary>Абзац правил при выключенном своде</summary><pre>${esc(out.plain)}</pre></details>
      <details><summary>Файл ${esc(out.path)}</summary><pre>${esc(out.json || 'файла ещё нет — действует заготовка')}</pre></details>`;
    return html;
  },
};

/* ---------- события DOM ---------- */

document.addEventListener('click', ev => {
  const el = ev.target.closest('[data-action]');
  if (!el || el.disabled) return;
  const fn = actions[el.dataset.action];
  if (!fn) return;
  ev.preventDefault();
  fn(el.dataset.arg, el);
});
document.addEventListener('change', ev => {
  const el = ev.target.closest('[data-change]');
  if (el && actions[el.dataset.change]) actions[el.dataset.change](el.value, el);
});
document.addEventListener('submit', ev => {
  const el = ev.target.closest('[data-submit]');
  if (!el) return;
  ev.preventDefault();
  actions[el.dataset.submit]();
});
document.addEventListener('keydown', ev => {
  if (ev.target.id === 'composer-text' && ev.key === 'Enter' && !ev.shiftKey) {
    ev.preventDefault();
    actions.sendComposer();
  }
});
window.addEventListener('hashchange', () => {
  const id = hashId();
  if (id && (!app.conv || app.conv.id !== id)) loadConv(id);
});

boot();

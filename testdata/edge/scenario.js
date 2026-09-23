'use strict';

/* Сценарий проверок интерфейса для headless Edge. Подключается после
   app.js, ходит по интерфейсу теми же кнопками, что и человек (клик по
   элементу с data-action уходит в общий обработчик app.js), и пишет итог
   в <pre id="test-log">: строка на проверку — «OK имя» или «FAIL имя —
   причина», в конце «DONE». Какой сценарий — из ?scenario=… в адресе. */

(function () {
  const log = document.createElement('pre');
  log.id = 'test-log';
  log.style.display = 'none';
  document.body.appendChild(log);
  const errors = [];
  window.addEventListener('error', e => errors.push('error: ' + e.message));
  window.addEventListener('unhandledrejection', e => errors.push('rejection: ' + (e.reason && e.reason.message || e.reason)));
  // Вопросы пользователю отвечаем сами: имя точки, ветки и т. п.
  window.prompt = () => 'из сценария';
  window.confirm = () => true;

  const write = line => { log.textContent += line + '\n'; };
  const sleep = ms => new Promise(r => setTimeout(r, ms));
  const q = sel => document.querySelector(sel);
  const qa = sel => [...document.querySelectorAll(sel)];
  const text = sel => { const el = q(sel); return el ? el.textContent.replace(/\s+/g, ' ').trim() : ''; };

  // until — ждёт, пока условие станет истинным (и возвращает его значение).
  async function until(what, fn, ms) {
    const end = Date.now() + (ms || 5000);
    for (;;) {
      let v;
      try { v = fn(); } catch (e) { v = null; }
      if (v) return v;
      if (Date.now() > end) throw new Error('не дождались: ' + what);
      await sleep(50);
    }
  }
  function assert(cond, msg) { if (!cond) throw new Error(msg); }
  function click(sel) {
    const el = typeof sel === 'string' ? q(sel) : sel;
    assert(el, 'нет элемента ' + sel);
    el.click();
    return el;
  }
  async function check(name, fn) {
    try {
      await fn();
      write('OK ' + name);
    } catch (e) {
      write('FAIL ' + name + ' — ' + String(e && e.message || e).replace(/\n/g, ' '));
    }
  }
  const windowOpen = () => $('window').open;
  async function openWin(name) {
    click('#windows-button');
    await until('список окон', () => windowOpen() && q('#window-body .wlist'));
    click(q(`#window-body [data-action="openWindow"][data-arg="${name}"]`));
    await until('окно ' + name, () => !text('#window-body').includes('загружаю') && $('window-title').textContent === app.windows[name].title && text('#window-body'));
  }
  const booted = () => until('загрузка диалога', () => app.conv && q('#feed').children.length, 8000);
  const exportURL = el => `/api/conversations/${app.conv.id}/export?kind=${encodeURIComponent(el.dataset.kind)}&id=${encodeURIComponent(el.dataset.arg)}`;

  const scenarios = {};

  /* Основной диалог: карточка рыси, раздел, узел дерева, реплика ведущему
     с правками профиля и памяти, точка, ветка «сравнение» со сравнением. */
  scenarios.main = async () => {
    await booted();

    await check('пульт: липкая шапка с показаниями', () => {
      const p = q('header.pult');
      assert(getComputedStyle(p).position === 'sticky', 'пульт не sticky');
      const r = text('#readings');
      assert(r.includes('Модель') && r.includes('Ходов') && r.includes('Собеседник'), 'показания: ' + r);
      assert(text('#readings').includes('me'), 'собеседник не показан');
    });

    await check('пульт: список диалогов и выбранный', () => {
      const opts = qa('#dialog-select option');
      assert(opts.length === 3, 'диалогов в списке ' + opts.length);
      assert($('dialog-select').value === app.conv.id, 'выбран не текущий диалог');
    });

    await check('ветки: дерево, активная ветка и точка сохранения', () => {
      const br = qa('#branches .branch');
      assert(br.length === 2, 'веток ' + br.length);
      assert(text('#branches .branch.active').includes('↳ сравнение: рысь и манул'), 'активна не ветка сравнения: ' + text('#branches .branch.active'));
      assert(qa('#branches [data-action="fork"]').some(b => b.textContent.includes('до сравнения')), 'нет кнопки точки «до сравнения»');
    });

    await check('ветки: «+ точка» ставит точку сохранения', async () => {
      const before = app.conv.checkpoints.length;
      click('#branches [data-action="mark"]');
      await until('новая точка', () => app.conv.checkpoints.length === before + 1);
      assert(qa('#branches [data-action="fork"]').some(b => b.textContent.includes('из сценария')), 'кнопки новой точки нет');
    });

    await check('механизмы: все из реестра, счётчик', () => {
      const m = qa('#mechanisms .mech');
      assert(m.length === app.meta.mechanisms.length && m.length >= 10, 'кнопок ' + m.length);
      const on = qa('#mechanisms .mech.on').length;
      assert(text('#mech-count') === `включено ${on} из ${m.length}`, 'счётчик: ' + text('#mech-count'));
      assert(m.every(b => b.title.includes('Выключен:')), 'в подсказке нет запасного пути');
    });

    await check('выключатель механизма: выкл и обратно', async () => {
      const sel = '#mechanisms .mech[data-arg="facts"]';
      assert(q(sel).classList.contains('on'), 'карточка фактов не включена');
      click(sel);
      await until('выключение', () => q(sel).classList.contains('off'));
      assert(app.conv.features.facts === false || !app.conv.mechanisms.find(m => m.name === 'facts').on, 'сервер не выключил');
      click(sel);
      await until('включение', () => q(sel).classList.contains('on'));
    });

    await check('контекст: шкала и бюджет', () => {
      assert(qa('#context .scale span').length >= 2, 'в шкале меньше двух частей');
      assert(text('#context .budget').includes('постоянная часть'), 'нет строки бюджета');
    });

    await check('лента: ходы и карточка с латынью', () => {
      assert(qa('#feed .turn').length === app.conv.turnList.length, 'ходов в ленте ' + qa('#feed .turn').length);
      const card = q('#feed article.acard');
      assert(card, 'нет карточки');
      assert(text('#feed .acard h3') === 'Обыкновенная рысь', 'название: ' + text('#feed .acard h3'));
      assert(text('#feed .acard .latin') === 'Lynx lynx', 'латынь: ' + text('#feed .acard .latin'));
      assert(qa('#feed .bubble.user.click').length >= 2, 'клики не помечены как клики');
    });

    await check('разделы: прочитанный раскрывается', async () => {
      const read = qa('#feed .acard details.section').find(d => d.textContent.includes('Питание'));
      assert(read, 'нет прочитанного раздела «Питание»');
      assert(read.querySelector('.s-read'), 'статус не «прочитан»');
      read.querySelector('summary').click();
      await until('раскрытие', () => read.open);
      assert(read.querySelector('.s-body').textContent.includes('Пересказ'), 'в разделе нет пересказа');
    });

    await check('разделы: непрочитанные с кнопкой «прочитать»', () => {
      const lynx = q('#feed .acard');
      assert(lynx.querySelector('h3').textContent === 'Обыкновенная рысь', 'первая карточка не рысь');
      const btns = [...lynx.querySelectorAll('[data-action="section"]')];
      assert(btns.length >= 3, 'кнопок «прочитать» ' + btns.length);
      assert(btns.every(b => b.dataset.card && b.dataset.topic && !b.disabled), 'кнопка без карточки/темы или выключена');
      assert(!btns.some(b => b.dataset.topic === 'diet'), 'прочитанный раздел снова предлагают прочитать');
    });

    await check('дерево: узлы кликабельны, сам вид не нажимается', () => {
      const nodes = qa('#feed .acard .tree [data-action="node"]');
      assert(nodes.length >= 2, 'узлов ' + nodes.length);
      assert(nodes.every(n => n.dataset.key && n.dataset.card), 'узел без ключа');
      assert(q('#feed .acard .tree button.self[disabled]'), 'сам вид не выделен');
    });

    await check('соседи узла: список видов «Кошачьи»', () => {
      const nb = q('#feed .acard .neighbors');
      assert(nb && nb.textContent.includes('Кошачьи'), 'нет блока соседей');
      assert(nb.querySelectorAll('[data-action="open"]').length >= 2, 'в соседях меньше двух видов');
    });

    await check('сравнение: таблица и выгрузка', async () => {
      const cmp = q('#feed .compare');
      assert(cmp, 'нет сравнения');
      assert(cmp.querySelectorAll('table.cmp tr').length >= 3, 'строк мало');
      assert(cmp.querySelector('td.nodata'), 'нет пометки «сведений нет»');
      const res = await fetch(exportURL(cmp.querySelector('[data-action="export"]')));
      assert(res.ok, 'выгрузка сравнения: HTTP ' + res.status);
    });

    await check('чипы: память и профиль под ответом', () => {
      const chips = qa('#feed .chips .chip').map(c => c.textContent);
      assert(chips.some(c => c.includes('🧠') && c.includes('интерес')), 'нет чипа памяти: ' + chips.join(' | '));
      assert(chips.some(c => c.includes('👤')), 'нет чипа профиля');
    });

    await check('панели человека: собеседник и память', () => {
      assert(text('#panel-person').includes('«me»'), 'нет панели собеседника');
      assert(text('#panel-person .chips').includes('Кратко') || qa('#panel-person .chip.ok').length > 0, 'анкета пуста');
      assert(text('#panel-memory').includes('хищники тайги'), 'в панели памяти нет записи');
    });

    await check('журнал: события последнего хода', () => {
      assert($('tab-events').classList.contains('active'), 'не открыт журнал');
      const evs = qa('#journal-body .ev');
      assert(evs.length > 0, 'событий нет');
      assert(text('#journal-turn').includes('событ'), 'нет счётчика событий');
    });

    await check('журнал: событие раскрывается', async () => {
      const ev = qa('#journal-body .ev').find(e => e.querySelector('.ev-detail'));
      assert(ev, 'нет события с подробностями');
      click(ev.querySelector('.ev-head'));
      await until('раскрытие', () => ev.classList.contains('open'));
      assert(getComputedStyle(ev.querySelector('.ev-detail')).display === 'block', 'подробности не видны');
    });

    await check('вкладка «Промпты»: системный промпт и блоки', async () => {
      click('#tab-prompts');
      await until('промпты', () => $('tab-prompts').classList.contains('active') && q('#journal-body .prompt-block'));
      assert(text('#journal-body').includes('системный промпт'), 'нет системного промпта');
      assert(text('#journal-body').includes('инструменты'), 'нет списка инструментов');
      click('#tab-events');
      await until('журнал', () => $('tab-events').classList.contains('active'));
    });

    await check('«журнал хода» переключает журнал', async () => {
      const first = app.conv.turnList[0].id;
      click(`#feed [data-action="selectTurn"][data-arg="${first}"]`);
      await until('выбор хода', () => app.selected === first && q(`#turn-${first}.selected`));
    });

    await check('«почему так» ведёт к событию журнала', async () => {
      const btn = qa('#feed .acard .why').find(b => b.dataset.call);
      assert(btn, 'нет кнопки «?» с вызовом');
      const call = btn.dataset.call;
      click(btn);
      await until('событие вызова', () => qa('#journal-body .ev.open').some(e => e.dataset.call === call));
      assert(app.selected === btn.dataset.turn, 'журнал не того хода');
    });

    await check('выгрузка карточки в markdown', async () => {
      const btn = q('#feed .acard [data-action="export"][data-kind="card"]');
      assert(btn, 'нет кнопки выгрузки');
      const res = await fetch(exportURL(btn));
      const body = await res.text();
      assert(res.ok, 'HTTP ' + res.status);
      assert(body.includes('Обыкновенная рысь') && body.includes('## Питание'), 'в markdown нет карточки: ' + body.slice(0, 120));
      assert((res.headers.get('Content-Disposition') || '').includes('attachment'), 'не вложение');
    });

    await check('окно «Окна»: список окон', async () => {
      click('#windows-button');
      await until('окно', () => windowOpen() && q('#window-body .wlist'));
      const names = qa('#window-body .wlist [data-action="openWindow"]').map(b => b.dataset.arg);
      ['file', 'people', 'memory', 'collections'].forEach(n => assert(names.includes(n), 'нет окна ' + n));
    });

    await check('окно «Файл диалога»', async () => {
      await openWin('file');
      assert(text('#window-body pre').includes('"schema"'), 'в файле нет schema');
      assert(text('#window-body .hint').length > 0, 'нет пути к файлу');
    });

    await check('окно «Картотека профилей»: правка анкеты', async () => {
      await openWin('people');
      const sel = q('#window-body select[data-field="level"]') || q('#window-body select[data-change="setProfileField"]');
      assert(sel, 'нет полей анкеты');
      const opt = [...sel.options].find(o => o.value && o.value !== sel.value);
      sel.value = opt.value;
      sel.dispatchEvent(new Event('change', { bubbles: true }));
      await until('тост', () => !$('toast').hidden && $('toast').textContent.includes('Анкета записана'));
      await until('перерисовка', () => q(`#window-body select[data-field="${sel.dataset.field}"]`).value === opt.value);
    });

    await check('окно «Память целиком»: запись и «забыть»', async () => {
      await openWin('memory');
      assert(text('#window-body table.grid').includes('интерес'), 'нет записи «интерес»');
      assert(q('#window-body [data-action="forgetMemory"]'), 'нет кнопки «забыть»');
    });

    await check('окно «Подборки»: список и выгрузка', async () => {
      await openWin('collections');
      assert(q('#window-body table.grid [data-action="continueCollection"]'), 'нет подборки');
      const a = q('#window-body a[href*="/export"]');
      const res = await fetch(a.getAttribute('href'));
      assert(res.ok && (await res.text()).includes('#'), 'выгрузка подборки');
      click('#window [data-action="closeWindow"]');
      await until('закрытие', () => !windowOpen());
    });

    await check('переход в другую ветку', async () => {
      const before = app.conv.turnList.length;
      const main = qa('#branches .branch').find(b => !b.classList.contains('active'));
      click(main.querySelector('[data-action="switchBranch"]'));
      await until('смена ветки', () => app.conv.turnList.length === before - 1 && !q('#feed .compare'));
      assert(q('#branches .branch.active') && !text('#branches .branch.active').includes('сравнение'), 'активна прежняя ветка');
    });

    await check('прокрутка: пульт у верха окна, журнал под ним', async () => {
      window.scrollTo(0, document.body.scrollHeight);
      await until('прокрутка', () => window.scrollY > 100, 2000);
      await sleep(100);
      const p = q('header.pult').getBoundingClientRect();
      assert(Math.abs(p.top) < 1, 'пульт съехал: top=' + p.top);
      const j = $('journal').getBoundingClientRect();
      assert(j.top >= p.bottom - 1, `журнал заходит под пульт: журнал ${Math.round(j.top)}, низ пульта ${Math.round(p.bottom)}`);
      assert(j.bottom <= window.innerHeight + 1, `журнал ниже окна: ${Math.round(j.bottom)} > ${window.innerHeight}`);
      assert(document.documentElement.scrollWidth <= window.innerWidth + 1, 'горизонтальная прокрутка');
    });
  };

  /* Диалог подборки: план утверждён, первый вид собран. */
  scenarios.collection = async () => {
    await booted();
    await check('подборка: панель с этапами', () => {
      const p = q('#panel-collection');
      assert(p, 'нет панели подборки');
      const steps = qa('#panel-collection .stage-step').map(s => s.textContent);
      assert(steps.join(',') === 'план,сбор,сверка,принята', 'этапы: ' + steps);
      assert(text('#panel-collection .stage-step.now') === 'сбор', 'текущий этап: ' + text('#panel-collection .stage-step.now'));
      assert(q('#panel-collection .stage-step.past'), 'нет пройденного этапа');
    });
    await check('подборка: виды и права этапа', () => {
      const items = qa('#panel-collection .items .item').map(i => i.textContent);
      assert(items.length === 2, 'видов ' + items.length);
      assert(items[0].includes('●'), 'первый вид не отмечен собранным: ' + items[0]);
      assert(qa('#panel-collection .chip.ok').length > 0, 'нет разрешённых инструментов');
      assert(qa('#panel-collection .chip.warn').some(c => c.textContent.includes('🔒')), 'нет запертых инструментов');
    });
    await check('подборка: чипы переходов в ленте', () => {
      const chips = qa('#feed .chip').map(c => c.textContent);
      assert(chips.some(c => c.includes('📋') && c.includes('→')), 'нет чипа перехода этапа: ' + chips.join(' | '));
    });
    await check('подборка: карточка собранного вида', () => {
      assert(q('#feed article.acard'), 'карточки вида нет в ленте');
      assert(q('#panel-collection [data-action="open"]'), 'у вида нет ссылки «карточка»');
    });
  };

  /* Пустой диалог: заглушка, затем «Новый диалог» и живой ход. */
  scenarios.empty = async () => {
    await until('загрузка', () => app.conv && q('#feed .empty-feed'), 8000);
    await check('пустое состояние: подсказка и примеры', () => {
      assert(text('#feed .empty-feed h2') === 'Спросите про животное', 'заголовок');
      assert(qa('#feed .examples [data-action="example"]').length === 5, 'примеров не пять');
      assert(text('#context').includes('появится'), 'шкала контекста без хода');
      assert(text('#journal-body').includes('Журнал хода появится'), 'журнал без хода');
    });
    await check('«Новый диалог» заводит пустой диалог', async () => {
      const before = app.convs.length, old = app.conv.id;
      click('#new-dialog');
      await until('новый диалог', () => app.conv.id !== old && app.convs.length === before + 1);
      assert(location.hash === '#c=' + app.conv.id, 'адрес не обновлён: ' + location.hash);
      assert(q('#feed .empty-feed'), 'новый диалог не пустой');
    });
    await check('живой ход из композера: карточка по мере сборки', async () => {
      $('composer-text').value = 'рысь';
      q('#composer button[type="submit"]').click();
      await until('ход пошёл', () => app.live || app.conv.turnList.length, 5000);
      await until('ход записан', () => !app.live && app.conv.turnList.length === 1, 20000);
      assert(text('#feed .acard h3') === 'Обыкновенная рысь', 'карточки нет: ' + text('#feed'));
      assert(!$('send-button').disabled, 'кнопка «Отправить» осталась выключенной');
      assert(qa('#dialog-select option').some(o => o.selected && o.textContent.includes('(1)')), 'список диалогов не обновился');
    });
  };

  /* Снимки экрана: только довести страницу до нужного вида. */
  scenarios['shot-top'] = async () => { await until('загрузка', () => app.conv, 8000); await sleep(300); };
  scenarios['shot-bottom'] = async () => {
    await booted();
    await sleep(300);
    document.body.scrollTop = document.body.scrollHeight;
    window.scrollTo(0, document.body.scrollHeight);
  };
  scenarios['shot-people'] = async () => { await booted(); await openWin('people'); };

  async function run() {
    const name = new URLSearchParams(location.search).get('scenario') || 'main';
    const fn = scenarios[name];
    if (!fn) { write('FAIL сценарий — нет сценария ' + name); write('DONE'); return; }
    try {
      await fn();
    } catch (e) {
      write('FAIL ' + name + ' — ' + (e && e.message || e));
    }
    if (!name.startsWith('shot')) {
      await check(name + ': без ошибок JavaScript', () => assert(!errors.length, errors.join('; ')));
    }
    write('DONE');
  }
  run();
})();

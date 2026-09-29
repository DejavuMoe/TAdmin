// TAdmin verification Mini App.
//
// Implements the HTTP contract in docs/architecture.md ("Mini App HTTP 契约"):
//   POST /api/v/{token}/open   {init_data}
//   POST /api/v/{token}/answer {init_data, gen, answer}
//   POST /api/v/{token}/help   {init_data}
// initData is only ever sent to our own API; it is never logged or stored.
(() => {
  'use strict';

  const tg = window.Telegram && window.Telegram.WebApp;

  const STRINGS = {
    'zh-CN': {
      docTitle: '入群验证',
      loading: '正在加载验证…',
      title: '入群验证',
      undo: '撤销',
      reset: '重选',
      help: '需要帮助？',
      helpConfirm: '将通知管理员为你人工审核，确定吗？',
      progress: '已选 {n}/{total}',
      attempts: '剩余 {n} 次机会',
      timer: '剩余 {n} 秒',
      cell: '第 {row} 行第 {col} 列',
      cellPicked: '第 {row} 行第 {col} 列，第 {n} 个',
      imageAlt: '验证图片',
      optionsLabel: '候选答案',
      gridLabel: '九宫格，按提示依次点击',
      wrong: '不对哦，已换一题',
      refreshed: '题目已更新，请重新作答',
      rateLimited: '操作太频繁，请稍候再试',
      networkRetry: '网络不稳定，请再试一次',
      close: '关闭',
      retry: '重试',
      passedTitle: '验证通过',
      passedText: '欢迎加入{chat}！\n即将自动返回…',
      failedTitle: '验证未通过',
      failedText: '本次验证已结束。你可以稍后再次尝试加入，或联系群管理员。',
      reviewTitle: '已通知管理员，请耐心等待',
      reviewText: '管理员处理后你会收到通知，现在可以关闭此页面。',
      expiredTitle: '验证已超时',
      expiredText: '请回到群聊重新发起验证，或联系群管理员。',
      closedTitle: '此验证已结束',
      closedText: '无需再次操作，可以关闭此页面。',
      invalidTitle: '链接无效',
      invalidText: '登录信息无效或已过期，请回到 Telegram 重新打开验证页面。',
      forbiddenTitle: '无法验证身份',
      forbiddenText: '这个验证不属于当前 Telegram 账号。',
      outsideTitle: '请在 Telegram 中打开',
      outsideText: '该页面需要在 Telegram 内打开才能完成验证。',
      networkTitle: '网络连接失败',
      networkText: '请检查网络后重试。',
      chatFallback: '本群',
    },
    en: {
      docTitle: 'Verification',
      loading: 'Loading verification…',
      title: 'Verify to join',
      undo: 'Undo',
      reset: 'Reset',
      help: 'Need help?',
      helpConfirm: 'An admin will be asked to review you manually. Continue?',
      progress: '{n}/{total} selected',
      attempts: '{n} attempts left',
      timer: '{n} seconds left',
      cell: 'Row {row}, column {col}',
      cellPicked: 'Row {row}, column {col}, pick {n}',
      imageAlt: 'Verification image',
      optionsLabel: 'Answer options',
      gridLabel: '3 by 3 grid, tap in the order shown',
      wrong: 'Not quite — here is a new one',
      refreshed: 'The challenge was updated, please try again',
      rateLimited: 'Too many attempts, please wait a moment',
      networkRetry: 'Connection is unstable, please try again',
      close: 'Close',
      retry: 'Try again',
      passedTitle: 'Verified',
      passedText: 'Welcome to {chat}!\nReturning to Telegram…',
      failedTitle: 'Verification failed',
      failedText: 'This verification has ended. You can try joining again later or contact the group admins.',
      reviewTitle: 'Admins have been notified',
      reviewText: 'You will get a message once an admin decides. You can close this page now.',
      expiredTitle: 'Verification timed out',
      expiredText: 'Go back to the group to start again, or contact the group admins.',
      closedTitle: 'This verification is over',
      closedText: 'Nothing else to do here, you can close this page.',
      invalidTitle: 'Invalid link',
      invalidText: 'Your session is invalid or expired. Please reopen the page from Telegram.',
      forbiddenTitle: 'Cannot verify identity',
      forbiddenText: 'This verification belongs to a different Telegram account.',
      outsideTitle: 'Open in Telegram',
      outsideText: 'This page must be opened inside Telegram to verify.',
      networkTitle: 'Connection failed',
      networkText: 'Check your connection and try again.',
      chatFallback: 'the group',
    },
  };

  // Geometry of the image challenge, mirroring internal/captcha (grid.go):
  // a 540 px square with a 15 px margin, 162 px cells and 12 px gaps.
  const GRID = { size: 540, margin: 15, cell: 162, gap: 12, n: 3 };
  const REQUEST_TIMEOUT_MS = 12000;
  const AUTO_SUBMIT_DELAY_MS = 260;
  const AUTO_CLOSE_MS = 2500;
  const RING = 2 * Math.PI * 19;

  const $ = (id) => document.getElementById(id);
  const ui = {
    app: $('app'),
    chip: $('chip'),
    chipAvatar: $('chip-avatar'),
    chipTitle: $('chip-title'),
    timer: $('timer'),
    timerBar: $('timer-bar'),
    timerText: $('timer-text'),
    attempts: $('attempts'),
    prompt: $('prompt'),
    board: $('board'),
    img: $('board-img'),
    grid: $('grid'),
    controls: $('controls'),
    undo: $('undo'),
    reset: $('reset'),
    progress: $('progress'),
    options: $('options'),
    toast: $('toast'),
    help: $('help'),
    resultIcon: $('result-icon'),
    resultTitle: $('result-title'),
    resultText: $('result-text'),
    resultAction: $('result-action'),
  };

  const state = {
    token: '',
    initData: '',
    lang: 'zh-CN',
    view: null,
    picks: [],
    busy: false,
    maxAttempts: 0,
    total: 0,
    clockOffset: 0,
    expiryChecked: 0,
    timerId: 0,
    toastId: 0,
    submitId: 0,
    finished: false,
    staleReloads: 0,
  };

  // ---------- i18n ----------

  function pickLang(code) {
    const c = String(code || '').toLowerCase();
    if (c.startsWith('zh')) return 'zh-CN';
    if (c.startsWith('en')) return 'en';
    return '';
  }

  function t(key, vars) {
    const table = STRINGS[state.lang] || STRINGS['zh-CN'];
    let s = table[key] ?? STRINGS['zh-CN'][key] ?? key;
    if (vars) s = s.replace(/\{(\w+)\}/g, (m, k) => (k in vars ? String(vars[k]) : m));
    return s;
  }

  function setLang(lang) {
    const next = pickLang(lang);
    if (!next || next === state.lang) return;
    state.lang = next;
    applyStaticText();
  }

  function applyStaticText() {
    document.documentElement.lang = state.lang;
    document.title = t('docTitle');
    for (const node of document.querySelectorAll('[data-i18n]')) node.textContent = t(node.dataset.i18n);
    ui.grid.setAttribute('aria-label', t('gridLabel'));
    ui.options.setAttribute('aria-label', t('optionsLabel'));
  }

  // ---------- Telegram integration ----------

  function tgCall(fn, minVersion) {
    if (!tg) return;
    try {
      if (!minVersion || (tg.isVersionAtLeast && tg.isVersionAtLeast(minVersion))) fn();
    } catch (_) {
      // Unsupported on this client; ignore.
    }
  }

  function haptic(type, style) {
    tgCall(() => {
      const h = tg.HapticFeedback;
      if (type === 'impact') h.impactOccurred(style || 'light');
      else if (type === 'notify') h.notificationOccurred(style);
      else h.selectionChanged();
    }, '6.1');
  }

  function paintChrome() {
    if (!tg) return;
    if (tg.platform && tg.platform !== 'unknown') {
      document.documentElement.dataset.scheme = tg.colorScheme === 'dark' ? 'dark' : 'light';
    }
    tgCall(() => {
      tg.setHeaderColor('secondary_bg_color');
      tg.setBackgroundColor('secondary_bg_color');
    }, '6.1');
    tgCall(() => tg.setBottomBarColor('secondary_bg_color'), '7.10');
  }

  function setupTelegram() {
    if (!tg) return;
    tgCall(() => tg.ready());
    tgCall(() => tg.expand());
    tgCall(() => tg.disableVerticalSwipes(), '7.7');
    paintChrome();
    tgCall(() => tg.onEvent('themeChanged', paintChrome));
  }

  function closeApp() {
    if (tg) tgCall(() => tg.close());
    else window.close();
  }

  function confirmAsync(message) {
    return new Promise((resolve) => {
      if (tg && tg.isVersionAtLeast && tg.isVersionAtLeast('6.2')) {
        try {
          tg.showConfirm(message, (ok) => resolve(Boolean(ok)));
          return;
        } catch (_) {
          // Fall through to the browser dialog.
        }
      }
      resolve(window.confirm(message));
    });
  }

  // ---------- Networking ----------

  class ApiError extends Error {
    constructor(code, httpStatus) {
      super(code);
      this.code = code;
      this.httpStatus = httpStatus;
    }
  }

  async function requestOnce(action, payload) {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), REQUEST_TIMEOUT_MS);
    try {
      const res = await fetch(`/api/v/${encodeURIComponent(state.token)}/${action}`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(payload),
        credentials: 'omit',
        cache: 'no-store',
        signal: ctrl.signal,
      });
      let data = null;
      try {
        data = await res.json();
      } catch (_) {
        data = null;
      }
      if (!res.ok) {
        const code = data && typeof data.error === 'string' ? data.error : res.status >= 500 ? 'server' : 'invalid';
        throw new ApiError(code, res.status);
      }
      if (!data || typeof data.status !== 'string') throw new ApiError('server', res.status);
      return data;
    } catch (err) {
      if (err instanceof ApiError) throw err;
      throw new ApiError('network', 0);
    } finally {
      clearTimeout(timer);
    }
  }

  // api retries exactly once on network failures (not on HTTP errors).
  async function api(action, extra) {
    const payload = Object.assign({ init_data: state.initData }, extra);
    try {
      return await requestOnce(action, payload);
    } catch (err) {
      if (err.code !== 'network') throw err;
      await sleep(600);
      return requestOnce(action, payload);
    }
  }

  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

  // ---------- Rendering ----------

  function showScreen(name) {
    ui.app.dataset.screen = name;
  }

  function setBusy(busy) {
    state.busy = busy;
    ui.board.setAttribute('aria-busy', String(busy));
    for (const b of ui.grid.children) b.disabled = busy;
    for (const b of ui.options.children) b.disabled = busy;
    ui.help.disabled = busy;
    updateControls();
  }

  function renderHeader(view) {
    const title = String(view.chat_title || '').trim();
    ui.chip.hidden = !title;
    if (title) {
      ui.chipTitle.textContent = title;
      ui.chipAvatar.textContent = Array.from(title)[0].toUpperCase();
    }
  }

  function renderAttempts(left) {
    if (typeof left !== 'number' || left < 0) {
      ui.attempts.hidden = true;
      return;
    }
    state.maxAttempts = Math.min(Math.max(state.maxAttempts, left), 10);
    ui.attempts.hidden = false;
    ui.attempts.replaceChildren();
    ui.attempts.setAttribute('aria-label', t('attempts', { n: left }));
    ui.attempts.setAttribute('role', 'img');
    for (let i = 0; i < state.maxAttempts; i++) {
      const dot = document.createElement('i');
      if (i >= left) dot.className = 'is-used';
      ui.attempts.append(dot);
    }
  }

  // The image prompt reads "<lead>: step → step → step"; render the steps as
  // chips that track progress. Any other shape is shown verbatim.
  function renderPrompt(view) {
    ui.prompt.replaceChildren();
    const text = String(view.prompt || '');
    if (view.kind === 'image') {
      const m = text.match(/^(.*?[:：])\s*(.+)$/);
      const steps = m ? m[2].split(/\s*→\s*/) : [];
      if (m && steps.length === view.picks) {
        const lead = document.createElement('span');
        lead.className = 'prompt-lead';
        lead.textContent = m[1].replace(/[:：]\s*$/, '');
        const list = document.createElement('ol');
        list.className = 'seq';
        list.setAttribute('aria-label', text);
        steps.forEach((step, i) => {
          const li = document.createElement('li');
          const chip = document.createElement('span');
          chip.className = 'seq-step';
          const n = document.createElement('span');
          n.className = 'seq-n';
          n.textContent = String(i + 1);
          const label = document.createElement('span');
          label.textContent = step;
          chip.append(n, label);
          li.append(chip);
          list.append(li);
        });
        ui.prompt.append(lead, list);
        return;
      }
    }
    ui.prompt.textContent = text;
  }

  function renderGrid(view) {
    ui.grid.replaceChildren();
    if (view.kind !== 'image') return;
    const pct = (px) => `${(px / GRID.size) * 100}%`;
    for (let i = 0; i < GRID.n * GRID.n; i++) {
      const row = Math.floor(i / GRID.n);
      const col = i % GRID.n;
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'cell';
      b.dataset.index = String(i);
      b.style.left = pct(GRID.margin + col * (GRID.cell + GRID.gap));
      b.style.top = pct(GRID.margin + row * (GRID.cell + GRID.gap));
      b.style.width = pct(GRID.cell);
      b.style.height = pct(GRID.cell);
      b.addEventListener('click', () => pickCell(i));
      ui.grid.append(b);
    }
  }

  function renderOptions(view) {
    ui.options.replaceChildren();
    const opts = Array.isArray(view.options) ? view.options : [];
    if (view.kind === 'image' || !opts.length) return;
    ui.options.classList.toggle('is-single', opts.length === 1);
    opts.forEach((label, i) => {
      const b = document.createElement('button');
      b.type = 'button';
      b.className = 'option';
      b.textContent = String(label);
      b.addEventListener('click', () => chooseOption(i, b));
      ui.options.append(b);
    });
  }

  async function renderImage(view) {
    const src = typeof view.image === 'string' && view.image.startsWith('data:image/') ? view.image : '';
    ui.board.dataset.kind = src ? view.kind : 'button';
    if (!src) {
      ui.img.removeAttribute('src');
      return;
    }
    const probe = new Image();
    probe.src = src;
    try {
      await probe.decode();
    } catch (_) {
      // Decoding errors surface as a broken image; nothing else to do.
    }
    if (probe.naturalWidth && probe.naturalHeight) {
      ui.board.style.aspectRatio = `${probe.naturalWidth} / ${probe.naturalHeight}`;
    }
    ui.img.classList.add('is-entering');
    ui.img.src = src;
    ui.img.alt = `${t('imageAlt')}: ${view.prompt || ''}`;
    requestAnimationFrame(() => requestAnimationFrame(() => ui.img.classList.remove('is-entering')));
  }

  function updateControls() {
    const view = state.view;
    const isImage = Boolean(view && view.kind === 'image');
    ui.controls.hidden = !isImage;
    if (!isImage) return;
    const total = view.picks || 3;
    const n = state.picks.length;
    ui.progress.textContent = t('progress', { n, total });
    ui.undo.disabled = state.busy || n === 0;
    ui.reset.disabled = state.busy || n === 0;

    ui.grid.querySelectorAll('.cell').forEach((b, i) => {
      const order = state.picks.indexOf(i);
      const row = Math.floor(i / GRID.n) + 1;
      const col = (i % GRID.n) + 1;
      b.classList.toggle('is-picked', order >= 0);
      b.setAttribute('aria-pressed', String(order >= 0));
      b.setAttribute('aria-label', order >= 0 ? t('cellPicked', { row, col, n: order + 1 }) : t('cell', { row, col }));
      const badge = b.querySelector('.badge');
      if (order >= 0 && !badge) {
        const span = document.createElement('span');
        span.className = 'badge';
        span.setAttribute('aria-hidden', 'true');
        span.textContent = String(order + 1);
        b.append(span);
      } else if (order < 0 && badge) {
        badge.remove();
      }
    });

    ui.prompt.querySelectorAll('.seq-step').forEach((chip, i) => {
      chip.classList.toggle('is-done', i < n);
      chip.classList.toggle('is-current', i === n && !state.busy);
    });
  }

  async function renderView(view, { retry = false } = {}) {
    const prevGen = state.view ? state.view.gen : null;
    state.view = view;
    state.picks = [];
    setLang(view.lang);
    renderHeader(view);
    renderAttempts(view.attempts_left);
    renderPrompt(view);
    renderGrid(view);
    renderOptions(view);
    ui.help.hidden = !view.help;
    syncClock(view);
    showScreen('challenge');
    await renderImage(view);
    setBusy(false);
    if (retry && prevGen !== null) {
      shake();
      toast(t('wrong'), true);
      haptic('notify', 'error');
    }
  }

  function shake() {
    ui.board.classList.remove('shake');
    void ui.board.offsetWidth; // Restart the animation.
    ui.board.classList.add('shake');
  }

  function toast(message, isError) {
    clearTimeout(state.toastId);
    ui.toast.textContent = message;
    ui.toast.classList.toggle('is-error', Boolean(isError));
    ui.toast.classList.add('is-visible');
    state.toastId = setTimeout(() => ui.toast.classList.remove('is-visible'), 3200);
  }

  // ---------- Countdown ----------

  function syncClock(view) {
    if (typeof view.expires_at !== 'number' || typeof view.server_time !== 'number') {
      ui.timer.hidden = true;
      return;
    }
    state.clockOffset = view.server_time - Date.now() / 1000;
    state.total = Math.max(state.total, view.expires_at - view.server_time, 1);
    ui.timer.hidden = false;
    clearInterval(state.timerId);
    tick();
    state.timerId = setInterval(tick, 250);
  }

  function tick() {
    const view = state.view;
    if (!view || state.finished) return;
    const now = Date.now() / 1000 + state.clockOffset;
    const left = Math.max(0, view.expires_at - now);
    const secs = Math.ceil(left);
    ui.timerText.textContent = String(secs);
    ui.timer.setAttribute('aria-label', t('timer', { n: secs }));
    ui.timerBar.style.strokeDashoffset = String(RING * (1 - Math.min(1, left / state.total)));
    ui.timer.classList.toggle('is-low', secs <= 15);
    if (left <= 0 && state.expiryChecked !== view.expires_at && !state.busy) {
      // Ask the server for the authoritative state once per deadline.
      state.expiryChecked = view.expires_at;
      open();
    }
  }

  function stopTimer() {
    clearInterval(state.timerId);
    ui.timer.hidden = true;
  }

  // ---------- Results ----------

  const ICON_CHECK = 'M31 53l12 12 24-27';
  const ICON_CROSS = 'M36 36l24 24M60 36L36 60';
  const ICON_CLOCK = 'M48 30v19l12 8';
  const ICON_WAIT = 'M36 48h.01M48 48h.01M60 48h.01';
  const ICON_INFO = 'M48 44v18M48 33h.01';

  const RESULTS = {
    passed: { tone: 'success', icon: ICON_CHECK, title: 'passedTitle', text: 'passedText', haptic: 'success' },
    failed: { tone: 'error', icon: ICON_CROSS, title: 'failedTitle', text: 'failedText', haptic: 'error' },
    review: { tone: 'info', icon: ICON_WAIT, title: 'reviewTitle', text: 'reviewText', haptic: 'success' },
    expired: { tone: 'warning', icon: ICON_CLOCK, title: 'expiredTitle', text: 'expiredText', haptic: 'warning' },
    closed: { tone: 'info', icon: ICON_INFO, title: 'closedTitle', text: 'closedText' },
    invalid: { tone: 'error', icon: ICON_CROSS, title: 'invalidTitle', text: 'invalidText', haptic: 'error' },
    forbidden: { tone: 'error', icon: ICON_CROSS, title: 'forbiddenTitle', text: 'forbiddenText', haptic: 'error' },
    outside: { tone: 'info', icon: ICON_INFO, title: 'outsideTitle', text: 'outsideText' },
    network: { tone: 'warning', icon: ICON_INFO, title: 'networkTitle', text: 'networkText', retry: true },
  };

  function iconSVG(spec) {
    const sparks = spec.tone === 'success'
      ? Array.from({ length: 8 }, (_, i) => `<i class="spark s${i}"></i>`).join('')
      : '';
    return `<svg viewBox="0 0 96 96" focusable="false"><circle class="disc" cx="48" cy="48" r="44"/>` +
      `<circle class="ring" cx="48" cy="48" r="44" transform="rotate(-90 48 48)"/>` +
      `<path class="mark" d="${spec.icon}"/></svg>${sparks}`;
  }

  function showResult(kind) {
    const spec = RESULTS[kind] || RESULTS.closed;
    state.finished = !spec.retry;
    clearTimeout(state.submitId);
    stopTimer();
    if (state.finished) ui.help.hidden = true;
    ui.resultIcon.className = `result-icon tone-${spec.tone}`;
    ui.resultIcon.innerHTML = iconSVG(spec);
    ui.resultTitle.textContent = t(spec.title);
    const title = state.view && String(state.view.chat_title || '').trim();
    const chat = title ? (state.lang === 'en' ? title : `「${title}」`) : t('chatFallback');
    ui.resultText.textContent = t(spec.text, { chat });
    ui.resultAction.textContent = spec.retry ? t('retry') : t('close');
    ui.resultAction.onclick = spec.retry ? () => boot() : closeApp;
    ui.resultAction.classList.remove('is-busy');
    showScreen('result');
    ui.resultTitle.focus({ preventScroll: true });
    if (spec.haptic) haptic('notify', spec.haptic);
    if (kind === 'passed') setTimeout(closeApp, AUTO_CLOSE_MS);
  }

  // ---------- Flow ----------

  function handleResponse(data, fromAnswer) {
    state.staleReloads = 0;
    if (data.status === 'challenge') return renderView(data, { retry: fromAnswer });
    showResult(data.status);
    return undefined;
  }

  // handleError maps API errors to UI. It returns true when the caller should
  // re-enable input.
  function handleError(err, context) {
    switch (err.code) {
      case 'stale':
        // The challenge moved on (e.g. answered in another client): reload it.
        if (state.staleReloads++ < 2) {
          if (context !== 'open') toast(t('refreshed'), false);
          open();
          return false;
        }
        showResult('closed');
        return false;
      case 'expired':
        showResult('expired');
        return false;
      case 'forbidden':
        showResult('forbidden');
        return false;
      case 'rate_limited':
        if (!state.view) {
          setTimeout(open, 3000);
          return false;
        }
        toast(t('rateLimited'), true);
        haptic('notify', 'warning');
        return true;
      case 'invalid':
        showResult('invalid');
        return false;
      default:
        if (context === 'open' && !state.view) {
          showResult('network');
          return false;
        }
        toast(t('networkRetry'), true);
        haptic('notify', 'error');
        return true;
    }
  }

  async function open() {
    setBusy(true);
    try {
      const data = await api('open');
      await handleResponse(data, false);
    } catch (err) {
      if (handleError(err, 'open')) setBusy(false);
    }
  }

  async function submit(answer) {
    const view = state.view;
    if (!view || state.finished) return;
    setBusy(true);
    try {
      const data = await api('answer', { gen: view.gen, answer });
      await handleResponse(data, true);
    } catch (err) {
      if (handleError(err, 'answer')) {
        state.picks = [];
        for (const b of ui.options.children) b.classList.remove('is-chosen');
        setBusy(false);
      }
    }
  }

  function pickCell(i) {
    const view = state.view;
    if (!view || state.busy || state.finished) return;
    const at = state.picks.indexOf(i);
    if (at >= 0) {
      // Tapping the most recent pick again deselects it.
      if (at === state.picks.length - 1) {
        state.picks.pop();
        haptic('impact', 'soft');
        updateControls();
      } else {
        haptic('notify', 'warning');
      }
      return;
    }
    const total = view.picks || 3;
    if (state.picks.length >= total) return;
    state.picks.push(i);
    haptic('impact', 'light');
    updateControls();
    if (state.picks.length === total) {
      state.busy = true; // Block further taps while the badge animates.
      const answer = state.picks.slice();
      state.submitId = setTimeout(() => submit(answer), AUTO_SUBMIT_DELAY_MS);
    }
  }

  function chooseOption(i, button) {
    if (state.busy || state.finished) return;
    haptic('impact', 'medium');
    button.classList.add('is-chosen');
    submit([i]);
  }

  function undo() {
    if (state.busy || !state.picks.length) return;
    state.picks.pop();
    haptic('impact', 'soft');
    updateControls();
  }

  function resetPicks() {
    if (state.busy || !state.picks.length) return;
    state.picks = [];
    haptic('impact', 'rigid');
    updateControls();
  }

  async function requestHelp() {
    if (state.busy || state.finished) return;
    if (!(await confirmAsync(t('helpConfirm')))) return;
    setBusy(true);
    try {
      const data = await api('help');
      await handleResponse(data, false);
    } catch (err) {
      if (handleError(err, 'help')) setBusy(false);
    }
  }

  function onKey(e) {
    if (ui.app.dataset.screen !== 'challenge' || !state.view || state.view.kind !== 'image') return;
    if (e.key === 'Backspace' && !e.metaKey && !e.ctrlKey) {
      e.preventDefault();
      undo();
    } else if (e.key === 'Escape') {
      resetPicks();
    }
  }

  function readToken() {
    const m = location.pathname.match(/\/v\/([^/?#]+)\/?$/);
    if (!m) return '';
    try {
      return decodeURIComponent(m[1]);
    } catch (_) {
      return '';
    }
  }

  function boot() {
    state.view = null;
    state.finished = false;
    state.maxAttempts = 0;
    state.total = 0;
    showScreen('loading');
    if (!state.token) {
      showResult('invalid');
      return;
    }
    if (!state.initData) {
      showResult('outside');
      return;
    }
    open();
  }

  function init() {
    setupTelegram();
    const user = tg && tg.initDataUnsafe && tg.initDataUnsafe.user;
    state.lang = pickLang(user && user.language_code) || 'zh-CN';
    applyStaticText();
    state.token = readToken();
    state.initData = (tg && typeof tg.initData === 'string' && tg.initData) || '';

    ui.undo.addEventListener('click', undo);
    ui.reset.addEventListener('click', resetPicks);
    ui.help.addEventListener('click', requestHelp);
    ui.board.addEventListener('animationend', () => ui.board.classList.remove('shake'));
    ui.img.addEventListener('contextmenu', (e) => e.preventDefault());
    document.addEventListener('keydown', onKey);
    document.addEventListener('visibilitychange', () => {
      if (!document.hidden) tick();
    });
    boot();
  }

  init();
})();

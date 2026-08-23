(() => {
  "use strict";

  const text = {
    "zh-CN": {
      stateLoading: "连接中",
      stateReady: "等待点击",
      stateSending: "正在检查",
      stateDone: "已完成",
      title: "入群验证",
      loading: "正在安全地载入验证题目…",
      wait: "请稍候。",
      step: "当前步骤",
      time: "剩余时间",
      attempts: "剩余尝试",
      tapFirst: "请按提示点击第一个目标。",
      tapSecond: "现在点击第二个目标。",
      retry: "顺序不正确，已更换题目，请重新尝试。",
      success: "验证成功，可以关闭此窗口。",
      review: "尝试次数已用完，申请将等待管理员审核。",
      expired: "验证已过期，请返回 Telegram 重新开始。",
      invalid: "无法打开此验证，请从 Telegram 中重新进入。",
      stale: "题目已更新，正在重新载入。",
      imageAlt: "3 × 3 随机交互验证图"
    },
    en: {
      stateLoading: "Connecting",
      stateReady: "Ready",
      stateSending: "Checking",
      stateDone: "Complete",
      title: "Join verification",
      loading: "Securely loading the challenge…",
      wait: "Please wait.",
      step: "Current step",
      time: "Time left",
      attempts: "Attempts left",
      tapFirst: "Tap the first target described above.",
      tapSecond: "Now tap the second target.",
      retry: "That order was not correct. A new challenge is ready.",
      success: "Verification passed. You may close this window.",
      review: "No attempts remain. Your request will await administrator review.",
      expired: "Verification expired. Return to Telegram and start again.",
      invalid: "This verification cannot be opened. Please re-enter from Telegram.",
      stale: "The challenge changed and is being reloaded.",
      imageAlt: "3 by 3 random interactive verification image"
    }
  };

  const elements = {
    state: document.querySelector("#state-label"),
    eyebrow: document.querySelector("#instruction-title"),
    prompt: document.querySelector("#prompt"),
    frame: document.querySelector("#challenge-frame"),
    image: document.querySelector("#challenge-image"),
    markers: document.querySelector("#tap-markers"),
    stepLabel: document.querySelector("#step-label"),
    stepText: document.querySelector("#step-text"),
    stepDots: [document.querySelector("#step-dot-1"), document.querySelector("#step-dot-2")],
    timeLabel: document.querySelector("#time-label"),
    time: document.querySelector("#time-value"),
    attemptLabel: document.querySelector("#attempt-label"),
    attempts: document.querySelector("#attempt-value"),
    status: document.querySelector("#status-message")
  };

  const state = {
    language: "zh-CN",
    token: "",
    initData: "",
    generation: 0,
    expiresAt: 0,
    clicks: [],
    busy: false,
    terminal: false,
    timer: null
  };

  function strings() {
    return text[state.language] || text["zh-CN"];
  }

  function applyLanguage(language) {
    state.language = language === "en" ? "en" : "zh-CN";
    const copy = strings();
    document.documentElement.lang = state.language;
    elements.eyebrow.textContent = copy.title;
    elements.stepLabel.textContent = copy.step;
    elements.timeLabel.textContent = copy.time;
    elements.attemptLabel.textContent = copy.attempts;
    elements.image.alt = copy.imageAlt;
  }

  function setStatus(message, kind = "") {
    elements.status.textContent = message;
    elements.status.className = `status-message${kind ? ` ${kind}` : ""}`;
  }

  function setStep(step) {
    elements.stepText.textContent = `${Math.min(step, 2)} / 2`;
    elements.stepDots[0].classList.toggle("active", step <= 1);
    elements.stepDots[1].classList.toggle("active", step >= 2);
  }

  function resetClicks() {
    state.clicks = [];
    elements.markers.replaceChildren();
    setStep(1);
  }

  function setBusy(value) {
    state.busy = value;
    elements.frame.classList.toggle("busy", value);
    elements.state.textContent = value ? strings().stateSending : strings().stateReady;
  }

  function terminal(message, kind) {
    state.terminal = true;
    setBusy(false);
    elements.state.textContent = strings().stateDone;
    elements.frame.hidden = true;
    setStatus(message, kind);
    if (state.timer) {
      clearInterval(state.timer);
    }
  }

  function updateClock() {
    if (!state.expiresAt || state.terminal) return;
    const seconds = Math.max(0, Math.ceil(state.expiresAt - Date.now() / 1000));
    const minutesPart = String(Math.floor(seconds / 60)).padStart(2, "0");
    const secondsPart = String(seconds % 60).padStart(2, "0");
    elements.time.textContent = `${minutesPart}:${secondsPart}`;
    if (seconds === 0) terminal(strings().expired, "error");
  }

  async function jsonRequest(path, body) {
    const response = await fetch(path, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
      cache: "no-store",
      credentials: "same-origin"
    });
    let payload = {};
    try {
      payload = await response.json();
    } catch (_) {
      // The generic error below intentionally avoids exposing response details.
    }
    if (!response.ok) {
      const error = new Error(payload.error || "invalid_session");
      error.code = payload.error || "invalid_session";
      error.status = response.status;
      throw error;
    }
    return payload;
  }

  function applyView(view, message) {
    applyLanguage(view.language);
    state.generation = view.generation;
    state.expiresAt = view.expires_at;
    elements.prompt.textContent = view.prompt;
    elements.attempts.textContent = String(view.remaining_attempts);
    elements.image.src = view.image_url;
    elements.frame.hidden = false;
    resetClicks();
    setBusy(false);
    setStatus(message || strings().tapFirst);
    updateClock();
    if (!state.timer) state.timer = setInterval(updateClock, 250);
  }

  async function openChallenge() {
    try {
      const view = await jsonRequest(`/api/verify/${encodeURIComponent(state.token)}/open`, { init_data: state.initData });
      applyView(view);
    } catch (error) {
      if (error.code === "expired") terminal(strings().expired, "error");
      else terminal(strings().invalid, "error");
    }
  }

  async function reloadAfterStale() {
    setStatus(strings().stale);
    await openChallenge();
  }

  async function submitClicks() {
    setBusy(true);
    try {
      const result = await jsonRequest(`/api/verify/${encodeURIComponent(state.token)}/answer`, {
        init_data: state.initData,
        generation: state.generation,
        clicks: state.clicks.map(({ x, y }) => ({ x, y }))
      });
      if (result.status === "challenge_passed") {
        terminal(strings().success, "success");
      } else if (result.status === "awaiting_review") {
        terminal(strings().review, "");
      } else {
        applyView(result, strings().retry);
      }
    } catch (error) {
      if (error.code === "stale_challenge") await reloadAfterStale();
      else if (error.code === "expired") terminal(strings().expired, "error");
      else terminal(strings().invalid, "error");
    }
  }

  function addMarker(point, index) {
    const marker = document.createElement("span");
    const column = Math.floor(point.x * 3);
    const row = Math.floor(point.y * 3);
    marker.className = `tap-marker marker-cell-${row * 3 + column}`;
    marker.textContent = String(index + 1);
    elements.markers.append(marker);
  }

  elements.image.addEventListener("pointerup", (event) => {
    if (state.busy || state.terminal || state.clicks.length >= 2) return;
    const bounds = elements.image.getBoundingClientRect();
    if (bounds.width <= 0 || bounds.height <= 0) return;
    const point = {
      x: (event.clientX - bounds.left) / bounds.width,
      y: (event.clientY - bounds.top) / bounds.height
    };
    if (point.x < 0 || point.x >= 1 || point.y < 0 || point.y >= 1) return;
    state.clicks.push(point);
    addMarker(point, state.clicks.length - 1);
    if (state.clicks.length === 1) {
      setStep(2);
      setStatus(strings().tapSecond);
    } else {
      submitClicks();
    }
  });

  function start() {
    const webApp = window.Telegram && window.Telegram.WebApp;
    const pathParts = window.location.pathname.split("/").filter(Boolean);
    state.token = pathParts.length === 2 && pathParts[0] === "verify" ? pathParts[1] : "";
    state.initData = webApp ? webApp.initData : "";
    if (webApp) {
      webApp.ready();
      webApp.expand();
    }
    if (!state.token || !state.initData) {
      terminal(strings().invalid, "error");
      return;
    }
    openChallenge();
  }

  applyLanguage("zh-CN");
  start();
})();

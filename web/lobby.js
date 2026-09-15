// owned by client-net: lobby UI (create/join room, roster, ready, countdown, errors); hands off to game.js on match_start
import { createNet } from "./net.js";
import { startGame } from "./game.js";

const NAME_KEY = "mazeroyale.name";
const CONFIG_KEY = "mazeroyale.config";

const DEFAULT_CONFIG = {
  maze_size: "medium",
  catchers: 2,
  catcher_speed: 1.0,
  round_seconds: 120,
  min_players: 2,
  fill_bots: false,
};

const state = {
  name: localStorage.getItem(NAME_KEY) || "",
  room: null, // latest room_state frame
  countdownMs: 0,
  countdownAt: 0, // Date.now() when countdownMs was received
  error: null,
  errorTimer: null,
  connected: false,
  inMatch: false,
  view: "home", // home | room
  ready: false,
  copied: false,
  copiedTimer: null,
};

const root = document.getElementById("lobby");
const canvas = document.getElementById("game");
const net = createNet();

// ---- networking ----

net.on("open", ({ reconnecting }) => {
  state.connected = true;
  if (!reconnecting && state.name) net.hello(state.name);
  render();
});

net.on("close", () => {
  state.connected = false;
  render();
});

net.on("welcome", () => render());

net.on("room_state", (msg) => {
  const me = msg.players.find((p) => p.player_id === net.playerId);
  if (!me) {
    // Roster without us: the ack for our own leave. Stay on the home view.
    if (state.room && state.room.code === msg.code) state.room = null;
    if (!state.room) state.view = "home";
    net.roomCode = null;
    render();
    return;
  }
  state.room = msg;
  state.view = "room";
  state.countdownMs = msg.countdown_ms || 0;
  state.countdownAt = Date.now();
  state.ready = me.ready;
  render();
});

net.on("event", (msg) => {
  if (msg.kind === "countdown") {
    state.countdownMs = msg.ms || 0;
    state.countdownAt = Date.now();
    render();
  } else if (msg.kind === "bot_joined" && state.room) {
    // room_state follows; nothing to do beyond a hint.
    render();
  }
});

net.on("error", (msg) => {
  showError(`${msg.code || "error"}: ${msg.message || "unknown error"}`);
});

net.on("match_start", (msg) => {
  state.inMatch = true;
  state.countdownMs = 0;
  root.hidden = true;
  canvas.hidden = false;
  render();
  try {
    startGame(net, msg, {
      onPlayAgain: () => {
        // The room is already back in the lobby (gateway called /end); just ready up.
        if (state.room) {
          state.view = "room";
          state.ready = true;
          net.setReady(true);
        }
        render();
      },
      onBackToLobby: () => {
        if (state.room) net.leaveRoom();
        state.room = null;
        state.ready = false;
        state.countdownMs = 0;
        state.view = "home";
        render();
      },
    });
  } catch (err) {
    console.error("lobby: startGame failed", err);
    showError(`client: ${err.message}`);
  }
});

net.on("match_end", () => {
  state.inMatch = false;
  state.ready = false;
  state.countdownMs = 0;
  render();
});

function copyCode(code) {
  const done = () => {
    state.copied = true;
    clearTimeout(state.copiedTimer);
    state.copiedTimer = setTimeout(() => { state.copied = false; render(); }, 1500);
    render();
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(code).then(done, () => fallbackCopy(code) && done());
  } else if (fallbackCopy(code)) {
    done();
  }
}

function fallbackCopy(text) {
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.setAttribute("readonly", "");
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch (err) { ok = false; }
  ta.remove();
  return ok;
}

function showError(text) {
  state.error = text;
  clearTimeout(state.errorTimer);
  state.errorTimer = setTimeout(() => {
    state.error = null;
    render();
  }, 6000);
  render();
}

// Countdown ticker: re-render every 100 ms while a countdown is running.
setInterval(() => {
  if (remainingCountdown() > 0 && !state.inMatch) render();
}, 100);

function remainingCountdown() {
  if (!state.countdownMs) return 0;
  return Math.max(0, state.countdownMs - (Date.now() - state.countdownAt));
}

// ---- actions ----

function setName(name) {
  name = name.trim().slice(0, 24);
  state.name = name;
  localStorage.setItem(NAME_KEY, name);
}

function requireName() {
  if (!state.name) {
    showError("client: enter a name first");
    return false;
  }
  net.hello(state.name);
  return true;
}

function readConfig(form) {
  const fd = new FormData(form);
  const cfg = {
    maze_size: String(fd.get("maze_size")),
    catchers: Number(fd.get("catchers")),
    catcher_speed: Number(fd.get("catcher_speed")),
    round_seconds: Number(fd.get("round_seconds")),
    min_players: Number(fd.get("min_players")),
    fill_bots: fd.get("fill_bots") === "on",
  };
  const seed = String(fd.get("seed") || "").trim();
  if (seed !== "") {
    if (!/^-?\d+$/.test(seed)) throw new Error("seed must be an integer");
    cfg.seed = Number(seed);
  }
  if (!["small", "medium", "large"].includes(cfg.maze_size)) throw new Error("maze size must be small, medium or large");
  if (!Number.isInteger(cfg.catchers) || cfg.catchers < 1 || cfg.catchers > 4) throw new Error("catchers must be 1-4");
  if (!(cfg.catcher_speed >= 0.6 && cfg.catcher_speed <= 1.4)) throw new Error("catcher speed must be 0.6-1.4");
  if (!Number.isInteger(cfg.round_seconds) || cfg.round_seconds < 30 || cfg.round_seconds > 300) throw new Error("round seconds must be 30-300");
  if (!Number.isInteger(cfg.min_players) || cfg.min_players < 2 || cfg.min_players > 8) throw new Error("min players must be 2-8");
  return cfg;
}

function loadConfig() {
  try {
    return { ...DEFAULT_CONFIG, ...JSON.parse(localStorage.getItem(CONFIG_KEY) || "{}") };
  } catch (err) {
    return { ...DEFAULT_CONFIG };
  }
}

// ---- rendering ----

function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === false || v == null) continue;
    if (k === "class") el.className = v;
    else if (k === "style") el.style.cssText = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, String(v));
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function render() {
  if (!root) return;
  const focused = document.activeElement;
  const focusId = focused && root.contains(focused) ? focused.id : null;
  const focusValue = focusId && "value" in focused ? focused.value : null;
  const focusSel = focusId && typeof focused.selectionStart === "number" ? [focused.selectionStart, focused.selectionEnd] : null;

  root.replaceChildren(...[
    renderHeader(),
    state.error ? h("div", { class: "lobby-error", role: "alert" }, state.error) : null,
    state.view === "room" && state.room ? renderRoom() : renderHome(),
  ].filter(Boolean));

  if (focusId) {
    const el = document.getElementById(focusId);
    if (el) {
      if (focusValue != null && "value" in el && el.value !== focusValue) el.value = focusValue;
      el.focus();
      if (focusSel && typeof el.setSelectionRange === "function") {
        try { el.setSelectionRange(focusSel[0], focusSel[1]); } catch (err) { /* non-text input */ }
      }
    }
  }
}

function renderHeader() {
  const latency = net.latencyMs == null ? "–" : `${net.latencyMs} ms`;
  return h("div", { class: "lobby-header" },
    h("h1", null, "Maze Royale"),
    h("div", { class: "lobby-status" },
      h("span", { class: state.connected ? "status-ok" : "status-bad" }, state.connected ? "connected" : "reconnecting…"),
      " · ping ", latency,
      net.playerId ? [" · you: ", h("code", null, net.playerId)] : null,
    ),
  );
}

function renderHome() {
  const cfg = loadConfig();
  const nameInput = h("input", {
    id: "lobby-name", type: "text", maxlength: 24, placeholder: "Your name", value: state.name, autocomplete: "off",
    onchange: (e) => setName(e.target.value),
    oninput: (e) => { state.name = e.target.value; },
  });

  const createForm = h("form", {
    class: "lobby-create", id: "lobby-create-form",
    onsubmit: (e) => {
      e.preventDefault();
      setName(nameInput.value);
      if (!requireName()) return;
      let config;
      try {
        config = readConfig(e.target);
      } catch (err) {
        showError(`client: ${err.message}`);
        return;
      }
      localStorage.setItem(CONFIG_KEY, JSON.stringify(config));
      net.createRoom(config);
    },
  },
    h("h2", null, "Create room"),
    field("Maze size", h("select", { id: "cfg-maze-size", name: "maze_size" },
      ...[["small", "small (15×15)"], ["medium", "medium (21×21)"], ["large", "large (31×31)"]].map(([v, label]) =>
        h("option", { value: v, selected: cfg.maze_size === v }, label)))),
    field("Catchers (1-4)", h("input", { id: "cfg-catchers", name: "catchers", type: "number", min: 1, max: 4, step: 1, value: cfg.catchers, required: true })),
    field("Catcher speed (0.6-1.4)", h("input", { id: "cfg-catcher-speed", name: "catcher_speed", type: "number", min: 0.6, max: 1.4, step: 0.1, value: cfg.catcher_speed, required: true })),
    field("Round seconds (30-300)", h("input", { id: "cfg-round-seconds", name: "round_seconds", type: "number", min: 30, max: 300, step: 1, value: cfg.round_seconds, required: true })),
    field("Min players (2-8)", h("input", { id: "cfg-min-players", name: "min_players", type: "number", min: 2, max: 8, step: 1, value: cfg.min_players, required: true })),
    field("Fill with bots", h("input", { id: "cfg-fill-bots", name: "fill_bots", type: "checkbox", checked: !!cfg.fill_bots })),
    field("Seed (optional)", h("input", { id: "cfg-seed", name: "seed", type: "text", inputmode: "numeric", placeholder: "random", value: cfg.seed ?? "" })),
    h("button", { type: "submit", class: "primary", disabled: !state.connected, title: state.connected ? null : "waiting for connection" }, "Create room"),
  );

  const joinForm = h("form", {
    class: "lobby-join", id: "lobby-join-form",
    onsubmit: (e) => {
      e.preventDefault();
      setName(nameInput.value);
      if (!requireName()) return;
      const code = new FormData(e.target).get("code").toString().trim().toUpperCase();
      if (!code) {
        showError("client: enter a room code");
        return;
      }
      net.joinRoom(code);
    },
  },
    h("h2", null, "Join room"),
    field("Room code", h("input", {
      id: "join-code", name: "code", type: "text", placeholder: "ABCD", autocomplete: "off",
      autocapitalize: "characters", spellcheck: "false", maxlength: 8, class: "code-input",
    })),
    h("button", { type: "submit", class: "primary", disabled: !state.connected, title: state.connected ? null : "waiting for connection" }, "Join"),
  );

  return h("div", { class: "lobby-home" },
    h("section", { class: "lobby-name-section" }, h("label", { for: "lobby-name" }, "Name "), nameInput),
    h("div", { class: "lobby-columns" }, createForm, joinForm),
  );
}

function field(label, control) {
  return h("label", { class: "lobby-field" }, h("span", null, label), control);
}

function renderRoom() {
  const room = state.room;
  const me = room.players.find((p) => p.player_id === net.playerId);
  const isCreator = !!(me && me.is_creator);
  const remaining = remainingCountdown();
  const counting = remaining > 0;
  const cfg = room.config || {};
  const humans = room.players.filter((p) => !p.is_bot);
  const readyCount = room.players.filter((p) => p.ready).length;
  const minPlayers = cfg.min_players || 2;
  const missing = Math.max(0, minPlayers - room.players.length);
  const canStart = room.players.length >= 2 || !!cfg.fill_bots;

  let hint;
  if (counting) hint = null;
  else if (missing > 0) hint = `Waiting for ${missing} more player${missing === 1 ? "" : "s"}` + (cfg.fill_bots ? " (bots will fill the rest)" : "") + "…";
  else if (readyCount < room.players.length) hint = `${readyCount} / ${room.players.length} ready — starts when everyone is ready` + (isCreator ? " or you press Start now" : "");
  else hint = "Everyone is ready — starting…";

  return h("div", { class: "lobby-room" },
    h("div", { class: "lobby-code" },
      h("span", { class: "code-label" }, isCreator ? "Share this code" : "Room code"),
      h("div", { class: "code-row" },
        h("strong", {
          class: "code", id: "room-code", title: "Click to copy", role: "button", tabindex: 0,
          onclick: () => copyCode(room.code),
          onkeydown: (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); copyCode(room.code); } },
        }, room.code),
        h("button", {
          type: "button", class: state.copied ? "small copied" : "small",
          onclick: () => copyCode(room.code),
        }, state.copied ? "Copied!" : "Copy"),
      ),
    ),
    counting
      ? h("div", { class: "lobby-countdown", role: "status" }, "Starting in ", h("strong", null, (remaining / 1000).toFixed(1)), " s")
      : h("div", { class: "lobby-hint", role: "status" }, hint),
    h("div", { class: "lobby-config" },
      `${cfg.maze_size} maze · ${cfg.catchers} catcher${cfg.catchers === 1 ? "" : "s"} @ ${cfg.catcher_speed}× · ` +
      `${cfg.round_seconds}s round · min ${cfg.min_players} players` +
      (cfg.fill_bots ? " · bots fill" : "") +
      (cfg.seed != null ? ` · seed ${cfg.seed}` : ""),
    ),
    h("h2", { class: "roster-title" }, `Players (${room.players.length}${humans.length !== room.players.length ? `, ${humans.length} human` : ""})`),
    h("ul", { class: "lobby-roster" },
      ...room.players.map((p) => h("li", { class: p.player_id === net.playerId ? "me" : null },
        h("span", { class: "swatch", style: `background:${p.color || "#888"}`, title: p.color || "" }),
        h("span", { class: "name" }, p.name || p.player_id, p.player_id === net.playerId ? h("span", { class: "you" }, " (you)") : null),
        p.is_creator ? h("span", { class: "badge creator", title: "room creator" }, "creator") : null,
        p.is_bot ? h("span", { class: "badge bot" }, "bot") : null,
        h("span", { class: p.ready ? "ready yes" : "ready no" }, p.ready ? "✓ ready" : "not ready"),
      )),
    ),
    h("div", { class: "lobby-actions" },
      h("button", {
        type: "button", class: state.ready ? "" : "primary", disabled: counting || !state.connected,
        title: counting ? "match is starting" : null,
        onclick: () => { state.ready = !state.ready; net.setReady(state.ready); render(); },
      }, state.ready ? "Unready" : "Ready"),
      isCreator ? h("button", {
        type: "button", class: "primary", disabled: counting || !canStart || !state.connected,
        title: counting ? "match is starting" : !canStart ? "need at least 2 players (or fill with bots)" : "start without waiting for everyone to be ready",
        onclick: () => net.startRoom(),
      }, "Start now") : null,
      h("button", {
        type: "button", disabled: counting, title: counting ? "match is starting" : null,
        onclick: () => {
          net.leaveRoom();
          state.room = null;
          state.ready = false;
          state.countdownMs = 0;
          state.view = "home";
          render();
        },
      }, "Leave"),
    ),
  );
}

render();

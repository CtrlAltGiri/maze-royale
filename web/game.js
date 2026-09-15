// owned by client-render: match loop, keyboard input, snapshot interpolation, spectator mode, standings overlay
import { drawFrame, DEATH_MS } from './render.js';

const TICK_MS = 50;
const KEY_DIRS = {
  ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right',
  w: 'up', s: 'down', a: 'left', d: 'right',
  W: 'up', S: 'down', A: 'left', D: 'right',
};

function fmtTime(ms) {
  const s = Math.max(0, Math.ceil(ms / 1000));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text != null) e.textContent = text;
  return e;
}

function lerp(a, b, t) { return a + (b - a) * t; }

// Start a match on the #game canvas. net: {send(obj), on(type, handler) -> unsubscribe, playerId}.
export function startGame(net, matchStart) {
  const canvas = document.getElementById('game');
  const lobby = document.getElementById('lobby');
  const overlay = document.getElementById('overlay');
  const ctx = canvas.getContext('2d');

  if (lobby) lobby.hidden = true;
  canvas.hidden = false;
  overlay.innerHTML = '';
  overlay.hidden = true;

  let hud = document.getElementById('hud');
  if (!hud) {
    hud = el('div');
    hud.id = 'hud';
    document.body.appendChild(hud);
  }
  hud.innerHTML = '';
  hud.hidden = false;
  const hudTimer = el('span', 'hud-timer', fmtTime(matchStart.round_ms));
  const hudAlive = el('span', 'hud-alive', '');
  const hudStatus = el('span', 'hud-status', '');
  hud.append(hudTimer, hudAlive, hudStatus);

  const myId = (matchStart.you && matchStart.you.player_id) || net.playerId;
  let spectating = !matchStart.you || matchStart.you.role !== 'runner';

  // Static per player info from match_start.
  const info = new Map();
  for (const p of matchStart.players || []) {
    info.set(p.player_id, { name: p.name, color: p.color || '#4fc3f7', x: p.x, y: p.y });
  }

  // Snapshot buffer: interpolate from prev toward cur over one tick.
  let prev = null, cur = null, curAt = 0;
  const initial = {
    tick: 0, ts: 0, time_left_ms: matchStart.round_ms,
    alive_count: (matchStart.players || []).length,
    players: (matchStart.players || []).map(p => ({ id: p.player_id, x: p.x, y: p.y, dir: 'none', alive: true })),
    catchers: (matchStart.catchers || []).map(c => ({ id: c.id, x: c.x, y: c.y, dir: 'none' })),
  };
  cur = initial; prev = initial; curAt = performance.now();

  // Deaths: id -> {start, x, y}
  const deaths = new Map();
  const deadIds = new Set();

  const unsubs = [];
  let ended = false;

  function setStatus() {
    hudAlive.textContent = `Alive: ${cur.alive_count}`;
    hudStatus.textContent = spectating ? 'Spectating' : '';
    hud.classList.toggle('spectating', spectating);
  }
  setStatus();

  unsubs.push(net.on('snapshot', snap => {
    if (ended) return;
    prev = cur; cur = snap; curAt = performance.now();
    hudTimer.textContent = fmtTime(snap.time_left_ms);
    for (const p of snap.players) {
      if (!p.alive && !deadIds.has(p.id) && !deaths.has(p.id)) {
        deaths.set(p.id, { start: performance.now(), x: p.x, y: p.y });
      }
    }
    setStatus();
  }));

  unsubs.push(net.on('event', ev => {
    if (ended || ev.kind !== 'caught') return;
    if (!deaths.has(ev.player_id) && !deadIds.has(ev.player_id)) {
      const sp = (cur.players || []).find(p => p.id === ev.player_id);
      const st = info.get(ev.player_id);
      deaths.set(ev.player_id, {
        start: performance.now(),
        x: sp ? sp.x : (st ? st.x : 0),
        y: sp ? sp.y : (st ? st.y : 0),
      });
    }
    if (ev.player_id === myId && !spectating) {
      spectating = true;
      sendDir('none');
      setStatus();
    }
  }));

  unsubs.push(net.on('match_end', end => {
    ended = true;
    sendDir('none');
    showStandings(end);
  }));

  // ---- keyboard input ----
  let seq = 0;
  let lastDir = 'none';
  const held = [];
  function sendDir(dir) {
    if (dir === lastDir) return;
    lastDir = dir;
    net.send({ t: 'input', seq: ++seq, dir });
  }
  function onKeyDown(e) {
    const dir = KEY_DIRS[e.key];
    if (!dir) return;
    e.preventDefault();
    if (spectating || ended) return;
    if (e.repeat) return;
    const i = held.indexOf(dir);
    if (i >= 0) held.splice(i, 1);
    held.push(dir);
    sendDir(dir);
  }
  function onKeyUp(e) {
    const dir = KEY_DIRS[e.key];
    if (!dir) return;
    e.preventDefault();
    const i = held.indexOf(dir);
    if (i >= 0) held.splice(i, 1);
    if (spectating || ended) return;
    sendDir(held.length ? held[held.length - 1] : 'none');
  }
  function onBlur() {
    held.length = 0;
    if (!spectating && !ended) sendDir('none');
  }
  window.addEventListener('keydown', onKeyDown);
  window.addEventListener('keyup', onKeyUp);
  window.addEventListener('blur', onBlur);

  // ---- render loop ----
  let raf = 0;
  function frame(now) {
    const alpha = Math.min(1, Math.max(0, (now - curAt) / TICK_MS));
    const prevById = new Map((prev.players || []).map(p => [p.id, p]));
    const prevCatch = new Map((prev.catchers || []).map(c => [c.id, c]));

    const runners = [];
    for (const p of cur.players || []) {
      const st = info.get(p.id) || { name: p.id, color: '#4fc3f7' };
      const d = deaths.get(p.id);
      if (d) {
        const prog = (now - d.start) / DEATH_MS;
        if (prog >= 1) {
          deaths.delete(p.id);
          deadIds.add(p.id);
          runners.push({ x: d.x, y: d.y, color: st.color, name: st.name, alive: false, isYou: p.id === myId, deathProgress: null });
        } else {
          runners.push({ x: d.x, y: d.y, color: st.color, name: st.name, alive: false, isYou: p.id === myId, deathProgress: prog });
        }
        continue;
      }
      if (!p.alive) {
        runners.push({ x: p.x, y: p.y, color: st.color, name: st.name, alive: false, isYou: p.id === myId, deathProgress: null });
        continue;
      }
      const q = prevById.get(p.id) || p;
      runners.push({
        x: lerp(q.x, p.x, alpha), y: lerp(q.y, p.y, alpha),
        color: st.color, name: st.name, alive: true, isYou: p.id === myId, deathProgress: null,
      });
    }
    const catchers = [];
    for (const c of cur.catchers || []) {
      const q = prevCatch.get(c.id) || c;
      catchers.push({ x: lerp(q.x, c.x, alpha), y: lerp(q.y, c.y, alpha), dir: c.dir });
    }
    drawFrame(ctx, canvas, { maze: matchStart.maze, runners, catchers });
    raf = requestAnimationFrame(frame);
  }
  raf = requestAnimationFrame(frame);

  function stop() {
    cancelAnimationFrame(raf);
    window.removeEventListener('keydown', onKeyDown);
    window.removeEventListener('keyup', onKeyUp);
    window.removeEventListener('blur', onBlur);
    for (const u of unsubs) if (typeof u === 'function') u();
    unsubs.length = 0;
  }

  function showStandings(end) {
    overlay.innerHTML = '';
    overlay.hidden = false;
    const panel = el('div', 'panel');
    const winners = new Set(end.winner_ids || []);
    const youWon = winners.has(myId);
    panel.append(el('h1', null, youWon ? 'You survived!' : winners.size ? 'Match over' : 'Nobody survived'));
    const table = el('table', 'standings');
    const thead = el('thead');
    const hr = el('tr');
    for (const h of ['#', 'Player', 'Survived']) hr.append(el('th', null, h));
    thead.append(hr);
    table.append(thead);
    const tbody = el('tbody');
    const rows = [...(end.standings || [])].sort((a, b) => a.place - b.place);
    for (const s of rows) {
      const tr = el('tr');
      if (winners.has(s.player_id)) tr.classList.add('winner');
      if (s.player_id === myId) tr.classList.add('you');
      const st = info.get(s.player_id);
      const nameCell = el('td');
      const swatch = el('span', 'swatch');
      swatch.style.background = st ? st.color : '#888';
      nameCell.append(swatch, document.createTextNode(s.name || (st ? st.name : s.player_id)));
      tr.append(el('td', null, String(s.place)), nameCell, el('td', null, fmtTime(s.survived_ms)));
      tbody.append(tr);
    }
    table.append(tbody);
    panel.append(table);
    const btn = el('button', 'primary', 'Back to lobby');
    btn.addEventListener('click', () => {
      stop();
      overlay.hidden = true;
      overlay.innerHTML = '';
      hud.hidden = true;
      canvas.hidden = true;
      if (lobby) lobby.hidden = false;
    });
    panel.append(btn);
    overlay.append(panel);
  }

  return { stop };
}

// ---------------------------------------------------------------------------
// Demo mode: open index.html#demo via a static file server to exercise the
// renderer without the backend. Fabricates match_start, snapshots, a caught
// event and match_end through a fake net.
// ---------------------------------------------------------------------------

function demoMaze(w, h, seed) {
  let s = seed >>> 0 || 1;
  const rnd = () => { s = (s * 1664525 + 1013904223) >>> 0; return s / 4294967296; };
  const walls = new Array(w * h).fill(15);
  const seen = new Uint8Array(w * h);
  const stack = [[0, 0]];
  seen[0] = 1;
  const D = [[0, -1, 1, 4], [1, 0, 2, 8], [0, 1, 4, 1], [-1, 0, 8, 2]];
  while (stack.length) {
    const [x, y] = stack[stack.length - 1];
    const opts = D.filter(([dx, dy]) => {
      const nx = x + dx, ny = y + dy;
      return nx >= 0 && ny >= 0 && nx < w && ny < h && !seen[ny * w + nx];
    });
    if (!opts.length) { stack.pop(); continue; }
    const [dx, dy, bit, back] = opts[Math.floor(rnd() * opts.length)];
    const nx = x + dx, ny = y + dy;
    walls[y * w + x] &= ~bit;
    walls[ny * w + nx] &= ~back;
    seen[ny * w + nx] = 1;
    stack.push([nx, ny]);
  }
  // knock out a few extra walls for loops
  for (let i = 0; i < (w * h) / 6; i++) {
    const x = Math.floor(rnd() * w), y = Math.floor(rnd() * h);
    const [dx, dy, bit, back] = D[Math.floor(rnd() * 4)];
    const nx = x + dx, ny = y + dy;
    if (nx < 0 || ny < 0 || nx >= w || ny >= h) continue;
    walls[y * w + x] &= ~bit;
    walls[ny * w + nx] &= ~back;
  }
  return { w, h, walls, rnd };
}

const DIR_VEC = { up: [0, -1, 1], down: [0, 1, 4], left: [-1, 0, 8], right: [1, 0, 2], none: [0, 0, 0] };

function demoStep(maze, ent, speed, dt) {
  // Move along cell centers; only change direction at cell centers, block on walls.
  const dist = speed * dt;
  const cx = Math.floor(ent.x), cy = Math.floor(ent.y);
  const atCenter = Math.abs(ent.x - (cx + 0.5)) < 1e-6 && Math.abs(ent.y - (cy + 0.5)) < 1e-6;
  const want = ent.want || 'none';
  if (atCenter) {
    ent.x = cx + 0.5; ent.y = cy + 0.5;
    const [, , bit] = DIR_VEC[want];
    if (want === 'none' || (maze.walls[cy * maze.w + cx] & bit)) { ent.dir = 'none'; return; }
    ent.dir = want;
  } else if (want !== 'none' && want !== ent.dir) {
    // reversing along the current axis is allowed mid cell
    const [wx, wy] = DIR_VEC[want];
    const [ex, ey] = DIR_VEC[ent.dir || 'none'];
    if ((wx && ex) || (wy && ey)) ent.dir = want;
  }
  const [dx, dy] = DIR_VEC[ent.dir || 'none'];
  if (!dx && !dy) return;
  const ahead = v => Math.floor(v - 0.5) + 1.5;
  const behind = v => Math.ceil(v - 0.5) - 0.5;
  const tx = dx > 0 ? ahead(ent.x) : dx < 0 ? behind(ent.x) : ent.x;
  const ty = dy > 0 ? ahead(ent.y) : dy < 0 ? behind(ent.y) : ent.y;
  const rem = Math.abs(dx ? tx - ent.x : ty - ent.y);
  if (dist >= rem - 1e-9) { ent.x = tx; ent.y = ty; }
  else { ent.x += dx * dist; ent.y += dy * dist; }
}

function runDemo() {
  const handlers = new Map();
  const net = {
    playerId: 'p1',
    send(obj) {
      if (obj.t === 'input' && me.alive) me.want = obj.dir;
    },
    on(type, h) {
      if (!handlers.has(type)) handlers.set(type, new Set());
      handlers.get(type).add(h);
      return () => handlers.get(type).delete(h);
    },
  };
  const emit = (type, msg) => { for (const h of handlers.get(type) || []) h(msg); };

  const size = 21;
  const maze = demoMaze(size, size, 12345);
  const roundMs = 60000;
  const names = ['You', 'Bravo', 'Charlie', 'Delta'];
  const colors = ['#4fc3f7', '#ffd54f', '#81c784', '#ce93d8'];
  const spawns = [[0.5, 0.5], [size - 0.5, 0.5], [0.5, size - 0.5], [size - 0.5, size - 0.5]];
  const players = names.map((n, i) => ({
    id: `p${i + 1}`, name: n, color: colors[i], x: spawns[i][0], y: spawns[i][1], dir: 'none', want: 'none', alive: true, survived: 0,
  }));
  const me = players[0];
  const catchers = [
    { id: 'c1', x: Math.floor(size / 2) + 0.5, y: Math.floor(size / 2) + 0.5, dir: 'none', want: 'none' },
    { id: 'c2', x: Math.floor(size / 2) + 0.5, y: 3.5, dir: 'none', want: 'none' },
  ];

  const matchStart = {
    t: 'match_start', match_id: 'demo', maze: { w: maze.w, h: maze.h, walls: maze.walls },
    you: { player_id: 'p1', role: 'runner', color: colors[0] },
    players: players.map(p => ({ player_id: p.id, name: p.name, role: 'runner', color: p.color, x: p.x, y: p.y })),
    catchers: catchers.map(c => ({ id: c.id, x: c.x, y: c.y })),
    round_ms: roundMs,
  };
  startGame(net, matchStart);

  const opposite = { up: 'down', down: 'up', left: 'right', right: 'left', none: 'none' };
  function pickDir(ent, avoidReverse) {
    const cx = Math.floor(ent.x), cy = Math.floor(ent.y);
    const b = maze.walls[cy * maze.w + cx];
    let opts = ['up', 'right', 'down', 'left'].filter(d => !(b & DIR_VEC[d][2]));
    if (avoidReverse && opts.length > 1) opts = opts.filter(d => d !== opposite[ent.dir]);
    return opts[Math.floor(maze.rnd() * opts.length)] || 'none';
  }

  let tick = 0, elapsed = 0, ended = false;
  const timer = setInterval(() => {
    if (ended) return;
    tick++; elapsed += TICK_MS;
    const dt = TICK_MS / 1000;
    for (const p of players) {
      if (!p.alive) continue;
      p.survived = elapsed;
      if (p !== me) {
        const cx = Math.floor(p.x), cy = Math.floor(p.y);
        if (Math.abs(p.x - cx - 0.5) < 1e-3 && Math.abs(p.y - cy - 0.5) < 1e-3) p.want = pickDir(p, true);
      }
      demoStep(maze, p, 4.0, dt);
    }
    for (const c of catchers) {
      const cx = Math.floor(c.x), cy = Math.floor(c.y);
      if (Math.abs(c.x - cx - 0.5) < 1e-3 && Math.abs(c.y - cy - 0.5) < 1e-3) c.want = pickDir(c, true);
      demoStep(maze, c, 4.0 * 1.0, dt);
    }
    const events = [];
    for (const c of catchers) {
      for (const p of players) {
        if (p.alive && Math.hypot(c.x - p.x, c.y - p.y) < 0.45) {
          p.alive = false; p.dir = 'none';
          events.push({ t: 'event', kind: 'caught', player_id: p.id, catcher_id: c.id, tick });
        }
      }
    }
    // scripted elimination so the death animation is always visible
    if (elapsed === 6000) {
      const victim = players.find(p => p.alive && p !== me) || me;
      if (victim.alive) {
        victim.alive = false;
        events.push({ t: 'event', kind: 'caught', player_id: victim.id, catcher_id: 'c1', tick });
      }
    }
    const alive = players.filter(p => p.alive).length;
    emit('snapshot', {
      t: 'snapshot', tick, ts: Date.now(),
      players: players.map(p => ({ id: p.id, x: p.x, y: p.y, dir: p.dir, alive: p.alive })),
      catchers: catchers.map(c => ({ id: c.id, x: c.x, y: c.y, dir: c.dir })),
      time_left_ms: roundMs - elapsed, alive_count: alive,
    });
    for (const e of events) emit('event', e);
    if (alive <= 1 || elapsed >= roundMs) {
      ended = true;
      clearInterval(timer);
      const order = [...players].sort((a, b) => (b.alive - a.alive) || (b.survived - a.survived));
      emit('match_end', {
        t: 'match_end', match_id: 'demo',
        winner_ids: players.filter(p => p.alive).map(p => p.id),
        standings: order.map((p, i) => ({ player_id: p.id, name: p.name, survived_ms: p.survived, place: i + 1 })),
      });
    }
  }, TICK_MS);
}

if (typeof location !== 'undefined' && location.hash === '#demo' && typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', runDemo);
  else runDemo();
}

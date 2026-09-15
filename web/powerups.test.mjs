import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const webDir = dirname(fileURLToPath(import.meta.url));
const renderSrc = readFileSync(join(webDir, 'render.js'), 'utf8');
const renderURL = 'data:text/javascript;base64,' + Buffer.from(renderSrc).toString('base64');
const render = await import(renderURL);

const gameSrc = readFileSync(join(webDir, 'game.js'), 'utf8')
  .replace(`from './render.js'`, `from '${renderURL}'`);
const game = await import('data:text/javascript;base64,' + Buffer.from(gameSrc).toString('base64'));

function fakeCtx(canvas) {
  const ops = [];
  const ctx = {
    canvas,
    ops,
    fillStyle: '#000', strokeStyle: '#000', lineWidth: 1, globalAlpha: 1,
    font: '', textAlign: '', textBaseline: '', lineCap: '',
    alphaLog: [],
    save() { ops.push(['save']); this.alphaLog.push(this.globalAlpha); },
    restore() { ops.push(['restore']); this.globalAlpha = this.alphaLog.pop() ?? 1; },
    translate(x, y) { ops.push(['translate', x, y]); },
    scale() {},
    beginPath() { ops.push(['beginPath']); },
    moveTo() {}, lineTo() {}, closePath() {}, arc() { ops.push(['arc']); },
    arcTo() {}, roundRect() {}, rect() {},
    fill() { ops.push(['fill', this.fillStyle, this.globalAlpha]); },
    stroke() { ops.push(['stroke', this.strokeStyle, this.globalAlpha]); },
    fillRect() { ops.push(['fillRect', this.fillStyle]); },
    strokeRect() {}, fillText() { ops.push(['fillText', this.fillStyle, this.globalAlpha]); },
    measureText(t) { return { width: String(t).length * 7 }; },
    setLineDash(d) { ops.push(['setLineDash', d]); },
  };
  return ctx;
}

function fakeCanvas(w = 600, h = 600) {
  const c = { width: w, height: h, hidden: false };
  c.getContext = () => c._ctx || (c._ctx = fakeCtx(c));
  return c;
}

const maze = { w: 3, h: 3, walls: [15, 11, 15, 14, 0, 7, 15, 13, 15] };

test('drawPowerUp draws a gold bolt for speed and a violet ring for invisibility', () => {
  const canvas = fakeCanvas();
  const ctx = canvas.getContext();
  const lay = render.layout(canvas, maze);
  render.drawPowerUp(ctx, lay, { id: 'power1', kind: 'speed', x: 1.5, y: 0.5 }, 100);
  assert.ok(ctx.ops.some(o => o[0] === 'translate' && o[1] === lay.ox + 1.5 * lay.cell && o[2] === lay.oy + 0.5 * lay.cell),
    'speed pickup must draw at its own position');
  const fills = ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(fills.includes('#ffd54f'), `expected gold bolt fill, got ${JSON.stringify(fills)}`);
  const strokes = ctx.ops.filter(o => o[0] === 'stroke').map(o => o[1]);
  assert.ok(strokes.includes('#ffd54f'), 'expected gold glow stroke');
  assert.ok(strokes.includes('#fff'), 'expected white contrast stroke');

  ctx.ops.length = 0;
  render.drawPowerUp(ctx, lay, { id: 'power2', kind: 'invisibility', x: 0.5, y: 2.5 }, 100);
  assert.ok(ctx.ops.some(o => o[0] === 'translate' && o[1] === lay.ox + 0.5 * lay.cell && o[2] === lay.oy + 2.5 * lay.cell),
    'invisibility pickup must draw at its own position');
  const vStrokes = ctx.ops.filter(o => o[0] === 'stroke').map(o => o[1]);
  assert.ok(vStrokes.includes('#ce93d8'), `expected violet ring, got ${JSON.stringify(vStrokes)}`);
  const vFills = ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(vFills.includes('#fff'), 'expected white center dot');
});

test('drawFrame renders powerups from the scene and tolerates their absence', () => {
  const canvas = fakeCanvas();
  const ctx = canvas.getContext();
  const scene = {
    maze,
    runners: [{ x: 0.5, y: 0.5, color: '#4fc3f7', name: 'Ann', alive: true, isYou: true, deathProgress: null }],
    catchers: [{ x: 2.5, y: 2.5, dir: 'up' }],
    powerUps: [{ id: 'power1', kind: 'speed', x: 1.5, y: 0.5 }],
    now: 50,
  };
  render.drawFrame(ctx, canvas, scene);
  const fills = ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(fills.includes('#ffd54f'), 'pickup bolt should be drawn at its scene position');

  ctx.ops.length = 0;
  render.drawFrame(ctx, canvas, { ...scene, powerUps: [] });
  const fills2 = ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(!fills2.includes('#ffd54f'), 'collected pickup must disappear');

  ctx.ops.length = 0;
  const { powerUps, now, ...legacy } = scene;
  assert.doesNotThrow(() => render.drawFrame(ctx, canvas, legacy));
});

test('invisible runner is ghosted at 0.35 alpha and state is restored for the next entity', () => {
  const canvas = fakeCanvas();
  const ctx = canvas.getContext();
  const lay = render.layout(canvas, maze);
  render.drawRunner(ctx, lay, { x: 1.5, y: 1.5, color: '#4fc3f7', name: 'Ann', alive: true, isYou: true, deathProgress: null, invisible: true, speedBoost: true });
  const ghostFill = ctx.ops.find(o => o[0] === 'fill' && o[1] === '#4fc3f7');
  assert.ok(ghostFill, 'runner fill missing');
  assert.equal(ghostFill[2], 0.35, `invisible runner alpha ${ghostFill[2]} want 0.35`);
  const strokes = ctx.ops.filter(o => o[0] === 'stroke');
  assert.ok(strokes.some(o => o[1] === '#ce93d8'), 'expected violet dashed ring');
  assert.ok(strokes.some(o => o[1] === '#ffd54f'), 'expected gold speed ring');
  assert.ok(ctx.ops.some(o => o[0] === 'setLineDash' && Array.isArray(o[1]) && o[1].length === 2), 'dashed ring expected');
  assert.equal(ctx.globalAlpha, 1, 'alpha must be restored after the runner');

  ctx.ops.length = 0;
  render.drawCatcher(ctx, lay, { x: 2.5, y: 2.5, dir: 'up' });
  const catcherFill = ctx.ops.find(o => o[0] === 'fill' && o[1] === '#e53935');
  assert.equal(catcherFill[2], 1, 'no alpha leak onto the catcher');
});

function fakeEl(tag) {
  const el = {
    tagName: tag, id: '', className: '', textContent: '', hidden: false,
    children: [], style: {}, title: '',
    classList: {
      _set: new Set(),
      add(c) { this._set.add(c); },
      toggle(c, on) { on ? this._set.add(c) : this._set.delete(c); },
      contains(c) { return this._set.has(c); },
    },
    append(...kids) { this.children.push(...kids); },
    appendChild(k) { this.children.push(k); return k; },
    setAttribute() {}, addEventListener() {}, focus() {},
  };
  Object.defineProperty(el, 'innerHTML', {
    get() { return ''; },
    set() { el.children.length = 0; },
  });
  return el;
}

function installDOM() {
  const ids = {};
  const canvas = fakeCanvas();
  ids.game = canvas;
  ids.lobby = fakeEl('div');
  ids.overlay = fakeEl('div');
  const document_ = {
    body: fakeEl('body'),
    readyState: 'complete',
    getElementById(id) { return ids[id] || null; },
    createElement(tag) { return fakeEl(tag); },
    createTextNode(t) { return { text: t }; },
    addEventListener() {},
  };
  const rafQueue = [];
  const listeners = {};
  globalThis.document = document_;
  globalThis.window = { addEventListener(t, h) { (listeners[t] ||= []).push(h); } };
  globalThis.requestAnimationFrame = cb => { rafQueue.push(cb); return rafQueue.length; };
  globalThis.cancelAnimationFrame = () => {};
  globalThis.performance = globalThis.performance || { now: () => Date.now() };
  const pump = now => {
    const q = rafQueue.splice(0);
    for (const cb of q) cb(now);
  };
  return { ids, canvas, document_, pump };
}

function fakeNet() {
  const handlers = new Map();
  const sent = [];
  return {
    playerId: 'p1',
    sent,
    send(o) { sent.push(o); },
    input(d) { sent.push({ t: 'input', dir: d }); },
    on(type, h) {
      if (!handlers.has(type)) handlers.set(type, new Set());
      handlers.get(type).add(h);
      return () => handlers.get(type).delete(h);
    },
    emit(type, msg) { for (const h of handlers.get(type) || []) h(msg); },
  };
}

const matchStart = {
  t: 'match_start', match_id: 'm1',
  maze,
  you: { player_id: 'p1', role: 'runner', color: '#4fc3f7' },
  players: [
    { player_id: 'p1', name: 'Ann', role: 'runner', color: '#4fc3f7', x: 0.5, y: 0.5 },
    { player_id: 'p2', name: 'Bob', role: 'runner', color: '#81c784', x: 2.5, y: 2.5 },
  ],
  catchers: [{ id: 'c1', x: 1.5, y: 1.5 }],
  power_ups: [{ id: 'power1', kind: 'speed', x: 1.5, y: 0.5 }],
  round_ms: 30000,
};

test('startGame shows snapshot-driven power badges and renders live pickups', () => {
  const { ids, canvas, document_, pump } = installDOM();
  const net = fakeNet();
  game.startGame(net, matchStart);

  const hud = document_.body.children.find(c => c.id === 'hud');
  assert.ok(hud, 'hud missing');
  const powers = hud.children.find(c => c.className === 'hud-powers');
  assert.ok(powers, 'hud-powers row missing');
  const [speedBadge, invisBadge, hint] = powers.children;
  assert.equal(speedBadge.hidden, true, 'speed badge hidden before any effect');
  assert.equal(invisBadge.hidden, true);
  assert.equal(hint.hidden, false, 'hint visible while no effect is active');

  pump(16);
  let fills = canvas._ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(fills.includes('#ffd54f'), 'initial pickup should render');

  net.emit('snapshot', {
    t: 'snapshot', tick: 1, ts: 1000,
    players: [
      { id: 'p1', x: 0.6, y: 0.5, dir: 'right', alive: true, speed_boost_ms: 5000 },
      { id: 'p2', x: 2.5, y: 2.5, dir: 'none', alive: true },
    ],
    catchers: [{ id: 'c1', x: 1.5, y: 1.5, dir: 'none' }],
    power_ups: [], time_left_ms: 29950, alive_count: 2,
  });
  assert.equal(speedBadge.hidden, false);
  assert.match(speedBadge.textContent, /Speed ×1.5 · 5\.0s/);
  assert.equal(hint.hidden, true);
  canvas._ctx.ops.length = 0;
  pump(32);
  fills = canvas._ctx.ops.filter(o => o[0] === 'fill').map(o => o[1]);
  assert.ok(!fills.includes('#ffd54f'), 'collected pickup must vanish from the scene');
  const runnerFill = canvas._ctx.ops.find(o => o[0] === 'fill' && o[1] === '#4fc3f7');
  assert.equal(runnerFill[2], 1, 'speeded runner is not ghosted');
  const strokes = canvas._ctx.ops.filter(o => o[0] === 'stroke').map(o => o[1]);
  assert.ok(strokes.includes('#ffd54f'), 'gold speed ring on the runner');

  net.emit('snapshot', {
    t: 'snapshot', tick: 2, ts: 1050,
    players: [
      { id: 'p1', x: 0.8, y: 0.5, dir: 'right', alive: true, speed_boost_ms: 4950, invisible_ms: 3000 },
      { id: 'p2', x: 2.5, y: 2.5, dir: 'none', alive: true },
    ],
    catchers: [{ id: 'c1', x: 1.5, y: 1.5, dir: 'none' }],
    power_ups: [], time_left_ms: 29900, alive_count: 2,
  });
  assert.equal(invisBadge.hidden, false);
  assert.match(invisBadge.textContent, /Invisible · 3\.0s/);
  canvas._ctx.ops.length = 0;
  pump(48);
  const ghostFill = canvas._ctx.ops.find(o => o[0] === 'fill' && o[1] === '#4fc3f7');
  assert.equal(ghostFill[2], 0.35, 'invisible runner rendered as ghost');

  net.emit('snapshot', {
    t: 'snapshot', tick: 3, ts: 1100,
    players: [
      { id: 'p1', x: 1.0, y: 0.5, dir: 'right', alive: true },
      { id: 'p2', x: 2.5, y: 2.5, dir: 'none', alive: true },
    ],
    catchers: [{ id: 'c1', x: 1.5, y: 1.5, dir: 'none' }],
    power_ups: [], time_left_ms: 29850, alive_count: 2,
  });
  assert.equal(speedBadge.hidden, true, 'speed badge hides when the snapshot drops the field');
  assert.equal(invisBadge.hidden, true, 'invisibility badge hides when the snapshot drops the field');
  assert.equal(hint.hidden, false, 'hint returns once no effect is active');

  assert.ok(net.sent.every(o => o.t !== 'input' || typeof o.dir === 'string'), `unexpected frames ${JSON.stringify(net.sent)}`);
  assert.ok(!net.sent.some(o => /power|pickup/i.test(JSON.stringify(o))), 'client must not send pickup commands');

  ids.overlay.hidden = true;
});

test('startGame without power fields is safe', () => {
  const { canvas, pump } = installDOM();
  const net = fakeNet();
  const legacy = { ...matchStart };
  delete legacy.power_ups;
  assert.doesNotThrow(() => game.startGame(net, legacy));
  net.emit('snapshot', {
    t: 'snapshot', tick: 1, ts: 1,
    players: [{ id: 'p1', x: 0.5, y: 0.5, dir: 'none', alive: true }],
    catchers: [], time_left_ms: 29950, alive_count: 1,
  });
  assert.doesNotThrow(() => pump(16));
  assert.ok(canvas._ctx.ops.length > 0);
});

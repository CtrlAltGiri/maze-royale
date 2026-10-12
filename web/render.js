// owned by client-render: canvas drawing of maze, runners, catchers, name tags, death animation

const WALL_N = 1, WALL_E = 2, WALL_S = 4, WALL_W = 8;
export const DEATH_MS = 600;

const COLORS = {
  bg: '#111',
  floor: '#1c1f26',
  wall: '#c8ccd6',
  catcher: '#e53935',
  catcherEye: '#fff',
  tag: '#eee',
  tagShadow: 'rgba(0,0,0,0.85)',
  dead: 'rgba(200,200,200,0.35)',
  speed: '#ffd54f',
  invisibility: '#ce93d8',
  gun: '#ff7043',
  bullet: '#ffee58',
  catcherDown: 'rgba(229,57,53,0.4)',
};

// Compute cell size and offset so the whole maze fits centered on the canvas.
export function layout(canvas, maze) {
  const w = maze.w, h = maze.h;
  const cell = Math.floor(Math.min(canvas.width / w, canvas.height / h));
  const ox = Math.floor((canvas.width - cell * w) / 2);
  const oy = Math.floor((canvas.height - cell * h) / 2);
  return { cell, ox, oy, w, h };
}

export function drawMaze(ctx, maze, lay) {
  const { cell, ox, oy, w, h } = lay;
  ctx.fillStyle = COLORS.floor;
  ctx.fillRect(ox, oy, cell * w, cell * h);
  ctx.strokeStyle = COLORS.wall;
  ctx.lineWidth = Math.max(2, cell * 0.12);
  ctx.lineCap = 'square';
  ctx.beginPath();
  const walls = maze.walls;
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const b = walls[y * w + x] | 0;
      const x0 = ox + x * cell, y0 = oy + y * cell;
      const x1 = x0 + cell, y1 = y0 + cell;
      if (b & WALL_N) { ctx.moveTo(x0, y0); ctx.lineTo(x1, y0); }
      if (b & WALL_E) { ctx.moveTo(x1, y0); ctx.lineTo(x1, y1); }
      if (b & WALL_S) { ctx.moveTo(x0, y1); ctx.lineTo(x1, y1); }
      if (b & WALL_W) { ctx.moveTo(x0, y0); ctx.lineTo(x0, y1); }
    }
  }
  ctx.stroke();
}

function toPx(lay, x, y) {
  return [lay.ox + x * lay.cell, lay.oy + y * lay.cell];
}

// Name tag: a rounded pill in the runner's color with dark or light text
// chosen for contrast, clamped inside the canvas.
function drawTag(ctx, px, py, r, text, color, dim) {
  const size = Math.max(11, Math.round(r * 1.05));
  ctx.save();
  ctx.font = `bold ${size}px system-ui, sans-serif`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'middle';
  const padX = 5, padY = 2;
  const tw = ctx.measureText(text).width;
  const bw = tw + padX * 2, bh = size + padY * 2;
  let cy = py - r - 4 - bh / 2;
  if (cy - bh / 2 < 0) cy = py + r + 4 + bh / 2;
  cy = Math.min(Math.max(cy, bh / 2), ctx.canvas.height - bh / 2);
  const cx = Math.min(Math.max(px, bw / 2), ctx.canvas.width - bw / 2);
  if (dim) ctx.globalAlpha = 0.6;
  ctx.fillStyle = color || '#555';
  roundRect(ctx, cx - bw / 2, cy - bh / 2, bw, bh, bh / 2);
  ctx.fill();
  ctx.strokeStyle = COLORS.tagShadow;
  ctx.lineWidth = 1;
  ctx.stroke();
  ctx.fillStyle = isLight(color) ? '#111' : '#fff';
  ctx.fillText(text, cx, cy + 0.5);
  ctx.restore();
}

function roundRect(ctx, x, y, w, h, rad) {
  ctx.beginPath();
  ctx.moveTo(x + rad, y);
  ctx.arcTo(x + w, y, x + w, y + h, rad);
  ctx.arcTo(x + w, y + h, x, y + h, rad);
  ctx.arcTo(x, y + h, x, y, rad);
  ctx.arcTo(x, y, x + w, y, rad);
  ctx.closePath();
}

function isLight(color) {
  const m = /^#([0-9a-f]{6})$/i.exec(color || '');
  if (!m) return false;
  const n = parseInt(m[1], 16);
  const r = (n >> 16) & 255, g = (n >> 8) & 255, b = n & 255;
  return (0.299 * r + 0.587 * g + 0.114 * b) > 150;
}

export function drawPowerUp(ctx, lay, power, now = 0) {
  const [px, py] = toPx(lay, power.x, power.y);
  const r = lay.cell * 0.23;
  const pulse = 1 + 0.1 * Math.sin(now / 220);
  const col = power.kind === 'invisibility' ? COLORS.invisibility
    : power.kind === 'gun' ? COLORS.gun
    : COLORS.speed;
  ctx.save();
  ctx.translate(px, py);
  ctx.scale(pulse, pulse);
  ctx.strokeStyle = col;
  ctx.fillStyle = col;
  ctx.lineWidth = Math.max(1.5, r * 0.18);
  ctx.globalAlpha = 0.85;
  ctx.beginPath();
  ctx.moveTo(0, -r * 1.6);
  ctx.lineTo(r * 1.6, 0);
  ctx.lineTo(0, r * 1.6);
  ctx.lineTo(-r * 1.6, 0);
  ctx.closePath();
  ctx.stroke();
  ctx.globalAlpha = 1;
  if (power.kind === 'invisibility') {
    ctx.beginPath();
    ctx.arc(0, 0, r * 0.7, 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = '#fff';
    ctx.beginPath();
    ctx.arc(0, 0, r * 0.16, 0, Math.PI * 2);
    ctx.fill();
  } else if (power.kind === 'gun') {
    // simple gun silhouette: barrel + grip
    ctx.fillRect(-r * 0.9, -r * 0.45, r * 1.8, r * 0.5);
    ctx.fillRect(-r * 0.35, -r * 0.05, r * 0.5, r * 0.9);
    ctx.strokeStyle = '#fff';
    ctx.lineWidth = Math.max(1, r * 0.1);
    ctx.strokeRect(-r * 0.9, -r * 0.45, r * 1.8, r * 0.5);
  } else {
    const bolt = [[.2, -.9], [-.55, .1], [-.05, .1], [-.2, .9], [.55, -.15], [.05, -.15]];
    ctx.beginPath();
    ctx.moveTo(bolt[0][0] * r, bolt[0][1] * r);
    for (let i = 1; i < bolt.length; i++) ctx.lineTo(bolt[i][0] * r, bolt[i][1] * r);
    ctx.closePath();
    ctx.fill();
    ctx.strokeStyle = '#fff';
    ctx.lineWidth = Math.max(1, r * 0.1);
    ctx.stroke();
  }
  ctx.restore();
}

// runner: {x, y, color, name, alive, isYou, deathProgress (0..1 or null)}
export function drawRunner(ctx, lay, runner) {
  const [px, py] = toPx(lay, runner.x, runner.y);
  const r = lay.cell * 0.32;
  if (runner.deathProgress != null) {
    const p = Math.min(1, Math.max(0, runner.deathProgress));
    ctx.save();
    ctx.globalAlpha = 1 - p;
    ctx.strokeStyle = runner.color;
    ctx.lineWidth = Math.max(2, r * 0.3 * (1 - p));
    ctx.beginPath();
    ctx.arc(px, py, r * (1 + p * 1.8), 0, Math.PI * 2);
    ctx.stroke();
    ctx.fillStyle = runner.color;
    ctx.beginPath();
    ctx.arc(px, py, r * (1 - p), 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
    drawTag(ctx, px, py, r, runner.name, runner.color, false);
    return;
  }
  if (!runner.alive) {
    ctx.save();
    ctx.strokeStyle = COLORS.dead;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.moveTo(px - r * 0.6, py - r * 0.6); ctx.lineTo(px + r * 0.6, py + r * 0.6);
    ctx.moveTo(px + r * 0.6, py - r * 0.6); ctx.lineTo(px - r * 0.6, py + r * 0.6);
    ctx.stroke();
    ctx.restore();
    if (runner.isYou) drawTag(ctx, px, py, r, runner.name, runner.color, true);
    return;
  }
  ctx.save();
  if (runner.invisible) ctx.globalAlpha = 0.35;
  ctx.fillStyle = runner.color;
  ctx.beginPath();
  ctx.arc(px, py, r, 0, Math.PI * 2);
  ctx.fill();
  if (runner.isYou) {
    ctx.strokeStyle = '#fff';
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.arc(px, py, r + 3, 0, Math.PI * 2);
    ctx.stroke();
  }
  if (runner.invisible) {
    ctx.strokeStyle = COLORS.invisibility;
    ctx.lineWidth = 2;
    ctx.setLineDash([3, 3]);
    ctx.beginPath();
    ctx.arc(px, py, r + 6, 0, Math.PI * 2);
    ctx.stroke();
    ctx.setLineDash([]);
  }
  if (runner.speedBoost) {
    ctx.strokeStyle = COLORS.speed;
    ctx.lineWidth = 2;
    ctx.beginPath();
    ctx.arc(px, py, r + 9, 0, Math.PI * 2);
    ctx.stroke();
  }
  drawTag(ctx, px, py, r, runner.isYou ? `${runner.name} (you)` : runner.name, runner.color, false);
  ctx.restore();
}

// catcher: {x, y, dir, down}
export function drawCatcher(ctx, lay, c) {
  const [px, py] = toPx(lay, c.x, c.y);
  const r = lay.cell * 0.36;
  ctx.fillStyle = c.down ? COLORS.catcherDown : COLORS.catcher;
  ctx.beginPath();
  ctx.arc(px, py, r, 0, Math.PI * 2);
  ctx.fill();
  const er = r * 0.18, off = r * 0.35;
  if (c.down) {
    // X eyes while downed
    ctx.strokeStyle = COLORS.catcherEye;
    ctx.lineWidth = Math.max(1.5, r * 0.12);
    const xr = er * 1.1;
    ctx.beginPath();
    for (const s of [-1, 1]) {
      const ex = px + s * off;
      ctx.moveTo(ex - xr, py - xr); ctx.lineTo(ex + xr, py + xr);
      ctx.moveTo(ex + xr, py - xr); ctx.lineTo(ex - xr, py + xr);
    }
    ctx.stroke();
    return;
  }
  // eyes pointing in the travel direction
  let dx = 0, dy = 0;
  switch (c.dir) {
    case 'up': dy = -1; break;
    case 'down': dy = 1; break;
    case 'left': dx = -1; break;
    case 'right': dx = 1; break;
  }
  const ex = px + dx * off * 0.6, ey = py + dy * off * 0.6;
  ctx.fillStyle = COLORS.catcherEye;
  ctx.beginPath();
  ctx.arc(ex - dy * off, ey + dx * off, er, 0, Math.PI * 2);
  ctx.arc(ex + dy * off, ey - dx * off, er, 0, Math.PI * 2);
  ctx.fill();
}

// bullet: {x, y, dir}
export function drawBullet(ctx, lay, b) {
  const [px, py] = toPx(lay, b.x, b.y);
  const r = lay.cell * 0.12;
  let dx = 0, dy = 0;
  switch (b.dir) {
    case 'up': dy = -1; break;
    case 'down': dy = 1; break;
    case 'left': dx = -1; break;
    case 'right': dx = 1; break;
  }
  ctx.save();
  ctx.strokeStyle = COLORS.bullet;
  ctx.globalAlpha = 0.55;
  ctx.lineWidth = r * 1.2;
  ctx.lineCap = 'round';
  ctx.beginPath();
  ctx.moveTo(px - dx * r * 4, py - dy * r * 4);
  ctx.lineTo(px, py);
  ctx.stroke();
  ctx.globalAlpha = 1;
  ctx.fillStyle = COLORS.bullet;
  ctx.beginPath();
  ctx.arc(px, py, r, 0, Math.PI * 2);
  ctx.fill();
  ctx.restore();
}

// scene: {maze, runners: [...], catchers: [...]}
export function drawFrame(ctx, canvas, scene) {
  ctx.fillStyle = COLORS.bg;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  const lay = layout(canvas, scene.maze);
  drawMaze(ctx, scene.maze, lay);
  for (const p of scene.powerUps || []) drawPowerUp(ctx, lay, p, scene.now || 0);
  for (const r of scene.runners) if (!r.alive && r.deathProgress == null) drawRunner(ctx, lay, r);
  for (const c of scene.catchers) drawCatcher(ctx, lay, c);
  for (const b of scene.bullets || []) drawBullet(ctx, lay, b);
  for (const r of scene.runners) if (r.alive || r.deathProgress != null) drawRunner(ctx, lay, r);
  return lay;
}

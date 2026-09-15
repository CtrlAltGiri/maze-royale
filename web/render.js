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

function drawTag(ctx, px, py, r, text) {
  ctx.font = `bold ${Math.max(10, Math.round(r * 1.1))}px system-ui, sans-serif`;
  ctx.textAlign = 'center';
  ctx.textBaseline = 'bottom';
  ctx.lineWidth = 3;
  ctx.strokeStyle = COLORS.tagShadow;
  ctx.strokeText(text, px, py - r - 3);
  ctx.fillStyle = COLORS.tag;
  ctx.fillText(text, px, py - r - 3);
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
    drawTag(ctx, px, py, r, runner.name);
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
    return;
  }
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
  drawTag(ctx, px, py, r, runner.name);
}

// catcher: {x, y, dir}
export function drawCatcher(ctx, lay, c) {
  const [px, py] = toPx(lay, c.x, c.y);
  const r = lay.cell * 0.36;
  ctx.fillStyle = COLORS.catcher;
  ctx.beginPath();
  ctx.arc(px, py, r, 0, Math.PI * 2);
  ctx.fill();
  // eyes pointing in the travel direction
  let dx = 0, dy = 0;
  switch (c.dir) {
    case 'up': dy = -1; break;
    case 'down': dy = 1; break;
    case 'left': dx = -1; break;
    case 'right': dx = 1; break;
  }
  const er = r * 0.18, off = r * 0.35;
  const ex = px + dx * off * 0.6, ey = py + dy * off * 0.6;
  ctx.fillStyle = COLORS.catcherEye;
  ctx.beginPath();
  ctx.arc(ex - dy * off, ey + dx * off, er, 0, Math.PI * 2);
  ctx.arc(ex + dy * off, ey - dx * off, er, 0, Math.PI * 2);
  ctx.fill();
}

// scene: {maze, runners: [...], catchers: [...]}
export function drawFrame(ctx, canvas, scene) {
  ctx.fillStyle = COLORS.bg;
  ctx.fillRect(0, 0, canvas.width, canvas.height);
  const lay = layout(canvas, scene.maze);
  drawMaze(ctx, scene.maze, lay);
  for (const r of scene.runners) if (!r.alive && r.deathProgress == null) drawRunner(ctx, lay, r);
  for (const c of scene.catchers) drawCatcher(ctx, lay, c);
  for (const r of scene.runners) if (r.alive || r.deathProgress != null) drawRunner(ctx, lay, r);
  return lay;
}

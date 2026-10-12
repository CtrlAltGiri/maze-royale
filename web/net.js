// owned by client-net: websocket client (connect, reconnect, ping, send/receive contract messages)
// Wire contract: docs/CONTRACT.md / internal/protocol. Every frame is a JSON object with a `t` field.

const PING_INTERVAL_MS = 5000;
const BACKOFF_BASE_MS = 500;
const BACKOFF_MAX_MS = 10000;

export class Net {
  constructor(url) {
    this.url = url || defaultUrl();
    this.ws = null;
    this.playerId = null;
    this.latencyMs = null;
    this.connected = false;
    this.closed = false;
    this.attempt = 0;
    this.name = null;
    this.roomCode = null;
    this.inputSeq = 0;
    this._handlers = new Map();
    this._pingTimer = null;
    this._reconnectTimer = null;
    this._queue = [];
  }

  connect() {
    this.closed = false;
    this._open();
    return this;
  }

  close() {
    this.closed = true;
    clearTimeout(this._reconnectTimer);
    this._stopPing();
    if (this.ws) this.ws.close();
  }

  // on(type, handler) -> unsubscribe. Special types: "open", "close", "*" (every frame).
  on(type, handler) {
    if (!this._handlers.has(type)) this._handlers.set(type, new Set());
    const set = this._handlers.get(type);
    set.add(handler);
    return () => set.delete(handler);
  }

  send(obj) {
    if (!obj || typeof obj.t !== "string") throw new Error("net: frame needs a t field");
    if (obj.t === "hello") this.name = obj.name;
    if (obj.t === "join_room") this.roomCode = obj.code;
    if (obj.t === "leave_room") this.roomCode = null;
    const data = JSON.stringify(obj);
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(data);
    } else if (obj.t !== "ping" && obj.t !== "input") {
      this._queue.push(data);
    }
  }

  // Convenience helpers mirroring the client -> gateway table.
  hello(name) { this.send({ t: "hello", name }); }
  createRoom(config) { this.send({ t: "create_room", config }); }
  joinRoom(code) { this.send({ t: "join_room", code }); }
  leaveRoom() { this.send({ t: "leave_room" }); }
  setReady(ready) { this.send({ t: "set_ready", ready: !!ready }); }
  startRoom() { this.send({ t: "start_room" }); }
  input(dir) { this.send({ t: "input", seq: ++this.inputSeq, dir }); }
  shoot() { this.send({ t: "shoot" }); }

  _open() {
    clearTimeout(this._reconnectTimer);
    let ws;
    try {
      ws = new WebSocket(this.url);
    } catch (err) {
      this._scheduleReconnect();
      return;
    }
    this.ws = ws;
    ws.onopen = () => {
      if (ws !== this.ws) return;
      const reconnecting = this.attempt > 0;
      this.attempt = 0;
      this.connected = true;
      if (reconnecting) {
        if (this.name) ws.send(JSON.stringify({ t: "hello", name: this.name }));
        if (this.roomCode) ws.send(JSON.stringify({ t: "join_room", code: this.roomCode }));
      }
      for (const data of this._queue) ws.send(data);
      this._queue = [];
      this._startPing();
      this._emit("open", { t: "open", reconnecting });
    };
    ws.onmessage = (ev) => {
      if (ws !== this.ws) return;
      let msg;
      try {
        msg = JSON.parse(ev.data);
      } catch (err) {
        return;
      }
      if (!msg || typeof msg.t !== "string") return;
      if (msg.t === "welcome") this.playerId = msg.player_id;
      if (msg.t === "pong" && typeof msg.ts === "number") this.latencyMs = Date.now() - msg.ts;
      if (msg.t === "room_state" && msg.code) this.roomCode = msg.code;
      this._emit(msg.t, msg);
      this._emit("*", msg);
    };
    ws.onclose = () => {
      if (ws !== this.ws) return;
      const wasConnected = this.connected;
      this.connected = false;
      this._stopPing();
      this._emit("close", { t: "close", wasConnected });
      if (!this.closed) this._scheduleReconnect();
    };
    ws.onerror = () => {
      // onclose always follows; nothing to do here.
    };
  }

  _scheduleReconnect() {
    clearTimeout(this._reconnectTimer);
    const delay = Math.min(BACKOFF_MAX_MS, BACKOFF_BASE_MS * 2 ** this.attempt) * (0.75 + Math.random() * 0.5);
    this.attempt++;
    this._reconnectTimer = setTimeout(() => this._open(), delay);
  }

  _startPing() {
    this._stopPing();
    const ping = () => this.send({ t: "ping", ts: Date.now() });
    ping();
    this._pingTimer = setInterval(ping, PING_INTERVAL_MS);
  }

  _stopPing() {
    clearInterval(this._pingTimer);
    this._pingTimer = null;
  }

  _emit(type, msg) {
    const set = this._handlers.get(type);
    if (!set) return;
    for (const h of Array.from(set)) {
      try {
        h(msg);
      } catch (err) {
        console.error("net: handler for", type, "threw", err);
      }
    }
  }
}

export function defaultUrl() {
  const proto = location.protocol === "https:" ? "wss:" : "ws:";
  return `${proto}//${location.host}/ws`;
}

export function createNet(url) {
  return new Net(url).connect();
}

// ws.js — WebSocket client com reconexão exponencial
// Usado pelo preload.js para expor bridge.ws

class DenundikeWS {
  constructor() {
    this._ws = null;
    this._url = null;
    this._attempt = 0;
    this._reconnectTimer = null;
    this._msgCbs = [];
    this._reconCbs = [];
    this._closed = false;
  }

  // serverBase: 'http://...' → converted to 'ws://...'
  connect(serverBase, token) {
    this._url =
      serverBase.replace(/^http/, 'ws') +
      '/ws?token=' +
      encodeURIComponent(token);
    this._closed = false;
    this._attempt = 0;
    this._open();
  }

  _open() {
    if (this._ws) {
      try { this._ws.close(); } catch (_) {}
    }
    const ws = new WebSocket(this._url);
    this._ws = ws;

    ws.onopen = () => {
      this._attempt = 0;
    };

    ws.onmessage = (ev) => {
      let obj;
      try { obj = JSON.parse(ev.data); } catch (_) { return; }
      this._msgCbs.forEach(cb => { try { cb(obj); } catch (_) {} });
    };

    ws.onclose = () => {
      if (this._closed) return;
      // backoff: 1s → 2s → 4s → … → máx 30s
      const delay = Math.min(1000 * Math.pow(2, this._attempt), 30000);
      this._attempt++;
      this._reconCbs.forEach(cb => { try { cb(this._attempt); } catch (_) {} });
      this._reconnectTimer = setTimeout(() => this._open(), delay);
    };

    ws.onerror = () => {
      // close fires after error; handled there
    };
  }

  send(obj) {
    if (this._ws && this._ws.readyState === 1 /* OPEN */) {
      this._ws.send(JSON.stringify(obj));
    }
  }

  onMessage(cb) {
    this._msgCbs.push(cb);
  }

  onReconnect(cb) {
    this._reconCbs.push(cb);
  }

  disconnect() {
    this._closed = true;
    clearTimeout(this._reconnectTimer);
    if (this._ws) {
      try { this._ws.close(); } catch (_) {}
      this._ws = null;
    }
  }
}

module.exports = DenundikeWS;

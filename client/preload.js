const { contextBridge, ipcRenderer } = require('electron');
const DenundikeWS = require('./src/js/ws.js');

const BASE_URL = process.env.DENUNDIKE_SERVER || 'https://denundike.estudiofachada.com.br';
const wsClient = new DenundikeWS();

contextBridge.exposeInMainWorld('bridge', {
  api: async function (method, path, body) {
    const opts = {
      method,
      headers: { 'Content-Type': 'application/json' },
    };
    if (body && method !== 'GET') {
      opts.body = JSON.stringify(body);
    }
    const url = BASE_URL + path;
    const res = await fetch(url, opts);
    if (!res.ok) {
      const text = await res.text();
      throw new Error(text.trim() || res.statusText);
    }
    if (res.status === 204) return null;
    return res.json();
  },

  openMain: function () {
    ipcRenderer.send('open-main');
  },

  serverUrl: BASE_URL,

  ws: {
    connect: (token) => wsClient.connect(BASE_URL, token),
    send: (obj) => wsClient.send(obj),
    onMessage: (cb) => wsClient.onMessage(cb),
    onReconnect: (cb) => wsClient.onReconnect(cb),
    disconnect: () => wsClient.disconnect(),
  },

  onUpdateAvailable: (cb) => ipcRenderer.on('update-available', () => cb()),
  onUpdateDownloaded: (cb) => ipcRenderer.on('update-downloaded', () => cb()),
  onUpdateProgress: (cb) => ipcRenderer.on('update-progress', (_e, pct) => cb(pct)),
  onUpdateInstalling: (cb) => ipcRenderer.on('update-installing', () => cb()),
  installUpdate: () => ipcRenderer.send('install-update'),
});

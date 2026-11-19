const { contextBridge, ipcRenderer } = require('electron');
const DenundikeWS = require('./src/js/ws.js');

const BASE_URL = process.env.DENUNDIKE_SERVER || 'https://denundike.estudiofachada.com.br';
const wsClient = new DenundikeWS();

contextBridge.exposeInMainWorld('bridge', {
  api: async function (method, path, body, token) {
    const opts = {
      method,
      headers: { 'Content-Type': 'application/json' },
    };
    if (token) opts.headers.Authorization = 'Bearer ' + token;
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

  win: {
    minimize: () => ipcRenderer.send('win-minimize'),
    toggleMaximize: () => ipcRenderer.send('win-toggle-maximize'),
    close: () => ipcRenderer.send('win-close'),
    state: () => ipcRenderer.invoke('win-state'),
    onState: (cb) => ipcRenderer.on('win-state', (_e, st) => cb(st)),
  },

  openExternal: (url) => ipcRenderer.invoke('open-external', url),

  // Bandeja do sistema e avisos nativos (o processo principal decide se mostra).
  app: {
    callState: (s) => ipcRenderer.send('app-call-state', s),
    notify: (n) => ipcRenderer.send('app-notify', n),
    onTrayAction: (cb) => ipcRenderer.on('tray-action', (_e, a) => cb(a)),
  },

  screen: {
    sources: () => ipcRenderer.invoke('screen-sources'),
    choose: (id, audio) => ipcRenderer.invoke('screen-choose', { id, audio }),
  },

  browser: {
    available: () => ipcRenderer.invoke('browser-available'),
    info: () => ipcRenderer.invoke('browser-info'),
    start: (url, opts) => ipcRenderer.invoke('browser-start', url, opts),
    resize: (h) => ipcRenderer.invoke('browser-resize', h),
    stop: () => ipcRenderer.invoke('browser-stop'),
    nav: (n) => ipcRenderer.invoke('browser-nav', n),
    input: (from, ev) => ipcRenderer.invoke('browser-input', { from, ev }),
    overlay: (on) => ipcRenderer.invoke('browser-overlay', on),
    park: (on) => ipcRenderer.invoke('browser-park', on),
    onUrl: (cb) => ipcRenderer.on('browser-url', (_e, u) => cb(u)),
    onEnded: (cb) => ipcRenderer.on('browser-ended', () => cb()),
    onSize: (cb) => ipcRenderer.on('browser-size', () => cb()),
    onMotion: (cb) => ipcRenderer.on('browser-motion', (_e, m) => cb(!!m)),
    download: () => ipcRenderer.invoke('browser-download-start'),
    onDownloadProgress: (cb) => ipcRenderer.on('browser-download-progress', (_e, pct) => cb(pct)),
    onPrep: (cb) => ipcRenderer.on('browser-prep', (_e, p) => cb(p)),
  },

  ws: {
    connect: (token) => wsClient.connect(BASE_URL, token),
    send: (obj) => wsClient.send(obj),
    onMessage: (cb) => wsClient.onMessage(cb),
    onReconnect: (cb) => wsClient.onReconnect(cb),
    onOpen: (cb) => wsClient.onOpen(cb),
    disconnect: () => wsClient.disconnect(),
  },

  onUpdateAvailable: (cb) => ipcRenderer.on('update-available', () => cb()),
  onUpdateDownloaded: (cb) => ipcRenderer.on('update-downloaded', () => cb()),
  onUpdateProgress: (cb) => ipcRenderer.on('update-progress', (_e, pct) => cb(pct)),
  onUpdateInstalling: (cb) => ipcRenderer.on('update-installing', () => cb()),
  onUpdateStatus: (cb) => ipcRenderer.on('update-status', (_e, s) => cb(s)),
  installUpdate: () => ipcRenderer.send('install-update'),
  deferUpdate: () => ipcRenderer.send('update-defer'),
});

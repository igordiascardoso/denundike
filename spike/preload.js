const { contextBridge, ipcRenderer } = require('electron');
contextBridge.exposeInMainWorld('bridge', {
  signal: (m) => ipcRenderer.send('signal', m),
  onSignal: (cb) => ipcRenderer.on('signal', (_e, m) => cb(m)),
  navigate: (u) => ipcRenderer.send('navigate', u),
  onStatus: (cb) => ipcRenderer.on('status', (_e, s) => cb(s)),
});

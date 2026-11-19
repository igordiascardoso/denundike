const { contextBridge, ipcRenderer } = require('electron');

const BASE_URL = process.env.DENUNDIKE_SERVER || 'http://localhost:8080';

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
});

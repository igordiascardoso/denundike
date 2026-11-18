// SPIKE DESCARTAVEL do Denundike. Nao e o app.
// Janela "anfitriao": barra de topo + aba do site (WebContentsView).
// Janela "convidado": recebe o stream por WebRTC (sinalizacao pelo ipc, na mesma maquina).
const { app, BrowserWindow, WebContentsView, ipcMain, session, desktopCapturer } = require('electron');
const path = require('path');

let hostWin, viewerWin, siteView;
const TOP = 56; // altura da barra do anfitriao

async function enableAdblock() {
  try {
    const { ElectronBlocker } = require('@cliqz/adblocker-electron');
    const blocker = await ElectronBlocker.fromPrebuiltAdsAndTracking(fetch);
    blocker.enableBlockingInSession(session.defaultSession);
    return 'adblock cliqz ligado';
  } catch (e) {
    return 'adblock cliqz nao carregou: ' + e.message;
  }
}

async function loadAdguardIfAny() {
  const p = process.env.ADGUARD_PATH;
  if (!p) return 'sem ADGUARD_PATH (extensao nao carregada)';
  try {
    const ext = await session.defaultSession.loadExtension(p, { allowFileAccess: true });
    return 'extensao carregada: ' + ext.name + ' ' + ext.version;
  } catch (e) {
    return 'extensao falhou: ' + e.message;
  }
}

function layoutSite() {
  const [w, h] = hostWin.getContentSize();
  siteView.setBounds({ x: 0, y: TOP, width: w, height: h - TOP });
}

async function createWindows() {
  const status = [];
  if (process.env.ADBLOCK === '1') status.push(await enableAdblock());
  status.push(await loadAdguardIfAny());

  hostWin = new BrowserWindow({
    width: 1280, height: 800, x: 20, y: 20, title: 'ANFITRIAO',
    webPreferences: { preload: path.join(__dirname, 'preload.js') },
  });
  hostWin.loadFile('host.html');

  siteView = new WebContentsView();
  hostWin.contentView.addChildView(siteView);
  siteView.webContents.on('did-fail-load', (_e, code, desc, url) => console.log('FALHOU', code, desc, url));
  siteView.webContents.on('did-finish-load', () => console.log('CARREGOU', siteView.webContents.getURL()));
  siteView.webContents.loadURL('https://www.youtube.com');
  hostWin.on('resize', layoutSite);
  layoutSite();

  // Quando o anfitriao pede getDisplayMedia, entrega SO o frame da aba + audio de loopback.
  hostWin.webContents.session.setDisplayMediaRequestHandler((request, callback) => {
    callback({
      video: siteView.webContents.mainFrame,
      audio: 'loopback', // audio de TODO o sistema (o que queremos medir)
    });
  });

  viewerWin = new BrowserWindow({
    width: 900, height: 620, x: 1320, y: 20, title: 'CONVIDADO',
    webPreferences: { preload: path.join(__dirname, 'preload.js') },
  });
  viewerWin.loadFile('viewer.html');

  hostWin.webContents.once('did-finish-load', () => hostWin.webContents.send('status', status));
}

// Sinalizacao: repassa mensagens entre as duas janelas.
ipcMain.on('signal', (e, msg) => {
  const target = e.sender === hostWin.webContents ? viewerWin : hostWin;
  target.webContents.send('signal', msg);
});
ipcMain.on('navigate', (e, url) => {
  if (!/^https?:\/\//.test(url)) url = 'https://' + url;
  siteView.webContents.loadURL(url);
});

// Controle remoto: recebe eventos do convidado e aplica no navegador via CDP
ipcMain.on('remote-control', async (_e, ev) => {
  const wc = siteView.webContents;
  if (!wc.debugger.isAttached()) {
    try { wc.debugger.attach('1.3'); } catch (_) { return; }
  }
  const { width, height } = siteView.getBounds();
  const cx = Math.round((ev.x || 0) * width);
  const cy = Math.round((ev.y || 0) * height);

  if (ev.type === 'click') {
    await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', x: cx, y: cy, clickCount: 1 });
    await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', x: cx, y: cy, clickCount: 1 });
  } else if (ev.type === 'move') {
    await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseMoved', x: cx, y: cy });
  } else if (ev.type === 'key') {
    const mods = ((ev.modifiers?.shift ? 8 : 0) | (ev.modifiers?.ctrl ? 2 : 0) | (ev.modifiers?.alt ? 1 : 0));
    await wc.debugger.sendCommand('Input.dispatchKeyEvent', { type: 'keyDown', key: ev.key, code: ev.code, modifiers: mods });
    await wc.debugger.sendCommand('Input.dispatchKeyEvent', { type: 'keyUp', key: ev.key, code: ev.code, modifiers: mods });
  }
});

app.whenReady().then(createWindows);
app.on('window-all-closed', () => app.quit());

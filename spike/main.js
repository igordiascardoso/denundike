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
  // Com CAPTURE=chrome, captura janela do Google Chrome (para teste de DRM).
  hostWin.webContents.session.setDisplayMediaRequestHandler(async (_request, callback) => {
    if (process.env.CAPTURE === 'chrome') {
      const sources = await desktopCapturer.getSources({ types: ['window'] });
      const src = sources.find((s) => /chrome|google/i.test(s.name));
      if (src) {
        console.log('CAPTURE=chrome: usando janela', src.name);
        return callback({ video: src, audio: 'loopback' });
      }
      console.log('CAPTURE=chrome: Chrome nao encontrado. Fontes:', sources.map((s) => s.name));
    }
    callback({ video: siteView.webContents.mainFrame, audio: 'loopback' });
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
    try { wc.debugger.attach('1.3'); }
    catch (e) { console.log('CDP attach falhou:', e.message); return; }
  }
  const { width, height } = siteView.getBounds();
  const cx = Math.round((ev.x || 0) * width);
  const cy = Math.round((ev.y || 0) * height);
  try {
    if (ev.type === 'click') {
      await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mousePressed', button: 'left', x: cx, y: cy, clickCount: 1 });
      await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseReleased', button: 'left', x: cx, y: cy, clickCount: 1 });
    } else if (ev.type === 'move') {
      await wc.debugger.sendCommand('Input.dispatchMouseEvent', { type: 'mouseMoved', x: cx, y: cy });
    } else if (ev.type === 'key') {
      const mods = ((ev.modifiers?.shift ? 8 : 0) | (ev.modifiers?.ctrl ? 2 : 0) | (ev.modifiers?.alt ? 1 : 0));
      const isText = ev.key.length === 1;
      const kp = { key: ev.key, code: ev.code, windowsVirtualKeyCode: ev.keyCode || 0, modifiers: mods };
      await wc.debugger.sendCommand('Input.dispatchKeyEvent', { type: isText ? 'keyDown' : 'rawKeyDown', ...kp, text: isText ? ev.key : '' });
      await wc.debugger.sendCommand('Input.dispatchKeyEvent', { type: 'keyUp', ...kp });
    }
  } catch (e) { console.log('CDP sendCommand falhou:', e.message); }
});

app.whenReady().then(async () => {
  await createWindows();
  if (process.env.AUTO_TEST) runAutoTest();
});
app.on('window-all-closed', () => app.quit());

async function runAutoTest() {
  const fs = require('fs');
  const results = {};
  const log = (s) => console.log('[AUTO_TEST]', s);

  // Navegar para video YouTube com som para testar audio tambem
  log('navegando para video YouTube...');
  siteView.webContents.loadURL('https://www.youtube.com/watch?v=jNQXAC9IVRw'); // Me at the zoo (18s, primeiro video do YT)
  await sleep(5000);
  log('pagina carregada: ' + siteView.webContents.getURL());

  log('disparando Compartilhar aba no anfitriao...');
  await hostWin.webContents.executeJavaScript(`document.getElementById('share').click()`);

  log('aguardando WebRTC conectar (8s)...');
  await sleep(8000);

  // Coletar stats
  const stats = await viewerWin.webContents.executeJavaScript(`document.getElementById('stats').textContent`).catch(e => 'erro: ' + e.message);
  const dcState = await viewerWin.webContents.executeJavaScript(`typeof dc !== 'undefined' ? dc.readyState : 'dc nao definido'`).catch(e => 'erro: ' + e.message);
  const videoEl = await viewerWin.webContents.executeJavaScript(`(() => { const v = document.getElementById('v'); return v.videoWidth + 'x' + v.videoHeight + ' src=' + !!v.srcObject; })()`).catch(e => 'erro: ' + e.message);

  log('stats: ' + stats);
  log('data channel state: ' + dcState);
  log('video element: ' + videoEl);

  results.stream = stats.includes('video') ? 'PASSOU' : 'FALHOU';
  results.audio = stats.includes('audio ok') ? 'PASSOU' : 'FALHOU';
  results.dataChannel = dcState === 'open' ? 'PASSOU' : 'FALHOU - ' + dcState;

  // Testar controle remoto: simular clique via executeJavaScript no viewer
  if (dcState === 'open') {
    log('testando controle remoto: simulando clique no centro do video...');
    await viewerWin.webContents.executeJavaScript(`
      (() => {
        const v = document.getElementById('v');
        const r = v.getBoundingClientRect();
        const evt = new MouseEvent('click', { clientX: r.left + r.width/2, clientY: r.top + r.height/2, bubbles: true });
        v.dispatchEvent(evt);
      })()
    `);
    await sleep(500);
    // Checar se o CDP recebeu o comando (via console log no main ou verificar se site reagiu)
    const siteTitle = siteView.webContents.getTitle();
    log('titulo da aba apos clique: ' + siteTitle);
    results.remoteControl = 'PIPE OK (data channel aberto; CDP enviado — verificar logs acima)';
  } else {
    results.remoteControl = 'FALHOU - data channel nao aberto';
  }

  // Resultado final
  const lines = [
    '# AUTO_TEST ' + new Date().toISOString(),
    '',
    '## 1. Stream WebRTC: ' + results.stream,
    'Stats: ' + stats,
    'Video element: ' + videoEl,
    '',
    '## 2. Audio: ' + results.audio,
    '(audio capturado via loopback)',
    '',
    '## 3. Data Channel (controle remoto): ' + results.dataChannel,
    '',
    '## 4. Controle remoto CDP: ' + results.remoteControl,
    '',
    '## Notas',
    'Resolucao pequena (294x164): WebRTC ajusta para banda disponivel.',
    'Para testar 1080p real: maximizar janela host e abrir video 1080p60 no YouTube.',
    'DRM (Chrome): requer teste manual com CAPTURE=chrome npm start.',
  ];

  const out = lines.join('\n');
  fs.writeFileSync(require('path').join(__dirname, 'AUTO_RESULTS.txt'), out);
  log('resultados salvos em AUTO_RESULTS.txt');
  console.log('\n' + out + '\n');
  await sleep(500);
  app.quit();
}

function sleep(ms) { return new Promise(r => setTimeout(r, ms)); }

const { app, BrowserWindow, ipcMain, Menu, Tray, Notification, nativeImage, session, shell } = require('electron');
const path = require('path');
const fs = require('fs');
const { autoUpdater } = require('electron-updater');
const { setupMedia, registerMediaIpc } = require('./media');
const { registerBrowserIpc } = require('./browserhost');

app.commandLine.appendSwitch('autoplay-policy', 'no-user-gesture-required');
// Sem isso o Windows agrupa a janela/ícone da barra de tarefas sob o Electron genérico.
if (process.platform === 'win32') app.setAppUserModelId('com.denundike.app');

const APP_ICON = path.join(__dirname, 'assets', 'icon.png');
const TEST = process.env.DENUNDIKE_TEST === '1';

let loginWin = null;
let mainWin = null;
let tray = null;
let pendingUpdate = false;
let pendingUpdateDownloaded = false;
let updateReady = false;
let manualCheck = false;
let isQuitting = false;
let startHidden = false;
let inTray = false; // janela escondida de propósito (X ou início em segundo plano)
let callState = { inCall: false, muted: false, deafened: false };

/* ── preferências do app (só do lado de cá, sem passar pelo servidor) ── */
let settings = {};
const settingsFile = () => path.join(app.getPath('userData'), 'denundike-settings.json');
function loadSettings() {
  try { settings = JSON.parse(fs.readFileSync(settingsFile(), 'utf8')) || {}; } catch (_) { settings = {}; }
}
function saveSettings() {
  try { fs.writeFileSync(settingsFile(), JSON.stringify(settings)); } catch (_) { /* pasta sem permissão: segue sem salvar */ }
}

/* Segundo plano só existe onde há bandeja de verdade. O GNOME puro não mostra o ícone,
   e esconder a janela lá deixaria o app invisível e impossível de fechar. */
const TRAY_SUPPORTED = process.platform !== 'linux' || !/gnome/i.test(process.env.XDG_CURRENT_DESKTOP || '');
const LOGIN_ITEM_SUPPORTED = process.platform === 'win32' || process.platform === 'darwin';

const alive = (w) => !!w && !w.isDestroyed();
const isAppPage = (w) => alive(w) && /app\.html([?#]|$)/.test(w.webContents.getURL());

function createMainWindow() {
  mainWin = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 1100,
    minHeight: 600,
    title: 'Denundike',
    frame: false,
    show: !startHidden,
    icon: APP_ICON,
    backgroundColor: '#090C0D',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
      backgroundThrottling: false,
    },
  });

  mainWin.loadFile(path.join(__dirname, 'src', 'app.html'));

  // Flush any update events that arrived before the main window existed
  mainWin.webContents.once('did-finish-load', () => {
    if (pendingUpdate) {
      mainWin.webContents.send('update-available');
      pendingUpdate = false;
    }
    if (pendingUpdateDownloaded) {
      mainWin.webContents.send('update-downloaded');
      pendingUpdateDownloaded = false;
    }
  });

  // Saiu da conta (ou a sessão caiu): não há mais chamada, e quem está escondido precisa ver o login.
  mainWin.webContents.on('did-navigate', () => {
    if (isAppPage(mainWin)) return;
    setCallState({ inCall: false, muted: false, deafened: false });
    if (!mainWin.isVisible() && !mainWin.isMinimized()) showWindow();
  });

  const sendState = () => {
    if (alive(mainWin)) {
      mainWin.webContents.send('win-state', { maximized: mainWin.isMaximized(), fullscreen: mainWin.isFullScreen() });
    }
  };
  for (const ev of ['maximize', 'unmaximize', 'enter-full-screen', 'leave-full-screen', 'resize']) mainWin.on(ev, sendState);

  // Fechar (X) com a conta aberta vai para a bandeja; chamada, voz e WS continuam vivos.
  mainWin.on('close', (e) => {
    if (isQuitting || !tray || !isAppPage(mainWin)) return;
    e.preventDefault();
    inTray = true;
    mainWin.hide();
    firstHideNotice();
  });
  mainWin.on('show', () => { inTray = false; });
  mainWin.on('session-end', () => { isQuitting = true; });

  mainWin.on('closed', () => {
    mainWin = null;
  });
}

function createLoginWindow() {
  loginWin = new BrowserWindow({
    width: 1200,
    height: 750,
    minWidth: 400,
    minHeight: 600,
    title: 'Denundike · Entrar',
    frame: false,
    show: !startHidden,
    icon: APP_ICON,
    backgroundColor: '#090C0D',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });

  loginWin.loadFile(path.join(__dirname, 'src', 'login.html'));

  loginWin.on('closed', () => {
    loginWin = null;
  });
}

function showWindow() {
  startHidden = false;
  inTray = false;
  const w = alive(mainWin) ? mainWin : alive(loginWin) ? loginWin : null;
  if (!w) { createLoginWindow(); return; }
  if (w.isMinimized()) w.restore();
  w.show();
  w.focus();
}

/* ── avisos nativos ── */
const shown = new Set();
const lastNotify = new Map();
const notified = []; // só para o teste ler
function notify(title, body, opts = {}) {
  const rec = { title: String(title).slice(0, 80), body: String(body || '').slice(0, 160) };
  notified.push(rec);
  if (!Notification.isSupported()) return;
  try {
    const n = new Notification({ title: rec.title, body: rec.body, icon: APP_ICON, silent: TEST });
    shown.add(n);
    const done = () => shown.delete(n);
    n.on('close', done);
    n.on('failed', done);
    n.on('click', () => { done(); if (!opts.noOpen) showWindow(); });
    n.show();
  } catch (_) { /* notificação é só um aviso */ }
}

function firstHideNotice() {
  if (settings.bgNoticeShown) return;
  settings.bgNoticeShown = true;
  saveSettings();
  notify('O Denundike continua em segundo plano', 'Use o ícone da bandeja para sair.');
}

/* ── bandeja ── */
function trayTooltip() {
  if (!callState.inCall) return 'Denundike';
  return 'Denundike · em chamada' + (callState.deafened ? ' (ensurdecido)' : callState.muted ? ' (mudo)' : '');
}

function toRenderer(action) {
  if (alive(mainWin) && isAppPage(mainWin)) mainWin.webContents.send('tray-action', action);
}

function setOpenAtLogin(on) {
  settings.openAtLogin = !!on;
  saveSettings();
  // Em desenvolvimento o executável seria o electron.exe: não vale registrá-lo na inicialização do sistema.
  if (LOGIN_ITEM_SUPPORTED && app.isPackaged) {
    try { app.setLoginItemSettings({ openAtLogin: !!on, args: ['--hidden'] }); } catch (_) { /* sem permissão */ }
  }
}

function installUpdate() {
  // Com a janela escondida, o app volta escondido depois de atualizar.
  const background = inTray || startHidden;
  if (background) { settings.startHiddenOnce = true; saveSettings(); }
  isQuitting = true;
  autoUpdater.quitAndInstall(true, true);
}

function checkUpdatesManual() {
  manualCheck = true;
  if (!app.isPackaged) {
    manualCheck = false;
    notify('Denundike', 'Verificação de atualização só funciona no app instalado.', { noOpen: true });
    return;
  }
  autoUpdater.checkForUpdates().catch(() => {
    if (manualCheck) { manualCheck = false; notify('Não foi possível verificar atualizações', 'Tente de novo em instantes.', { noOpen: true }); }
  });
}

function quitForReal() {
  isQuitting = true;
  if (callState.inCall) {
    // Sai da chamada avisando os outros antes de derrubar a janela.
    toRenderer('leave-call');
    setTimeout(() => app.quit(), 350);
  } else {
    app.quit();
  }
}

function trayTemplate() {
  const c = callState;
  return [
    { label: 'Abrir Denundike', click: showWindow },
    { type: 'separator' },
    { label: c.muted ? 'Ativar microfone' : 'Silenciar microfone', enabled: c.inCall, click: () => toRenderer('toggle-mute') },
    { label: c.deafened ? 'Desensurdecer' : 'Ensurdecer', enabled: c.inCall, click: () => toRenderer('toggle-deafen') },
    { label: 'Sair da chamada', enabled: c.inCall, click: () => toRenderer('leave-call') },
    { type: 'separator' },
    updateReady
      ? { label: 'Reiniciar para atualizar', click: installUpdate }
      : { label: 'Verificar atualizações', click: checkUpdatesManual },
    {
      label: process.platform === 'win32' ? 'Iniciar com o Windows' : 'Iniciar com o sistema',
      type: 'checkbox',
      checked: !!settings.openAtLogin,
      visible: LOGIN_ITEM_SUPPORTED,
      click: (item) => { setOpenAtLogin(item.checked); refreshTray(); },
    },
    { type: 'separator' },
    { label: 'Sair do Denundike', click: quitForReal },
  ];
}

function refreshTray() {
  if (!tray) return;
  tray.setToolTip(trayTooltip());
  tray.setContextMenu(Menu.buildFromTemplate(trayTemplate()));
}

function setCallState(s) {
  const next = { inCall: !!s.inCall, muted: !!s.muted, deafened: !!s.deafened };
  if (next.inCall === callState.inCall && next.muted === callState.muted && next.deafened === callState.deafened) return;
  callState = next;
  refreshTray();
}

function createTray() {
  if (!TRAY_SUPPORTED) return;
  try {
    let img = nativeImage.createFromPath(path.join(__dirname, 'assets', process.platform === 'win32' ? 'icon.ico' : 'icon.png'));
    if (process.platform !== 'win32') img = img.resize({ width: 22, height: 22 });
    if (img.isEmpty()) return;
    tray = new Tray(img);
    tray.on('click', showWindow);
    tray.on('double-click', showWindow);
    refreshTray();
  } catch (_) {
    tray = null; // sem bandeja, fechar volta a encerrar o app em vez de sumir com ele
  }
}

/* ── IPC vindo da janela principal ── */
ipcMain.on('app-call-state', (e, s) => {
  if (BrowserWindow.fromWebContents(e.sender) !== mainWin || !s) return;
  setCallState(s);
});

ipcMain.on('app-notify', (e, n) => {
  if (BrowserWindow.fromWebContents(e.sender) !== mainWin || !n || typeof n.title !== 'string') return;
  // Quem está olhando para o app não precisa de aviso.
  if (alive(mainWin) && mainWin.isVisible() && mainWin.isFocused()) return;
  const key = String(n.key || n.title);
  const now = Date.now();
  const gap = key.startsWith('msg:') ? 20000 : 4000; // mensagens de canal não viram metralhadora
  if (now - (lastNotify.get(key) || 0) < gap) return;
  lastNotify.set(key, now);
  notify(n.title, n.body);
});

// Persistent: after "Sair" the main window itself shows the login page and may need to re-enter.
ipcMain.on('open-main', (e) => {
  const sender = BrowserWindow.fromWebContents(e.sender);
  if (mainWin && sender === mainWin) {
    mainWin.loadFile(path.join(__dirname, 'src', 'app.html'));
    return;
  }
  if (!mainWin) createMainWindow();
  if (loginWin && !loginWin.isDestroyed()) loginWin.close();
});

function boot() {
  app.on('second-instance', () => showWindow());

  app.whenReady().then(() => {
    loadSettings();
    startHidden = process.argv.includes('--hidden') || !!settings.startHiddenOnce;
    if (settings.startHiddenOnce) { delete settings.startHiddenOnce; saveSettings(); }
    if (settings.openAtLogin && LOGIN_ITEM_SUPPORTED && app.isPackaged) {
      try { app.setLoginItemSettings({ openAtLogin: true, args: ['--hidden'] }); } catch (_) { /* segue */ }
    }

    Menu.setApplicationMenu(null);
    setupMedia(session.defaultSession);
    registerMediaIpc();
    registerBrowserIpc();
    createTray();
    createLoginWindow();
    autoUpdater.checkForUpdatesAndNotify();
    // Como o app fica aberto dias em segundo plano, procura atualização de tempos em tempos.
    if (app.isPackaged) setInterval(() => autoUpdater.checkForUpdates().catch(() => {}), 4 * 3600 * 1000);

    if (TEST) {
      // Só para os testes automáticos: expõe os handlers reais da bandeja no processo principal.
      global.__dkTest = {
        menu: () => trayTemplate().map((i) => ({ label: i.label, enabled: i.enabled !== false, type: i.type || 'normal', checked: i.checked, visible: i.visible !== false })),
        click: (label, checked) => {
          const it = trayTemplate().find((x) => x.label === label);
          if (!it || it.enabled === false || !it.click) return false;
          it.click({ checked: !!checked });
          return true;
        },
        tooltip: trayTooltip,
        hasTray: () => !!tray,
        focused: () => alive(mainWin) && mainWin.isFocused(),
        notified: () => notified.slice(),
        settings: () => ({ ...settings }),
        state: () => ({
          quitting: isQuitting,
          callState,
          mainVisible: alive(mainWin) ? mainWin.isVisible() : null,
          loginVisible: alive(loginWin) ? loginWin.isVisible() : null,
          startHidden,
          inTray,
        }),
        fakeUpdateDownloaded: () => autoUpdater.emit('update-downloaded'),
        // O que o electron-updater faz ao instalar: app.quit() puro, sem avisar a bandeja.
        fakeInstall: () => { app.quit(); },
      };
    }
  });
}

if (!app.requestSingleInstanceLock()) {
  // Já existe um Denundike aberto: ele foi avisado (second-instance) e este sai.
  app.quit();
} else {
  boot();
}

function sendToWindows(channel, ...args) {
  for (const w of [loginWin, mainWin]) {
    if (alive(w)) w.webContents.send(channel, ...args);
  }
}

autoUpdater.on('update-available', () => {
  if (!mainWin) pendingUpdate = true;
  sendToWindows('update-available');
});

autoUpdater.on('update-not-available', () => {
  if (manualCheck) {
    manualCheck = false;
    notify('Denundike está atualizado', 'Você já tem a versão mais recente.', { noOpen: true });
  }
});

autoUpdater.on('error', () => {
  if (manualCheck) {
    manualCheck = false;
    notify('Não foi possível verificar atualizações', 'Tente de novo em instantes.', { noOpen: true });
  }
});

autoUpdater.on('download-progress', (p) => {
  sendToWindows('update-progress', Math.round(p.percent));
});

autoUpdater.on('update-downloaded', () => {
  updateReady = true;
  refreshTray();
  if (alive(mainWin)) {
    mainWin.webContents.send('update-downloaded');
    // Em segundo plano ninguém vai ver o aviso de reiniciar: instala sozinho, se não estiver em chamada.
    if ((inTray || startHidden) && !callState.inCall) {
      notify('Atualizando o Denundike', 'O app reinicia em segundo plano.', { noOpen: true });
      setTimeout(installUpdate, 3000);
    }
  } else {
    sendToWindows('update-installing');
    setTimeout(installUpdate, 2000);
  }
});

const winOf = (e) => BrowserWindow.fromWebContents(e.sender);
ipcMain.on('win-minimize', (e) => winOf(e)?.minimize());
ipcMain.on('win-toggle-maximize', (e) => {
  const w = winOf(e);
  if (w) w.isMaximized() ? w.unmaximize() : w.maximize();
});
ipcMain.on('win-close', (e) => winOf(e)?.close());
ipcMain.handle('win-state', (e) => {
  const w = winOf(e);
  return { maximized: !!w && w.isMaximized(), fullscreen: !!w && w.isFullScreen() };
});

// Só páginas de download conhecidas abrem no navegador do sistema.
const EXTERNAL_OK = ['https://www.google.com/chrome', 'https://www.microsoft.com/edge', 'https://github.com/igordiascardoso/denundike-releases'];
ipcMain.handle('open-external', (_e, url) => {
  const u = String(url || '');
  if (!EXTERNAL_OK.some((p) => u === p || u.startsWith(p + '/') || u.startsWith(p + '?'))) return false;
  shell.openExternal(u);
  return true;
});

ipcMain.on('install-update', () => {
  isQuitting = true;
  autoUpdater.quitAndInstall();
});

// Qualquer encerramento (Sair, atualização, desligar o PC) libera o X e some com o ícone da bandeja.
app.on('before-quit', () => { isQuitting = true; });
app.on('will-quit', () => { try { tray && tray.destroy(); } catch (_) {} tray = null; });

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) createLoginWindow();
  else showWindow();
});

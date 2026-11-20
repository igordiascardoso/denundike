const { app, BrowserWindow, ipcMain, Menu, Tray, Notification, nativeImage, session, shell } = require('electron');
const path = require('path');
const fs = require('fs');
const { autoUpdater } = require('electron-updater');
const { setupMedia, registerMediaIpc } = require('./media');
const { registerBrowserIpc, ensureChrome } = require('./browserhost');

app.setName('Denundike');
app.commandLine.appendSwitch('autoplay-policy', 'no-user-gesture-required');
// Sem isso o Windows agrupa a janela/ícone da barra de tarefas sob o Electron genérico.
if (process.platform === 'win32') app.setAppUserModelId('com.denundike.app');
// No Linux (Wayland/Ubuntu moderno) o compartilhamento de tela precisa passar pelo portal PipeWire.
if (process.platform === 'linux') {
  app.commandLine.appendSwitch('enable-features', 'WebRTCPipeWireCapturer');
  app.commandLine.appendSwitch('ozone-platform-hint', 'auto');
}

const APP_ICON = path.join(__dirname, 'assets', 'icon.png');
const TEST = process.env.DENUNDIKE_TEST === '1';

let loginWin = null;
let mainWin = null;
let tray = null;
let pendingUpdate = false;
let updateReady = false;
let manualCheck = false;
// Atualização automática: o main decide quando instalar; a janela só mostra o banner.
let updateVersion = '';
let updatePhase = 'idle'; // idle | checking | uptodate | downloading | ready
let updatePct = 0;
let updateTimer = null; // fim do adiamento ou espera da instalação em segundo plano
let countdownTimer = null; // contagem regressiva visível
let deferUntil = 0;
let installing = false;
let uptodateTimer = null;
const COUNTDOWN_S = 10;
const HIDDEN_DELAY_MS = 3000;
const DEFER_MS = TEST && Number(process.env.DENUNDIKE_DEFER_MS) > 0 ? Number(process.env.DENUNDIKE_DEFER_MS) : 30 * 60 * 1000;
const CHECK_EVERY_MS = 3600 * 1000;
let testStubInstall = false;
const installCalls = []; // só para o teste ler
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
    if (updateReady) scheduleInstall();
    else if (updatePhase === 'downloading') sendUpdateStatus({ state: 'downloading', pct: updatePct });
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

function clearUpdateTimers() {
  clearTimeout(updateTimer);
  clearInterval(countdownTimer);
  updateTimer = null;
  countdownTimer = null;
}

function sendUpdateStatus(s) {
  if (alive(mainWin) && isAppPage(mainWin)) mainWin.webContents.send('update-status', { version: updateVersion, ...s });
}

function installUpdate() {
  if (installing) return;
  installing = true;
  clearUpdateTimers();
  // Com a janela escondida, o app volta escondido depois de atualizar.
  const background = inTray || startHidden || (alive(mainWin) && isAppPage(mainWin) && !mainWin.isVisible());
  if (background) { settings.startHiddenOnce = true; saveSettings(); }
  isQuitting = true;
  if (testStubInstall) { installCalls.push({ at: Date.now(), background: !!background }); installing = false; isQuitting = false; return; }
  autoUpdater.quitAndInstall(true, true);
}

// Última conferência antes de reiniciar: chamada que começou no meio da contagem cancela a instalação.
function runInstall() {
  clearUpdateTimers();
  if (callState.inCall) { scheduleInstall(); return; }
  installUpdate();
}

/* Decide quando instalar a atualização já baixada, sem pedir clique:
   em chamada espera terminar; adiada espera o prazo; escondido instala direto; visível faz contagem. */
function scheduleInstall() {
  if (!updateReady || installing) return;
  clearUpdateTimers();
  if (callState.inCall) { sendUpdateStatus({ state: 'waiting-call' }); return; }
  const wait = deferUntil - Date.now();
  if (wait > 0) {
    sendUpdateStatus({ state: 'deferred' });
    updateTimer = setTimeout(scheduleInstall, wait + 50);
    return;
  }
  if (!isAppPage(mainWin)) { // tela de login ou sem janela principal: não há nada a perder
    sendToWindows('update-installing');
    updateTimer = setTimeout(runInstall, 2000);
    return;
  }
  if (!mainWin.isVisible()) { // em segundo plano: instala direto, só avisa pelo Windows
    notify(`Denundike atualizado — reiniciando para a versão ${updateVersion || 'nova'}`, 'O app volta em segundo plano em instantes.', { noOpen: true });
    updateTimer = setTimeout(runInstall, HIDDEN_DELAY_MS);
    return;
  }
  let left = COUNTDOWN_S;
  sendUpdateStatus({ state: 'countdown', seconds: left });
  countdownTimer = setInterval(() => {
    left -= 1;
    if (left <= 0) runInstall();
    else sendUpdateStatus({ state: 'countdown', seconds: left });
  }, 1000);
}

function checkForUpdate() {
  if (!app.isPackaged || updateReady || installing) return Promise.resolve();
  return autoUpdater.checkForUpdates().catch(() => { /* sem rede: a próxima verificação tenta de novo */ });
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

function updateStatusLabel() {
  if (updatePhase === 'checking') return 'Procurando atualizações…';
  if (updatePhase === 'uptodate') return 'Você está na versão mais recente';
  if (updatePhase === 'downloading') return `Baixando atualização ${updatePct}%`;
  return '';
}

function setUpdatePhase(phase) {
  updatePhase = phase;
  clearTimeout(uptodateTimer);
  if (phase === 'uptodate') uptodateTimer = setTimeout(() => { if (updatePhase === 'uptodate') setUpdatePhase('idle'); }, 6000);
  refreshTray();
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
    ...(updateStatusLabel() ? [{ label: updateStatusLabel(), enabled: false }] : []),
    updateReady
      ? { label: 'Reiniciar para atualizar', click: installUpdate }
      : { label: 'Verificar atualizações', enabled: updatePhase !== 'checking' && updatePhase !== 'downloading', click: checkUpdatesManual },
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
  const callChanged = next.inCall !== callState.inCall;
  callState = next;
  refreshTray();
  // Entrou na chamada: cancela a contagem; saiu: retoma a instalação pendente.
  if (callChanged && updateReady) scheduleInstall();
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
    ensureChrome(); // baixa o Chrome em background se ainda não estiver presente
    createTray();
    createLoginWindow();
    // Sem checkForUpdatesAndNotify: o aviso nativo dele sai em inglês. Quem avisa somos nós, em português.
    autoUpdater.autoDownload = true;
    autoUpdater.autoInstallOnAppQuit = true;
    checkForUpdate();
    // O app fica aberto dias em segundo plano: procura atualização de hora em hora.
    setInterval(checkForUpdate, CHECK_EVERY_MS);

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
        fakeUpdateDownloaded: (v) => autoUpdater.emit('update-downloaded', { version: v || '9.9.9' }),
        stubInstall: (on) => { testStubInstall = !!on; },
        resetUpdate: () => { clearUpdateTimers(); updateReady = false; installing = false; deferUntil = 0; updatePhase = 'idle'; updatePct = 0; installCalls.length = 0; refreshTray(); },
        installCalls: () => installCalls.slice(),
        setCall: (s) => setCallState(s),
        updateState: () => ({ updateReady, updatePhase, updatePct, installing, deferUntil, timers: !!updateTimer || !!countdownTimer, label: updateStatusLabel() }),
        fakeEvent: (name, arg) => autoUpdater.emit(name, arg),
        hide: () => { if (alive(mainWin)) { inTray = true; mainWin.hide(); } },
        show: () => showWindow(),
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

// Nada do que o electron-updater diz chega ao usuário: as mensagens dele são em inglês.
// Tudo que aparece (banner, bandeja, avisos do Windows) é escrito aqui, em português.
autoUpdater.on('checking-for-update', () => {
  if (updateReady) return;
  setUpdatePhase('checking');
});

autoUpdater.on('update-available', (info) => {
  updateVersion = (info && info.version) || updateVersion;
  updatePct = 0;
  if (!mainWin) pendingUpdate = true;
  sendToWindows('update-available');
  sendUpdateStatus({ state: 'downloading', pct: 0 });
  setUpdatePhase('downloading');
  if (manualCheck) {
    manualCheck = false;
    notify('Nova versão encontrada', `Baixando a versão ${updateVersion || 'nova'}…`, { noOpen: true });
  }
});

autoUpdater.on('update-not-available', () => {
  setUpdatePhase('uptodate');
  if (manualCheck) {
    manualCheck = false;
    notify('Denundike está atualizado', 'Você já tem a versão mais recente.', { noOpen: true });
  }
});

autoUpdater.on('error', () => {
  if (!updateReady) setUpdatePhase('idle');
  if (manualCheck) {
    manualCheck = false;
    notify('Não foi possível verificar atualizações', 'Tente de novo em instantes.', { noOpen: true });
  }
  // Sem aviso para o usuário em verificação automática: a próxima rodada (a cada hora) tenta de novo.
});

autoUpdater.on('download-progress', (p) => {
  const pct = Math.round(p.percent);
  sendToWindows('update-progress', pct);
  sendUpdateStatus({ state: 'downloading', pct });
  if (pct !== updatePct) { updatePct = pct; refreshTray(); }
});

autoUpdater.on('update-downloaded', (info) => {
  updateVersion = (info && info.version) || updateVersion;
  updateReady = true;
  setUpdatePhase('ready');
  scheduleInstall();
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
const EXTERNAL_OK = ['https://www.google.com/chrome', 'https://github.com/igordiascardoso/denundike-releases'];
ipcMain.handle('open-external', (_e, url) => {
  const u = String(url || '');
  if (!EXTERNAL_OK.some((p) => u === p || u.startsWith(p + '/') || u.startsWith(p + '?'))) return false;
  shell.openExternal(u);
  return true;
});

// "Reiniciar agora" do banner (e do menu da bandeja): instala já, mesmo em chamada, porque o usuário pediu.
ipcMain.on('install-update', (e) => {
  if (!updateReady || BrowserWindow.fromWebContents(e.sender) !== mainWin) return;
  installUpdate();
});

// "Depois": adia e volta a instalar sozinho quando o prazo acabar.
ipcMain.on('update-defer', (e) => {
  if (!updateReady || BrowserWindow.fromWebContents(e.sender) !== mainWin) return;
  deferUntil = Date.now() + DEFER_MS;
  scheduleInstall();
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

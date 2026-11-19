const { app, BrowserWindow, ipcMain, Menu } = require('electron');
const path = require('path');
const { autoUpdater } = require('electron-updater');

let loginWin = null;
let mainWin = null;
let pendingUpdate = false;
let pendingUpdateDownloaded = false;

function createMainWindow() {
  mainWin = new BrowserWindow({
    width: 1280,
    height: 800,
    minWidth: 1100,
    minHeight: 600,
    title: 'Denundike',
    frame: false,
    backgroundColor: '#090C0D',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
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

  // Register once per login window so handlers don't accumulate on macOS reactivation
  ipcMain.once('open-main', () => {
    createMainWindow();
    if (loginWin) {
      loginWin.close();
    }
  });
}

app.whenReady().then(() => {
  Menu.setApplicationMenu(null);
  createLoginWindow();
  autoUpdater.checkForUpdatesAndNotify();
});

function sendToWindows(channel, ...args) {
  for (const w of [loginWin, mainWin]) {
    if (w && !w.isDestroyed()) w.webContents.send(channel, ...args);
  }
}

autoUpdater.on('update-available', () => {
  if (!mainWin) pendingUpdate = true;
  sendToWindows('update-available');
});

autoUpdater.on('download-progress', (p) => {
  sendToWindows('update-progress', Math.round(p.percent));
});

autoUpdater.on('update-downloaded', () => {
  if (mainWin) {
    mainWin.webContents.send('update-downloaded');
  } else {
    sendToWindows('update-installing');
    setTimeout(() => autoUpdater.quitAndInstall(true, true), 2000);
  }
});

const winOf = (e) => BrowserWindow.fromWebContents(e.sender);
ipcMain.on('win-minimize', (e) => winOf(e)?.minimize());
ipcMain.on('win-toggle-maximize', (e) => {
  const w = winOf(e);
  if (w) w.isMaximized() ? w.unmaximize() : w.maximize();
});
ipcMain.on('win-close', (e) => winOf(e)?.close());

ipcMain.on('install-update', () => {
  autoUpdater.quitAndInstall();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) createLoginWindow();
});

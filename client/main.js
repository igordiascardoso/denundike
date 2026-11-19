const { app, BrowserWindow, ipcMain } = require('electron');
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
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
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
  createLoginWindow();
  autoUpdater.checkForUpdatesAndNotify();
});

autoUpdater.on('update-available', () => {
  if (mainWin) mainWin.webContents.send('update-available');
  else pendingUpdate = true;
});

autoUpdater.on('update-downloaded', () => {
  if (mainWin) mainWin.webContents.send('update-downloaded');
  else autoUpdater.quitAndInstall(true, true);
});

ipcMain.on('install-update', () => {
  autoUpdater.quitAndInstall();
});

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) createLoginWindow();
});

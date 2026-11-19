const { app, BrowserWindow, ipcMain } = require('electron');
const path = require('path');

let loginWin = null;
let mainWin = null;

function createMainWindow() {
  mainWin = new BrowserWindow({
    width: 1200,
    height: 750,
    minWidth: 400,
    minHeight: 600,
    title: 'Denundike',
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  mainWin.loadFile(path.join(__dirname, 'app.html'));

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

app.whenReady().then(createLoginWindow);

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit();
});

app.on('activate', () => {
  if (BrowserWindow.getAllWindows().length === 0) createLoginWindow();
});

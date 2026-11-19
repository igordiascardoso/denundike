const { ipcMain, desktopCapturer } = require('electron');

let chosenSource = null;

function setupMedia(ses) {
  const allowed = new Set(['media', 'display-capture', 'fullscreen', 'clipboard-sanitized-write']);
  ses.setPermissionRequestHandler((_wc, permission, cb) => cb(allowed.has(permission)));
  ses.setPermissionCheckHandler((_wc, permission) => allowed.has(permission));
  ses.setDisplayMediaRequestHandler((_request, callback) => {
    const pick = chosenSource;
    chosenSource = null;
    if (!pick) return callback({});
    desktopCapturer.getSources({ types: ['screen', 'window'] }).then((sources) => {
      const src = sources.find((x) => x.id === pick.id);
      if (!src) return callback({});
      const result = { video: src };
      if (pick.audio && process.platform === 'win32') result.audio = 'loopback';
      callback(result);
    }).catch(() => callback({}));
  });
}

function registerMediaIpc() {
  ipcMain.handle('screen-sources', async () => {
    const sources = await desktopCapturer.getSources({
      types: ['screen', 'window'],
      thumbnailSize: { width: 320, height: 200 },
      fetchWindowIcons: false,
    });
    return sources.map((x) => ({
      id: x.id,
      name: x.name,
      isScreen: x.id.startsWith('screen:'),
      thumb: x.thumbnail.isEmpty() ? '' : x.thumbnail.toDataURL(),
    }));
  });
  ipcMain.handle('screen-choose', (_e, pick) => {
    chosenSource = { id: String(pick.id), audio: !!pick.audio };
    return true;
  });
}

module.exports = { setupMedia, registerMediaIpc };

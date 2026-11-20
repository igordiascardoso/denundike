const { ipcMain, desktopCapturer } = require('electron');

let chosenSource = null;

// No Wayland cada getSources abre o seletor do sistema e devolve só a fonte escolhida:
// listar e depois capturar abriria o seletor duas vezes e os IDs nunca bateriam.
const WAYLAND = process.platform === 'linux' && (process.env.XDG_SESSION_TYPE === 'wayland' || !!process.env.WAYLAND_DISPLAY);

function setupMedia(ses) {
  const allowed = new Set(['media', 'display-capture', 'fullscreen', 'clipboard-sanitized-write']);
  ses.setPermissionRequestHandler((_wc, permission, cb) => cb(allowed.has(permission)));
  ses.setPermissionCheckHandler((_wc, permission) => allowed.has(permission));
  ses.setDisplayMediaRequestHandler((_request, callback) => {
    const pick = chosenSource;
    chosenSource = null;
    if (!pick) return callback({});
    desktopCapturer.getSources({ types: ['screen', 'window'], thumbnailSize: { width: 0, height: 0 }, fetchWindowIcons: false }).then((sources) => {
      let src = sources.find((x) => x.id === pick.id);
      if (!src && process.platform === 'linux' && sources.length) src = sources[0];
      if (!src) return callback({});
      const result = { video: src };
      if (pick.audio && process.platform === 'win32') result.audio = 'loopback';
      callback(result);
    }).catch(() => callback({}));
  });
}

function registerMediaIpc() {
  ipcMain.handle('screen-sources', async () => {
    if (WAYLAND) return [{ id: 'portal', name: 'Escolher a tela ou janela no seletor do sistema', isScreen: true, thumb: '' }];
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

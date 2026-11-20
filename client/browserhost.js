const { app, ipcMain, desktopCapturer } = require('electron');
const { spawn, execFile } = require('child_process');
const crypto = require('crypto');
const fs = require('fs');
const path = require('path');
const https = require('https');
const http = require('http');
const os = require('os');

/* Navegador compartilhado — lado do anfitrião.
   Lança um Chrome real (com Widevine) num perfil próprio e o controla pelo
   protocolo DevTools. Convidados nunca falam com o Chrome: só eventos de
   mouse/teclado/navegação já filtrados passam por aqui. */

// O app sempre usa o Chrome que ele mesmo baixou — nunca o Chrome do sistema da pessoa.
// Linux: extraído do .deb em userData/chrome
// Windows: instalado pelo setup e marcado com userData/chrome-managed
function linuxLocalChromePath() {
  return path.join(app.getPath('userData'), 'chrome', 'opt', 'google', 'chrome', 'google-chrome');
}

function winManagedChromePath() {
  return path.join(process.env.LOCALAPPDATA || '', 'Google', 'Chrome', 'Application', 'chrome.exe');
}

function winManagedMarker() {
  return path.join(app.getPath('userData'), 'chrome-managed');
}

function findChrome() {
  if (process.env.DENUNDIKE_CHROME && fs.existsSync(process.env.DENUNDIKE_CHROME)) return process.env.DENUNDIKE_CHROME;
  if (process.platform === 'linux') {
    const p = linuxLocalChromePath();
    return fs.existsSync(p) ? p : null;
  }
  if (process.platform === 'win32') {
    // só usa o Chrome se foi instalado pelo próprio app (marcador presente)
    if (fs.existsSync(winManagedMarker())) {
      const p = winManagedChromePath();
      return fs.existsSync(p) ? p : null;
    }
    return null;
  }
  return null;
}

function findBrowser() {
  const chrome = findChrome();
  if (chrome) return { path: chrome, kind: 'chrome', name: 'Google Chrome' };
  return null;
}

/* ── download helpers ── */

/** resolve redirects e retorna URL final + tamanho + suporte a Range */
function resolveUrl(url, hops) {
  if ((hops || 0) > 10) return Promise.reject(new Error('too many redirects'));
  return new Promise((res, rej) => {
    const mod = url.startsWith('https') ? https : http;
    const req = mod.request(url, { method: 'HEAD', headers: { 'User-Agent': 'Denundike/1.0' } }, (resp) => {
      resp.resume();
      if (resp.statusCode >= 300 && resp.statusCode < 400 && resp.headers.location) {
        return resolveUrl(resp.headers.location, (hops || 0) + 1).then(res).catch(rej);
      }
      res({
        url,
        length: parseInt(resp.headers['content-length'] || '0', 10),
        ranges: resp.headers['accept-ranges'] === 'bytes',
      });
    });
    req.on('error', rej);
    req.end();
  });
}

/** baixa um intervalo [start, end] e grava em destFile */

/** download paralelo com N conexões; cai em conexão única se o servidor não suportar Range */
async function downloadFile(url, dest, onProgress, connections) {
  const N = connections || 16;
  let info;
  try { info = await resolveUrl(url); } catch (_) { info = { url, length: 0, ranges: false }; }
  const { url: finalUrl, length, ranges } = info;

  if (!ranges || !length || length < N * 64 * 1024) {
    // conexão única (servidor não suporta Range ou arquivo pequeno)
    return new Promise((res, rej) => {
      const file = fs.createWriteStream(dest);
      let got = 0;
      function request(u, hops) {
        if (hops > 10) { file.close(); return rej(new Error('too many redirects')); }
        const mod = u.startsWith('https') ? https : http;
        mod.get(u, { headers: { 'User-Agent': 'Denundike/1.0' } }, (resp) => {
          if (resp.statusCode >= 300 && resp.statusCode < 400 && resp.headers.location) {
            resp.resume(); return request(resp.headers.location, hops + 1);
          }
          if (resp.statusCode !== 200) { resp.resume(); file.close(); return rej(new Error('HTTP ' + resp.statusCode)); }
          const total = parseInt(resp.headers['content-length'] || '0', 10);
          resp.on('data', (chunk) => { got += chunk.length; if (total && onProgress) onProgress(Math.round(got / total * 100)); });
          resp.pipe(file);
          file.on('finish', () => file.close(() => res()));
          file.on('error', (e) => { file.close(); rej(e); });
          resp.on('error', (e) => { file.close(); rej(e); });
        }).on('error', (e) => { file.close(); rej(e); });
      }
      request(url, 0);
    });
  }

  // download paralelo em N chunks
  const chunkSize = Math.ceil(length / N);
  const parts = Array.from({ length: N }, (_, i) => dest + '.part' + i);
  let got = 0;
  try {
    await Promise.all(parts.map((partFile, i) => {
      const start = i * chunkSize, end = Math.min(start + chunkSize - 1, length - 1);
      return new Promise((res, rej) => {
        const file = fs.createWriteStream(partFile);
        const mod = finalUrl.startsWith('https') ? https : http;
        mod.get(finalUrl, { headers: { 'User-Agent': 'Denundike/1.0', Range: 'bytes=' + start + '-' + end } }, (resp) => {
          if (resp.statusCode !== 206 && resp.statusCode !== 200) { resp.resume(); file.close(); return rej(new Error('HTTP ' + resp.statusCode)); }
          resp.on('data', (chunk) => { got += chunk.length; if (onProgress) onProgress(Math.round(got / length * 100)); });
          resp.pipe(file);
          file.on('finish', () => file.close(() => res()));
          file.on('error', (e) => { file.close(); rej(e); });
          resp.on('error', (e) => { file.close(); rej(e); });
        }).on('error', (e) => { file.close(); rej(e); });
      });
    }));
    // monta arquivo final a partir das partes
    const out = fs.createWriteStream(dest);
    for (const part of parts) {
      await new Promise((res, rej) => {
        const inp = fs.createReadStream(part);
        inp.on('error', rej); inp.on('end', res);
        inp.pipe(out, { end: false });
      });
    }
    await new Promise((res) => out.end(res));
  } finally {
    for (const part of parts) try { fs.unlinkSync(part); } catch (_) {}
  }
}

async function extractDeb(debPath, destDir) {
  fs.mkdirSync(destDir, { recursive: true });
  try {
    await new Promise((res, rej) => { execFile('dpkg-deb', ['-x', debPath, destDir], (err) => err ? rej(err) : res()); });
    return;
  } catch (_) {}
  const arDir = debPath + '-ar';
  fs.mkdirSync(arDir, { recursive: true });
  await new Promise((res, rej) => { execFile('ar', ['x', debPath], { cwd: arDir }, (err) => err ? rej(err) : res()); });
  const files = fs.readdirSync(arDir);
  const dataTar = files.find((f) => f.startsWith('data.tar'));
  if (!dataTar) throw new Error('no data.tar in deb');
  await new Promise((res, rej) => { execFile('tar', ['xf', path.join(arDir, dataTar), '-C', destDir], (err) => err ? rej(err) : res()); });
  try { fs.rmSync(arDir, { recursive: true, force: true }); } catch (_) {}
}

async function extractZip(zipPath, destDir) {
  fs.mkdirSync(destDir, { recursive: true });
  if (process.platform === 'win32') {
    await new Promise((res, rej) => {
      execFile('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command',
        `Expand-Archive -Force -Path "${zipPath}" -DestinationPath "${destDir}"`
      ], (err) => err ? rej(err) : res());
    });
  } else {
    try {
      await new Promise((res, rej) => { execFile('unzip', ['-o', zipPath, '-d', destDir], (err) => err ? rej(err) : res()); });
    } catch (_) {
      await new Promise((res, rej) => { execFile('python3', ['-m', 'zipfile', '-e', zipPath, destDir], (err) => err ? rej(err) : res()); });
    }
  }
}

/* ── AdGuard ── */
const ADGUARD_API = 'https://api.github.com/repos/AdguardTeam/AdguardBrowserExtension/releases/latest';
const adguardApi = () => process.env.DENUNDIKE_ADGUARD_API || ADGUARD_API; // a variável existe só para testar falha de rede

/* Log próprio (sem segredos): antes a falha era totalmente silenciosa e ninguém via por que o bloqueador não carregava. */
function adguardLogFile() { return path.join(app.getPath('userData'), 'adguard.log'); }
function adgLog(msg) {
  try {
    const f = adguardLogFile();
    let old = ''; try { old = fs.readFileSync(f, 'utf8'); } catch (_) {}
    const line = new Date().toISOString() + ' ' + String(msg).split(/\s+/).join(' ').slice(0, 300) + '\n';
    fs.writeFileSync(f, (old + line).slice(-60000));
  } catch (_) {}
}

// uma pasta por versão (adguard/<tag>): atualizar nunca mexe na pasta que um Chrome aberto está usando
function adguardRoot() { return path.join(app.getPath('userData'), 'adguard'); }
function adguardVerFile() { return path.join(app.getPath('userData'), 'adguard-version'); }
function adguardTag() { try { return fs.readFileSync(adguardVerFile(), 'utf8').trim(); } catch (_) { return ''; } }
function adguardDir() {
  const tag = adguardTag();
  if (!tag || !/^[\w.\-]+$/.test(tag)) return null;
  const d = path.join(adguardRoot(), tag);
  return fs.existsSync(path.join(d, 'manifest.json')) ? d : null;
}

// O Chrome atual não carrega mais extensão MV2: vale só o pacote MV3 (chrome-mv3.zip).
function pickAdguardAsset(assets) {
  const l = Array.isArray(assets) ? assets : [];
  return l.find((a) => a && /^chrome-mv3\.zip$/i.test(a.name)) || l.find((a) => a && /^chrome[-_].*mv3.*\.zip$/i.test(a.name)) || null;
}

function httpsJson(url, timeoutMs) {
  return new Promise((res, rej) => {
    const rq = https.get(url, { headers: { 'User-Agent': 'Denundike/1.0', Accept: 'application/vnd.github+json' } }, (resp) => {
      let d = ''; resp.on('data', (c) => { d += c; });
      resp.on('end', () => {
        if (resp.statusCode !== 200) return rej(new Error('HTTP ' + resp.statusCode));
        try { res(JSON.parse(d)); } catch (e) { rej(e); }
      });
      resp.on('error', rej);
    });
    rq.setTimeout(timeoutMs || 10000, () => rq.destroy(new Error('tempo esgotado')));
    rq.on('error', rej);
  });
}

async function installAdGuard(onProgress) {
  const info = await httpsJson(adguardApi());
  const tag = info && info.tag_name;
  if (!tag || !/^[\w.\-]+$/.test(tag)) throw new Error('resposta da API sem versão');
  if (adguardTag() === tag && adguardDir()) { adgLog('AdGuard ' + tag + ' já está em dia'); return true; }
  const asset = pickAdguardAsset(info.assets);
  if (!asset) throw new Error('a versão ' + tag + ' não tem chrome-mv3.zip');
  const tmpZip = path.join(os.tmpdir(), 'adguard-' + Date.now() + '.zip');
  const tmpExt = tmpZip + '-ext';
  try {
    await downloadFile(asset.browser_download_url, tmpZip, onProgress);
    await extractZip(tmpZip, tmpExt);
    let extRoot = tmpExt;
    if (!fs.existsSync(path.join(extRoot, 'manifest.json'))) {
      const entries = fs.readdirSync(tmpExt);
      if (entries.length === 1 && fs.statSync(path.join(tmpExt, entries[0])).isDirectory()) extRoot = path.join(tmpExt, entries[0]);
    }
    if (!fs.existsSync(path.join(extRoot, 'manifest.json'))) throw new Error('o zip não trouxe manifest.json');
    const root = adguardRoot();
    fs.mkdirSync(root, { recursive: true });
    const dest = path.join(root, tag);
    fs.rmSync(dest, { recursive: true, force: true });
    fs.cpSync(extRoot, dest, { recursive: true });
    fs.writeFileSync(adguardVerFile(), tag);
    adgLog('AdGuard ' + tag + ' instalado');
    // versões antigas saem, menos a que um Chrome aberto está usando
    for (const d of fs.readdirSync(root)) {
      const full = path.join(root, d);
      if (d === tag || (cur && cur.adgDir === full)) continue;
      try { fs.rmSync(full, { recursive: true, force: true }); } catch (_) {}
    }
    return true;
  } finally {
    try { fs.rmSync(tmpZip, { force: true }); } catch (_) {}
    try { fs.rmSync(tmpExt, { recursive: true, force: true }); } catch (_) {}
  }
}

/* ── download silencioso do Chrome na inicialização ── */
let chromeBusy = null;
let _chromeProgressCb = null;
/** baixa o Chrome em background se ainda não estiver presente; nunca lança.
 *  Se já estiver baixando, onProgress é registrado na promise em curso. */
function ensureChrome(onProgress) {
  if (findChrome()) return Promise.resolve(true);
  if (onProgress) _chromeProgressCb = onProgress;
  if (!chromeBusy) {
    chromeBusy = downloadChrome((pct) => { if (_chromeProgressCb) _chromeProgressCb(pct); })
      .then(() => true)
      .catch(() => false)
      .finally(() => { chromeBusy = null; _chromeProgressCb = null; });
  }
  return chromeBusy;
}

let adgBusy = null;
/** baixa/atualiza o AdGuard; nunca lança. Chamadas simultâneas dividem o mesmo download. */
function ensureAdGuard(onProgress) {
  if (!adgBusy) {
    adgBusy = installAdGuard(onProgress)
      .catch((e) => { adgLog('falha ao baixar/atualizar: ' + ((e && e.message) || e)); return false; })
      .finally(() => { adgBusy = null; });
  }
  return adgBusy;
}

/* ── download do Chrome ── */
async function downloadChrome(onProgress) {
  if (process.platform === 'win32') {
    const tmpExe = path.join(os.tmpdir(), 'chrome_setup_' + Date.now() + '.exe');
    await downloadFile('https://dl.google.com/chrome/install/standalonesetup64.exe', tmpExe, onProgress);
    await new Promise((res, rej) => {
      execFile(tmpExe, [], (err) => { try { fs.unlinkSync(tmpExe); } catch (_) {} err ? rej(err) : res(); });
    });
    // marca que o Chrome foi instalado pelo próprio app
    try { fs.writeFileSync(winManagedMarker(), '1'); } catch (_) {}
  } else if (process.platform === 'linux') {
    const tmpDeb = path.join(os.tmpdir(), 'chrome_' + Date.now() + '.deb');
    await downloadFile('https://dl.google.com/linux/direct/google-chrome-stable_current_amd64.deb', tmpDeb, onProgress);
    const destDir = path.join(app.getPath('userData'), 'chrome');
    try { fs.rmSync(destDir, { recursive: true, force: true }); } catch (_) {}
    await extractDeb(tmpDeb, destDir);
    try { fs.unlinkSync(tmpDeb); } catch (_) {}
    const bin = linuxLocalChromePath();
    if (fs.existsSync(bin)) try { fs.chmodSync(bin, 0o755); } catch (_) {}
  } else {
    throw new Error('unsupported-platform');
  }
}

function isWebUrl(u) {
  try {
    const x = new URL(String(u));
    return (x.protocol === 'http:' || x.protocol === 'https:') && !!x.host && String(u).length <= 2048;
  } catch (_) { return false; }
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const VK = {
  Enter: 13, Backspace: 8, Tab: 9, Escape: 27, ' ': 32, ArrowLeft: 37, ArrowUp: 38, ArrowRight: 39,
  ArrowDown: 40, Delete: 46, Home: 36, End: 35, PageUp: 33, PageDown: 34, Insert: 45,
  Shift: 16, Control: 17, Alt: 18, Meta: 91, CapsLock: 20,
};
for (let i = 1; i <= 12; i++) VK['F' + i] = 111 + i;

function vkFor(key, code) {
  if (VK[key]) return VK[key];
  if (/^Key[A-Z]$/.test(code || '')) return code.charCodeAt(3);
  if (/^Digit\d$/.test(code || '')) return code.charCodeAt(5);
  if (key && key.length === 1) return key.toUpperCase().charCodeAt(0);
  return 0;
}

class CDP {
  constructor(ws) {
    this.ws = ws; this.id = 0; this.pending = new Map(); this.onEvent = () => {};
    ws.addEventListener('message', (ev) => {
      let m; try { m = JSON.parse(ev.data); } catch (_) { return; }
      if (m.id && this.pending.has(m.id)) {
        const { res, rej, timer } = this.pending.get(m.id);
        clearTimeout(timer); this.pending.delete(m.id);
        if (m.error) rej(new Error(m.error.message || 'cdp error')); else res(m.result || {});
      } else if (m.method) this.onEvent(m);
    });
    ws.addEventListener('close', () => {
      for (const { rej, timer } of this.pending.values()) { clearTimeout(timer); rej(new Error('cdp closed')); }
      this.pending.clear();
    });
  }
  send(method, params = {}, sessionId) {
    return new Promise((res, rej) => {
      const id = ++this.id;
      const timer = setTimeout(() => { this.pending.delete(id); rej(new Error('cdp timeout ' + method)); }, 10000);
      this.pending.set(id, { res, rej, timer });
      try { this.ws.send(JSON.stringify({ id, method, params, sessionId })); }
      catch (e) { clearTimeout(timer); this.pending.delete(id); rej(e); }
    });
  }
}

let cur = null; // sessão única por app

async function waitForPort(profileDir, proc) {
  const f = path.join(profileDir, 'DevToolsActivePort');
  for (let i = 0; i < 100; i++) {
    if (proc.exitCode !== null) throw new Error('chrome exited');
    try {
      const [port, p] = fs.readFileSync(f, 'utf8').split(/\r?\n/);
      if (port && p) return { port: Number(port), wsPath: p };
    } catch (_) { /* ainda não escreveu */ }
    await sleep(100);
  }
  throw new Error('chrome did not start');
}

function killTree(proc) {
  if (!proc || proc.exitCode !== null) return;
  if (process.platform === 'win32') execFile('taskkill', ['/pid', String(proc.pid), '/T', '/F'], () => {});
  else { try { proc.kill('SIGTERM'); } catch (_) {} }
}

async function stopSession(notify) {
  const s = cur; cur = null;
  if (!s) return;
  clearInterval(s.poll);
  try { s.cdp.ws.close(); } catch (_) {}
  killTree(s.proc);
  if (notify && s.sender && !s.sender.isDestroyed()) s.sender.send('browser-ended');
  // espera o Chrome sair de verdade: um Chrome novo no mesmo perfil, aberto antes disso, entrega o pedido ao antigo e fecha ("chrome exited")
  const p = s.proc;
  if (p && p.exitCode === null) await new Promise((res) => { const t = setTimeout(res, 4000); p.once('exit', () => { clearTimeout(t); res(); }); });
}

// Viewport 16:9 de h linhas. Na abertura a moldura (--window-size) soma 16 px de largura e 104 de altura
// (título + barra de abas + barra de endereço do Chrome completo); ao redimensionar por CDP o loop de
// calibração mede o viewport real e corrige.
const FRAME_W = 16, FRAME_H = 104, TITLE_H = 100;
function windowFor(h) {
  h = [720, 1080, 1440].includes(Number(h)) ? Number(h) : 1080;
  let w = Math.round(h * 16 / 9);
  // a janela nunca passa do tamanho da tela: o Windows corta o excedente e o recorte/mouse deixariam de bater com a página
  try {
    const d = require('electron').screen.getPrimaryDisplay().size; // só depois do 'ready'
    const k = Math.min(1, (d.width - 24) / w, (d.height - FRAME_H - 24) / h);
    if (k < 1) { h = Math.max(360, Math.floor(h * k / 2) * 2); w = Math.round(h * 16 / 9 / 2) * 2; }
  } catch (_) {}
  return { h, w };
}

/** carrega o AdGuard (pasta da versão atual) no Chrome da sessão e fecha as páginas que ele abre ao instalar */
async function loadAdguard(s) {
  const adg = adguardDir();
  if (!adg || s.adgId || !s.cdp) return;
  try {
    const r = await s.cdp.send('Extensions.loadUnpacked', { path: adg });
    s.adgDir = adg; s.adgId = r.id;
    adgLog('extensão carregada no Chrome (id ' + r.id + ', versão ' + path.basename(adg) + ')');
    s.cdp.send('Target.setDiscoverTargets', { discover: true }).catch(() => {});
    const stray = (t) => t && t.type === 'page' && t.targetId !== s.targetId && (/^chrome-extension:\/\//i.test(t.url || '') || /^https?:\/\/([\w-]+\.)*adguard\.(com|org|info)\b/i.test(t.url || ''));
    s.onTarget = (m) => {
      if (m.method === 'Target.targetCreated' || m.method === 'Target.targetInfoChanged') {
        const t = m.params && m.params.targetInfo;
        if (stray(t)) s.cdp.send('Target.closeTarget', { targetId: t.targetId }).catch(() => {});
      }
    };
  } catch (e) { adgLog('Extensions.loadUnpacked falhou: ' + ((e && e.message) || e)); }
}

async function startSession(sender, url, opts, retried) {
  if (cur) await stopSession(false);
  let found = findBrowser();
  if (!found) {
    const prep = (state, pct) => { try { if (!sender.isDestroyed()) sender.send('browser-prep', { what: 'chrome', state, pct: pct || 0 }); } catch (_) {} };
    prep('start', 0);
    // usa ensureChrome para não duplicar download se já estiver rodando em background
    const ok = await ensureChrome((pct) => prep('progress', pct));
    prep('end', 100);
    if (!ok) return { error: 'no-chrome' };
    found = findBrowser();
    if (!found) return { error: 'no-chrome' };
  }
  const chrome = found.path;
  if (url && !isWebUrl(url)) return { error: 'bad-url' };

  // Sem AdGuard ainda (primeira vez, ou o download antigo falhou): baixa antes de abrir, com barra de progresso.
  // Se a internet estiver lenta, não segura a abertura: abre o Chrome sem bloqueador e, quando o download terminar,
  // carrega a extensão no Chrome que já está aberto (sem reiniciar nada).
  let adgPending = null;
  if (!adguardDir()) {
    let barOpen = true;
    const prep = (state, pct) => { try { if (!sender.isDestroyed()) sender.send('browser-prep', { what: 'adguard', state, pct: pct || 0 }); } catch (_) {} };
    prep('start', 0);
    adgPending = ensureAdGuard((pct) => { if (barOpen) prep('progress', pct); });
    const ok = await Promise.race([adgPending, sleep(20000).then(() => 'timeout')]);
    barOpen = false;
    if (ok === 'timeout') adgLog('download lento (mais de 20 s); abrindo o navegador agora e carregando o AdGuard quando terminar');
    prep('end', 100);
  }

  const win = windowFor(opts && opts.h);
  const token = 'DNDK-' + crypto.randomBytes(5).toString('hex');
  const profileDir = path.join(app.getPath('userData'), 'chrome-profile');
  fs.mkdirSync(profileDir, { recursive: true });
  try { fs.unlinkSync(path.join(profileDir, 'DevToolsActivePort')); } catch (_) {}

  const args = [
    '--user-data-dir=' + profileDir,
    '--remote-debugging-port=0',
    '--remote-debugging-address=127.0.0.1',
    '--disable-blink-features=AutomationControlled',
    '--test-type',
    '--window-size=' + (win.w + FRAME_W) + ',' + (win.h + FRAME_H), '--window-position=30,20',
    '--no-first-run', '--no-default-browser-check',
    '--disable-gpu',
    '--autoplay-policy=no-user-gesture-required',
    '--disable-backgrounding-occluded-windows', '--disable-renderer-backgrounding',
    '--disable-background-timer-throttling',
    // abre uma aba com o token no título para acharmos a janela; navega para o Google logo depois
    'data:text/html,<title>' + token + '</title><script>setTimeout(()=>location.replace("https://www.google.com.br"),80)</script>',
  ];
  // O Chrome de marca (137+) ignora --load-extension. O que funciona é carregar a extensão pelo protocolo
  // DevTools (Extensions.loadUnpacked), que exige --enable-unsafe-extension-debugging. A porta só escuta em 127.0.0.1.
  args.push('--enable-unsafe-extension-debugging');
  if (process.platform === 'linux') {
    args.push('--enable-audio-output-capture');
    // Ubuntu e derivados podem não ter user namespaces habilitados; --no-sandbox resolve
    args.push('--no-sandbox');
  }
  const proc = spawn(chrome, args, { stdio: 'ignore', windowsHide: false });
  const s = { proc, sender, token, cdp: null, sessionId: null, targetId: null, windowId: null, vp: null, vpAt: 0, buttons: 0, lastDown: {}, size: '', owner: null, ownerT: 0, queue: [], moving: false };
  cur = s;
  proc.on('exit', () => { if (cur === s) { cur = null; clearInterval(s.poll); if (!sender.isDestroyed()) sender.send('browser-ended'); } });

  try {
    const { port, wsPath } = await waitForPort(profileDir, proc);
    const ws = new WebSocket('ws://127.0.0.1:' + port + wsPath);
    await new Promise((res, rej) => { ws.addEventListener('open', res); ws.addEventListener('error', () => rej(new Error('cdp connect'))); });
    s.cdp = new CDP(ws);

    if (adguardDir()) await loadAdguard(s);
    else if (adgPending) adgPending.then((ok) => { if (ok === true && cur === s && s.cdp) loadAdguard(s); }).catch(() => {});

    let target = null;
    for (let i = 0; i < 50 && !target; i++) {
      const { targetInfos } = await s.cdp.send('Target.getTargets');
      target = targetInfos.find((t) => t.type === 'page');
      if (!target) await sleep(100);
    }
    if (!target) throw new Error('no page');
    s.targetId = target.targetId;
    s.sessionId = (await s.cdp.send('Target.attachToTarget', { targetId: target.targetId, flatten: true })).sessionId;
    await s.cdp.send('Page.enable', {}, s.sessionId);
    s.cdp.onEvent = (m) => {
      if (s.onTarget && !m.sessionId) s.onTarget(m);
      if (m.sessionId !== s.sessionId || sender.isDestroyed()) return;
      if (m.method === 'Page.frameNavigated' && !m.params.frame.parentId) sender.send('browser-url', m.params.frame.url);
      else if (m.method === 'Page.navigatedWithinDocument') sender.send('browser-url', m.params.url);
    };
    try { s.windowId = (await s.cdp.send('Browser.getWindowForTarget', { targetId: s.targetId })).windowId; } catch (_) {}

    // acha a janela do Chrome pelo título único e devolve o id de captura
    let sourceId = null;
    for (let i = 0; i < 60 && !sourceId; i++) {
      const srcs = await desktopCapturer.getSources({ types: ['window'], thumbnailSize: { width: 0, height: 0 } });
      const hit = srcs.find((x) => x.name.includes(token));
      if (hit) sourceId = hit.id; else await sleep(150);
    }
    if (!sourceId) throw new Error('window not found');

    s.poll = setInterval(async () => {
      if (cur !== s || !s.windowId) return;
      try {
        const { bounds } = await s.cdp.send('Browser.getWindowBounds', { windowId: s.windowId });
        const k = bounds.width + 'x' + bounds.height + ':' + bounds.windowState;
        if (k !== s.size) { const first = s.size === ''; s.size = k; s.vpAt = 0; if (!first && !sender.isDestroyed()) sender.send('browser-size'); }
        // há vídeo tocando na página? (o app troca a dica de codificação entre 'detail' e 'motion')
        const mv = await s.cdp.send('Runtime.evaluate', { expression: "[...document.querySelectorAll('video')].some(v=>!v.paused&&!v.ended&&v.readyState>2&&v.videoWidth>0)", returnByValue: true }, s.sessionId);
        const moving = mv.result && mv.result.value === true;
        if (moving !== s.moving) { s.moving = moving; if (!sender.isDestroyed()) sender.send('browser-motion', moving); }
      } catch (_) {}
    }, 1000);

    return { ok: true, sourceId, startUrl: url || null, browser: found.name };
  } catch (e) {
    await stopSession(false);
    // o perfil pode ainda estar preso ao Chrome anterior (parou e abriu logo em seguida): uma segunda tentativa resolve
    if (!retried && /chrome exited/.test(String(e && e.message))) { await sleep(1500); return startSession(sender, url, opts, true); }
    return { error: e.message || 'failed' };
  }
}

async function viewport(s) {
  if (s.vp && Date.now() - s.vpAt < 1000) return s.vp;
  const m = await s.cdp.send('Page.getLayoutMetrics', {}, s.sessionId);
  const v = m.cssVisualViewport || m.cssLayoutViewport;
  s.vp = { w: v.clientWidth, h: v.clientHeight };
  s.vpAt = Date.now();
  return s.vp;
}

async function navigate(s, n) {
  if (!s.cdp || !s.sessionId) return; // o Chrome ainda está abrindo
  const c = s.cdp, sid = s.sessionId;
  if (n.action === 'go') {
    if (!isWebUrl(n.url)) return;
    await c.send('Page.navigate', { url: n.url }, sid);
  } else if (n.action === 'reload') {
    await c.send('Page.reload', {}, sid);
  } else if (n.action === 'back' || n.action === 'forward') {
    const h = await c.send('Page.getNavigationHistory', {}, sid);
    const e = h.entries[h.currentIndex + (n.action === 'back' ? -1 : 1)];
    if (e) await c.send('Page.navigateToHistoryEntry', { entryId: e.id }, sid);
  }
}

// Um único ponteiro virtual serve a todos: o gesto (apertar→soltar) de um convidado é atômico e os dos
// outros esperam na fila, assim cliques simultâneos de pessoas diferentes não se misturam.
const gestureBusy = (s, from) => s.owner && s.owner !== from && Date.now() - s.ownerT < 2000;

async function applyMouse(s, from, m) {
  if (s.owner && s.owner !== from) s.owner = null;
  if (m.t === 'down') { s.owner = from; s.ownerT = Date.now(); }
  await inputEvent(s, m);
  if (m.t === 'up' && s.owner === from) s.owner = null;
}

async function drain(s) {
  if (s.draining) return;
  s.draining = true;
  try {
    for (;;) {
      const live = s.owner && Date.now() - s.ownerT < 2000;
      // com um gesto aberto só o dono avança (na ordem dele); sem dono, vale a ordem de chegada
      const i = live ? s.queue.findIndex(([f]) => f === s.owner) : 0;
      if (i < 0 || !s.queue.length) break;
      const [f, mm] = s.queue.splice(i, 1)[0];
      await applyMouse(s, f, mm);
    }
  } finally { s.draining = false; }
}

async function route(s, from, m) {
  const mouse = m.t === 'move' || m.t === 'down' || m.t === 'up' || m.t === 'wheel';
  if (!mouse) return inputEvent(s, m);
  const mine = s.owner === from && Date.now() - s.ownerT < 2000;
  const queuedBefore = s.queue.some(([f]) => f === from);
  if (queuedBefore || (!mine && (gestureBusy(s, from) || s.queue.length))) {
    if (m.t !== 'move' && s.queue.length < 50) {
      s.queue.push([from, m]);
      setTimeout(() => { if (cur === s) drain(s).catch(() => {}); }, 2100);
    }
    await drain(s);
    return;
  }
  await applyMouse(s, from, m);
  await drain(s);
}

const BTN = { left: 1, right: 2, middle: 4 };
const clamp01 = (n) => Math.min(1, Math.max(0, Number(n) || 0));

async function inputEvent(s, m) {
  if (!s.cdp || !s.sessionId) return;
  const c = s.cdp, sid = s.sessionId;
  const mods = (m.mods | 0) & 15;
  if (m.t === 'move' || m.t === 'down' || m.t === 'up' || m.t === 'wheel') {
    const vp = await viewport(s);
    const x = clamp01(m.x) * vp.w, y = clamp01(m.y) * vp.h;
    if (m.t === 'move') {
      return c.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y, button: 'none', buttons: s.buttons, modifiers: mods }, sid);
    }
    if (m.t === 'wheel') {
      return c.send('Input.dispatchMouseEvent', { type: 'mouseWheel', x, y, deltaX: Number(m.dx) || 0, deltaY: Number(m.dy) || 0, modifiers: mods }, sid);
    }
    if (!BTN[m.button]) return;
    if (m.t === 'down') {
      const now = Date.now(), last = s.lastDown;
      const dbl = last.t && now - last.t < 450 && Math.abs(last.x - x) < 6 && Math.abs(last.y - y) < 6 && last.b === m.button;
      const clickCount = dbl ? 2 : 1;
      s.lastDown = { t: dbl ? 0 : now, x, y, b: m.button };
      s.buttons |= BTN[m.button];
      return c.send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: m.button, buttons: s.buttons, clickCount, modifiers: mods }, sid);
    }
    s.buttons &= ~BTN[m.button];
    return c.send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: m.button, buttons: s.buttons, clickCount: 1, modifiers: mods }, sid);
  }
  if (m.t === 'key_down' || m.t === 'key_up') {
    const key = String(m.key || '').slice(0, 32), code = String(m.code || '').slice(0, 32);
    const vk = vkFor(key, code);
    const base = { key, code, windowsVirtualKeyCode: vk, nativeVirtualKeyCode: vk, modifiers: mods };
    if (m.t === 'key_up') return c.send('Input.dispatchKeyEvent', { type: 'keyUp', ...base }, sid);
    const text = String(m.text || '').slice(0, 8);
    if (text && !(mods & 6)) return c.send('Input.dispatchKeyEvent', { type: 'keyDown', text, unmodifiedText: text, ...base }, sid);
    return c.send('Input.dispatchKeyEvent', { type: 'rawKeyDown', ...base }, sid);
  }
}

function registerBrowserIpc() {
  ipcMain.handle('browser-available', () => !!findBrowser());
  ipcMain.handle('browser-info', () => { const b = findBrowser(); return b ? { ok: true, name: b.name } : { ok: false, name: null }; });
  ipcMain.handle('browser-download-start', async (e) => {
    try {
      await downloadChrome((pct) => { if (!e.sender.isDestroyed()) e.sender.send('browser-download-progress', pct); });
      return { ok: true };
    } catch (err) {
      return { ok: false, error: err.message };
    }
  });
  ipcMain.handle('browser-start', (e, url, opts) => startSession(e.sender, url, opts));
  // a resolução escolhida na Qualidade vira o tamanho real da janela: 1440p captura de fato 2560x1440
  ipcMain.handle('browser-resize', async (_e, h) => {
    if (!cur || !cur.windowId) return false;
    const s = cur, w = windowFor(h);
    try {
      let bw = w.w, bh = w.h + TITLE_H;
      for (let i = 0; i < 3; i++) {
        await s.cdp.send('Browser.setWindowBounds', { windowId: s.windowId, bounds: { windowState: 'normal', width: bw, height: bh } });
        await sleep(350);
        const m = await s.cdp.send('Page.getLayoutMetrics', {}, s.sessionId);
        const v = m.cssLayoutViewport || m.cssVisualViewport;
        const dw = w.w - v.clientWidth, dh = w.h - v.clientHeight;
        if (Math.abs(dw) <= 1 && Math.abs(dh) <= 1) break;
        bw += dw; bh += dh; // corrige a diferença da moldura até o viewport sair exato
      }
      s.vpAt = 0;
      return true;
    } catch (_) { return false; }
  });
  ipcMain.handle('browser-stop', () => stopSession(false));
  ipcMain.handle('browser-nav', async (_e, n) => {
    if (!cur || !n || typeof n !== 'object') return false;
    try { await navigate(cur, { action: String(n.action), url: n.url }); return true; } catch (_) { return false; }
  });
  ipcMain.handle('browser-input', async (_e, p) => {
    if (!cur || !p || typeof p !== 'object' || !p.ev || typeof p.ev !== 'object') return false;
    try { await route(cur, String(p.from || ''), p.ev); return true; } catch (_) { return false; }
  });
  // esconde a janela solta do Chrome do anfitrião sem perder a captura (minimizar pararia o vídeo)
  ipcMain.handle('browser-park', async (_e, on) => {
    if (!cur || !cur.windowId) return false;
    try {
      await cur.cdp.send('Browser.setWindowBounds', { windowId: cur.windowId, bounds: { windowState: 'normal', left: on ? -12000 : 30, top: on ? 0 : 20 } });
      return true;
    } catch (_) { return false; }
  });
  // lente de calibração: pinta o viewport de magenta para o app achar a área exata da página no quadro capturado
  ipcMain.handle('browser-overlay', async (_e, on) => {
    if (!cur) return false;
    const expr = on
      ? "(()=>{let d=document.getElementById('__dndk_cal');if(!d){d=document.createElement('div');d.id='__dndk_cal';d.style.cssText='position:fixed;inset:0;background:#ff00ff;pointer-events:none;z-index:2147483647';(document.documentElement).appendChild(d)}})()"
      : "(()=>{const d=document.getElementById('__dndk_cal');if(d)d.remove()})()";
    try { await cur.cdp.send('Runtime.evaluate', { expression: expr }, cur.sessionId); return true; } catch (_) { return false; }
  });
  app.on('before-quit', () => { stopSession(false); });
  // AdGuard: baixa na inicialização (ou tenta de novo se falhou) e confere versão nova a cada hora
  ensureAdGuard();
  setInterval(() => ensureAdGuard(), 3600 * 1000);
}

module.exports = { registerBrowserIpc, ensureChrome, ensureAdGuard, adguardDir, adguardTag, findChrome, findBrowser, isWebUrl, startSession, stopSession, navigate, inputEvent, route, getCurrent: () => cur };

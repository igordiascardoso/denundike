/* browser.js — navegador compartilhado: o anfitrião transmite um Chrome real pela chamada e todo mundo controla */
(function () {
  'use strict';

  const COLORS = ['#C8F560', '#FF7A59', '#5BC0FF', '#E58BFF', '#FFD25B', '#6EF0B0'];
  const colorOf = (id) => { let h = 0; for (const c of String(id)) h = (h * 31 + c.charCodeAt(0)) >>> 0; return COLORS[h % COLORS.length]; };
  const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
  const $ = (s) => document.querySelector(s);
  const MAX_W = 2560;

  function normalizeUrl(v) {
    v = String(v || '').trim();
    if (!v) return null;
    if (/^https?:\/\//i.test(v)) return v;
    if (/^(localhost|\d{1,3}(\.\d{1,3}){3})(:\d+)?(\/\S*)?$/i.test(v)) return 'http://' + v;
    if (!/\s/.test(v) && /^[^\s/]+\.[^\s/]{2,}(\/\S*)?$/.test(v)) return 'https://' + v;
    return 'https://www.google.com/search?q=' + encodeURIComponent(v);
  }

  class BrowserShare {
    /** o: { me, call, send(obj), toast(msg,isErr), onChange() } */
    constructor(o) {
      this.o = o;
      this.state = { active: false };
      this.local = null;
      this.starting = false;
      this.cursors = new Map();
      this.heldButton = null;
      this.hiddenLocal = false;
      this.focused = true;
      this._lastMove = 0;
      this.el = {
        stage: $('#bStage'), view: $('#bView'), video: $('#bVideo'), cur: $('#bCur'), wait: $('#bWait'),
        url: $('#bUrl'), form: $('#bForm'), back: $('#bBack'), fwd: $('#bFwd'), reload: $('#bReload'),
        stop: $('#bStop'), who: $('#bWho'), btn: $('#cbBrw'), full: $('#bFull'), hide: $('#bHide'), banner: $('#bBanner'), open: $('#bOpen'),
      };
      this._bindUi();
      if (window.bridge && window.bridge.browser) {
        window.bridge.browser.onUrl((u) => { if (this.isHost) this._sendCall('browser_url', { url: u }); });
        window.bridge.browser.onEnded(() => { if (this.local) { this._sendCall('browser_stop'); this._stopLocal(); this.o.toast('O navegador do anfitrião foi fechado — sessão encerrada.'); } });
        window.bridge.browser.onSize(() => this._recalibrateSoon());
        window.bridge.browser.onFrame((d) => this._onFrame(d));
        window.bridge.browser.onMotion((m) => {
          const t = this.local && this.local.out && this.local.out.getVideoTracks()[0];
          // no modo automático o app troca a dica quando há vídeo tocando na página; nos outros modos vale a escolha do usuário
          if (t && this.o.call.opts.mode === 'auto') t.contentHint = m ? 'motion' : 'detail';
        });
        window.bridge.browser.onDownloadProgress((pct) => {
          if (this._downloading) this._setDlProgress(pct);
        });
        window.bridge.browser.onPrep((p) => this._onPrep(p));
      }
    }

    get isHost() { return !!(this.state.active && this.state.host_id === this.o.me.id); }
    get inThisCall() { return this.o.call.inCall && this.state.channel_id === this.o.call.channelId; }

    _sendCall(type, data) {
      if (!this.o.call.channelId) return;
      this.o.send({ type, channel_id: this.o.call.channelId, data: data || {} });
    }

    /* ── botão da barra de chamada ── */
    async toggle() {
      const call = this.o.call;
      if (!call.inCall) return;
      if (this.state.active && this.inThisCall) {
        if (this.hiddenLocal || !this.focused) { this.show(); return; }
        if (this.isHost) {
          window.openConfirm({
            title: 'Fechar o navegador compartilhado?',
            text: 'A transmissão termina para todo mundo na chamada.',
            ok: 'Fechar navegador',
            run: async () => this.stop(),
          });
        } else this.el.view.focus();
        return;
      }
      if (this.starting) return;
      const info = await window.bridge.browser.info();
      if (!info.ok) {
        this._downloadAndOpen();
        return;
      }
      if (this.o.askQuality && !(await this.o.askQuality())) return;
      this.starting = true;
      this._sendCall('browser_start', { url: 'https://www.google.com' });
    }

    stop() {
      if (this.isHost || this.local) this._sendCall('browser_stop');
      this._stopLocal();
    }

    async _downloadAndOpen() {
      if (this._downloading) return;
      this._downloading = true;
      this._setDlProgress(0);
      const m = $('#m-chromedl');
      if (m) m.hidden = false;
      try {
        const r = await window.bridge.browser.download();
        if (m) m.hidden = true;
        this._downloading = false;
        if (!r || !r.ok) throw new Error((r && r.error) || 'falha');
        this.toggle();
      } catch (_) {
        if (m) m.hidden = true;
        this._downloading = false;
        this.o.toast('Não foi possível baixar o Chrome. Verifique sua conexão e tente novamente.', true);
      }
    }

    /** o processo principal está baixando o Chrome ou o AdGuard antes de abrir (só na primeira vez) */
    _onPrep(p) {
      if (this._downloading || !p || (p.what !== 'adguard' && p.what !== 'chrome')) return;
      const m = $('#m-chromedl');
      if (!m) return;
      const h = m.querySelector('h2'), t = m.querySelector('p');
      if (!this._dlTxt) this._dlTxt = { h: h.textContent, p: t.textContent };
      if (p.state === 'end') { m.hidden = true; h.textContent = this._dlTxt.h; t.textContent = this._dlTxt.p; return; }
      if (p.what === 'chrome') {
        h.textContent = 'Baixando o navegador';
        t.textContent = 'O Google Chrome será instalado automaticamente (só na primeira vez).';
      } else {
        h.textContent = 'Preparando o bloqueador de anúncios';
        t.textContent = 'Baixando o AdGuard para o navegador compartilhado (só na primeira vez).';
      }
      m.hidden = false;
      this._setDlProgress(p.pct || 0);
    }

    _setDlProgress(pct) {
      const bar = document.getElementById('dlChromeBar');
      const txt = document.getElementById('dlChromePct');
      if (bar) bar.value = pct;
      if (txt) txt.textContent = pct < 100 ? 'Baixando… ' + pct + '%' : 'Instalando…';
    }

    /** recolhe o painel só para mim; a sessão continua para os outros */
    hide() {
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
      this.hiddenLocal = true;
      if (this.o.onHide) this.o.onHide();
      this.render();
    }

    show() {
      this.hiddenLocal = false;
      if (this.o.onShow) this.o.onShow();
      this.render();
    }

    /* ── mensagens do servidor ── */
    handleWS(ev) {
      switch (ev.type) {
        case 'browser_state': {
          if (ev.channel_id !== this.o.call.channelId) return;
          const wasHost = this.isHost;
          const prevHost = this.state.active ? this.state.host_id : null;
          this.state = ev.active ? ev : { active: false, channel_id: ev.channel_id };
          if (!ev.active || ev.host_id !== prevHost) this.hiddenLocal = false;
          if (!ev.active) { this.cursors.clear(); if (this.local || wasHost) this._stopLocal(); this.starting = false; }
          else if (this.isHost && !this.local) this._launch(ev.url);
          else if (!this.isHost) this.starting = false;
          this.render();
          break;
        }
        case 'browser_cmd':
          if (this.isHost && this.local && !this.local.ready) {
            // o Chrome ainda está abrindo (baixando o bloqueador, carregando a extensão, calibrando): guarda o último endereço pedido
            if (ev.kind === 'nav') this._pendingNav = ev.data;
          } else if (this.isHost && this.local) {
            if (ev.kind === 'nav') window.bridge.browser.nav(ev.data);
            else if (ev.kind === 'input') window.bridge.browser.input(ev.from, ev.data);
          }
          break;
        case 'browser_cursor':
          this.cursors.set(ev.user_id, { login: ev.login, x: ev.x, y: ev.y, at: Date.now() });
          this._drawCursors();
          break;
        case 'browser_error': {
          const map = {
            'session active': 'Já existe um navegador compartilhado nesta chamada.',
            'not in call': 'Entre na chamada para usar o navegador compartilhado.',
            'invalid url': 'Esse endereço não é válido (só http e https).',
          };
          this.starting = false;
          this.o.toast(map[ev.error] || 'Não foi possível usar o navegador compartilhado.', true);
          break;
        }
      }
    }

    /** a chamada acabou (ou o usuário saiu): encerra o que for local */
    reset() {
      this.state = { active: false };
      this.starting = false;
      this.cursors.clear();
      this._stopLocal();
      this.render();
    }

    /* ── anfitrião: lança o Chrome, captura, recorta e transmite ── */
    async _launch(url) {
      if (this.local) return;
      this.starting = true;
      const fail = (msg) => {
        this.o.toast(msg, true);
        this._sendCall('browser_stop');
        this._stopLocal();
      };
      const L = this.local = { raw: null, out: null, video: null, canvas: null, ctx: null, crop: null, timer: null, calibrating: true, running: true };
      try {
        const r = await window.bridge.browser.start(null, { h: this.o.call.quality.h });
        if (!this.local) return;
        if (r.error === 'no-chrome') return fail('Não foi possível iniciar o Chrome. Reinicie o app e tente novamente.');
        if (r.error) return fail('Não consegui abrir o navegador (' + r.error + ').');
        if (r.cast) {
          // Linux: o Chrome não tem janela; os quadros chegam do processo principal e vão direto para o canvas da transmissão
          L.cast = true;
          L.canvas = document.createElement('canvas');
          L.canvas.width = r.w - (r.w % 4); L.canvas.height = r.h - (r.h % 2);
          L.ctx = L.canvas.getContext('2d', { alpha: false });
          L.ctx.fillStyle = '#000'; L.ctx.fillRect(0, 0, L.canvas.width, L.canvas.height);
          L.ctx.imageSmoothingQuality = 'high';
          this._winH = this.o.call.quality.h;
          this._restartDraw();
          const ctrack = L.canvas.captureStream().getVideoTracks()[0];
          ctrack.contentHint = this.o.call.hint();
          L.out = new MediaStream([ctrack]);
          this.o.call.setBrowserStream(L.out);
          await window.bridge.browser.nav({ action: 'go', url: url || 'https://www.google.com' });
          L.ready = true;
          if (this._pendingNav) { const n = this._pendingNav; this._pendingNav = null; window.bridge.browser.nav(n); }
          this.starting = false;
          this.render();
          return;
        }
        // A captura do áudio do sistema pode travar sem erro em alguns PCs: dá 12 s e tenta de novo só com vídeo.
        const grab = (audio) => new Promise(async (res, rej) => {
          let over = false;
          const t = setTimeout(() => { over = true; rej(new Error('timeout')); }, 12000);
          try {
            await window.bridge.screen.choose(r.sourceId, audio);
            const s = await navigator.mediaDevices.getDisplayMedia({ video: { frameRate: { ideal: 30 } }, audio });
            clearTimeout(t);
            if (over) { s.getTracks().forEach((x) => x.stop()); return; }
            over = true; res(s);
          } catch (e) { clearTimeout(t); if (!over) { over = true; rej(e); } }
        });
        let raw;
        try { raw = await grab(true); }
        catch (_) {
          if (!this.local) return;
          try { raw = await grab(false); }
          catch (_) { return fail('Não consegui capturar a janela do navegador.'); }
        }
        if (!this.local) { raw.getTracks().forEach((t) => t.stop()); return; }
        L.raw = raw;
        raw.getVideoTracks()[0].onended = () => { if (this.local === L) { this._sendCall('browser_stop'); this._stopLocal(); } };

        const v = document.createElement('video');
        v.muted = true; v.playsInline = true; v.autoplay = true;
        v.srcObject = new MediaStream(raw.getVideoTracks());
        L.video = v;
        await v.play().catch(() => {});
        for (let i = 0; i < 50 && !v.videoWidth; i++) await sleep(100);
        if (!v.videoWidth) return fail('A captura do navegador não enviou imagem.');

        L.canvas = document.createElement('canvas');
        L.ctx = L.canvas.getContext('2d', { alpha: false });
        L.ctx.imageSmoothingQuality = 'high';
        this._winH = this.o.call.quality.h;
        await this._calibrate();
        if (!this.local) return;
        this._applyCrop();
        this._restartDraw();
        const track = L.canvas.captureStream().getVideoTracks()[0];
        track.contentHint = this.o.call.hint();
        L.out = new MediaStream([track, ...raw.getAudioTracks()]);
        this.o.call.setBrowserStream(L.out);
        await window.bridge.browser.nav({ action: 'go', url: url || 'https://www.google.com' });
        await window.bridge.browser.park(true);
        L.ready = true;
        if (this._pendingNav) { const n = this._pendingNav; this._pendingNav = null; window.bridge.browser.nav(n); }
        this.starting = false;
        this.render();
      } catch (e) {
        fail('Falha ao iniciar o navegador compartilhado.');
      }
    }

    /** acha, no quadro capturado, o retângulo exato da página (pintada de magenta por um instante) */
    async _calibrate() {
      const L = this.local;
      if (!L) return;
      L.calibrating = true;
      let rect = null;
      try {
        const until = Date.now() + 8000;
        while (!rect && Date.now() < until && this.local === L) {
          await window.bridge.browser.overlay(true); // reaplica: o Chrome pode trocar de documento logo ao abrir
          await sleep(250);
          rect = this._findMagenta(L.video);
        }
      } finally {
        await window.bridge.browser.overlay(false).catch(() => {});
        L.calibrating = false;
      }
      L.crop = rect || { x: 0, y: 0, w: L.video.videoWidth, h: L.video.videoHeight };
      L.fallback = !rect;
      if (!rect && !this._retried) { this._retried = true; setTimeout(() => { this._retried = false; if (this.local === L && L.fallback) this._recalibrateSoon(); }, 4000); }
    }

    _findMagenta(v) {
      const w = v.videoWidth, h = v.videoHeight;
      if (!w || !h) return null;
      const c = document.createElement('canvas');
      c.width = w; c.height = h;
      const x = c.getContext('2d', { willReadFrequently: true });
      x.drawImage(v, 0, 0, w, h);
      const d = x.getImageData(0, 0, w, h).data;
      let minX = w, minY = h, maxX = -1, maxY = -1, n = 0;
      for (let py = 0; py < h; py += 2) {
        for (let px = 0; px < w; px += 2) {
          const i = (py * w + px) * 4;
          if (d[i] > 225 && d[i + 1] < 45 && d[i + 2] > 225) {
            n++;
            if (px < minX) minX = px; if (px > maxX) maxX = px;
            if (py < minY) minY = py; if (py > maxY) maxY = py;
          }
        }
      }
      if (n < 400 || maxX - minX < 100 || maxY - minY < 100) return null;
      return { x: minX, y: minY, w: Math.min(w - minX, maxX - minX + 2), h: Math.min(h - minY, maxY - minY + 2) };
    }

    _applyCrop() {
      const L = this.local;
      if (!L || !L.crop) return;
      const q = this.o.call.quality;
      const k = Math.min(1, MAX_W / L.crop.w, q.h / L.crop.h);
      // O WebRTC reamostra (e borra) todo quadro cuja LARGURA não é múltipla de 4: um recorte de 1918 px deixava a imagem "apagada"
      // para todo mundo, em qualquer codec e com qualquer bitrate. Alinha cortando até 3 colunas e 1 linha em vez de escalar.
      let w = Math.round(L.crop.w * k), h = Math.round(L.crop.h * k);
      w = Math.max(4, w - (w % 4)); h = Math.max(2, h - (h % 2));
      L.src = { x: L.crop.x, y: L.crop.y, w: w / k, h: h / k };
      if (L.canvas.width !== w || L.canvas.height !== h) { L.canvas.width = w; L.canvas.height = h; L.ctx.imageSmoothingQuality = 'high'; }
    }

    _restartDraw() {
      const L = this.local;
      if (!L) return;
      clearInterval(L.timer);
      L.timer = setInterval(() => this._draw(), Math.round(1000 / this.o.call.quality.fps));
    }

    /** o usuário trocou a qualidade: a tela cheia do canvas acompanha (resolução e fps) */
    applyQuality() {
      if (!this.local) return;
      const h = this.o.call.quality.h;
      if (this.local.cast) {
        if (this._winH !== h) { this._winH = h; window.bridge.browser.resize(h).catch(() => {}); }
        this._restartDraw();
        return;
      }
      if (!this.local.crop) return;
      // a janela do Chrome acompanha a resolução: o aviso de mudança de tamanho recalibra o recorte sozinho
      if (this._winH !== h) { this._winH = h; window.bridge.browser.resize(h).catch(() => {}); }
      this._applyCrop();
      this._restartDraw();
    }

    /** quadro JPEG novo do Chrome sem janela: guarda só o mais recente e decodifica um por vez */
    _onFrame(data) {
      const L = this.local;
      if (!L || !L.cast || !L.running) return;
      L.frameData = data;
      if (L.decoding) return;
      L.decoding = true;
      const img = new Image();
      const done = () => {
        L.decoding = false;
        if (this.local === L && L.frameData !== data) this._onFrame(L.frameData);
      };
      img.onload = () => { L.img = img; done(); };
      img.onerror = done;
      img.src = 'data:image/jpeg;base64,' + data;
    }

    _draw() {
      const L = this.local;
      if (L && L.cast) {
        if (!L.running || !L.img) return;
        const w = L.img.naturalWidth - (L.img.naturalWidth % 4), h = L.img.naturalHeight - (L.img.naturalHeight % 2);
        if (w < 4 || h < 2) return;
        if (L.canvas.width !== w || L.canvas.height !== h) { L.canvas.width = w; L.canvas.height = h; L.ctx.imageSmoothingQuality = 'high'; }
        // redesenha o último quadro a cada tick: página parada não gera quadro novo e a transmissão não pode congelar para quem entra depois
        try { L.ctx.drawImage(L.img, 0, 0, w, h); } catch (_) {}
        return;
      }
      if (!L || !L.running || L.calibrating || !L.crop || !L.video.videoWidth) return;
      const r = L.src || L.crop;
      try { L.ctx.drawImage(L.video, r.x, r.y, r.w, r.h, 0, 0, L.canvas.width, L.canvas.height); } catch (_) {}
    }

    _recalibrateSoon() {
      const L = this.local;
      if (!L || !L.video) return;
      clearTimeout(this._rc);
      this._rc = setTimeout(async () => {
        if (this.local !== L) return;
        await this._calibrate();
        if (this.local === L) this._applyCrop();
      }, 700);
    }

    async _stopLocal() {
      const L = this.local;
      this.local = null;
      this.starting = false;
      if (!L) return;
      L.running = false;
      this._pendingNav = null;
      clearInterval(L.timer);
      clearTimeout(this._rc);
      try { this.o.call.setBrowserStream(null); } catch (_) {}
      [L.raw, L.out].forEach((s) => s && s.getTracks().forEach((t) => { t.onended = null; try { t.stop(); } catch (_) {} }));
      if (L.video) { L.video.pause(); L.video.srcObject = null; }
      L.img = null; L.frameData = null;
      try { await window.bridge.browser.stop(); } catch (_) {}
      this.render();
    }

    /* ── tela cheia (qualquer participante, só neste computador) ── */
    toggleFullscreen() {
      if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return; }
      if (this.el.stage.hidden) return;
      this.el.stage.requestFullscreen().catch(() => this.o.toast('Não consegui abrir em tela cheia.', true));
    }

    /* ── interface (todos) ── */
    render() {
      this._render();
      if (this.o.onRender) this.o.onRender();
    }

    _render() {
      const e = this.el;
      const session = !!(this.state.active && this.inThisCall);
      const active = session && !this.hiddenLocal && this.focused;
      e.stage.hidden = !active;
      e.banner.hidden = !(session && this.hiddenLocal);
      if (session && this.hiddenLocal) e.open.textContent = 'Sessão de navegador ativa de ' + (this.isHost ? 'você' : '@' + this.state.host_login) + ' — abrir';
      e.btn.classList.toggle('on', session);
      e.btn.title = active ? (this.isHost ? 'Encerrar navegador compartilhado' : 'Navegador compartilhado (ativo)') : 'Abrir navegador compartilhado';
      if (!active) { e.video.srcObject = null; this._drawCursors(); return; }
      e.who.textContent = this.isHost ? 'Você está transmitindo' : 'Transmitido por @' + this.state.host_login;
      e.stop.hidden = !this.isHost;
      if (document.activeElement !== e.url) e.url.value = this.state.url || '';
      const stream = this.isHost ? (this.local && this.local.out) : this.o.call.browserStreamOf(this.state.host_id);
      if (e.video.srcObject !== (stream || null)) {
        e.video.srcObject = stream || null;
        const p = e.video.play(); if (p && p.catch) p.catch(() => {});
      }
      e.video.muted = this.isHost || this.o.call.deafened;
      const hasVideo = !!(stream && stream.getVideoTracks().length);
      e.wait.hidden = hasVideo;
      this._drawCursors();
    }

    _contentRect() {
      const v = this.el.video, r = v.getBoundingClientRect();
      const vw = v.videoWidth, vh = v.videoHeight;
      if (!vw || !vh || !r.width) return null;
      const s = Math.min(r.width / vw, r.height / vh);
      const w = vw * s, h = vh * s;
      return { left: r.left + (r.width - w) / 2, top: r.top + (r.height - h) / 2, w, h };
    }

    _norm(ev, clamp) {
      const c = this._contentRect();
      if (!c) return null;
      let x = (ev.clientX - c.left) / c.w, y = (ev.clientY - c.top) / c.h;
      if (clamp) { x = Math.min(1, Math.max(0, x)); y = Math.min(1, Math.max(0, y)); }
      else if (x < 0 || x > 1 || y < 0 || y > 1) return null;
      return { x, y };
    }

    _drawCursors() {
      const box = this.el.cur;
      const now = Date.now();
      for (const [id, c] of this.cursors) if (now - c.at > 3500) this.cursors.delete(id);
      const c = this._contentRect();
      if (!c || this.el.stage.hidden) { box.innerHTML = ''; return; }
      const vr = this.el.view.getBoundingClientRect();
      box.innerHTML = [...this.cursors].map(([id, k]) => {
        const col = colorOf(id);
        const left = c.left - vr.left + k.x * c.w, top = c.top - vr.top + k.y * c.h;
        return `<div class="bc" style="left:${left}px;top:${top}px;--c:${col}"><svg viewBox="0 0 16 16"><path d="M1 1l5 13 2-5 5-2z" fill="${col}" stroke="#000" stroke-width="1"/></svg><span>@${String(k.login).replace(/[<>&]/g, '')}</span></div>`;
      }).join('');
      clearTimeout(this._curT);
      if (this.cursors.size) this._curT = setTimeout(() => this._drawCursors(), 1000);
    }

    /* ── entrada: tudo que o usuário faz sobre o vídeo vira evento ── */
    _in(data) { this._sendCall('browser_input', data); }

    _mods(e) { return (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0); }

    _bindUi() {
      const e = this.el;
      const btnName = (b) => (b === 0 ? 'left' : b === 1 ? 'middle' : b === 2 ? 'right' : null);
      e.btn.onclick = () => this.toggle();
      e.stop.onclick = () => this.stop();
      e.full.onclick = () => this.toggleFullscreen();
      e.hide.onclick = () => this.hide();
      e.open.onclick = () => this.show();
      e.view.addEventListener('dblclick', (ev) => { ev.preventDefault(); this.toggleFullscreen(); });
      document.addEventListener('keydown', (ev) => {
        if (e.stage.hidden) return;
        if (ev.key === 'F11') { ev.preventDefault(); this.toggleFullscreen(); return; }
        if ((ev.key === 'f' || ev.key === 'F') && !ev.ctrlKey && !ev.metaKey && !ev.altKey) {
          const a = document.activeElement;
          // com o vídeo em foco, "f" é uma tecla da página; fora dele, é o atalho de tela cheia
          if (a === e.view || (a && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable))) return;
          ev.preventDefault();
          this.toggleFullscreen();
        }
      });
      document.addEventListener('fullscreenchange', () => {
        e.stage.classList.toggle('fs', document.fullscreenElement === e.stage);
        setTimeout(() => this._drawCursors(), 150);
      });
      e.back.onclick = () => this._sendCall('browser_nav', { action: 'back' });
      e.fwd.onclick = () => this._sendCall('browser_nav', { action: 'forward' });
      e.reload.onclick = () => this._sendCall('browser_nav', { action: 'reload' });
      e.form.onsubmit = (ev) => {
        ev.preventDefault();
        const u = normalizeUrl(e.url.value);
        if (!u) return;
        this._sendCall('browser_nav', { action: 'go', url: u });
        e.view.focus();
      };

      e.view.addEventListener('mousemove', (ev) => {
        const now = performance.now();
        if (now - this._lastMove < 40) return;
        const p = this._norm(ev);
        if (!p) return;
        this._lastMove = now;
        this._in({ t: 'move', x: p.x, y: p.y, mods: this._mods(ev) });
      });
      e.view.addEventListener('mousedown', (ev) => {
        const b = btnName(ev.button), p = this._norm(ev);
        e.view.focus();
        ev.preventDefault();
        if (!b || !p) return;
        this.heldButton = b;
        this._in({ t: 'down', x: p.x, y: p.y, button: b, mods: this._mods(ev) });
      });
      window.addEventListener('mouseup', (ev) => {
        const b = btnName(ev.button);
        if (!this.heldButton || b !== this.heldButton) return;
        this.heldButton = null;
        const p = this._norm(ev, true);
        if (p) this._in({ t: 'up', x: p.x, y: p.y, button: b, mods: this._mods(ev) });
      });
      e.view.addEventListener('wheel', (ev) => {
        const p = this._norm(ev);
        if (!p) return;
        ev.preventDefault();
        this._in({ t: 'wheel', x: p.x, y: p.y, dx: Math.max(-5000, Math.min(5000, ev.deltaX)), dy: Math.max(-5000, Math.min(5000, ev.deltaY)), mods: this._mods(ev) });
      }, { passive: false });
      e.view.addEventListener('contextmenu', (ev) => ev.preventDefault());
      const key = (type) => (ev) => {
        if (!ev.key || ev.key === 'Unidentified') return;
        ev.preventDefault();
        const printable = ev.key.length === 1 && !ev.ctrlKey && !ev.metaKey;
        this._in({ t: type, key: ev.key.slice(0, 32), code: (ev.code || '').slice(0, 32), text: printable && type === 'key_down' ? ev.key : '', mods: this._mods(ev) });
      };
      e.view.addEventListener('keydown', key('key_down'));
      e.view.addEventListener('keyup', key('key_up'));
      e.view.addEventListener('blur', () => {
        if (this.heldButton) { const b = this.heldButton; this.heldButton = null; this._in({ t: 'up', x: 0.5, y: 0.5, button: b, mods: 0 }); }
      });
      window.addEventListener('resize', () => this._drawCursors());
    }
  }

  window.BrowserShare = BrowserShare;
})();

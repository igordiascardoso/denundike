/* call.js — chamadas de voz, vídeo e tela (malha WebRTC, sinalização pelo WS) */
(function () {
  'use strict';

  const FALLBACK_ICE = [{ urls: ['stun:stun.l.google.com:19302'] }];
  const SPEAK_ON = 0.035;
  const SPEAK_HOLD_MS = 450;
  const BITRATE = { '720_30': 2500000, '720_60': 4000000, '1080_30': 6000000, '1080_60': 9000000, '1440_30': 10000000, '1440_60': 14000000 };
  const QKEY = 'dk_quality';
  function loadQuality() {
    try {
      const q = JSON.parse(localStorage.getItem(QKEY) || 'null');
      if (q && [720, 1080, 1440].includes(+q.h) && [30, 60].includes(+q.fps)) return { h: +q.h, fps: +q.fps };
    } catch (_) {}
    return { h: 1080, fps: 30 };
  }

  class CallManager {
    /**
     * opts: { me:{id,login}, send(obj), getIce():Promise<RTCIceServer[]>,
     *         onChange(), toast(msg,isErr), pickScreen():Promise<{id,audio}|null> }
     */
    constructor(opts) {
      this.o = opts;
      this.channelId = null;
      this.peers = new Map(); // userId → peer
      this.mic = null;
      this.camTrack = null;
      this.camStream = null;
      this.screenStream = null;
      this.browserStream = null;
      this.muted = false;
      this.deafened = false;
      this._mutedBeforeDeaf = false;
      this.speakingSelf = false;
      this._ctx = null;
      this._selfAnalyser = null;
      this._speakTimer = null;
      this._ice = null;
      this.quality = loadQuality();
      this.sendStats = null;
      this._strain = 0;
      this._lastWarn = 0;
    }

    static bitrate(q) { return BITRATE[q.h + '_' + q.fps] || 6000000; }
    get inCall() { return !!this.channelId; }
    get cameraOn() { return !!this.camTrack; }
    get screenOn() { return !!this.screenStream; }
    get browserOn() { return !!this.browserStream; }

    _changed() { try { this.o.onChange(); } catch (_) {} }

    /* ── entrar / sair ── */
    async join(channelId) {
      if (this.channelId === channelId) return;
      if (this.channelId) this.leave();
      this.channelId = channelId;

      try { this._ctx = this._ctx || new (window.AudioContext || window.webkitAudioContext)(); } catch (_) {}
      if (this._ctx && this._ctx.state === 'suspended') this._ctx.resume().catch(() => {});

      this.camStream = new MediaStream();
      try {
        const s = await navigator.mediaDevices.getUserMedia({
          audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true },
        });
        this.mic = s.getAudioTracks()[0];
        this.mic.enabled = !this.muted;
        this.camStream.addTrack(this.mic);
        this._watchSelf();
      } catch (e) {
        this.mic = null;
        this.o.toast(
          e && e.name === 'NotFoundError'
            ? 'Nenhum microfone encontrado — você entra só ouvindo.'
            : 'Sem acesso ao microfone — você entra só ouvindo.',
          true
        );
      }
      if (this.channelId !== channelId) return; // saiu enquanto pedia permissão

      try { this._ice = this._ice || (await this.o.getIce()); } catch (_) { this._ice = FALLBACK_ICE; }
      if (this.channelId !== channelId) return;

      this.o.send({ type: 'voice_join', channel_id: channelId });
      this._startSpeakLoop();
      clearInterval(this._statsT); this._statsT = setInterval(() => this._pollStats(), 3000);
      this._changed();
    }

    leave() {
      if (!this.channelId) return;
      this.o.send({ type: 'voice_leave' });
      this._teardown();
    }

    _teardown() {
      for (const id of [...this.peers.keys()]) this._removePeer(id);
      [this.mic, this.camTrack].forEach(t => { try { t && t.stop(); } catch (_) {} });
      if (this.screenStream) this.screenStream.getTracks().forEach(t => { try { t.stop(); } catch (_) {} });
      this.mic = null; this.camTrack = null; this.camStream = null; this.screenStream = null; this.browserStream = null;
      this._selfAnalyser = null;
      clearInterval(this._speakTimer); this._speakTimer = null;
      clearInterval(this._statsT); this._statsT = null; this.sendStats = null; this._prev = null;
      this.speakingSelf = false;
      this.channelId = null;
      try { this.o.onLeave && this.o.onLeave(); } catch (_) {}
      this._changed();
    }

    /* ── mutar / ensurdecer ── */
    setMuted(m) {
      this.muted = !!m;
      if (this.mic) this.mic.enabled = !this.muted;
      if (!this.muted && this.deafened) this.deafened = false;
      this._sendMedia();
      this._changed();
    }

    setDeafened(d) {
      d = !!d;
      if (d === this.deafened) return;
      this.deafened = d;
      if (d) { this._mutedBeforeDeaf = this.muted; this.muted = true; }
      else { this.muted = this._mutedBeforeDeaf; }
      if (this.mic) this.mic.enabled = !this.muted;
      this._sendMedia();
      this._changed();
    }

    /* ── câmera ── */
    async setCamera(on) {
      if (!this.inCall) return;
      if (on === this.cameraOn) return;
      if (on) {
        let stream;
        try {
          stream = await navigator.mediaDevices.getUserMedia({
            video: { width: { ideal: 1920 }, height: { ideal: 1080 }, frameRate: { ideal: 30 } },
          });
        } catch (e) {
          this.o.toast(e && e.name === 'NotFoundError' ? 'Nenhuma câmera encontrada.' : 'Sem acesso à câmera.', true);
          return;
        }
        if (!this.inCall) { stream.getTracks().forEach(t => t.stop()); return; }
        this.camTrack = stream.getVideoTracks()[0];
        this.camTrack.onended = () => this.setCamera(false);
        this.camStream.addTrack(this.camTrack);
        for (const p of this.peers.values()) this._attachCam(p);
      } else {
        for (const p of this.peers.values()) {
          if (p.camSender) { try { p.pc.removeTrack(p.camSender); } catch (_) {} p.camSender = null; }
        }
        const t = this.camTrack;
        this.camTrack = null;
        try { this.camStream.removeTrack(t); } catch (_) {}
        t.onended = null; t.stop();
      }
      this._sendMedia();
      this._changed();
    }

    /* ── compartilhar tela ── */
    async setScreen(on) {
      if (!this.inCall) return;
      if (on === this.screenOn) return;
      if (on) {
        let choice;
        try { choice = await this.o.pickScreen(); } catch (_) { choice = null; }
        if (!choice) return;
        let stream;
        try {
          stream = await navigator.mediaDevices.getDisplayMedia({
            video: { frameRate: { ideal: this.quality.fps } },
            audio: !!choice.audio,
          });
        } catch (e) {
          this.o.toast('Não consegui capturar a tela.', true);
          return;
        }
        if (!this.inCall) { stream.getTracks().forEach(t => t.stop()); return; }
        this.screenStream = stream;
        const v = stream.getVideoTracks()[0];
        if (v) { v.onended = () => this.setScreen(false); v.contentHint = 'detail'; }
        for (const p of this.peers.values()) this._attachScreen(p);
      } else {
        const s = this.screenStream;
        this.screenStream = null;
        for (const p of this.peers.values()) {
          (p.screenSenders || []).forEach(snd => { try { p.pc.removeTrack(snd); } catch (_) {} });
          p.screenSenders = [];
        }
        s.getTracks().forEach(t => { t.onended = null; t.stop(); });
      }
      this._sendMedia();
      this._changed();
    }

    /* ── navegador compartilhado (faixa extra, só do anfitrião) ── */
    setBrowserStream(stream) {
      if (stream && !this.inCall) return;
      for (const p of this.peers.values()) {
        (p.browserSenders || []).forEach(snd => { try { p.pc.removeTrack(snd); } catch (_) {} });
        p.browserSenders = [];
      }
      this.browserStream = stream || null;
      if (stream) for (const p of this.peers.values()) this._attachBrowser(p);
      this._sendMedia();
      this._changed();
    }

    browserStreamOf(userId) {
      const p = this.peers.get(userId);
      if (!p || !p.media || !p.media.browser) return null;
      return p.streams.get(p.media.browser) || null;
    }

    _sendMedia() {
      if (!this.inCall) return;
      this.o.send({
        type: 'voice_media', channel_id: this.channelId,
        data: {
          muted: this.muted, deafened: this.deafened, cam: this.cameraOn,
          screen: this.screenStream ? this.screenStream.id : null,
          browser: this.browserStream ? this.browserStream.id : null,
        },
      });
    }

    /* ── mensagens do servidor ── */
    handleWS(ev) {
      switch (ev.type) {
        case 'voice_peers':
          if (ev.channel_id !== this.channelId) return;
          this._syncPeers(ev.peers || []);
          this._sendMedia();
          break;
        case 'voice_joined':
          if (ev.channel_id !== this.channelId) return;
          this._addPeer({ user_id: ev.user_id, login: ev.login }, false);
          this._sendMedia();
          this._changed();
          break;
        case 'voice_left':
          if (ev.channel_id !== this.channelId) return;
          this._removePeer(ev.user_id);
          this._changed();
          break;
        case 'signal':
          if (ev.channel_id !== this.channelId) return;
          this._onSignal(ev);
          break;
        case 'voice_media': {
          if (ev.channel_id !== this.channelId) return;
          const p = this.peers.get(ev.user_id);
          if (p) { p.media = ev.data || {}; this._changed(); }
          break;
        }
        case 'voice_error': {
          const map = {
            'room full': 'A sala está cheia (máximo de 6 pessoas).',
            'forbidden': 'Você não faz parte desse grupo.',
            'not a voice channel': 'Esse canal não é de voz.',
            'channel not found': 'Esse canal não existe mais.',
          };
          this.o.toast(map[ev.error] || 'Não foi possível entrar na chamada.', true);
          if (this.channelId) this._teardown();
          break;
        }
      }
    }

    /** O WS reconectou: refaz a entrada na sala (as conexões antigas morreram). */
    onWSReopen() {
      if (!this.channelId) return;
      for (const id of [...this.peers.keys()]) this._removePeer(id);
      this.o.send({ type: 'voice_join', channel_id: this.channelId });
      this._changed();
    }

    _syncPeers(list) {
      const keep = new Set(list.map(p => p.user_id));
      for (const id of [...this.peers.keys()]) if (!keep.has(id)) this._removePeer(id);
      list.forEach(u => this._addPeer(u, true));
      this._changed();
    }

    /* ── pares ── */
    _addPeer(user, initiator) {
      if (user.user_id === this.o.me.id) return null;
      const existing = this.peers.get(user.user_id);
      if (existing) { if (user.login) existing.login = user.login; return existing; }

      const pc = new RTCPeerConnection({ iceServers: this._ice || FALLBACK_ICE });
      const p = {
        id: user.user_id, login: user.login || '…', pc,
        polite: this.o.me.id < user.user_id,
        makingOffer: false, ignoreOffer: false, suppress: !initiator,
        streams: new Map(), media: {}, speaking: false, _lastVoice: 0, analysers: new Map(),
        camSender: null, screenSenders: [], browserSenders: [], q: Promise.resolve(), failedOnce: false,
      };
      this.peers.set(p.id, p);

      pc.onicecandidate = e => {
        if (e.candidate) this._sig(p, 'ice', e.candidate.toJSON ? e.candidate.toJSON() : e.candidate);
      };
      pc.onnegotiationneeded = async () => {
        if (p.suppress) return;
        try {
          p.makingOffer = true;
          await pc.setLocalDescription();
          this._sig(p, 'offer', pc.localDescription.toJSON ? pc.localDescription.toJSON() : pc.localDescription);
        } catch (_) {
          /* renegociação descartada por colisão */
        } finally {
          p.makingOffer = false;
        }
      };
      pc.ontrack = e => {
        const stream = (e.streams && e.streams[0]) || new MediaStream([e.track]);
        p.streams.set(stream.id, stream);
        const refresh = () => this._changed();
        stream.onremovetrack = refresh;
        stream.onaddtrack = refresh;
        e.track.onunmute = refresh;
        e.track.onended = refresh;
        if (e.track.kind === 'audio') this._watchPeerStream(p, stream);
        this._changed();
      };
      pc.onconnectionstatechange = () => {
        if (pc.connectionState === 'failed') {
          if (!p.failedOnce) { p.failedOnce = true; try { pc.restartIce(); } catch (_) {} }
          else this.o.toast('Conexão com @' + p.login + ' caiu.', true);
        }
        this._changed();
      };

      // mídia local
      if (this.mic) pc.addTrack(this.mic, this.camStream);
      else if (initiator) pc.addTransceiver('audio', { direction: 'recvonly' });
      if (this.camTrack) this._attachCam(p);
      if (this.screenStream) this._attachScreen(p);
      if (this.browserStream) this._attachBrowser(p);
      return p;
    }

    _attachCam(p) {
      if (!this.camTrack || p.camSender) return;
      p.camSender = p.pc.addTrack(this.camTrack, this.camStream);
      this._tune(p.camSender, 3500000);
    }

    _attachScreen(p) {
      if (!this.screenStream) return;
      p.screenSenders = this.screenStream.getTracks().map(t => t.kind === 'video'
        ? this._addHdVideo(p, t, this.screenStream)
        : p.pc.addTrack(t, this.screenStream));
    }

    _attachBrowser(p) {
      if (!this.browserStream) return;
      p.browserSenders = this.browserStream.getTracks().map(t => t.kind === 'video'
        ? this._addHdVideo(p, t, this.browserStream)
        : p.pc.addTrack(t, this.browserStream));
    }

    /** vídeo de tela/navegador: resolução nativa, bitrate alto, sem reduzir resolução sob pressão, codec VP9/H264 */
    _addHdVideo(p, track, stream) {
      const enc = this._hdEncoding(track);
      const tr = p.pc.addTransceiver(track, { direction: 'sendonly', streams: [stream], sendEncodings: [enc] });
      this._preferCodecs(tr);
      const snd = tr.sender;
      this._tuneHd(snd, track);
      setTimeout(() => this._tuneHd(snd, track), 1500);
      setTimeout(() => this._tuneHd(snd, track), 5000);
      return snd;
    }

    _hdEncoding(track) {
      const q = this.quality;
      const st = (track && track.getSettings && track.getSettings()) || {};
      const scale = st.height ? Math.max(1, Math.round((st.height / q.h) * 100) / 100) : 1;
      return { maxBitrate: CallManager.bitrate(q), maxFramerate: q.fps, scaleResolutionDownBy: scale };
    }

    _preferCodecs(tr) {
      try {
        const caps = RTCRtpSender.getCapabilities('video');
        if (!caps || !tr.setCodecPreferences) return;
        const rank = m => (/vp9/i.test(m) ? 0 : /h264/i.test(m) ? 1 : /vp8/i.test(m) ? 2 : /av1/i.test(m) ? 3 : 4);
        tr.setCodecPreferences([...caps.codecs].sort((a, b) => rank(a.mimeType) - rank(b.mimeType)));
      } catch (_) {}
    }

    /** nitidez antes de tudo: não reduz a resolução sob pressão de banda */
    _tuneHd(sender, track) {
      try {
        const params = sender.getParameters();
        params.encodings = params.encodings && params.encodings.length ? params.encodings : [{}];
        Object.assign(params.encodings[0], this._hdEncoding(track || sender.track));
        params.degradationPreference = 'maintain-resolution';
        sender.setParameters(params).catch(() => {});
      } catch (_) {}
    }

    /* ── qualidade escolhida pelo usuário (tela e navegador) ── */
    setQuality(q) {
      this.quality = { h: [720, 1080, 1440].includes(+q.h) ? +q.h : 1080, fps: +q.fps === 60 ? 60 : 30 };
      try { localStorage.setItem(QKEY, JSON.stringify(this.quality)); } catch (_) {}
      for (const p of this.peers.values()) {
        [...(p.screenSenders || []), ...(p.browserSenders || [])].forEach(snd => {
          if (snd.track && snd.track.kind === 'video') this._tuneHd(snd, snd.track);
        });
      }
      const sv = this.screenStream && this.screenStream.getVideoTracks()[0];
      if (sv) sv.applyConstraints({ frameRate: { ideal: this.quality.fps } }).catch(() => {});
      this._strain = 0;
      try { this.o.onQuality && this.o.onQuality(this.quality); } catch (_) {}
      this._changed();
    }

    /** próximo patamar abaixo, ou null se já está no mínimo */
    suggestLower() {
      const { h, fps } = this.quality;
      if (fps === 60) return { h, fps: 30 };
      if (h === 1440) return { h: 1080, fps };
      if (h === 1080) return { h: 720, fps };
      return null;
    }

    /** avalia uma amostra de envio e avisa se está engasgando (2 amostras seguidas) */
    _evalStrain(s) {
      const bad = s.limit !== 'none' && s.fps > 0 && s.fps < this.quality.fps * 0.75;
      this._strain = bad ? this._strain + 1 : 0;
      if (this._strain >= 2 && Date.now() - this._lastWarn > 60000) {
        const sug = this.suggestLower();
        if (sug) {
          this._lastWarn = Date.now();
          try { this.o.onStrain && this.o.onStrain({ reason: s.limit, fps: s.fps, suggestion: sug }); } catch (_) {}
        }
      }
    }

    async _pollStats() {
      const snds = [];
      for (const p of this.peers.values()) {
        [...(p.screenSenders || []), ...(p.browserSenders || [])].forEach(s => { if (s.track && s.track.kind === 'video') snds.push(s); });
      }
      if (!snds.length) { this.sendStats = null; this._prev = null; return; }
      let bytes = 0, fps = 0, w = 0, h = 0, limit = 'none';
      for (const s of snds) {
        try {
          (await s.getStats()).forEach(r => {
            if (r.type === 'outbound-rtp' && r.kind === 'video') {
              bytes += r.bytesSent || 0;
              if ((r.framesPerSecond || 0) > fps) fps = r.framesPerSecond;
              w = r.frameWidth || w; h = r.frameHeight || h;
              if (r.qualityLimitationReason && r.qualityLimitationReason !== 'none') limit = r.qualityLimitationReason;
            }
          });
        } catch (_) {}
      }
      const now = performance.now();
      const bps = this._prev ? Math.max(0, (bytes - this._prev.bytes) * 8 / ((now - this._prev.t) / 1000)) : 0;
      this._prev = { bytes, t: now };
      this.sendStats = { bps, fps, w, h, limit, peers: snds.length };
      this._evalStrain(this.sendStats);
    }

    _tune(sender, maxBitrate) {
      try {
        const params = sender.getParameters();
        params.encodings = params.encodings && params.encodings.length ? params.encodings : [{}];
        params.encodings[0].maxBitrate = maxBitrate;
        sender.setParameters(params).catch(() => {});
      } catch (_) {}
    }

    _removePeer(id) {
      const p = this.peers.get(id);
      if (!p) return;
      this.peers.delete(id);
      try { p.pc.ontrack = null; p.pc.onicecandidate = null; p.pc.onnegotiationneeded = null; p.pc.close(); } catch (_) {}
    }

    _sig(p, kind, data) {
      this.o.send({ type: 'signal', channel_id: this.channelId, to: p.id, kind, data });
    }

    _onSignal(ev) {
      let p = this.peers.get(ev.from);
      if (!p) p = this._addPeer({ user_id: ev.from, login: '…' }, false);
      if (!p) return;
      // um par por vez: um candidato ICE não pode passar na frente da descrição que o precede
      p.q = p.q.then(() => this._handleSignal(p, ev));
    }

    async _handleSignal(p, ev) {
      const pc = p.pc;
      try {
        if (ev.kind === 'ice') {
          try { await pc.addIceCandidate(ev.data); }
          catch (e) { if (!p.ignoreOffer) throw e; }
          return;
        }
        const desc = ev.data;
        const collision = desc.type === 'offer' && (p.makingOffer || pc.signalingState !== 'stable');
        p.ignoreOffer = !p.polite && collision;
        if (p.ignoreOffer) return;
        await pc.setRemoteDescription(desc);
        if (desc.type === 'offer') {
          p.suppress = false;
          await pc.setLocalDescription();
          this._sig(p, 'answer', pc.localDescription.toJSON ? pc.localDescription.toJSON() : pc.localDescription);
        }
      } catch (e) {
        console.warn('signal', ev.kind, e && e.message);
      }
    }

    /* ── quem está falando ── */
    _analyser(stream) {
      if (!this._ctx) return null;
      try {
        const src = this._ctx.createMediaStreamSource(stream);
        const an = this._ctx.createAnalyser();
        an.fftSize = 512;
        src.connect(an);
        return { an, buf: new Uint8Array(an.fftSize) };
      } catch (_) { return null; }
    }

    _watchSelf() {
      if (!this.mic) return;
      this._selfAnalyser = this._analyser(new MediaStream([this.mic]));
    }

    _watchPeerStream(p, stream) {
      if (p.analysers.has(stream.id)) return;
      const a = this._analyser(stream);
      if (a) p.analysers.set(stream.id, a);
    }

    _level(a) {
      a.an.getByteTimeDomainData(a.buf);
      let sum = 0;
      for (let i = 0; i < a.buf.length; i++) { const v = (a.buf[i] - 128) / 128; sum += v * v; }
      return Math.sqrt(sum / a.buf.length);
    }

    _startSpeakLoop() {
      clearInterval(this._speakTimer);
      this._speakTimer = setInterval(() => {
        const now = performance.now();
        let dirty = false;
        const selfOn = !!(this._selfAnalyser && !this.muted && this._level(this._selfAnalyser) > SPEAK_ON);
        if (selfOn) this._selfLast = now;
        const selfSpeaking = now - (this._selfLast || 0) < SPEAK_HOLD_MS && !this.muted;
        if (selfSpeaking !== this.speakingSelf) { this.speakingSelf = selfSpeaking; dirty = true; }
        for (const p of this.peers.values()) {
          let loud = false;
          for (const [sid, a] of p.analysers) if (sid !== (p.media && p.media.browser) && this._level(a) > SPEAK_ON) loud = true;
          if (loud) p._lastVoice = now;
          const sp = now - p._lastVoice < SPEAK_HOLD_MS && !p.media.muted;
          if (sp !== p.speaking) { p.speaking = sp; dirty = true; }
        }
        if (dirty) this._changed();
      }, 140);
    }

    /* ── o que a interface desenha ── */
    tiles() {
      if (!this.inCall) return [];
      const out = [];
      out.push({
        key: 'self', self: true, userId: this.o.me.id, login: this.o.me.login,
        stream: this.camStream, hasVideo: this.cameraOn,
        muted: this.muted, deafened: this.deafened, speaking: this.speakingSelf, state: 'connected',
      });
      if (this.screenStream) {
        out.push({
          key: 'self:screen', self: true, screen: true, userId: this.o.me.id, login: this.o.me.login,
          stream: this.screenStream, hasVideo: true, state: 'connected',
        });
      }
      const sorted = [...this.peers.values()].sort((a, b) => a.login.localeCompare(b.login));
      for (const p of sorted) {
        const screenId = p.media && p.media.screen;
        let camStream = null;
        for (const [sid, s] of p.streams) {
          if (p.media && p.media.browser && sid === p.media.browser) continue;
          if (screenId && sid === screenId) {
            out.push({
              key: p.id + ':screen', userId: p.id, login: p.login, screen: true,
              stream: s, hasVideo: s.getVideoTracks().length > 0, state: p.pc.connectionState,
            });
          } else if (!camStream) {
            camStream = s;
          }
        }
        out.push({
          key: p.id, userId: p.id, login: p.login, stream: camStream,
          hasVideo: !!(camStream && p.media && p.media.cam && camStream.getVideoTracks().length > 0),
          muted: !!(p.media && p.media.muted), deafened: !!(p.media && p.media.deafened),
          speaking: p.speaking, state: p.pc.connectionState,
        });
      }
      return out;
    }
  }

  window.CallManager = CallManager;
})();

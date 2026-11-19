/* call.js — chamadas de voz, vídeo e tela (malha WebRTC, sinalização pelo WS) */
(function () {
  'use strict';

  const FALLBACK_ICE = [{ urls: ['stun:stun.l.google.com:19302'] }];
  const SPEAK_ON = 0.035;
  const SPEAK_HOLD_MS = 450;

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
      this.muted = false;
      this.deafened = false;
      this._mutedBeforeDeaf = false;
      this.speakingSelf = false;
      this._ctx = null;
      this._selfAnalyser = null;
      this._speakTimer = null;
      this._ice = null;
    }

    get inCall() { return !!this.channelId; }
    get cameraOn() { return !!this.camTrack; }
    get screenOn() { return !!this.screenStream; }

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
      this.mic = null; this.camTrack = null; this.camStream = null; this.screenStream = null;
      this._selfAnalyser = null;
      clearInterval(this._speakTimer); this._speakTimer = null;
      this.speakingSelf = false;
      this.channelId = null;
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
            video: { width: { ideal: 1280 }, height: { ideal: 720 }, frameRate: { ideal: 30 } },
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
            video: { frameRate: { ideal: 30 } },
            audio: !!choice.audio,
          });
        } catch (e) {
          this.o.toast('Não consegui capturar a tela.', true);
          return;
        }
        if (!this.inCall) { stream.getTracks().forEach(t => t.stop()); return; }
        this.screenStream = stream;
        const v = stream.getVideoTracks()[0];
        if (v) v.onended = () => this.setScreen(false);
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

    _sendMedia() {
      if (!this.inCall) return;
      this.o.send({
        type: 'voice_media', channel_id: this.channelId,
        data: {
          muted: this.muted, deafened: this.deafened, cam: this.cameraOn,
          screen: this.screenStream ? this.screenStream.id : null,
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
        camSender: null, screenSenders: [], q: Promise.resolve(), failedOnce: false,
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
      return p;
    }

    _attachCam(p) {
      if (!this.camTrack || p.camSender) return;
      p.camSender = p.pc.addTrack(this.camTrack, this.camStream);
      this._tune(p.camSender, 1500000);
    }

    _attachScreen(p) {
      if (!this.screenStream) return;
      p.screenSenders = this.screenStream.getTracks().map(t => {
        const s = p.pc.addTrack(t, this.screenStream);
        if (t.kind === 'video') this._tune(s, 4000000);
        return s;
      });
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
          for (const a of p.analysers.values()) if (this._level(a) > SPEAK_ON) loud = true;
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

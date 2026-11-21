/* Cadeia do microfone: redução de ruído por IA (RNNoise) com intensidade, corte de ruído (gate) e volume.
   Tudo ajustável na hora em Configurações > Áudio. Se algo falhar, a chamada segue com o microfone puro. */
(() => {
  const read = (k, d) => { try { const v = localStorage.getItem(k); return v === null ? d : v; } catch (_) { return d; } };
  const num = (k, d) => { const v = parseFloat(read(k, d)); return isFinite(v) ? Math.min(1, Math.max(0, v)) : d; };

  // 'off' = sem tratamento · 'basic' = redução do próprio Chrome · 'ai' = RNNoise
  window.dkNs = {
    mode: () => { const m = read('dk_ns_mode', 'ai'); return m === 'off' || m === 'basic' ? m : 'ai'; },
    amount: () => num('dk_ns_amt', 1),     // quanto do som tratado entra na mistura (0 a 1); 1 = só o som tratado, sem vazar ruído
    gate: () => num('dk_ns_gate', 0.35),
    // opções com que o microfone é aberto: na IA o Chrome não trata o ruído nem ajusta o ganho sozinho
    constraints: () => { const m = window.dkNs.mode(); return { echoCancellation: true, noiseSuppression: m === 'basic', autoGainControl: m !== 'ai' }; },   // 0 = corte desligado; quanto maior, mais alto o limite
  };

  let wasmCache = null;
  async function loadWasm() {
    if (wasmCache) return wasmCache;
    const simd = WebAssembly.validate(new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0, 1, 5, 1, 96, 0, 1, 123, 3, 2, 1, 0, 10, 10, 1, 8, 0, 65, 0, 253, 15, 253, 98, 11]));
    const url = new URL('vendor/rnnoise/' + (simd ? 'rnnoise_simd.wasm' : 'rnnoise.wasm'), document.baseURI).href;
    wasmCache = await (await fetch(url)).arrayBuffer();
    return wasmCache;
  }

  class MicChain {
    constructor(raw, volume) {
      this.raw = raw;
      this.vol = volume;
      this.track = raw;     // trilha que vai para a chamada (a original, até a cadeia ficar pronta)
      this.ctx = null; this.ai = null; this.timer = null; this.dead = false;
    }

    async init() {
      const ctx = this.ctx = new AudioContext({ sampleRate: 48000 }); // o RNNoise trabalha em 48 kHz
      if (ctx.state === 'suspended') await ctx.resume().catch(() => {});
      const src = ctx.createMediaStreamSource(new MediaStream([this.raw]));
      this.dry = ctx.createGain();
      this.wet = ctx.createGain();
      this.sum = ctx.createGain();
      this.gateG = ctx.createGain();
      this.volG = ctx.createGain();
      const dst = ctx.createMediaStreamDestination();
      // o caminho "seco" espera o atraso do RNNoise (10 ms) para as duas vozes somarem alinhadas
      const delay = ctx.createDelay(0.1); delay.delayTime.value = 0.01;
      src.connect(delay); delay.connect(this.dry); this.dry.connect(this.sum);
      try {
        await ctx.audioWorklet.addModule(new URL('vendor/rnnoise/workletProcessor.js', document.baseURI).href);
        const wasmBinary = await loadWasm();
        this.ai = new AudioWorkletNode(ctx, '@sapphi-red/web-noise-suppressor/rnnoise', { processorOptions: { maxChannels: 1, wasmBinary } });
        src.connect(this.ai); this.ai.connect(this.wet); this.wet.connect(this.sum);
      } catch (e) { this.ai = null; }
      this.an = ctx.createAnalyser(); this.an.fftSize = 1024;
      this.buf = new Float32Array(this.an.fftSize);
      this.sum.connect(this.an);
      this.sum.connect(this.gateG); this.gateG.connect(this.volG); this.volG.connect(dst);
      this.track = dst.stream.getAudioTracks()[0];
      this.apply();
      this._startGate();
      return this;
    }

    /** relê os ajustes salvos e aplica na cadeia */
    apply() {
      if (!this.ctx) return;
      const mode = window.dkNs.mode(), k = window.dkNs.amount();
      const useAi = mode === 'ai' && !!this.ai;
      this.dry.gain.value = useAi ? 1 - k : 1;
      this.wet.gain.value = useAi ? k : 0;
      // com a IA ligada o Chrome não precisa tratar o ruído por conta própria
      // o ganho automático do navegador sobe o volume do fundo quando você para de falar: só fica ligado no modo "navegador"
      try { this.raw.applyConstraints({ noiseSuppression: mode === 'basic', autoGainControl: mode !== 'ai' }).catch(() => {}); } catch (_) {}
      this.volG.gain.value = this.vol;
    }

    setVolume(v) { this.vol = v; if (this.volG) this.volG.gain.value = v; }

    /** nível atual (dB) do som já tratado, antes do corte; serve ao medidor das configurações */
    level() {
      if (!this.an || this.dead) return -100;
      this.an.getFloatTimeDomainData(this.buf);
      let sum = 0;
      for (let i = 0; i < this.buf.length; i++) sum += this.buf[i] * this.buf[i];
      return 20 * Math.log10(Math.sqrt(sum / this.buf.length) + 1e-9);
    }

    /** corte de ruído: abaixo do limite o microfone fecha (com uma pausa curta para não cortar o fim das palavras) */
    _startGate() {
      let holdUntil = 0, open = true;
      this.timer = setInterval(() => {
        if (this.dead || !this.ctx) return;
        const g = window.dkNs.gate();
        const now = this.ctx.currentTime;
        if (g <= 0) { if (!open) { open = true; this.gateG.gain.setTargetAtTime(1, now, 0.01); } return; }
        this.an.getFloatTimeDomainData(this.buf);
        let sum = 0;
        for (let i = 0; i < this.buf.length; i++) sum += this.buf[i] * this.buf[i];
        const db = 20 * Math.log10(Math.sqrt(sum / this.buf.length) + 1e-9);
        const limit = -70 + g * 45;      // 0% desligado · 100% = -25 dB
        if (db > limit) holdUntil = performance.now() + 300;
        const want = performance.now() < holdUntil;
        if (want !== open) { open = want; this.gateG.gain.setTargetAtTime(want ? 1 : 0, now, want ? 0.008 : 0.06); }
      }, 20);
    }

    destroy() {
      this.dead = true;
      clearInterval(this.timer); this.timer = null;
      try { this.ai && this.ai.port.postMessage('destroy'); } catch (_) {}
      try { this.track && this.track !== this.raw && this.track.stop(); } catch (_) {}
      try { this.raw && this.raw.stop(); } catch (_) {}
      try { this.ctx && this.ctx.close(); } catch (_) {}
      this.ctx = null;
    }
  }

  window.MicChain = MicChain;
})();

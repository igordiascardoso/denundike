/* Sons da chamada (gerados na hora, sem arquivos) e volumes gerais de saída e de microfone. */
(() => {
  const num = (k, d) => { try { const v = parseFloat(localStorage.getItem(k)); return isFinite(v) ? Math.min(2, Math.max(0, v)) : d; } catch (_) { return d; } };
  window.dkOutVol = () => num('dk_out_vol', 1);
  window.dkMicVol = () => num('dk_mic_vol', 1);

  // [frequência em Hz, início em s, duração em s]
  const TONES = {
    join:     [[523, 0, 0.12], [784, 0.11, 0.18]],                      // sobe: alguém entrou
    leave:    [[659, 0, 0.12], [392, 0.11, 0.2]],                       // desce: alguém saiu
    mute:     [[330, 0, 0.07], [220, 0.07, 0.1]],                       // microfone desligado
    unmute:   [[330, 0, 0.07], [494, 0.07, 0.1]],                       // microfone ligado
    deafen:   [[392, 0, 0.07], [294, 0.07, 0.07], [196, 0.14, 0.12]],   // fone desligado (também muta)
    undeafen: [[196, 0, 0.07], [294, 0.07, 0.07], [392, 0.14, 0.12]],
  };
  let ctx = null;
  function sink() {
    try { const id = localStorage.getItem('dk_out_device') || ''; if (ctx && ctx.setSinkId) ctx.setSinkId(id).catch(() => {}); } catch (_) {}
  }
  function context() {
    if (!ctx) {
      try { ctx = new (window.AudioContext || window.webkitAudioContext)(); } catch (_) { return null; }
      sink();
    }
    if (ctx.state === 'suspended') ctx.resume().catch(() => {});
    return ctx;
  }
  function play(name) {
    const seq = TONES[name], vol = window.dkOutVol();
    if (!seq || vol <= 0) return;
    const c = context();
    if (!c) return;
    const t0 = c.currentTime + 0.01, peak = Math.min(0.5, 0.16 * vol);
    for (const [f, at, dur] of seq) {
      const o = c.createOscillator(), g = c.createGain();
      o.type = 'sine'; o.frequency.value = f;
      g.gain.setValueAtTime(0.0001, t0 + at);
      g.gain.exponentialRampToValueAtTime(peak, t0 + at + 0.015);
      g.gain.exponentialRampToValueAtTime(0.0001, t0 + at + dur);
      o.connect(g); g.connect(c.destination);
      o.start(t0 + at); o.stop(t0 + at + dur + 0.02);
    }
  }
  window.sfx = { play, sink };
})();

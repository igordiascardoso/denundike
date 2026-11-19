/* view.js — o que cada pessoa vê no centro, barras que somem, volume individual */
(function () {
  'use strict';

  /* ── volume individual (só neste computador) ── */
  const VKEY = 'dk_vol';
  let vols = {};
  try { vols = JSON.parse(localStorage.getItem(VKEY) || '{}') || {}; } catch (_) { vols = {}; }
  const getVol = (k) => {
    const x = vols[k];
    return { v: x && typeof x.v === 'number' ? Math.min(1, Math.max(0, x.v)) : 1, m: !!(x && x.m) };
  };
  function setVol(k, patch) {
    vols[k] = Object.assign(getVol(k), patch);
    try { localStorage.setItem(VKEY, JSON.stringify(vols)); } catch (_) {}
    applyAllVolumes();
  }
  window.getVol = getVol;

  let centerKey = null; // fonte aberta no centro: 'browser' | 'screen:<userId>' | null
  let viewSel;          // undefined = automático · 'none' = só pessoas · ou a chave de uma fonte

  window.applyTileVolume = function (v, t) {
    if (t.self) { v.muted = true; return; }
    const g = getVol((t.screen ? 's:' : 'u:') + t.userId);
    v.volume = g.v;
    const dupe = t.screen && centerKey === 'screen:' + t.userId; // já toca no centro: não dobra o som
    v.muted = call.deafened || g.m || dupe;
  };

  function syncSlider(range, btn, g) {
    range.value = Math.round(g.v * 100);
    rng(range);
    setIcon(btn, g.m || g.v === 0 ? 'volx' : 'vol');
  }

  function applyCenterVolumes() {
    const gb = getVol('browser');
    const bv = $('#bVideo');
    bv.volume = gb.v;
    bv.muted = !!(bshare && bshare.isHost) || call.deafened || gb.m;
    syncSlider($('#bVol'), $('#bMute'), gb);
    if (centerKey && centerKey.startsWith('screen:')) {
      const uid = centerKey.slice(7);
      const g = getVol('s:' + uid);
      const sv = $('#sVideo');
      sv.volume = g.v;
      sv.muted = uid === USER.id || call.deafened || g.m;
      syncSlider($('#sVol'), $('#sMute'), g);
      syncSlider($('#sVol2'), $('#sMute2'), g);
    }
  }

  function applyAllVolumes() {
    renderCall();
    applyCenterVolumes();
    if (volKey) syncVolPop();
  }

  $('#bVol').oninput = (e) => setVol('browser', { v: e.target.value / 100, m: false });
  $('#bMute').onclick = () => setVol('browser', { m: !getVol('browser').m });
  const sVolSet = (e) => { if (centerKey && centerKey.startsWith('screen:')) setVol('s:' + centerKey.slice(7), { v: e.target.value / 100, m: false }); };
  const sMuteTgl = () => { if (centerKey && centerKey.startsWith('screen:')) { const k = 's:' + centerKey.slice(7); setVol(k, { m: !getVol(k).m }); } };
  $('#sVol2').oninput = sVolSet;
  $('#sMute2').onclick = sMuteTgl;
  $('#sVol').oninput = (e) => { if (centerKey && centerKey.startsWith('screen:')) setVol('s:' + centerKey.slice(7), { v: e.target.value / 100, m: false }); };
  $('#sMute').onclick = () => { if (centerKey && centerKey.startsWith('screen:')) { const k = 's:' + centerKey.slice(7); setVol(k, { m: !getVol(k).m }); } };

  /* popup de volume (botão direito numa pessoa ou numa tela) */
  let volKey = null;
  function syncVolPop() {
    const g = getVol(volKey);
    $('#volRange').value = Math.round(g.v * 100);
    rng($('#volRange'));
    $('#volPct').textContent = g.m ? 'mudo' : Math.round(g.v * 100) + '%';
    setIcon($('#volMute'), g.m || g.v === 0 ? 'volx' : 'vol');
  }
  function openVolPop(key, title, x, y) {
    hidePops();
    volKey = key;
    $('#volTitle').textContent = title;
    syncVolPop();
    const p = $('#volPop');
    p.hidden = false;
    p.style.left = Math.max(8, Math.min(x, innerWidth - p.offsetWidth - 8)) + 'px';
    p.style.top = Math.max(8, Math.min(y, innerHeight - p.offsetHeight - 8)) + 'px';
    $('#volRange').focus();
  }
  $('#volRange').oninput = (e) => setVol(volKey, { v: e.target.value / 100, m: false });
  $('#volMute').onclick = () => setVol(volKey, { m: !getVol(volKey).m });
  const loginOf = (uid) => { const p = call.peers.get(uid); return p ? p.login : 'participante'; };
  document.addEventListener('contextmenu', (e) => {
    const tile = e.target.closest('#callGrid .tile');
    if (tile) {
      const uid = tile.dataset.uid, scr = tile.classList.contains('screen');
      if (!uid || uid === USER.id) return;
      e.preventDefault();
      openVolPop((scr ? 's:' : 'u:') + uid, (scr ? 'Volume da tela de @' : 'Volume de @') + loginOf(uid), e.clientX, e.clientY);
      return;
    }
    const vp = e.target.closest('.vp[data-uid]');
    if (vp && vp.dataset.uid !== USER.id) {
      e.preventDefault();
      openVolPop('u:' + vp.dataset.uid, 'Volume de @' + (vp.dataset.ul || loginOf(vp.dataset.uid)), e.clientX, e.clientY);
      return;
    }
    if (e.target.closest('#sView') && centerKey && centerKey.startsWith('screen:') && centerKey.slice(7) !== USER.id) {
      e.preventDefault();
      const uid = centerKey.slice(7);
      openVolPop('s:' + uid, 'Volume da tela de @' + loginOf(uid), e.clientX, e.clientY);
    }
  });

  /* ── fontes no centro: cada pessoa escolhe o que ver ── */
  function sources() {
    const out = [];
    if (bshare && bshare.state.active && bshare.inThisCall) {
      out.push({ key: 'browser', kind: 'browser', label: 'Navegador de ' + (bshare.isHost ? 'você' : '@' + bshare.state.host_login) });
    }
    call.tiles().filter((t) => t.screen).forEach((t) => {
      out.push({ key: 'screen:' + t.userId, kind: 'screen', tile: t, label: 'Tela de ' + (t.self ? 'você' : '@' + t.login) });
    });
    return out;
  }

  function resolve(list) {
    if (!list.length) return null;
    if (viewSel === 'none') return null;
    if (viewSel && list.some((s) => s.key === viewSel)) return viewSel;
    const b = list.find((s) => s.kind === 'browser');
    if (b) return b.key;
    const other = list.find((s) => s.kind === 'screen' && !s.tile.self);
    return other ? other.key : null; // só a minha própria tela: continua no grid
  }

  window.selectSource = (key) => { viewSel = key; window.renderView(); };
  window.viewHide = () => { viewSel = 'none'; };

  window.renderView = function () {
    if (typeof bshare === 'undefined' || !bshare || !call) return;
    const list = sources();
    if (!list.length) viewSel = undefined;
    const sel = resolve(list);
    const prev = centerKey;
    centerKey = sel;

    const wantBrowser = sel === 'browser';
    if (bshare.focused !== wantBrowser) {
      bshare.focused = wantBrowser;
      if (wantBrowser) bshare.hiddenLocal = false;
      bshare._render();
    }

    const stage = $('#sStage'), sv = $('#sVideo');
    const showScreen = !!sel && sel.startsWith('screen:');
    stage.hidden = !showScreen;
    if (showScreen) {
      const src = list.find((x) => x.key === sel);
      const stream = (src && src.tile.stream) || null;
      if (sv.srcObject !== stream) { sv.srcObject = stream; const pr = sv.play(); if (pr && pr.catch) pr.catch(() => {}); }
      $('#sWho').textContent = src ? src.label : '';
    } else if (sv.srcObject) {
      sv.srcObject = null;
    }

    const bar = $('#srcBar');
    const showBar = list.length >= 2 || (list.length === 1 && sel === null && list[0].kind !== 'browser'); // só o navegador recolhido usa o aviso "sessão ativa — abrir"
    bar.hidden = !showBar;
    if (showBar) {
      bar.innerHTML = list.map((s) =>
        `<button type="button" class="sc${s.key === sel ? ' on' : ''}" role="tab" aria-selected="${s.key === sel}" data-sk="${esc(s.key)}">${ic(s.kind === 'browser' ? 'globe' : 'scr')}${esc(s.label)}</button>`).join('') +
        (sel ? `<button type="button" class="sc" data-sk="none">${ic('x')}Só pessoas</button>` : '');
    }
    if (showBar) bshare.el.banner.hidden = true;

    $('#callStage').classList.toggle('hasb', sel !== null);
    if (prev !== centerKey) renderCall();
    applyCenterVolumes();
    immEval();
  };

  $('#srcBar').addEventListener('click', (e) => {
    const b = e.target.closest('[data-sk]');
    if (b) window.selectSource(b.dataset.sk);
  });
  $('#callGrid').addEventListener('click', (e) => {
    const t = e.target.closest('.tile[data-sk]');
    if (t) window.selectSource(t.dataset.sk);
  });

  /* ── tela cheia da tela compartilhada ── */
  const sStage = $('#sStage');
  function toggleSFull() {
    if (document.fullscreenElement) { document.exitFullscreen().catch(() => {}); return; }
    if (sStage.hidden) return;
    sStage.requestFullscreen().catch(() => toast('Não consegui abrir em tela cheia.', true));
  }
  window.toggleSFull = toggleSFull;
  $('#sFull').onclick = toggleSFull;
  $('#sView').addEventListener('dblclick', (e) => { e.preventDefault(); toggleSFull(); });
  $('#sHide').onclick = () => {
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    window.viewHide();
    window.renderView();
  };
  document.addEventListener('keydown', (e) => {
    if (sStage.hidden) return;
    if (e.key === 'F11') { e.preventDefault(); toggleSFull(); return; }
    if ((e.key === 'f' || e.key === 'F') && !e.ctrlKey && !e.metaKey && !e.altKey) {
      const a = document.activeElement;
      if (a && (/^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName) || a.isContentEditable)) return;
      e.preventDefault();
      toggleSFull();
    }
  });

  /* ── barras que somem: tela cheia, ou janela maximizada vendo uma fonte ── */
  let winState = { maximized: false, fullscreen: false };
  let barsOn = false, barsT = null, immWas = false;
  const viewing = () => !$('#bStage').hidden || !$('#sStage').hidden;
  const immActive = () => viewing() && (!!document.fullscreenElement || winState.maximized || winState.fullscreen);
  const urlFocus = () => !!document.activeElement && document.activeElement.id === 'bUrl';

  function setBars(on) {
    barsOn = on;
    $('#app').classList.toggle('tb', on);
    $$('.bstage,.sstage').forEach((x) => x.classList.toggle('tb', on));
  }
  function scheduleHide() {
    clearTimeout(barsT);
    barsT = setTimeout(() => { barsT = null; if (!urlFocus()) setBars(false); }, 1500);
  }
  function immEval() {
    const on = immActive(), fs = !!document.fullscreenElement;
    $('#app').classList.toggle('imm', on && !fs);
    $$('.bstage,.sstage').forEach((x) => x.classList.toggle('imm', on));
    if (!on) {
      clearTimeout(barsT); barsT = null;
      setBars(false);
    } else if (!immWas) {
      setBars(true);
      scheduleHide();
    }
    immWas = on;
  }
  window.immEval = immEval;
  window.immState = () => ({ active: immActive(), barsOn, fullscreen: !!document.fullscreenElement, maximized: winState.maximized });

  document.addEventListener('mousemove', (e) => {
    if (!immActive()) return;
    if (e.clientY <= 8 || e.clientY >= innerHeight - 8) { clearTimeout(barsT); barsT = null; setBars(true); return; }
    if (!barsOn) return;
    const h = (document.fullscreenElement ? 0 : 38) + 56;
    const ovl = $('#sOvl').getBoundingClientRect();
    const overOvl = !$('#sStage').hidden && e.clientX >= ovl.left && e.clientX <= ovl.right && e.clientY >= ovl.top && e.clientY <= ovl.bottom;
    if (e.clientY <= h || urlFocus() || overOvl) { clearTimeout(barsT); barsT = null; }
    else if (!barsT) scheduleHide();
  });
  document.documentElement.addEventListener('mouseleave', () => { if (immActive() && barsOn) scheduleHide(); });
  document.addEventListener('fullscreenchange', () => {
    sStage.classList.toggle('fs', document.fullscreenElement === sStage);
    immEval();
  });

  if (window.bridge && window.bridge.win && window.bridge.win.state) {
    window.bridge.win.state().then((st) => { winState = st || winState; immEval(); }).catch(() => {});
    window.bridge.win.onState((st) => { winState = st || winState; immEval(); });
  }

  /* ── diálogo de qualidade ao iniciar o navegador compartilhado ── */
  window.askBrowserQuality = function () {
    if (window.__autoQuality) return Promise.resolve(true);
    return new Promise((resolveP) => {
      $('#bsRes').value = String(call.quality.h);
      $('#bsFps').value = String(call.quality.fps);
      const m = $('#m-bstart');
      let done = false;
      const obs = new MutationObserver(() => { if (m.hidden) finish(false); });
      const finish = (ok) => { if (done) return; done = true; obs.disconnect(); resolveP(ok); };
      obs.observe(m, { attributes: true, attributeFilter: ['hidden'] });
      openM('m-bstart', document.activeElement);
      $('#bsGo').onclick = () => {
        call.setQuality({ h: $('#bsRes').value, fps: $('#bsFps').value });
        finish(true);
        closeM();
      };
    });
  };

  syncSliders();
  function syncSliders() {
    syncSlider($('#bVol'), $('#bMute'), getVol('browser'));
  }
})();

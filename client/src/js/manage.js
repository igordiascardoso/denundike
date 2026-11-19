/* manage.js — menus e ações de grupo e canal (papéis: owner / admin / member) */
(function () {
  'use strict';

  const canManage = (r) => r === 'owner' || r === 'admin';
  const myRole = () => (activeGroup && activeGroup.role) || 'member';

  /* ── menu de contexto ── */
  function showCtx(items, x, y) {
    hidePops();
    const m = $('#ctxMenu');
    m._items = items;
    m.innerHTML = items.map((it, i) => it === 'hr' ? '<hr>'
      : `<button class="mi${it.danger ? ' danger' : ''}" role="menuitem" data-ci="${i}">${it.icon ? ic(it.icon) : ''}${esc(it.label)}</button>`).join('');
    m.hidden = false;
    m.style.left = Math.max(8, Math.min(x, innerWidth - m.offsetWidth - 8)) + 'px';
    m.style.top = Math.max(8, Math.min(y, innerHeight - m.offsetHeight - 8)) + 'px';
    const f = m.querySelector('button');
    if (f) f.focus();
  }
  $('#ctxMenu').addEventListener('click', (e) => {
    const b = e.target.closest('[data-ci]');
    if (!b) return;
    const it = $('#ctxMenu')._items[+b.dataset.ci];
    hidePops();
    if (it && it.fn) it.fn();
  });
  window.showCtx = showCtx;

  /* ── diálogos ── */
  function openText(o) {
    $('#txTitle').textContent = o.title;
    $('#txSub').textContent = o.sub || '';
    $('#txLabel').textContent = o.label || 'Nome';
    const inp = $('#txInput');
    inp.value = o.value || '';
    inp.maxLength = o.max || 50;
    $('#txOk').textContent = o.ok || 'Salvar';
    openM('m-text', document.activeElement);
    inp.focus();
    inp.select();
    const go = () => run($('#txOk'), async () => {
      const v = inp.value.trim();
      if (!v) return toast('Digite um nome.', true);
      await o.run(v);
      closeM();
    });
    $('#txOk').onclick = go;
    inp.onkeydown = (e) => { if (e.key === 'Enter') { e.preventDefault(); go(); } };
  }

  function openConfirm(o) {
    $('#cfTitle').textContent = o.title;
    $('#cfText').textContent = o.text;
    const box = $('#cfTypedBox'), inp = $('#cfTyped'), ok = $('#cfOk');
    ok.textContent = o.ok || 'Confirmar';
    if (o.typed) {
      box.hidden = false;
      $('#cfTypedLabel').textContent = 'Digite "' + o.typed + '" para confirmar';
      inp.value = '';
      ok.disabled = true;
      inp.oninput = () => { ok.disabled = inp.value.trim() !== o.typed; };
    } else {
      box.hidden = true;
      ok.disabled = false;
      inp.oninput = null;
    }
    openM('m-confirm', document.activeElement);
    if (o.typed) inp.focus(); else ok.focus();
    ok.onclick = () => run(ok, async () => {
      await o.run();
      closeM();
    });
  }
  window.openText = openText;
  window.openConfirm = openConfirm;

  /* ── nome do grupo na interface ── */
  function applyGroupName(id, name) {
    const g = allGroups.find((x) => x.id === id);
    const was = g ? g.name : null;
    if (g) g.name = name;
    if (activeGroup && activeGroup.id === id) {
      activeGroup.name = name;
      $('#gname').textContent = name;
      const iv = $('#invGroupName');
      if (iv) iv.textContent = name;
    }
    const b = document.querySelector('#rail [data-gid="' + id + '"]');
    if (b) { b.title = name; b.setAttribute('aria-label', name); b.textContent = initials(name); }
    return was !== null && was !== name;
  }

  /* ── grupo ── */
  function groupItems(g) {
    const r = g.role || 'member';
    const inGroup = (fn) => async () => {
      try {
        if (!activeGroup || activeGroup.id !== g.id) { closeFriends(); await activateGroup(g); }
        fn();
      } catch (e) { fail(e); }
    };
    const items = [
      { label: 'Convidar pessoas', icon: 'uplus', fn: inGroup(() => openInvite(document.activeElement)) },
      { label: 'Membros e papéis', icon: 'users', fn: inGroup(() => setMem(true)) },
    ];
    if (canManage(r)) items.push({ label: 'Editar nome do grupo', icon: 'sl', fn: () => editGroupName(g) });
    items.push('hr');
    if (r === 'owner') {
      items.push({ label: 'Excluir grupo', icon: 'x', danger: true, fn: () => deleteGroup(g) });
    } else {
      items.push({ label: 'Sair do grupo', icon: 'out', danger: true, fn: () => leaveGroup(g) });
    }
    return items;
  }

  function editGroupName(g) {
    openText({
      title: 'Editar nome do grupo', label: 'Nome do grupo', value: g.name, ok: 'Salvar',
      run: async (v) => {
        const r = await api('PATCH', '/api/groups/' + g.id, { name: v });
        applyGroupName(g.id, r.name);
        toast('Grupo renomeado para "' + r.name + '"');
      },
    });
  }

  function leaveGroup(g) {
    openConfirm({
      title: 'Sair do grupo',
      text: 'Você sai de "' + g.name + '" e deixa de ver os canais e as mensagens. Para voltar, precisa de um novo convite.',
      ok: 'Sair do grupo',
      run: async () => {
        await api('POST', '/api/groups/' + g.id + '/leave');
        toast('Você saiu de "' + g.name + '"');
        await loadGroups();
      },
    });
  }

  function deleteGroup(g) {
    openConfirm({
      title: 'Excluir grupo',
      text: 'Isto apaga "' + g.name + '" para todo mundo: canais, mensagens e convites. Não dá para desfazer.',
      typed: g.name, ok: 'Excluir para sempre',
      run: async () => {
        await api('DELETE', '/api/groups/' + g.id);
        toast('Grupo "' + g.name + '" excluído');
        await loadGroups();
      },
    });
  }

  function openGroupMenuAt(el) {
    if (!activeGroup) return toast('Escolha um grupo primeiro.', true);
    const r = el.getBoundingClientRect();
    showCtx(groupItems(activeGroup), r.left, r.bottom + 6);
  }
  $('#grpMenuBtn').onclick = (e) => { e.stopPropagation(); openGroupMenuAt(e.currentTarget); };
  $('#gname').onclick = (e) => { e.stopPropagation(); openGroupMenuAt(e.currentTarget); };
  $('.gh').addEventListener('contextmenu', (e) => {
    e.preventDefault();
    if (activeGroup) showCtx(groupItems(activeGroup), e.clientX, e.clientY);
  });
  $('#rail').addEventListener('contextmenu', (e) => {
    const b = e.target.closest('[data-gid]');
    if (!b) return;
    e.preventDefault();
    const g = allGroups.find((x) => x.id === b.dataset.gid);
    if (g) showCtx(groupItems(g), e.clientX, e.clientY);
  });

  /* ── canais ── */
  function channelItems(ch) {
    return [
      { label: 'Renomear canal', icon: 'sl', fn: () => renameChannel(ch) },
      'hr',
      { label: 'Excluir canal', icon: 'x', danger: true, fn: () => deleteChannel(ch) },
    ];
  }

  window.chWrap = function (btn, ch) {
    const w = document.createElement('div');
    w.className = 'chw';
    w.dataset.cw = ch.id;
    w.appendChild(btn);
    if (canManage(myRole())) {
      const m = document.createElement('button');
      m.type = 'button';
      m.className = 'ib chmore';
      m.dataset.chmenu = ch.id;
      m.setAttribute('aria-label', 'Ações do canal ' + ch.name);
      m.title = 'Ações do canal';
      m.innerHTML = ic('more', 'i sm');
      m.onclick = (e) => { e.stopPropagation(); const r = m.getBoundingClientRect(); showCtx(channelItems(ch), r.left, r.bottom + 4); };
      w.appendChild(m);
      w.addEventListener('contextmenu', (e) => { e.preventDefault(); showCtx(channelItems(ch), e.clientX, e.clientY); });
    }
    return w;
  };

  function renameChannel(ch) {
    openText({
      title: 'Renomear canal', label: 'Nome do canal', value: ch.name, ok: 'Salvar',
      run: async (v) => {
        const name = ch.type === 'voice' ? v : v.toLowerCase().replace(/\s+/g, '-');
        await api('PATCH', '/api/channels/' + ch.id, { name });
        toast('Canal renomeado para "' + name + '"');
        await refreshChannels();
      },
    });
  }

  function deleteChannel(ch) {
    openConfirm({
      title: 'Excluir canal',
      text: ch.type === 'voice'
        ? 'A sala de voz "' + ch.name + '" será apagada e quem estiver nela sai da chamada.'
        : 'O canal #' + ch.name + ' e todas as mensagens dele serão apagados. Não dá para desfazer.',
      ok: 'Excluir canal',
      run: async () => {
        await api('DELETE', '/api/channels/' + ch.id);
        toast('Canal "' + ch.name + '" excluído');
        await refreshChannels();
      },
    });
  }

  async function refreshChannels() {
    if (!activeGroup) return;
    const gid = activeGroup.id;
    const channels = await api('GET', '/api/groups/' + gid + '/channels');
    if (!activeGroup || activeGroup.id !== gid) return;
    renderChannels(channels);
    loadVoicePresence();
    const cur = activeChannel && channels.find((c) => c.id === activeChannel.id);
    if (cur) {
      activeChannel = cur;
      $$('[data-chid]').forEach((b) => b.classList.toggle('on', b.dataset.chid === cur.id));
      $('#chname').textContent = cur.name;
      $('#cin').placeholder = 'Mensagem em #' + cur.name;
    } else {
      const first = channels.find((c) => c.type !== 'voice');
      if (first) await activateChannel(first); else resetChat();
    }
  }
  window.refreshChannels = refreshChannels;

  /* ── papéis ── */
  async function refreshRoles() {
    let groups;
    try { groups = await api('GET', '/api/groups'); } catch (_) { return; }
    allGroups = groups;
    if (!activeGroup) return;
    const g = groups.find((x) => x.id === activeGroup.id);
    if (!g) { await loadGroups(); return; }
    const changed = g.role !== activeGroup.role;
    activeGroup = g;
    $('#gname').textContent = g.name;
    if (changed) {
      renderChannels(window.__chs || []);
      toast(g.role === 'owner' ? 'Você agora é o dono do grupo' : g.role === 'admin' ? 'Você agora é admin do grupo' : 'Você agora é membro do grupo');
    }
  }
  window.refreshRoles = refreshRoles;

  window.memberActions = function (my, m) {
    const a = [];
    if (my === 'owner') {
      if (m.role === 'member') a.push('admin');
      else if (m.role === 'admin') a.push('member');
      a.push('owner', 'kick');
    } else if (my === 'admin' && m.role === 'member') {
      a.push('kick');
    }
    return a;
  };

  async function setRole(m, role, msg) {
    await api('PATCH', '/api/groups/' + activeGroup.id + '/members/' + m.id, { role });
    toast(msg);
    await refreshRoles();
    loadMembers();
  }

  function memberItems(m) {
    const items = [];
    for (const k of window.memberActions(myRole(), m)) {
      if (k === 'admin') items.push({ label: 'Tornar admin', icon: 'user', fn: () => run(null, () => setRole(m, 'admin', '@' + m.login + ' agora é admin')) });
      if (k === 'member') items.push({ label: 'Remover de admin', icon: 'user', fn: () => run(null, () => setRole(m, 'member', '@' + m.login + ' voltou a ser membro')) });
      if (k === 'owner') items.push({
        label: 'Transferir propriedade…', icon: 'sl',
        fn: () => openConfirm({
          title: 'Transferir propriedade',
          text: '@' + m.login + ' passa a ser o dono do grupo. Você vira admin e deixa de poder excluir o grupo e mudar papéis.',
          ok: 'Transferir', run: () => setRole(m, 'owner', '@' + m.login + ' agora é o dono do grupo'),
        }),
      });
      if (k === 'kick') {
        if (items.length) items.push('hr');
        items.push({
          label: 'Expulsar do grupo', icon: 'out', danger: true,
          fn: () => openConfirm({
            title: 'Expulsar @' + m.login,
            text: '@' + m.login + ' sai do grupo e deixa de ver os canais. Só entra de novo com um novo convite.',
            ok: 'Expulsar',
            run: async () => {
              await api('DELETE', '/api/groups/' + activeGroup.id + '/members/' + m.id);
              toast('@' + m.login + ' foi removido do grupo');
              loadMembers();
            },
          }),
        });
      }
    }
    return items;
  }

  document.addEventListener('click', (e) => {
    const b = e.target.closest('[data-mact]');
    if (!b) return;
    const m = (window.__members || []).find((x) => x.id === b.dataset.mact);
    if (!m) return;
    const r = b.getBoundingClientRect();
    showCtx(memberItems(m), Math.max(8, r.right - 230), r.bottom + 4);
  });

  /* ── eventos em tempo real ── */
  window.manageWS = function (ev) {
    switch (ev.type) {
      case 'group_updated':
        if (applyGroupName(ev.group_id, ev.name)) toast('O grupo agora se chama "' + ev.name + '"');
        break;
      case 'channel_created':
      case 'channel_updated':
        if (activeGroup && ev.group_id === activeGroup.id) refreshChannels().catch(() => {});
        break;
      case 'channel_deleted':
        if (activeGroup && ev.group_id === activeGroup.id) {
          toast('O canal "' + ev.name + '" foi excluído');
          refreshChannels().catch(() => {});
        }
        break;
      case 'group_deleted':
        toast('O grupo "' + ev.name + '" foi excluído pelo dono', true);
        loadGroups().catch(fail);
        break;
      case 'group_removed':
        if (ev.reason === 'kicked') toast('Você foi removido do grupo "' + ev.name + '"', true);
        loadGroups().catch(fail);
        break;
    }
  };
})();

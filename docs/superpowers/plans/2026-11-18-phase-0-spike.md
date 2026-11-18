# Denundike Phase 0: Validação do Spike

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Validar que o compartilhamento de aba via WebRTC funciona de ponta a ponta, entender os limites do áudio por loopback, medir performance real a 1080p/60fps, e determinar se conteúdo DRM (Netflix, Spotify) pode ser capturado — e com qual abordagem.

**Architecture:** O spike em `spike/` já tem janela Host (Electron com WebContentsView para o site) e janela Viewer (recebe stream WebRTC). A Phase 0 completa o caminho de sinal entre as duas janelas, executa a campanha de testes, e registra os achados que vão guiar as decisões de arquitetura da Phase 1.

**Tech Stack:** Electron 45, WebRTC nativo, Node.js; Google Chrome (teste DRM); fork castLabs do Electron (fallback DRM).

**Spec:** `docs/superpowers/specs/2026-11-18-denundike-design.md`

## Global Constraints

- Spike fica em `spike/` — descartável, sem qualidade de produção.
- Nunca deixar `ELECTRON_RUN_AS_NODE=1` no ambiente; sempre `unset ELECTRON_RUN_AS_NODE` antes de rodar.
- Resultados vão em `spike/RESULTS.md` — entrada para as decisões da Phase 1.
- Alvo de qualidade: 1080p 60fps; 720p 30fps como fallback.
- Nenhum segredo no repositório; credenciais ficam em `C:\Users\brizao\Documents\.brizao\.secrets`.

## Review Focus

- **Loop de eco:** loopback + microfone aberto → áudio em loop para todos. Testar: rodar com mic ativo e loopback ligado, conferir se o viewer ouve eco.
- **Vazamento de sistema:** Spotify tocando fora da aba vaza para o stream. Testar: reproduzir áudio externo ao WebContentsView, verificar se o viewer escuta.
- **DRM com tela preta:** `setDisplayMediaRequestHandler` retorna vídeo preto para conteúdo protegido. Testar: abrir Netflix após configurar DRM, conferir o vídeo do viewer.
- **Falha de WebRTC através de NAT real:** P2P direto pode falhar sem TURN. Registrar: a Fase 0 testa na mesma máquina; anotar que NAT real precisa ser validado antes da Phase 2.
- **Saturação de upload:** stream 1080p/60fps pode exceder o upload do anfitrião. Testar: medir Mbps real durante o stream com o monitor de rede.

---

### Task 1: Verificar conexão WebRTC de ponta a ponta (mesma máquina)

**Files:**
- Modify: `spike/main.js`
- Modify: `spike/host.html`
- Modify: `spike/viewer.html`

**Interfaces:**
- Produces: caminho `offer → answer → ICE → stream` confirmado; relay de IPC `window.bridge.signal()` funcionando nos dois sentidos.

- [ ] **Step 1: Rodar o spike**

```bash
cd spike && unset ELECTRON_RUN_AS_NODE && npm start
```
Expected: janela Host abre com YouTube; janela Viewer abre.

- [ ] **Step 2: Clicar "Compartilhar aba" no host.html e verificar se o stream aparece no viewer.html**

Expected: viewer mostra o vídeo da aba YouTube. Se preto ou travado, abrir DevTools (Ctrl+Shift+I) e conferir o console.

- [ ] **Step 3: Se o stream não aparecer — corrigir o relay de ICE candidates**

O `ipcMain.on('signal', ...)` em `main.js` precisa repassar eventos `candidate` tanto do host para o viewer quanto do viewer para o host. Confirmar os dois sentidos. Se faltar, adicionar:

```js
ipcMain.on('signal', (evt, data) => {
  const target = evt.sender === hostWin.webContents ? viewerWin : hostWin;
  target.webContents.send('signal', data);
});
```

- [ ] **Step 4: Confirmar stats no viewer.html — fps e resolução reais**

Expected: painel de stats mostra ≥ 1280×720, fps > 20. Anotar valores exatos em `spike/RESULTS.md`.

- [ ] **Step 5: Commit**

```bash
git add spike/
git commit -m "spike: verificar WebRTC tab share de ponta a ponta na mesma máquina"
```

---

### Task 2: Teste de captura e isolamento de áudio

**Files:**
- Modify: `spike/viewer.html` (desmutar temporariamente para o teste)
- Modify: `spike/host.html` (garantir `audio: true` no getDisplayMedia)
- Create: `spike/RESULTS.md`

**Interfaces:**
- Consumes: stream funcionando da Task 1
- Produces: `spike/RESULTS.md` com achados de eco, vazamento e isolamento

- [ ] **Step 1: Desmutar o vídeo do viewer para o teste**

Em `viewer.html`, remover `muted` do `<video>` ou adicionar botão "Ouvir". Apenas para teste.

- [ ] **Step 2: Abrir um vídeo com som no YouTube na aba do host**

Expected: viewer escuta o áudio do YouTube. Anotar qualidade em RESULTS.md.

- [ ] **Step 3: Teste de eco — abrir microfone no host enquanto o áudio está sendo streamado**

Abrir qualquer app que reproduza o microfone (ex: configurações de som do Windows → escuta). Verificar se viewer ouve loop.
Anotar resultado: eco presente (esperado com loopback) / não presente.

- [ ] **Step 4: Teste de vazamento — tocar Spotify ou outro áudio fora da aba do host**

Tocar música na máquina host fora do WebContentsView. Verificar se viewer escuta.
Anotar: vaza / não vaza.

- [ ] **Step 5: Tentar áudio isolado da aba — alterar o parâmetro audio no setDisplayMediaRequestHandler**

Em `main.js`, testar:
```js
return { video: siteView.webContents.mainFrame, audio: siteView.webContents.mainFrame };
```
Verificar se o Electron 45 suporta isolamento de áudio por frame de webContents. Anotar resultado.

- [ ] **Step 6: Documentar em `spike/RESULTS.md`**

Seções: Eco (sim/não, severidade), Vazamento de sistema (sim/não), Áudio isolado (funciona/não suportado), Qualidade de áudio (boa/aceitável/ruim).

- [ ] **Step 7: Commit**

```bash
git add spike/RESULTS.md spike/host.html spike/viewer.html
git commit -m "spike: resultados de isolamento e eco de áudio"
```

---

### Task 3: Teste de performance a 1080p/60fps

**Files:**
- Modify: `spike/host.html` (definir constraint 1080p)

**Interfaces:**
- Consumes: stream funcionando da Task 1

- [ ] **Step 1: Definir 1080p no getDisplayMedia**

Em `host.html`:
```js
video: { width: 1920, height: 1080, frameRate: 60 }
```

- [ ] **Step 2: Maximizar a janela host e abrir um vídeo 1080p60 no YouTube**

Navegar para um vídeo YouTube em qualidade 1080p60.

- [ ] **Step 3: Ler stats do viewer.html após 30 segundos**

Anotar em RESULTS.md: resolução, fps, Mbps, packets lost.

- [ ] **Step 4: Testar 720p/30fps como fallback**

Alterar constraints para `{ width: 1280, height: 720, frameRate: 30 }`. Anotar stats.

- [ ] **Step 5: Medir banda de upload real**

Usar o Gerenciador de Tarefas (aba Desempenho → Ethernet/Wi-Fi) ou similar durante o stream 1080p. Anotar Mbps de upload em RESULTS.md.

- [ ] **Step 6: Commit**

```bash
git add spike/RESULTS.md spike/host.html
git commit -m "spike: resultados de performance 1080p60 e 720p30"
```

---

### Task 4: Teste de DRM — Google Chrome com aceleração de hardware desligada

**Files:**
- Create: `spike/drm-chrome-test.md`

**Interfaces:**
- Produces: achados do DRM; decisão sobre Chrome vs castLabs

- [ ] **Step 1: Desligar aceleração de hardware no Chrome**

Abrir `chrome://settings/system`, desligar "Usar aceleração de hardware quando disponível", reiniciar o Chrome.

- [ ] **Step 2: Confirmar que conteúdo DRM toca normalmente no Chrome**

Abrir Spotify Web Player (spotify.com) ou Netflix. Tocar qualquer faixa ou episódio. Expected: toca sem problema.

- [ ] **Step 3: Capturar a janela do Chrome via getDisplayMedia no host**

No `host.html`, ao clicar "Compartilhar aba", o seletor do sistema aparece. Selecionar a janela do Chrome (não a aba do Electron). Verificar se o viewer mostra o Chrome.

- [ ] **Step 4: Com o Chrome capturado, reproduzir conteúdo DRM e conferir o viewer**

Tocar música no Spotify ou episódio no Netflix dentro do Chrome capturado. Verificar se o vídeo/áudio chega no viewer ou se fica preto/silencioso.

- [ ] **Step 5: Documentar em `spike/drm-chrome-test.md`**

Resultado: (a) captura funciona com conteúdo DRM, (b) vídeo preto mas áudio ok, (c) tudo preto/silencioso, (d) janela do Chrome não aparece como fonte.

- [ ] **Step 6: Commit**

```bash
git add spike/drm-chrome-test.md
git commit -m "spike: teste DRM com Chrome hw acceleration off"
```

---

### Task 5: Teste de DRM — fork castLabs do Electron (se Task 4 não funcionou)

**Files:**
- Create: `spike/drm-castlabs-test.md`

**Interfaces:**
- Consumes: resultado da Task 4 — executar apenas se a captura do Chrome não resolveu o DRM

- [ ] **Step 1: Verificar a versão disponível do castLabs Electron**

Consultar https://github.com/castlabs/electron-releases para a versão mais recente compatível com a API do Electron 45. Anotar a versão em `spike/drm-castlabs-test.md`.

- [ ] **Step 2: Instalar o fork castLabs no spike**

```bash
cd spike
npm install --save-dev "castlabs-electron-releases@<versão-anotada>"
```

- [ ] **Step 3: Rodar o spike com o castLabs Electron**

```bash
unset ELECTRON_RUN_AS_NODE
./node_modules/.bin/electron . --no-sandbox
```
Ou o executável equivalente do castLabs.

- [ ] **Step 4: Abrir Spotify ou Netflix no WebContentsView e verificar se DRM toca**

Expected: conteúdo DRM toca dentro da aba do app (Widevine presente no castLabs).

- [ ] **Step 5: Verificar se setDisplayMediaRequestHandler captura o frame sem tela preta**

Clicar "Compartilhar aba" e conferir o viewer. Anotar resultado.

- [ ] **Step 6: Documentar em `spike/drm-castlabs-test.md`**

- [ ] **Step 7: Commit**

```bash
git add spike/drm-castlabs-test.md
git commit -m "spike: teste DRM com fork castLabs do Electron"
```

---

### Task 6: Controle remoto — viewer controla o navegador do host

**Files:**
- Modify: `spike/viewer.html` (encaminhar cliques do mouse)
- Modify: `spike/main.js` (receber via IPC e aplicar via CDP)

**Interfaces:**
- Consumes: stream funcionando da Task 1
- Produces: viewer consegue clicar em elementos na aba do host

- [ ] **Step 1: Adicionar handler de clique no elemento video do viewer**

Em `viewer.html`:
```js
video.addEventListener('click', (e) => {
  const rect = video.getBoundingClientRect();
  const x = (e.clientX - rect.left) / rect.width;
  const y = (e.clientY - rect.top) / rect.height;
  window.bridge.signal({ type: 'remote-click', x, y });
});
```

- [ ] **Step 2: Receber o remote-click em main.js e aplicar via CDP**

```js
// dentro do ipcMain.on('signal', ...) que já existe
if (data.type === 'remote-click') {
  const bounds = siteView.getBounds();
  const cx = Math.round(data.x * bounds.width);
  const cy = Math.round(data.y * bounds.height);
  if (!siteView.webContents.debugger.isAttached()) {
    siteView.webContents.debugger.attach('1.3');
  }
  await siteView.webContents.debugger.sendCommand('Input.dispatchMouseEvent',
    { type: 'mousePressed', button: 'left', x: cx, y: cy, clickCount: 1 });
  await siteView.webContents.debugger.sendCommand('Input.dispatchMouseEvent',
    { type: 'mouseReleased', button: 'left', x: cx, y: cy, clickCount: 1 });
}
```

- [ ] **Step 3: Testar — viewer clica no botão de pause do YouTube**

Expected: o vídeo pausa no host, e o viewer vê a pausa refletida no stream.

- [ ] **Step 4: Registrar resultado em RESULTS.md**

CDP remote click funciona / não funciona. Se não funcionar, anotar o erro.

- [ ] **Step 5: Commit**

```bash
git add spike/viewer.html spike/main.js spike/RESULTS.md
git commit -m "spike: controle remoto via CDP click forwarding"
```

---

### Task 7: Consolidar achados e decisões para a Phase 1

**Files:**
- Modify: `spike/RESULTS.md` (sumário final)
- Modify: `docs/superpowers/specs/2026-11-18-denundike-design.md` (seção 5)

- [ ] **Step 1: Preencher o sumário final em `spike/RESULTS.md`**

Seções:
- Compartilhamento de aba: funciona / latência medida
- Áudio: status do eco, status do isolamento
- Performance: fps e Mbps a 1080p e 720p
- DRM: resultado do Chrome, resultado do castLabs
- Controle remoto: CDP funciona

- [ ] **Step 2: Identificar bloqueadores para a Phase 1**

Um bloqueador é qualquer achado que muda a arquitetura. Exemplos:
- Se loopback for inutilizável → usar extensão Chrome com `chrome.tabCapture`
- Se DRM sempre preto e castLabs também falhou → modo sincronizado vira opção
- Se castLabs captura DRM → usar o fork castLabs como base do cliente

- [ ] **Step 3: Atualizar a seção 5 do design doc com os achados finais**

Substituir "Ainda não testado:" pelos resultados reais.

- [ ] **Step 4: Commit**

```bash
git add spike/RESULTS.md docs/superpowers/specs/2026-11-18-denundike-design.md
git commit -m "spike: phase 0 concluída — achados e decisão para phase 1"
```

# Spike Phase 0 — Resultados

Data: 2026-11-18

## 1. WebRTC de ponta a ponta

**Status:** [ ] Testado
**Correção feita:** `setParameters` antes do `createOffer` pode rejeitar e travar o fluxo — envolvido em try/catch (host.html).
**Relay de ICE:** correto em main.js (host ↔ viewer, bidirecional).

**Como testar:**
1. `cd spike && unset ELECTRON_RUN_AS_NODE && npm start`
2. Clicar "Compartilhar aba" na janela ANFITRIAO
3. Verificar se o vídeo aparece na janela CONVIDADO

**Resultado:**
- Stream aparece: sim / não
- Resolução observada: __________
- FPS observado: __________
- Latência visual estimada: __________

---

## 2. Áudio

**Status:** [ ] Testado

**Como testar:**
1. Desmarcar "mudo" na janela CONVIDADO
2. Tocar vídeo com áudio no YouTube (janela ANFITRIAO)
3. Eco: abrir microfone no host (Configurações de Som → Escuta) — verificar eco
4. Vazamento: tocar música em outro app no host — verificar se chega no viewer

**Resultado:**
- Áudio do site chega: sim / não
- Qualidade: boa / ok / ruim
- Eco com microfone aberto: sim / não
- Vazamento de sistema: sim / não
- Áudio isolado (frame de webContents): testado / não testado — resultado: __________

---

## 3. Performance 1080p/60fps

**Status:** [ ] Testado

**Como testar:**
1. Atualizar `host.html` constraints para `{ width: 1920, height: 1080, frameRate: 60 }`
2. Abrir vídeo 1080p60 no YouTube na aba do anfitrião
3. Ler stats no painel do CONVIDADO após 30 s

**Resultado 1080p/60fps:**
- Resolução real: __________
- FPS real: __________
- Mbps: __________
- Packets lost: __________
- Upload medido (Gerenciador de Tarefas): __________ Mbps

**Resultado 720p/30fps (fallback):**
- Resolução real: __________
- FPS real: __________
- Mbps: __________

---

## 4. DRM — Chrome com hw acceleration off

**Status:** [ ] Testado

**Como testar:**
1. `chrome://settings/system` → desligar aceleração de hardware → reiniciar Chrome
2. Confirmar que Netflix/Spotify toca normalmente no Chrome
3. Iniciar spike, clicar "Compartilhar aba", selecionar janela do Chrome no seletor
4. Tocar conteúdo DRM no Chrome, verificar viewer

**Resultado:**
- Captura da janela Chrome funciona: sim / não
- Conteúdo DRM no viewer: vídeo ok / preto / áudio ok mas vídeo preto / tudo preto

---

## 5. DRM — fork castLabs (executar se Task 4 falhou)

**Status:** [ ] Testado / [ ] Pulado (Chrome resolveu)

**Versão castLabs compatível:** __________

**Resultado:**
- DRM toca no WebContentsView: sim / não
- Captura sem tela preta: sim / não

---

## 6. Controle remoto via CDP

**Status:** [ ] Testado

**Como testar:**
1. Código adicionado em viewer.html (clique → remote-click) e main.js (CDP dispatchMouseEvent)
2. Clicar no botão pause do YouTube na janela CONVIDADO
3. Verificar se pausa no host e reflete no stream

**Resultado:**
- CDP click funciona: sim / não
- Erro observado: __________

---

## Sumário e decisões para Phase 1

| Aspecto | Resultado | Impacto na arquitetura |
|---------|-----------|------------------------|
| Tab share WebRTC | | |
| Áudio (eco) | | |
| Áudio (isolamento) | | |
| Performance 1080p | | |
| DRM Chrome | | |
| DRM castLabs | | |
| Controle remoto CDP | | |

**Bloqueadores identificados:** (preencher após os testes)

**Base Electron para Phase 1:** Electron padrão / castLabs fork / outro

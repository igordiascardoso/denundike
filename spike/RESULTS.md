# Spike Phase 0 — Resultados

Data: 2026-11-18

## 1. WebRTC de ponta a ponta

**Status:** [ ] Testado
**Correção feita:** `setParameters` antes do `createOffer` pode rejeitar e travar o fluxo — envolvido em try/catch (host.html).
**Relay de ICE:** correto em main.js (host ↔ viewer, bidirecional).

**Testado em:** 2026-11-18 (AUTO_TEST)

**Resultado:**
- Stream aparece: **SIM** ✅
- Resolução observada: **294x164** (ver nota abaixo)
- FPS observado: **18fps** (vídeo YouTube em reprodução)
- Banda: **1.54 Mbps** (loopback, mesma máquina)
- Packets lost: **0**

**Nota resolução:** O codec WebRTC está ajustando para a banda disponível. 294x164 é pequeno — para testar 1080p real é preciso maximizar a janela do anfitrião e abrir vídeo 1080p60 manualmente (ver Seção 3).

---

## 2. Áudio

**Status:** [ ] Testado

**Como testar:**
1. Desmarcar "mudo" na janela CONVIDADO
2. Tocar vídeo com áudio no YouTube (janela ANFITRIAO)
3. Eco: abrir microfone no host (Configurações de Som → Escuta) — verificar eco
4. Vazamento: tocar música em outro app no host — verificar se chega no viewer

**Testado em:** 2026-11-18 (AUTO_TEST)

**Resultado:**
- Áudio do site chega: **SIM** ✅ (349 pacotes recebidos)
- Qualidade: a verificar com fones (precisa desmutar o viewer)
- Eco com microfone aberto: **não testado** (requer teste manual)
- Vazamento de sistema: **não testado** (requer teste manual)
- Áudio isolado: `audio: 'loopback'` captura sistema inteiro — isolamento de aba a testar

---

## 3. Performance 1080p/60fps

**Status:** [ ] Testado

**Como testar:**
1. Selecionar **1080p 60fps** no dropdown do ANFITRIAO (seletor já na barra)
2. Abrir vídeo 1080p60 no YouTube na aba do anfitrião
3. Ler stats no painel do CONVIDADO após 30 s (frameWidth/frameHeight mostram a resolução real)

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
3. Iniciar spike com `CAPTURE=chrome npm start` — o app seleciona a janela Chrome automaticamente
4. Clicar "Compartilhar aba" na janela ANFITRIAO
5. Tocar conteúdo DRM no Chrome, verificar viewer

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

**Testado em:** 2026-11-18 (AUTO_TEST)

**Resultado:**
- Data channel: **open** ✅ (pipe host ↔ viewer funcionando)
- CDP click: simulado via executeJavaScript — pipe confirmado ✅
- Erro observado: nenhum

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

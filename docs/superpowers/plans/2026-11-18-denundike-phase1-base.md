# Denundike Phase 1 — Base

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Servidor Go + cliente Electron com interface real (mockups aprovados) e chat em tempo real, prontos para a Phase 2 (voz/vídeo).

**Architecture:** Servidor Go com SQLite, HTTP REST + WebSocket gorilla. Cliente Electron carregando os mockups aprovados (login-c.html, interface-v2.html) com JS ligando à API. Sem voz/vídeo/navegador compartilhado nesta fase.

**Tech Stack:** Go 1.23+, chi router, gorilla/websocket, modernc.org/sqlite (sem CGO), argon2id, Electron 34 (mesma linha do spike), Docker, Coolify, Cloudflare DNS.

**Spec:** `docs/superpowers/specs/2026-11-18-denundike-design.md`

## Global Constraints

- Servidor em Go; cliente em Electron para Windows.
- Banco: SQLite (`modernc.org/sqlite` — sem CGO, funciona no Docker scratch/alpine).
- Senhas com argon2id (`golang.org/x/crypto`).
- Tokens de sessão: UUID v4 aleatório, expiram em 30 dias, armazenados em `sessions` no SQLite.
- Cadastro **só por convite** — `POST /api/auth/register` exige `invite_code` válido e não usado.
- Marca: fundo `#090C0D`, destaque limão `#C8F560`, coral `#FF5C4D`, fontes Bricolage Grotesque + DM Mono.
- Nenhum segredo no repositório. `.env.example` documenta as variáveis; os valores reais ficam em `C:\Users\brizao\Documents\.brizao\.secrets` e na VPS.
- DNS `denundike.estudiofachada.com.br` **não criado ainda** — criar na Task 6 e pedir confirmação explícita do dono antes.
- Limite de memória do container Docker: 256 MB.
- Arquivos do servidor em `server/`; cliente em `client/`.

## Review Focus

- **Reutilização de convite:** um invite_code aceito deve retornar 409 em qualquer tentativa subsequente — verificar no handler, não só no service.
- **Token expirado:** sessões com `expires_at < now()` devem retornar 401, não 403 — o middleware de auth precisa checar a coluna.
- **Reconexão WebSocket:** o cliente JS deve reconectar automaticamente se o WS cair (backoff de 1 s, 2 s, 4 s, máx 30 s); testar desligando o servidor por 5 s durante uso.
- **CORS / protocolo Electron:** o servidor precisa aceitar `Origin: file://` e `Origin: app://denundike` que o Electron envia; testar com o Electron real (não curl).
- **Broadcast ao canal:** uma mensagem enviada por usuário A deve chegar só ao usuário B que está no mesmo canal — não vazar para outros canais ou conexões WS sem autenticação.

---

### Task 1: Servidor — scaffold + banco + auth

**Files:**
- Create: `server/go.mod`, `server/cmd/denundike/main.go`
- Create: `server/internal/db/db.go` — setup SQLite, run migrations
- Create: `server/internal/auth/model.go` — structs User, Session, Invite
- Create: `server/internal/auth/service.go` — RegisterUser, Login, Logout, GetUserByToken, CreateInvite
- Create: `server/internal/auth/handler.go` — HTTP handlers (chi)
- Create: `server/internal/auth/middleware.go` — RequireAuth extrai token Bearer, coloca User no context
- Create: `server/internal/router/router.go` — monta chi.Router, registra rotas
- Test: `server/internal/auth/auth_test.go`

**Interfaces:**
- Produces:
  - `auth.RequireAuth(next http.Handler) http.Handler` — middleware usado em todas as tarefas seguintes
  - `auth.UserFromContext(ctx) *auth.User` — extrai usuário autenticado do context
  - DB aberto como `*sql.DB` em `db.Open(path string) (*sql.DB, error)`

**Endpoints:**

```
POST /api/auth/register   body: {login, email, password, invite_code}  → {user}
POST /api/auth/login      body: {login, password}                       → {token, user}
POST /api/auth/logout     header: Authorization: Bearer <token>         → 204
GET  /api/auth/me         header: Authorization: Bearer <token>         → {user}
POST /api/invites         header: Authorization (qualquer user logado)  → {code, expires_at}
```

- [ ] **Step 1: Escrever testes que falham**

```go
// auth_test.go
func TestRegisterWithValidInvite(t *testing.T)   // 201 + user no body
func TestRegisterWithoutInvite(t *testing.T)     // 403
func TestRegisterWithUsedInvite(t *testing.T)    // 409
func TestLoginCorrect(t *testing.T)              // 200 + token
func TestLoginWrongPassword(t *testing.T)        // 401
func TestGetMeValidToken(t *testing.T)           // 200 + user
func TestGetMeExpiredToken(t *testing.T)         // 401
func TestGetMeInvalidToken(t *testing.T)         // 401
```

Usar `httptest.NewServer` com DB SQLite em memória (`file::memory:?cache=shared`).

- [ ] **Step 2: Rodar testes e confirmar falha**

```
cd server && go test ./internal/auth/...
```
Expected: `FAIL` — funções não existem.

- [ ] **Step 3: Implementar DB migrations**

`db.Open` cria tabelas se não existirem:

```sql
CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  login TEXT UNIQUE NOT NULL,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id),
  expires_at DATETIME NOT NULL
);
CREATE TABLE IF NOT EXISTS invites (
  code TEXT PRIMARY KEY,
  created_by TEXT REFERENCES users(id),
  used_by TEXT REFERENCES users(id),
  used_at DATETIME,
  expires_at DATETIME NOT NULL
);
```

- [ ] **Step 4: Implementar auth service + handlers**

`RegisterUser(db, login, email, password, inviteCode) (*User, error)` — valida convite não usado e não expirado, faz hash argon2id, insere user, marca invite como usado.

`Login(db, login, password) (token string, *User, error)` — verifica senha, cria session com UUID, expires_at = now+30d.

`GetUserByToken(db, token) (*User, error)` — retorna 401 se expirado ou não encontrado.

- [ ] **Step 5: Rodar testes e confirmar verde**

```
cd server && go test ./internal/auth/... -v
```
Expected: todos PASS.

- [ ] **Step 6: Commit**

```bash
git add server/
git commit -m "feat(server): scaffold Go + SQLite + auth (register/login/session)

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 2: Servidor — convites + amigos

**Files:**
- Create: `server/internal/invite/model.go`, `service.go`, `handler.go`
- Create: `server/internal/friend/model.go`, `service.go`, `handler.go`
- Modify: `server/internal/router/router.go` — adicionar rotas de invites e friends
- Test: `server/internal/invite/invite_test.go`, `server/internal/friend/friend_test.go`

**Interfaces:**
- Consumes: `auth.RequireAuth`, `auth.UserFromContext`, `db.Open`
- Produces:
  - `GET /api/invites/:code` — info pública do convite (usado para tela de cadastro mostrar "convidado por X")
  - `POST /api/friends/request` body `{login}` → 201
  - `GET /api/friends` → lista com status (pending/accepted)

**Endpoints:**

```
GET  /api/invites/:code          public  → {created_by_login, valid: bool}
POST /api/friends/request        auth    body: {login}  → 201
GET  /api/friends                auth    → [{user, status}]
POST /api/friends/:id/accept     auth    → 200
POST /api/friends/:id/reject     auth    → 200
```

- [ ] **Step 1: Escrever testes que falham**

```go
// invite_test.go
func TestGetInviteInfo_valid(t *testing.T)       // 200, valid:true
func TestGetInviteInfo_used(t *testing.T)        // 200, valid:false

// friend_test.go
func TestRequestFriend_found(t *testing.T)       // 201
func TestRequestFriend_notFound(t *testing.T)    // 404
func TestAcceptFriend(t *testing.T)              // 200, status=accepted
func TestListFriends(t *testing.T)               // lista com pendente + aceito
```

- [ ] **Step 2: Rodar e confirmar falha**

```
cd server && go test ./internal/invite/... ./internal/friend/...
```
Expected: FAIL.

- [ ] **Step 3: Implementar invite handler (somente GET /:code — criação já está na Task 1)**

Apenas lê o invite e retorna `{created_by_login, valid}`.

- [ ] **Step 4: Implementar friend service + handlers**

Tabela de amizades:

```sql
CREATE TABLE IF NOT EXISTS friendships (
  id TEXT PRIMARY KEY,
  requester_id TEXT NOT NULL REFERENCES users(id),
  addressee_id TEXT NOT NULL REFERENCES users(id),
  status TEXT NOT NULL DEFAULT 'pending',  -- pending | accepted | rejected
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
  UNIQUE(requester_id, addressee_id)
);
```

`RequestFriend(db, requesterID, targetLogin) error` — resolve login → id, insere friendship.

- [ ] **Step 5: Rodar e confirmar verde**

```
cd server && go test ./internal/invite/... ./internal/friend/... -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add server/internal/invite/ server/internal/friend/ server/internal/router/
git commit -m "feat(server): invites info + friends request/accept

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 3: Servidor — grupos, canais, mensagens e WebSocket

**Files:**
- Create: `server/internal/group/model.go`, `service.go`, `handler.go`
- Create: `server/internal/channel/model.go`, `service.go`, `handler.go`
- Create: `server/internal/message/model.go`, `service.go`, `handler.go`
- Create: `server/internal/ws/hub.go`, `client.go`
- Modify: `server/internal/router/router.go` — adicionar rotas e WS
- Test: `server/internal/group/group_test.go`, `server/internal/message/message_test.go`, `server/internal/ws/ws_test.go`

**Interfaces:**
- Consumes: `auth.RequireAuth`, `auth.UserFromContext`, `db.Open`
- Produces:
  - `ws.NewHub() *Hub` — hub singleton
  - `hub.Run()` — goroutine de broadcast
  - `hub.BroadcastToChannel(channelID string, msg []byte)` — usado pelo message handler
  - `GET /ws?token=<token>` — upgrade WebSocket, autentica pelo token na query string

**Endpoints:**

```
POST /api/groups                    auth  body:{name}           → {group}
GET  /api/groups                    auth                        → [{group}]
POST /api/groups/:id/channels       auth  body:{name,type}      → {channel}
GET  /api/groups/:id/channels       auth                        → [{channel}]
GET  /api/channels/:id/messages     auth  ?before=<id>&limit=50 → [{message}]
POST /api/channels/:id/messages     auth  body:{text}           → {message} + broadcast WS
GET  /ws                            query:token=<token>         → WebSocket upgrade
```

Evento WS enviado a todos os membros do canal quando mensagem é postada:

```json
{"type":"message","channel_id":"...","message":{...}}
```

- [ ] **Step 1: Escrever testes que falham**

```go
// group_test.go
func TestCreateGroup(t *testing.T)             // 201 + group
func TestListGroups_onlyMine(t *testing.T)     // lista só grupos do user

// message_test.go
func TestPostMessage(t *testing.T)             // 201 + message no body
func TestListMessages_paginated(t *testing.T)  // 50 mensagens, cursor before

// ws_test.go
func TestWSAuthByToken(t *testing.T)           // conecta com token válido → 101
func TestWSRejectInvalidToken(t *testing.T)    // token inválido → 401 antes do upgrade
func TestWSBroadcast(t *testing.T)             // A posta mensagem, B recebe via WS
```

Para `TestWSBroadcast`: criar 2 conexões WS no mesmo canal, postar mensagem por HTTP como A, ler evento em B dentro de 1 s.

- [ ] **Step 2: Rodar e confirmar falha**

```
cd server && go test ./internal/group/... ./internal/message/... ./internal/ws/...
```
Expected: FAIL.

- [ ] **Step 3: Implementar tabelas**

```sql
CREATE TABLE IF NOT EXISTS groups (
  id TEXT PRIMARY KEY, name TEXT NOT NULL,
  owner_id TEXT REFERENCES users(id),
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS group_members (
  group_id TEXT REFERENCES groups(id),
  user_id TEXT REFERENCES users(id),
  PRIMARY KEY(group_id, user_id)
);
CREATE TABLE IF NOT EXISTS channels (
  id TEXT PRIMARY KEY, group_id TEXT REFERENCES groups(id),
  name TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'text',
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY, channel_id TEXT REFERENCES channels(id),
  user_id TEXT REFERENCES users(id),
  text TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

- [ ] **Step 4: Implementar hub WS + handlers**

Hub mantém `map[channelID]map[*Client]bool`. `BroadcastToChannel` serializa e envia a todos os clientes do canal. O handler `GET /ws` autentica pelo token na query string (Bearer não está disponível no handshake WS), faz upgrade com `gorilla/websocket`, registra o client no hub.

- [ ] **Step 5: Rodar e confirmar verde**

```
cd server && go test ./... -v
```
Expected: todos PASS (suite completa).

- [ ] **Step 6: Commit**

```bash
git add server/internal/group/ server/internal/channel/ server/internal/message/ server/internal/ws/
git commit -m "feat(server): groups, channels, messages, WebSocket broadcast

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 4: Cliente Electron — tela de login

**Files:**
- Create: `client/package.json` (Electron 34, scripts start/build)
- Create: `client/main.js` — janela de login → janela principal após auth
- Create: `client/preload.js` — expõe `bridge.api(method, path, body)` e `bridge.openMain()`
- Create: `client/src/login.html` — cópia funcional de `design/login-c.html` com JS de API
- Create: `client/src/js/api.js` — fetch wrapper com base URL configurável

**Interfaces:**
- Consumes: `POST /api/auth/login`, `POST /api/auth/register`, `GET /api/invites/:code`
- Produces:
  - `api.login(login, password)` → `{token, user}` ou lança erro
  - `api.register(login, email, password, inviteCode)` → `{user}` ou lança erro
  - Token salvo em `localStorage` via preload (isolado por origem do Electron)

**Verificação (observacional — Electron GUI):**

- [ ] **Step 1: Criar estrutura do cliente**

`package.json` com `"main": "main.js"`, `"start": "electron ."`. Dependência `electron@34`.

- [ ] **Step 2: Adaptar login-c.html**

Copiar `design/login-c.html` para `client/src/login.html`. Substituir `<form onsubmit>` placeholder por chamadas reais a `window.api.login()` / `window.api.register()`. Comportamentos:
- Aba Entrar: submit → `api.login()` → salvar token → `bridge.openMain()`; erro → shake no cartão + socket.bad.
- Aba Criar conta: se URL tem `?invite=<code>`, auto-preencher e mostrar "convidado por X" (GET /api/invites/:code); submit → `api.register()` → auto-login.
- Plug/cabo: animação existente do mockup, sem alterar — só garantir que `socket.on` acende no sucesso e `socket.bad` no erro.

- [ ] **Step 3: Rodar e verificar**

```
cd client && npm start
```
Expected: janela de login abre; consegue fazer login com usuário criado manualmente no SQLite de dev; animação do cabo funciona; erro de senha errada mostra coral.

- [ ] **Step 4: Commit**

```bash
git add client/
git commit -m "feat(client): Electron shell + tela de login funcional

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 5: Cliente Electron — interface principal

**Files:**
- Create: `client/src/app.html` — cópia funcional de `design/interface-v2.html` com JS ligado à API
- Create: `client/src/js/ws.js` — WebSocket client com reconexão exponencial
- Modify: `client/main.js` — `openMain()` abre janela app.html (1280×800), fecha login.html
- Modify: `client/preload.js` — expõe `bridge.ws` (connect, send, onMessage, onReconnect)

**Interfaces:**
- Consumes: `GET /api/groups`, `GET /api/groups/:id/channels`, `GET /api/channels/:id/messages`, `POST /api/channels/:id/messages`, `GET /api/friends`, `GET /ws?token=`
- Produces: interface completa carregada com dados reais do servidor

**ws.js — contrato de reconexão:**

```js
// backoff: 1s → 2s → 4s → … → máx 30s
class DenundikeWS {
  connect(token)   // abre conexão, registra handlers
  send(obj)        // JSON.stringify + ws.send
  onMessage(cb)    // cb(parsedObj)
  onReconnect(cb)  // cb(attemptN) — para mostrar indicador na UI
}
```

**Verificação (observacional):**

- [ ] **Step 1: Adaptar interface-v2.html**

Copiar `design/interface-v2.html` para `client/src/app.html`. Ligar:
- Sidebar esquerda: lista grupos via `GET /api/groups`; clique → carrega canais.
- Lista de canais: clique → `GET /api/channels/:id/messages` popula painel central.
- Input de mensagem: Enter → `POST /api/channels/:id/messages`.
- WS: ao receber `{type:"message", channel_id}`, se for o canal ativo, adiciona bolha sem reload.
- Rodapé: nome/login do usuário logado (do `localStorage`).
- Área central: deixar vazia por enquanto (navegador compartilhado vem na Phase 2).

- [ ] **Step 2: Implementar ws.js com reconexão**

Backoff exponencial: `delay = Math.min(1000 * 2**attempt, 30000)`. `onReconnect` dispara após cada tentativa.

- [ ] **Step 3: Rodar e verificar fluxo completo**

```
cd client && npm start
```
Verificar:
- Login → app abre, grupos listados.
- Criar canal → digitar mensagem → aparece na tela.
- Abrir segundo cliente (segunda instância Electron ou outro PC) → mensagem aparece em tempo real via WS.
- Desligar servidor por 5 s → reconexão automática.

- [ ] **Step 4: Commit**

```bash
git add client/src/app.html client/src/js/ws.js client/main.js client/preload.js
git commit -m "feat(client): interface principal com chat em tempo real e reconexão WS

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 6: Docker + deploy Coolify + DNS

**Files:**
- Create: `server/Dockerfile`
- Create: `server/.env.example`
- Create: `server/docker-compose.yml` (referência; Coolify usa o Dockerfile)

**Interfaces:**
- Consumes: Coolify na VPS (painel em `https://painel.estudiofachada.com.br`), Cloudflare DNS para `estudiofachada.com.br`

**Variáveis de ambiente (`.env.example`):**

```
DB_PATH=/data/denundike.db
JWT_SECRET=changeme
CORS_ORIGINS=app://denundike,file://
PORT=8080
ADMIN_INVITE_CODE=changeme   # convite seed para o primeiro usuário
```

- [ ] **Step 1: Dockerfile**

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /denundike ./cmd/denundike

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /denundike .
VOLUME /data
EXPOSE 8080
CMD ["./denundike"]
```

- [ ] **Step 2: Build local e smoke test**

```bash
cd server
docker build -t denundike-server .
docker run --rm -e DB_PATH=/tmp/test.db -e PORT=8080 -e ADMIN_INVITE_CODE=testcode -p 8080:8080 denundike-server
curl http://localhost:8080/api/auth/me  # deve retornar 401
```
Expected: `{"error":"unauthorized"}` com status 401.

- [ ] **Step 3: DNS — CONFIRMAR COM O DONO ANTES DE CRIAR**

Criar registro A no Cloudflare para `denundike.estudiofachada.com.br` apontando para `179.199.157.16` (IP já confirmado da VPS). **Não executar sem confirmação explícita do dono.**

- [ ] **Step 4: Deploy no Coolify**

No painel `https://painel.estudiofachada.com.br`:
- Novo resource → Docker image ou Git source → apontar para `server/`
- Domínio: `denundike.estudiofachada.com.br` (TLS automático pelo Traefik do Coolify)
- Volume `/data` persistente
- Variáveis de ambiente (valores reais de `C:\Users\brizao\Documents\.brizao\.secrets`)
- Limite: `memory: 256m`, `cpus: 0.5`

- [ ] **Step 5: Verificar servidor ao vivo**

```bash
curl https://denundike.estudiofachada.com.br/api/auth/me
```
Expected: `{"error":"unauthorized"}` com status 401 e TLS válido.

- [ ] **Step 6: Commit**

```bash
git add server/Dockerfile server/.env.example server/docker-compose.yml
git commit -m "feat(infra): Dockerfile + env.example para deploy Coolify

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

---

### Task 7: Auto-update — cliente e servidor

**Goal:** ao abrir o app, o cliente verifica e aplica atualizações automaticamente. O servidor atualiza via redeploy automático no Coolify quando há push no GitHub.

**Files:**
- Modify: `client/package.json` — adicionar `electron-updater`, `electron-builder`
- Create: `client/electron-builder.yml` — config de build: Windows NSIS + Linux AppImage + publish para GitHub Releases
- Modify: `client/main.js` — chamar `autoUpdater.checkForUpdatesAndNotify()` após `app.whenReady`
- Modify: `client/src/app.html` — mostrar banner discreto quando update disponível (evento `update-available`)
- Create: `.github/workflows/release.yml` — CI: build + publish no GitHub Releases ao criar tag `v*`

**Interfaces:**
- Consumes: `electron-updater` (parte de `electron-builder`), GitHub Releases API (token no CI)
- Produces: instaladores `.exe` (Windows NSIS) e `.AppImage` (Linux) publicados em GitHub Releases

**Fluxo:**
1. Você faz push + cria tag `v0.1.0` no GitHub
2. GitHub Actions compila o cliente para Windows e Linux, publica os assets no Release
3. Coolify detecta o push e faz redeploy do servidor automaticamente (webhook já configurado)
4. Usuário abre o app → `autoUpdater` checa `latest.yml` no GitHub Releases → baixa em background → notifica com banner → usuário clica "Reiniciar" → app reinicia atualizado

**Verificação (observacional):**

- [ ] **Step 1: Instalar dependências e configurar electron-builder**

```bash
cd client
npm install --save-dev electron-builder electron-updater
```

Criar `client/electron-builder.yml`:
```yaml
appId: com.denundike.app
productName: Denundike
publish:
  provider: github
  owner: <seu-github-user>
  repo: denundike
win:
  target: nsis
linux:
  target: AppImage
```

- [ ] **Step 2: Adicionar autoUpdater ao main.js**

Em `main.js`, após `app.whenReady()`:
```js
const { autoUpdater } = require('electron-updater')
autoUpdater.checkForUpdatesAndNotify()
autoUpdater.on('update-available', () => {
  mainWin?.webContents.send('update-available')
})
autoUpdater.on('update-downloaded', () => {
  mainWin?.webContents.send('update-downloaded')
})
```

Expor `bridge.onUpdateAvailable(cb)` e `bridge.installUpdate()` via `preload.js`. `installUpdate` chama `autoUpdater.quitAndInstall()`.

- [ ] **Step 3: Banner de update em app.html**

Quando `bridge.onUpdateAvailable` disparar, mostrar banner fixo no topo: "Nova versão disponível — baixando…". Quando `update-downloaded`, trocar por "Pronto! Reiniciar para atualizar" com botão que chama `bridge.installUpdate()`.

- [ ] **Step 4: GitHub Actions — release workflow**

Criar `.github/workflows/release.yml`:
```yaml
on:
  push:
    tags: ['v*']
jobs:
  build:
    runs-on: windows-latest  # NSIS requer Windows
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - run: npm ci
        working-directory: client
      - run: npm run build
        working-directory: client
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  build-linux:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-node@v4
        with: { node-version: '20' }
      - run: npm ci
        working-directory: client
      - run: npm run build:linux
        working-directory: client
        env:
          GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Adicionar scripts em `client/package.json`:
```json
"build": "electron-builder --win",
"build:linux": "electron-builder --linux"
```

- [ ] **Step 5: Coolify — webhook de redeploy automático**

No painel Coolify, configurar webhook de deploy para o repositório Git. Quando há push na branch `main`, Coolify faz pull e redeploy automático do servidor. Verificar se já está configurado antes de criar.

- [ ] **Step 6: Commit**

```bash
git add client/package.json client/electron-builder.yml client/main.js client/preload.js client/src/app.html .github/workflows/release.yml
git commit -m "feat(update): auto-update Electron via GitHub Releases + redeploy Coolify

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>"
```

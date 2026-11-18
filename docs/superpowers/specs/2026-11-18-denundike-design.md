# Denundike: desenho do projeto

Data: 2026-11-18. Estado: rascunho para aprovação do dono.

## 1. Intenção

O dono quer um app de conversa parecido com o Discord, chamado **Denundike**, com chamadas de voz e vídeo **P2P** e um **navegador compartilhado**: uma pessoa abre um site (YouTube, Spotify, qualquer um) e os outros do canal veem e ouvem a página ao mesmo tempo, e podem controlá-la se o anfitrião deixar. Também quer chat, grupos, amigos e convites. Hoje são **2 usuários** (o dono e mais uma pessoa), todos em **Windows**. O servidor roda na VPS que o dono já tem (Hostinger KVM 2, com Coolify).

Por que existe: ter um lugar próprio, leve e rápido para conversar e assistir/usar sites juntos, sem depender de plataformas de terceiros.

## 2. Objetivos, não objetivos, anti-objetivos

**Objetivos**
- Tela única como o Discord: grupos, canais de texto e voz, membros, chat, call e navegador compartilhado na mesma janela.
- Voz e vídeo direto entre os usuários (P2P), com o servidor só para cadastro, sinalização e histórico.
- Dois modos de compartilhamento no centro: **site/navegador** (abas, endereço, controle remoto opcional) e **tela/janela do PC**.
- Tela cheia local: quem clica vê a página em tela cheia, os outros continuam na interface normal.
- Contas completas: cadastro, login, recuperar senha, perfil e status, amigos (por @login), convites por link e por @login, configurações de voz e vídeo.

**Não objetivos (por enquanto)**
- Salas grandes (mais de 4 a 6 pessoas): exigiria um SFU.
- **Acesso pelo site (futuro):** o dono quer deixar o Denundike disponível também por uma página web, para quem quiser entrar sem instalar. Ele resolve isso depois; não entra no MVP. Observação técnica: no site dá para conversar e ver o navegador compartilhado, mas quem **hospeda** o navegador precisa do app, porque o navegador roda dentro dele.
- App de celular, versão web pura, bots, moderação avançada, criptografia ponta a ponta do chat.
- Navegador rodando no servidor.

**Anti-objetivos (o projeto falha se...)**
- Parecer um clone cinza do Discord ou um template genérico.
- O áudio do navegador compartilhado misturar com a voz da call causando eco, ou vazar o som do resto do PC (Spotify de fundo etc.) sem querer.
- Gastar a VPS compartilhada (que também roda outros projetos do dono) a ponto de derrubá-los.

## 3. Restrições

- Servidor em **Go** (pedido do dono: rapidez e estabilidade).
- VPS Hostinger KVM 2 com Coolify. Especificações prováveis: 2 vCPU, 8 GB de RAM, 100 GB de disco (a confirmar na máquina; a URL do painel e o IP não estão registrados na memória).
- **VPS (verificado em 2026-11-18 por DNS e teste de portas):** os subdomínios `dados.`, `stats.`, `homolog.` e o domínio raiz de `estudiofachada.com.br` resolvem para **179.199.157.16** (IP direto, sem proxy). As portas TCP 22 (SSH), 80 e 443 respondem; a 8000 (painel padrão do Coolify) não responde de fora, então o painel está atrás de outro endereço ou de túnel. Painel Coolify 4.4.6 em `https://painel.estudiofachada.com.br` (achado em `presenca-digital/INFRAESTRUTURA.md`); a API respondeu com o token. Já rodam na VPS: site, homologação, all-mcp-api, Metabase (heap 1 GB), Umami, Twenty (com worker, Redis e Postgres), dois Postgres e dois cloudflared. Com tudo isso em 8 GB, o servidor do Denundike precisa ser leve. O **UFW só libera 22, 80 e 443**: o TURN exige abrir uma faixa UDP no UFW (e talvez no firewall da Hostinger), o que é uma mudança de segurança a confirmar com o dono. O uso real de RAM e CPU ainda não foi medido (o acesso SSH foi bloqueado pelas permissões desta sessão).
- O Denundike **não tem domínio próprio** por enquanto (decisão do dono: só o app `.exe`). O dono **autorizou o subdomínio `denundike.estudiofachada.com.br`** (2026-11-18) para o servidor, com TLS (certificado pelo Coolify/Traefik). O registro DNS ainda **não foi criado**: é um passo da fase do servidor, no Cloudflare, e pede confirmação antes de mexer.
- Usuários em Windows. Upload do anfitrião é o limite de qualidade (a testar, o dono diz que "vamos testar").
- Nada de segredo no repositório. As credenciais ficam em `C:\Users\brizao\Documents\.brizao\.secrets`.
- Marca: fundo escuro `#090C0D` a `#222B2F`, destaque limão `#C8F560` (texto escuro sobre ele), coral `#FF5C4D` para erro. Fontes dos mockups: Bricolage Grotesque, Onest, DM Mono/JetBrains Mono.

## 4. Abordagens consideradas

| | Abordagem | Resumo | Troca |
|---|---|---|---|
| **A (escolhida, decisão minha)** | Servidor Go + app **Electron** | Go faz auth, chat, grupos, sinalização e TURN (Pion). O app usa o Chromium e o WebRTC do Electron, captura a aba/tela, e injeta o controle do convidado via CDP. | Mais rápido para ter o MVP e mais estável para o navegador compartilhado. App maior (~150 MB), cliente em JavaScript. |
| B | Servidor Go + **Wails** (WebView2) | App leve, interface em Go e HTML. O navegador compartilhado seria um Chromium separado controlado por chromedp. | Capturar só o áudio de uma aba é difícil, e os recursos de captura são mais limitados. Mais pesquisa no começo. |
| C | Tudo em Go (Pion também no cliente) | Go puro. | Exige codificar vídeo em tempo real em Go (cgo, bibliotecas nativas). O mais trabalhoso. |

O dono não escolheu entre A, B e C. Ele aprovou a interface e o login e seguiu com os testes em Electron. **A escolha de A é minha, e fica sujeita a correção.**

Sub-decisão do navegador compartilhado (a validar):
- **Navegador embutido do Electron** (`WebContentsView`): mais simples, com captura do frame da aba. O áudio vem por *loopback* do Windows, que captura o som de **todo** o sistema.
- **Chromium real com extensão do Denundike** (`chrome.tabCapture`): áudio isolado da aba e extensões completas (inclusive AdGuard). É mais trabalho de montagem.
- Plano: começar com o Electron e migrar o navegador compartilhado para o Chromium com extensão se o áudio do loopback for um problema real.

**DRM (decisão do dono, 2026-11-18):** o navegador compartilhado tem que funcionar para tudo, incluindo Netflix, Spotify e similares. Não é negociável. Caminho principal a testar na Fase 0: Google Chrome com Widevine, aceleração de hardware desligada nesse perfil. Se a captura sair preta com Chrome, testar o fork castLabs do Electron (tem Widevine). Modo sincronizado (cada um com o seu, ações sincronizadas) é plano Z de último caso, caso nenhuma captura funcione tecnicamente.

## 5. Estado dos testes (spike descartável em `spike/`)

- O Electron abre o YouTube em uma aba (WebContentsView). Funciona.
- Sem bloqueador, aparece anúncio do YouTube.
- A extensão AdGuard (v5.5.x, MV3) **carrega com avisos e um erro de execução** no Electron (`tabs.create`, permissões desconhecidas). Suporte parcial. O dono viu uma sessão sem anúncio, mas isso não prova que o bloqueio funciona.
- A biblioteca `@cliqz/adblocker-electron` **não é compatível** com o Electron 45 (provavelmente causou a tela branca). Descartada.
- **Ainda não testado:** o compartilhamento da aba para o convidado por WebRTC, o áudio por loopback (eco/vazamento), fps, atraso e banda, e o Spotify (precisa de DRM Widevine, que o Electron padrão não tem; existe o fork da castLabs).
- Atenção ao rodar o Electron em terminais do VS Code: a variável `ELECTRON_RUN_AS_NODE` precisa estar desligada (`unset ELECTRON_RUN_AS_NODE`).

## 6. Decisões tomadas pelo dono (com a interface aprovada)

- **Interface:** `design/interface-v2.html`, aprovada ("gostei da interface"). Layout A (navegador grande no centro, chat do lado direito) com alternância para o layout B (chat no centro e navegador como painel acima). Centro com três estados: site, tela compartilhada e vazio. Seletor "o que compartilhar" (tela inteira, janela, site). Tela cheia local. Rodapé com usuário logado (avatar, nome, @login, status). Membros online e offline. Tela Amigos (Todos, Online, Pendentes, Adicionar). Convite por link e por @login. Configurações de conta e de voz e vídeo.
- **Login:** `design/login-c.html`, aprovado. Ideia "Sinal": onda de osciloscópio, manchete que muda a cada tela, cartão com abas Entrar/Criar conta, e um cabo com plugue que só conecta ao enviar (estica e encaixa; em erro, não conecta e volta em elástico). Cobre Entrar, Criar conta e Recuperar senha (e-mail, código de 6 dígitos, nova senha, sucesso).
- **Nome:** Denundike (substituiu Jemunje).
- **DRM (Spotify, Netflix):** o dono quer que funcione ("se não suportar, fazemos suportar"). É requisito a perseguir, não um "não suporta". Caminho a testar na Fase 0: **Google Chrome** de verdade no navegador compartilhado (o Chromium de código aberto e o Electron comum não têm Widevine), com a aceleração de hardware desligada nesse perfil, o que costuma liberar a captura (a confirmar). O Chrome recente também restringe carregar extensões por linha de comando (a testar).
- **Resultado esperado do navegador compartilhado (2026-11-18):** é **um navegador só que todos veem ao mesmo tempo e em que todos podem mexer** (clicar, rolar, digitar). Por padrão todos têm controle; o anfitrião pode restringir para "só assistir". O navegador roda **dentro do aplicativo, no PC de quem hospeda, e chega aos outros por P2P** (confirmado pelo dono em 2026-11-18). Não roda na VPS. Não é captura da tela inteira do PC: é só o navegador do app.
- **Cadastro:** **só por convite** (2026-11-18). A pessoa recebe o convite e consegue criar a conta com ele.
- **Instalação e atualização (2026-11-18):** instalador `.exe`; a atualização é feita pelo próprio aplicativo (auto-update), sem o usuário baixar de novo.
- **Qualidade do compartilhamento (2026-11-18):** foco em **1080p**, com opção de 720p. O usuário escolhe **30 ou 60 fps**.
- **Navegador (2026-11-18):** o dono diz que o **Chromium é melhor** (extensões completas como o AdGuard e áudio isolado da aba). Ele quer que o desenvolvimento seja **rápido**. Isso torna o áudio isolado e as extensões uma preferência forte, não um extra; a forma de entregá-los (Chromium embutido do Electron, ou Chromium completo com extensão) será decidida no teste da Fase 0.
- Os mockups são só visuais. Eles não foram abertos e testados em todos os estados (arrasto do plugue, celular, movimento reduzido) pelos agentes.

## 7. Arquitetura proposta (minha decisão, a refinar)

**Servidor (Go, na VPS via Coolify, container Docker)**
- API HTTP + WebSocket: cadastro, login, recuperação de senha (e-mail com código), perfil e status, amigos, grupos, canais, convites, mensagens (com histórico).
- Sinalização WebRTC (offer/answer/ICE) pelo WebSocket, por canal de voz.
- **TURN** (Pion TURN ou coturn) com credenciais temporárias, para quando a conexão direta falhar (NAT). Exige uma faixa de portas UDP liberada na VPS e no firewall, a verificar (o Coolify costuma expor só 80 e 443).
- Banco de dados: para 2 usuários, SQLite basta; Postgres se o dono quiser crescer. **A definir pelo construtor** (marcado como decisão dele).
- Senhas com argon2id, sessões por token com expiração, limites de tentativas de login.
- Limite de memória e CPU do container, para não afetar os outros apps da VPS.

**Cliente (Electron, Windows)**
- Interface conforme os mockups (HTML/CSS/JS). Tecnologia do front a definir pelo construtor, mas **a aparência vem dos mockups aprovados**.
- WebRTC no renderer: voz, câmera, tela, e a aba do navegador compartilhado.
- Navegador compartilhado: várias `WebContentsView` (abas), uma visível por vez; captura do frame da aba e do áudio; controle do convidado enviado por *data channel* e aplicado ao navegador via CDP (`Input.dispatchMouseEvent`, `Input.dispatchKeyEvent`).
- Perfil de navegador separado e limpo por sala; botões distintos de "só assistir" e "pode controlar".
- Bloqueio de anúncios: usar uma extensão (AdGuard) só funciona se o navegador for o Chromium real. No Electron, a alternativa é bloquear no nível da rede da sessão (lista de filtros). **A definir após os testes.**

## 8. Faseamento sugerido

1. **Base:** servidor Go, cadastro/login/recuperar senha, grupos, canais, mensagens, convites, amigos. Cliente com a interface real e login C.
2. **Voz 1:1** por WebRTC com TURN de reserva.
3. **Compartilhar tela/janela** com áudio.
4. **Navegador compartilhado** com abas, controle remoto e tela cheia local.
5. **Acabamento:** sincronizar player, extensão de bloqueio, status e configurações.
6. **Depois (se precisar):** SFU para salas maiores.

Fase 0, antes de tudo: terminar o spike para validar compartilhamento da aba, áudio, banda e Widevine.

## 9. Riscos a validar cedo

- Áudio por loopback: mistura com a voz e vaza o som do resto do PC.
- Banda de upload do anfitrião com vídeo 1080p.
- Portas UDP do TURN na VPS e no firewall da Hostinger.
- DRM: Netflix e similares saem pretos na captura; Spotify exige Widevine.
- Segurança do controle remoto: um convidado controlando a sessão logada do anfitrião.

## 10. O que fica para o construtor decidir

Banco de dados (SQLite ou Postgres), formato do protocolo do WebSocket, tecnologia do front dentro do Electron, estrutura do repositório e dos testes, serviço de e-mail para recuperação de senha, política de sessão, e o desenho exato do TURN.

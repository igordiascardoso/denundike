package ws_test

import "testing"

func TestBrowserSessionLifecycle(t *testing.T) {
	env := setupVoice(t)
	hostTok, hostID := env.member(t, "host")
	guestTok, _ := env.member(t, "guest")
	h := dialWS(t, env.srv, hostTok)
	g := dialWS(t, env.srv, guestTok)
	send(t, h, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, h, "voice_peers")
	send(t, g, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, g, "voice_peers")

	// convidado não inicia por cima de ninguém, mas qualquer um na sala pode iniciar se não houver sessão
	send(t, h, map[string]any{"type": "browser_start", "channel_id": env.voiceID, "data": map[string]any{"url": "https://exemplo.com/a"}})
	st := expect(t, g, "browser_state")
	if st["active"] != true || st["host_id"] != hostID || st["url"] != "https://exemplo.com/a" {
		t.Fatalf("browser_state inesperado: %v", st)
	}
	expect(t, h, "browser_state")

	// segunda sessão é recusada
	send(t, g, map[string]any{"type": "browser_start", "channel_id": env.voiceID})
	if e := expect(t, g, "browser_error"); e["error"] != "session active" {
		t.Fatalf("erro inesperado: %v", e)
	}

	// só o anfitrião publica a URL atual
	send(t, g, map[string]any{"type": "browser_url", "channel_id": env.voiceID, "data": map[string]any{"url": "https://hack.com"}})
	expectNone(t, h, "browser_state")
	send(t, h, map[string]any{"type": "browser_url", "channel_id": env.voiceID, "data": map[string]any{"url": "https://exemplo.com/b"}})
	if s := expect(t, g, "browser_state"); s["url"] != "https://exemplo.com/b" {
		t.Fatalf("url não sincronizou: %v", s)
	}

	// só o anfitrião encerra
	send(t, g, map[string]any{"type": "browser_stop", "channel_id": env.voiceID})
	expectNone(t, h, "browser_state")
	send(t, h, map[string]any{"type": "browser_stop", "channel_id": env.voiceID})
	if s := expect(t, g, "browser_state"); s["active"] != false {
		t.Fatalf("sessão devia encerrar: %v", s)
	}
}

func TestBrowserInputRelayedToHostOnlyWhenValid(t *testing.T) {
	env := setupVoice(t)
	hostTok, _ := env.member(t, "host")
	g1Tok, g1ID := env.member(t, "ana")
	g2Tok, g2ID := env.member(t, "bia")
	h := dialWS(t, env.srv, hostTok)
	a := dialWS(t, env.srv, g1Tok)
	b := dialWS(t, env.srv, g2Tok)
	send(t, h, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, h, "voice_peers")
	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, a, "voice_peers")
	send(t, b, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, b, "voice_peers")
	send(t, h, map[string]any{"type": "browser_start", "channel_id": env.voiceID})
	expect(t, a, "browser_state")
	expect(t, b, "browser_state")

	// dois convidados controlam ao mesmo tempo
	send(t, a, map[string]any{"type": "browser_input", "channel_id": env.voiceID,
		"data": map[string]any{"t": "down", "x": 0.25, "y": 0.5, "button": "left"}})
	send(t, b, map[string]any{"type": "browser_input", "channel_id": env.voiceID,
		"data": map[string]any{"t": "key_down", "key": "a", "code": "KeyA", "text": "a"}})
	got := map[string]string{}
	for i := 0; i < 2; i++ {
		m := expect(t, h, "browser_cmd")
		got[m["from"].(string)] = m["data"].(map[string]any)["t"].(string)
	}
	if got[g1ID] != "down" || got[g2ID] != "key_down" {
		t.Fatalf("host devia receber ambos os comandos: %v", got)
	}

	// cursor do convidado A aparece para B, com o login
	cur := expect(t, b, "browser_cursor")
	if cur["user_id"] != g1ID || cur["x"].(float64) != 0.25 {
		t.Fatalf("cursor inesperado: %v", cur)
	}

	// navegação válida chega; esquemas perigosos e lixo não chegam
	send(t, a, map[string]any{"type": "browser_nav", "channel_id": env.voiceID, "data": map[string]any{"action": "go", "url": "https://netflix.com"}})
	if m := expect(t, h, "browser_cmd"); m["kind"] != "nav" || m["data"].(map[string]any)["url"] != "https://netflix.com" {
		t.Fatalf("nav inesperado: %v", m)
	}
	for _, bad := range []map[string]any{
		{"action": "go", "url": "javascript:alert(1)"},
		{"action": "go", "url": "file:///C:/Windows/win.ini"},
		{"action": "go", "url": "chrome://settings"},
		{"action": "go", "url": ""},
		{"action": "eval", "url": "https://a.com"},
	} {
		send(t, a, map[string]any{"type": "browser_nav", "channel_id": env.voiceID, "data": bad})
	}
	for _, bad := range []map[string]any{
		{"t": "move", "x": 1.5, "y": 0.2},
		{"t": "down", "x": 0.2, "y": 0.2, "button": "x"},
		{"t": "wheel", "x": 0.2, "y": 0.2, "dy": 99999},
		{"t": "exec", "x": 0.2, "y": 0.2},
		{"t": "key_down", "key": ""},
		{"t": "key_down", "key": "a", "text": "123456789"},
	} {
		send(t, a, map[string]any{"type": "browser_input", "channel_id": env.voiceID, "data": bad})
	}
	expectNone(t, h, "browser_cmd")
}

func TestBrowserForbiddenOutsideRoom(t *testing.T) {
	env := setupVoice(t)
	hostTok, _ := env.member(t, "host")
	outTok, _ := env.outsider(t, "intruso")
	memberTok, _ := env.member(t, "membro")
	h := dialWS(t, env.srv, hostTok)
	o := dialWS(t, env.srv, outTok)
	m := dialWS(t, env.srv, memberTok)
	send(t, h, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, h, "voice_peers")
	send(t, h, map[string]any{"type": "browser_start", "channel_id": env.voiceID})
	expect(t, h, "browser_state")

	// quem não é do grupo nem da sala não controla nada
	send(t, o, map[string]any{"type": "browser_input", "channel_id": env.voiceID, "data": map[string]any{"t": "move", "x": 0.1, "y": 0.1}})
	send(t, o, map[string]any{"type": "browser_nav", "channel_id": env.voiceID, "data": map[string]any{"action": "reload"}})
	send(t, o, map[string]any{"type": "browser_start", "channel_id": env.voiceID})
	// membro do grupo que não entrou na chamada também não
	send(t, m, map[string]any{"type": "browser_input", "channel_id": env.voiceID, "data": map[string]any{"t": "move", "x": 0.1, "y": 0.1}})
	send(t, m, map[string]any{"type": "browser_nav", "channel_id": env.voiceID, "data": map[string]any{"action": "reload"}})
	expectNone(t, h, "browser_cmd")
	if e := expect(t, o, "browser_error"); e["error"] != "not in call" {
		t.Fatalf("erro inesperado: %v", e)
	}
}

func TestBrowserEndsWhenHostLeavesAndLateJoinerSeesState(t *testing.T) {
	env := setupVoice(t)
	hostTok, _ := env.member(t, "host")
	aTok, _ := env.member(t, "ana")
	bTok, _ := env.member(t, "bia")
	h := dialWS(t, env.srv, hostTok)
	a := dialWS(t, env.srv, aTok)
	send(t, h, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, h, "voice_peers")
	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, a, "voice_peers")
	send(t, h, map[string]any{"type": "browser_start", "channel_id": env.voiceID, "data": map[string]any{"url": "https://exemplo.com"}})
	expect(t, a, "browser_state")

	// quem entra depois já recebe a sessão ativa
	b := dialWS(t, env.srv, bTok)
	send(t, b, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	if s := expect(t, b, "browser_state"); s["active"] != true || s["url"] != "https://exemplo.com" {
		t.Fatalf("entrante devia ver a sessão: %v", s)
	}

	// anfitrião sai → sessão acaba para todos
	send(t, h, map[string]any{"type": "voice_leave"})
	if s := expect(t, a, "browser_state"); s["active"] != false {
		t.Fatalf("sessão devia acabar: %v", s)
	}
}

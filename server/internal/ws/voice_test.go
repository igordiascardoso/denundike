package ws_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type voiceEnv struct {
	srv     *httptest.Server
	db      *sql.DB
	suffix  string
	groupID string
	voiceID string
	textID  string
	owner   string // token of the group owner
}

func httpGet(t *testing.T, srv *httptest.Server, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func setupVoice(t *testing.T) *voiceEnv {
	t.Helper()
	srv, database, _ := setupTestServer(t)
	env := &voiceEnv{srv: srv, db: database, suffix: fmt.Sprintf("%d", time.Now().UnixNano())}
	env.owner = loginUser(t, srv, database, "dono"+env.suffix)
	gResp := doPost(t, srv, "/api/groups", map[string]string{"name": "Voz"}, env.owner)
	var g map[string]any
	json.NewDecoder(gResp.Body).Decode(&g) //nolint:errcheck
	env.groupID, _ = g["id"].(string)
	_, b := httpGet(t, srv, "/api/groups/"+env.groupID+"/channels", env.owner)
	var chans []map[string]any
	json.Unmarshal(b, &chans) //nolint:errcheck
	for _, c := range chans {
		switch c["type"] {
		case "voice":
			env.voiceID, _ = c["id"].(string)
		case "text":
			env.textID, _ = c["id"].(string)
		}
	}
	if env.voiceID == "" || env.textID == "" {
		t.Fatalf("grupo novo deve ter canal de texto e de voz: %s", b)
	}
	return env
}

// member registers a user, adds them to the group and returns token and id.
func (e *voiceEnv) member(t *testing.T, name string) (token, id string) {
	t.Helper()
	token = loginUser(t, e.srv, e.db, name+e.suffix)
	if err := e.db.QueryRow(`SELECT id FROM users WHERE login = ?`, name+e.suffix).Scan(&id); err != nil {
		t.Fatalf("user id: %v", err)
	}
	e.db.Exec(`INSERT OR IGNORE INTO group_members (group_id, user_id) VALUES (?, ?)`, e.groupID, id) //nolint:errcheck
	return
}

func (e *voiceEnv) outsider(t *testing.T, name string) (token, id string) {
	t.Helper()
	token = loginUser(t, e.srv, e.db, name+e.suffix)
	e.db.QueryRow(`SELECT id FROM users WHERE login = ?`, name+e.suffix).Scan(&id) //nolint:errcheck
	return
}

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.DefaultDialer.Dial(wsURL(srv, "/ws?token="+token), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	if err := c.WriteJSON(v); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// expect reads frames until one of the given type arrives (2s budget).
func expect(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.SetReadDeadline(deadline) //nolint:errcheck
		_, data, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("esperando %q: %v", typ, err)
		}
		var m map[string]any
		json.Unmarshal(data, &m) //nolint:errcheck
		if m["type"] == typ {
			return m
		}
	}
}

// expectNone fails if a frame of the given type shows up within 300ms.
func expectNone(t *testing.T, c *websocket.Conn, typ string) {
	t.Helper()
	deadline := time.Now().Add(300 * time.Millisecond)
	for {
		c.SetReadDeadline(deadline) //nolint:errcheck
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		var m map[string]any
		json.Unmarshal(data, &m) //nolint:errcheck
		if m["type"] == typ {
			t.Fatalf("frame %q inesperado: %s", typ, data)
		}
	}
}

func TestVoiceJoinSignalLeave(t *testing.T) {
	env := setupVoice(t)
	aliceTok, aliceID := env.member(t, "alice")
	bobTok, bobID := env.member(t, "bob")

	a := dialWS(t, env.srv, aliceTok)
	b := dialWS(t, env.srv, bobTok)

	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	peers := expect(t, a, "voice_peers")
	if got := peers["peers"].([]any); len(got) != 0 {
		t.Fatalf("primeiro a entrar não deve ter pares: %v", got)
	}

	send(t, b, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	bp := expect(t, b, "voice_peers")["peers"].([]any)
	if len(bp) != 1 || bp[0].(map[string]any)["user_id"] != aliceID {
		t.Fatalf("bob deve ver alice na sala: %v", bp)
	}
	joined := expect(t, a, "voice_joined")
	if joined["user_id"] != bobID {
		t.Fatalf("alice deve ser avisada da entrada do bob: %v", joined)
	}

	// presença visível para o grupo todo, também via REST
	st := expect(t, a, "voice_state")
	if len(st["users"].([]any)) == 0 {
		t.Fatalf("voice_state vazio: %v", st)
	}
	code, body := httpGet(t, env.srv, "/api/groups/"+env.groupID+"/voice", aliceTok)
	var list []map[string]any
	json.Unmarshal(body, &list) //nolint:errcheck
	if code != 200 || len(list) != 1 || len(list[0]["users"].([]any)) != 2 {
		t.Fatalf("GET voice = %d %s", code, body)
	}

	// sinalização encaminhada com o payload intacto
	send(t, a, map[string]any{"type": "signal", "channel_id": env.voiceID, "to": bobID, "kind": "offer",
		"data": map[string]any{"type": "offer", "sdp": "v=0..."}})
	sig := expect(t, b, "signal")
	if sig["from"] != aliceID || sig["kind"] != "offer" || sig["data"].(map[string]any)["sdp"] != "v=0..." {
		t.Fatalf("signal inesperado: %v", sig)
	}

	// estado de mídia vai para a sala
	send(t, b, map[string]any{"type": "voice_media", "channel_id": env.voiceID, "data": map[string]any{"muted": true}})
	if m := expect(t, a, "voice_media"); m["user_id"] != bobID {
		t.Fatalf("voice_media inesperado: %v", m)
	}

	// sair avisa a sala
	send(t, b, map[string]any{"type": "voice_leave"})
	if l := expect(t, a, "voice_left"); l["user_id"] != bobID {
		t.Fatalf("voice_left inesperado: %v", l)
	}
}

func TestVoiceDisconnectLeavesRoom(t *testing.T) {
	env := setupVoice(t)
	aliceTok, _ := env.member(t, "alice")
	bobTok, bobID := env.member(t, "bob")
	a := dialWS(t, env.srv, aliceTok)
	b := dialWS(t, env.srv, bobTok)
	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, a, "voice_peers")
	send(t, b, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, b, "voice_peers")
	expect(t, a, "voice_joined")

	b.Close()
	if l := expect(t, a, "voice_left"); l["user_id"] != bobID {
		t.Fatalf("queda do WS deve tirar o bob da sala: %v", l)
	}
}

func TestVoiceRejectsOutsiderAndTextChannel(t *testing.T) {
	env := setupVoice(t)
	aliceTok, aliceID := env.member(t, "alice")
	evilTok, _ := env.outsider(t, "evil")
	a := dialWS(t, env.srv, aliceTok)
	e := dialWS(t, env.srv, evilTok)

	send(t, e, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	if m := expect(t, e, "voice_error"); m["error"] != "forbidden" {
		t.Fatalf("não-membro deve receber forbidden: %v", m)
	}

	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.textID})
	if m := expect(t, a, "voice_error"); m["error"] != "not a voice channel" {
		t.Fatalf("canal de texto deve ser recusado: %v", m)
	}

	send(t, a, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	expect(t, a, "voice_peers")

	// quem está fora da sala não consegue sinalizar para quem está dentro
	send(t, e, map[string]any{"type": "signal", "channel_id": env.voiceID, "to": aliceID, "kind": "offer", "data": map[string]any{}})
	expectNone(t, a, "signal")

	// nem membro do grupo que não entrou na chamada
	bobTok, _ := env.member(t, "bob")
	b := dialWS(t, env.srv, bobTok)
	send(t, b, map[string]any{"type": "signal", "channel_id": env.voiceID, "to": aliceID, "kind": "ice", "data": map[string]any{}})
	expectNone(t, a, "signal")
}

func TestVoiceRoomLimit(t *testing.T) {
	env := setupVoice(t)
	conns := []*websocket.Conn{}
	for i := 0; i < 6; i++ {
		tok, _ := env.member(t, fmt.Sprintf("u%d", i))
		c := dialWS(t, env.srv, tok)
		send(t, c, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
		expect(t, c, "voice_peers")
		conns = append(conns, c)
	}
	tok, _ := env.member(t, "u7")
	c := dialWS(t, env.srv, tok)
	send(t, c, map[string]any{"type": "voice_join", "channel_id": env.voiceID})
	if m := expect(t, c, "voice_error"); m["error"] != "room full" {
		t.Fatalf("a sétima pessoa deve receber room full: %v", m)
	}
	_ = conns
}

func TestICEEndpoint(t *testing.T) {
	env := setupVoice(t)
	code, body := httpGet(t, env.srv, "/api/ice", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("sem token deve ser 401, veio %d", code)
	}
	t.Setenv("TURN_URL", "turn:turn.exemplo.com:3478, turns:turn.exemplo.com:5349")
	t.Setenv("TURN_USER", "u")
	t.Setenv("TURN_PASS", "p")
	code, body = httpGet(t, env.srv, "/api/ice", env.owner)
	var out struct {
		IceServers []struct {
			URLs       []string `json:"urls"`
			Username   string   `json:"username"`
			Credential string   `json:"credential"`
		} `json:"iceServers"`
	}
	json.Unmarshal(body, &out) //nolint:errcheck
	if code != 200 || len(out.IceServers) != 2 {
		t.Fatalf("esperava STUN + TURN, veio %d %s", code, body)
	}
	if len(out.IceServers[1].URLs) != 2 || out.IceServers[1].Username != "u" || out.IceServers[1].Credential != "p" {
		t.Fatalf("TURN mal montado: %+v", out.IceServers[1])
	}
}

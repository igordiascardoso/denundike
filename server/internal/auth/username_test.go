package auth_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jemunje/denundike/internal/auth"
	"github.com/jemunje/denundike/internal/db"
	"github.com/jemunje/denundike/internal/uname"
)

func TestUnameRules(t *testing.T) {
	ok := []string{"Brizão", "brizao", "João_Maçã", "ana.silva", "Ünal99", "abc", "a_1"}
	bad := []string{"ab", "", "com espaço", "@x", "ana!", "Иван", "аna", "😀😀😀", "___", "...", strings.Repeat("a", 33), "éaa"}
	for _, s := range ok {
		if !uname.Valid(s) {
			t.Errorf("deveria aceitar %q", s)
		}
	}
	for _, s := range bad {
		if uname.Valid(s) {
			t.Errorf("deveria recusar %q", s)
		}
	}
	for _, s := range []string{"Brizão", "BRIZAO", "brizão", "BrizÃo", "@brizao "} {
		if uname.Norm(s) != "brizao" {
			t.Errorf("Norm(%q)=%q", s, uname.Norm(s))
		}
	}
}

func doReq(t *testing.T, srv *httptest.Server, method, path string, body any, token string) (int, string) {
	t.Helper()
	var rd *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	} else {
		rd = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	buf.ReadFrom(resp.Body) //nolint:errcheck
	return resp.StatusCode, strings.TrimSpace(buf.String())
}

var regSeq int

func register(t *testing.T, srv *httptest.Server, database *sql.DB, login, email string) int {
	t.Helper()
	regSeq++
	code := fmt.Sprintf("reg-%d-%d", time.Now().UnixNano(), regSeq)
	database.Exec(`INSERT INTO invites (code, expires_at) VALUES (?, ?)`, code, time.Now().Add(time.Hour)) //nolint:errcheck
	resp := doPost(t, srv, "/api/auth/register", map[string]string{
		"login": login, "email": email, "display_name": "N", "password": "senha-forte-1", "invite_code": code,
	}, "")
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestAccentedLoginRegisterAndImpersonationBlocked(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	if c := register(t, srv, database, "Brizão", "a@x.com"); c != 201 {
		t.Fatalf("registrar Brizão: %d", c)
	}
	for _, dup := range []string{"Brizao", "BRIZÃO", "brizão", "@brizao"} {
		if c := register(t, srv, database, dup, dup+"@y.com"); c != 409 {
			t.Errorf("impostor %q deveria dar 409, deu %d", dup, c)
		}
	}
	for _, badLogin := range []string{"ab", "com espaço", "Иван", "x@y"} {
		if c := register(t, srv, database, badLogin, "b@x.com"); c != 400 {
			t.Errorf("login inválido %q deveria dar 400, deu %d", badLogin, c)
		}
	}
	// login com o nome exato, sem acento e em outra caixa funcionam
	for _, l := range []string{"Brizão", "brizao", "BRIZÃO", "@Brizão"} {
		resp := doPost(t, srv, "/api/auth/login", map[string]string{"login": l, "password": "senha-forte-1"}, "")
		if resp.StatusCode != 200 {
			t.Errorf("login %q: %d", l, resp.StatusCode)
		}
		resp.Body.Close()
	}
	// o nome é guardado como digitado
	var stored string
	database.QueryRow(`SELECT login FROM users`).Scan(&stored) //nolint:errcheck
	if stored != "Brizão" {
		t.Fatalf("login guardado = %q", stored)
	}
}

func TestChangeLoginFlow(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	idA, _ := signup(t, srv, database, "ana", "senha-ana-1", seedInvite(t, database, ""))
	signup(t, srv, database, "Bruno", "senha-bru-1", seedInvite(t, database, ""))
	tokA := loginToken(t, srv, "ana", "senha-ana-1")

	// disponibilidade
	check := func(l string) (bool, bool) {
		code, body := doReq(t, srv, "GET", "/api/me/login-available?login="+l, nil, tokA)
		if code != 200 {
			t.Fatalf("available %q: %d %s", l, code, body)
		}
		var m map[string]any
		json.Unmarshal([]byte(body), &m) //nolint:errcheck
		return m["valid"].(bool), m["available"].(bool)
	}
	if v, a := check("Anã_nova"); !v || !a {
		t.Errorf("Anã_nova deveria estar livre (v=%v a=%v)", v, a)
	}
	if v, a := check("BRUNO"); !v || a {
		t.Errorf("BRUNO deveria estar ocupado (v=%v a=%v)", v, a)
	}
	if v, a := check("brúno"); !v || a {
		t.Errorf("brúno deveria estar ocupado (v=%v a=%v)", v, a)
	}
	if v, _ := check("ab"); v {
		t.Errorf("ab deveria ser inválido")
	}
	if _, a := check("ANA"); !a {
		t.Errorf("o próprio nome (outra caixa) deveria contar como livre")
	}
	if code, _ := doReq(t, srv, "GET", "/api/me/login-available?login=x", nil, ""); code != 401 {
		t.Errorf("sem sessão deveria dar 401, deu %d", code)
	}

	// trocar: ocupado, inválido, igual
	if code, _ := doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "bruno"}, tokA); code != 409 {
		t.Errorf("ocupado: %d", code)
	}
	if code, _ := doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "a b"}, tokA); code != 400 {
		t.Errorf("inválido: %d", code)
	}
	if code, _ := doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "ana"}, tokA); code != 400 {
		t.Errorf("igual: %d", code)
	}

	// trocar de verdade, com acento; WS dos amigos recebe o evento
	friendReq(t, srv, tokA, loginToken(t, srv, "Bruno", "senha-bru-1"))
	tokB := loginToken(t, srv, "Bruno", "senha-bru-1")
	wsB := dialWS(t, srv, tokB)
	defer wsB.Close()
	time.Sleep(150 * time.Millisecond)

	code, body := doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "@Anã_Nova"}, tokA)
	if code != 200 {
		t.Fatalf("trocar: %d %s", code, body)
	}
	var out struct {
		User struct{ Login string } `json:"user"`
	}
	json.Unmarshal([]byte(body), &out) //nolint:errcheck
	if out.User.Login != "Anã_Nova" {
		t.Fatalf("login novo = %q", out.User.Login)
	}
	ev := readEvent(t, wsB, "user_renamed")
	if ev["login"] != "Anã_Nova" || ev["old_login"] != "ana" || ev["user_id"] != idA {
		t.Fatalf("evento errado: %v", ev)
	}

	// sessão antiga segue valendo e já mostra o nome novo; login pelo nome novo (sem acento) e não mais pelo antigo
	code, body = doReq(t, srv, "GET", "/api/auth/me", nil, tokA)
	if code != 200 || !strings.Contains(body, "Anã_Nova") {
		t.Fatalf("/me depois da troca: %d %s", code, body)
	}
	if r := doPost(t, srv, "/api/auth/login", map[string]string{"login": "ana_nova", "password": "senha-ana-1"}, ""); r.StatusCode != 200 {
		t.Errorf("login pelo nome novo: %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/auth/login", map[string]string{"login": "ana", "password": "senha-ana-1"}, ""); r.StatusCode != 401 {
		t.Errorf("nome antigo deveria falhar: %d", r.StatusCode)
	}

	// nova troca em seguida: bloqueada por 24h
	code, body = doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "outra_ana"}, tokA)
	if code != 429 || !strings.HasPrefix(body, "login change cooldown ") {
		t.Fatalf("cooldown: %d %q", code, body)
	}
	// passadas 24h, libera, e o nome antigo ficou livre para outra pessoa
	database.Exec(`UPDATE users SET login_changed_at = ?`, time.Now().Add(-25*time.Hour)) //nolint:errcheck
	if code, _ = doReq(t, srv, "PATCH", "/api/me/login", map[string]string{"login": "outra_ana"}, tokA); code != 200 {
		t.Fatalf("depois de 24h: %d", code)
	}
	if c := register(t, srv, database, "ana", "novo@x.com"); c != 201 {
		t.Errorf("nome antigo deveria estar livre: %d", c)
	}
}

func TestFriendRequestFindsAccentedLoginWithoutAccent(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	signup(t, srv, database, "carla", "senha-car-1", seedInvite(t, database, ""))
	signup(t, srv, database, "Brizão", "senha-bri-1", seedInvite(t, database, ""))
	tok := loginToken(t, srv, "carla", "senha-car-1")
	if code, body := doReq(t, srv, "POST", "/api/friends/request", map[string]string{"login": "@brizao"}, tok); code != 201 {
		t.Fatalf("pedido de amizade sem acento: %d %s", code, body)
	}
}

func TestMigrationBackfillsLoginNormKeepingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d.Exec(`INSERT INTO users (id, login, email, password_hash) VALUES ('u1','Légado','l@x.com','h')`) //nolint:errcheck
	d.Exec(`UPDATE users SET login_norm = NULL`)                                                      //nolint:errcheck
	d.Close()
	d, err = db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var login, norm string
	d.QueryRow(`SELECT login, login_norm FROM users WHERE id='u1'`).Scan(&login, &norm) //nolint:errcheck
	if login != "Légado" || norm != "legado" {
		t.Fatalf("login=%q norm=%q", login, norm)
	}
}

// --- helpers ---

func friendReq(t *testing.T, srv *httptest.Server, tokFrom, tokTo string) {
	t.Helper()
	_, body := doReq(t, srv, "GET", "/api/auth/me", nil, tokTo)
	var m struct{ User struct{ Login string } }
	json.Unmarshal([]byte(body), &m) //nolint:errcheck
	if code, b := doReq(t, srv, "POST", "/api/friends/request", map[string]string{"login": m.User.Login}, tokFrom); code != 201 {
		t.Fatalf("friend request: %d %s", code, b)
	}
}

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?token=" + token
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readEvent(t *testing.T, c *websocket.Conn, typ string) map[string]any {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(3 * time.Second)) //nolint:errcheck
	for {
		_, raw, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("esperando evento %s: %v", typ, err)
		}
		var m map[string]any
		json.Unmarshal(raw, &m) //nolint:errcheck
		if m["type"] == typ {
			return m
		}
	}
}

package auth_test

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jemunje/denundike/internal/auth"
)

var codeRe = regexp.MustCompile(`^[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}-[A-Z2-9]{4}$`)

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m) //nolint:errcheck
	return m
}

// signup registers a user and returns id, recovery code.
func signup(t *testing.T, srv *httptest.Server, database *sql.DB, login, password, inviteCode string) (string, string) {
	t.Helper()
	resp := doPost(t, srv, "/api/auth/register", map[string]string{
		"login": login, "email": login + "@example.com", "display_name": "Nome " + login,
		"password": password, "invite_code": inviteCode,
	}, "")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %s: status %d", login, resp.StatusCode)
	}
	u := decode(t, resp)["user"].(map[string]any)
	return u["id"].(string), u["recovery_code"].(string)
}

func loginToken(t *testing.T, srv *httptest.Server, login, password string) string {
	t.Helper()
	resp := doPost(t, srv, "/api/auth/login", map[string]string{"login": login, "password": password}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s: status %d", login, resp.StatusCode)
	}
	return decode(t, resp)["token"].(string)
}

func TestSignupReturnsRecoveryCodeOnceAndStoresOnlyHash(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	id, code := signup(t, srv, database, "ana", "senha-ana-1", seedInvite(t, database, ""))
	if !codeRe.MatchString(code) {
		t.Fatalf("formato do código inválido: %q", code)
	}
	var stored string
	database.QueryRow(`SELECT recovery_hash FROM users WHERE id = ?`, id).Scan(&stored) //nolint:errcheck
	if stored == "" || strings.Contains(stored, strings.ReplaceAll(code, "-", "")) {
		t.Fatalf("hash ausente ou código em texto puro: %q", stored)
	}
	tok := loginToken(t, srv, "ana", "senha-ana-1")
	me := decode(t, doGet(t, srv, "/api/auth/me", tok))["user"].(map[string]any)
	if _, leaked := me["recovery_code"]; leaked {
		t.Fatal("/me não pode devolver o código de recuperação")
	}
	if me["has_recovery"] != true {
		t.Fatal("has_recovery deveria ser true")
	}
}

func TestRecoverResetsPasswordRevokesSessionsAndRotatesCode(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	_, code := signup(t, srv, database, "bia", "senha-antiga-1", seedInvite(t, database, ""))
	oldTok := loginToken(t, srv, "bia", "senha-antiga-1")

	resp := doPost(t, srv, "/api/auth/recover", map[string]string{
		"login": "@bia", "recovery_code": strings.ToLower(code), "new_password": "senha-nova-22",
	}, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("recover: %d", resp.StatusCode)
	}
	body := decode(t, resp)
	newCode, _ := body["recovery_code"].(string)
	if !codeRe.MatchString(newCode) || newCode == code {
		t.Fatalf("novo código inválido ou repetido: %q", newCode)
	}
	if body["token"] == "" || body["token"] == nil {
		t.Fatal("recover deveria abrir uma sessão nova")
	}
	if r := doGet(t, srv, "/api/auth/me", oldTok); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sessão antiga deveria cair, status %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/auth/login", map[string]string{"login": "bia", "password": "senha-antiga-1"}, ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("senha antiga não pode mais funcionar")
	}
	loginToken(t, srv, "bia", "senha-nova-22")
	// the used code is dead; the rotated one works
	if r := doPost(t, srv, "/api/auth/recover", map[string]string{"login": "bia", "recovery_code": code, "new_password": "outra-senha-33"}, ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("código usado deveria falhar, status %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/auth/recover", map[string]string{"login": "bia", "recovery_code": newCode, "new_password": "outra-senha-33"}, ""); r.StatusCode != http.StatusOK {
		t.Fatalf("código novo deveria funcionar, status %d", r.StatusCode)
	}
}

func TestRecoverRejectsWrongCodeUnknownUserAndShortPassword(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	_, code := signup(t, srv, database, "caio", "senha-caio-1", seedInvite(t, database, ""))
	for name, req := range map[string]map[string]string{
		"código errado":       {"login": "caio", "recovery_code": "AAAA-AAAA-AAAA-AAAA", "new_password": "senha-nova-22"},
		"usuário inexistente": {"login": "fantasma", "recovery_code": code, "new_password": "senha-nova-22"},
	} {
		if r := doPost(t, srv, "/api/auth/recover", req, ""); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s: esperava 401, veio %d", name, r.StatusCode)
		}
	}
	if r := doPost(t, srv, "/api/auth/recover", map[string]string{"login": "caio", "recovery_code": code, "new_password": "curta"}, ""); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("senha curta: esperava 400, veio %d", r.StatusCode)
	}
	loginToken(t, srv, "caio", "senha-caio-1") // password untouched
}

func TestRecoverRateLimitedAfterRepeatedFailures(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	_, code := signup(t, srv, database, "dani", "senha-dani-1", seedInvite(t, database, ""))
	bad := map[string]string{"login": "dani", "recovery_code": "BBBB-BBBB-BBBB-BBBB", "new_password": "senha-nova-22"}
	for i := 0; i < 5; i++ {
		if r := doPost(t, srv, "/api/auth/recover", bad, ""); r.StatusCode != http.StatusUnauthorized {
			t.Fatalf("tentativa %d: %d", i, r.StatusCode)
		}
	}
	good := map[string]string{"login": "dani", "recovery_code": code, "new_password": "senha-nova-22"}
	if r := doPost(t, srv, "/api/auth/recover", good, ""); r.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("depois de 5 falhas deveria bloquear (429), veio %d", r.StatusCode)
	}
}

func TestRegenerateRecoveryNeedsPasswordAndInvalidatesOldCode(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	_, old := signup(t, srv, database, "edu", "senha-edu-1", seedInvite(t, database, ""))
	tok := loginToken(t, srv, "edu", "senha-edu-1")
	if r := doPost(t, srv, "/api/auth/recovery-code", map[string]string{"password": "errada"}, tok); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("senha errada: %d", r.StatusCode)
	}
	if r := doPost(t, srv, "/api/auth/recovery-code", map[string]string{"password": "senha-edu-1"}, ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sem login: %d", r.StatusCode)
	}
	r := doPost(t, srv, "/api/auth/recovery-code", map[string]string{"password": "senha-edu-1"}, tok)
	fresh, _ := decode(t, r)["recovery_code"].(string)
	if !codeRe.MatchString(fresh) || fresh == old {
		t.Fatalf("código novo inválido: %q", fresh)
	}
	if r := doPost(t, srv, "/api/auth/recover", map[string]string{"login": "edu", "recovery_code": old, "new_password": "senha-nova-22"}, ""); r.StatusCode != http.StatusUnauthorized {
		t.Fatal("código antigo deveria ter parado de valer")
	}
}

func TestLegacyAccountGetsRecoveryCodeOnceViaInit(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	id, _ := signup(t, srv, database, "fabi", "senha-fabi-1", seedInvite(t, database, ""))
	database.Exec(`UPDATE users SET recovery_hash = NULL WHERE id = ?`, id) //nolint:errcheck — simulates a pre-recovery account
	tok := loginToken(t, srv, "fabi", "senha-fabi-1")
	me := decode(t, doGet(t, srv, "/api/auth/me", tok))["user"].(map[string]any)
	if me["has_recovery"] != false {
		t.Fatalf("conta antiga deveria ter has_recovery=false: %v", me["has_recovery"])
	}
	r := doPost(t, srv, "/api/auth/recovery-code/init", map[string]string{}, tok)
	if r.StatusCode != http.StatusOK || !codeRe.MatchString(decode(t, r)["recovery_code"].(string)) {
		t.Fatal("init deveria devolver um código válido")
	}
	if r := doPost(t, srv, "/api/auth/recovery-code/init", map[string]string{}, tok); r.StatusCode != http.StatusConflict {
		t.Fatalf("segundo init deveria dar 409, veio %d", r.StatusCode)
	}
}

func TestLoginIsCaseInsensitiveAndDuplicateLoginRejected(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	signup(t, srv, database, "Brizao", "senha-briz-1", seedInvite(t, database, ""))
	loginToken(t, srv, "@brizao", "senha-briz-1")
	r := doPost(t, srv, "/api/auth/register", map[string]string{
		"login": "BRIZAO", "email": "outro@example.com", "password": "senha-briz-2", "invite_code": seedInvite(t, database, ""),
	}, "")
	if r.StatusCode != http.StatusConflict {
		t.Fatalf("login duplicado (outra caixa) deveria dar 409, veio %d", r.StatusCode)
	}
}

func TestLastSeenUpdatesWhenAuthenticated(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	id, _ := signup(t, srv, database, "gui", "senha-gui-1", seedInvite(t, database, ""))
	database.Exec(`UPDATE users SET last_seen_at = ? WHERE id = ?`, time.Now().Add(-time.Hour), id) //nolint:errcheck
	tok := loginToken(t, srv, "gui", "senha-gui-1")
	database.Exec(`UPDATE users SET last_seen_at = ? WHERE id = ?`, time.Now().Add(-time.Hour), id) //nolint:errcheck
	doGet(t, srv, "/api/auth/me", tok)
	var seen time.Time
	if err := database.QueryRow(`SELECT last_seen_at FROM users WHERE id = ?`, id).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if time.Since(seen) > 30*time.Second {
		t.Fatalf("last_seen_at não foi atualizado: %v", seen)
	}
}

func TestInviteLevelsPointsRankingAndQuota(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	hostID, _ := signup(t, srv, database, "host", "senha-host-1", seedInvite(t, database, ""))
	host := loginToken(t, srv, "host", "senha-host-1")

	// Novato: quota of 2 pending invites.
	var codes []string
	for i := 0; i < 2; i++ {
		r := doPost(t, srv, "/api/invites", map[string]string{}, host)
		if r.StatusCode != http.StatusCreated {
			t.Fatalf("convite %d: %d", i, r.StatusCode)
		}
		codes = append(codes, decode(t, r)["code"].(string))
	}
	if r := doPost(t, srv, "/api/invites", map[string]string{}, host); r.StatusCode != http.StatusConflict {
		t.Fatalf("3º convite pendente deveria bater na cota (409), veio %d", r.StatusCode)
	}

	st := decode(t, doGet(t, srv, "/api/me/stats", host))
	if st["accepted"].(float64) != 0 || st["pending"].(float64) != 2 || st["quota"].(float64) != 2 {
		t.Fatalf("stats iniciais: %v", st)
	}
	if st["level"].(map[string]any)["name"] != "Novato" {
		t.Fatalf("nível inicial: %v", st["level"])
	}

	// A friend signs up with the host's invite: +10 points, level Recrutador, quota 3.
	friendID, _ := signup(t, srv, database, "amigo", "senha-amigo-1", codes[0])
	var points int
	database.QueryRow(`SELECT points FROM users WHERE id = ?`, hostID).Scan(&points) //nolint:errcheck
	if points != 10 {
		t.Fatalf("pontos do anfitrião após cadastro = %d, esperado 10", points)
	}
	var invitedBy string
	database.QueryRow(`SELECT invited_by FROM users WHERE id = ?`, friendID).Scan(&invitedBy) //nolint:errcheck
	if invitedBy != hostID {
		t.Fatalf("invited_by = %q", invitedBy)
	}
	st = decode(t, doGet(t, srv, "/api/me/stats", host))
	if st["accepted"].(float64) != 1 || st["level"].(map[string]any)["name"] != "Recrutador" || st["quota"].(float64) != 3 {
		t.Fatalf("stats após 1 aceito: %v", st)
	}
	if st["next_level"].(map[string]any)["name"] != "Conector" || st["to_next"].(float64) != 2 {
		t.Fatalf("próximo nível: %v", st)
	}
	if len(st["badges"].([]any)) == 0 {
		t.Fatal("deveria ter o distintivo de primeiro convite")
	}

	// The invitee's first message is worth +5 to the inviter, once.
	database.Exec(`INSERT INTO groups (id, name, owner_id) VALUES ('g1','G',?)`, friendID)             //nolint:errcheck
	database.Exec(`INSERT INTO group_members (group_id, user_id) VALUES ('g1',?)`, friendID)           //nolint:errcheck
	database.Exec(`INSERT INTO channels (id, group_id, name, type) VALUES ('c1','g1','geral','text')`) //nolint:errcheck
	friendTok := loginToken(t, srv, "amigo", "senha-amigo-1")
	for i := 0; i < 2; i++ {
		if r := doPost(t, srv, "/api/channels/c1/messages", map[string]string{"text": "oi"}, friendTok); r.StatusCode != http.StatusCreated {
			t.Fatalf("mensagem: %d", r.StatusCode)
		}
	}
	database.QueryRow(`SELECT points FROM users WHERE id = ?`, hostID).Scan(&points) //nolint:errcheck
	if points != 15 {
		t.Fatalf("pontos após 1ª mensagem do convidado = %d, esperado 15 (e só uma vez)", points)
	}

	mine := func() []any {
		r := doGet(t, srv, "/api/invites/mine", host)
		var list []any
		json.NewDecoder(r.Body).Decode(&list) //nolint:errcheck
		return list
	}
	list := mine()
	if len(list) != 2 {
		t.Fatalf("meus convites: %d", len(list))
	}
	statuses := map[string]string{}
	for _, it := range list {
		m := it.(map[string]any)
		statuses[m["code"].(string)], _ = m["status"].(string)
		if m["status"] == "used" && m["used_by"] != "amigo" {
			t.Fatalf("used_by: %v", m)
		}
	}
	if statuses[codes[0]] != "used" || statuses[codes[1]] != "active" {
		t.Fatalf("status dos convites: %v", statuses)
	}

	rk := decode(t, doGet(t, srv, "/api/ranking", host))
	top := rk["top"].([]any)
	first := top[0].(map[string]any)
	if first["login"] != "host" || first["points"].(float64) != 15 || first["accepted"].(float64) != 1 {
		t.Fatalf("ranking: %v", top)
	}
	if rk["me"].(map[string]any)["rank"].(float64) != 1 {
		t.Fatalf("minha posição: %v", rk["me"])
	}
	if first["name"] != "Nome host" {
		t.Fatalf("nome de exibição no ranking: %v", first["name"])
	}

	// An accepted invite frees a pending slot: quota 3, 1 pending → can create 2 more, not 3.
	ok := 0
	for i := 0; i < 3; i++ {
		if r := doPost(t, srv, "/api/invites", map[string]string{}, host); r.StatusCode == http.StatusCreated {
			ok++
		}
	}
	if ok != 2 {
		t.Fatalf("convites criados dentro da cota de Recrutador = %d, esperado 2", ok)
	}
}

func TestSeedInviteWithoutCreatorStillWorks(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	id, _ := signup(t, srv, database, "seed", "senha-seed-1", seedInvite(t, database, ""))
	var by sql.NullString
	database.QueryRow(`SELECT invited_by FROM users WHERE id = ?`, id).Scan(&by) //nolint:errcheck
	if by.Valid {
		t.Fatalf("convite semente não tem anfitrião, invited_by deveria ser NULL: %v", by)
	}
}

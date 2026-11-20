package group_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func putRaw(t *testing.T, srv *httptest.Server, method, path, token string, body []byte) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGroupAvatarOnlyManagersChangeItAndMembersSeeIt(t *testing.T) {
	srv, _, tok, gid := newGroupWorld(t)
	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

	if got := putRaw(t, srv, "PUT", "/api/groups/"+gid+"/avatar", tok["bob"], gif).StatusCode; got != http.StatusForbidden {
		t.Fatalf("membro comum não pode trocar a foto do grupo: veio %d", got)
	}
	if got := putRaw(t, srv, "PUT", "/api/groups/"+gid+"/avatar", tok["eve"], gif).StatusCode; got != http.StatusForbidden {
		t.Fatalf("quem não é do grupo não pode trocar a foto: veio %d", got)
	}
	if got := putRaw(t, srv, "PUT", "/api/groups/"+gid+"/avatar", tok["alice"], []byte("texto")).StatusCode; got != http.StatusBadRequest {
		t.Fatalf("arquivo que não é imagem deveria ser 400, veio %d", got)
	}
	if got := putRaw(t, srv, "PUT", "/api/groups/"+gid+"/avatar", tok["alice"], gif).StatusCode; got != http.StatusOK {
		t.Fatalf("dono deveria poder trocar a foto (GIF): veio %d", got)
	}

	g := putRaw(t, srv, "GET", "/api/group-avatar/"+gid, tok["bob"], nil)
	body, _ := io.ReadAll(g.Body)
	g.Body.Close()
	if g.StatusCode != http.StatusOK || !bytes.Equal(body, gif) || g.Header.Get("Content-Type") != "image/gif" {
		t.Fatalf("membro deveria ver o GIF: %d %q", g.StatusCode, g.Header.Get("Content-Type"))
	}
	if got := putRaw(t, srv, "GET", "/api/group-avatar/"+gid, tok["eve"], nil).StatusCode; got != http.StatusForbidden {
		t.Fatalf("quem não é do grupo não pode baixar a foto: veio %d", got)
	}
	if list := decode(t, doReq(t, srv, "GET", "/api/group-avatars", nil, tok["bob"])); list[gid] == nil {
		t.Fatalf("lista do membro deveria ter o grupo: %v", list)
	}
	if list := decode(t, doReq(t, srv, "GET", "/api/group-avatars", nil, tok["eve"])); len(list) != 0 {
		t.Fatalf("lista de quem não é do grupo deveria ser vazia: %v", list)
	}

	if got := putRaw(t, srv, "DELETE", "/api/groups/"+gid+"/avatar", tok["bob"], nil).StatusCode; got != http.StatusForbidden {
		t.Fatalf("membro comum não pode remover a foto: veio %d", got)
	}
	if got := putRaw(t, srv, "DELETE", "/api/groups/"+gid+"/avatar", tok["alice"], nil).StatusCode; got != http.StatusOK {
		t.Fatalf("dono deveria poder remover: veio %d", got)
	}
	// excluir o grupo com foto não pode travar por causa da tabela de fotos
	putRaw(t, srv, "PUT", "/api/groups/"+gid+"/avatar", tok["alice"], gif).Body.Close()
	if got := putRaw(t, srv, "DELETE", "/api/groups/"+gid, tok["alice"], nil).StatusCode; got != http.StatusOK && got != http.StatusNoContent {
		t.Fatalf("excluir grupo com foto deveria funcionar: veio %d", got)
	}
}

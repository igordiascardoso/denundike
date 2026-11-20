package auth_test

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jemunje/denundike/internal/auth"
)

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for x := 0; x < 4; x++ {
		for y := 0; y < 4; y++ {
			img.Set(x, y, color.RGBA{R: 200, G: 30, B: 90, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func avatarReq(t *testing.T, method, url, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestAvatarUploadServeListAndRemove(t *testing.T) {
	auth.ResetRateLimits()
	srv, database := setupTestServer(t)
	id, _ := signup(t, srv, database, "carla", "senha-forte-11", seedInvite(t, database, ""))
	tok := loginToken(t, srv, "carla", "senha-forte-11")
	img := tinyPNG(t)

	if r := avatarReq(t, "PUT", srv.URL+"/api/me/avatar", "", img); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("sem login deveria ser 401, veio %d", r.StatusCode)
	}
	if r := avatarReq(t, "PUT", srv.URL+"/api/me/avatar", tok, []byte("isto nao e uma imagem")); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("arquivo que não é imagem deveria ser 400, veio %d", r.StatusCode)
	}
	if r := avatarReq(t, "PUT", srv.URL+"/api/me/avatar", tok, append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, auth.AvatarMax)...)); r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("imagem grande demais deveria ser 413, veio %d", r.StatusCode)
	}

	gif := []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")
	if r := avatarReq(t, "PUT", srv.URL+"/api/me/avatar", tok, gif); r.StatusCode != http.StatusOK {
		t.Fatalf("GIF deveria ser aceito, veio %d", r.StatusCode)
	} else if g := avatarReq(t, "GET", srv.URL+"/api/avatar/"+id, tok, nil); g.Header.Get("Content-Type") != "image/gif" {
		t.Fatalf("GIF deveria voltar como image/gif, veio %q", g.Header.Get("Content-Type"))
	}

	r := avatarReq(t, "PUT", srv.URL+"/api/me/avatar", tok, img)
	if r.StatusCode != http.StatusOK {
		t.Fatalf("upload: %d", r.StatusCode)
	}
	if v, _ := decode(t, r)["version"].(float64); v <= 0 {
		t.Fatal("upload deveria devolver a versão")
	}

	g := avatarReq(t, "GET", srv.URL+"/api/avatar/"+id, tok, nil)
	got, _ := io.ReadAll(g.Body)
	g.Body.Close()
	if g.StatusCode != http.StatusOK || !bytes.Equal(got, img) || g.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("GET avatar: %d, tipo %q, %d bytes (esperava %d)", g.StatusCode, g.Header.Get("Content-Type"), len(got), len(img))
	}

	l := avatarReq(t, "GET", srv.URL+"/api/avatars", tok, nil)
	list := decode(t, l)
	if _, ok := list[id]; !ok {
		t.Fatalf("lista de avatares deveria ter %s: %v", id, list)
	}

	if d := avatarReq(t, "DELETE", srv.URL+"/api/me/avatar", tok, nil); d.StatusCode != http.StatusOK {
		t.Fatalf("remover: %d", d.StatusCode)
	}
	if g2 := avatarReq(t, "GET", srv.URL+"/api/avatar/"+id, tok, nil); g2.StatusCode != http.StatusNotFound {
		t.Fatalf("depois de remover deveria ser 404, veio %d", g2.StatusCode)
	}
	if list2 := decode(t, avatarReq(t, "GET", srv.URL+"/api/avatars", tok, nil)); strings.Contains(toStr(list2), id) {
		t.Fatalf("lista ainda tem o avatar removido: %v", list2)
	}
}

func toStr(m map[string]any) string {
	var b strings.Builder
	for k := range m {
		b.WriteString(k)
	}
	return b.String()
}

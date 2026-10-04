package main

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
)

func testSite() fstest.MapFS {
	return fstest.MapFS{
		"index.html":                 {Data: []byte("<!doctype html><title>Quack</title>" + strings.Repeat(" ", 200))},
		"favicon.svg":                {Data: []byte("<svg></svg>")},
		"assets/index-abc123.js":     {Data: []byte(strings.Repeat("console.log('quack');", 100))},
		"assets/index-abc123.js.map": {Data: []byte("{}")},
	}
}

func newTestServer(t *testing.T, api string) *server {
	t.Helper()
	u, err := url.Parse(api)
	if err != nil {
		t.Fatal(err)
	}
	s, err := newServer(testSite(), u)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func get(t *testing.T, h http.Handler, path string, header map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func TestServesClientRoutesWithIndex(t *testing.T) {
	s := newTestServer(t, "http://api.invalid")
	for _, path := range []string{"/", "/guilds/123/cases/45", "/login"} {
		resp := get(t, s, path, nil)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>Quack</title>") {
			t.Fatalf("%s: status %d body %q", path, resp.StatusCode, body)
		}
		if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s: Cache-Control = %q, want no-cache", path, got)
		}
		if resp.Header.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", path)
		}
	}
}

func TestMissingFilesAreNotFound(t *testing.T) {
	s := newTestServer(t, "http://api.invalid")
	for _, path := range []string{"/assets/missing.js", "/assets/index-abc123.js.map", "/robots.txt"} {
		if resp := get(t, s, path, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestHashedAssetsAreImmutableAndGzipped(t *testing.T) {
	s := newTestServer(t, "http://api.invalid")
	resp := get(t, s, "/assets/index-abc123.js", map[string]string{"Accept-Encoding": "br, gzip"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", got)
	}
	if resp.Header.Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
	}
	zr, err := gzip.NewReader(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(zr)
	if !strings.HasPrefix(string(body), "console.log") {
		t.Errorf("unexpected body %q", body[:20])
	}

	plain := get(t, s, "/assets/index-abc123.js", nil)
	if plain.Header.Get("Content-Encoding") != "" {
		t.Error("served gzip to a client that did not ask for it")
	}
}

func TestProxiesAPIWithoutPrefix(t *testing.T) {
	var gotPath, gotQuery, gotForwarded string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotForwarded = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Forwarded-For")
		http.SetCookie(w, &http.Cookie{Name: "quack_session", Value: "s", Path: "/"})
		w.WriteHeader(http.StatusTeapot)
	}))
	defer api.Close()

	s := newTestServer(t, api.URL)
	resp := get(t, s, "/api/guilds/1/cases?limit=5", map[string]string{"X-Forwarded-For": "203.0.113.9"})
	if resp.StatusCode != http.StatusTeapot {
		t.Fatalf("status %d, want the API's", resp.StatusCode)
	}
	if gotPath != "/guilds/1/cases" || gotQuery != "limit=5" {
		t.Errorf("API saw %q?%q", gotPath, gotQuery)
	}
	if !strings.HasPrefix(gotForwarded, "203.0.113.9, ") {
		t.Errorf("X-Forwarded-For = %q, want the prior hop kept", gotForwarded)
	}
	if len(resp.Cookies()) != 1 {
		t.Error("session cookie not passed through")
	}
}

func TestProxyErrorUsesAPIEnvelope(t *testing.T) {
	s := newTestServer(t, "http://127.0.0.1:1")
	resp := get(t, s, "/api/auth/me", nil)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), `"dependency_unavailable"`) {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
}

func TestRequiresIndex(t *testing.T) {
	u, _ := url.Parse("http://api.invalid")
	if _, err := newServer(fstest.MapFS{}, u); err == nil {
		t.Fatal("want an error without index.html")
	}
}

func TestSingleJoin(t *testing.T) {
	cases := map[[2]string]string{
		{"", "/guilds"}:   "/guilds",
		{"/", "/guilds"}:  "/guilds",
		{"/quack", "/me"}: "/quack/me",
		{"/quack/", ""}:   "/quack/",
	}
	for in, want := range cases {
		if got := singleJoin(in[0], in[1]); got != want {
			t.Errorf("singleJoin(%q, %q) = %q, want %q", in[0], in[1], got, want)
		}
	}
}

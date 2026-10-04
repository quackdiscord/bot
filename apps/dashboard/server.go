package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"time"
)

// contentSecurityPolicy allows the app's own scripts, the visitors.now
// analytics script and its event endpoint, and the Discord CDN for avatars,
// server icons, and preserved evidence. Components set dynamic sizes and
// positions in style attributes, so inline styles are allowed.
const contentSecurityPolicy = "default-src 'self'; " +
	"script-src 'self' https://cdn.visitors.now; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: https://cdn.discordapp.com https://media.discordapp.net; " +
	"font-src 'self'; " +
	"connect-src 'self' https://e.visitors.now; " +
	"frame-ancestors 'none'; base-uri 'self'; form-action 'self'"

// asset is one built file, held in memory with its gzipped form when that
// is smaller.
type asset struct {
	body      []byte
	gzipped   []byte
	ctype     string
	immutable bool
}

// server serves the built dashboard and proxies /api to the Quack API.
type server struct {
	assets map[string]*asset
	index  *asset
	proxy  http.Handler
}

// newServer loads every file in site, which must contain index.html, and
// proxies /api/* to api with the /api prefix removed.
func newServer(site fs.FS, api *url.URL) (*server, error) {
	assets := map[string]*asset{}
	err := fs.WalkDir(site, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(name, ".map") || path.Base(name) == ".gitkeep" {
			return err
		}
		body, err := fs.ReadFile(site, name)
		if err != nil {
			return err
		}
		a := &asset{
			body:  body,
			ctype: contentType(name),
			// Vite fingerprints everything under assets/, so it never changes.
			immutable: strings.HasPrefix(name, "assets/"),
		}
		if compressible(a.ctype) {
			a.gzipped = gzipBytes(body)
		}
		assets["/"+name] = a
		return nil
	})
	if err != nil {
		return nil, err
	}
	index, ok := assets["/index.html"]
	if !ok {
		return nil, errors.New("index.html is missing; build the app with `bun run build` first")
	}
	return &server{assets: assets, index: index, proxy: apiProxy(api)}, nil
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/healthz":
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte("ok\n"))
	case r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/"):
		s.proxy.ServeHTTP(w, r)
	default:
		s.serveApp(w, r)
	}
}

// serveApp serves a built file, or index.html for any other path without a
// file extension so client-side routes work on reload and from links.
func (s *server) serveApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := path.Clean("/" + r.URL.Path)
	a, ok := s.assets[name]
	if !ok {
		if path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		a = s.index
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	if a.immutable {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		// index.html names the current asset hashes, so always revalidate it.
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
	}
	body := a.body
	if a.gzipped != nil {
		h.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r) {
			h.Set("Content-Encoding", "gzip")
			body = a.gzipped
		}
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(body))
}

// apiProxy forwards /api/* to the Quack API. The browser sees one origin, so
// the API's host-only session cookies and its CSRF origin check both work.
func apiProxy(target *url.URL) http.Handler {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.URL.Path = singleJoin(target.Path, strings.TrimPrefix(pr.In.URL.Path, "/api"))
			pr.Out.URL.RawPath = ""
			// Keep any forwarding chain from a load balancer in front of us;
			// the API decides which hops to trust.
			if prior := pr.In.Header.Values("X-Forwarded-For"); len(prior) > 0 {
				pr.Out.Header["X-Forwarded-For"] = prior
			}
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.WarnContext(r.Context(), "Quack API unreachable", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{
				"code":    "dependency_unavailable",
				"message": "Quack's API is unreachable right now",
			}})
		},
	}
}

// singleJoin joins a base path and a request path with exactly one slash.
func singleJoin(base, p string) string {
	if p == "" {
		p = "/"
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(p, "/")
}

func contentType(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func compressible(ctype string) bool {
	for _, prefix := range []string{"text/", "application/javascript", "application/json", "image/svg+xml"} {
		if strings.HasPrefix(ctype, prefix) {
			return true
		}
	}
	return false
}

func gzipBytes(body []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(body)
	_ = zw.Close()
	if buf.Len() >= len(body) {
		return nil
	}
	return buf.Bytes()
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		enc, q, _ := strings.Cut(strings.TrimSpace(part), ";")
		if strings.EqualFold(strings.TrimSpace(enc), "gzip") && strings.TrimSpace(q) != "q=0" {
			return true
		}
	}
	return false
}

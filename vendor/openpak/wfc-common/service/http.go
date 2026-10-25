package service

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"
)

func Key() string {
	k := os.Getenv("WFC_INTERNAL_KEY")
	if len(k) < 32 {
		panic("WFC_INTERNAL_KEY must be at least 32 bytes")
	}
	return k
}
func Guard(h http.Handler) http.Handler {
	k := Key()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.Header.Values("X-WFC-Internal-Key")
		if len(v) != 1 || subtle.ConstantTimeCompare([]byte(v[0]), []byte(k)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		h.ServeHTTP(w, r)
	})
}
func Proxy(raw, key string) (http.Handler, error) {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.Scheme != "http" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid internal WFC origin")
	}
	p := httputil.NewSingleHostReverseProxy(u)
	director := p.Director
	p.Director = func(r *http.Request) { director(r); r.Header.Set("X-WFC-Internal-Key", key) }
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = 10 * time.Second
	// Legacy NAS wraps sockets in an io.Pipe and closes responses independently
	// of standard HTTP idle pooling. Use a fresh internal hop so a POST is never
	// handed a stale connection after a NAS idle timeout or independent restart.
	tr.DisableKeepAlives = true
	p.Transport = tr
	p.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		log.Printf("WFC upstream %s failed: %v", u.Host, e)
		http.Error(w, "service unavailable", 503)
	}
	return p, nil
}
func Server(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second}
}
func Run(ctx context.Context, s *http.Server) error {
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe() }()
	var err error
	select {
	case <-ctx.Done():
	case err = <-done:
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e := s.Shutdown(shutdown); e != nil {
		s.Close()
		if err == nil {
			err = e
		}
	}
	if err == http.ErrServerClosed {
		err = nil
	}
	return err
}

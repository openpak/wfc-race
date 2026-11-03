// Package httplog is the one-line-per-request / per-command log every OpenPak WFC service writes to
// stderr through the standard log package.
//
// HTTP: "http <method> <path> -> <status> <duration>ms". Only the path is written: never the query,
// headers, cookies or bodies, which carry auth tokens, challenge responses and passwords.
//
// GameSpy-style TCP/UDP protocols: "<proto> cmd <name> -> <result> <duration>ms". The command name is
// the protocol verb only; never a payload value.
package httplog

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Middleware logs one line per request after the handler returns. /healthz is skipped (probe noise).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			log.Printf("http %s %s -> %d %dms", clean(r.Method), clean(r.URL.Path), sw.status, time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(sw, r)
	})
}

// Command logs one protocol message: proto ("gpcm", "qr2", ...), the verb, a short result word
// ("ok", "error", "unknown", ...) and how long it took since start.
func Command(proto, name, result string, start time.Time) {
	log.Printf("%s cmd %s -> %s %dms", clean(proto), clean(name), clean(result), time.Since(start).Milliseconds())
}

// clean keeps a client-chosen token from forging log lines or growing without bound.
func clean(s string) string {
	if len(s) > 64 {
		s = s[:64]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ' ' {
			return '?'
		}
		return r
	}, s)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusWriter) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := s.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, fmt.Errorf("httplog: underlying ResponseWriter is not a Hijacker")
}

// Unwrap lets http.ResponseController reach the real writer.
func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

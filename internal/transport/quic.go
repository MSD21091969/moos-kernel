package transport

import (
	"crypto/tls"
	"log"

	"github.com/quic-go/quic-go/http3"
)

// ServeQUIC starts an HTTP/3 server on the given UDP address using quic-go.
// It shares the same http.Handler as the HTTP/1.1+HTTP/2 TCP server, so all
// endpoints (including /fold, /log/stream, /rewrites) are available over QUIC.
//
// TLS is required for QUIC (RFC 9000 §4.4). Call from main.go after the TCP server.
func (s *Server) ServeQUIC(addr, certFile, keyFile string) {
	if addr == "" {
		return
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13}
	srv := &http3.Server{
		Addr:      addr,
		Handler:   s.Handler(),
		TLSConfig: tlsCfg,
	}
	log.Printf("transport: quic listening on %s", addr)
	if err := srv.ListenAndServeTLS(certFile, keyFile); err != nil {
		log.Printf("transport: quic: %v", err)
	}
}

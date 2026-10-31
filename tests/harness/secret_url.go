package harness

import (
	"crypto/tls"
	"crypto/x509"
	"deck2/internal/gateway"
	"net/http"
	"net/http/httptest"
)

// TrustTestTLS makes the tools of deck trust the certificate of test servers on https, as a public service's.
func TrustTestTLS() {
	s := httptest.NewTLSServer(http.NotFoundHandler())
	defer s.Close()
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	pool.AddCert(s.Certificate())
	gateway.Outbound.TLSClientConfig = &tls.Config{RootCAs: pool}
}

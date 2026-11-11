package targetdb

import (
	"crypto/x509"
	"errors"

	"github.com/jackc/pgx/v5"
)

// applySSLRootCert replaces pgx's system trust store with the CA bundle saved
// for this connection. It only applies to verify-ca and verify-full, where the
// TLS config created by pgx already performs chain verification.
func applySSLRootCert(cfg *pgx.ConnConfig, pem string) error {
	if pem == "" {
		return nil
	}
	if cfg.TLSConfig == nil {
		return errors.New("ssl root certificate requires TLS")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pem)) {
		return errors.New("invalid ssl root certificate PEM")
	}
	cfg.TLSConfig.RootCAs = pool
	cfg.TLSConfig.ClientCAs = pool
	return nil
}

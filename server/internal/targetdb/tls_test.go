package targetdb

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func testCAPEM(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestNormalizeSSLRootCert(t *testing.T) {
	ca := testCAPEM(t)
	base := ConnectionInput{
		Name: "db", Host: "db.example.com", DBName: "app", Username: "app", Password: "secret",
	}
	cases := []struct {
		name  string
		mode  string
		cert  string
		field string
	}{
		{name: "verify-full with CA", mode: "verify-full", cert: ca},
		{name: "verify-ca with CA", mode: "verify-ca", cert: ca},
		{name: "verify-full system trust", mode: "verify-full"},
		{name: "CA with prefer", mode: "prefer", cert: ca, field: "ssl_root_cert"},
		{name: "invalid PEM", mode: "verify-full", cert: "not a certificate", field: "ssl_root_cert"},
		{name: "too large", mode: "verify-full", cert: strings.Repeat("A", 64*1024+1), field: "ssl_root_cert"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.SSLMode = tc.mode
			in.SSLRootCert = tc.cert
			err := in.normalize(true)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("normalize: %v", err)
				}
				return
			}
			verr, ok := err.(*ValidationError)
			if !ok || verr.Field != tc.field {
				t.Fatalf("err = %v, want field %q", err, tc.field)
			}
		})
	}
}

func TestApplySSLRootCert(t *testing.T) {
	ca := testCAPEM(t)

	t.Run("verify-full", func(t *testing.T) {
		cfg, err := pgx.ParseConfig("host=db.example.com sslmode=verify-full")
		if err != nil {
			t.Fatal(err)
		}
		if err := applySSLRootCert(cfg, ca); err != nil {
			t.Fatal(err)
		}
		if cfg.TLSConfig == nil || cfg.TLSConfig.RootCAs == nil {
			t.Fatal("RootCAs not applied")
		}
		if cfg.TLSConfig.ServerName != "db.example.com" {
			t.Errorf("ServerName = %q", cfg.TLSConfig.ServerName)
		}
	})

	t.Run("verify-ca", func(t *testing.T) {
		cfg, err := pgx.ParseConfig("host=db.example.com sslmode=verify-ca")
		if err != nil {
			t.Fatal(err)
		}
		if err := applySSLRootCert(cfg, ca); err != nil {
			t.Fatal(err)
		}
		if cfg.TLSConfig == nil || cfg.TLSConfig.RootCAs == nil {
			t.Fatal("RootCAs not applied")
		}
	})

	t.Run("invalid PEM", func(t *testing.T) {
		cfg, err := pgx.ParseConfig("host=db.example.com sslmode=verify-full")
		if err != nil {
			t.Fatal(err)
		}
		if err := applySSLRootCert(cfg, "nope"); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("TLS disabled", func(t *testing.T) {
		cfg, err := pgx.ParseConfig("host=db.example.com sslmode=disable")
		if err != nil {
			t.Fatal(err)
		}
		if err := applySSLRootCert(cfg, ca); err == nil {
			t.Fatal("expected error")
		}
	})
}

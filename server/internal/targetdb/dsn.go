package targetdb

import (
	"fmt"
	"strings"
)

// ConnectTimeoutSeconds membatasi lama percobaan koneksi ke database target
const ConnectTimeoutSeconds = 5

var dsnValueEscaper = strings.NewReplacer(`\`, `\\`, `'`, `\'`)

func quoteDSNValue(v string) string {
	return "'" + dsnValueEscaper.Replace(v) + "'"
}

type ConnParams struct {
	Host     string
	Port     int
	DBName   string
	Username string
	Password string
	SSLMode  string
}

func (p ConnParams) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s connect_timeout=%d",
		quoteDSNValue(p.Host), p.Port, quoteDSNValue(p.DBName), quoteDSNValue(p.Username),
		quoteDSNValue(p.Password), quoteDSNValue(p.SSLMode), ConnectTimeoutSeconds,
	)
}

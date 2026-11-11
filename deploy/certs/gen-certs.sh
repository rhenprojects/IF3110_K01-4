#!/bin/sh
# Generate a local self-signed certificate shared by Grafana and Prometheus.
# Certs live in the tlscerts volume; nothing secret is committed to git.
set -eu

DIR=/certs
mkdir -p "$DIR"

if [ -s "$DIR/server.crt" ] && [ -s "$DIR/server.key" ]; then
	echo "TLS certificate already exists"
	exit 0
fi

openssl req -x509 -newkey rsa:2048 -nodes \
	-keyout "$DIR/ca.key" -out "$DIR/ca.crt" \
	-days 825 -subj "/CN=if3110-local-ca" >/dev/null 2>&1

openssl req -newkey rsa:2048 -nodes \
	-keyout "$DIR/server.key" -out "$DIR/server.csr" \
	-subj "/CN=localhost" >/dev/null 2>&1

cat >"$DIR/server.ext" <<'EOF'
subjectAltName=DNS:localhost,DNS:grafana,DNS:prometheus,IP:127.0.0.1
extendedKeyUsage=serverAuth
EOF

openssl x509 -req -in "$DIR/server.csr" \
	-CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" -CAcreateserial \
	-out "$DIR/server.crt" -days 825 -extfile "$DIR/server.ext" >/dev/null 2>&1

# Grafana (uid 472) and Prometheus (nobody) read the same key from the volume.
chmod 644 "$DIR/server.crt" "$DIR/server.key" "$DIR/ca.crt"
rm -f "$DIR/ca.key" "$DIR/server.csr" "$DIR/server.ext" "$DIR/ca.srl"

echo "TLS certificate generated in $DIR"

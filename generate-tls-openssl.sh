#!/bin/bash
# Generate TLS certificates for testing environment using OpenSSL
# Alternative to generate-tls.sh that uses OpenSSL instead of cfssl

set -euo pipefail

# Use provided directory or default to /tmp
CERT_DIR="${1:-/tmp}"

echo "Generating TLS certificates for webhook using OpenSSL in ${CERT_DIR}..."

# Ensure the certificate directory exists
mkdir -p "${CERT_DIR}"

# Generate CA private key
openssl genrsa -out "${CERT_DIR}/ca-key.pem" 2048

# Generate CA certificate
openssl req -new -x509 -key "${CERT_DIR}/ca-key.pem" -out "${CERT_DIR}/ca.pem" -days 365 \
  -subj "/CN=Model Validation Operator CA/O=sigstore.dev"

# Generate webhook private key
openssl genrsa -out "${CERT_DIR}/example-webhook-key.pem" 2048

# Generate webhook certificate request
openssl req -new -key "${CERT_DIR}/example-webhook-key.pem" -out "${CERT_DIR}/example-webhook.csr" \
  -subj "/CN=model-validation-webhook.model-validation-operator-test.svc"

# Create extensions file for webhook certificate
cat > "${CERT_DIR}/webhook-extensions.cnf" <<EOF
subjectAltName=DNS:model-validation-webhook.model-validation-operator-test.svc.cluster.local,DNS:model-validation-webhook.model-validation-operator-test.svc,DNS:localhost,IP:127.0.0.1
basicConstraints=CA:FALSE
keyUsage=nonRepudiation,digitalSignature,keyEncipherment
EOF

# Generate webhook certificate signed by CA
openssl x509 -req -in "${CERT_DIR}/example-webhook.csr" -CA "${CERT_DIR}/ca.pem" -CAkey "${CERT_DIR}/ca-key.pem" \
  -CAcreateserial -out "${CERT_DIR}/example-webhook.pem" -days 365 \
  -extfile "${CERT_DIR}/webhook-extensions.cnf"

# Create individual certificate files for generate-manifests.sh to use
cp "${CERT_DIR}/example-webhook.pem" "${CERT_DIR}/tls.crt"
cp "${CERT_DIR}/example-webhook-key.pem" "${CERT_DIR}/tls.key"
cp "${CERT_DIR}/ca.pem" "${CERT_DIR}/ca.crt"

# Clean up temporary files
rm -f "${CERT_DIR}/example-webhook.csr" "${CERT_DIR}/webhook-extensions.cnf"

echo "TLS certificates generated:"
echo "  - CA certificate: ${CERT_DIR}/ca.crt"
echo "  - TLS certificate: ${CERT_DIR}/tls.crt"
echo "  - TLS private key: ${CERT_DIR}/tls.key"
echo ""
echo "Note: These will be integrated into the kustomize secret template by generate-manifests.sh"
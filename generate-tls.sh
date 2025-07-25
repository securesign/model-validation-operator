#!/bin/bash
# Generate TLS certificates for testing environment
# Used by scripts/generate-manifests.sh for testing overlay

set -euo pipefail

# Use provided directory or default to /tmp
CERT_DIR="${1:-/tmp}"

echo "Generating TLS certificates for webhook in ${CERT_DIR}..."

# Ensure the certificate directory exists
mkdir -p "${CERT_DIR}"

# Generate CA certificate
cfssl gencert -initca ./tls/ca-csr.json | cfssljson -bare "${CERT_DIR}/ca"

# Generate webhook certificate
cfssl gencert \
  -ca="${CERT_DIR}/ca.pem" \
  -ca-key="${CERT_DIR}/ca-key.pem" \
  -config=./tls/ca-config.json \
  -hostname="model-validation-webhook.model-validation-operator-test.svc.cluster.local,model-validation-webhook.model-validation-operator-test.svc,localhost,127.0.0.1" \
  -profile=default \
  ./tls/ca-csr.json | cfssljson -bare "${CERT_DIR}/example-webhook"

# Create individual certificate files for generate-manifests.sh to use
cp "${CERT_DIR}/example-webhook.pem" "${CERT_DIR}/tls.crt"
cp "${CERT_DIR}/example-webhook-key.pem" "${CERT_DIR}/tls.key"
cp "${CERT_DIR}/ca.pem" "${CERT_DIR}/ca.crt"

echo "TLS certificates generated:"
echo "  - CA certificate: ${CERT_DIR}/ca.crt"
echo "  - TLS certificate: ${CERT_DIR}/tls.crt"
echo "  - TLS private key: ${CERT_DIR}/tls.key"
echo ""
echo "Note: These will be integrated into the kustomize secret template by generate-manifests.sh"

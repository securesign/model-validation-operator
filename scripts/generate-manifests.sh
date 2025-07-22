#!/bin/bash
set -euo pipefail

# Script to generate manifests for different environments using kustomize overlays
# Usage: ./scripts/generate-manifests.sh <environment> [output-dir] [image]

DIR=$(cd "$(dirname "$0")" && pwd -P)
PROJECT_ROOT=$(dirname "$DIR")

# Default values
MODE=${1:-testing}
OUTPUT_DIR=${2:-manifests}
IMAGE=${3:-controller:latest}

# Webhook configuration defaults
WEBHOOK_SERVICE_NAME=${WEBHOOK_SERVICE_NAME:-"model-validation-webhook"}
WEBHOOK_NAMESPACE=""  # Will be set based on MODE if not provided

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

error() {
    echo -e "${RED}[ERROR]${NC} $1"
    exit 1
}

success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

# Help function
show_help() {
    cat << EOF
Usage: $0 <environment> [output-dir] [image]

Generate Kubernetes manifests for different environments using kustomize overlays.

Arguments:
    environment    Environment to generate manifests for (testing, production, development, olm)
    output-dir     Directory to output manifests to (default: manifests)
    image          Container image to use (default: controller:latest)

Examples:
    $0 testing
    $0 production dist ghcr.io/sigstore/model-validation-operator:v1.0.0
    $0 development manifests localhost:5000/model-validation-operator:dev
    $0 olm manifests ghcr.io/sigstore/model-validation-operator:v1.0.0

Available environments:
    testing        - Uses manual TLS certificates and webhook
    production     - Uses cert-manager for TLS and webhook
    development    - Basic operator without webhook
    olm            - Uses webhook with OLM-managed certificates

Environment Variables:
    WEBHOOK_SERVICE_NAME    Service name for webhook (default: model-validation-webhook)
    WEBHOOK_NAMESPACE       Namespace for webhook (default: based on environment)

EOF
}

# Check for help flag
if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
    show_help
    exit 0
fi

# Validate environment
if [[ ! -d "$PROJECT_ROOT/config/overlays/$MODE" ]]; then
    error "Unknown environment '$MODE'. Available environments: testing, production, development, olm"
fi

# Use kustomize from project bin if available, otherwise system
KUSTOMIZE="kustomize"
if [[ -f "$PROJECT_ROOT/bin/kustomize" ]]; then
    KUSTOMIZE="$PROJECT_ROOT/bin/kustomize"
elif ! command -v kustomize &> /dev/null; then
    error "kustomize is not installed or available. Run 'make kustomize' or install it manually."
fi

log "Generating manifests for '$MODE' environment..."

# Create output directory
mkdir -p "$OUTPUT_DIR"

# Determine namespace based on mode if not set
if [ -z "$WEBHOOK_NAMESPACE" ]; then
    case "$MODE" in
        testing)
            WEBHOOK_NAMESPACE="model-validation-operator-test"
            ;;
        production)
            WEBHOOK_NAMESPACE="model-validation-operator-system"
            ;;
        development)
            WEBHOOK_NAMESPACE="model-validation-operator-dev"
            ;;
        olm)
            WEBHOOK_NAMESPACE="model-validation-operator-system"
            ;;
        *)
            WEBHOOK_NAMESPACE="model-validation-operator-system"
            ;;
    esac
fi

# Update webhook configuration if ConfigMap exists in the overlay
update_webhook_config() {
    local overlay_dir="$PROJECT_ROOT/config/overlays/$MODE"
    local kustomization_file="$overlay_dir/kustomization.yaml"
    
    # Check if configMapGenerator exists in the kustomization
    if grep -q "configMapGenerator:" "$kustomization_file" && grep -q "webhook-config" "$kustomization_file"; then
        log "Updating webhook configuration..."
        log "  Service Name: $WEBHOOK_SERVICE_NAME"
        log "  Namespace: $WEBHOOK_NAMESPACE"
        
        cd "$overlay_dir"
        
        # Remove existing webhook-config configMapGenerator
        "$KUSTOMIZE" edit remove configmap webhook-config 2>/dev/null || true
        
        # Add new configMapGenerator with updated values
        if [[ "$MODE" == "production" ]]; then
            # Production mode uses cert-manager
            "$KUSTOMIZE" edit add configmap webhook-config \
                --from-literal="service-name=$WEBHOOK_SERVICE_NAME" \
                --from-literal="namespace=$WEBHOOK_NAMESPACE" \
                --from-literal="dns-name-short=${WEBHOOK_SERVICE_NAME}.${WEBHOOK_NAMESPACE}.svc" \
                --from-literal="dns-name-full=${WEBHOOK_SERVICE_NAME}.${WEBHOOK_NAMESPACE}.svc.cluster.local" \
                --from-literal="cert-annotation=${WEBHOOK_NAMESPACE}/serving-cert"
        else
            # Testing, development, and OLM modes don't use cert-manager
            "$KUSTOMIZE" edit add configmap webhook-config \
                --from-literal="service-name=$WEBHOOK_SERVICE_NAME" \
                --from-literal="namespace=$WEBHOOK_NAMESPACE" \
                --from-literal="dns-name-short=${WEBHOOK_SERVICE_NAME}.${WEBHOOK_NAMESPACE}.svc" \
                --from-literal="dns-name-full=${WEBHOOK_SERVICE_NAME}.${WEBHOOK_NAMESPACE}.svc.cluster.local"
        fi
        
        cd - > /dev/null
        
        success "Webhook configuration updated"
    fi
}

# Set image in the overlay
log "Setting image to: $IMAGE"
cd "$PROJECT_ROOT/config/overlays/$MODE"
"$KUSTOMIZE" edit set image controller="$IMAGE"
cd - > /dev/null

# Update webhook configuration
update_webhook_config

# Generate TLS certificates for testing mode
if [[ "$MODE" == "testing" ]]; then
    log "Generating TLS certificates for testing environment..."

    # Create a temporary directory for certificates
    TEMP_CERT_DIR=$(mktemp -d)
    trap "rm -rf $TEMP_CERT_DIR" EXIT

    log "Using temporary certificate directory: $TEMP_CERT_DIR"

    # Use the Makefile target to generate certificates
    log "Generating certificates using Makefile target..."
    (cd "$PROJECT_ROOT" && make generate-certs CERT_DIR="$TEMP_CERT_DIR")

    # Update the TLS secret in the component with generated certificates
    if [[ -f "$TEMP_CERT_DIR/tls.crt" && -f "$TEMP_CERT_DIR/tls.key" && -f "$TEMP_CERT_DIR/ca.crt" ]]; then
        TLS_CRT=$(base64 -w 0 < "$TEMP_CERT_DIR/tls.crt" 2>/dev/null || base64 -i "$TEMP_CERT_DIR/tls.crt")
        TLS_KEY=$(base64 -w 0 < "$TEMP_CERT_DIR/tls.key" 2>/dev/null || base64 -i "$TEMP_CERT_DIR/tls.key")
        CA_CRT=$(base64 -w 0 < "$TEMP_CERT_DIR/ca.crt" 2>/dev/null || base64 -i "$TEMP_CERT_DIR/ca.crt")

        # Update the TLS secret with actual certificate data
        sed -i.bak "s/tls.crt: \"\"/tls.crt: $TLS_CRT/g" "$PROJECT_ROOT/config/components/manual-tls/tls-secret.yaml"
        sed -i.bak "s/tls.key: \"\"/tls.key: $TLS_KEY/g" "$PROJECT_ROOT/config/components/manual-tls/tls-secret.yaml"
        sed -i.bak "s/ca.crt: \"\"/ca.crt: $CA_CRT/g" "$PROJECT_ROOT/config/components/manual-tls/tls-secret.yaml"

        # Clean up backup files
        rm -f "$PROJECT_ROOT/config/components/manual-tls/tls-secret.yaml.bak"

        success "TLS certificates generated and integrated"
    else
        error "Failed to generate TLS certificates. Check 'make generate-certs' target."
    fi
fi

# Generate manifests
log "Building manifests with kustomize..."
"$KUSTOMIZE" build "$PROJECT_ROOT/config/overlays/$MODE" > "$OUTPUT_DIR/$MODE.yaml"

# Reset TLS secret for testing mode (clean up for next run)
if [[ "$MODE" == "testing" ]]; then
    log "Resetting TLS secret template..."
    cat > "$PROJECT_ROOT/config/components/manual-tls/tls-secret.yaml" << EOF
# This secret will be populated by make generate-certs
apiVersion: v1
kind: Secret
metadata:
  name: example-webhook-tls
  namespace: system
type: kubernetes.io/tls
data:
  tls.crt: ""  # Will be populated by make generate-certs
  tls.key: ""  # Will be populated by make generate-certs
  ca.crt: ""   # Will be populated by make generate-certs
EOF
fi

success "Manifests generated successfully: $OUTPUT_DIR/$MODE.yaml"

# Show some useful information
log "Manifest summary:"
echo "  Environment: $MODE"
echo "  Output file: $OUTPUT_DIR/$MODE.yaml"
echo "  Image: $IMAGE"
echo "  Namespace: $(grep 'namespace:' "$PROJECT_ROOT/config/overlays/$MODE/kustomization.yaml" | awk '{print $2}')"
if grep -q "webhook-config" "$PROJECT_ROOT/config/overlays/$MODE/kustomization.yaml" 2>/dev/null; then
    echo "  Webhook Service: $WEBHOOK_SERVICE_NAME"
    echo "  Webhook Namespace: $WEBHOOK_NAMESPACE"
fi

log "To deploy:"
echo "  kubectl apply -f $OUTPUT_DIR/$MODE.yaml"

log "To undeploy:"
echo "  kubectl delete -f $OUTPUT_DIR/$MODE.yaml"

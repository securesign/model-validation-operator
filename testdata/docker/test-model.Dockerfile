FROM golang:1.26 AS fixture-source

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download
RUN fixture_dir="$(go list -m -f '{{.Dir}}' github.com/sigstore/model-signing)" && \
    sigstore_dir="$(go list -m -f '{{.Dir}}' github.com/sigstore/sigstore)" && \
    mkdir -p /fixtures/keys/certificate /fixtures/keys/sigstore && \
    cp -R "${fixture_dir}/scripts/tests/v1.1.0-sigstore" /fixtures/ && \
    cp -R "${fixture_dir}/scripts/tests/v1.1.0-certificate" /fixtures/ && \
    cp "${fixture_dir}/scripts/tests/keys/certificate/ca-cert.pem" /fixtures/keys/certificate/ && \
    cp "${sigstore_dir}/pkg/tuf/repository/targets/trusted_root.json" /fixtures/keys/sigstore/

FROM busybox

# Copy the real TensorFlow SavedModel files from testdata
COPY testdata/tensorflow_saved_model/ /data/

# Copy public keys for verification tests - separate directory
RUN mkdir -p /keys
COPY testdata/docker/test_public_key.pub /keys/test_public_key.pub
COPY testdata/docker/test_invalid_public_key.pub /keys/test_invalid_public_key.pub
COPY --from=fixture-source /fixtures/ /fixtures/

# Make files readable and ensure no stray public key files in model directory
RUN chmod -R a+rX /data /keys /fixtures && rm -f /data/test_public_key.pub /data/*.pub

# Default command
CMD ["sleep", "3600"]

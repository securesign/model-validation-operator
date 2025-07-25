// Package constants provides shared constants used throughout the model validation operator
package constants

var (
	// ModelTransparencyCliImage is the default image for the model transparency CLI
	// used as an init container to validate model signatures
	ModelTransparencyCliImage = "ghcr.io/sigstore/model-transparency-cli:v1.0.1"
)

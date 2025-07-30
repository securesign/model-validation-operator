package constants

const (
	// ModelValidationDomain is the domain used for model validation labels
	ModelValidationDomain = "validation.ml.sigstore.dev"

	// ModelValidationLabel is the label used to enable model validation for a pod
	ModelValidationLabel = ModelValidationDomain + "/ml"

	// IgnoreNamespaceLabel is the label used to ignore a namespace for model validation
	IgnoreNamespaceLabel = ModelValidationDomain + "/ignore"

	// ModelValidationInitContainerName is the name of the init container injected for model validation
	ModelValidationInitContainerName = "model-validation"
)

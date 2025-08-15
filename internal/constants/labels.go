// Copyright 2025 The Sigstore Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

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

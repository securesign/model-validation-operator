#!/usr/bin/env bash
set -euo pipefail


IMG_NAME="${IMG%@*}"
IMG_DIGEST="${IMG#*@}"

cat << EOF >> "config/overlays/${BUNDLE_OVERLAY}/kustomization.yaml"

images:
- digest: ${IMG_DIGEST}
  name: controller
  newName: ${IMG_NAME}
EOF

# Generate and validate the Operator bundle
oc kustomize "config/overlays/${BUNDLE_OVERLAY}" | operator-sdk generate bundle ${BUNDLE_GEN_FLAGS}

CSV="bundle/manifests/model-validation-operator.clusterserviceversion.yaml"

if [[ -f "${CSV}" ]]; then
  sed -i.bak  's/deploymentName: webhook/deploymentName: model-validation-controller-manager/' "${CSV}"
  sed -i.bak2 's/deploymentName: model-validation-controller-manager/deploymentName: model-validation-controller-manager\
    serviceName: model-validation-webhook\
    containerPort: 9443/' "${CSV}"
  rm -f "${CSV}.bak" "${CSV}.bak2"
fi

operator-sdk bundle validate ./bundle

#!/usr/bin/env bash
set -euo pipefail

TOOLS="/tmp"

if [ -d "/cachi2" ]; then
  tar -xzf /cachi2/output/deps/generic/kustomize_v5.6.0_linux_amd64.tar.gz -C "${TOOLS}"
  KUSTOMIZE="${TOOLS}/kustomize"
else
  curl -Lo "${TOOLS}/kustomize.tar.gz" "https://github.com/kubernetes-sigs/kustomize/releases/download/kustomize%2Fv5.6.0/kustomize_v5.6.0_linux_amd64.tar.gz"
  tar -xzf "${TOOLS}/kustomize.tar.gz" -C "${TOOLS}"
  rm "${TOOLS}/kustomize.tar.gz"
  KUSTOMIZE="${TOOLS}/kustomize"
fi
chmod +x "${KUSTOMIZE}"

operator-sdk generate kustomize manifests -q

if [[ -n "${IMG:-}" ]]; then
  pushd "config/overlays/${BUNDLE_OVERLAY}" >/dev/null
  "${KUSTOMIZE}" edit set image "controller=${IMG}"
  popd >/dev/null
fi

"${KUSTOMIZE}" build "config/overlays/${BUNDLE_OVERLAY}" \
  | operator-sdk generate bundle ${BUNDLE_GEN_FLAGS}

CSV="bundle/manifests/model-validation-operator.clusterserviceversion.yaml"
if [[ -f "${CSV}" ]]; then
  sed -i.bak  's/deploymentName: webhook/deploymentName: model-validation-controller-manager/' "${CSV}"
  sed -i.bak2 's/deploymentName: model-validation-controller-manager/deploymentName: model-validation-controller-manager\
    serviceName: model-validation-webhook/' "${CSV}"
  rm -f "${CSV}.bak" "${CSV}.bak2"
fi

operator-sdk bundle validate ./bundle

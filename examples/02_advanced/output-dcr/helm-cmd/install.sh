#!/bin/sh
# Installs demo-webapp in the namespace demo, one Helm release per chart: its
# upstream chart with its values file, written by okdp-compile.
set -eu
cd "$(dirname "$0")"
helm upgrade --install demo-webapp-oidc-dcr oci://quay.io/adaltas/oidc-dcr --version 0.5.0 --namespace demo -f oidc-dcr.values.yaml
helm upgrade --install demo-webapp-app app --repo https://charts.example.org --version 1.8.0 --namespace demo -f app.values.yaml
helm upgrade --install demo-webapp-okdp oci://registry.example.org/okdp/okdp-objects --version 1.0.0 --namespace demo -f okdp.values.yaml

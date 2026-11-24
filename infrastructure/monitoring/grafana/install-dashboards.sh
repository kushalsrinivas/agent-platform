#!/usr/bin/env sh
# Install the provisioned dashboards as a labelled ConfigMap for the Grafana sidecar.
set -eu
DIR=$(CDPATH= cd -- "$(dirname -- "$0")/dashboards" && pwd)
kubectl -n monitoring create configmap agent-platform-dashboards \
  --from-file="$DIR" --dry-run=client -o yaml \
  | kubectl label --local -f - grafana_dashboard=1 -o yaml \
  | kubectl apply -f -

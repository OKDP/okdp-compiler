#!/usr/bin/env bash
# Regenerates the Helm outputs the compiler tests compare with, using the helm
# binary on the PATH (Helm 4). Run from anywhere:
#
#   go test ./compiler -run TestHelmValues -update   # values/<case>/final.yaml
#   compiler/testdata/helm/generate.sh
#
# values/<case>/: a chart whose values.yaml is <case>/values.yaml, rendered
#   with -f <case>/layer-*.yaml in order: .Values as JSON in helm.json; and
#   rendered with -f <case>/final.yaml (the compiler's final values for these
#   layers): helm-final.json.
# schema/<case>/: a chart with <case>/values.schema.json and values.yaml,
#   rendered with -f <case>/layer-*.yaml: what helm reports in helm.txt
#   ("ok" when the values are valid).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# render <chart dir> <out> [helm args]: the data.out key of the ConfigMap
# the chart renders.
render() {
  local chart=$1 out=$2
  shift 2
  if ! helm template t "$chart" --namespace demo "$@" > "$tmp/render.yaml" 2> "$tmp/render.err"; then
    echo "$out: helm template failed:" >&2
    cat "$tmp/render.err" >&2
    exit 1
  fi
  python3 -c 'import sys, yaml; d = next(x for x in yaml.safe_load_all(open(sys.argv[1])) if x); sys.stdout.write(d["data"]["out"])' "$tmp/render.yaml" > "$out"
}

chart=$tmp/values
mkdir -p "$chart/templates"
printf 'apiVersion: v2\nname: values\nversion: 1.0.0\n' > "$chart/Chart.yaml"
cat > "$chart/templates/values.yaml" <<'EOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: values
data:
  out: {{ toPrettyJson .Values | quote }}
EOF
for c in "$here"/values/*/; do
  cp "$c/values.yaml" "$chart/values.yaml"
  layers=()
  for l in "$c"/layer-*.yaml; do layers+=(-f "$l"); done
  render "$chart" "$c/helm.json" "${layers[@]}"
  if [[ -f $c/final.yaml ]]; then
    render "$chart" "$c/helm-final.json" -f "$c/final.yaml"
  fi
done

chart=$tmp/schema
mkdir -p "$chart/templates"
printf 'apiVersion: v2\nname: schema\nversion: 1.0.0\n' > "$chart/Chart.yaml"
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n' > "$chart/templates/x.yaml"
for c in "$here"/schema/*/; do
  cp "$c/values.yaml" "$c/values.schema.json" "$chart/"
  layers=()
  for l in "$c"/layer-*.yaml; do layers+=(-f "$l"); done
  if helm template t "$chart" "${layers[@]}" > /dev/null 2> "$tmp/err"; then
    echo ok > "$c/helm.txt"
  else
    grep -v '^level=' "$tmp/err" > "$c/helm.txt"
  fi
done

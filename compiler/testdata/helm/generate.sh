#!/usr/bin/env bash
# Regenerates the Helm outputs the compiler tests compare with, using the helm
# binary on the PATH (Helm 4) and the okdp-lib-chart checkout $OKDP_LIB_CHART
# (its templates are what the compiler reproduces). Run from anywhere:
#
#   go test ./compiler -run TestHelmValues -update   # values/<case>/final.yaml
#   OKDP_LIB_CHART=../okdp-lib-chart compiler/testdata/helm/generate.sh
#
# values/<case>/: a chart whose values.yaml is <case>/values.yaml, rendered
#   with -f <case>/layer-*.yaml in order: .Values as JSON in helm.json; and
#   rendered with -f <case>/final.yaml (the compiler's final values for these
#   layers): helm-final.json.
# schema/<case>/: a chart with <case>/values.schema.json and values.yaml,
#   rendered with -f <case>/layer-*.yaml: what helm reports in helm.txt
#   ("ok" when the values are valid).
# functions/: Helm's sha256sum, toJson, toPrettyJson, toYaml and quote of
#   each input of functions/values.yaml, and okdp.fullname of each suffix:
#   helm.json.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# render <release> <chart dir> <out> [helm args]: the data.out key of the
# first object the chart renders.
render() {
  local release=$1 chart=$2 out=$3
  shift 3
  if ! helm template "$release" "$chart" --namespace demo "$@" > "$tmp/render.yaml" 2> "$tmp/render.err"; then
    echo "$out: helm template failed:" >&2
    cat "$tmp/render.err" >&2
    exit 1
  fi
  python3 -c 'import sys, yaml; d = next(x for x in yaml.safe_load_all(open(sys.argv[1])) if x); sys.stdout.write(d["data"]["out"])' "$tmp/render.yaml" > "$out"
}

# newchart <name>: an empty chart in $tmp/<name>.
newchart() {
  chart=$tmp/$1
  mkdir -p "$chart/templates"
  printf 'apiVersion: v2\nname: %s\nversion: 1.0.0\n' "$1" > "$chart/Chart.yaml"
}

newchart values
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
  render t "$chart" "$c/helm.json" "${layers[@]}"
  if [[ -f $c/final.yaml ]]; then
    render t "$chart" "$c/helm-final.json" -f "$c/final.yaml"
  fi
done

newchart schema
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

newchart functions
cp "$here/functions/values.yaml" "$chart/values.yaml"
cat > "$chart/templates/functions.yaml" <<'EOF'
{{- $out := dict }}
{{- range $k, $v := .Values.inputs }}
{{- $r := dict "toJson" (toJson $v) "toPrettyJson" (toPrettyJson $v) "toYaml" (toYaml $v) "quote" (quote $v) }}
{{- if not (kindIs "invalid" $v) }}{{ $_ := set $r "sha256" (sha256sum (toString $v)) }}{{ end }}
{{- $_ := set $out $k $r }}
{{- end }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: functions
data:
  out: {{ toJson (dict "functions" $out) | quote }}
EOF
render demo-a-rather-long-release-name-for-names "$chart" "$here/functions/helm.json"

# labels/<case>/: okdp.labels and okdp.selectorLabels, without and with a
# component, of a chart with <case>/Chart.yaml: helm.json.
for c in "$here"/labels/*/; do
  rm -rf "$tmp/labels"
  newchart labels
  cp "$c/Chart.yaml" "$chart/Chart.yaml"
  cat > "$chart/templates/labels.yaml" <<'EOF'
{{- $out := dict
  "labels" (include "okdp.labels" $ | fromYaml)
  "labelsComponent" (include "okdp.labels" (dict "ctx" $ "component" "hook") | fromYaml)
  "selectorLabels" (include "okdp.selectorLabels" $ | fromYaml)
  "selectorLabelsComponent" (include "okdp.selectorLabels" (dict "ctx" $ "component" "hook") | fromYaml) }}
apiVersion: v1
kind: ConfigMap
metadata:
  name: labels
data:
  out: {{ toJson (dict "release" .Release.Name "chart" .Chart "out" $out) | quote }}
EOF
  render demo-labels "$chart" "$c/helm.json"
done

#!/usr/bin/env bash
# Regenerates the Helm outputs the compiler tests compare with, using the helm
# binary on the PATH (Helm 4) and the okdp-lib-chart checkout $OKDP_LIB_CHART
# (its templates are what the compiler reproduces). Run from anywhere:
#
#   go test ./internal/chart -run TestHelmValues -update   # values/<case>/final.yaml
#   go test ./internal/fsio -run TestHelmUmbrella -update  # umbrella/Chart.yaml, instance.values.yaml
#   UMBRELLA_HELMS="<helm> ..." testdata/helm/generate.sh
#
# values/<case>/: a chart whose values.yaml is <case>/values.yaml, rendered
#   with -f <case>/layer-*.yaml in order: .Values as JSON in helm.json; and
#   rendered with -f <case>/final.yaml (the compiler's final values for these
#   layers): helm-final.json.
# umbrella/: the umbrella the compiler writes over the values cases
#   (Chart.yaml, instance.values.yaml), each case's chart a dependency named
#   after the case, rendered with -f instance.values.yaml: the .Values of
#   each subchart, by its .Chart.Name (the key), in helm.json. Rendered too
#   by each helm binary of $UMBRELLA_HELMS (paths, separated by spaces; the
#   deployers' Helm versions): helm<major>.json, none when it is helm.json;
#   the binaries of one major version must render the same.
# schema/<case>/: a chart with <case>/values.schema.json and values.yaml,
#   rendered with -f <case>/layer-*.yaml: what helm reports in helm.txt
#   ("ok" when the values are valid; Helm's order varies, a report with the
#   same lines is not rewritten).
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

# The umbrella: a chart per values case, named after it.
u=$tmp/umbrella
mkdir -p "$u"
for c in "$here"/values/*/; do
  name=$(basename "$c")
  mkdir -p "$tmp/$name/templates"
  printf 'apiVersion: v2\nname: %s\nversion: 1.0.0\n' "$name" > "$tmp/$name/Chart.yaml"
  cp "$c/values.yaml" "$tmp/$name/values.yaml"
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Chart.Name }}\ndata:\n  out: {{ toJson .Values | quote }}\n' > "$tmp/$name/templates/values.yaml"
done
cp "$here/umbrella/Chart.yaml" "$u/"

# umbrella <helm> <out>: the umbrella built and rendered by the binary
# <helm>: the .Values of each subchart, by its .Chart.Name, in <out>.
umbrella() {
  local helm=$1 out=$2
  "$helm" dependency build "$u" > /dev/null
  if ! "$helm" template t "$u" --namespace demo -f "$here/umbrella/instance.values.yaml" > "$tmp/render.yaml" 2> "$tmp/render.err"; then
    echo "umbrella ($helm): helm template failed:" >&2
    cat "$tmp/render.err" >&2
    exit 1
  fi
  python3 -c '
import json, sys, yaml
out = {d["metadata"]["name"]: json.loads(d["data"]["out"]) for d in yaml.safe_load_all(open(sys.argv[1])) if d}
json.dump(out, open(sys.argv[2], "w"), indent=2, sort_keys=True)
open(sys.argv[2], "a").write("\n")' "$tmp/render.yaml" "$out"
}
umbrella helm "$here/umbrella/helm.json"
declare -A first # major -> the first version rendered
read -ra helms <<< "${UMBRELLA_HELMS:-}"
for h in "${helms[@]}"; do
  v=$("$h" version --template '{{.Version}}' 2> /dev/null)
  major=${v#v}
  major=${major%%.*}
  umbrella "$h" "$tmp/umbrella-$v.json"
  out=helm$major.json
  if [[ -n ${first[$major]:-} ]]; then
    if ! cmp -s "$tmp/umbrella-$v.json" "$tmp/umbrella-${first[$major]}.json"; then
      echo "umbrella: Helm $v renders differently from Helm ${first[$major]}" >&2
      exit 1
    fi
  else
    first[$major]=$v
    if cmp -s "$tmp/umbrella-$v.json" "$here/umbrella/helm.json"; then
      rm -f "$here/umbrella/$out"
    else
      cp "$tmp/umbrella-$v.json" "$here/umbrella/$out"
    fi
  fi
  [[ -f $here/umbrella/$out ]] || out=helm.json
  echo "umbrella: Helm $v renders $out"
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
    grep -v '^level=' "$tmp/err" > "$tmp/report"
    # Kept when only the order changes.
    if ! cmp -s <(sort "$tmp/report") <(sort "$c/helm.txt" 2> /dev/null); then
      mv "$tmp/report" "$c/helm.txt"
    fi
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

# The chart of the secrets cases (the tests build the same).
svc='apiVersion: v2\nname: svc\nversion: 1.2.3\nappVersion: "4.5"\n'

# secrets/generated/<case>.yaml: okdp.generatedSecret with the arguments
# under args, rendered in <case>.helm.yaml (or the first line of the error
# in <case>.helm.txt).
newchart secrets
printf "$svc" > "$chart/Chart.yaml"
printf '{{ include "okdp.generatedSecret" (merge (dict "ctx" $) .Values.args) }}\n' > "$chart/templates/secret.yaml"
for f in "$here"/secrets/generated/*.yaml; do
  [[ $f == *.helm.yaml ]] && continue
  rm -f "${f%.yaml}.helm.yaml" "${f%.yaml}.helm.txt"
  if helm template demo-secrets "$chart" --namespace demo -f "$f" > "$tmp/out" 2> "$tmp/err"; then
    mv "$tmp/out" "${f%.yaml}.helm.yaml"
  else
    grep -v -m1 '^level=' "$tmp/err" > "${f%.yaml}.helm.txt"
  fi
done

# secrets/descriptor/: okdp.descriptor of a chart with outputs.tpl (outputs
# with a secret), in helm.yaml.
newchart svc
d=$here/secrets/descriptor
printf "$svc" > "$chart/Chart.yaml"
sed -i '/define "okdp.instance.\(url\|usage\|outputs\)"/d' "$chart/templates/_descriptor.tpl"
cp "$d/outputs.tpl" "$chart/templates/_outputs.tpl"
printf '{{ include "okdp.descriptor" . }}\n' > "$chart/templates/descriptor.yaml"
helm template demo-secrets "$chart" --namespace demo > "$d/helm.yaml"

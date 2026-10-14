#!/usr/bin/env bash
# Regenerates the Helm outputs the compiler tests compare with, using the helm
# binary on the PATH (Helm 4, the reference) and the helm binaries the
# deployers embed ($UMBRELLA_HELMS). Run from anywhere:
#
#   go test ./internal/chart -run TestHelmValues -update   # values/<case>/final.yaml
#   go test ./internal/umbrella -run TestHelmUmbrella -update  # umbrella/Chart.yaml, instance.values.yaml
#   UMBRELLA_HELMS="<helm> ..." testdata/helm/generate.sh
#
# It needs bash (3.2 or later) and python3 with PyYAML; nothing that differs
# between the GNU and BSD tools.
#
# $UMBRELLA_HELMS: the paths of the deployers' helm binaries, separated by
#   spaces (docs/00_architecture.md#helm-fixtures). Each renders every
#   fixture again; the binaries of one major version must render the same.
#   What a major version M renders differently from the helm on the PATH
#   goes beside the reference file, helm renamed helmM (helm3.json,
#   helm3-final.json, helm3.txt), removed when it renders the same. Unset,
#   the helm<M> files and their versions in SOURCE are left as they are.
#
# values/<case>/: a chart whose values.yaml is <case>/values.yaml, rendered
#   with -f <case>/layer-*.yaml in order: .Values as JSON in helm.json; and
#   rendered with -f <case>/final.yaml (the compiler's final values for these
#   layers): helm-final.json. helm<M>.json: Helm M with the layers, where it
#   differs from helm.json; helm<M>-final.json: Helm M with the final values,
#   where it differs from Helm M with the layers.
# umbrella/: the umbrella the compiler writes over the values cases
#   (Chart.yaml, instance.values.yaml, okdp.objects.yaml,
#   templates/okdp.yaml), each case's chart a dependency named after the
#   case, rendered with -f instance.values.yaml: the .Values of each
#   subchart, by its .Chart.Name (the key), in helm.json; helm<M>.json. The
#   manifests of templates/okdp.yaml, in Helm's order, one document each:
#   helm-objects.yaml, which every version must render the same.
# schema/<case>/: a chart with <case>/values.schema.json and values.yaml,
#   rendered with -f <case>/layer-*.yaml: what helm reports in helm.txt
#   ("ok" when the values are valid; Helm's order varies, a report with the
#   same lines is not rewritten); helm<M>.txt where the lines differ.
# functions/: Helm's sha256sum, toJson, toPrettyJson, toYaml and quote of
#   each input of functions/values.yaml (given with -f: user values, whose
#   nulls every Helm version keeps): helm.json; helm<M>.json.
# SOURCE: what rendered the fixtures: the version of the helm on the PATH
#   (helm) and the deployers' (helms).
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/helm-fixtures.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
r=$tmp/r # the renders
mkdir "$r"
label= # the fixture rendered, for the messages

if ! ref=$(helm version --template '{{.Version}}' 2> /dev/null); then
  echo "no helm on the PATH" >&2
  exit 1
fi
ref=${ref#v}

# The deployers' binaries (helms) and their versions, latest first; lead[i]:
# the index of the latest binary of the major version of helms[i]; majors:
# the major versions, each with the index of its latest binary in leads.
helms=() versions=() lead=() majors=() leads=()
read -ra given <<< "${UMBRELLA_HELMS:-}"
for ((i = 0; i < ${#given[@]}; i++)); do
  if ! v=$("${given[i]}" version --template '{{.Version}}' 2> /dev/null) || [[ -z $v ]]; then
    echo "UMBRELLA_HELMS: ${given[i]} is not a helm binary" >&2
    exit 1
  fi
  echo "${v#v} ${given[i]}"
done > "$tmp/helms"
sort -t. -k1,1nr -k2,2nr -k3,3nr "$tmp/helms" > "$tmp/helms.sorted"
while read -r v h; do
  m=${v%%.*} l=${#helms[@]}
  for ((k = 0; k < ${#majors[@]}; k++)); do
    if [[ ${majors[k]} == "$m" ]]; then l=${leads[k]}; fi
  done
  if ((l == ${#helms[@]})); then majors+=("$m") leads+=("$l"); fi
  helms+=("$h") versions+=("$v") lead+=("$l")
done < "$tmp/helms.sorted"

# versions_of <major>: the deployers' versions of a major version.
versions_of() {
  local i out=
  for ((i = 0; i < ${#versions[@]}; i++)); do
    if [[ ${versions[i]%%.*} == "$1" ]]; then out+="${out:+, }${versions[i]}"; fi
  done
  echo "$out"
}

# same <kind> <a> <b>: whether two renders are the same: bytes, lines (the
# same lines in any order) or yaml (the same YAML documents).
same() {
  case $1 in
    bytes) cmp -s "$2" "$3" ;;
    lines) [[ -f $3 ]] && cmp -s <(sort "$2") <(sort "$3") ;;
    yaml) cmp -s "$2" "$3" || python3 -c '
import sys, yaml
try:
    a, b = (list(yaml.safe_load_all(open(f))) for f in sys.argv[1:])
except (OSError, yaml.YAMLError):
    sys.exit(1)
sys.exit(a != b)' "$2" "$3" ;;
  esac
}

# put <kind> <src> <dest>: <src> copied to <dest>, unless they are the same.
put() {
  same "$1" "$2" "$3" || cp "$2" "$3"
}

# run <kind> <name> <fn> [args]: <fn> <helm> <file> [args] writes what the
# binary <helm> renders in <file>: the helm on the PATH in $r/<name>, the
# deployers' binary i in $r/<name>.<i>; the binaries of one major version
# must render the same (<kind>, as same compares).
run() {
  local kind=$1 name=$2 fn=$3 i l
  shift 3
  "$fn" helm "$r/$name" "$@"
  for ((i = 0; i < ${#helms[@]}; i++)); do
    "$fn" "${helms[i]}" "$r/$name.$i" "$@"
    l=${lead[i]}
    if ((l != i)) && ! same "$kind" "$r/$name.$i" "$r/$name.$l"; then
      echo "$label: Helm ${versions[i]} renders differently from Helm ${versions[l]}" >&2
      exit 1
    fi
  done
}

# keep <kind> <name> <out> [<base>]: the render of <name> by each major
# version M of the deployers in <out> with helm renamed helmM, removed when
# it is the same as <out>, or as M's render of <base>.
keep() {
  local kind=$1 name=$2 out=$3 base=${4:-} k j b dest against
  for ((k = 0; k < ${#majors[@]}; k++)); do
    j=${leads[k]} b=${out##*/}
    dest=${out%/*}/${b/helm/helm${majors[k]}}
    against=$out
    if [[ -n $base ]]; then against=$r/$base.$j; fi
    if same "$kind" "$r/$name.$j" "$against"; then
      rm -f "$dest"
    else
      put "$kind" "$r/$name.$j" "$dest"
      echo "${dest#"$here"/}: Helm $(versions_of "${majors[k]}")"
    fi
  done
}

# alike <kind> <name>: every deployer's binary renders <name> as the helm on
# the PATH does.
alike() {
  local i
  for ((i = 0; i < ${#helms[@]}; i++)); do
    if ! same "$1" "$r/$2.$i" "$r/$2"; then
      echo "$label: Helm ${versions[i]} renders differently from Helm $ref:" >&2
      diff "$r/$2" "$r/$2.$i" >&2 || true
      exit 1
    fi
  done
}

# render <helm> <out> <release> <chart> [helm args]: the data.out key of the
# first object the chart renders.
render() {
  local helm=$1 out=$2 release=$3 chart=$4
  shift 4
  if ! "$helm" template "$release" "$chart" --namespace demo "$@" > "$tmp/render.yaml" 2> "$tmp/render.err"; then
    echo "$label ($helm): helm template failed:" >&2
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
  c=${c%/} label=values/${c##*/}
  cp "$c/values.yaml" "$chart/values.yaml"
  layers=()
  for l in "$c"/layer-*.yaml; do layers+=(-f "$l"); done
  run bytes layers render t "$chart" "${layers[@]}"
  cp "$r/layers" "$c/helm.json"
  keep bytes layers "$c/helm.json"
  if [[ -f $c/final.yaml ]]; then
    run bytes final render t "$chart" -f "$c/final.yaml"
    cp "$r/final" "$c/helm-final.json"
    keep bytes final "$c/helm-final.json" layers
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
cp "$here/umbrella/Chart.yaml" "$here/umbrella/okdp.objects.yaml" "$u/"
mkdir "$u/templates"
cp "$here/umbrella/templates/okdp.yaml" "$u/templates/"

# build <helm>: the umbrella built and rendered by the binary <helm>: the
# manifests of its subcharts in $tmp/charts.yaml, those of its own template
# in $tmp/objects.yaml, by the "# Source: <template>" line that heads each,
# in Helm's order, one YAML document each, without the blank lines around
# it.
build() {
  local helm=$1
  "$helm" dependency build "$u" > /dev/null
  if ! "$helm" template t "$u" --namespace demo -f "$here/umbrella/instance.values.yaml" > "$tmp/render.yaml" 2> "$tmp/render.err"; then
    echo "$label ($helm): helm template failed:" >&2
    cat "$tmp/render.err" >&2
    exit 1
  fi
  python3 -c '
import re, sys
out = {"charts/": [], "templates/okdp.yaml": []}
for x in re.split(r"(?m)^---\n", open(sys.argv[1]).read()):
    s, _, b = x.partition("\n")
    for k in out:
        if s.startswith("# Source: umbrella/" + k):
            out[k].append(b.strip() + "\n")
open(sys.argv[2], "w").write("---\n".join(out["charts/"]))
open(sys.argv[3], "w").write("---\n".join(out["templates/okdp.yaml"]))' "$tmp/render.yaml" "$tmp/charts.yaml" "$tmp/objects.yaml"
}

# umbrella <helm> <out>: the .Values of each subchart of the umbrella the
# binary <helm> renders, by its .Chart.Name, in <out>.
umbrella() {
  build "$1"
  python3 -c '
import json, sys, yaml
out = {d["metadata"]["name"]: json.loads(d["data"]["out"]) for d in yaml.safe_load_all(open(sys.argv[1])) if d}
json.dump(out, open(sys.argv[2], "w"), indent=2, sort_keys=True)
open(sys.argv[2], "a").write("\n")' "$tmp/charts.yaml" "$2"
}

# objects <helm> <out>: the manifests of templates/okdp.yaml the binary
# <helm> renders, in <out>.
objects() {
  build "$1"
  cp "$tmp/objects.yaml" "$2"
}
label=umbrella
run bytes umbrella umbrella
cp "$r/umbrella" "$here/umbrella/helm.json"
keep bytes umbrella "$here/umbrella/helm.json"
label=umbrella/objects
run bytes objects objects
alike bytes objects
cp "$r/objects" "$here/umbrella/helm-objects.yaml"

# report <helm> <out> [helm args]: what helm reports on the values of the
# schema chart, ok when they are valid.
report() {
  local helm=$1 out=$2
  shift 2
  if "$helm" template t "$chart" "$@" > /dev/null 2> "$tmp/err"; then
    echo ok > "$out"
  else
    awk '!/^level=/' "$tmp/err" > "$out"
  fi
}
newchart schema
printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n' > "$chart/templates/x.yaml"
for c in "$here"/schema/*/; do
  c=${c%/} label=schema/${c##*/}
  cp "$c/values.yaml" "$c/values.schema.json" "$chart/"
  layers=()
  for l in "$c"/layer-*.yaml; do layers+=(-f "$l"); done
  run lines report report "${layers[@]}"
  put lines "$r/report" "$c/helm.txt" # kept when only the order changes
  keep lines report "$c/helm.txt"
done

newchart functions
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
label=functions
run bytes functions render demo "$chart" -f "$here/functions/values.yaml"
cp "$r/functions" "$here/functions/helm.json"
keep bytes functions "$here/functions/helm.json"

# SOURCE.
if ((${#versions[@]})); then
  deployers=${versions[*]}
else
  deployers=$(sed -n 's/^helms //p' "$here/SOURCE" 2> /dev/null || true)
  echo "UMBRELLA_HELMS unset: the helm<major> files and the helms of SOURCE are left as they are" >&2
fi
{
  echo "# What rendered the fixtures of testdata/helm (generate.sh): the helm on"
  echo "# the PATH and the deployers' (UMBRELLA_HELMS)."
  echo "helm $ref"
  echo "helms${deployers:+ $deployers}"
} > "$here/SOURCE"

#!/usr/bin/env bash
# Downloads the editor libraries into web/assets/vendor/ so they get compiled
# into the binary by //go:embed. Run once after cloning; the result is committed.
#
#   ./scripts/fetch-vendor.sh
set -euo pipefail

CM=5.65.16
JSYAML=4.1.0
QR=1.0.0

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dest="$root/web/assets/vendor"
mkdir -p "$dest"

get() { # get <url> <filename>
  printf '  %-24s' "$2"
  curl -fsSL --retry 3 --max-time 60 "$1" -o "$dest/$2"
  printf '%8s\n' "$(wc -c <"$dest/$2" | tr -d ' ')"
}

base="https://cdnjs.cloudflare.com/ajax/libs/codemirror/$CM"

echo "CodeMirror $CM"
get "$base/codemirror.min.js"                    codemirror.min.js
get "$base/codemirror.min.css"                   codemirror.min.css
get "$base/mode/yaml/yaml.min.js"                yaml.min.js
get "$base/addon/edit/matchbrackets.min.js"      matchbrackets.min.js
get "$base/addon/selection/active-line.min.js"   active-line.min.js
get "$base/addon/fold/foldcode.min.js"           foldcode.min.js
get "$base/addon/fold/foldgutter.min.js"         foldgutter.min.js
get "$base/addon/fold/foldgutter.min.css"        foldgutter.min.css
get "$base/addon/fold/indent-fold.min.js"        indent-fold.min.js
get "$base/addon/lint/lint.min.js"               lint.min.js
get "$base/addon/lint/lint.min.css"              lint.min.css

echo "js-yaml $JSYAML"
get "https://cdnjs.cloudflare.com/ajax/libs/js-yaml/$JSYAML/js-yaml.min.js" js-yaml.min.js

echo "qrcodejs $QR"
get "https://cdnjs.cloudflare.com/ajax/libs/qrcodejs/$QR/qrcode.min.js" qrcode.min.js

echo
echo "done -> web/assets/vendor"

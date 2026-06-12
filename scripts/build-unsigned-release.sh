#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${1:-$(awk '/MARKETING_VERSION:/ {print $2; exit}' MacBridge/project.yml)}"
build_number="${BUILD_NUMBER:-$(awk '/CURRENT_PROJECT_VERSION:/ {print $2; exit}' MacBridge/project.yml)}"
commit="$(git rev-parse --short=12 HEAD)"
build_date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
derived_data="$repo_root/build/unsigned-release"
app="$derived_data/Build/Products/Release/CCCodeBridge.app"

if [[ -z "${DEVELOPER_DIR:-}" ]]; then
  if [[ -d /Applications/Xcode.app ]]; then
    export DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer
  elif [[ -d /Applications/Xcode-beta.app ]]; then
    export DEVELOPER_DIR=/Applications/Xcode-beta.app/Contents/Developer
  fi
fi

rm -rf "$derived_data"
mkdir -p dist

xcodebuild \
  -project MacBridge/CCCodeBridge.xcodeproj \
  -scheme CCCodeBridge \
  -configuration Release \
  -destination 'platform=macOS' \
  -derivedDataPath "$derived_data" \
  MARKETING_VERSION="$version" \
  CURRENT_PROJECT_VERSION="$build_number" \
  CCCODE_RUNTIME_VERSION="$version" \
  CCCODE_RUNTIME_COMMIT="$commit" \
  CCCODE_RUNTIME_DATE="$build_date" \
  CODE_SIGN_IDENTITY=- \
  CODE_SIGNING_REQUIRED=YES \
  CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO \
  build

test -x "$app/Contents/MacOS/CCCodeBridge"
test -x "$app/Contents/Resources/cccode-bridge-runtime"
codesign --verify --deep --strict "$app"
if codesign -d --entitlements :- "$app" 2>/dev/null | grep -q 'get-task-allow'; then
  echo "Unsigned release unexpectedly contains get-task-allow" >&2
  exit 1
fi

runtime_version="$("$app/Contents/Resources/cccode-bridge-runtime" -version)"
if [[ "$runtime_version" != *"$version"* || "$runtime_version" != *"$commit"* ]]; then
  echo "Runtime metadata mismatch: $runtime_version" >&2
  exit 1
fi

app_archs="$(lipo -archs "$app/Contents/MacOS/CCCodeBridge")"
runtime_archs="$(lipo -archs "$app/Contents/Resources/cccode-bridge-runtime")"
if [[ "$app_archs" != "$runtime_archs" ]]; then
  echo "Architecture mismatch: app=$app_archs runtime=$runtime_archs" >&2
  exit 1
fi

arch="$(tr ' ' '-' <<<"$app_archs")"
artifact="CCCodeBridge-${version}-macos-${arch}-unsigned.zip"
rm -f "dist/$artifact" "dist/$artifact.sha256"
ditto -c -k --sequesterRsrc --keepParent "$app" "dist/$artifact"
(
  cd dist
  shasum -a 256 "$artifact" > "$artifact.sha256"
)

printf 'Created %s\n' "$repo_root/dist/$artifact"
printf 'Runtime %s\n' "$runtime_version"

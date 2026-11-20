#!/usr/bin/env bash
#
# Builds an UNSIGNED device .ipa on macOS, with no Apple developer account.
#
# `mygo build -platform ios` refuses to build without a provisioning profile
# and signs what it builds. So this makes a throwaway self-signed identity and
# a stub profile that authorizes it, lets mygo build the device app with them,
# then strips the signature and the profile from the bundle and packs
# Payload/ into an .ipa.
#
# The result installs through a re-signing tool: Sideloadly or AltStore with a
# free Apple ID (7 days at a time), or a jailbroken device, or TrollStore on
# iOS 17.0 and older.
#
# Needs: Xcode, Go, jq.

set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
work="${RUNNER_TEMP:-${TMPDIR:-/tmp}}/ios-unsigned"
uuid="$(uuidgen)"
team="MYGO000000" # fake team ID, only inside the stub profile
identifier="$(jq -r .identifier "$root/mygo.json")"

echo "::group::Create a throwaway signing identity"
rm -rf "$work"
mkdir -p "$work"
cd "$work"

cat > openssl.cnf <<'EOF'
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = MyGo Unsigned Signer
O = MyGo Unsigned
[ext]
basicConstraints = critical,CA:FALSE
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
subjectKeyIdentifier = hash
EOF

# A self-signed certificate with the Code Signing extended key usage, which is
# what codesign asks of an identity.
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 -config openssl.cnf \
  -keyout key.pem -out cert.pem 2>/dev/null

pass="$(openssl rand -hex 12)"
# LibreSSL, which macOS has, writes the legacy PBE that `security import` reads;
# OpenSSL 3 needs -legacy for the same.
if ! openssl pkcs12 -export -inkey key.pem -in cert.pem -out signer.p12 \
      -passout "pass:$pass" -legacy 2>/dev/null; then
  openssl pkcs12 -export -inkey key.pem -in cert.pem -out signer.p12 \
    -passout "pass:$pass"
fi
echo "::endgroup::"

echo "::group::Import it into a keychain of this run"
keychain="$work/signing.keychain-db"
security create-keychain -p "$pass" "$keychain"
security set-keychain-settings -lut 21600 "$keychain"
security unlock-keychain -p "$pass" "$keychain"
security import signer.p12 -k "$keychain" -P "$pass" \
  -T /usr/bin/codesign -T /usr/bin/security >/dev/null
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$pass" \
  "$keychain" >/dev/null
security list-keychains -d user -s "$keychain" login.keychain
# Trust it, so that the verification mygo runs after signing passes.
sudo security add-trusted-cert -d -r trustRoot -p codeSign \
  -k /Library/Keychains/System.keychain "$work/cert.pem"
echo "::endgroup::"

echo "::group::Write a stub provisioning profile"
# app-store kind: no get-task-allow, no ProvisionedDevices. mygo picks it for
# the identifier, and its certificate is the identity above, by SHA-1.
der="$(openssl x509 -in cert.pem -outform DER | base64)"
cat > profile.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Name</key><string>MyGo Unsigned</string>
	<key>UUID</key><string>$uuid</string>
	<key>TeamIdentifier</key><array><string>$team</string></array>
	<key>ExpirationDate</key><date>2036-01-01T00:00:00Z</date>
	<key>DeveloperCertificates</key><array><data>$der</data></array>
	<key>Entitlements</key>
	<dict>
		<key>application-identifier</key><string>$team.$identifier</string>
		<key>com.apple.developer.team-identifier</key><string>$team</string>
	</dict>
</dict>
</plist>
EOF
# A real .mobileprovision is CMS-signed; `security cms -D` decodes this one.
openssl smime -sign -binary -in profile.plist -signer cert.pem -inkey key.pem \
  -outform DER -nodetach -out profile.mobileprovision
mkdir -p "$HOME/Library/MobileDevice/Provisioning Profiles"
cp profile.mobileprovision \
  "$HOME/Library/MobileDevice/Provisioning Profiles/mygo-unsigned.mobileprovision"
echo "::endgroup::"

echo "::group::Build the device app"
cd "$root"
mygo build -platform ios
ipa="$(ls build/ios-arm64/*.ipa | head -1)"
echo "built $ipa"
echo "::endgroup::"

echo "::group::Strip the signature"
unpacked="$work/ipa"
rm -rf "$unpacked"
mkdir -p "$unpacked"
ditto -x -k "$ipa" "$unpacked"
app="$(ls -d "$unpacked"/Payload/*.app)"
rm -rf "$app/_CodeSignature" "$app/embedded.mobileprovision"
exe="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleExecutable' "$app/Info.plist")"
if ! codesign --remove-signature "$app/$exe"; then
  echo "note: codesign could not remove the executable's signature" >&2
fi
while IFS= read -r framework; do
  name="$(basename "$framework" .framework)"
  codesign --remove-signature "$framework/$name" 2>/dev/null || true
  rm -rf "$framework/_CodeSignature"
done < <(find "$app" -mindepth 1 -maxdepth 3 -name "*.framework" -type d)
echo "::endgroup::"

out="$root/build/ios-arm64/$(basename "${ipa%.ipa}")-unsigned.ipa"
rm -f "$out"
(cd "$unpacked" && ditto -c -k --sequesterRsrc --keepParent Payload "$out")
rm -f "$ipa"

# A signature Apple gave out is what matters: the throwaway one is gone, and the
# ad-hoc one ld64 writes on arm64 is not a signature a phone accepts.
if otool -l "$app/$exe" | grep -q LC_CODE_SIGNATURE; then
  echo "the executable keeps the linker's ad-hoc signature, and no profile or identity: unsigned for our purposes"
else
  echo "the executable has no signature at all"
fi
ls -la "$out"

#!/usr/bin/env bash
# Makes a private CA plus one leaf certificate for the given hostnames.
# Adapted from proxy-tests/testdata/make-test-cert.sh (same session, same author):
# changed to take the host list from arguments instead of fixed demo hosts, and
# to keep an existing CA so the phone does not need a new profile each time.
#
# Usage: make-cert.sh <output-dir> <dns-name> [dns-name ...]
#   e.g. make-cert.sh certs app.homewav.com
#
# Outputs in <output-dir>:
#   ca.pem / ca.key      -> install ca.pem on the phone (AirDrop/mail it, install the
#                           profile, then enable it under Settings > General > About >
#                           Certificate Trust Settings). Keep ca.key private.
#   leaf.pem / leaf.key  -> pass to homowav as -cert / -key.
set -euo pipefail

if [[ $# -lt 2 ]]; then
	echo "usage: $0 <output-dir> <dns-name> [dns-name ...]" >&2
	exit 1
fi

OUT_DIR="$1"
shift
DNS_NAMES=("$@")

# iOS rejects TLS server certs valid for longer than 398 days, so stay under it.
LEAF_VALID_DAYS=397
CA_VALID_DAYS=3650

mkdir -p "$OUT_DIR"
cd "$OUT_DIR"
umask 077

san="subjectAltName="
for name in "${DNS_NAMES[@]}"; do
	san+="DNS:${name},"
done
san="${san%,}"

# Reuse the CA if present: re-issuing only the leaf needs no change on the phone.
if [[ ! -f ca.pem || ! -f ca.key ]]; then
	# basicConstraints CA:TRUE is required or iOS will not accept it as an issuer.
	openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
		-keyout ca.key -out ca.pem -days "$CA_VALID_DAYS" \
		-subj "/CN=homowav private CA" \
		-addext "basicConstraints=critical,CA:TRUE" \
		-addext "keyUsage=critical,keyCertSign,cRLSign"
fi

# Leaf: SAN + serverAuth EKU are both mandatory for Apple platforms.
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
	-keyout leaf.key -out leaf.csr -subj "/CN=${DNS_NAMES[0]}"
openssl x509 -req -in leaf.csr -CA ca.pem -CAkey ca.key -CAcreateserial \
	-out leaf.pem -days "$LEAF_VALID_DAYS" \
	-extfile <(printf '%s\nextendedKeyUsage=serverAuth\nbasicConstraints=CA:FALSE\n' "$san")
rm -f leaf.csr

echo "wrote leaf for ${DNS_NAMES[*]} in $(pwd); CA is ca.pem"

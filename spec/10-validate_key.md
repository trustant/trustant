# Validating an ai-proxy API key

Every API key issued by the ai-proxy is **Ed25519-signed**. Anyone holding the proxy's public verification key can decide *offline* whether a given key was issued by that proxy.

This document is the contract for client-side validators. It is implementation-language agnostic.

## Key format

```
aip_<id>.<sig>
```

| Component | Encoding              | Decoded size |
|-----------|-----------------------|--------------|
| `aip`     | literal prefix        | —            |
| `id`      | base64url, no padding | 1+ bytes (typically 16; for the test user, the original `TEST_KEY` value verbatim) |
| `sig`     | base64url, no padding | 64 bytes     |

`base64url` is RFC 4648 §5 — uses `-` and `_` instead of `+`/`/`, **no `=` padding**. To decode in libraries that require padding, append `=` characters until the length is a multiple of 4.

Separators:
- `_` after the `aip` prefix (single, fixed).
- `.` between `id` and `sig`. The dot is unambiguous because the base64url alphabet contains `_` and `-` but never `.`.

## Public key

Fetch once and cache:

```
GET <PROXY_BASE_URL>/.well-known/ai-proxy-pubkey
```

Response:

```json
{
  "alg": "Ed25519",
  "public_key": "<base64-std>",
  "format": "base64-std"
}
```

`public_key` is **standard base64** (with `+`/`/` and `=` padding, RFC 4648 §4) of the 32-byte raw Ed25519 public key.

`<PROXY_BASE_URL>` here is the proxy origin without the `/v1` suffix. If `PROXY_BASE_URL=https://ai.trustable.ai/v1`, the well-known is at `https://ai.trustable.ai/.well-known/ai-proxy-pubkey`.

## Validation algorithm

Given:
- `key`: the bearer string starting with `aip_`
- `pub_b64`: the `public_key` field from `/.well-known/ai-proxy-pubkey`

```
assert key.startswith("aip_")
rest  = key[len("aip_"):]
parts = rest.split(".")
assert len(parts) == 2

id_bytes  = base64url_decode(parts[0])   # add "=" padding to reach a multiple of 4
sig_bytes = base64url_decode(parts[1])   # idem
assert len(sig_bytes) == 64

pub_bytes = base64_std_decode(pub_b64)
assert len(pub_bytes) == 32

ed25519.verify(pub_bytes, message=id_bytes, signature=sig_bytes)
# -> raises / returns false if the key was not issued by this proxy
```

A signature that verifies successfully proves: the holder of the `SIGN_KEY` (i.e. this proxy) produced `sig` for `id_bytes`. It does **not** prove that the corresponding user is currently active; for that, use the key as a Bearer token against `GET /api/v2/credits` (the proxy returns 401 on inactive/unknown).

## Reference implementations

### Python

Requires `cryptography` (`pip install cryptography`).

```python
import base64
from cryptography.hazmat.primitives.asymmetric import ed25519
from cryptography.exceptions import InvalidSignature

def _b64url(s: str) -> bytes:
    return base64.urlsafe_b64decode(s + "=" * (-len(s) % 4))

def validate_key(key: str, pub_b64: str) -> bool:
    if not key.startswith("aip_"):
        return False
    parts = key[len("aip_"):].split(".")
    if len(parts) != 2:
        return False
    try:
        id_bytes  = _b64url(parts[0])
        sig_bytes = _b64url(parts[1])
        pub       = base64.b64decode(pub_b64)
        ed25519.Ed25519PublicKey.from_public_bytes(pub).verify(sig_bytes, id_bytes)
        return True
    except (InvalidSignature, ValueError):
        return False
```

### Node.js (built-in `crypto`, ≥ 16)

```js
const crypto = require('crypto');

function b64url(s) {
  s = s.replace(/-/g, '+').replace(/_/g, '/');
  while (s.length % 4) s += '=';
  return Buffer.from(s, 'base64');
}

function validateKey(key, pubB64) {
  if (!key.startsWith('aip_')) return false;
  const parts = key.slice('aip_'.length).split('.');
  if (parts.length !== 2) return false;
  const id  = b64url(parts[0]);
  const sig = b64url(parts[1]);
  const pub = Buffer.from(pubB64, 'base64');
  const pk  = crypto.createPublicKey({
    key: Buffer.concat([
      Buffer.from('302a300506032b6570032100', 'hex'), // SPKI header for Ed25519
      pub,
    ]),
    format: 'der',
    type: 'spki',
  });
  return crypto.verify(null, id, pk, sig);
}
```

### Go

```go
import (
    "crypto/ed25519"
    "encoding/base64"
    "strings"
)

func ValidateKey(key, pubB64 string) bool {
    rest, ok := strings.CutPrefix(key, "aip_")
    if !ok {
        return false
    }
    parts := strings.Split(rest, ".")
    if len(parts) != 2 {
        return false
    }
    id, err := base64.RawURLEncoding.DecodeString(parts[0])
    if err != nil {
        return false
    }
    sig, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil {
        return false
    }
    pub, err := base64.StdEncoding.DecodeString(pubB64)
    if err != nil || len(pub) != ed25519.PublicKeySize {
        return false
    }
    return ed25519.Verify(pub, id, sig)
}
```

## Failure modes

| Symptom | Cause |
|---|---|
| Doesn't start with `aip_` | Not an ai-proxy key (different vendor or mangled string). |
| `len(parts) != 2` after splitting on `.` | Truncated, double-encoded, or wrong format. |
| `len(sig) != 64` | Truncated key. |
| `ed25519.verify` returns false | Either the key was not issued by this proxy, or the proxy rotated `SIGN_KEY` after issuing this key. |
| Public key fetch returns a different value than cached | Proxy was rotated. Re-fetch and re-verify. Old keys signed with the previous private key will permanently fail to verify under the new public key. |

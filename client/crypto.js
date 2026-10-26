// client/crypto.js
// All cryptography happens locally. Zero third-party deps: node:crypto only.
//
// Security model:
//   - A short TOKEN is generated locally, shown UPPERCASE to the user.
//   - Tokens are normalized as upper(trim(token)); verification is case-insensitive.
//   - The encryption key is derived from the TOKEN via PBKDF2-HMAC-SHA256
//     (high iteration count, per-message random salt).
//   - AES-256-GCM provides authenticated encryption with a per-message random nonce.
//   - A separate SHA-256 hash of the normalized token is sent to the server ONLY
//     for token verification. The hash is never used as an encryption key.
import crypto from 'node:crypto';

export const ENVELOPE_VERSION = 1;
export const ALGORITHM = 'AES-256-GCM';
export const KDF = 'PBKDF2-HMAC-SHA256';
export const KDF_ITERATIONS = 200_000;
export const KEY_LENGTH = 32; // 256-bit
export const SALT_LENGTH = 16;
export const NONCE_LENGTH = 12; // 96-bit nonce recommended for GCM
export const TAG_LENGTH = 16;

// Crockford-ish alphabet: no 0/1/I/O/U to avoid ambiguity and accidental words.
const TOKEN_ALPHABET = 'ABCDEFGHJKMNPQRSTVWXYZ23456789';
export const TOKEN_LENGTH = 20; // ~100 bits of entropy

export function normalizeToken(token) {
  if (typeof token !== 'string') throw new Error('TOKEN must be a string');
  const normalized = token.trim().toUpperCase();
  if (!normalized) throw new Error('TOKEN is empty');
  return normalized;
}

export function generateToken(length = TOKEN_LENGTH) {
  const bytes = crypto.randomBytes(length);
  let out = '';
  for (let i = 0; i < length; i++) {
    out += TOKEN_ALPHABET[bytes[i] % TOKEN_ALPHABET.length];
  }
  return out; // already uppercase
}

// Server-side-verification hash. NOT a key-derivation input.
export function hashToken(token) {
  const normalized = normalizeToken(token);
  return crypto.createHash('sha256').update(normalized, 'utf8').digest('hex');
}

function deriveKey(token, salt, iterations = KDF_ITERATIONS) {
  const normalized = normalizeToken(token);
  return crypto.pbkdf2Sync(normalized, salt, iterations, KEY_LENGTH, 'sha256');
}

// Returns a self-describing, JSON-serializable envelope (then stored as a string).
export function encryptPlaintext(plaintext, token) {
  if (typeof plaintext !== 'string' || plaintext.length === 0) {
    throw new Error('plaintext must be a non-empty string');
  }
  const salt = crypto.randomBytes(SALT_LENGTH);
  const nonce = crypto.randomBytes(NONCE_LENGTH);
  const key = deriveKey(token, salt);
  const cipher = crypto.createCipheriv(ALGORITHM, key, nonce, { authTagLength: TAG_LENGTH });
  const ciphertext = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  const tag = cipher.getAuthTag();
  return {
    v: ENVELOPE_VERSION,
    alg: ALGORITHM,
    kdf: KDF,
    kdfParams: {
      iterations: KDF_ITERATIONS,
      keyLength: KEY_LENGTH,
      saltLength: SALT_LENGTH,
      nonceLength: NONCE_LENGTH,
      digest: 'sha256'
    },
    salt: salt.toString('base64'),
    nonce: nonce.toString('base64'),
    ciphertext: ciphertext.toString('base64'),
    tag: tag.toString('base64')
  };
}

export function encryptPayload(plaintext, token) {
  return JSON.stringify(encryptPlaintext(plaintext, token));
}

export function decryptPayload(payload, token) {
  let env;
  try {
    env = typeof payload === 'string' ? JSON.parse(payload) : payload;
  } catch {
    throw new Error('ciphertext payload is not valid JSON');
  }
  if (!env || env.v !== ENVELOPE_VERSION) throw new Error('unsupported envelope version');
  if (env.alg !== ALGORITHM) throw new Error(`unsupported algorithm: ${env.alg}`);
  if (env.kdf !== KDF) throw new Error(`unsupported kdf: ${env.kdf}`);

  const iterations = env.kdfParams?.iterations ?? KDF_ITERATIONS;
  const salt = Buffer.from(env.salt, 'base64');
  const nonce = Buffer.from(env.nonce, 'base64');
  const ciphertext = Buffer.from(env.ciphertext, 'base64');
  const tag = Buffer.from(env.tag, 'base64');
  if (!salt.length || !nonce.length || !ciphertext.length || !tag.length) {
    throw new Error('incomplete ciphertext envelope');
  }

  const key = deriveKey(token, salt, iterations);
  try {
    const decipher = crypto.createDecipheriv(env.alg, key, nonce, { authTagLength: tag.length });
    decipher.setAuthTag(tag);
    return Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8');
  } catch (err) {
    // Wrong token, tampered ciphertext, corrupted envelope — all indistinguishable by design.
    throw new Error(`decryption failed: ${err.message}`);
  }
}

// Constant-time comparison of hex hashes.
export function tokenHashMatches(providedHash, expectedHash) {
  if (typeof providedHash !== 'string' || typeof expectedHash !== 'string') return false;
  const a = Buffer.from(providedHash.toLowerCase(), 'hex');
  const b = Buffer.from(expectedHash.toLowerCase(), 'hex');
  if (a.length !== b.length || a.length === 0) return false;
  return crypto.timingSafeEqual(a, b);
}

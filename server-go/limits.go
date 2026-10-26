package main

// Limits and defaults shared by the HTTP layer and storage.
const (
	defaultTTLSeconds = 30 * 60      // 30 minutes
	maxTTLSeconds     = 24 * 60 * 60 // 1 day
	maxBodyBytes      = 256 * 1024   // ciphertext payload
	maxMetadataBytes  = 8 * 1024     // metadata JSON
	maxRequestBytes   = 512 * 1024   // whole request body allowance
	fileMode          = 0o600
	dirMode           = 0o700
)

// Stable error codes (API contract, mirroring the skill docs).
const (
	errInvalidJSON        = "INVALID_JSON"
	errEmptyBody          = "EMPTY_BODY"
	errBodyTooLarge       = "BODY_TOO_LARGE"
	errMetadataTooLarge   = "METADATA_TOO_LARGE"
	errInvalidTTL         = "INVALID_TTL"
	errInvalidRequest     = "INVALID_REQUEST"
	errInvalidToken       = "INVALID_TOKEN"
	errWrongToken         = "WRONG_TOKEN"
	errDecryptionFailed   = "DECRYPTION_FAILED"
	errNotFound           = "NOT_FOUND"
	errExpired            = "EXPIRED"
	errStorageUnavailable = "STORAGE_UNAVAILABLE"
	errNotFoundRoute      = "NOT_FOUND_ROUTE"
	errPayloadTooLarge    = "PAYLOAD_TOO_LARGE"
)

package main

import "testing"

func TestCryptoRoundTrip(t *testing.T) {
	cases := []string{"x", "# Goal\n\n中文与 emoji 🐱 secrets markers"}
	for _, pt := range cases {
		payload, err := encryptEnvelope(pt, "  MixedCaseToken1234567 ", 50000)
		if err != nil {
			t.Fatal(err)
		}
		got, err := decryptEnvelope(payload, "mixedcasetoken1234567")
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if got != pt {
			t.Fatalf("roundtrip mismatch: %q != %q", got, pt)
		}
		if _, err := decryptEnvelope(payload, "WRONGTOKEN00000000000000"); err == nil {
			t.Fatal("wrong token unexpectedly decrypted")
		}
	}
}

func TestHashNormalization(t *testing.T) {
	if hashTokenHex("abcdef1234567890ABCD") != hashTokenHex("  abcdef1234567890abcd ") {
		t.Fatal("token hash not normalized")
	}
	if !tokenHashMatches(hashTokenHex("X"), hashTokenHex("x")) {
		t.Fatal("case-insensitive hash match failed")
	}
	if tokenHashMatches("zz", hashTokenHex("x")) {
		t.Fatal("bogus hash matched")
	}
}

func TestTamperDetected(t *testing.T) {
	payload, err := encryptEnvelope("hello", "TOKENTOOLARGETOKEN0000000099", 50000)
	if err != nil {
		t.Fatal(err)
	}
	tampered := payload[:len(payload)-8] + `AAAAAAAA"`
	if _, err := decryptEnvelope(tampered, "TOKENTOOLARGETOKEN0000000099"); err == nil {
		t.Fatal("tampering not detected")
	}
}

package webhook

import "testing"

func TestSignProducesGitHubHeaders(t *testing.T) {
	// The vector is GitHub's documented example: the body "Hello, World!" under
	// the secret "It's a Secret to Everybody" yields this exact sha256 digest, so
	// a receiver built against GitHub's docs verifies a Githome delivery.
	sig := Sign("It's a Secret to Everybody", []byte("Hello, World!"))
	const want = "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17"
	if sig.SHA256 != want {
		t.Errorf("SHA256 = %q, want %q", sig.SHA256, want)
	}
	if sig.SHA1 == "" || sig.SHA1[:5] != "sha1=" {
		t.Errorf("SHA1 = %q, want a sha1= prefix", sig.SHA1)
	}
}

func TestSignEmptySecret(t *testing.T) {
	sig := Sign("", []byte("anything"))
	if sig.SHA256 != "" || sig.SHA1 != "" {
		t.Errorf("empty secret produced signatures: %+v", sig)
	}
}

func TestVerifyRoundTrip(t *testing.T) {
	secret := "swordfish"
	body := []byte(`{"action":"opened"}`)
	sig := Sign(secret, body)
	if !Verify(secret, sig.SHA256, body) {
		t.Error("Verify rejected a signature it produced")
	}
	if Verify(secret, sig.SHA256, []byte("tampered")) {
		t.Error("Verify accepted a signature over a different body")
	}
	if Verify("wrong", sig.SHA256, body) {
		t.Error("Verify accepted a signature under the wrong secret")
	}
	if Verify(secret, "", body) {
		t.Error("Verify accepted an empty signature")
	}
}

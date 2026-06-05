package webhook

import (
	"net/netip"
	"testing"
)

func TestCheckAddressBlocksPrivateByDefault(t *testing.T) {
	blocked := []string{
		"127.0.0.1:443",
		"10.0.0.5:80",
		"192.168.1.1:8080",
		"169.254.169.254:80", // cloud metadata, the classic SSRF target
		"[::1]:443",
		"0.0.0.0:80",
	}
	for _, addr := range blocked {
		if err := checkAddress(addr, nil); err == nil {
			t.Errorf("checkAddress(%q) allowed a private destination", addr)
		}
	}
}

func TestCheckAddressAllowsPublic(t *testing.T) {
	for _, addr := range []string{"93.184.216.34:443", "[2606:2800:220:1::1]:443"} {
		if err := checkAddress(addr, nil); err != nil {
			t.Errorf("checkAddress(%q) blocked a public destination: %v", addr, err)
		}
	}
}

func TestCheckAddressAllowlist(t *testing.T) {
	allow := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	if err := checkAddress("127.0.0.1:443", allow); err != nil {
		t.Errorf("allowlisted loopback was blocked: %v", err)
	}
	// A range outside the allowlist is still blocked.
	if err := checkAddress("10.0.0.1:80", allow); err == nil {
		t.Error("a private range outside the allowlist was allowed")
	}
}

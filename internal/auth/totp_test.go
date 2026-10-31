package auth

import (
	"strings"
	"testing"
	"time"
)

// rfcSecret 是 RFC 6238 附录 B 的 SHA1 测试密钥 "12345678901234567890"。
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	// RFC 给出 8 位验证码，6 位验证码取其末 6 位。
	cases := map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"}
	for unix, want := range cases {
		got, err := TOTPCode(rfcSecret, TOTPStep(time.Unix(unix, 0)))
		if err != nil || got != want {
			t.Fatalf("t=%d: got %q err=%v, want %q", unix, got, err, want)
		}
	}
}

func TestVerifyTOTPWindowAndReplay(t *testing.T) {
	now := time.Unix(1234567890, 0)
	step := TOTPStep(now)
	previous, _ := TOTPCode(rfcSecret, step-1)
	if got, ok := VerifyTOTP(rfcSecret, previous, now, 0); !ok || got != step-1 {
		t.Fatalf("code from the previous step should be accepted, got step=%d ok=%v", got, ok)
	}
	if _, ok := VerifyTOTP(rfcSecret, previous, now, step-1); ok {
		t.Fatal("a code whose step was already used must be rejected")
	}
	stale, _ := TOTPCode(rfcSecret, step-2)
	if _, ok := VerifyTOTP(rfcSecret, stale, now, 0); ok {
		t.Fatal("a code two steps old must be rejected")
	}
	if _, ok := VerifyTOTP(rfcSecret, "12345", now, 0); ok {
		t.Fatal("a short code must be rejected")
	}
}

func TestNewTOTPSecretAndURI(t *testing.T) {
	secret := NewTOTPSecret()
	if len(secret) != 32 || secret == NewTOTPSecret() {
		t.Fatalf("unexpected secret %q", secret)
	}
	uri := TOTPURI("proxystack-web", "admin", secret)
	if !strings.HasPrefix(uri, "otpauth://totp/proxystack-web:admin?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatalf("unexpected uri %q", uri)
	}
}

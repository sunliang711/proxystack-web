package auth

import "testing"

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := VerifyPassword("correct horse battery", hash)
	if err != nil || !ok {
		t.Fatalf("expected match, got ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong", hash)
	if err != nil || ok {
		t.Fatalf("expected mismatch, got ok=%v err=%v", ok, err)
	}
	if _, err := VerifyPassword("x", "$bcrypt$foo"); err == nil {
		t.Fatal("expected error for unsupported hash")
	}
}

func TestRandomString(t *testing.T) {
	a, b := RandomString(24), RandomString(24)
	if len(a) != 24 || a == b {
		t.Fatalf("unexpected random strings %q %q", a, b)
	}
}

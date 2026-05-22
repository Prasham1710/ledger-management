package auth_test

import (
	"testing"
	"time"

	"github.com/prasham/ledger/services/gateway/internal/auth"
)

func TestSignAndVerify(t *testing.T) {
	mgr := auth.NewManager("test-secret", time.Hour)

	token, err := mgr.Sign("alice", "admin")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := mgr.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.Subject != "alice" {
		t.Errorf("subject: got %q, want %q", claims.Subject, "alice")
	}
	if claims.Role != "admin" {
		t.Errorf("role: got %q, want %q", claims.Role, "admin")
	}
}

func TestVerify_InvalidSignature(t *testing.T) {
	mgr := auth.NewManager("secret-a", time.Hour)
	token, _ := mgr.Sign("bob", "user")

	other := auth.NewManager("secret-b", time.Hour)
	if _, err := other.Verify(token); err == nil {
		t.Error("expected error for wrong secret, got nil")
	}
}

func TestVerify_Expired(t *testing.T) {
	mgr := auth.NewManager("test-secret", -time.Second) // already expired
	token, err := mgr.Sign("carol", "admin")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := mgr.Verify(token); err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

func TestVerify_Tampered(t *testing.T) {
	mgr := auth.NewManager("test-secret", time.Hour)
	token, _ := mgr.Sign("dave", "admin")
	tampered := token[:len(token)-4] + "XXXX"
	if _, err := mgr.Verify(tampered); err == nil {
		t.Error("expected error for tampered token, got nil")
	}
}

func TestVerify_EmptyToken(t *testing.T) {
	mgr := auth.NewManager("test-secret", time.Hour)
	if _, err := mgr.Verify(""); err == nil {
		t.Error("expected error for empty token, got nil")
	}
}

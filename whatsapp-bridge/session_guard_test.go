package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionGuardRequiresExactConsentWithoutReadingOrChangingStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	data := []byte("synthetic session sentinel, not credentials")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "false", "TRUE", "1", " true", "true "} {
		if err := checkSessionOpenConsent(path, value); err != errSessionReview {
			t.Fatalf("consent %q accepted", value)
		}
	}
	if err := checkSessionOpenConsent(path, "true"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("session sentinel changed")
	}
}

func TestSessionGuardMissingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whatsapp.db")
	if err := checkSessionOpenConsent(path, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("session file created")
	}
}

func TestSessionGuardRejectsSymlinkEvenWithConsent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "synthetic.db")
	if err := os.WriteFile(target, []byte("synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "whatsapp.db")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unavailable in this test environment")
	}
	if err := checkSessionOpenConsent(link, "true"); err != errSessionReview {
		t.Fatal("symlink accepted")
	}
}

package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestCacheGuardMissingDoesNotCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	if err := checkExistingMessageCache(context.Background(), path); err != nil {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("guard created a database")
	}
}

func TestCacheGuardValidIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE fixture (id INTEGER PRIMARY KEY); INSERT INTO fixture VALUES (1)"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkExistingMessageCache(context.Background(), path); err != nil {
		t.Fatalf("got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(before) != sha256.Sum256(after) {
		t.Fatal("guard changed valid history")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatal("guard left unexpected sidecars")
	}
}

func TestCacheGuardMalformedPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	data := []byte("synthetic invalid SQLite fixture")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkExistingMessageCache(context.Background(), path); err != errCacheIntegrity {
		t.Fatalf("got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("guard changed malformed history")
	}
}

func TestCacheGuardCorruptIndexPreserved(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE fixture (id INTEGER PRIMARY KEY, value TEXT); CREATE INDEX fixture_idx ON fixture(value); INSERT INTO fixture VALUES (1, 'synthetic')"); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var rootPage, pageSize int
	if err := db.QueryRow("SELECT rootpage FROM sqlite_master WHERE name = 'fixture_idx'").Scan(&rootPage); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	position := (rootPage - 1) * pageSize
	if position < 100 || position >= len(data) {
		t.Fatal("fixture index position invalid")
	}
	data[position] = 0 // Corrupt only this temporary fixture's index page type.
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkExistingMessageCache(context.Background(), path); err != errCacheIntegrity {
		t.Fatalf("got %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(data) {
		t.Fatal("guard altered corrupt fixture")
	}
}

func TestCacheGuardSidecarFailsClosed(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "messages.db")
			if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+suffix, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := checkExistingMessageCache(context.Background(), path); err != errCacheIntegrity {
				t.Fatalf("got %v", err)
			}
		})
	}
}

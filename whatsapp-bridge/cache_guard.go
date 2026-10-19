package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

var errCacheIntegrity = errors.New("CACHE_INTEGRITY_FAILED")

// Existing history is only inspected through a read-only SQLite connection.
// No recovery, REINDEX, VACUUM, deletion or credential-store access is allowed.
func checkExistingMessageCache(parent context.Context, path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errCacheIntegrity
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(path + suffix); !os.IsNotExist(err) {
			return errCacheIntegrity
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return errCacheIntegrity
	}
	var header [100]byte
	n, readErr := io.ReadFull(file, header[:])
	file.Close()
	if readErr != nil || n != len(header) || string(header[:16]) != "SQLite format 3\x00" || header[18] != 1 || header[19] != 1 {
		return errCacheIntegrity
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return errCacheIntegrity
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	query := url.Values{"mode": {"ro"}, "_query_only": {"on"}, "_busy_timeout": {"1000"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite3", uri.String())
	if err != nil {
		return errCacheIntegrity
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check(1)").Scan(&result); err != nil || result != "ok" {
		return errCacheIntegrity
	}
	return nil
}

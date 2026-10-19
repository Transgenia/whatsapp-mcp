package main

import (
	"errors"
	"os"
)

var errSessionReview = errors.New("SESSION_STORE_REVIEW_REQUIRED")

// sqlstore.New automatically upgrades existing stores. Require an explicit
// operator decision before that call, without inspecting authentication data.
func checkSessionOpenConsent(path, consent string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || consent != "true" {
		return errSessionReview
	}
	return nil
}

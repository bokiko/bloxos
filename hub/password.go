package main

import "errors"

const minPasswordLength = 8

var errPasswordTooShort = errors.New("password must be at least 8 characters")

// validatePassword enforces the single source of truth for password policy
// across first-boot setup, admin user create/update, and the self-service
// change-password endpoint. Keep all sites routed through this helper so the
// policy can never silently diverge again.
func validatePassword(s string) error {
	if len(s) < minPasswordLength {
		return errPasswordTooShort
	}
	return nil
}

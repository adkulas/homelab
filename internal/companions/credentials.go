package companions

import (
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var houndarrUsername = regexp.MustCompile(`(?i)^[a-z0-9_.-]{3,32}$`)

// ValidateAdminCredentials checks the pinned account interfaces without ever
// including supplied values in an error. Houndarr's bcrypt limit is in bytes.
func ValidateAdminCredentials(service string, credentials AdminCredentials) error {
	if credentials.Username == "" || credentials.Password == "" {
		return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", service+" requires both an administrator username and password.")
	}
	if service == "Houndarr" {
		if !houndarrUsername.MatchString(credentials.Username) {
			return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Houndarr username must be 3–32 letters, digits, dots, underscores or dashes.")
		}
		if utf8.RuneCountInString(credentials.Password) < 8 || len(credentials.Password) > 72 {
			return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Houndarr password must contain at least 8 characters and at most 72 UTF-8 bytes.")
		}
	} else {
		size := len(utf16.Encode([]rune(credentials.Password)))
		nameSize := len(utf16.Encode([]rune(credentials.Username)))
		if strings.TrimSpace(credentials.Username) != credentials.Username || nameSize < 3 || nameSize > 50 {
			return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Cleanuparr username must contain 3–50 characters without leading or trailing whitespace.")
		}
		if size < 8 || size > 128 {
			return fail("VERIFY_COMPANION_CREDENTIALS_INVALID", "Cleanuparr password must contain 8–128 characters.")
		}
	}
	return nil
}

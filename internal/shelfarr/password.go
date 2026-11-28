package shelfarr

import (
	"strings"
	"unicode/utf8"
)

const PasswordRequirement = "Shelfarr requires at least 12 characters and at most 72 bytes, including uppercase and lowercase letters and a digit."
const PasswordRemedy = "Set a compatible household password in Jellyfin and any initialized Audiobookshelf account used by apply. Update jellyfin.password in the selected Environment SOPS document before first applying Shelfarr."

// AcceptsInitialPassword follows the pinned release's Rails user validation.
func AcceptsInitialPassword(password string) bool {
	return utf8.RuneCountInString(password) >= 12 && len(password) <= 72 &&
		!strings.ContainsRune(password, '\n') &&
		strings.ContainsAny(password, "abcdefghijklmnopqrstuvwxyz") &&
		strings.ContainsAny(password, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
		strings.ContainsAny(password, "0123456789")
}

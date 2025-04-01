package util

import (
	"net/http"
	"strings"
)

var (
	// Regex to validate the path
	//validPathRegex = regexp.MustCompile(`^[a-zA-Z0-9\-\_\/\.\?\=\&\%\+\@\(\)]*$`)
	// Allowed HTTP methods
	validMethods = map[string]bool{
		http.MethodGet:     true,
		http.MethodPost:    true,
		http.MethodPut:     true,
		http.MethodDelete:  true,
		http.MethodPatch:   true,
		http.MethodHead:    true,
		http.MethodOptions: true,
	}
)

// ValidatePath checks if the path is secure
func ValidatePath(path string) bool {
	// Fast check for empty path
	if len(path) == 0 {
		return false
	}

	// Check for null bytes which can be used for injection attacks
	if strings.Contains(path, "\x00") {
		return false
	}

	// Normalize path for consistent checking
	normalized := path

	// Detect directory traversal attempts
	// Look for patterns like "../" or "/.." that could be used to access parent directories
	if strings.Contains(normalized, "../") || strings.Contains(normalized, "/..") ||
		strings.HasPrefix(normalized, "..") || normalized == ".." {
		return false
	}

	// Check for control characters that shouldn't be in URLs
	for i := 0; i < len(normalized); i++ {
		// Control characters below space (32) except for tab (9), LF (10), and CR (13)
		if normalized[i] < 32 && normalized[i] != 9 && normalized[i] != 10 && normalized[i] != 13 {
			return false
		}
	}

	// Allow absolute paths starting with / and relative paths (common for APIs)
	// This is more permissive than requiring paths to start with /
	return true
}

// ValidateMethod checks if the HTTP method is allowed
func ValidateMethod(method string) bool {
	return validMethods[strings.ToUpper(method)]
}

// ValidateHost checks if the host is valid
func ValidateHost(host string) bool {
	// Prevents header injection and ensures the host is not empty
	return host != "" && !strings.ContainsAny(host, "\r\n")
}

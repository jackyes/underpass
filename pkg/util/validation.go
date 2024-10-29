package util

import (
	"net/http"
	"regexp"
	"strings"
)

var (
	// Regex to validate the path
	validPathRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-\_\/\.\?\=\&\%\+]*$`)
	// Regex to validate the host
	validHostRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-\.]*[a-zA-Z0-9])?$`)

	// Metodi HTTP consentiti
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
	// Prevents path traversal and ensures the path starts with a slash
	if strings.Contains(path, "..") || !strings.HasPrefix(path, "/") {
		return false
	}

	// Additional security checks
	if strings.Contains(path, "//") || // Prevents double slash
		strings.Contains(path, "./") || // Prevents current directory
		strings.Contains(path, "%00") { // Prevents null byte injection
		return false
	}

	// Checks that it contains only allowed characters
	return validPathRegex.MatchString(path)
}

// ValidateMethod checks if the HTTP method is allowed
func ValidateMethod(method string) bool {
	return validMethods[strings.ToUpper(method)]
}

// ValidateHost checks if the host is valid
func ValidateHost(host string) bool {
	// Prevents header injection
	if strings.ContainsAny(host, "\r\n\t ") {
		return false
	}

	// Removes the port if present
	if i := strings.Index(host, ":"); i != -1 {
		host = host[:i]
	}

	// Checks hostname format
	return validHostRegex.MatchString(host)
}

package util

import (
	"net/http"
	"strings"
)

var (
	// Regex to validate the path
	//validPathRegex = regexp.MustCompile(`^[a-zA-Z0-9\-\_\/\.\?\=\&\%\+\@\(\)]*$`)
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
	// Checks that it contains only allowed characters
	//return validPathRegex.MatchString(path)
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

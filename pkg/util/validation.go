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

// ValidatePath checks if the path is secure (optimized version)
func ValidatePath(path string) bool {
	// Fast check for empty path
	if len(path) == 0 {
		return false
	}
	
	// Check first character directly
	if path[0] != '/' {
		return false
	}
	
	// Check for parent directory traversal using byte loop
	if len(path) >= 2 {
		for i := 0; i < len(path)-1; i++ {
			if path[i] == '.' && path[i+1] == '.' {
				return false
			}
		}
	}
	
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

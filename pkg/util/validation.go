package util

import (
	"net/http"
	"regexp"
	"strings"
)

var (
	// Regex per validare il path
	validPathRegex = regexp.MustCompile(`^[a-zA-Z0-9\-\_\/\.\?\=\&\%\+]*$`)
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

// ValidatePath verifica che il path sia sicuro
func ValidatePath(path string) bool {
	// Previene path traversal
	if strings.Contains(path, "..") {
		return false
	}
	// Verifica che contenga solo caratteri consentiti
	return validPathRegex.MatchString(path)
}

// ValidateMethod verifica che il metodo HTTP sia consentito
func ValidateMethod(method string) bool {
	return validMethods[strings.ToUpper(method)]
}

// ValidateHost verifica che l'host sia valido
func ValidateHost(host string) bool {
	// Previene header injection
	return !strings.ContainsAny(host, "\r\n")
}

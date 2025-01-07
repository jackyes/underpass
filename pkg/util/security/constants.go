package security

import (
	"crypto/tls"
	"time"
)

const (
	// Message size limits for WebSocket
	MaxMessageSize = 1024 * 1024        // 1MB per chunk
	MinMessageSize = 1                  // 1 byte
	MaxFileSize    = 1024 * 1024 * 1024 // 1GB total file size limit

	// Timeouts
	HandshakeTimeout = 45 * time.Second
	ReadTimeout      = 30 * time.Second
	WriteTimeout     = 30 * time.Second

	// Attempt limits
	MaxReconnectAttempts = 5
	MaxRequestRetries    = 3

	// Buffer sizes
	ReadBufferSize  = 1024
	WriteBufferSize = 1024
)

// ConfigureTLS configures basic TLS options
func ConfigureTLS(config *tls.Config) {
	config.MinVersion = tls.VersionTLS12
	config.CipherSuites = []uint16{
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
	}
}

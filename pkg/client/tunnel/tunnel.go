package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/gorilla/websocket"
	"github.com/jackyes/underpass/pkg/models"
	"github.com/jackyes/underpass/pkg/util"
	"github.com/vmihailenco/msgpack/v5"
)

type Tunnel struct {
	Subdomain string
	Address   string
	URL       string
	AuthToken string

	closeChan       chan error
	closeOnce       sync.Once
	closeMutex      sync.Mutex
	isChanClosed    bool
	closeNotify     chan struct{} // Channel to signal closure completion
	reconnectActive bool          // Flag to indicate if reconnection is in progress

	activeRequests    sync.Map // map[int]*io.PipeWriter
	requestTimeouts   sync.Map // map[int]*time.Timer
	cleanupInterval   time.Duration
	requestTimeout    time.Duration
	reconnectAttempts int
	maxRetries        int
	reconnectDelay    time.Duration
}

// Connect establishes a tunnel connection with optional authentication
func Connect(url, address, subdomain, authToken string) (*Tunnel, error) {
	const defaultCleanupInterval = 5 * time.Minute
	const defaultRequestTimeout = 600 * time.Second
	const defaultMaxRetries = 5
	const defaultReconnectDelay = 5 * time.Second
	header := http.Header{}
	if authToken != "" {
		header.Add("Authorization", "Bearer "+authToken)
		fmt.Printf("Connected to subdomain: %s\n", address)
	}

	// Clean and normalize the URL
	cleanURL := url
	if strings.HasPrefix(cleanURL, "ws://") {
		cleanURL = strings.TrimPrefix(cleanURL, "ws://")
	} else if strings.HasPrefix(cleanURL, "wss://") {
		cleanURL = strings.TrimPrefix(cleanURL, "wss://")
	}
	cleanURL = strings.Split(cleanURL, "/")[0]

	// Determine if we should use wss:// based on the input
	scheme := "ws"
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "wss://") {
		scheme = "wss"
	}

	// Ensure subdomain and address are clean
	cleanSubdomain := strings.Split(subdomain, "/")[0]
	cleanAddress := strings.TrimSpace(address)

	// Construct the complete URL with the subdomain and address parameters
	fullURL := fmt.Sprintf("%s://%s/start?subdomain=%s&address=%s", scheme, cleanURL, cleanSubdomain, cleanAddress)
	fmt.Printf("Attempting to connect to: %s\n", fullURL)

	// Create a custom dialer with debug logging
	dialer := websocket.Dialer{
		HandshakeTimeout: 45 * time.Second,
	}

	fmt.Printf("Headers: %v\n", header)
	c, resp, err := dialer.Dial(fullURL, header)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		if resp != nil {
			return nil, fmt.Errorf("websocket handshake failed - Status: %d, Error: %v, URL: %s", resp.StatusCode, err, fullURL)
		}
		return nil, fmt.Errorf("websocket connection failed - Error: %v, URL: %s", err, fullURL)
	}

	subdomainChan := make(chan string)
	closeChan := make(chan error, 1)   // Buffered channel to prevent blocking
	closeNotify := make(chan struct{}) // Channel to signal closure

	t := &Tunnel{
		closeChan:       closeChan,
		closeNotify:     closeNotify,
		activeRequests:  sync.Map{},
		requestTimeouts: sync.Map{},
		cleanupInterval: defaultCleanupInterval,
		requestTimeout:  defaultRequestTimeout,
		maxRetries:      defaultMaxRetries,
		reconnectDelay:  defaultReconnectDelay,
		Address:         cleanAddress, // Store the clean address
	}

	go t.periodicCleanup()

	writeMutex := sync.Mutex{}

	go func() {
	X:
		for {
			var msg models.ServerMessage

			_, m, err := c.ReadMessage()
			if err != nil {
				select {
				case closeChan <- err:
				default:
					// Channel is already closed or full
				}
				break
			}
			err = msgpack.Unmarshal(m, &msg)
			if err != nil {
				fmt.Println(err)
			}

			switch msg.Type {
			case "subdomain":
				subdomainChan <- msg.Subdomain
				close(subdomainChan)
			case "request":
				fmt.Printf("\nReceived request: %s %s\n", msg.Request.Method, msg.Request.Path)
				read, write := io.Pipe()
				ctx, cancel := context.WithTimeout(context.Background(), t.requestTimeout)
				// Ensure the address has a protocol
				if !strings.HasPrefix(address, "http://") && !strings.HasPrefix(address, "https://") {
					address = "http://" + address
				}
				// Create the request with the original path
				// Ensure proper URL construction
				targetURL := address
				if !strings.HasSuffix(targetURL, "/") && !strings.HasPrefix(msg.Request.Path, "/") {
					targetURL += "/"
				}
				targetURL += msg.Request.Path

				request, err := http.NewRequestWithContext(ctx, msg.Request.Method, targetURL, read)
				if err != nil {
					fmt.Printf("Error creating request: %v\n", err)
					writeMutex.Lock()
					util.WriteMsgPack(c, models.ClientMessage{
						Type:      "proxy_error",
						RequestID: msg.RequestID,
					})
					writeMutex.Unlock()
					continue
				}

				// Store using sync.Map with optimized allocation
				t.activeRequests.Store(msg.RequestID, write)
				timer := time.AfterFunc(t.requestTimeout, func() {
					t.cleanupRequest(msg.RequestID)
				})
				t.requestTimeouts.Store(msg.RequestID, timer)

				request.Header = msg.Request.Headers
				request.Host = msg.Request.Host

				// Perform the request in a goroutine
				go func(request *http.Request, reqID int, cancel context.CancelFunc) {
					defer cancel()

					// Configure HTTP client with proper timeouts
					client := http.Client{
						CheckRedirect: func(req *http.Request, via []*http.Request) error {
							return http.ErrUseLastResponse
						},
						Timeout: t.requestTimeout,
						Transport: &http.Transport{
							DisableKeepAlives: false,
							IdleConnTimeout:   90 * time.Second,
							MaxIdleConns:      100,
							MaxConnsPerHost:   10,
							ForceAttemptHTTP2: true,
							DialContext: (&net.Dialer{
								Timeout:   30 * time.Second,
								KeepAlive: 30 * time.Second,
							}).DialContext,
						},
					}

					resp, err := client.Do(request)
					if err != nil {
						color.New(color.FgHiBlack).Printf("%d --> ", msg.RequestID)
						fmt.Printf("%s %s", msg.Request.Method, msg.Request.Path)
						color.New(color.FgHiBlack).Print(" --> ")
						color.New(color.FgRed).Printf("Proxy error: %s\n", err)

						writeMutex.Lock()
						err = util.WriteMsgPack(c, models.ClientMessage{Type: "proxy_error", RequestID: reqID})
						if err != nil {
							fmt.Println(err)
						}
						writeMutex.Unlock()
						return
					}

					color.New(color.FgHiBlack).Printf("%d --> ", msg.RequestID)
					fmt.Printf("%s %s", msg.Request.Method, msg.Request.Path)
					color.New(color.FgHiBlack).Print(" --> ")
					fmt.Printf("%s\n", resp.Status)

					writeMutex.Lock()
					err = util.WriteMsgPack(c, models.ClientMessage{
						Type:      "response",
						RequestID: reqID,
						Response:  util.MarshalResponse(resp),
					})
					if err != nil {
						fmt.Println(err)
					}
					writeMutex.Unlock()

					// Read the body with proper error handling
					defer resp.Body.Close() // Ensure body is always closed

					var readErr error
					buffer := make([]byte, 64*1024) // 64KB buffer size for optimal reads

					for readErr == nil {
						var n int
						n, readErr = resp.Body.Read(buffer)

						// If we read any data, send it even if there was an error
						if n > 0 {
							// Make a copy of the data to avoid race conditions
							dataCopy := make([]byte, n)
							copy(dataCopy, buffer[:n])

							writeMutex.Lock()
							sendErr := util.WriteMsgPack(c, models.ClientMessage{
								Type:      "data",
								RequestID: reqID,
								Data:      dataCopy,
							})
							writeMutex.Unlock()

							if sendErr != nil {
								fmt.Printf("Error sending data for request %d: %v\n", reqID, sendErr)
								// If we can't send, exit the loop, but still try to send close message
								break
							}
						}

						// Break on EOF (normal end) or continue on any other error
						if readErr == io.EOF {
							readErr = nil
							break
						}
					}

					// Send close message, regardless of how we exited the loop
					writeMutex.Lock()
					closeErr := util.WriteMsgPack(c, models.ClientMessage{Type: "close", RequestID: reqID})
					if closeErr != nil {
						fmt.Printf("Error sending close message for request %d: %v\n", reqID, closeErr)
					}
					writeMutex.Unlock()
				}(request, msg.RequestID, cancel)
			case "close":
				fmt.Printf("Closing request ID: %d\n", msg.RequestID)
				if v, ok := t.activeRequests.LoadAndDelete(msg.RequestID); ok {
					if pw, ok := v.(*io.PipeWriter); ok {
						pw.Close()
					}
				}
			case "data":
				if v, ok := t.activeRequests.Load(msg.RequestID); ok {
					if pw, ok := v.(*io.PipeWriter); ok {
						pw.Write(msg.Data)
					}
				}
			case "error":
				fmt.Printf("Error received: %s\n", msg.Error)
				c.Close()
				closeChan <- errors.New(msg.Error)
				break X
			}
		}
	}()

	select {
	case subdomain := <-subdomainChan:
		t.Subdomain = subdomain
		t.URL = url
		t.AuthToken = authToken
		t.maxRetries = defaultMaxRetries
		t.reconnectDelay = defaultReconnectDelay

		go t.handleReconnection()

		return t, nil
	case err = <-closeChan:
		return nil, err
	}
}

func (t *Tunnel) Wait() error {
	t.closeMutex.Lock()
	defer t.closeMutex.Unlock()

	select {
	case err, ok := <-t.closeChan:
		if !ok {
			// Channel already closed
			return nil
		}

		// Close safely
		t.closeOnce.Do(func() {
			close(t.closeChan)
			t.isChanClosed = true
			if t.closeNotify != nil {
				close(t.closeNotify)
			}
		})
		return err
	case <-t.closeNotify:
		return nil
	}
}

// safeClose safely closes the tunnel's channels to avoid race conditions
func (t *Tunnel) safeClose(err error) {
	t.closeOnce.Do(func() {
		t.closeMutex.Lock()
		defer t.closeMutex.Unlock()

		if !t.isChanClosed {
			// Try to send the error, but don't block if channel is full
			select {
			case t.closeChan <- err:
			default:
				// Channel is already full or closed
			}

			close(t.closeChan)
			t.isChanClosed = true
			close(t.closeNotify)
		}
	})
}

func (t *Tunnel) handleReconnection() {
	for {
		// Wait for an error signal from the closeChan
		err, ok := <-t.closeChan
		if !ok {
			// Channel was closed externally, exit gracefully
			return
		}
		if err == nil {
			return
		}

		// We're now disconnected
		fmt.Printf("\n❌ Disconnected from server: %s\n", err)

		// Use atomic operation to mark reconnection as active
		t.reconnectActive = true

		// Attempt reconnection
		var lastError error
		var reconnected bool

		for attempt := 1; attempt <= t.maxRetries; attempt++ {
			fmt.Printf("Reconnection attempt %d/%d...\n", attempt, t.maxRetries)

			newTunnel, err := Connect(t.URL, t.Address, t.Subdomain, t.AuthToken)
			if err == nil {
				fmt.Printf("✅ Reconnection successful!\n")

				// Lock for updating shared state
				t.closeMutex.Lock()
				// Copy all necessary fields from the new tunnel
				t.closeChan = newTunnel.closeChan
				t.activeRequests = newTunnel.activeRequests
				t.requestTimeouts = newTunnel.requestTimeouts
				t.Address = newTunnel.Address
				reconnected = true
				t.closeMutex.Unlock()

				break
			}

			// Log detailed error information
			lastError = err
			fmt.Printf("❌ Reconnection attempt %d failed: %v\n", attempt, err)

			// Log specific error types
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				fmt.Println("Server closed connection normally")
			} else if websocket.IsUnexpectedCloseError(err) {
				fmt.Println("Unexpected connection close")
			} else if errors.Is(err, context.DeadlineExceeded) {
				fmt.Println("Connection timeout")
			}

			if attempt < t.maxRetries {
				// Exponential backoff with jitter and max cap
				baseDelay := float64(t.reconnectDelay) * math.Pow(2, float64(attempt-1))
				jitter := rand.Float64() * baseDelay * 0.2
				maxDelay := 30 * time.Second
				delay := time.Duration(math.Min(baseDelay+jitter, float64(maxDelay)))
				fmt.Printf("Waiting %s before next attempt...\n", delay)
				time.Sleep(delay)
			}
		}

		// Clear reconnection flag
		t.reconnectActive = false

		if !reconnected {
			fmt.Printf("❌ Unable to reconnect after %d attempts. Last error: %v\n", t.maxRetries, lastError)
			fmt.Println("The tunnel will be closed. Please check your network connection and try again later.")

			// Close the tunnel permanently if reconnection failed
			t.safeClose(lastError)
			return
		}
	}
}

func (t *Tunnel) cleanupRequest(requestID int) {
	// Check if tunnel is closed first
	select {
	case <-t.closeNotify:
		return
	default:
	}

	// Close writer if exists
	if writer, ok := t.activeRequests.LoadAndDelete(requestID); ok {
		if pw, ok := writer.(*io.PipeWriter); ok {
			pw.Close()
		}
	}

	// Stop and remove timer
	if timer, ok := t.requestTimeouts.LoadAndDelete(requestID); ok {
		if t, ok := timer.(*time.Timer); ok {
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
	}
}

func (t *Tunnel) periodicCleanup() {
	ticker := time.NewTicker(t.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.closeChan:
			return
		case <-ticker.C:
			// Iterate through all active requests using sync.Map's Range
			t.activeRequests.Range(func(key, value interface{}) bool {
				if requestID, ok := key.(int); ok {
					t.cleanupRequest(requestID)
				}
				return true
			})
		}
	}
}

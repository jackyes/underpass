package tunnel

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"context"

	"github.com/jackyes/underpass/pkg/models"
	"github.com/jackyes/underpass/pkg/util"
	"github.com/fatih/color"
	"github.com/gorilla/websocket"
	"github.com/vmihailenco/msgpack/v5"
)

type Tunnel struct {
	Subdomain string
	Address   string

	closeChan chan error

	activeRequests   map[int]*io.PipeWriter
	requestsMutex    sync.RWMutex
	requestTimeouts  map[int]*time.Timer
	cleanupInterval  time.Duration
	requestTimeout   time.Duration
}

// Connect establishes a tunnel connection with optional authentication
func Connect(url, address, subdomain, authToken string) (*Tunnel, error) {
	const defaultCleanupInterval = 5 * time.Minute
	const defaultRequestTimeout = 30 * time.Second
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
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("websocket handshake failed - Status: %d, Error: %v, URL: %s", resp.StatusCode, err, url)
		}
		return nil, fmt.Errorf("websocket connection failed - Error: %v, URL: %s", err, url)
	}

	subdomainChan := make(chan string)
	closeChan := make(chan error)

	t := &Tunnel{
		closeChan:       closeChan,
		activeRequests:  make(map[int]*io.PipeWriter),
		requestsMutex:   sync.RWMutex{},
		requestTimeouts: make(map[int]*time.Timer),
		cleanupInterval: defaultCleanupInterval,
		requestTimeout:  defaultRequestTimeout,
	}

	// Avvia il goroutine di pulizia periodica
	go t.periodicCleanup()

	writeMutex := sync.Mutex{}

	go func() {
	X:
		for {
			var msg models.ServerMessage

			_, m, err := c.ReadMessage()
			if err != nil {
				closeChan <- err
				close(closeChan)
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
				
				t.requestsMutex.Lock()
				t.activeRequests[msg.RequestID] = write
				timer := time.AfterFunc(t.requestTimeout, func() {
					t.cleanupRequest(msg.RequestID)
				})
				t.requestTimeouts[msg.RequestID] = timer
				t.requestsMutex.Unlock()

				request.Header = msg.Request.Headers
				request.Host = msg.Request.Host

				// Perform the request in a goroutine
				go func(request *http.Request, reqID int, cancel context.CancelFunc) {
					defer cancel()

					client := http.Client{
						CheckRedirect: func(req *http.Request, via []*http.Request) error {
							return http.ErrUseLastResponse
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

					// Read the body
					for {
						// Read up to 1 MB
						d := make([]byte, 1000000)
						n, err := resp.Body.Read(d)

						if n > 0 {
							writeMutex.Lock()
							util.WriteMsgPack(c, models.ClientMessage{
								Type:      "data",
								RequestID: reqID,
								Data:      d[0:n],
							})
							writeMutex.Unlock()
						}

						if err != nil {
							writeMutex.Lock()
							err = util.WriteMsgPack(c, models.ClientMessage{Type: "close", RequestID: reqID})
							if err != nil {
								fmt.Println(err)
							}
							writeMutex.Unlock()
							break
						}
					}
				}(request, msg.RequestID, cancel)
			case "close":
				fmt.Printf("Closing request ID: %d\n", msg.RequestID)
				if v, ok := t.activeRequests[msg.RequestID]; ok {
					v.Close()
					delete(t.activeRequests, msg.RequestID)
				}
			case "data":
				if v, ok := t.activeRequests[msg.RequestID]; ok {
					v.Write(msg.Data)
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
		return t, nil
	case err = <-closeChan:
		return nil, err
	}
}

func (t *Tunnel) Wait() error {
	return <-t.closeChan
}

// cleanupRequest rimuove una richiesta specifica e le sue risorse associate
func (t *Tunnel) cleanupRequest(requestID int) {
	t.requestsMutex.Lock()
	defer t.requestsMutex.Unlock()

	if writer, exists := t.activeRequests[requestID]; exists {
		writer.Close()
		delete(t.activeRequests, requestID)
	}
	
	if timer, exists := t.requestTimeouts[requestID]; exists {
		timer.Stop()
		delete(t.requestTimeouts, requestID)
	}
}

// periodicCleanup esegue una pulizia periodica delle richieste scadute
func (t *Tunnel) periodicCleanup() {
	ticker := time.NewTicker(t.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-t.closeChan:
			return
		case <-ticker.C:
			t.requestsMutex.Lock()
			for requestID := range t.activeRequests {
				// Forza la pulizia delle richieste vecchie
				t.cleanupRequest(requestID)
			}
			t.requestsMutex.Unlock()
		}
	}
}

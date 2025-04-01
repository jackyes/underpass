package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/jackyes/underpass/pkg/models"
	"github.com/jackyes/underpass/pkg/util"
)

// RequestData represents the possible types of data in a request
type RequestData struct {
	HTTPRequest *http.Request
	RawData     []byte
}

type Request struct {
	RequestID int
	Close     bool
	Data      *RequestData
}

type Tunnel struct {
	reqChan chan Request

	listeners      map[int]chan models.ClientMessage
	listenersMutex sync.RWMutex
}

var tunnels = make(map[string]*Tunnel)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all connections - this is internal
		return true
	},
}

// Configuration constants
const (
	// Timeouts
	websocketPingInterval = 30 * time.Second
	websocketTimeout      = 60 * time.Second
	requestTimeout        = 30 * time.Second

	// Channel buffer sizes
	requestChannelBuffer = 100
	messageChannelBuffer = 10
)

func main() {
	host := flag.String("host", "", "Host address")
	CertCrt := flag.String("CertCrt", "", "Path to CertCrt")
	CertKey := flag.String("CertKey", "", "Path to CertKey")
	port := flag.String("port", "80", "Local server port")
	TLS := flag.Bool("TLS", false, "Enable TLS (need -CertCrt <path> and -CertKey <path>")
	authToken := flag.String("token", "", "Authentication token for clients")
	flag.Parse()

	r := mux.NewRouter()

	r.Handle("/", http.RedirectHandler("https://github.com/jackyes/underpass", http.StatusTemporaryRedirect)).Host(*host)

	r.HandleFunc("/start", func(rw http.ResponseWriter, r *http.Request) {
		// Verify the authentication token
		if *authToken != "" {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != *authToken {
				log.Printf("Unauthorized access attempt from %s\n", r.RemoteAddr)
				rw.WriteHeader(http.StatusUnauthorized)
				rw.Write([]byte("Unauthorized: Invalid or missing authentication token"))
				return
			}
		}

		// Extract and clean the subdomain from query
		subdomain := strings.TrimSpace(r.URL.Query().Get("subdomain"))
		if subdomain == "" {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte("Subdomain is required"))
			return
		}
		// Clean the subdomain by removing any path or query components
		subdomain = strings.Split(subdomain, "/")[0]
		subdomain = strings.Split(subdomain, "?")[0]
		subdomain = strings.Split(subdomain, "&")[0]

		c, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(err.Error()))
			return
		}

		writeMutex := sync.Mutex{}

		if _, ok := tunnels[subdomain]; ok {
			// Tunnel already exists

			writeMutex.Lock()
			err = util.WriteMsgPack(c, models.ServerMessage{
				Type:  "error",
				Error: fmt.Sprintf("Tunnel %s is already in use.", subdomain),
			})
			if err != nil {
				log.Println(err)
			}
			writeMutex.Unlock()

			c.Close()
			return
		}

		reqChan := make(chan Request, requestChannelBuffer)
		t := Tunnel{
			reqChan:   reqChan,
			listeners: make(map[int]chan models.ClientMessage),
		}

		// Set up ping/pong for connection health monitoring
		c.SetReadDeadline(time.Now().Add(websocketTimeout))
		c.SetPongHandler(func(string) error {
			// Reset the read deadline upon receiving a pong
			c.SetReadDeadline(time.Now().Add(websocketTimeout))
			return nil
		})

		// Start a ping loop in a separate goroutine
		pingTicker := time.NewTicker(websocketPingInterval)
		pingDone := make(chan struct{})
		go func() {
			defer pingTicker.Stop()
			for {
				select {
				case <-pingTicker.C:
					// Send a ping message with the current timestamp
					deadline := time.Now().Add(5 * time.Second)
					if err := c.WriteControl(websocket.PingMessage, []byte{}, deadline); err != nil {
						log.Printf("Failed to send ping to client %s: %v", subdomain, err)
						// Don't close here - let the read timeout handle it
					}
				case <-pingDone:
					return
				}
			}
		}()

		tunnels[subdomain] = &t

		log.Printf("New tunnel created with subdomain: %s\n", subdomain)
		writeMutex.Lock()
		err = util.WriteMsgPack(c, models.ServerMessage{
			Type:      "subdomain",
			Subdomain: subdomain,
		})
		if err != nil {
			log.Println(err)
		}
		writeMutex.Unlock()

		// Listen for messages and handle disconnections
		closeChan := make(chan struct{})
		go func() {
			defer func() {
				close(closeChan)
				close(pingDone)
				c.Close()

				// Clean up all listeners
				t.listenersMutex.Lock()
				for id, listener := range t.listeners {
					if listener != nil {
						close(listener)
						delete(t.listeners, id)
					}
				}
				t.listenersMutex.Unlock()

				// Remove tunnel from registry
				delete(tunnels, subdomain)
				log.Printf("Tunnel removed for subdomain: %s\n", subdomain)
			}()

			for {
				var message models.ClientMessage

				// Set read deadline for each message
				c.SetReadDeadline(time.Now().Add(websocketTimeout))

				err = util.ReadMsgPack(c, &message)
				if err != nil {
					// Check for specific websocket error types for better logging
					if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
						log.Printf("Unexpected close for subdomain %s: %v\n", subdomain, err)
					} else {
						log.Printf("Connection closed for subdomain: %s - %v\n", subdomain, err)
					}
					return
				}

				if message.Type == "close" {
					t.listenersMutex.Lock()
					if listener, exists := t.listeners[message.RequestID]; exists {
						select {
						case <-listener: // Drain the channel if necessary
						default:
						}
						close(listener)
						delete(t.listeners, message.RequestID)
					}
					t.listenersMutex.Unlock()
					continue
				}

				t.listenersMutex.RLock()
				if listener, ok := t.listeners[message.RequestID]; ok {
					select {
					case listener <- message:
						// Message sent successfully
					case <-time.After(requestTimeout):
						// If we can't send within the timeout, clean up the listener
						t.listenersMutex.Lock()
						// Double-check to avoid race conditions
						if l, stillExists := t.listeners[message.RequestID]; stillExists && l == listener && l != nil {
							select {
							case <-l: // Drain the channel if necessary
							default:
							}
							close(l)
							delete(t.listeners, message.RequestID)
							log.Printf("Cleaned up stuck listener for request %d (subdomain: %s)", message.RequestID, subdomain)
						}
						t.listenersMutex.Unlock()
						continue
					}
				}
				t.listenersMutex.RUnlock()
			}
		}()

	X:
		for {
			select {
			case _, ok := <-closeChan:
				if !ok {
					// Clean up
					close(reqChan)
					delete(tunnels, subdomain)
					break X
				}
			case req := <-reqChan:
				if req.Close {
					// This indicates that the request body has ended
					writeMutex.Lock()
					err = util.WriteMsgPack(c, models.ServerMessage{
						Type:      "close",
						RequestID: req.RequestID,
					})
					if err != nil {
						log.Println(err)
					}
					writeMutex.Unlock()
				} else {
					// Handle request based on data type
					if req.Data != nil {
						if req.Data.HTTPRequest != nil {
							marshalled, err := util.MarshalRequest(req.Data.HTTPRequest)
							if err != nil {
								log.Printf("Invalid request for path '%s': %v", req.Data.HTTPRequest.RequestURI, err)
								select {
								case <-closeChan:
									return
								default:
									writeMutex.Lock()
									err = util.WriteMsgPack(c, models.ServerMessage{
										Type:  "error",
										Error: "Invalid request parameters",
									})
									writeMutex.Unlock()
									continue
								}
							}

							writeMutex.Lock()
							err = util.WriteMsgPack(c, models.ServerMessage{
								Type:      "request",
								RequestID: req.RequestID,
								Request:   marshalled,
							})
							if err != nil {
								log.Println(err)
							}
							writeMutex.Unlock()
						} else if req.Data.RawData != nil {
							writeMutex.Lock()
							err = util.WriteMsgPack(c, models.ServerMessage{
								Type:      "data",
								RequestID: req.RequestID,
								Data:      req.Data.RawData,
							})
							if err != nil {
								log.Println(err)
							}
							writeMutex.Unlock()
						}
					}
				}
			}
		}
	}).Host(*host)

	r.NewRoute().HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		subdomain := strings.Split(r.Host, ".")[0]

		if t, ok := tunnels[subdomain]; ok {
			reqID := rand.Int()
			t.reqChan <- Request{
				RequestID: reqID,
				Data:      &RequestData{HTTPRequest: r},
			}

			// Pipe request body with improved reliability
			if r.Body != nil {
				go func() {
					defer func() {
						// Always send close request when finished
						t.reqChan <- Request{
							RequestID: reqID,
							Close:     true,
						}
					}()

					// Use a more reasonable buffer size
					buffer := make([]byte, 64*1024) // 64KB buffer

					for {
						n, readErr := r.Body.Read(buffer)

						// If we read any data, send it
						if n > 0 {
							// Copy the data to avoid buffer reuse issues
							dataCopy := make([]byte, n)
							copy(dataCopy, buffer[:n])

							select {
							case t.reqChan <- Request{
								RequestID: reqID,
								Data:      &RequestData{RawData: dataCopy},
							}:
								// Sent successfully
							case <-time.After(5 * time.Second):
								// Timeout sending to channel, log and continue
								log.Printf("Warning: Timeout sending data for request %d", reqID)
								continue
							}
						}

						// Check for end of data or errors
						if readErr != nil {
							if readErr != io.EOF {
								log.Printf("Read error for request %d: %v", reqID, readErr)
							}
							break
						}
					}
				}()
			}

			messageChannel := make(chan models.ClientMessage, messageChannelBuffer)
			t.listenersMutex.Lock()
			t.listeners[reqID] = messageChannel
			t.listenersMutex.Unlock()

		X:
			for {
				if message, ok := <-messageChannel; ok {
					switch message.Type {
					case "proxy_error":
						rw.WriteHeader(http.StatusBadGateway)
						rw.Write([]byte("Proxy error. See your terminal for more information."))
						t.listenersMutex.Lock()
						close(messageChannel)
						delete(t.listeners, reqID)
						t.listenersMutex.Unlock()
						break X
					case "response":
						for i, v := range message.Response.Headers {
							rw.Header().Add(i, strings.Join(v, ","))
						}
						rw.WriteHeader(message.Response.StatusCode)
					case "data":
						rw.Write(message.Data)
					}
				} else {
					break
				}
			}
		} else {
			log.Printf("Tunnel not found for subdomain: %s (active tunnels: %d)\n", subdomain, len(tunnels))
			rw.WriteHeader(http.StatusNotFound)
			rw.Write([]byte(fmt.Sprintf("Tunnel %[1]s not found.\n\nStart it with `underpass -p PORT -s %[1]s` 😎", subdomain)))
		}
	})

	log.Println("starting...")
	log.Printf("Server starting on port %s...\n", *port)
	if *TLS && *CertCrt != "" && *CertKey != "" {
		log.Printf("Starting with TLS enabled\n")
		err := http.ListenAndServeTLS(":"+*port, *CertCrt, *CertKey, r)
		if err != nil {
			log.Fatalf("Error starting server: %v", err)
		}
	} else {
		log.Printf("Starting without TLS\n")
		err := http.ListenAndServe(":"+*port, r)
		if err != nil {
			log.Fatalf("Error starting server: %v", err)
		}
	}
}

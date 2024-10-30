package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackyes/underpass/pkg/models"
	"github.com/jackyes/underpass/pkg/util"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
)

// RequestData rappresenta i possibili tipi di dati in una richiesta
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

var upgrader = websocket.Upgrader{}

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
		// Verifica del token di autenticazione
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

		reqChan := make(chan Request)
		t := Tunnel{
			reqChan:   reqChan,
			listeners: make(map[int]chan models.ClientMessage),
		}

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

		// Listen for messages (and disconnections)
		closeChan := make(chan struct{})
		go func() {
			for {
				var message models.ClientMessage
				err = util.ReadMsgPack(c, &message)
				if err != nil {
					log.Printf("Connection closed for subdomain: %s\n", subdomain)
					close(closeChan)
					c.Close()
					
					// Clean up all listeners before removing tunnel
					t.listenersMutex.Lock()
					for id, listener := range t.listeners {
						if listener != nil {
							close(listener)
						}
						delete(t.listeners, id)
					}
					t.listenersMutex.Unlock()
					
					delete(tunnels, subdomain)
					log.Printf("Tunnel removed for subdomain: %s\n", subdomain)
					break
				}

				if message.Type == "close" {
					t.listenersMutex.Lock()
					close(t.listeners[message.RequestID])
					delete(t.listeners, message.RequestID)
					t.listenersMutex.Unlock()
					continue
				}

				t.listenersMutex.RLock()
				if listener, ok := t.listeners[message.RequestID]; ok {
					select {
					case listener <- message:
					case <-time.After(2 * time.Second):
						// If we can't send within 2 seconds, assume the listener is stuck
						t.listenersMutex.RUnlock()
						t.listenersMutex.Lock()
						if l, stillExists := t.listeners[message.RequestID]; stillExists && l == listener && l != nil {
							close(listener)
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

			// Pipe request body
			if r.Body != nil {
				go func() {
					for {
						// Read up to 1 MB
						d := make([]byte, 1000000)
						n, err := r.Body.Read(d)

						if n > 0 {
							t.reqChan <- Request{
								RequestID: reqID,
								Data:      &RequestData{RawData: d[0:n]},
							}
						}

						if err != nil {
							t.reqChan <- Request{
								RequestID: reqID,
								Close:     true,
							}
							break
						}
					}
				}()
			}

			messageChannel := make(chan models.ClientMessage)
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

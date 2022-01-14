package main

import (
	"flag"
	"io"
	"log"
	"math/rand"
	"net/http"
	"strings"

	"github.com/cjdenio/underpass/pkg/server/util"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	gonanoid "github.com/matoous/go-nanoid/v2"
	"github.com/vmihailenco/msgpack/v5"
)

type Request struct {
	RequestID int
	Data      interface{}
}

type Tunnel struct {
	reqChan chan Request
}

var tunnels = make(map[string]Tunnel)

var upgrader = websocket.Upgrader{}

func main() {
	host := flag.String("host", "", "")

	flag.Parse()

	r := mux.NewRouter()

	r.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write([]byte("welcome to underpass"))
	}).Host(*host)

	r.HandleFunc("/start", func(rw http.ResponseWriter, r *http.Request) {
		subdomain := r.URL.Query().Get("subdomain")
		if subdomain == "" {
			subdomain, _ = gonanoid.Generate("abcdefghijklmnopqrstuvwxyz0123456789", 10)
		}

		if _, ok := tunnels[subdomain]; ok {
			// Tunnel already exists

			rw.WriteHeader(http.StatusConflict)
			rw.Write([]byte("Tunnel is already running"))
			return
		}

		reqChan := make(chan Request)
		tunnels[subdomain] = Tunnel{
			reqChan: reqChan,
		}

		c, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			rw.WriteHeader(http.StatusBadRequest)
			rw.Write([]byte(err.Error()))
			return
		}

		closeChan := make(chan struct{})
		// Listen for disconnections
		go func() {
			for {
				_, _, err := c.ReadMessage()
				if err != nil {
					close(closeChan)
					break
				}
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
				switch data := req.Data.(type) {
				case *http.Request:
					marshalled := util.MarshalRequest(data)

					resp, err := msgpack.Marshal(map[string]interface{}{
						"type":       "request",
						"request_id": req.RequestID,
						"data":       marshalled,
					})
					if err != nil {
						log.Println(err)
					}

					err = c.WriteMessage(websocket.BinaryMessage, resp)
					if err != nil {
						log.Println(err)
					}
				case []byte:
					resp, err := msgpack.Marshal(map[string]interface{}{
						"type":       "data",
						"request_id": req.RequestID,
						"data":       data,
					})
					if err != nil {
						log.Println(err)
					}

					err = c.WriteMessage(websocket.BinaryMessage, resp)
					if err != nil {
						log.Println(err)
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
				Data:      r,
			}

			if r.Body != nil {
				for {
					d := make([]byte, 50)
					n, err := r.Body.Read(d)

					if n > 0 {
						t.reqChan <- Request{
							RequestID: reqID,
							Data:      d[0:n],
						}
					}

					if err == io.EOF {
						break
					}
				}
			}
			rw.Write([]byte("success ✅"))
		} else {
			rw.WriteHeader(http.StatusNotFound)
			rw.Write([]byte("tunnel not found"))
		}
	})

	log.Println("starting...")

	http.ListenAndServe(":80", r)
}

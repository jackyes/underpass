package util

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/jackyes/underpass/pkg/models"
	"github.com/vmihailenco/msgpack/v5"
)

// msgPackPool is a sync.Pool for reusing byte slices to reduce memory allocations.
var msgPackPool = &sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, 1024) // Initial capacity
	},
}

// MarshalRequest validates the HTTP request and converts it into a models.Request object.
func MarshalRequest(r *http.Request) (models.Request, error) {
	// Validates fields before creating the Request
	if !ValidatePath(r.RequestURI) {
		return models.Request{}, fmt.Errorf("invalid path")
	}
	if !ValidateMethod(r.Method) {
		return models.Request{}, fmt.Errorf("invalid HTTP method")
	}
	if !ValidateHost(r.Host) {
		return models.Request{}, fmt.Errorf("invalid host")
	}

	return models.Request{
		Headers: r.Header,
		Path:    r.RequestURI,
		Method:  r.Method,
		Host:    r.Host,
	}, nil
}

// MarshalResponse converts an HTTP response into a models.Response object.
func MarshalResponse(r *http.Response) models.Response {
	return models.Response{
		Headers:    r.Header,
		StatusCode: r.StatusCode,
	}
}

// WriteMsgPack marshals the given data into MessagePack format and writes it to the WebSocket connection.
func WriteMsgPack(c *websocket.Conn, v interface{}) error {
	b := msgPackPool.Get().([]byte)
	defer msgPackPool.Put(b)

	b = b[:0] // Reset the slice

	marshalled, err := msgpack.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	b = append(b, marshalled...)

	err = c.WriteMessage(websocket.BinaryMessage, b)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	return nil
}

// ReadMsgPack reads a MessagePack formatted message from the WebSocket connection and unmarshals it into the given interface.
func ReadMsgPack(c *websocket.Conn, v interface{}) error {
	_, m, err := c.ReadMessage()
	if err != nil {
		return err
	}

	err = msgpack.Unmarshal(m, v)
	if err != nil {
		fmt.Println("unmarshal err")
		fmt.Println(err)
		return err
	}

	return nil
}

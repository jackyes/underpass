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
const (
	initialBufferSize = 32 * 1024 // 32KB 
	maxBufferSize     = 128 * 1024 // 128KB
)

var msgPackPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 0, initialBufferSize)
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
	// Get buffer from pool
	buf := msgPackPool.Get().([]byte)
	defer func() {
		// Only keep reasonably sized buffers in pool
		if cap(buf) <= maxBufferSize {
			buf = buf[:0] // Reset slice
			msgPackPool.Put(buf)
		} else {
			msgPackPool.Put(make([]byte, 0, initialBufferSize))
		}
	}()

	// Marshal data
	marshalled, err := msgpack.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// If marshalled data is larger than current buffer capacity,
	// allocate a new slice with exact needed capacity
	if len(marshalled) > cap(buf) {
		buf = make([]byte, len(marshalled))
	} else {
		buf = buf[:len(marshalled)]
	}
	copy(buf, marshalled)

	err = c.WriteMessage(websocket.BinaryMessage, buf)
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

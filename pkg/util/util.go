package util

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/jackyes/underpass/pkg/models"
	"github.com/vmihailenco/msgpack/v5"
)

// Buffer sizes based on message types for more efficient memory usage
const (
	// Small messages (headers, status updates)
	smallBufferSize = 4 * 1024 // 4KB
	// Medium messages (requests, responses)
	mediumBufferSize = 32 * 1024 // 32KB
	// Large messages (data chunks)
	largeBufferSize = 128 * 1024 // 128KB
	// Maximum buffer size to keep in pool
	maxBufferSize = 256 * 1024 // 256KB
)

// Multiple pools for different message sizes to reduce memory waste
var (
	smallMsgPackPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, smallBufferSize)
		},
	}

	mediumMsgPackPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, mediumBufferSize)
		},
	}

	largeMsgPackPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, 0, largeBufferSize)
		},
	}
)

// getBuffer selects the appropriate buffer pool based on estimated size
func getBuffer(estimatedSize int) []byte {
	switch {
	case estimatedSize <= smallBufferSize:
		return smallMsgPackPool.Get().([]byte)
	case estimatedSize <= mediumBufferSize:
		return mediumMsgPackPool.Get().([]byte)
	default:
		return largeMsgPackPool.Get().([]byte)
	}
}

// returnBuffer returns a buffer to the appropriate pool
func returnBuffer(buf []byte, pool *sync.Pool) {
	// Only keep reasonably sized buffers in pool
	if cap(buf) <= maxBufferSize {
		buf = buf[:0] // Reset slice length to 0
		pool.Put(buf)
	}
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
	// Estimate size based on message type
	var estimatedSize int

	// Estimate size based on message type to select appropriate buffer
	switch msg := v.(type) {
	case models.ClientMessage:
		if msg.Type == "data" && len(msg.Data) > 0 {
			estimatedSize = len(msg.Data) + 128 // Data plus overhead
		} else {
			estimatedSize = mediumBufferSize
		}
	case models.ServerMessage:
		if msg.Type == "data" && len(msg.Data) > 0 {
			estimatedSize = len(msg.Data) + 128 // Data plus overhead
		} else {
			estimatedSize = mediumBufferSize
		}
	default:
		estimatedSize = smallBufferSize
	}

	// Get buffer from appropriate pool
	buf := getBuffer(estimatedSize)

	// Determine which pool to return the buffer to
	var poolToUse *sync.Pool
	switch {
	case cap(buf) <= smallBufferSize:
		poolToUse = &smallMsgPackPool
	case cap(buf) <= mediumBufferSize:
		poolToUse = &mediumMsgPackPool
	default:
		poolToUse = &largeMsgPackPool
	}

	defer func() {
		returnBuffer(buf, poolToUse)
	}()

	// Marshal data directly without intermediate allocation
	marshalled, err := msgpack.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed to marshal data: %w", err)
	}

	// Use the marshalled data directly if it's too large for our buffer
	if len(marshalled) > cap(buf) {
		err = c.WriteMessage(websocket.BinaryMessage, marshalled)
		if err != nil {
			return fmt.Errorf("failed to write message: %w", err)
		}
		return nil
	}

	// Otherwise use our pooled buffer
	buf = buf[:len(marshalled)]
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

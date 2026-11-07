// Package pool provides object pools for high-allocation types on the proxy
// hot path. It wraps sync.Pool with generic helpers and pre-defined pools for
// common request/response and buffer types.
package pool

import (
	"bytes"
	"sync"

	"github.com/mayiants/ai-proxy-go/dto"
)

// Pool is a thin generic wrapper around sync.Pool.
type Pool[T any] struct {
	pool sync.Pool
}

// NewPool creates a pool that uses newFunc to allocate missing objects.
func NewPool[T any](newFunc func() T) *Pool[T] {
	return &Pool[T]{
		pool: sync.Pool{
			New: func() interface{} {
				return newFunc()
			},
		},
	}
}

// Get returns an object from the pool, allocating a new one if necessary.
func (p *Pool[T]) Get() T {
	return p.pool.Get().(T)
}

// Put returns an object to the pool. Callers must ensure the object will not
// be used after it is returned.
func (p *Pool[T]) Put(v T) {
	p.pool.Put(v)
}

// Pre-defined public pools for high-allocation types.

// GeneralOpenAIRequest pools *dto.GeneralOpenAIRequest.
var GeneralOpenAIRequest = NewPool(func() *dto.GeneralOpenAIRequest {
	return &dto.GeneralOpenAIRequest{}
})

// ChatCompletionsStreamResponse pools *dto.ChatCompletionsStreamResponse.
var ChatCompletionsStreamResponse = NewPool(func() *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{}
})

// BytesBuffer pools *bytes.Buffer and resets them on put.
var BytesBuffer = NewPool(func() *bytes.Buffer {
	return &bytes.Buffer{}
})

// byte slice size classes, from smallest to largest.
var byteSliceSizes = []int{
	4 * 1024,    // 4 KB
	16 * 1024,   // 16 KB
	64 * 1024,   // 64 KB
	256 * 1024,  // 256 KB
	1024 * 1024, // 1 MB
}

var byteSlicePools []*Pool[[]byte]

func init() {
	byteSlicePools = make([]*Pool[[]byte], len(byteSliceSizes))
	for i, size := range byteSliceSizes {
		size := size
		byteSlicePools[i] = NewPool(func() []byte {
			return make([]byte, 0, size)
		})
	}
}

// GetGeneralOpenAIRequest returns a zeroed *dto.GeneralOpenAIRequest from the pool.
func GetGeneralOpenAIRequest() *dto.GeneralOpenAIRequest {
	return GeneralOpenAIRequest.Get()
}

// PutGeneralOpenAIRequest resets and returns a *dto.GeneralOpenAIRequest to the pool.
func PutGeneralOpenAIRequest(req *dto.GeneralOpenAIRequest) {
	if req == nil {
		return
	}
	*req = dto.GeneralOpenAIRequest{}
	GeneralOpenAIRequest.Put(req)
}

// GetChatCompletionsStreamResponse returns a zeroed *dto.ChatCompletionsStreamResponse from the pool.
func GetChatCompletionsStreamResponse() *dto.ChatCompletionsStreamResponse {
	return ChatCompletionsStreamResponse.Get()
}

// PutChatCompletionsStreamResponse resets and returns a *dto.ChatCompletionsStreamResponse to the pool.
func PutChatCompletionsStreamResponse(resp *dto.ChatCompletionsStreamResponse) {
	if resp == nil {
		return
	}
	*resp = dto.ChatCompletionsStreamResponse{}
	ChatCompletionsStreamResponse.Put(resp)
}

// GetBuffer returns a reset *bytes.Buffer from the pool.
func GetBuffer() *bytes.Buffer {
	buf := BytesBuffer.Get()
	buf.Reset()
	return buf
}

// PutBuffer resets and returns a *bytes.Buffer to the pool.
func PutBuffer(buf *bytes.Buffer) {
	if buf == nil {
		return
	}
	buf.Reset()
	BytesBuffer.Put(buf)
}

// GetBytes returns a []byte with capacity >= minCap. The returned slice has
// length 0; callers should reslice it to the desired length before use.
func GetBytes(minCap int) []byte {
	for i, size := range byteSliceSizes {
		if minCap <= size {
			b := byteSlicePools[i].Get()
			return b[:0]
		}
	}
	// Request is larger than the largest class; allocate a dedicated slice.
	return make([]byte, 0, minCap)
}

// PutBytes returns a []byte to the appropriate size-class pool. Slices that do
// not fit any class are dropped and left for GC.
func PutBytes(b []byte) {
	if b == nil {
		return
	}
	c := cap(b)
	for i := len(byteSliceSizes) - 1; i >= 0; i-- {
		if c >= byteSliceSizes[i] {
			byteSlicePools[i].Put(b[:0])
			return
		}
	}
	// Slice is smaller than the smallest class; drop it.
}

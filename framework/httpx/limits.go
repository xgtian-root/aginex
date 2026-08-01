package httpx

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type RequestLimits struct {
	MaxBodyBytes   int64
	MaxHeaderBytes int
	MaxHeaderCount int
}

func NewRequestLimits(limits RequestLimits) (gin.HandlerFunc, error) {
	if limits.MaxBodyBytes < 1 {
		return nil, fmt.Errorf("maximum request body bytes must be positive")
	}
	if limits.MaxHeaderBytes < 1 {
		return nil, fmt.Errorf("maximum request header bytes must be positive")
	}
	if limits.MaxHeaderCount < 1 {
		return nil, fmt.Errorf("maximum request header count must be positive")
	}
	return func(c *gin.Context) {
		count, size := requestHeaderSize(c.Request.Header)
		if count > limits.MaxHeaderCount || size > limits.MaxHeaderBytes {
			AbortProblem(c, http.StatusRequestHeaderFieldsTooLarge, "HEADERS_TOO_LARGE", "Request headers are too large", "Reduce the number or size of request headers.")
			return
		}
		if c.Request.ContentLength > limits.MaxBodyBytes {
			AbortProblem(c, http.StatusRequestEntityTooLarge, "REQUEST_TOO_LARGE", "Request body is too large", "Reduce the request body size.")
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limits.MaxBodyBytes)
		}
		c.Next()
	}, nil
}

func requestHeaderSize(header http.Header) (count, size int) {
	for name, values := range header {
		for _, value := range values {
			count++
			size += len(name) + len(value)
		}
	}
	return count, size
}

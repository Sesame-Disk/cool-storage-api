package httputil

import "github.com/gin-gonic/gin"

// ResponseBytesSince returns the response-body delta since before. Gin uses -1
// as the size of an unwritten response, which is equivalent to zero here.
func ResponseBytesSince(w gin.ResponseWriter, before int64) int64 {
	if w == nil {
		return 0
	}
	if before < 0 {
		before = 0
	}
	after := int64(w.Size())
	if after < before {
		return 0
	}
	return after - before
}

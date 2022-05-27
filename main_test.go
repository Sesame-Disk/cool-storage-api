package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestPingResponse(t *testing.T) {

	w := httptest.NewRecorder()
	r := SetUpRouter()
	gin.SetMode(gin.TestMode)

	r.GET("/api/v1/ping", PingResponse)
	req, err := http.NewRequest(http.MethodGet, "/api/v1/ping", nil)
	if err != nil {
		t.Fatalf("Couldn't create request: %v\n", err)
	}

	// Perform the request
	r.ServeHTTP(w, req)

	// Check to see if the response was what you expected
	if w.Code != http.StatusOK {
		t.Fatalf("Expected to get status %d but instead got %d\n", http.StatusOK, w.Code)
	}
	assert.Equal(t, "pong1", w.Body.String())
	assert.Equal(t, http.StatusOK, w.Code)
}

func SetUpRouter() *gin.Engine {
	router := gin.Default()
	return router
}

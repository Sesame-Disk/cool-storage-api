package main

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPingResponse(t *testing.T) {

	w := httptest.NewRecorder()
	router := gin.Default()
	gin.SetMode(gin.TestMode)

	router.GET("/api/v1/ping", PingResponse)

	t.Run("pongTest", func(t *testing.T) {

		if w.Code != 200 {
			t.Errorf("Expected %v but got %v", 200, w.Code)
		}

		t.Log(w.Body.String())

	})
}

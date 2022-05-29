package main

import (
	"cool-storage-api/authenticate"
	register "cool-storage-api/register"
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

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
	assert.Equal(t, "pong", w.Body.String())
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetAuthenticationTokenHandler(t *testing.T) {

	w := httptest.NewRecorder()
	r := SetUpRouter()
	gin.SetMode(gin.TestMode)

	r.POST("/api/v1/auth-token/", GetAuthenticationTokenHandler)

	randomUser, randomPassword := getNewFakeUserPassword()
	register.RegisterUser(randomUser, randomPassword)

	v := make(url.Values)
	v.Set("username", randomUser)
	v.Add("password", randomPassword)

	req, err := http.NewRequest(http.MethodPost, "http://localhost:3001/api/v1/auth-token/", strings.NewReader(v.Encode()))
	if err != nil {
		t.Fatalf("Couldn't create request: %v\n", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	// Perform the request
	r.ServeHTTP(w, req)

	// Check to see if the response was what you expected
	if w.Code != http.StatusOK {
		t.Fatalf("Expected to get status %d but instead got %d\n", http.StatusOK, w.Code)
	}
	// assert.Equal(t, http.StatusOK, w.Code)

	var got gin.H
	err1 := json.Unmarshal(w.Body.Bytes(), &got)
	if err1 != nil {
		t.Fatal(err1)
	}

	token := got["token"].(string)

	_, err2 := authenticate.ValidateToken(token)
	if err2 != nil {
		t.Errorf("Expected %v but got %v", nil, err2)
	}
}

func TestRegistrationsHandler(t *testing.T) {

	w := httptest.NewRecorder()
	r := SetUpRouter()
	gin.SetMode(gin.TestMode)

	r.POST("/api/v1/registrations", RegistrationsHandler)

	randomUser, randomPassword := getNewFakeUserPassword()

	v := make(url.Values)
	v.Set("username", randomUser)
	v.Add("password", randomPassword)

	req, err := http.NewRequest(http.MethodPost, "http://localhost:3001/api/v1/registrations", strings.NewReader(v.Encode()))
	if err != nil {
		t.Fatalf("Couldn't create request: %v\n", err)
	}
	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")

	// Perform the request
	r.ServeHTTP(w, req)

	// Check to see if the response was what you expected
	if w.Code != http.StatusOK {
		t.Fatalf("Expected to get status %d but instead got %d\n", http.StatusOK, w.Code)
	}
	assert.Equal(t, "success", w.Body.String())
}

func TestAuthPing(t *testing.T) {

	w := httptest.NewRecorder()
	r := SetUpRouter()
	gin.SetMode(gin.TestMode)

	r.GET("/api/v1/auth/ping/", AuthPing)

	req, err := http.NewRequest(http.MethodGet, "http://localhost:3001/api/v1/auth/ping/", nil)
	if err != nil {
		t.Fatalf("Couldn't create request: %v\n", err)
	}

	randomUser, randomPassword := getNewFakeUserPassword()
	register.RegisterUser(randomUser, randomPassword)

	tokenDetails, _ := authenticate.GetToken(randomUser, randomPassword)
	auth_token := tokenDetails["auth_token"]
	value := "Token " + auth_token
	req.Header.Set("Authorization", value)

	// Perform the request
	r.ServeHTTP(w, req)

	// Check to see if the response was what you expected
	if w.Code != http.StatusOK {
		t.Fatalf("Expected to get status %d but instead got %d\n", http.StatusOK, w.Code)
	}
	assert.Equal(t, "pong", w.Body.String())
}

func SetUpRouter() *gin.Engine {
	router := gin.Default()
	return router
}

func getNewFakeUserPassword() (string, string) {
	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))
	return randomUser, randomPassword
}

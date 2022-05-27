package authenticate_test

import (
	"cool-storage-api/authenticate"
	"cool-storage-api/register"
	"fmt"
	"math/rand"
	"strconv"
	"testing"
	"time"
)

func TestValidateToken_WithRandomUser(t *testing.T) {
	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))

	register.RegisterUser(randomUser, randomPassword)

	tokenDetails, _ := authenticate.GetToken(randomUser, randomPassword)

	userDetails, _ := authenticate.ValidateToken(tokenDetails["auth_token"])

	username := fmt.Sprint(userDetails["username"])
	if username != randomUser {
		t.Errorf("Expected %v but got %v", randomUser, username)
	}

}

func TestValidateToken_WithNotValidToken(t *testing.T) {
	_, err := authenticate.ValidateToken("authToken")
	if err.Error() != "invalid access token" {
		t.Errorf("Expected %v but got %v", "invalid access token", err.Error())
	}
}

func TestGetToken_WithRandomUser(t *testing.T) {

	rand.Seed(time.Now().UnixNano())
	randomUser := strconv.Itoa(rand.Intn(1000000))
	randomPassword := strconv.Itoa(rand.Intn(1000000))

	register.RegisterUser(randomUser, randomPassword)

	tokenDetails, _ := authenticate.GetToken(randomUser, randomPassword)

	userDetails, _ := authenticate.ValidateToken(tokenDetails["auth_token"])

	username := fmt.Sprint(userDetails["username"])
	if username != randomUser {
		t.Errorf("Expected %v but got %v", randomUser, username)
	}

}

func TestBuildRandomToken(t *testing.T) {
	tokenDetails, err := authenticate.BuildRandomToken()
	if err != nil {
		t.Errorf("Expected %v but got %v", nil, err.Error())
	}

	const timeInterval = time.Minute * 1
	const timeLayout = "2006-01-02 15:04:05"
	token_type := tokenDetails["token_type"]
	auth_token := tokenDetails["auth_token"]
	generated_at := tokenDetails["generated_at"]
	expires_at := tokenDetails["expires_at"]

	t1, err2 := time.Parse(timeLayout, generated_at)
	t2, err3 := time.Parse(timeLayout, expires_at)

	if err2 != nil || err3 != nil || token_type != "Bearer" || len(auth_token) != 40 || t2 != t1.Add(timeInterval) {
		t.Errorf("Expected %v,%v,%v,%v,%v, but got %v,%v,%v,%v,%v,", nil, nil, "Bearer", 40, t1.Add(timeInterval),
			err2, err3, token_type, len(auth_token), t2)
	}
}

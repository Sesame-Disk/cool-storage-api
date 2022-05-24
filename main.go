package main

import (
	authenticate "cool-storage-api/authenticate"
	register "cool-storage-api/register"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func main() {

	r := gin.Default()
	r.GET("/api/v1/ping", func(c *gin.Context) {
		c.String(http.StatusOK, "pong")
	})
	r.POST("/api/v1/auth-token/", func(c *gin.Context) {
		getAuthenticationTokenHandler(c)
	})
	r.GET("/api/v1/auth/ping/", func(c *gin.Context) {
		authPing(c)
	})
	r.POST("/api/v1/registrations", func(c *gin.Context) {
		registrationsHandler(c)
	})

	r.Run(":3001")
}

func getAuthenticationTokenHandler(c *gin.Context) {

	c.Request.ParseForm()
	username := c.Request.FormValue("username")
	password := c.Request.FormValue("password")
	if username == "" || password == "" {
		c.String(http.StatusOK, "Please enter a valid username and password.\r\n")
	} else {

		tokenDetails, err := authenticate.Get_Token(username, password)
		token := tokenDetails["auth_token"]

		if err != nil {
			c.String(http.StatusOK, err.Error())
		} else {
			c.JSON(http.StatusOK, gin.H{"token": token})
		}
	}
}

func registrationsHandler(c *gin.Context) {
	c.Request.ParseForm()
	username := c.Request.FormValue("username")
	password := c.Request.FormValue("password")
	if username == "" || password == "" {
		c.String(http.StatusOK, "Please enter a valid username and password.\r\n")
	} else {
		response, err := register.RegisterUser(username, password)
		if err != nil {
			c.String(http.StatusOK, err.Error())
		} else {
			c.String(http.StatusOK, response)
		}
	}
}

func authPing(c *gin.Context) {

	// authToken := strings.Split(c.Request.Header.Get("Authorization"), "Token ")[1]
	data := strings.Split(c.Request.Header.Get("Authorization"), "Token ")

	if len(data) < 2 {
		c.String(http.StatusBadRequest, errors.New("request not valid").Error())
	} else {
		authToken := data[1]

		// userDetails, err := authenticate.ValidateToken(authToken)
		_, err := authenticate.ValidateToken(authToken)

		if err != nil {
			c.String(http.StatusOK, err.Error())
		} else {

			// username := fmt.Sprint(userDetails["username"])
			// c.String(http.StatusOK, "Welcome, "+username+"\r\n")
			c.String(http.StatusOK, "pong")
		}
	}
}

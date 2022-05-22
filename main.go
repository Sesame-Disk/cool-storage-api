package main

import (
	authentications "C/Users/acast/Documents/GitHub/cool-storage-api/authenticate"
	registrations "C/Users/acast/Documents/GitHub/cool-storage-api/register"
	"encoding/json"
	"fmt"
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
		seafile_authenticationsHandler(c)
	})

	r.POST("/registrations", func(c *gin.Context) {
		registrationsHandler(c)
	})

	r.GET("/authentications", func(c *gin.Context) {
		authenticationsHandler(c)
	})
	r.GET("/test", func(c *gin.Context) {
		testResourceHandler(c)
	})
	r.Run(":3001")
}

func seafile_authenticationsHandler(c *gin.Context) {

	c.Request.ParseForm()
	username := c.Request.FormValue("username")
	password := c.Request.FormValue("password")
	if username == "" || password == "" {
		c.String(http.StatusOK, "Please enter a valid username and password.\r\n")
	} else {

		tokenDetails, err := authentications.Get_Token(username, password)
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
		response, err := registrations.RegisterUser(username, password)
		if err != nil {
			c.String(http.StatusOK, err.Error())
		} else {
			c.String(http.StatusOK, response)
		}
	}
}

func authenticationsHandler(c *gin.Context) {

	username, password, ok := c.Request.BasicAuth()

	if ok {

		tokenDetails, err := authentications.GenerateToken(username, password)

		if err != nil {
			c.String(http.StatusOK, err.Error())
		} else {
			enc := json.NewEncoder(c.Writer)
			enc.SetIndent("", "  ")
			enc.Encode(tokenDetails)
		}
	} else {
		c.String(http.StatusOK, "You require a username/password to get a token.\r\n")
	}
}

func testResourceHandler(c *gin.Context) {

	authToken := strings.Split(c.Request.Header.Get("Authorization"), "Bearer ")[1]

	userDetails, err := authentications.ValidateToken(authToken)

	if err != nil {
		c.String(http.StatusOK, err.Error())
	} else {

		username := fmt.Sprint(userDetails["username"])
		c.String(http.StatusOK, "Welcome, "+username+"\r\n")
	}
}

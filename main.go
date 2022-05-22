package main

import (
	"C/Users/acast/Documents/GitHub/cool-storage-api/authenticate"
	"C/Users/acast/Documents/GitHub/cool-storage-api/register"
	"encoding/json"
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
	r.GET("/auth/ping/", func(c *gin.Context) {
		authPing(c)
	})

	r.POST("/registrations", func(c *gin.Context) {
		registrationsHandler(c)
	})

	r.GET("/authentications", func(c *gin.Context) {
		authenticationsHandler(c)
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

func authenticationsHandler(c *gin.Context) {

	username, password, ok := c.Request.BasicAuth()

	if ok {

		tokenDetails, err := authenticate.GenerateToken(username, password)

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

func authPing(c *gin.Context) {

	authToken := strings.Split(c.Request.Header.Get("Authorization"), "Token ")[1]

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

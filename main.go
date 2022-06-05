package main

import (
	authenticate "cool-storage-api/authenticate"
	register "cool-storage-api/register"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"PUT", "PATCH", "POST", "GET"},
		AllowHeaders:     []string{"authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
	}))

	r.GET("/api/v1/ping", func(c *gin.Context) {
		PingResponse(c)
	})
	r.POST("/api/v1/auth-token/", func(c *gin.Context) {
		GetAuthenticationTokenHandler(c)
	})
	r.GET("/api/v1/auth/ping/", func(c *gin.Context) {
		AuthPing(c)
	})
	r.POST("/api/v1/registrations", func(c *gin.Context) {
		RegistrationsHandler(c)
	})
	r.GET("/api/v1/account/info/", func(c *gin.Context) {
		AccountInfoResponse(c)
	})

	r.Run(":3001")
}

func enableCors(c *gin.Context) {
	c.Request.Header.Add("Access-Control-Allow-Origin", "*")
	c.Request.Header.Add("Access-Control-Allow-Credentials", "true")
	c.Request.Header.Add("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
	c.Request.Header.Add("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")
}

//"pong" response
func PingResponse(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

//return a valid token
func GetAuthenticationTokenHandler(c *gin.Context) {
	enableCors(c)
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {
		username := c.Request.FormValue("username")
		password := c.Request.FormValue("password")

		if username == "" || password == "" {
			c.String(http.StatusNotAcceptable, "please enter a valid username and password %v,%v,%v", username, password, c.Request.Body)
		} else {
			tokenDetails, err := authenticate.GetToken(username, password)
			if err != nil {
				c.String(http.StatusInternalServerError, err.Error())
			} else {
				token := tokenDetails["auth_token"]
				c.JSON(http.StatusOK, gin.H{"token": token})
			}
		}
	}
}

//register a user in the database
func RegistrationsHandler(c *gin.Context) {
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {
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
}

//ping request with token
func AuthPing(c *gin.Context) {

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

//to get user info
func AccountInfoResponse(c *gin.Context) {

	data := strings.Split(c.Request.Header.Get("Authorization"), "Token ")

	if len(data) < 2 {
		c.String(http.StatusBadRequest, errors.New("request not valid").Error())
	} else {
		authToken := data[1]
		userDetails, err := authenticate.ValidateToken(authToken)

		if err != nil {
			c.String(403, errors.New("invalid token").Error())
		} else {
			username := fmt.Sprint(userDetails["username"])
			ss := strings.Split(username, "@")
			name := ss[0]
			c.JSON(http.StatusOK, gin.H{

				"login_id": "",

				"is_staff": false,

				"name": name,

				"email_notification_interval": 0,

				"institution": "",

				"department": "",

				"avatar_url": "http://127.0.0.1:8000/media/avatars/default.png",

				"contact_email": nil,

				"space_usage": "0.00%",

				"usage": 0,

				"total": 0,

				"email": username,
			})
		}
	}
}

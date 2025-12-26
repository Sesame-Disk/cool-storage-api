package main

import (
	"cool-storage-api/authenticate"
	"cool-storage-api/configread"
	"cool-storage-api/dba"
	"cool-storage-api/plugins/glacierManager"
	"cool-storage-api/register"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {

	config := configread.Configuration

	r := gin.Default()
	r.MaxMultipartMemory = 8 << 20
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"PUT", "PATCH", "POST", "GET", "OPTIONS"},
		AllowHeaders:     []string{"authorization", "uploader-chunk-number", "uploader-chunks-total", "uploader-file-id", "uploader-file-name", "uploader-file-hash"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           86400,
	}))

	r.GET("/api/v1/ping", PingResponse)
	r.POST("/api/v1/auth-token", GetAuthenticationTokenHandler)
	r.GET("/api/v1/auth/ping", AuthPing)
	r.POST("/api/v1/registrations", RegistrationsHandler)
	r.GET("/api/v1/account/info", AccountInfoResponse)
	r.POST("/api/v1/single/upload", glacierManager.Upload)
	r.POST("/api/v1/single/download", glacierManager.Download)
	r.GET("/api/v1/get-archive", GetArchive)

	if err := r.Run(config.ServerConfig.Port); nil != err {
		panic(err)
	}
}

func enableCors(c *gin.Context) {
	c.Request.Header.Add("Access-Control-Allow-Origin", "*")
	c.Request.Header.Add("Access-Control-Allow-Credentials", "true")
	c.Request.Header.Add("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
	c.Request.Header.Add("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT")
}

func PingResponse(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

func GetArchive(c *gin.Context) {
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {
		archiveId := c.Request.FormValue("archiveId")
		res, err := dba.GetArchive(archiveId)
		if err != nil {
			c.String(http.StatusBadRequest, err1.Error())
		}
		c.JSON(http.StatusOK, gin.H{"status": http.StatusOK, "data": res})
	}
}

func GetAuthenticationTokenHandler(c *gin.Context) {
	enableCors(c)
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {
		username := c.Request.FormValue("username")
		password := c.Request.FormValue("password")

		if username == "" || password == "" {
			c.String(http.StatusNotAcceptable, "please enter a not void username and password")
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

func RegistrationsHandler(c *gin.Context) {
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {
		username := c.Request.FormValue("username")
		password := c.Request.FormValue("password")
		if username == "" || password == "" {
			c.String(http.StatusOK, "Please enter not void username and password.")
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

				"avatar_url": "http://127.0.0.1:3000/media/avatars/default.png",

				"contact_email": nil,

				"space_usage": "0.00%",

				"usage": 0,

				"total": 0,

				"email": username,
			})
		}
	}
}

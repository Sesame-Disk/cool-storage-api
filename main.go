package main

import (
	authenticate "cool-storage-api/authenticate"
	configread "cool-storage-api/configread"
	register "cool-storage-api/register"
	util "cool-storage-api/util"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {

	config := configread.Configuration

	r := gin.Default()
	// Set a lower memory limit for multipart forms (default is 32 MiB)
	r.MaxMultipartMemory = 8 << 20 // 8 MiB
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"PUT", "PATCH", "POST", "GET", "OPTIONS"},
		AllowHeaders:     []string{"authorization", "uploader-chunk-number", "uploader-chunks-total", "uploader-file-id", "uploader-file-name", "uploader-file-hash"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           86400,
	}))

	r.GET("/api/v1/ping", PingResponse)
	r.POST("/api/v1/auth-token/", GetAuthenticationTokenHandler)
	r.GET("/api/v1/auth/ping/", AuthPing)
	r.POST("/api/v1/registrations", RegistrationsHandler)
	r.GET("/api/v1/account/info/", AccountInfoResponse)
	r.POST("/api/v1/single/upload", Upload)

	// r.POST("/api/v1/multiple/upload", func(c *gin.Context) {
	// 	// Multipart form
	// 	form, err := c.MultipartForm()
	// 	if err != nil {
	// 		c.String(http.StatusBadRequest, "get form err: %s", err.Error())
	// 		return
	// 	}
	// 	files := form.File["upload[]"]

	// 	for _, file := range files {
	// 		filename := filepath.Base(file.Filename)
	// 		dst := "./upload/" + filename //<- destino del archivo
	// 		if err := c.SaveUploadedFile(file, dst); err != nil {
	// 			c.String(http.StatusBadRequest, "upload file err: %s", err.Error())
	// 			return
	// 		}
	// 	}

	// 	c.String(http.StatusOK, "Uploaded successfully %d files", len(files))
	// })

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

//"pong" response
func PingResponse(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}

//upload file
func Upload(c *gin.Context) {
	// Source
	_, uploadFile, err := c.Request.FormFile("file")
	if err != nil {
		c.String(http.StatusBadRequest, "get form err: %s", err.Error())
		return
	}

	file, err1 := uploadFile.Open()
	if err1 != nil {
		c.String(http.StatusBadRequest, "get form err: %s", err.Error())
		return
	}
	fileData, err2 := ioutil.ReadAll(file)
	if err2 != nil {
		c.String(http.StatusBadRequest, "get form err: %s", err.Error())
		return
	}

	filename := c.GetHeader("uploader-file-name")
	path := "./upload/"
	dst := path + filename //<- destino del archivo

	util.AppendData(dst, fileData)

	c.String(http.StatusOK, "File %s uploaded successfully.", filename)
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

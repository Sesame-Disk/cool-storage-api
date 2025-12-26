# Table of Content
- [Table of Content](#table-of-content)
  - [Installation](#installation)
    - [1. With Doker](#1-with-doker)
    - [2. Without Doker](#2-without-doker)
  - [Requirements](#requirements)
  - [Testing app](#testing-app)
    - [1. Basic test](#1-basic-test)
    - [2. Test coverage checks](#2-test-coverage-checks)
  - [Endpoints](#endpoints)
      - [1. To make sure the server started:  "/api/v1/ping"](#1-to-make-sure-the-server-started--apiv1ping)
      - [2. To add a sample john_doe's account to your application. Replace EXAMPLE_PASSWORD with a strong value: "/api/v1/registrations"](#2-to-add-a-sample-john_does-account-to-your-application-replace-example_password-with-a-strong-value-apiv1registrations)
      - [3. Request to the "/api/v1/auth-token/" endpoint using john_doe's credentials to get a time-based token.](#3-request-to-the-apiv1auth-token-endpoint-using-john_does-credentials-to-get-a-time-based-token)
      - [4. Authorization token request: "/api/v1/auth/ping/"](#4-authorization-token-request-apiv1authping)
      - [5. To get account info: "/api/v1/account/info/"](#5-to-get-account-info-apiv1accountinfo)
      - [6. To upload a file: "/api/v1/single/upload](#6-to-upload-a-file-apiv1singleupload)
      - [7. To download a file: "/api/v1/single/download"](#7-to-download-a-file-apiv1singledownload)
  - [References:](#references)

## Installation

### 1. With Doker

+ **Clone this repo**
```
git clone https://github.com/Sesame-Disk/cool-storage-api.git
cd cool-storage-api
```

+ **Build the image**
```
docker-compose build
```

+ **Run as a container**
```
docker-compose up
```
Now the server started on port :3001

[🔝Table of Contents](#table-of-content)

### 2. Without Doker

+ **Clone this repo**
```
git clone https://github.com/Sesame-Disk/cool-storage-api.git
cd cool-storage-api
```

+ **Install all the dependencies**
```
go mod tidy
```

+ **Create the configuration file**
```
cd conf
vim cool-api.yaml
```

> Use the *cool-api.dist.yaml* template file.

+ **Start the mysql server and run**
```
go run main.go
```
Now the server started on port :3001

[🔝Table of Contents](#table-of-content)

## Requirements 
Every name.go file must have a name_test.go where the tests for the respective functions of the name.go file must be

## Testing app 

### 1. Basic test 
* To run the all tests : 

```
go test ./...
```

* To run some test : 

```
go test -v .\some_test.go
```


### 2. Test coverage checks 
* Run the tests and save the coverage profile in "coverage.out" 

```
go test ./... --coverprofile=coverage.out
```

* View the coverage profile in your browser

```
go tool cover --html=coverage.out
```

[🔝Table of Contents](#table-of-content)

## Endpoints 

#### 1. To make sure the server started:  "/api/v1/ping" 
```
curl http://localhost:3001/api/v1/ping
```
output:
```
pong
```

#### 2. To add a sample john_doe's account to your application. Replace EXAMPLE_PASSWORD with a strong value: "/api/v1/registrations" 
```
curl -X POST http://localhost:3001/registrations -H "Content-Type: application/x-www-form-urlencoded" -d "username=john_doe&password=EXAMPLE_PASSWORD"
```

output:
```
Success
```

#### 3. Request to the "/api/v1/auth-token/" endpoint using john_doe's credentials to get a time-based token. 

```
curl -d "username=john_doe&password=EXAMPLE_PASSWORD" http://localhost:3001/api/v1/auth-token/
```

output:
{"token":"l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs"}

#### 4. Authorization token request: "/api/v1/auth/ping/" 

```
curl -H "Authorization: Token l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs" http://localhost:3001/api/v1/auth/ping/
```

output if token is valid:
```
pong
```

output if token not valid
```
invalid access token
```

output if token expired
```
the token is expired
```

#### 5. To get account info: "/api/v1/account/info/" 
```
curl -H "Authorization: Token 5DwfTS8iOkbV4LkyHUDucmdlLfMuyum8VBDTgz2j" http://localhost:3001/api/v1/account/info/
```
output example:
```
{
  "avatar_url":"http://127.0.0.1:3000/media/avatars/default.png",
  "contact_email":null,
  "department":"",
  "email":"john_doe",
  "email_notification_interval":0,
  "institution":"",
  "is_staff":false,
  "login_id":"",
  "name":"john_doe",
  "space_usage":"0.00%",
  "total":0,
  "usage":0
}
```

>Please note: When using Golang token-based authentication in a production environment, you should always use SSL/TLS certificates to prevent attacks during token requests, and responses flow.

#### 6. To upload a file: "/api/v1/single/upload 
```
curl -X POST http://localhost:3001/api/v1/single/upload -F "file=@filename.extension" -H "uploader-file-name: filename.extension"
```
output example:
```
File filename upload successfully
```

#### 7. To download a file: "/api/v1/single/download" 
```
curl -X POST http://localhost:3001/api/v1/single/download -F "archiveId=somerandomid" -F "fileName=filename.extenison"
```
output example:
```
download success
```

[🔝Table of Contents](#table-of-content)

## References: 
> https://www.vultr.com/docs/implement-tokenbased-authentication-with-golang-and-mysql-8-server/

[🔝Table of Contents](#table-of-content)

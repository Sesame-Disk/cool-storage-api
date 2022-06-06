# cool-storage-api

## Installation
**Clone this repo**
```
git clone https://github.com/Sesame-Disk/cool-storage-api.git

cd cool-storage-api
```

**Build the image**

```
docker-compose build
```
**Run as a container**

```
docker-compose up
```

## Requirements
Every name.go file must have a name_test.go where the tests for the respective functions of the name.go file must be






## Testing app

### Basic test
1. To run the all tests : 

```
go test ./...
```

2. To run some test : 

```
go test -v .\some_test.go
```


### Test coverage checks
1. Run the tests and save the coverage profile in "coverage.out" 

```
go test --coverprofile=coverage.out
```

2. View the coverage profile in your browser

```
go tool cover --html=coverage.out
```

## Test the Golang Token-based Authentication Logic
You'll now run and test Golang's token-based authentication logic in your application.

1. Download all the Golang packages you're using to import them into your application.

```
go mod tidy
```

2. Next, run the project.

```
cd cool-storage-api
go run main.go
```


3. The above command has a blocking function that makes your web application listen on port 3001. Therefore, don't enter any other command on your terminal window.

4. First make sure the server started executing de curl command below
```
curl http://localhost:3001/api/v1/ping
```

5. Then, execute the curl command below to add a sample john_doe's account to your application. Replace EXAMPLE_PASSWORD with a strong value.
```
curl -X POST http://localhost:3001/registrations -H "Content-Type: application/x-www-form-urlencoded" -d "username=john_doe&password=EXAMPLE_PASSWORD"
```

6. You should now receive the following output.
```
Success
```

7. Next, make a request to the /authentications endpoint using john_doe's credentials to get a time-based token.

```
curl -d "username=john_doe&password=EXAMPLE_PASSWORD" http://localhost:3001/api/v1/auth-token/
```

8. You should now get a JSON-based response showing the token details as shown below. The token is valid for sixty minutes(1 hour) since you defined this using the statement expirtyTime := time.Now().Add(time.Minute * 60) in the authentications.go file.

{"token":"l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs"}

9. Copy the value of the auth_token. For example l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs. Next, execute the curl command below and include your token in an Authorization header preceded by the term Token. 

```
curl -H "Authorization: Token l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs" http://localhost:3001/api/v1/auth/ping/
```

10. You should receive the following response, which shows you're now authenticated to the system using the time-based token.
```
pong
```

11. Attempt authenticating to the application using an invalid token. For instance, fakerandomtoken.
```
curl -H "Authorization: Token fakerandomtoken" http://localhost:3001/api/v1/auth/ping/
```

* Your application should not allow you in, and you'll get the error below.
```
invalid access token
```

12. Next, attempt requesting a expired token.

```
curl -H "Authorization: Token l7p81hy0iEPzKZY5l0SEfpiKecwGQ1aqsGO4DyYs" http://localhost:3001/api/v1/auth/ping/
```

* output 
```
the token is expired
```

14. Your token-based authentication logic is now working as expected.

15. Please note: When using Golang token-based authentication in a production environment, you should always use SSL/TLS certificates to prevent attacks during token requests, and responses flow.

## References:
> https://www.vultr.com/docs/implement-tokenbased-authentication-with-golang-and-mysql-8-server/

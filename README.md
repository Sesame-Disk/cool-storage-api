# cool-storage-api

Every name.go file must have a name_test.go where the tests for the respective functions of the name.go file must be

# Test the Golang Token-based Authentication Logic
You'll now run and test Golang's token-based authentication logic in your application.

1. Download all the Golang packages you're using to import them into your application.

$ go get github.com/go-sql-driver/mysql
$ go get golang.org/x/crypto/bcrypt
2. Next, run the project.
    >> cd directory
    >> go run main.go

3. The above command has a blocking function that makes your web application listen on port 3001. Therefore, don't enter any other command on your terminal window.

4. SSH to your server on another terminal window.

5. Then, execute the curl command below to add a sample john_doe's account to your application. Replace EXAMPLE_PASSWORD with a strong value.
>> curl -X POST http://localhost:3001/registrations -H "Content-Type: application/x-www-form-urlencoded" -d "username=john_doe&password=EXAMPLE_PASSWORD"

6. You should now receive the following output.
    >>Success

7. Next, make a request to the /authentications endpoint using john_doe's credentials to get a time-based token.

>> curl -u john_doe:EXAMPLE_PASSWORD http://localhost:3001/authentications

8. You should now get a JSON-based response showing the token details as shown below. The token is valid for sixty minutes(1 hour) since you defined this using the statement expirtyTime := time.Now().Add(time.Minute * 60) in the authentications.go file.

{
  "auth_token": "sxGfdDPQvb8ygi7wuAHt90CjMspteY8lDLtvV4AENlw=",
  "expires_at": "2021-11-27 14:05:39",
  "generated_at": "2021-11-27 13:05:39",
  "token_type": "Bearer"
}

9. Copy the value of the auth_token. For example sxGfdDPQvb8ygi7wuAHt90CjMspteY8lDLtvV4AENlw=. Next, execute the curl command below and include your token in an Authorization header preceded by the term Bearer. In the following command, you're querying the /test resource/endpoint. In a production environment, you can query any resource that allows authentication using the time-based token.

  >> curl -H "Authorization: Bearer sxGfdDPQvb8ygi7wuAHt90CjMspteY8lDLtvV4AENlw=" http://localhost:3001/test

10. You should receive the following response, which shows you're now authenticated to the system using the time-based token.
    >>Welcome, john_doe

11. Attempt authenticating to the application using an invalid token. For instance, fakerandomtoken.
  >> curl -H "Authorization: Bearer fakerandomtoken" http://localhost:3001/test

    *   Your application should not allow you in, and you'll get the error below.

        >>Invalid access token.

12. Next, attempt requesting a token without a valid user account.
    >> curl -u john_doe:WRONG_PASSWORD http://localhost:3001/authentications

    * output 
        >> Invalid username or password.

13. Also, if you attempt authenticating to the system after sixty minutes, your token should be expired, and you should receive the following error.
    >> The token is expired.

14. Your token-based authentication logic is now working as expected.

15. Please note: When using Golang token-based authentication in a production environment, you should always use SSL/TLS certificates to prevent attacks during token requests, and responses flow.

Reference: https://www.vultr.com/docs/implement-tokenbased-authentication-with-golang-and-mysql-8-server/

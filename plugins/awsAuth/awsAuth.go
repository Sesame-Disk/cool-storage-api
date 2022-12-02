package awsAuth

import (
	"context"
	"cool-storage-api/configread"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
)

var awsConfig = configread.Configuration.AWSConfig

func Authenticate() (aws.Config, error) {
	if awsConfig.AccessProfileName != "" {
		return AuthWithProfile()
	} else if awsConfig.AccessKeyID != "" && awsConfig.SecretAccessKey != "" {
		return AuthWithCredentials()
	}
	return aws.Config{}, errors.New("No autentification method found")
}

func AuthWithProfile() (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(awsConfig.AccessProfileName))
	return cfg, err
}

func AuthWithCredentials() (aws.Config, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	return cfg, err
}

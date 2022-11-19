package glacier

import (
	"context"
	"cool-storage-api/configread"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	// "github.com/aws/aws-sdk-go-v2/service/glacier"
	// "github.com/aws/aws-sdk-go-v2/service/s3/types"
	// "github.com/aws/smithy-go"
)

func AWSAuth() (aws.Config, error) {
	awsConfig := configread.Configuration.AWSConfig
	isProfileAuth := strings.Contains(awsConfig.AuthMethod, "profile")
	isKeyAuth := strings.Contains(awsConfig.AuthMethod, "key") || strings.Contains(awsConfig.AuthMethod, "secret")
	if isProfileAuth {
		return AuthWithProfile("")
	} else if isKeyAuth {
		return AuthWithCredentials()
	}
	return aws.Config{}, errors.New("No autentification method found")
}

func AuthWithProfile(profileName string) (aws.Config, error) {
	if profileName == "" {
		profileName = "default"
	}
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithSharedConfigProfile(profileName))
	return cfg, err
}

func AuthWithCredentials() (aws.Config, error) {
	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	return cfg, err
}

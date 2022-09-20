package glacierjob

import (
	"context"
	"cool-storage-api/configread"
	"errors"
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/glacier"
	glaciertypes "github.com/aws/aws-sdk-go-v2/service/glacier/types"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/aws/smithy-go"
)

// To initiate an inventory-retrieval job
// The example initiates an inventory-retrieval job for the vault input.
func Glacier_InitiateJob() {
	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	if err != nil {
		log.Fatalf("failed to load AWS configuration, %v", err)
	}

	svc := glacier.NewFromConfig(cfg)

	input := &glacier.InitiateJobInput{
		AccountId: aws.String("-"),
		JobParameters: &glaciertypes.JobParameters{
			Description: aws.String(awsConfig.JobDescription),
			Format:      aws.String("CSV"),
			SNSTopic:    aws.String(awsConfig.SNSTopic),
			Type:        aws.String("inventory-retrieval"),
			// ArchiveId:   aws.String("iD"),
		},
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.InitiateJob(context.TODO(), input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			// handle NoSuchKey error		//PENDING
			return
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// code := apiErr.ErrorCode()
			// message := apiErr.ErrorMessage()
			// handle error code
			//PENDING
			return
		}
		// handle error //PENDING
		return
	}
	fmt.Println(result)
}

// To list jobs for a vault
// The example lists jobs for the vault input.
func Glacier_ListJobs() {
	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	if err != nil {
		log.Fatalf("failed to load AWS configuration, %v", err)
	}

	svc := glacier.NewFromConfig(cfg)

	input := &glacier.ListJobsInput{
		AccountId: aws.String("-"),
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.ListJobs(context.TODO(), input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			// handle NoSuchKey error		//PENDING
			return
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// code := apiErr.ErrorCode()
			// message := apiErr.ErrorMessage()
			// handle error code
			//PENDING
			return
		}
		// handle error //PENDING
		return
	}

	fmt.Println(result)
}

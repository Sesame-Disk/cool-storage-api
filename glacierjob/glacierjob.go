package glacierjob

import (
	"context"
	"cool-storage-api/configread"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

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
func Glacier_InitiateInventoryJob() {
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
			Description: aws.String("My inventory job"),
			Format:      aws.String("CSV"),
			SNSTopic:    aws.String(awsConfig.SNSTopic),
			Type:        aws.String("inventory-retrieval"),
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
	fmt.Println(*result)
}

func Glacier_InitiateRetrievalJob(archiveId string, archiveName string) (string, error) {

	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	if err != nil {
		// log.Fatalf("failed to load AWS configuration, %v", err)
		return "", errors.New("failed to load AWS configuration")
	}

	svc := glacier.NewFromConfig(cfg)
	description := fmt.Sprintf("Retrieval job to download %s file", archiveName)
	input := &glacier.InitiateJobInput{
		AccountId: aws.String("-"),
		JobParameters: &glaciertypes.JobParameters{
			Description: aws.String(description),
			SNSTopic:    aws.String(awsConfig.SNSTopic),
			Type:        aws.String("archive-retrieval"),
			ArchiveId:   aws.String(archiveId),
			Tier:        aws.String("Standard"),
		},
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.InitiateJob(context.TODO(), input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			// handle NoSuchKey error		//PENDING
			// fmt.Println(err.Error())
			return "", err
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// code := apiErr.ErrorCode()
			// message := apiErr.ErrorMessage()
			// handle error code
			//PENDING
			fmt.Println(err.Error())
			return "", err
		}
		// handle error //PENDING
		fmt.Println(err.Error())
		return "", err
	}

	return *result.JobId, nil
}

// To get information about a previously initiated job
// The example returns information about the previously initiated job specified by the
// job ID.
func Glacier_DescribeJob(jobId string) (glacier.DescribeJobOutput, error) {

	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	if err != nil {
		log.Fatalf("failed to load AWS configuration, %v", err)
		return glacier.DescribeJobOutput{}, errors.New("failed to load AWS configuration")
	}

	svc := glacier.NewFromConfig(cfg)

	input := &glacier.DescribeJobInput{
		AccountId: aws.String("-"),
		JobId:     aws.String(jobId),
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.DescribeJob(context.TODO(), input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			// handle NoSuchKey error		//PENDING
			fmt.Println(err.Error())
			return glacier.DescribeJobOutput{}, err
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// code := apiErr.ErrorCode()
			// message := apiErr.ErrorMessage()
			// handle error code
			//PENDING
			fmt.Println(err.Error())
			return glacier.DescribeJobOutput{}, err
		}
		// handle error //PENDING
		fmt.Println(err.Error())
		return glacier.DescribeJobOutput{}, err
	}

	// fmt.Println(*result.ArchiveId)
	// fmt.Println(result.StatusCode)
	// fmt.Println(result.Completed)
	// fmt.Println(*result.StatusMessage)
	// fmt.Println(*result.CreationDate)
	// fmt.Println(*result.CompletionDate)
	// fmt.Println(*result.JobDescription)
	return *result, nil
}

func GlacierIsJobCompleted(jobId string) (bool, error) {
	jobInformation, err := Glacier_DescribeJob(jobId)
	if err != nil {
		return false, err
	}
	return jobInformation.Completed, nil
}

// To get the output of a previously initiated job
// The example downloads the output of a previously initiated inventory retrieval job
// that is identified by the job ID.
func Glacier_GetJobOutput(jobId, fileName string) (int32, error) {

	log.Println("Downloading...")

	awsConfig := configread.Configuration.AWSConfig
	cfg, err := config.LoadDefaultConfig(context.TODO(), config.WithRegion(awsConfig.Region),
		config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(awsConfig.AccessKeyID, awsConfig.SecretAccessKey, awsConfig.AccessToken)))
	if err != nil {
		// log.Fatalf("failed to load AWS configuration, %v", err)
		return 400, errors.New("failed to load AWS configuration")
	}

	svc := glacier.NewFromConfig(cfg)
	input := &glacier.GetJobOutputInput{
		AccountId: aws.String("-"),
		JobId:     aws.String(jobId),
		Range:     aws.String(""),
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.GetJobOutput(context.TODO(), input)
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			// handle NoSuchKey error		//PENDING
			return 400, err
		}
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) {
			// code := apiErr.ErrorCode()
			// message := apiErr.ErrorMessage()
			// handle error code
			//PENDING
			return 400, err
		}
		// handle error //PENDING
		return 400, err
	}
	defer result.Body.Close()

	//MAKE DIRECTORY IF NOT EXIST
	pathsample := "../cool-storage-api/download"
	if _, err := os.Stat(pathsample); os.IsNotExist(err) {
		os.MkdirAll(pathsample, 0700) // Create your file
	}

	outputFile := "../cool-storage-api/download/"
	outputFilename := outputFile + fileName

	out, err := os.Create(outputFilename)
	if err != nil {
		// handle error //PENDING
		return 400, err
	}
	defer out.Close()

	bufferLength := 1024 * 1024 * 100 //100MB

	buf := make([]byte, bufferLength) //make([]byte, 1024*1024*100) // 100MB
	_, err = io.CopyBuffer(out, result.Body, buf)
	if err != nil {
		// handle error //PENDING
		return 400, err
	}

	// log("Finished. Saved to %s\n", outputFile)
	// fmt.Println(result.Status)
	return result.Status, nil
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

	fmt.Println(*result)
}

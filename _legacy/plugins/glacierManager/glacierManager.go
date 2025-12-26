package glacierManager

import (
	"context"
	"cool-storage-api/authenticate"
	"cool-storage-api/configread"
	"cool-storage-api/dba"
	"cool-storage-api/plugins/glacierManager/glacierDownload"
	"cool-storage-api/plugins/glacierManager/glacierUpload"
	"log"
	"time"

	"cool-storage-api/util"
	"errors"
	"io/ioutil"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/glacier"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/gin-gonic/gin"
)

var awsConfig = configread.Configuration.AWSConfig

func Upload(c *gin.Context) {
	// Get data from request
	userToken := c.GetHeader("user-token")
	tokenDetails, err := authenticate.ValidateToken(userToken)
	if err != nil {
		c.String(http.StatusBadRequest, "user token not valid")
		return
	}
	_, uploadFile, err := c.Request.FormFile("file")

	file, err := uploadFile.Open()
	fileData, err := ioutil.ReadAll(file)
	if err != nil {
		c.String(http.StatusBadRequest, "get form err: %s", err.Error())
		return
	}

	filename := c.GetHeader("uploader-file-name")
	chunkid := c.GetHeader("uploader-chunk-number")
	chunksTotal := c.GetHeader("uploader-chunks-total")
	path := "./upload/"
	dst := path + filename //<- destino del archivo

	// marge actual chunck with prev
	util.AppendData(dst, fileData)

	//AWS-Glacier
	if chunkid == chunksTotal {
		user_id := tokenDetails["user_id"]
		db := glacierUpload.Upload(dst, filename, user_id.(int))
		if db != nil {
			c.String(http.StatusInternalServerError, db.Error())
		} else {
			// c.String(http.StatusOK, "File %s uploaded successfully with id %s", filename, *result.ArchiveId)
			c.String(http.StatusOK, "File %s uploaded successfully", filename)
		}
	} else {
		c.String(http.StatusOK, "Chunk # %s of file %s uploaded successfully.", chunkid, filename)
	}
}

func Download(c *gin.Context) {
	err1 := c.Request.ParseForm()
	if err1 != nil {
		c.String(http.StatusBadRequest, err1.Error())
	} else {

		archiveId := c.Request.FormValue("archiveId")
		archiveStruc, err := dba.GetArchive(archiveId)
		if err != nil {
			c.String(http.StatusBadRequest, err1.Error())
		}

		if archiveStruc.File_state != "uploaded" {
			c.String(http.StatusInternalServerError, "The file you're trying to download has already been uploaded")
		} else {

			start := time.Now()
			log.Print("starting download file at")
			log.Print(start)

			err := glacierDownload.Download(archiveStruc)
			if err != nil {
				c.String(http.StatusBadGateway, err.Error())
			} else {
				c.String(http.StatusOK, "download success")
			}

			end := time.Now()
			log.Print("finish download file at")
			log.Print(end)
		}
	}
}

func JobInit() {}

func ListJobs(cfg aws.Config) (*glacier.ListJobsOutput, error) {

	svc := glacier.NewFromConfig(cfg)

	input := &glacier.ListJobsInput{
		AccountId: aws.String("-"),
		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.ListJobs(context.TODO(), input)
	return result, err
}

func GetJob(cfg aws.Config, jobId string) (glacier.DescribeJobOutput, error) {

	svc := glacier.NewFromConfig(cfg)
	input := &glacier.DescribeJobInput{

		AccountId: aws.String("-"),

		JobId: aws.String(jobId),

		VaultName: aws.String(awsConfig.VaultName),
	}

	result, err := svc.DescribeJob(context.TODO(), input)
	if err != nil {

		var nsk *types.NoSuchKey

		if errors.As(err, &nsk) {

			return glacier.DescribeJobOutput{}, errors.New("job no such key error")

		}

		var apiErr smithy.APIError

		if errors.As(err, &apiErr) {

			return glacier.DescribeJobOutput{}, errors.New("job api error")

		}

		return glacier.DescribeJobOutput{}, errors.New("job unknown error")
	}
	return *result, nil
}

func IsJobCompleted() {}

func GetJobOutput() {}

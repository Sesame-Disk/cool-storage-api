package glacierUpload

import (
	"bytes"
	"context"
	configread "cool-storage-api/configread"
	"cool-storage-api/dba"
	"cool-storage-api/plugins/awsAuth"
	util "cool-storage-api/util"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/glacier"
)

func Upload(dst string, filename string, user_id int) error {
	Ufile, err := ioutil.ReadFile(dst)
	if err != nil {
		return errors.New(fmt.Sprintf("Fail to load the file: %s", err))
	}

	Rfile, err := os.Stat(dst)
	if err != nil {
		return errors.New(fmt.Sprintf("Fail to load the size of the file: %s", err))
	}
	file_size := util.HumanFileSize(float64(Rfile.Size()))

	cfg, err := awsAuth.Authenticate()
	if err != nil {
		return errors.New(fmt.Sprintf("failed to load AWS configuration: %s", err))
	}

	client := glacier.NewFromConfig(cfg)
	awsConfig := configread.Configuration.AWSConfig

	vaultName := awsConfig.VaultName
	input := glacier.UploadArchiveInput{
		VaultName:          &vaultName,
		ArchiveDescription: &filename,
		Body:               bytes.NewReader(Ufile),
	}
	result, err := client.UploadArchive(context.TODO(), &input)
	if err != nil {
		return errors.New(fmt.Sprintf("failed to upload archive to AWS-Glacier: %s", err))
	}
	// save data to db
	// user_Int, _ := strconv.Atoi(user_id)
	archive_data := util.Archive{
		Vault_file_id: *result.ArchiveId,
		Library_id:    1,
		User_id:       user_id,
		File_name:     filename,
		Upload_date:   time.Now().Format("2006-01-02 15:04:05"),
		File_size:     file_size,
		File_checksum: *result.Checksum,
		File_state:    "uploaded",
	}

	response := dba.InsertArchive(archive_data)
	if response != nil {
		return errors.New(fmt.Sprintf("Error on save upload data to DB: %s", response.Error()))
	}
	return nil
}

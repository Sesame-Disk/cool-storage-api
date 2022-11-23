package glacierDownload

import (
	"cool-storage-api/plugins/glacierManager/glacierJob"
	"cool-storage-api/util"
	"errors"
	"time"
)

func Download(a util.Archive) error {

	// *steps:
	//1-initiate retrieval archive job
	//2-wait for job is completed(ask for job description)
	//3-get job output and write the file

	jobId, err := glacierJob.Glacier_InitiateRetrievalJob(a.Vault_file_id, a.File_name)
	if err != nil {
		return err
	} else {
		i := 0
		LIMIT := 300 //  wait at most 75h
		for i < LIMIT {
			i++
			time.Sleep(15 * time.Minute)
			completed, err := glacierJob.GlacierIsJobCompleted(jobId)
			if err != nil {
				return err
			}
			if completed {
				_, err := glacierJob.Glacier_GetJobOutput(jobId, a.File_name)
				return err
			}
		}
	}
	return errors.New("initiate retrieval archive job time limit")
}

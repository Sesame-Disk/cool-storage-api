package glacierdownloader

import (
	glacierjob "cool-storage-api/glacierjob"
	util "cool-storage-api/util"
	"errors"
	"time"
)

func Download(a util.Archive) error {

	// *steps:
	//1-initiate retrieval archive job
	//2-wait for job is completed(ask for job description)
	//3-get job output and write the file

	jobId, err := glacierjob.Glacier_InitiateRetrievalJob(a.Vault_file_id)
	if err != nil {
		return err
	} else {
		i := 0
		LIMIT := 300 //  wait at most 75h
		for i < LIMIT {
			i++
			time.Sleep(15 * time.Minute)
			completed, err := glacierjob.GlacierIsJobCompleted(jobId)
			if err != nil {
				return err
			}
			if completed {
				_, err := glacierjob.Glacier_GetJobOutput(jobId, a.File_name)
				return err
			}
		}
	}
	return errors.New("initiate retrieval archive job time limit")
}

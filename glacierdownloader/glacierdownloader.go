package glacierdownloader

import (
	glacierjob "cool-storage-api/glacierjob"
	"errors"
	"time"
)

func Download(archiveId, fileName string) error {

	// *steps:
	//1-initiate retrieval archive job
	//2-wait for job is completed(ask for job description)
	//3-get job output and write the file

	jobId, err := glacierjob.Glacier_InitiateRetrievalJob(archiveId)
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
				_, err := glacierjob.Glacier_GetJobOutput(jobId, fileName)
				return err
			}
		}
	}
	return errors.New("initiate retrieval archive job time limit")
}

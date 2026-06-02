package httputil

import (
	"errors"
	"fmt"
	"io"
)

const SingleShotUploadReadLimitBytes int64 = 1 << 30

var ErrReadLimitExceeded = errors.New("read limit exceeded")

func ReadAllWithLimit(r io.Reader, declaredSize, limit int64) ([]byte, error) {
	if r == nil {
		return nil, errors.New("reader is nil")
	}
	if limit <= 0 {
		return nil, fmt.Errorf("%w: invalid limit %d", ErrReadLimitExceeded, limit)
	}
	if declaredSize > limit {
		return nil, fmt.Errorf("%w: declared size %d exceeds limit %d", ErrReadLimitExceeded, declaredSize, limit)
	}

	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: body exceeds limit %d", ErrReadLimitExceeded, limit)
	}
	return data, nil
}

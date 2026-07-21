package v2

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Sesame-Disk/sesamefs/internal/db"
	"github.com/Sesame-Disk/sesamefs/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestValidateManifestNormalizesUppercaseSHA256(t *testing.T) {
	hash := strings.Repeat("AB", 32)
	req := &fileFromBlocksRequest{
		Filename: "case.bin",
		Size:     4,
		Blocks:   []fileFromBlocksBlock{{SHA256: hash, Size: 4}},
	}
	if err := validateManifest(req, 8); err != nil {
		t.Fatalf("validateManifest() error = %v", err)
	}
	want := strings.ToLower(hash)
	if req.Blocks[0].SHA256 != want {
		t.Fatalf("normalized hash = %q, want %q", req.Blocks[0].SHA256, want)
	}
	digest := req.manifestDigest()
	req.Blocks[0].SHA256 = hash
	if err := validateManifest(req, 8); err != nil || req.manifestDigest() != digest {
		t.Fatalf("case-only replay digest changed: err=%v got=%s want=%s", err, req.manifestDigest(), digest)
	}
}

// histogramSampleCount reads the observation count off a single Histogram
// series (a HistogramVec.WithLabelValues() result), used to assert a metric
// gained an observation since Histogram values aren't comparable via
// testutil.ToFloat64 (which only works for single-value Counters/Gauges).
func histogramSampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	h, ok := o.(prometheus.Histogram)
	if !ok {
		t.Fatalf("observer is not a prometheus.Histogram: %T", o)
	}
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}
	return m.GetHistogram().GetSampleCount()
}

// hex64 builds deterministic, distinct valid lowercase-hex SHA-256 ids for
// manifest validation tests (n keeps each block unique without colliding).
func hex64(n int) string { return fmt.Sprintf("%064x", n) }

func validBlock(n int, size int64) fileFromBlocksBlock {
	return fileFromBlocksBlock{SHA256: hex64(n), Size: size}
}

func TestManifestDigest_DependsOnSHA256AndSize(t *testing.T) {
	// The client no longer sends a SHA-1; the digest is the true content identity
	// (ordered SHA-256s + sizes). It must vary with content/size and be stable.
	base := func(sha256 string, size int64) *fileFromBlocksRequest {
		return &fileFromBlocksRequest{
			ParentDir: "/", Filename: "f.bin", Size: size,
			Blocks: []fileFromBlocksBlock{{SHA256: sha256, Size: size}},
		}
	}
	if base(hex64(1), 100).manifestDigest() == base(hex64(2), 100).manifestDigest() {
		t.Fatal("manifestDigest must differ when sha256 differs")
	}
	if base(hex64(1), 100).manifestDigest() == base(hex64(1), 101).manifestDigest() {
		t.Fatal("manifestDigest must differ when size differs")
	}
	if base(hex64(1), 100).manifestDigest() != base(hex64(1), 100).manifestDigest() {
		t.Fatal("manifestDigest must be stable for identical manifests")
	}
}

func TestValidateManifest_AcceptsBlocks(t *testing.T) {
	req := &fileFromBlocksRequest{
		Filename: "movie.mov",
		Size:     WebUploadBlockSize + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, WebUploadBlockSize),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(req, WebUploadBlockSize); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestValidateManifest_RejectsInvalidSHA256(t *testing.T) {
	for _, sha256 := range []string{"", "zz", strings.Repeat("a", 63), strings.Repeat("g", 64)} {
		req := &fileFromBlocksRequest{
			Filename: "f.bin",
			Size:     100,
			Blocks:   []fileFromBlocksBlock{{SHA256: sha256, Size: 100}},
		}
		if err := validateManifest(req, WebUploadBlockSize); err == nil {
			t.Fatalf("expected rejection for invalid sha256 %q", sha256)
		}
	}
}

func TestValidateManifest_RejectsConflictingSizeForSameSHA256(t *testing.T) {
	// Same content (same SHA-256) declared with two different sizes is a lie that
	// would corrupt the committed file's size/offsets; it must be rejected.
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize * 2,
		Blocks: []fileFromBlocksBlock{
			{SHA256: hex64(7), Size: WebUploadBlockSize},
			{SHA256: hex64(7), Size: WebUploadBlockSize - 1},
		},
	}
	if err := validateManifest(req, WebUploadBlockSize); err == nil {
		t.Fatal("expected rejection for conflicting size for same sha256")
	}
}

func TestValidateManifest_HonorsConfiguredBlockSize(t *testing.T) {
	// The non-final block size is validated against the CONFIGURED CAS block size,
	// not a hardcoded 8 MB. With a 4 MB configured size, a 4 MB non-final block is
	// valid and an 8 MB one is rejected -- and vice versa.
	const fourMB = int64(4 * 1024 * 1024)

	okReq := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     fourMB + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, fourMB),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(okReq, fourMB); err != nil {
		t.Fatalf("4 MB non-final block rejected under 4 MB config: %v", err)
	}

	badReq := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize + 100,
		Blocks: []fileFromBlocksBlock{
			validBlock(1, WebUploadBlockSize),
			validBlock(2, 100),
		},
	}
	if err := validateManifest(badReq, fourMB); err == nil {
		t.Fatal("8 MB non-final block must be rejected under a 4 MB block-size config")
	}
}

func TestValidateManifest_AllowsRepeatedIdenticalBlocks(t *testing.T) {
	// A file may legitimately reference the same content block twice; identical
	// (sha256, size) repeats must be allowed.
	b := validBlock(3, WebUploadBlockSize)
	req := &fileFromBlocksRequest{
		Filename: "f.bin",
		Size:     WebUploadBlockSize * 2,
		Blocks:   []fileFromBlocksBlock{b, b},
	}
	if err := validateManifest(req, WebUploadBlockSize); err != nil {
		t.Fatalf("repeated identical block rejected: %v", err)
	}
}

func TestClassifyBlockUploadCommitConflict_ReturnsPublishedResultForMatchingDigest(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
		ResultFilename: "published.txt",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-a")
	if resultName != "published.txt" {
		t.Fatalf("resultName = %q, want published.txt", resultName)
	}
	if errorCode != "" || errorMessage != "" {
		t.Fatalf("unexpected error classification: code=%q message=%q", errorCode, errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_DetectsDifferentCommittedFile(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
		ResultFilename: "other.txt",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-b")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommittedDifferentFileConflictCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommittedDifferentFileConflictCode)
	}
	if errorMessage != "session already committed a different file" {
		t.Fatalf("errorMessage = %q, want permanent different-file conflict", errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_TreatsMissingResultAsInProgress(t *testing.T) {
	session := db.BlockUploadSession{
		ManifestDigest: "digest-a",
	}

	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(session, true, "digest-a")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommitInProgressCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommitInProgressCode)
	}
	if errorMessage != "commit still in progress; retry" {
		t.Fatalf("errorMessage = %q, want retryable in-progress conflict", errorMessage)
	}
}

func TestClassifyBlockUploadCommitConflict_TreatsMissingSessionStateAsInProgress(t *testing.T) {
	resultName, errorCode, errorMessage := classifyBlockUploadCommitConflict(db.BlockUploadSession{}, false, "digest-a")
	if resultName != "" {
		t.Fatalf("resultName = %q, want empty", resultName)
	}
	if errorCode != blockUploadCommitInProgressCode {
		t.Fatalf("errorCode = %q, want %q", errorCode, blockUploadCommitInProgressCode)
	}
	if errorMessage != "commit still in progress; retry" {
		t.Fatalf("errorMessage = %q, want retryable in-progress conflict", errorMessage)
	}
}

func TestCommittedFileIDFromSession(t *testing.T) {
	valid := strings.Repeat("a", 40)
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: valid}); got != valid {
		t.Fatalf("committedFileIDFromSession(valid) = %q, want %q", got, valid)
	}
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: strings.Repeat("b", 64)}); got != "" {
		t.Fatalf("committedFileIDFromSession(sha256) = %q, want empty", got)
	}
	if got := committedFileIDFromSession(db.BlockUploadSession{ResultCommitID: "not-a-fsid"}); got != "" {
		t.Fatalf("committedFileIDFromSession(invalid) = %q, want empty", got)
	}
}

// finding 8: observeBlockVerification must classify the WHOLE pass as
// "needs_upload" if even one block wasn't ready (size_mismatch counts as not
// ready), and must tally each distinct block exactly once into the right
// per-status counter.
func TestObserveBlockVerification_AllReady(t *testing.T) {
	before := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready"))
	beforeSamples := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("ready"))

	hashes := []string{hex64(1), hex64(2)}
	statuses := map[string]int{hex64(1): blockStatusReady, hex64(2): blockStatusReady}
	observeBlockVerification(time.Now(), hashes, statuses, nil)

	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready")); got != before+2 {
		t.Errorf("ready count = %v, want %v", got, before+2)
	}
	if got := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("ready")); got != beforeSamples+1 {
		t.Errorf("ready duration sample count = %d, want %d", got, beforeSamples+1)
	}
}

func TestObserveBlockVerification_MixedResultIsNeedsUpload(t *testing.T) {
	beforeReady := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready"))
	beforeNeeds := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload"))
	beforeMismatch := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("size_mismatch"))
	beforeNeedsUploadSamples := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload"))

	hashes := []string{hex64(3), hex64(4), hex64(5)}
	statuses := map[string]int{
		hex64(3): blockStatusReady,
		hex64(4): blockStatusNeedsUpload,
		hex64(5): blockStatusSizeMismatch,
	}
	observeBlockVerification(time.Now(), hashes, statuses, nil)

	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready")); got != beforeReady+1 {
		t.Errorf("ready count = %v, want %v", got, beforeReady+1)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload")); got != beforeNeeds+1 {
		t.Errorf("needs_upload count = %v, want %v", got, beforeNeeds+1)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("size_mismatch")); got != beforeMismatch+1 {
		t.Errorf("size_mismatch count = %v, want %v", got, beforeMismatch+1)
	}
	// A single not-ready block downgrades the WHOLE pass's duration label to
	// "needs_upload", not just "ready" for the ready ones.
	if got := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload")); got != beforeNeedsUploadSamples+1 {
		t.Errorf("needs_upload duration sample count = %d, want %d", got, beforeNeedsUploadSamples+1)
	}
}

func TestObserveBlockVerification_ForcedNeedsUploadOverridesReadyMetric(t *testing.T) {
	beforeReady := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready"))
	beforeNeeds := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload"))
	beforeNeedsUploadSamples := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload"))

	hashes := []string{hex64(6)}
	statuses := map[string]int{hex64(6): blockStatusReady}
	forcedNeedsUpload := map[string]struct{}{hex64(6): {}}
	observeBlockVerification(time.Now(), hashes, statuses, forcedNeedsUpload)

	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("ready")); got != beforeReady {
		t.Errorf("ready count = %v, want unchanged %v", got, beforeReady)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload")); got != beforeNeeds+1 {
		t.Errorf("needs_upload count = %v, want %v", got, beforeNeeds+1)
	}
	if got := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload")); got != beforeNeedsUploadSamples+1 {
		t.Errorf("needs_upload duration sample count = %d, want %d", got, beforeNeedsUploadSamples+1)
	}
}

func TestSummarizeBlockVerification_ObservesSizeMismatchBefore422Path(t *testing.T) {
	beforeMismatch := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("size_mismatch"))
	beforeNeeds := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload"))
	beforeSamples := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload"))

	blocks := []fileFromBlocksBlock{
		{SHA256: hex64(7), Size: 10},
		{SHA256: hex64(8), Size: 11},
	}
	statuses := map[string]int{
		hex64(7): blockStatusSizeMismatch,
		hex64(8): blockStatusNeedsUpload,
	}

	blockIDs, needsUpload, sizeMismatchHash := summarizeBlockVerification(time.Now(), blocks, []string{hex64(7), hex64(8)}, statuses, map[string]string{})

	if len(blockIDs) != 2 || blockIDs[0] != hex64(7) || blockIDs[1] != hex64(8) {
		t.Fatalf("blockIDs = %v, want ordered SHA-256 list", blockIDs)
	}
	if len(needsUpload) != 1 || needsUpload[0] != hex64(8) {
		t.Fatalf("needsUpload = %v, want [%s]", needsUpload, hex64(8))
	}
	if sizeMismatchHash != hex64(7) {
		t.Fatalf("sizeMismatchHash = %q, want %q", sizeMismatchHash, hex64(7))
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("size_mismatch")); got != beforeMismatch+1 {
		t.Fatalf("size_mismatch count = %v, want %v", got, beforeMismatch+1)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyBlocksTotal.WithLabelValues("needs_upload")); got != beforeNeeds+1 {
		t.Fatalf("needs_upload count = %v, want %v", got, beforeNeeds+1)
	}
	if got := histogramSampleCount(t, metrics.BlockUploadVerifyDuration.WithLabelValues("needs_upload")); got != beforeSamples+1 {
		t.Fatalf("needs_upload duration sample count = %d, want %d", got, beforeSamples+1)
	}
}

func TestBlockUploadVerifyDurationBucketsExtendForLargeFiles(t *testing.T) {
	h, ok := metrics.BlockUploadVerifyDuration.WithLabelValues("ready").(prometheus.Histogram)
	if !ok {
		t.Fatalf("observer is not a prometheus.Histogram: %T", metrics.BlockUploadVerifyDuration.WithLabelValues("ready"))
	}

	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("write histogram metric: %v", err)
	}

	var got []float64
	for _, b := range m.GetHistogram().GetBucket() {
		got = append(got, b.GetUpperBound())
	}
	want := []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 15, 30, 60, 120}
	if len(got) != len(want) {
		t.Fatalf("bucket count = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("bucket[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestBlockUploadVerifyErrorsTotal(t *testing.T) {
	beforePresence := testutil.ToFloat64(metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("presence"))
	beforeClassify := testutil.ToFloat64(metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("classify"))

	metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("presence").Inc()
	metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("classify").Inc()

	if got := testutil.ToFloat64(metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("presence")); got != beforePresence+1 {
		t.Fatalf("presence errors = %v, want %v", got, beforePresence+1)
	}
	if got := testutil.ToFloat64(metrics.BlockUploadVerifyErrorsTotal.WithLabelValues("classify")); got != beforeClassify+1 {
		t.Fatalf("classify errors = %v, want %v", got, beforeClassify+1)
	}
}

//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// This PR exists because the verification stores drifted away from the storage
// class the server actually writes through: they followed the generic S3_* set,
// which R23b moved onto the legacy backend's own bucket, and the mismatch showed
// up as a silent skip rather than a failure. defaultClassS3Config fixes today's
// drift but reintroduces the shape of the problem -- it repeats the class name and
// the class's declared location, so editing config.docker.yaml alone would put
// them out of step again and send the GC tests straight back to skipping.
//
// This gate is the structural half: it fails loudly the moment the shipped dev
// profile and the verification helper disagree, instead of letting the disagreement
// express itself as absent coverage.
func TestVerificationStoreMatchesShippedDefaultClass(t *testing.T) {
	// Resolve the helper's fallbacks, not whatever the runner's environment has
	// set: the contract under test is "the helper's defaults track the file".
	for _, key := range []string{
		"S3_CLASS_HOT_MINIO_LOCAL_BUCKET",
		"S3_CLASS_HOT_MINIO_LOCAL_ENDPOINT",
		"S3_CLASS_HOT_MINIO_LOCAL_REGION",
	} {
		t.Setenv(key, "")
	}

	data, err := os.ReadFile(filepath.Join("..", "..", "configs", "config.docker.yaml"))
	if err != nil {
		t.Fatalf("read config.docker.yaml: %v", err)
	}
	var shipped struct {
		Storage struct {
			DefaultClass string `yaml:"default_class"`
			Classes      map[string]struct {
				Bucket   string `yaml:"bucket"`
				Endpoint string `yaml:"endpoint"`
				Region   string `yaml:"region"`
			} `yaml:"classes"`
		} `yaml:"storage"`
	}
	if err := yaml.Unmarshal(data, &shipped); err != nil {
		t.Fatalf("parse config.docker.yaml: %v", err)
	}

	// The helper spells the class name into its S3_CLASS_* variables, so a renamed
	// default class silently disables its own overrides.
	const helperClass = "hot-minio-local"
	if shipped.Storage.DefaultClass != helperClass {
		t.Fatalf("config.docker.yaml default_class = %q, but defaultClassS3Config reads S3_CLASS_%s_* overrides; rename them together",
			shipped.Storage.DefaultClass, "HOT_MINIO_LOCAL")
	}
	declared, ok := shipped.Storage.Classes[helperClass]
	if !ok {
		t.Fatalf("config.docker.yaml declares no storage class %q", helperClass)
	}

	got := defaultClassS3Config()
	if got.Bucket != declared.Bucket {
		t.Errorf("verification bucket = %q, config.docker.yaml declares %q", got.Bucket, declared.Bucket)
	}
	if got.Endpoint != declared.Endpoint {
		t.Errorf("verification endpoint = %q, config.docker.yaml declares %q", got.Endpoint, declared.Endpoint)
	}
	if got.Region != declared.Region {
		t.Errorf("verification region = %q, config.docker.yaml declares %q", got.Region, declared.Region)
	}
}

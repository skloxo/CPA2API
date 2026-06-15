package buildinfo

import (
	"strings"
	"testing"
	"time"
)

func TestGitDescribe(t *testing.T) {
	result := gitDescribe()
	
	// Should return a non-empty string
	if result == "" {
		t.Fatal("gitDescribe() returned empty string")
	}
	
	// Should not contain newlines
	if strings.Contains(result, "\n") {
		t.Fatalf("gitDescribe() contains newline: %q", result)
	}
}

func TestInitSetsVersion(t *testing.T) {
	// After init(), Version should not be "dev" (unless git is not available)
	// In a git repo, it should be set to git describe output
	if Version == "" {
		t.Fatal("Version should not be empty after init")
	}
}

func TestInitSetsBuildDate(t *testing.T) {
	// After init(), BuildDate should not be "unknown"
	if BuildDate == "unknown" {
		t.Fatal("BuildDate should not be 'unknown' after init")
	}
	
	// Should be a valid RFC3339 timestamp
	_, err := time.Parse(time.RFC3339, BuildDate)
	if err != nil {
		t.Fatalf("BuildDate is not valid RFC3339: %q, error: %v", BuildDate, err)
	}
}

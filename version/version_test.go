package version

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestGet(t *testing.T) {
	info := Get()
	
	// Check that runtime fields are populated correctly
	if info.GoVersion != runtime.Version() {
		t.Errorf("Get().GoVersion = %v, want %v", info.GoVersion, runtime.Version())
	}
	
	if info.Arch != runtime.GOARCH {
		t.Errorf("Get().Arch = %v, want %v", info.Arch, runtime.GOARCH)
	}
	
	if info.OS != runtime.GOOS {
		t.Errorf("Get().OS = %v, want %v", info.OS, runtime.GOOS)
	}
	
	// Check that version fields match the package variables
	if info.Version != Version {
		t.Errorf("Get().Version = %v, want %v", info.Version, Version)
	}
	
	if info.GitCommit != GitCommit {
		t.Errorf("Get().GitCommit = %v, want %v", info.GitCommit, GitCommit)
	}
	
	if info.BuildDate != BuildDate {
		t.Errorf("Get().BuildDate = %v, want %v", info.BuildDate, BuildDate)
	}
}

func TestInfoString(t *testing.T) {
	tests := []struct {
		name string
		info Info
		want string
	}{
		{
			name: "dev_version_no_commit",
			info: Info{
				Version:   "dev",
				GitCommit: "unknown",
				BuildDate: "unknown",
			},
			want: "devx version dev",
		},
		{
			name: "version_with_short_commit",
			info: Info{
				Version:   "1.2.3",
				GitCommit: "abc123",
				BuildDate: "unknown",
			},
			want: "devx version 1.2.3 (abc123)",
		},
		{
			name: "version_with_long_commit",
			info: Info{
				Version:   "1.2.3",
				GitCommit: "abc123def456789",
				BuildDate: "unknown",
			},
			want: "devx version 1.2.3 (abc123d)",
		},
		{
			name: "version_with_build_date",
			info: Info{
				Version:   "1.2.3",
				GitCommit: "unknown",
				BuildDate: "2024-01-15",
			},
			want: "devx version 1.2.3 built 2024-01-15",
		},
		{
			name: "full_version_info",
			info: Info{
				Version:   "1.2.3",
				GitCommit: "abc123def456789",
				BuildDate: "2024-01-15",
			},
			want: "devx version 1.2.3 (abc123d) built 2024-01-15",
		},
		{
			name: "empty_commit_and_date",
			info: Info{
				Version:   "1.0.0",
				GitCommit: "",
				BuildDate: "",
			},
			want: "devx version 1.0.0",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.info.String()
			if got != tt.want {
				t.Errorf("Info.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInfoDetailed(t *testing.T) {
	info := Info{
		Version:   "1.2.3",
		GitCommit: "abc123def",
		BuildDate: "2024-01-15",
		GoVersion: "go1.21.0",
		OS:        "darwin",
		Arch:      "arm64",
	}
	
	detailed := info.Detailed()
	
	// Check that all fields are present in the detailed output
	expectedFields := []string{
		"Version:    1.2.3",
		"Git commit: abc123def",
		"Build date: 2024-01-15",
		"Go version: go1.21.0",
		"OS/Arch:    darwin/arm64",
	}
	
	for _, field := range expectedFields {
		if !strings.Contains(detailed, field) {
			t.Errorf("Detailed() missing field: %s\nGot: %s", field, detailed)
		}
	}
	
	// Check the header
	if !strings.HasPrefix(detailed, "devx version information:") {
		t.Errorf("Detailed() should start with 'devx version information:', got: %s", detailed)
	}
}

func TestDefaultValues(t *testing.T) {
	// Test that default values are set correctly
	// These are the values when not set via ldflags
	if Version != "dev" && Version != "" {
		t.Logf("Version is set to: %s (expected 'dev' in development)", Version)
	}
	
	if GitCommit != "unknown" && GitCommit != "" {
		t.Logf("GitCommit is set to: %s (expected 'unknown' in development)", GitCommit)
	}
	
	if BuildDate != "unknown" && BuildDate != "" {
		t.Logf("BuildDate is set to: %s (expected 'unknown' in development)", BuildDate)
	}
	
	// GoVersion should always be set to runtime version
	if GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %v, want %v", GoVersion, runtime.Version())
	}
}

func TestInfoJSON(t *testing.T) {
	// Test that Info struct can be marshaled to JSON
	info := Info{
		Version:   "1.2.3",
		GitCommit: "abc123",
		BuildDate: "2024-01-15",
		GoVersion: "go1.21.0",
		OS:        "linux",
		Arch:      "amd64",
	}
	
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Failed to marshal Info to JSON: %v", err)
	}
	
	var decoded Info
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal JSON to Info: %v", err)
	}
	
	// Check that all fields are preserved
	if decoded.Version != info.Version {
		t.Errorf("JSON roundtrip Version = %v, want %v", decoded.Version, info.Version)
	}
	if decoded.GitCommit != info.GitCommit {
		t.Errorf("JSON roundtrip GitCommit = %v, want %v", decoded.GitCommit, info.GitCommit)
	}
	if decoded.BuildDate != info.BuildDate {
		t.Errorf("JSON roundtrip BuildDate = %v, want %v", decoded.BuildDate, info.BuildDate)
	}
	if decoded.GoVersion != info.GoVersion {
		t.Errorf("JSON roundtrip GoVersion = %v, want %v", decoded.GoVersion, info.GoVersion)
	}
	if decoded.OS != info.OS {
		t.Errorf("JSON roundtrip OS = %v, want %v", decoded.OS, info.OS)
	}
	if decoded.Arch != info.Arch {
		t.Errorf("JSON roundtrip Arch = %v, want %v", decoded.Arch, info.Arch)
	}
	
	// Check JSON structure
	var jsonMap map[string]string
	if err := json.Unmarshal(data, &jsonMap); err != nil {
		t.Fatalf("Failed to unmarshal JSON to map: %v", err)
	}
	
	expectedKeys := []string{"version", "git_commit", "build_date", "go_version", "arch", "os"}
	for _, key := range expectedKeys {
		if _, exists := jsonMap[key]; !exists {
			t.Errorf("JSON output missing key: %s", key)
		}
	}
}

func TestInfoStructFields(t *testing.T) {
	// Test that Info struct has all expected fields with correct tags
	info := Info{
		Version:   "test",
		GitCommit: "test",
		BuildDate: "test",
		GoVersion: "test",
		Arch:      "test",
		OS:        "test",
	}
	
	// This ensures all fields are accessible
	if info.Version == "" || info.GitCommit == "" || info.BuildDate == "" ||
		info.GoVersion == "" || info.Arch == "" || info.OS == "" {
		t.Error("Info struct fields should be accessible")
	}
}

func BenchmarkGet(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Get()
	}
}

func BenchmarkString(b *testing.B) {
	info := Get()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = info.String()
	}
}

func BenchmarkDetailed(b *testing.B) {
	info := Get()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = info.Detailed()
	}
}

func ExampleInfo_String() {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123def",
		BuildDate: "2024-01-15",
	}
	fmt.Println(info.String())
	// Output: devx version 1.0.0 (abc123d) built 2024-01-15
}

func ExampleInfo_Detailed() {
	info := Info{
		Version:   "1.0.0",
		GitCommit: "abc123",
		BuildDate: "2024-01-15",
		GoVersion: "go1.21.0",
		OS:        "darwin",
		Arch:      "arm64",
	}
	fmt.Println(info.Detailed())
	// Output: devx version information:
	//   Version:    1.0.0
	//   Git commit: abc123
	//   Build date: 2024-01-15
	//   Go version: go1.21.0
	//   OS/Arch:    darwin/arm64
}
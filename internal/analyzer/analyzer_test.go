package analyzer

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

func TestAnalyzeFixture(t *testing.T) {
	input, err := os.Open("../../fixtures/access.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	got, err := Analyze(input, 1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	expectedBytes, err := os.ReadFile("../../fixtures/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected Report
	if err := json.Unmarshal(expectedBytes, &expected); err != nil {
		t.Fatal(err)
	}
	gotBytes, _ := json.Marshal(got)
	expectedBytes, _ = json.Marshal(expected)
	if string(gotBytes) != string(expectedBytes) {
		t.Fatalf("unexpected report\n got: %s\nwant: %s", gotBytes, expectedBytes)
	}
}

func TestAnalyzeReportsInvalidLine(t *testing.T) {
	input, err := os.Open("../../fixtures/invalid.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	_, err = Analyze(input, 1024*1024)
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("expected error on line 2, got %v", err)
	}
}

func TestAnalyzeEmptyFile(t *testing.T) {
	report, err := Analyze(strings.NewReader("\n"), 1024)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalRequests != 0 || report.ServerErrorPercent != 0 || report.TopRoutes == nil || math.IsNaN(report.ServerErrorPercent) {
		t.Fatalf("unexpected empty report: %#v", report)
	}
}

func TestAnalyzeRequiresRequestTime(t *testing.T) {
	line := `{"time":"2026-09-05T12:00:01Z","ip":"192.0.2.10","method":"GET","path":"/","status":200}`
	_, err := Analyze(strings.NewReader(line), 1024)
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("expected missing request_time error, got %v", err)
	}
}

func TestAnalyzeReportsLineLimit(t *testing.T) {
	line := `{"time":"2026-09-05T12:00:01Z","ip":"192.0.2.10","method":"GET","path":"/very-long-path","status":200,"request_time":0.1}`
	_, err := Analyze(strings.NewReader(line), 64)
	if err == nil || !strings.Contains(err.Error(), "line 1") || !strings.Contains(err.Error(), "exceeds 64 bytes") {
		t.Fatalf("expected line length error on line 1, got %v", err)
	}
}

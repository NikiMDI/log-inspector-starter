package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestScenarios(t *testing.T) {
	for _, scenario := range []string{"normal", "errors", "slow", "burst"} {
		t.Run(scenario, func(t *testing.T) {
			var a, b bytes.Buffer
			if err := generate(&a, 2000, 42, scenario); err != nil {
				t.Fatal(err)
			}
			if err := generate(&b, 2000, 42, scenario); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a.Bytes(), b.Bytes()) {
				t.Fatal("not deterministic")
			}
			lines := strings.Split(strings.TrimSpace(a.String()), "\n")
			if len(lines) != 2000 {
				t.Fatal("wrong count")
			}
			matches := 0
			for _, line := range lines {
				var e entry
				if err := json.Unmarshal([]byte(line), &e); err != nil {
					t.Fatal(err)
				}
				if e.Status >= 500 || e.RequestTime >= 2 || e.IP == "192.0.2.50" {
					matches++
				}
			}
			if scenario == "normal" && matches != 0 {
				t.Fatal("unexpected incident")
			}
			if scenario != "normal" && matches == 0 {
				t.Fatal("missing incident")
			}
		})
	}
}

func TestBadOptions(t *testing.T) {
	var b bytes.Buffer
	if generate(&b, -1, 42, "normal") == nil {
		t.Fatal("negative rows accepted")
	}
	if generate(&b, 1, 42, "unknown") == nil {
		t.Fatal("unknown scenario accepted")
	}
}

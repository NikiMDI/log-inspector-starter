// Общий генератор учебных логов. Не является частью решения студента.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"time"
)

type entry struct {
	Time        string  `json:"time"`
	IP          string  `json:"ip"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Status      int     `json:"status"`
	RequestTime float64 `json:"request_time"`
}

func generate(w io.Writer, n int, seed int64, scenario string) error {
	if n < 0 {
		return fmt.Errorf("rows must be nonnegative")
	}
	switch scenario {
	case "normal", "errors", "slow", "burst":
	default:
		return fmt.Errorf("unknown scenario %q", scenario)
	}
	rng := rand.New(rand.NewSource(seed))
	start := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	paths := []string{"/api/products", "/api/orders", "/api/health", "/api/search"}
	enc := json.NewEncoder(w)
	for i := 0; i < n; i++ {
		path := paths[rng.Intn(len(paths))]
		ip := fmt.Sprintf("192.0.2.%d", 1+rng.Intn(20))
		method := "GET"
		if path == "/api/orders" {
			method = "POST"
		}
		status := 200
		durationMS := 10 + rng.Intn(190)
		// Фиксированный час и ограниченный набор ключей
		// влияние числа строк, не увеличивая размер карт агрегации.
		second := rng.Intn(3600)
		switch scenario {
		case "errors":
			if second >= 1800 && rng.Intn(2) == 0 {
				status = 500
			}
		case "slow":
			if path == "/api/search" {
				durationMS += 2000
			}
		case "burst":
			if rng.Intn(10) < 7 {
				ip = "192.0.2.50"
				second = 1800 + rng.Intn(60)
			}
		}
		e := entry{start.Add(time.Duration(second) * time.Second).Format(time.RFC3339), ip, method, path, status, float64(durationMS) / 1000}
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	n := flag.Int("rows", 10000, "number of records")
	seed := flag.Int64("seed", 42, "reproducible seed")
	scenario := flag.String("scenario", "normal", "normal, errors, slow, burst")
	flag.Parse()
	w := bufio.NewWriter(os.Stdout)
	if err := generate(w, *n, *seed, *scenario); err != nil {
		log.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		log.Fatal(err)
	}
}

package analyzer

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"sort"
	"strings"
	"time"
)

type Report struct {
	TotalRequests      int64         `json:"total_requests"`
	ServerErrors       int64         `json:"server_errors"`
	ServerErrorPercent float64       `json:"server_error_percent"`
	TopRoutes          []RouteCount  `json:"top_routes"`
	SlowRoutes         []RouteTiming `json:"slow_routes"`
	TopIPs             []IPCount     `json:"top_ips"`
	ByMinute           []MinuteCount `json:"by_minute"`
}

type RouteCount struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Requests int64  `json:"requests"`
}

type RouteTiming struct {
	Method         string  `json:"method"`
	Path           string  `json:"path"`
	Requests       int64   `json:"requests"`
	AvgRequestTime float64 `json:"avg_request_time"`
}

type IPCount struct {
	IP       string `json:"ip"`
	Requests int64  `json:"requests"`
}

type MinuteCount struct {
	Time         string `json:"time"`
	Requests     int64  `json:"requests"`
	ServerErrors int64  `json:"server_errors"`
}

type entry struct {
	Time        string   `json:"time"`
	IP          string   `json:"ip"`
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Status      int      `json:"status"`
	RequestTime *float64 `json:"request_time"`
}

type routeKey struct {
	Method string
	Path   string
}

type routeAggregate struct {
	Requests int64
	Total    float64
}

type minuteAggregate struct {
	Requests     int64
	ServerErrors int64
}

func Analyze(r io.Reader, maxLineBytes int) (Report, error) {
	if maxLineBytes <= 0 {
		return Report{}, fmt.Errorf("max line length must be positive")
	}
	routes := make(map[routeKey]routeAggregate)
	ips := make(map[string]int64)
	minutes := make(map[time.Time]minuteAggregate)
	report := emptyReport()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, min(maxLineBytes, 64*1024)), maxLineBytes)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var item entry
		if err := json.Unmarshal(line, &item); err != nil {
			return Report{}, fmt.Errorf("line %d: invalid JSON: %w", lineNumber, err)
		}
		parsedTime, err := validate(item)
		if err != nil {
			return Report{}, fmt.Errorf("line %d: %w", lineNumber, err)
		}

		report.TotalRequests++
		serverError := item.Status >= 500
		if serverError {
			report.ServerErrors++
		}
		key := routeKey{Method: item.Method, Path: item.Path}
		route := routes[key]
		route.Requests++
		route.Total += *item.RequestTime
		routes[key] = route
		ips[item.IP]++
		minuteKey := parsedTime.UTC().Truncate(time.Minute)
		minute := minutes[minuteKey]
		minute.Requests++
		if serverError {
			minute.ServerErrors++
		}
		minutes[minuteKey] = minute
	}
	if err := scanner.Err(); err != nil {
		return Report{}, fmt.Errorf("line %d: line exceeds %d bytes or cannot be read: %w", lineNumber+1, maxLineBytes, err)
	}

	if report.TotalRequests > 0 {
		report.ServerErrorPercent = float64(report.ServerErrors) * 100 / float64(report.TotalRequests)
	}
	report.TopRoutes, report.SlowRoutes = routeLists(routes)
	report.TopIPs = ipList(ips)
	report.ByMinute = minuteList(minutes)
	return report, nil
}

func validate(item entry) (time.Time, error) {
	parsedTime, err := time.Parse(time.RFC3339, item.Time)
	if err != nil {
		return time.Time{}, fmt.Errorf("time must be RFC 3339")
	}
	if net.ParseIP(item.IP) == nil {
		return time.Time{}, fmt.Errorf("ip is invalid")
	}
	if strings.TrimSpace(item.Method) == "" {
		return time.Time{}, fmt.Errorf("method is empty")
	}
	if item.Path == "" || !strings.HasPrefix(item.Path, "/") || strings.Contains(item.Path, "?") {
		return time.Time{}, fmt.Errorf("path must start with / and contain no query")
	}
	if item.Status < 100 || item.Status > 599 {
		return time.Time{}, fmt.Errorf("status must be between 100 and 599")
	}
	if item.RequestTime == nil {
		return time.Time{}, fmt.Errorf("request_time is required")
	}
	if *item.RequestTime < 0 || math.IsNaN(*item.RequestTime) || math.IsInf(*item.RequestTime, 0) {
		return time.Time{}, fmt.Errorf("request_time must be a nonnegative finite number")
	}
	return parsedTime, nil
}

func emptyReport() Report {
	return Report{
		TopRoutes:  []RouteCount{},
		SlowRoutes: []RouteTiming{},
		TopIPs:     []IPCount{},
		ByMinute:   []MinuteCount{},
	}
}

func routeLists(routes map[routeKey]routeAggregate) ([]RouteCount, []RouteTiming) {
	counts := make([]RouteCount, 0, len(routes))
	timings := make([]RouteTiming, 0, len(routes))
	for key, aggregate := range routes {
		counts = append(counts, RouteCount{Method: key.Method, Path: key.Path, Requests: aggregate.Requests})
		timings = append(timings, RouteTiming{Method: key.Method, Path: key.Path, Requests: aggregate.Requests, AvgRequestTime: aggregate.Total / float64(aggregate.Requests)})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Requests != counts[j].Requests {
			return counts[i].Requests > counts[j].Requests
		}
		return routeLess(counts[i].Method, counts[i].Path, counts[j].Method, counts[j].Path)
	})
	sort.Slice(timings, func(i, j int) bool {
		if timings[i].AvgRequestTime != timings[j].AvgRequestTime {
			return timings[i].AvgRequestTime > timings[j].AvgRequestTime
		}
		return routeLess(timings[i].Method, timings[i].Path, timings[j].Method, timings[j].Path)
	})
	return firstTen(counts), firstTen(timings)
}

func routeLess(leftMethod, leftPath, rightMethod, rightPath string) bool {
	if leftMethod != rightMethod {
		return leftMethod < rightMethod
	}
	return leftPath < rightPath
}

func ipList(ips map[string]int64) []IPCount {
	result := make([]IPCount, 0, len(ips))
	for ip, requests := range ips {
		result = append(result, IPCount{IP: ip, Requests: requests})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Requests != result[j].Requests {
			return result[i].Requests > result[j].Requests
		}
		return result[i].IP < result[j].IP
	})
	return firstTen(result)
}

func minuteList(minutes map[time.Time]minuteAggregate) []MinuteCount {
	keys := make([]time.Time, 0, len(minutes))
	for minute := range minutes {
		keys = append(keys, minute)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	result := make([]MinuteCount, 0, len(keys))
	for _, minute := range keys {
		aggregate := minutes[minute]
		result = append(result, MinuteCount{Time: minute.Format(time.RFC3339), Requests: aggregate.Requests, ServerErrors: aggregate.ServerErrors})
	}
	return result
}

func firstTen[T any](items []T) []T {
	if len(items) > 10 {
		return items[:10]
	}
	return items
}

// Command resultdemo prints the netbackup result table using mock data,
// so the table's look (and its color output in a real terminal) can be
// checked without running an actual backup. Build it the same offline,
// vendor-only way as the main binary:
//
//	go build -mod=vendor -o bin/resultdemo ./cmd/resultdemo
//	./bin/resultdemo
package main

import "netbackup/internal/report"

func main() {
	report.PrintSummaryTable(report.MockResults())
}

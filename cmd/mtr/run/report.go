package run

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"
)

type Reporter struct {
	db *DB
}

func NewReporter() *Reporter {
	db, err := NewDB()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize database: %v\n", err)
	}
	return &Reporter{db: db}
}

func (r *Reporter) Report(testName, since string) error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	defer r.db.Close()

	var results []TestResult
	var err error

	if testName != "" {
		results, err = r.db.GetByTestName(testName, "v1")
	} else if since != "" {
		results, err = r.db.GetSince(since)
	} else {
		results, err = r.db.GetAll(100)
	}

	if err != nil {
		return fmt.Errorf("failed to fetch results: %w", err)
	}

	if len(results) == 0 {
		fmt.Println("No test results found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 8, 2, ' ', 0)

	fmt.Fprintf(w, "TIMESTAMP\tTEST\tVERSION\tSTATUS\tDURATION\tMEMORY\t\n")
	for _, r := range results {
		status := "PASS"
		if !r.Passed {
			status = "FAIL"
		}
		memStr := "-"
		if r.PeakMemoryKB > 0 {
			memStr = fmt.Sprintf("%dKB", r.PeakMemoryKB)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%dms\t%s\t\n",
			r.Timestamp.Format("2006-01-02 15:04:05"),
			r.TestName,
			r.TestVersion,
			status,
			r.DurationMs,
			memStr,
		)

		if !r.Passed && r.ErrorMsg != "" {
			fmt.Fprintf(os.Stderr, "  Error: %s\n", r.ErrorMsg)
		}
	}

	w.Flush()

	return nil
}

func (r *Reporter) Stats() error {
	if r.db == nil {
		return fmt.Errorf("database not available")
	}
	defer r.db.Close()

	total, passed, failed, avgDuration, err := r.db.GetStats()
	if err != nil {
		return fmt.Errorf("failed to fetch stats: %w", err)
	}

	if total == 0 {
		fmt.Println("No test results in database.")
		return nil
	}

	passRate := float64(passed) / float64(total) * 100

	fmt.Println("=== Test Statistics ===")
	fmt.Printf("Total tests run:  %d\n", total)
	fmt.Printf("Passed:          %d (%.1f%%)\n", passed, passRate)
	fmt.Printf("Failed:          %d\n", failed)
	fmt.Printf("Average duration: %dms\n", avgDuration)

	if passRate < 100 {
		fmt.Println("\nRecent failures:")
		results, err := r.db.GetAll(10)
		if err == nil {
			for _, r := range results {
				if !r.Passed {
					fmt.Printf("  [%s] %s: %s\n",
						r.Timestamp.Format("2006-01-02 15:04"),
						r.TestName,
						r.ErrorMsg,
					)
				}
			}
		}
	}

	return nil
}

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	if d < time.Second {
		return fmt.Sprintf("%dms", ms)
	}
	return d.Round(time.Millisecond).String()
}

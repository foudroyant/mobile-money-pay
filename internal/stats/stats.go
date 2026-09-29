// Package stats computes per-merchant sales aggregates directly in SQL
// (via app.DB()) rather than loading transaction records into Go and
// summing them — the totals shown here are meant to be exact revenue
// figures, not bounded by the ~200-row pagination cap used by the list
// views elsewhere in internal/handlers.
package stats

import (
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// DayPoint is one bucket in the evolution chart: validated revenue for a
// single calendar day (zero if the merchant had no validated sale that
// day, so the chart shows gaps rather than skipping them).
type DayPoint struct {
	Date   string // "2026-07-20", stable key
	Label  string // "20/07", for display
	Amount float64
}

// Summary is a merchant's all-time totals plus a recent daily breakdown.
type Summary struct {
	TotalRevenue   float64 // sum of amount over all "validated" transactions, all-time
	ValidatedCount int
	PendingCount   int // status = "submitted", awaiting an admin decision
	RejectedCount  int
	Days           []DayPoint // chronological, oldest first
}

type statusAggregate struct {
	Status string  `db:"status"`
	Total  float64 `db:"total"`
	Count  int     `db:"count"`
}

type dayAggregate struct {
	Day   string  `db:"day"`
	Total float64 `db:"total"`
}

// ForMerchant computes a merchant's sales summary, with the daily
// breakdown covering the last `days` calendar days (inclusive of today).
func ForMerchant(app core.App, merchantID string, days int) (Summary, error) {
	var totals []statusAggregate
	err := app.DB().NewQuery(`
		SELECT status, COALESCE(SUM(amount), 0) AS total, COUNT(*) AS count
		FROM transactions
		WHERE merchant = {:merchant}
		GROUP BY status
	`).Bind(dbx.Params{"merchant": merchantID}).All(&totals)
	if err != nil {
		return Summary{}, err
	}

	var summary Summary
	for _, t := range totals {
		switch t.Status {
		case "validated":
			summary.TotalRevenue = t.Total
			summary.ValidatedCount = t.Count
		case "submitted":
			summary.PendingCount = t.Count
		case "rejected":
			summary.RejectedCount = t.Count
		}
	}

	since := time.Now().UTC().AddDate(0, 0, -days+1)
	startOfDay := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, time.UTC)

	var dayRows []dayAggregate
	err = app.DB().NewQuery(`
		SELECT date(resolved_at) AS day, COALESCE(SUM(amount), 0) AS total
		FROM transactions
		WHERE merchant = {:merchant} AND status = 'validated' AND resolved_at >= {:since}
		GROUP BY date(resolved_at)
	`).Bind(dbx.Params{
		"merchant": merchantID,
		"since":    startOfDay.Format("2006-01-02 15:04:05.000Z"),
	}).All(&dayRows)
	if err != nil {
		return Summary{}, err
	}

	byDay := make(map[string]float64, len(dayRows))
	for _, r := range dayRows {
		byDay[r.Day] = r.Total
	}

	summary.Days = make([]DayPoint, days)
	for i := 0; i < days; i++ {
		d := startOfDay.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		summary.Days[i] = DayPoint{
			Date:   key,
			Label:  d.Format("02/01"),
			Amount: byDay[key],
		}
	}

	return summary, nil
}

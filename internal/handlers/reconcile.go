package handlers

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"momopay/internal/realtime"
	"momopay/internal/session"
	"momopay/internal/txn"
)

const confirmedRefsCollection = "confirmed_refs"

// DefaultReconcileInterval is used when RECONCILE_INTERVAL_SECONDS is unset
// or invalid.
const DefaultReconcileInterval = 120 * time.Second

// ReconcileInterval reads RECONCILE_INTERVAL_SECONDS from the environment,
// falling back to DefaultReconcileInterval.
func ReconcileInterval() time.Duration {
	raw := os.Getenv("RECONCILE_INTERVAL_SECONDS")
	if raw == "" {
		return DefaultReconcileInterval
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 {
		return DefaultReconcileInterval
	}
	return time.Duration(seconds) * time.Second
}

// sweepIntervalLabel renders ReconcileInterval() for display on the
// reconciliation page (e.g. "2 min", "45 s").
func sweepIntervalLabel() string {
	d := ReconcileInterval()
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d.Seconds()))
	}
	return fmt.Sprintf("%d min", int(d.Minutes()))
}

// registerAdminReconcileRoutes adds the reconciliation tool under the
// already-authenticated /admin group: paste one or more operator-confirmed
// transaction IDs, get matched against "submitted" transactions immediately,
// and anything left over is retried by the periodic sweep (StartReconcileLoop).
func registerAdminReconcileRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App, hub *realtime.Hub, store *session.Store) {
	g.GET("/reconcile", adminReconcilePage(app, store))
	g.GET("/reconcile/stream", adminReconcileStream(hub))
	g.POST("/reconcile", adminAddConfirmedRefs(app))
	g.POST("/reconcile/{id}/delete", adminDeleteConfirmedRef(app))
}

func adminReconcilePage(app core.App, store *session.Store) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		refs, err := app.FindRecordsByFilter(confirmedRefsCollection, "status = 'pending'", "-created", 200, 0)
		if err != nil {
			return e.InternalServerError("Failed to load pending references.", err)
		}
		rows := make([]ReconcileRow, len(refs))
		for i, r := range refs {
			rows[i] = reconcileRowData(r)
		}

		data := struct {
			Layout
			Rows               []ReconcileRow
			SweepIntervalLabel string
		}{
			Layout:             Layout{Title: "Rapprochement", CSRFToken: store.CSRFToken(e.Request)},
			Rows:               rows,
			SweepIntervalLabel: sweepIntervalLabel(),
		}

		return renderPage(e, "admin_reconcile", data)
	}
}

func adminReconcileStream(hub *realtime.Hub) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return serveSSE(e, hub, "reconcile")
	}
}

// adminAddConfirmedRefs accepts one operator reference per line (or
// comma-separated), records each as a pending confirmation, and tries to
// match it immediately. The response is discarded (hx-swap="none") — new
// and matched-away rows both reach the page through the "reconcile" SSE
// broadcast (see BindReconcileHooks), same convention as the rest of the
// admin dashboard.
func adminAddConfirmedRefs(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		raw := e.Request.FormValue("refs")
		adminEmail := e.Auth.GetString("email")

		collection, err := app.FindCollectionByNameOrId(confirmedRefsCollection)
		if err != nil {
			return e.InternalServerError("Failed to load collection.", err)
		}

		for _, ref := range splitRefs(raw) {
			existing, err := app.FindFirstRecordByFilter(
				confirmedRefsCollection, "operator_ref = {:r}", dbx.Params{"r": ref},
			)
			if err == nil && existing != nil {
				continue // already added (pending or already matched) — skip duplicate
			}

			record := core.NewRecord(collection)
			record.Set("operator_ref", ref)
			record.Set("status", "pending")
			record.Set("added_by_email", adminEmail)
			if err := app.Save(record); err != nil {
				continue
			}

			tryMatch(app, record)
		}

		return e.NoContent(http.StatusCreated)
	}
}

func adminDeleteConfirmedRef(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := app.FindRecordById(confirmedRefsCollection, e.Request.PathValue("id"))
		if err != nil {
			return e.NotFoundError("Référence introuvable.", nil)
		}
		if err := app.Delete(record); err != nil {
			return e.InternalServerError("Failed to delete.", err)
		}
		return e.NoContent(http.StatusOK)
	}
}

// splitRefs parses a textarea's worth of pasted operator IDs, one per line
// or comma-separated, trimming blanks and de-duplicating within the batch.
func splitRefs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ','
	})

	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		ref := strings.TrimSpace(f)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	return out
}

// tryMatch looks for a "submitted" transaction matching ref's operator_ref
// and, if found, validates it and marks ref as matched. Returns whether a
// match was made. Used both for the immediate on-add check and the
// periodic sweep below — the two orderings (payer submits first vs.
// operator confirmation added first) both end up here.
func tryMatch(app core.App, ref *core.Record) bool {
	transaction, err := txn.FindSubmittedByRef(app, ref.GetString("operator_ref"))
	if err != nil {
		return false
	}

	adminEmail := ref.GetString("added_by_email")
	if err := txn.Resolve(app, transaction, true, adminEmail, "Rapprochement automatique (ID confirmé)"); err != nil {
		return false
	}
	dispatchWebhook(app, transaction)

	ref.Set("status", "matched")
	ref.Set("matched_transaction", transaction.Id)
	if err := app.Save(ref); err != nil {
		log.Printf("reconcile: matched transaction %s but failed to update reference %s: %v", transaction.Id, ref.Id, err)
	}

	return true
}

// StartReconcileLoop periodically retries every still-pending confirmed
// reference against newly submitted transactions, so an operator
// confirmation added before the payer actually submits their reference
// still gets matched once they do. Runs for the lifetime of the process;
// call once at startup.
func StartReconcileLoop(app core.App, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			sweepReconcile(app)
		}
	}()
}

func sweepReconcile(app core.App) {
	refs, err := app.FindRecordsByFilter(confirmedRefsCollection, "status = 'pending'", "-created", 500, 0)
	if err != nil {
		log.Println("reconcile sweep: failed to list pending references:", err)
		return
	}

	matched := 0
	for _, ref := range refs {
		if tryMatch(app, ref) {
			matched++
		}
	}
	if matched > 0 {
		log.Printf("reconcile sweep: matched %d of %d pending reference(s)", matched, len(refs))
	}
}

// Package txn centralizes the transaction state machine so the REST API,
// the merchant dashboard, the admin back office and the public payment page
// all create/submit/resolve transactions the exact same way.
package txn

import (
	"encoding/json"
	"errors"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	StatusPending   = "pending"
	StatusSubmitted = "submitted"
	StatusValidated = "validated"
	StatusRejected  = "rejected"
)

var (
	ErrNotSubmittable = errors.New("txn: transaction is not open for a new submission")
	ErrNotResolvable  = errors.New("txn: transaction is not awaiting a decision")
)

const CollectionName = "transactions"

// CreateFromLink starts a new pending payment attempt against a payment
// link. amount/currency/description/operator/mode/merchant are copied from
// the link at this moment (a snapshot), so the transaction's record of what
// was paid stays accurate even if the link is edited or revoked later.
func CreateFromLink(app core.App, link *core.Record) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("payment_link", link.Id)
	record.Set("merchant", link.GetString("merchant"))
	record.Set("amount", link.GetFloat("amount"))
	record.Set("currency", link.GetString("currency"))
	record.Set("description", link.GetString("title"))
	record.Set("operator", link.GetString("operator"))
	record.Set("mode", link.GetString("mode"))
	record.Set("status", StatusPending)
	if meta := Metadata(link); meta != nil {
		record.Set("metadata", meta)
	}

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

// Submit records the payer's phone number and the operator transaction
// reference. Allowed from "pending" (first submission) and "rejected"
// (payer corrects and retries) — not from "submitted" or "validated".
func Submit(app core.App, record *core.Record, msisdn, operatorRef string) error {
	status := record.GetString("status")
	if status != StatusPending && status != StatusRejected {
		return ErrNotSubmittable
	}

	record.Set("payer_msisdn", msisdn)
	record.Set("payer_operator_ref", operatorRef)
	record.Set("status", StatusSubmitted)
	record.Set("submitted_at", types.NowDateTime())
	record.Set("resolution_note", "")

	return app.Save(record)
}

// Resolve validates or rejects a submitted transaction.
func Resolve(app core.App, record *core.Record, approve bool, adminEmail, note string) error {
	if record.GetString("status") != StatusSubmitted {
		return ErrNotResolvable
	}

	if approve {
		record.Set("status", StatusValidated)
	} else {
		record.Set("status", StatusRejected)
	}
	record.Set("resolved_by_email", adminEmail)
	record.Set("resolved_at", types.NowDateTime())
	record.Set("resolution_note", note)

	return app.Save(record)
}

// FindSubmittedByRef returns the first "submitted" transaction whose
// payer_operator_ref matches ref (used by the reconciliation tool — see
// internal/handlers/reconcile.go). Returns an error (no rows) if none
// matches yet, which is the expected/common case, not a failure.
func FindSubmittedByRef(app core.App, ref string) (*core.Record, error) {
	return app.FindFirstRecordByFilter(
		CollectionName,
		"status = {:status} && payer_operator_ref = {:ref}",
		dbx.Params{"status": StatusSubmitted, "ref": ref},
	)
}

// Metadata returns the merchant-supplied passthrough metadata as raw JSON,
// or nil if none was set.
func Metadata(r *core.Record) json.RawMessage {
	raw, ok := r.GetRaw("metadata").(types.JSONRaw)
	if !ok || len(raw) == 0 {
		return nil
	}
	return json.RawMessage(raw)
}

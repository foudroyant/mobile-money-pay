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

	"momopay/internal/payid"
)

const (
	StatusPending   = "pending"
	StatusSubmitted = "submitted"
	StatusValidated = "validated"
	StatusRejected  = "rejected"
)

const (
	OperatorAirtel = "airtel"
	OperatorMTN    = "mtn"
)

const (
	ModeLive = "live"
	ModeTest = "test"
)

const (
	CreatedByAPI      = "api"
	CreatedByMerchant = "merchant"
	CreatedByAdmin    = "admin"
)

var (
	ErrOperatorNotSupported = errors.New("txn: operator not supported yet")
	ErrNotSubmittable       = errors.New("txn: transaction is not open for a new submission")
	ErrNotResolvable        = errors.New("txn: transaction is not awaiting a decision")
)

const CollectionName = "transactions"

type CreateParams struct {
	MerchantID  string
	Amount      float64
	Description string
	Metadata    types.JSONRaw
	SuccessURL  string
	CancelURL   string
	Mode        string
	CreatedBy   string
}

// Create makes a new pending transaction with a fresh public payment token.
// Operator is always "airtel" for now (MTN is not wired up yet).
func Create(app core.App, p CreateParams) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil, err
	}

	token, err := payid.GeneratePaymentToken()
	if err != nil {
		return nil, err
	}

	record := core.NewRecord(collection)
	record.Set("merchant", p.MerchantID)
	record.Set("token", token)
	record.Set("amount", p.Amount)
	record.Set("currency", "XAF")
	record.Set("operator", OperatorAirtel)
	record.Set("description", p.Description)
	if len(p.Metadata) > 0 {
		record.Set("metadata", p.Metadata)
	}
	record.Set("success_url", p.SuccessURL)
	record.Set("cancel_url", p.CancelURL)
	record.Set("mode", p.Mode)
	record.Set("created_by", p.CreatedBy)
	record.Set("status", StatusPending)

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

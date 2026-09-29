// Package paylink centralizes the payment link ("product") state machine:
// merchants create a reusable link once (title, description, image, fixed
// amount), and any number of payers can pay against it via /pay/{token}.
// Each payment attempt is recorded separately in the transactions
// collection (see internal/txn) — the link itself only tracks whether it is
// currently payable.
package paylink

import (
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	"momopay/internal/payid"
)

const (
	StatusPublished   = "published"
	StatusUnpublished = "unpublished"
	StatusRevoked     = "revoked"
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
	ErrOperatorNotSupported = errors.New("paylink: operator not supported yet")
	ErrRevoked              = errors.New("paylink: link has been revoked and cannot change status")
)

const CollectionName = "payment_links"

type CreateParams struct {
	MerchantID  string
	Title       string
	Description string
	Amount      float64
	Metadata    types.JSONRaw
	SuccessURL  string
	CancelURL   string
	Mode        string
	CreatedBy   string
	Image       *filesystem.File
}

// Create makes a new published payment link with a fresh public token.
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
	record.Set("title", p.Title)
	record.Set("description", p.Description)
	record.Set("amount", p.Amount)
	record.Set("currency", "XAF")
	record.Set("operator", OperatorAirtel)
	record.Set("mode", p.Mode)
	record.Set("status", StatusPublished)
	record.Set("created_by", p.CreatedBy)
	record.Set("success_url", p.SuccessURL)
	record.Set("cancel_url", p.CancelURL)
	if len(p.Metadata) > 0 {
		record.Set("metadata", p.Metadata)
	}
	if p.Image != nil {
		record.Set("image", p.Image)
	}

	if err := app.Save(record); err != nil {
		return nil, err
	}

	return record, nil
}

// SetStatus transitions a link between published/unpublished/revoked.
// Revoked is terminal: once revoked, a link can never be published or
// unpublished again (it must stay permanently hidden from /pay/{token}).
func SetStatus(app core.App, record *core.Record, status string) error {
	if record.GetString("status") == StatusRevoked && status != StatusRevoked {
		return ErrRevoked
	}

	record.Set("status", status)
	return app.Save(record)
}

// IsUsable reports whether a link currently accepts payments.
func IsUsable(record *core.Record) bool {
	return record.GetString("status") == StatusPublished
}

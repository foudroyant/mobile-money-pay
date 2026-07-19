package handlers

import (
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/txn"
)

// Layout carries the fields every full page needs regardless of its
// specific content (browser tab title, and the CSRF token embedded once as
// `hx-headers` on <body> for logged-in pages — see session.ValidateCSRF).
// Page-specific structs embed Layout so templates can access both the
// common chrome fields and their own fields through the same ".".
type Layout struct {
	Title     string
	CSRFToken string
}

// TxnRow is the view model for one row in the admin/merchant transaction
// tables (and for the SSE partial used to update/insert that row live).
type TxnRow struct {
	ID             string
	MerchantName   string
	Amount         float64
	Description    string
	Status         string
	Operator       string
	Mode           string
	PayerMSISDN    string
	PayerRef       string
	Created        string
	ResolutionNote string
	PaymentURL     string
	CanResolve     bool
}

func txnRowData(app core.App, r *core.Record, canResolve bool) TxnRow {
	merchantName := ""
	if merchant, err := app.FindRecordById("merchants", r.GetString("merchant")); err == nil {
		merchantName = merchant.GetString("name")
	}

	return TxnRow{
		ID:             r.Id,
		MerchantName:   merchantName,
		Amount:         r.GetFloat("amount"),
		Description:    r.GetString("description"),
		Status:         r.GetString("status"),
		Operator:       r.GetString("operator"),
		Mode:           r.GetString("mode"),
		PayerMSISDN:    r.GetString("payer_msisdn"),
		PayerRef:       r.GetString("payer_operator_ref"),
		Created:        r.GetDateTime("created").Time().Format("02/01 15:04"),
		ResolutionNote: r.GetString("resolution_note"),
		PaymentURL:     paymentURL(r.GetString("token")),
		CanResolve:     canResolve,
	}
}

// paymentURL builds the absolute /pay/{token} link shown to merchants/admins
// so they have something to actually copy and send to their customer. Row
// rendering also happens from SSE hooks with no incoming request to derive
// a host from (unlike api.go's baseURL), so this always relies on
// APP_BASE_URL, falling back to a same-origin relative link — which still
// works when clicked from the dashboard, just isn't a shareable absolute
// URL until APP_BASE_URL is set (see AGENTS.md).
func paymentURL(token string) string {
	base := strings.TrimSuffix(os.Getenv("APP_BASE_URL"), "/")
	return base + "/pay/" + token
}

// PayStatus is the view model for the public hosted payment page and its
// live-updating status partial.
type PayStatus struct {
	Token          string
	MerchantName   string
	Amount         float64
	Description    string
	Status         string
	PayNumber      string
	PayerMSISDN    string
	PayerRef       string
	ResolutionNote string
	SuccessURL     string
	Locked         bool
}

func payStatusData(app core.App, r *core.Record) PayStatus {
	merchantName := ""
	if merchant, err := app.FindRecordById("merchants", r.GetString("merchant")); err == nil {
		merchantName = merchant.GetString("name")
	}

	status := r.GetString("status")

	return PayStatus{
		Token:          r.GetString("token"),
		MerchantName:   merchantName,
		Amount:         r.GetFloat("amount"),
		Description:    r.GetString("description"),
		Status:         status,
		PayNumber:      airtelPayNumber(),
		PayerMSISDN:    r.GetString("payer_msisdn"),
		PayerRef:       r.GetString("payer_operator_ref"),
		ResolutionNote: r.GetString("resolution_note"),
		SuccessURL:     r.GetString("success_url"),
		Locked:         status == txn.StatusSubmitted || status == txn.StatusValidated,
	}
}

// MerchantOption is one entry in the "create payment link" merchant picker
// shown to admins, and one row of the admin's merchant management list.
type MerchantOption struct {
	ID        string
	Name      string
	Email     string
	Status    string
	KeyPrefix string
}

func merchantOptionData(r *core.Record) MerchantOption {
	return MerchantOption{
		ID:        r.Id,
		Name:      r.GetString("name"),
		Email:     r.GetString("email"),
		Status:    r.GetString("status"),
		KeyPrefix: r.GetString("api_key_prefix"),
	}
}

func airtelPayNumber() string {
	if v := os.Getenv("AIRTEL_PAY_NUMBER"); v != "" {
		return v
	}
	return "(numéro Airtel Money à configurer — variable d'environnement AIRTEL_PAY_NUMBER)"
}

// ReconcileRow is the view model for one pending row in the admin
// reconciliation tool (see internal/handlers/reconcile.go).
type ReconcileRow struct {
	ID           string
	OperatorRef  string
	AddedByEmail string
	Created      string
}

func reconcileRowData(r *core.Record) ReconcileRow {
	return ReconcileRow{
		ID:           r.Id,
		OperatorRef:  r.GetString("operator_ref"),
		AddedByEmail: r.GetString("added_by_email"),
		Created:      r.GetDateTime("created").Time().Format("02/01 15:04"),
	}
}

package handlers

import (
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/stats"
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
// Transactions no longer carry their own token — the payment link they
// were made against does — so the row's link is looked up once here.
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

	token := ""
	if link, err := app.FindRecordById("payment_links", r.GetString("payment_link")); err == nil {
		token = link.GetString("token")
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
		PaymentURL:     paymentURL(token),
		CanResolve:     canResolve,
	}
}

// LinkRow is the view model for one row in the admin/merchant payment link
// ("product") tables, and for the SSE partial used to update/insert that
// row live.
type LinkRow struct {
	ID           string
	MerchantName string
	Title        string
	Description  string
	ImageURL     string
	Amount       float64
	Status       string
	Operator     string
	Mode         string
	PaymentURL   string
	Created      string
	CanManage    bool
}

func linkRowData(app core.App, r *core.Record, canManage bool) LinkRow {
	merchantName := ""
	if merchant, err := app.FindRecordById("merchants", r.GetString("merchant")); err == nil {
		merchantName = merchant.GetString("name")
	}

	token := r.GetString("token")
	imageURL := ""
	if r.GetString("image") != "" {
		imageURL = "/pay/" + token + "/image"
	}

	return LinkRow{
		ID:           r.Id,
		MerchantName: merchantName,
		Title:        r.GetString("title"),
		Description:  r.GetString("description"),
		ImageURL:     imageURL,
		Amount:       r.GetFloat("amount"),
		Status:       r.GetString("status"),
		Operator:     r.GetString("operator"),
		Mode:         r.GetString("mode"),
		PaymentURL:   paymentURL(token),
		Created:      r.GetDateTime("created").Time().Format("02/01 15:04"),
		CanManage:    canManage,
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

// PayLinkView is the view model for the "product" card and initial payment
// form shown on GET /pay/{token}, sourced from the payment link itself —
// there is no transaction yet at this point, since a payment link can be
// paid by any number of different payers.
// ProductTitle (not "Title") to avoid colliding with the promoted
// Layout.Title field when both are embedded together in payShow's page
// data — html/template can't resolve an ambiguous promoted field name.
type PayLinkView struct {
	Token        string
	MerchantName string
	ProductTitle string
	Description  string
	ImageURL     string
	Amount       float64
	PayNumber    string
}

func payLinkViewData(app core.App, link *core.Record) PayLinkView {
	merchantName := ""
	if merchant, err := app.FindRecordById("merchants", link.GetString("merchant")); err == nil {
		merchantName = merchant.GetString("name")
	}

	token := link.GetString("token")
	imageURL := ""
	if link.GetString("image") != "" {
		imageURL = "/pay/" + token + "/image"
	}

	return PayLinkView{
		Token:        token,
		MerchantName: merchantName,
		ProductTitle: link.GetString("title"),
		Description:  link.GetString("description"),
		ImageURL:     imageURL,
		Amount:       link.GetFloat("amount"),
		PayNumber:    airtelPayNumber(),
	}
}

// PayStatus is the view model for one payment attempt's live-updating
// status partial, shown after a payer submits the form on /pay/{token}.
// Scoped by TransactionID (not Token, which is shared across every payer
// using the same link) so the SSE stream only reflects this one attempt.
type PayStatus struct {
	Token          string
	TransactionID  string
	Amount         float64
	Description    string
	Status         string
	PayNumber      string
	PayerMSISDN    string
	PayerRef       string
	ResolutionNote string
	SuccessURL     string
}

func payStatusData(app core.App, r *core.Record) PayStatus {
	token := ""
	successURL := ""
	if link, err := app.FindRecordById("payment_links", r.GetString("payment_link")); err == nil {
		token = link.GetString("token")
		successURL = link.GetString("success_url")
	}

	return PayStatus{
		Token:          token,
		TransactionID:  r.Id,
		Amount:         r.GetFloat("amount"),
		Description:    r.GetString("description"),
		Status:         r.GetString("status"),
		PayNumber:      airtelPayNumber(),
		PayerMSISDN:    r.GetString("payer_msisdn"),
		PayerRef:       r.GetString("payer_operator_ref"),
		ResolutionNote: r.GetString("resolution_note"),
		SuccessURL:     successURL,
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

// StatsBar is one bar in the sales evolution chart.
type StatsBar struct {
	Label     string
	Amount    float64
	HeightPct float64 // 0-100, relative to the busiest day in the window
}

// StatsView is the view model for a merchant's sales stats page, shown to
// admins (any merchant) and to the merchant themselves — see
// internal/stats for how the underlying numbers are computed.
// Deliberately no "MerchantTitle"-shaped ambiguity risk: no field here is
// named "Title" (see the note in AGENTS.md about Layout/Title collisions).
type StatsView struct {
	MerchantName   string
	TotalRevenue   float64
	ValidatedCount int
	PendingCount   int
	RejectedCount  int
	Days           []StatsBar
}

func statsViewData(merchantName string, s stats.Summary) StatsView {
	maxAmount := 0.0
	for _, d := range s.Days {
		if d.Amount > maxAmount {
			maxAmount = d.Amount
		}
	}

	bars := make([]StatsBar, len(s.Days))
	for i, d := range s.Days {
		heightPct := 0.0
		if maxAmount > 0 {
			heightPct = d.Amount / maxAmount * 100
		}
		bars[i] = StatsBar{Label: d.Label, Amount: d.Amount, HeightPct: heightPct}
	}

	return StatsView{
		MerchantName:   merchantName,
		TotalRevenue:   s.TotalRevenue,
		ValidatedCount: s.ValidatedCount,
		PendingCount:   s.PendingCount,
		RejectedCount:  s.RejectedCount,
		Days:           bars,
	}
}

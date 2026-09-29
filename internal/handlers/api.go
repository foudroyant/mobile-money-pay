package handlers

import (
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"momopay/internal/payid"
	"momopay/internal/paylink"
)

// registerAPIRoutes exposes the Stripe-style merchant REST API under /v1,
// authenticated with a per-merchant Bearer secret key. Payment links are
// the creation primitive (like Stripe Payment Links) — a link is created
// once and can be paid by any number of payers; /v1/transactions is a
// read-only ledger of the resulting payment attempts. Image upload isn't
// exposed over the JSON API in v1 — set it from the /app dashboard.
func registerAPIRoutes(se *core.ServeEvent, app core.App) {
	g := se.Router.Group("/v1")
	g.BindFunc(requireAPIKey(app))

	g.POST("/payment_links", apiCreateLink(app))
	g.GET("/payment_links/{id}", apiGetLink(app))
	g.GET("/payment_links", apiListLinks(app))
	g.POST("/payment_links/{id}/publish", apiSetLinkStatus(app, paylink.StatusPublished))
	g.POST("/payment_links/{id}/unpublish", apiSetLinkStatus(app, paylink.StatusUnpublished))
	g.POST("/payment_links/{id}/revoke", apiSetLinkStatus(app, paylink.StatusRevoked))

	g.GET("/transactions/{id}", apiGetTransaction(app))
	g.GET("/transactions", apiListTransactions(app))
}

type merchantCtx struct {
	Record *core.Record
	Live   bool
}

func requireAPIKey(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		const prefix = "Bearer "
		header := e.Request.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) {
			return e.UnauthorizedError("Missing API key. Send it as \"Authorization: Bearer sk_...\".", nil)
		}
		secret := strings.TrimPrefix(header, prefix)

		merchant, err := app.FindFirstRecordByFilter(
			"merchants",
			"api_key_hash = {:hash}",
			dbx.Params{"hash": payid.HashAPIKey(secret)},
		)
		if err != nil {
			return e.UnauthorizedError("Invalid API key.", nil)
		}
		if merchant.GetString("status") != "active" {
			return e.ForbiddenError("This merchant account is disabled.", nil)
		}

		e.Set("merchantCtx", &merchantCtx{Record: merchant, Live: payid.IsLiveKey(secret)})
		return e.Next()
	}
}

func currentMerchant(e *core.RequestEvent) *merchantCtx {
	ctx, _ := e.Get("merchantCtx").(*merchantCtx)
	return ctx
}

type createLinkRequest struct {
	Title       string        `json:"title"`
	Amount      float64       `json:"amount"`
	Operator    string        `json:"operator"`
	Description string        `json:"description"`
	SuccessURL  string        `json:"success_url"`
	CancelURL   string        `json:"cancel_url"`
	Metadata    types.JSONRaw `json:"metadata"`
}

func apiCreateLink(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		var body createLinkRequest
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("Invalid request body.", err)
		}
		if body.Title == "" {
			return e.BadRequestError("\"title\" is required.", nil)
		}
		if body.Amount <= 0 {
			return e.BadRequestError("\"amount\" must be a positive number.", nil)
		}
		if body.Operator != "" && body.Operator != paylink.OperatorAirtel {
			return e.BadRequestError(
				"MTN Mobile Money arrive bientôt. Utilisez \"operator\": \"airtel\" ou omettez ce champ.",
				nil,
			)
		}

		mode := paylink.ModeTest
		if merchant.Live {
			mode = paylink.ModeLive
		}

		record, err := paylink.Create(app, paylink.CreateParams{
			MerchantID:  merchant.Record.Id,
			Title:       body.Title,
			Description: body.Description,
			Amount:      body.Amount,
			Metadata:    body.Metadata,
			SuccessURL:  body.SuccessURL,
			CancelURL:   body.CancelURL,
			Mode:        mode,
			CreatedBy:   paylink.CreatedByAPI,
		})
		if err != nil {
			return e.InternalServerError("Failed to create payment link.", err)
		}

		return e.JSON(http.StatusCreated, paymentLinkJSON(record, baseURL(e.Request)))
	}
}

func apiGetLink(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		record, err := app.FindRecordById("payment_links", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != merchant.Record.Id {
			return e.NotFoundError("Payment link not found.", nil)
		}

		return e.JSON(http.StatusOK, paymentLinkJSON(record, baseURL(e.Request)))
	}
}

func apiListLinks(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		filter := "merchant = {:merchant}"
		params := dbx.Params{"merchant": merchant.Record.Id}

		if status := e.Request.URL.Query().Get("status"); status != "" {
			filter += " && status = {:status}"
			params["status"] = status
		}

		records, err := app.FindRecordsByFilter("payment_links", filter, "-created", apiLimit(e), 0, params)
		if err != nil {
			return e.InternalServerError("Failed to list payment links.", err)
		}

		base := baseURL(e.Request)
		out := make([]map[string]any, len(records))
		for i, r := range records {
			out[i] = paymentLinkJSON(r, base)
		}

		return e.JSON(http.StatusOK, map[string]any{"data": out})
	}
}

func apiSetLinkStatus(app core.App, status string) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		record, err := app.FindRecordById("payment_links", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != merchant.Record.Id {
			return e.NotFoundError("Payment link not found.", nil)
		}

		if err := paylink.SetStatus(app, record, status); err != nil {
			return e.BadRequestError("This link has been revoked and can no longer change status.", nil)
		}

		return e.JSON(http.StatusOK, paymentLinkJSON(record, baseURL(e.Request)))
	}
}

func apiGetTransaction(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		record, err := app.FindRecordById("transactions", e.Request.PathValue("id"))
		if err != nil || record.GetString("merchant") != merchant.Record.Id {
			return e.NotFoundError("Transaction not found.", nil)
		}

		return e.JSON(http.StatusOK, transactionJSON(app, record, baseURL(e.Request)))
	}
}

func apiListTransactions(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		merchant := currentMerchant(e)

		filter := "merchant = {:merchant}"
		params := dbx.Params{"merchant": merchant.Record.Id}

		if status := e.Request.URL.Query().Get("status"); status != "" {
			filter += " && status = {:status}"
			params["status"] = status
		}
		if linkID := e.Request.URL.Query().Get("payment_link"); linkID != "" {
			filter += " && payment_link = {:link}"
			params["link"] = linkID
		}

		records, err := app.FindRecordsByFilter("transactions", filter, "-created", apiLimit(e), 0, params)
		if err != nil {
			return e.InternalServerError("Failed to list transactions.", err)
		}

		base := baseURL(e.Request)
		out := make([]map[string]any, len(records))
		for i, r := range records {
			out[i] = transactionJSON(app, r, base)
		}

		return e.JSON(http.StatusOK, map[string]any{"data": out})
	}
}

func apiLimit(e *core.RequestEvent) int {
	limit := 20
	if raw := e.Request.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	return limit
}

func paymentLinkJSON(r *core.Record, base string) map[string]any {
	return map[string]any{
		"id":          r.Id,
		"title":       r.GetString("title"),
		"description": r.GetString("description"),
		"status":      r.GetString("status"),
		"amount":      r.GetFloat("amount"),
		"currency":    r.GetString("currency"),
		"operator":    r.GetString("operator"),
		"mode":        r.GetString("mode"),
		"url":         base + "/pay/" + r.GetString("token"),
		"created":     r.GetDateTime("created").String(),
	}
}

func transactionJSON(app core.App, r *core.Record, base string) map[string]any {
	token := ""
	if link, err := app.FindRecordById("payment_links", r.GetString("payment_link")); err == nil {
		token = link.GetString("token")
	}

	return map[string]any{
		"id":           r.Id,
		"payment_link": r.GetString("payment_link"),
		"status":       r.GetString("status"),
		"amount":       r.GetFloat("amount"),
		"currency":     r.GetString("currency"),
		"operator":     r.GetString("operator"),
		"mode":         r.GetString("mode"),
		"description":  r.GetString("description"),
		"payment_url":  base + "/pay/" + token,
		"created":      r.GetDateTime("created").String(),
	}
}

// baseURL resolves the public origin used to build payment_url links and
// webhook payloads. Set APP_BASE_URL in production (e.g. behind a reverse
// proxy) so it doesn't depend on the incoming Host header.
func baseURL(r *http.Request) string {
	if v := os.Getenv("APP_BASE_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

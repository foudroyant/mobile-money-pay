package handlers

import (
	"bytes"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"momopay/internal/view"
)

// renderPage renders a full page and writes it as a 200 OK HTTP response,
// shared by every handler that returns a full (non-fragment) HTML page.
func renderPage(e *core.RequestEvent, name string, data any) error {
	return renderPageStatus(e, name, http.StatusOK, data)
}

// renderPageStatus is renderPage with an explicit status code, for the rare
// case (a failed login) where the page itself doesn't change but the
// response code should reflect the outcome.
func renderPageStatus(e *core.RequestEvent, name string, status int, data any) error {
	var buf bytes.Buffer
	if err := view.Page(&buf, name, data); err != nil {
		return e.InternalServerError("Failed to render page.", err)
	}
	return e.HTML(status, buf.String())
}

// renderDialog renders the "txn_detail" partial into the shared #dialog
// placeholder (see layout.html) — the row-click detail popup.
func renderDialog(e *core.RequestEvent, data TxnRow) error {
	html, err := view.Partial("txn_detail", data)
	if err != nil {
		return e.InternalServerError("Failed to render page.", err)
	}
	return e.HTML(http.StatusOK, html)
}

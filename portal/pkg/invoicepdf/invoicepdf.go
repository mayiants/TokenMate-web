// Package invoicepdf renders issued invoices as PDF files using gofpdf.
// The layout is intentionally minimal (letterhead, meta block, one amount
// line, footer) — enough for a downloadable record, not a designed document.
package invoicepdf

import (
	"bytes"
	"fmt"
	"time"

	"github.com/jung-kurt/gofpdf"
)

// Invoice is the data needed to render an invoice or receipt PDF.
type Invoice struct {
	DocType string // "INVOICE" (default) | "RECEIPT"
	// PaymentMethod (receipts only): e.g. "stripe", "alipay".
	PaymentMethod string
	OrderID       string
	InvoiceNumber string
	IssuedAt      time.Time
	BillTo        string // enterprise or personal title
	TaxNumber     string // registration / tax id, may be empty
	Description   string // e.g. "AI API services"
	AmountUSD     float64
	// GSTPercent is the jurisdiction rate shown as a separate line
	// (Singapore 9%; zero-rated overseas = 0). Total = Amount * (1+p/100).
	GSTPercent      float64
	CustomerCountry string
	PlatformName    string // e.g. "Woodgit"
	PlatformURL     string // e.g. "https://api.woodgit.com"
}

func (inv Invoice) gstUSD() float64   { return inv.AmountUSD * inv.GSTPercent / 100.0 }
func (inv Invoice) totalUSD() float64 { return inv.AmountUSD + inv.gstUSD() }

func (inv Invoice) docType() string {
	if inv.DocType == "" {
		return "INVOICE"
	}
	return inv.DocType
}

// Render builds the PDF and returns its bytes.
func Render(inv Invoice) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.AddPage()

	left := 20.0
	width := 170.0

	// Letterhead
	pdf.SetFont("Helvetica", "B", 20)
	pdf.SetTextColor(59, 130, 246)
	pdf.CellFormat(width, 10, inv.PlatformName, "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
	pdf.SetTextColor(107, 114, 128)
	pdf.CellFormat(width, 5, inv.PlatformURL, "", 1, "L", false, 0, "")
	pdf.Ln(6)

	// Title
	pdf.SetFont("Helvetica", "B", 16)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(width, 10, inv.docType(), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	// Meta block: two columns (invoice meta | bill to)
	y := pdf.GetY()
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(55, 65, 81)
	var meta []struct{ k, v string }
	if inv.docType() == "RECEIPT" {
		meta = []struct{ k, v string }{
			{"Receipt No.", inv.OrderID},
			{"Date", inv.IssuedAt.Format("2006-01-02")},
			{"Payment Method", inv.PaymentMethod},
			{"Currency", "USD"},
		}
	} else {
		meta = []struct{ k, v string }{
			{"Invoice No.", inv.InvoiceNumber},
			{"Issued", inv.IssuedAt.Format("2006-01-02")},
			{"Currency", "USD"},
		}
	}
	for _, m := range meta {
		pdf.SetXY(left, y)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(30, 6, m.k, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.CellFormat(50, 6, m.v, "", 1, "L", false, 0, "")
		y += 6
	}

	billX := left + 95
	y = pdf.GetY() - float64(len(meta))*6
	pdf.SetXY(billX, y)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(75, 6, "Bill To", "", 1, "L", false, 0, "")
	pdf.SetX(billX)
	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(55, 65, 81)
	pdf.MultiCell(75, 6, inv.BillTo, "", "L", false)
	if inv.TaxNumber != "" {
		pdf.SetX(billX)
		pdf.MultiCell(75, 6, "Tax ID: "+inv.TaxNumber, "", "L", false)
	}
	pdf.Ln(10)

	// Amount table (single line item).
	pdf.SetFillColor(249, 250, 251)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(120, 8, " Description", "1", 0, "L", true, 0, "")
	pdf.CellFormat(50, 8, "Amount ", "1", 1, "R", true, 0, "")

	pdf.SetFont("Helvetica", "", 10)
	pdf.SetTextColor(55, 65, 81)
	pdf.CellFormat(120, 8, " "+inv.Description, "LR", 0, "L", false, 0, "")
	pdf.CellFormat(50, 8, fmt.Sprintf("$%.2f ", inv.AmountUSD), "R", 1, "R", false, 0, "")

	// GST line (zero-rated renders as 0.00 for transparency).
	if inv.GSTPercent > 0 || inv.CustomerCountry != "" {
		gstLabel := fmt.Sprintf(" GST (%.0f%%)", inv.GSTPercent)
		if inv.GSTPercent == 0 {
			gstLabel = " GST (0% — zero-rated export)"
		}
		pdf.CellFormat(120, 8, gstLabel, "LR", 0, "L", false, 0, "")
		pdf.CellFormat(50, 8, fmt.Sprintf("$%.2f ", inv.gstUSD()), "R", 1, "R", false, 0, "")
	}

	pdf.SetFont("Helvetica", "B", 11)
	pdf.SetTextColor(17, 24, 39)
	pdf.CellFormat(120, 9, " Total (USD)", "1", 0, "L", true, 0, "")
	pdf.CellFormat(50, 9, fmt.Sprintf("$%.2f ", inv.totalUSD()), "1", 1, "R", true, 0, "")
	pdf.Ln(12)

	// Footer note
	pdf.SetFont("Helvetica", "I", 8)
	pdf.SetTextColor(156, 163, 175)
	footer := "This invoice was generated electronically by " + inv.PlatformName + " and does not require a signature. " +
		"For billing questions, reply to the notification email or contact support via the portal."
	if inv.docType() == "RECEIPT" {
		footer = "This receipt confirms your payment to " + inv.PlatformName + ". " +
			"Keep it for your records; for billing questions contact support via the portal."
	}
	pdf.MultiCell(width, 4.5, footer, "", "L", false)

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("render invoice pdf: %w", err)
	}
	return buf.Bytes(), nil
}

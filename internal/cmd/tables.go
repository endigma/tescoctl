package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/endigma/tescoctl/internal/render"
	"github.com/endigma/tescoctl/internal/view"
)

// productTable renders a product listing. Offers and unavailability share a
// column because a product rarely has both and the terminal is narrow.
func productTable(r *render.Renderer, products []view.Product) string {
	if len(products) == 0 {
		return r.Styles.Muted.Render("no results")
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers("TPNC", "PRODUCT", "PRICE", "UNIT", "NOTES")

	for _, p := range products {
		t.Row(
			p.TPNC,
			render.Truncate(p.Title, 52),
			render.Money(p.Price),
			render.UnitPrice(p.UnitPrice, p.UnitOfMeasure),
			notes(r, p),
		)
	}

	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		switch col {
		case 2:
			return r.Styles.Price.Padding(0, 1)
		case 3:
			return r.Styles.Muted.Padding(0, 1)
		case 4:
			return r.Styles.Offer.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	return t.Render()
}

func notes(r *render.Renderer, p view.Product) string {
	var parts []string
	if p.IsForSale != nil && !*p.IsForSale {
		parts = append(parts, "not for sale")
	}
	parts = append(parts, p.Offers...)
	return render.Truncate(strings.Join(parts, "; "), 34)
}

// categoryTree renders the taxonomy as an indented list. Facets are shown
// because they are what `tescoctl browse` takes.
func categoryTree(r *render.Renderer, cats []view.Category) string {
	var b strings.Builder
	for _, c := range cats {
		b.WriteString(r.Styles.Title.Render(c.Name))
		b.WriteString("  ")
		b.WriteString(r.Styles.Muted.Render(c.Facet))
		b.WriteString("\n")
		for _, child := range c.Children {
			b.WriteString("  ")
			b.WriteString(child.Name)
			b.WriteString("  ")
			b.WriteString(r.Styles.Muted.Render(child.Facet))
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// productDetail renders a single product as a labelled block plus its nutrition
// table.
// formatWeightOption renders one catchweight choice, e.g. "1.8kg — £9.00 (default)".
func formatWeightOption(w view.Weight) string {
	text := strconv.FormatFloat(w.Weight, 'f', -1, 64) + "kg"
	if w.Price != nil {
		text += " — " + render.Money(w.Price)
	}
	if w.Default {
		text += " (default)"
	}
	return text
}

func productDetail(r *render.Renderer, p view.ProductDetail) string {
	var b strings.Builder

	b.WriteString(r.Styles.Title.Render(p.Title))
	b.WriteString("\n")

	rows := [][2]string{
		{"TPNC", p.TPNC},
		{"Brand", p.Brand},
		{"Price", render.Money(p.Price)},
		{"Unit price", render.UnitPrice(p.UnitPrice, p.UnitOfMeasure)},
		{"Pack size", p.PackSize},
		{"Link", p.URL},
	}
	if p.IsForSale != nil && !*p.IsForSale {
		rows = append(rows, [2]string{"For sale", "no"})
	}
	for i, w := range p.Weights {
		label := "Weights"
		if i > 0 {
			label = ""
		}
		rows = append(rows, [2]string{label, formatWeightOption(w)})
	}
	for _, o := range p.Offers {
		rows = append(rows, [2]string{"Offer", o})
	}
	for _, row := range rows {
		if row[1] == "" {
			continue
		}
		b.WriteString(r.Styles.Muted.Render(pad(row[0], 11)))
		b.WriteString(row[1])
		b.WriteString("\n")
	}

	if len(p.Ingredients) > 0 {
		b.WriteString("\n")
		b.WriteString(r.Styles.Header.Render("Ingredients"))
		b.WriteString("\n")
		b.WriteString(strings.Join(p.Ingredients, ", "))
		b.WriteString("\n")
	}

	if len(p.Nutrition) > 1 {
		b.WriteString("\n")
		b.WriteString(nutritionTable(r, p.Nutrition))
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// nutritionTable renders the nutrition rows. Tesco's first row is the column
// header ("Typical Values", "Per 100ml", ...), so it becomes the table header
// rather than a data row. Trailing footnote rows carry no values and would
// otherwise stretch the name column past the width of every real row, so they
// are printed underneath instead.
func nutritionTable(r *render.Renderer, rows []view.Nutrient) string {
	header := rows[0]
	headers := append([]string{header.Name}, header.Values...)

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers(headers...)

	var footnotes []string
	for _, row := range rows[1:] {
		if isFootnote(row) {
			footnotes = append(footnotes, row.Name)
			continue
		}
		t.Row(append([]string{row.Name}, row.Values...)...)
	}
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	out := t.Render()
	for _, note := range footnotes {
		out += "\n" + r.Styles.Muted.Render(note)
	}
	return out
}

// isFootnote reports whether a nutrition row is annotation rather than data.
// Tesco fills the value columns of such rows with "-" or leaves them empty.
func isFootnote(row view.Nutrient) bool {
	for _, v := range row.Values {
		switch strings.TrimSpace(v) {
		case "", "-":
		default:
			return false
		}
	}
	return true
}

func pad(s string, n int) string {
	if len(s) >= n {
		return s + " "
	}
	return s + strings.Repeat(" ", n-len(s))
}

// basketTable renders the trolley plus its total and the checkout link, since
// tescoctl deliberately cannot pay.
func basketTable(r *render.Renderer, b view.Basket) string {
	if len(b.Items) == 0 {
		return r.Styles.Muted.Render("Basket is empty.")
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers("TPNC", "PRODUCT", "QTY", "COST")

	for _, item := range b.Items {
		t.Row(
			item.TPNC,
			render.Truncate(item.Title, 52),
			quantity(item.Quantity, item.Unit),
			render.Money(item.Cost),
		)
	}
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		if col == 3 {
			return r.Styles.Price.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	var out strings.Builder
	out.WriteString(t.Render())
	fmt.Fprintf(&out, "\n%s %s", pad("Guide total", 11), r.Styles.Price.Render(render.Money(b.GuidePrice)))
	if b.InAmend {
		out.WriteString("\n" + r.Styles.Offer.Render("This basket is amending an existing order."))
	}
	fmt.Fprintf(&out, "\n%s %s", pad("Checkout", 11), r.Styles.Muted.Render(b.CheckoutURL))
	return out.String()
}

// orderTable renders an order listing.
func orderTable(r *render.Renderer, orders []view.Order) string {
	if len(orders) == 0 {
		return r.Styles.Muted.Render("no orders")
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers("ORDER", "STATUS", "SLOT", "TOTAL", "ID")

	for _, o := range orders {
		t.Row(o.OrderNo, o.Status, slotWindow(o.Slot), render.Money(o.Total), render.Truncate(o.ID, 24))
	}
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		switch col {
		case 3:
			return r.Styles.Price.Padding(0, 1)
		case 4:
			return r.Styles.Muted.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})
	return t.Render()
}

// orderDetail renders one order with its lines.
func orderDetail(r *render.Renderer, o view.Order) string {
	var b strings.Builder
	b.WriteString(r.Styles.Title.Render("Order " + o.OrderNo))
	b.WriteString("\n")

	for _, row := range [][2]string{
		{"Status", o.Status},
		{"Placed", formatStamp(o.Placed)},
		{"Slot", slotWindow(o.Slot)},
		{"Total", render.Money(o.Total)},
	} {
		if row[1] == "" {
			continue
		}
		b.WriteString(r.Styles.Muted.Render(pad(row[0], 11)))
		b.WriteString(row[1])
		b.WriteString("\n")
	}

	if len(o.Items) == 0 {
		return strings.TrimRight(b.String(), "\n")
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers("TPNC", "PRODUCT", "QTY", "COST")
	for _, item := range o.Items {
		t.Row(item.TPNC, render.Truncate(item.Title, 52), quantity(item.Quantity, ""), render.Money(item.Cost))
	}
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		if col == 3 {
			return r.Styles.Price.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})

	b.WriteString("\n")
	b.WriteString(t.Render())
	return b.String()
}

// slotTable renders delivery windows grouped by day.
func slotTable(r *render.Renderer, slots []view.Slot) string {
	if len(slots) == 0 {
		return r.Styles.Muted.Render("no slots available in that range")
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(r.Styles.Border).
		BorderRow(false).
		BorderColumn(false).
		Headers("DAY", "WINDOW", "CHARGE", "ID")

	for _, s := range slots {
		day, window := splitWindow(s)
		t.Row(day, window, render.Money(s.Charge), render.Truncate(s.ID, 28))
	}
	t.StyleFunc(func(row, col int) lipgloss.Style {
		if row == table.HeaderRow {
			return r.Styles.Header.Padding(0, 1)
		}
		switch col {
		case 2:
			return r.Styles.Price.Padding(0, 1)
		case 3:
			return r.Styles.Muted.Padding(0, 1)
		}
		return lipgloss.NewStyle().Padding(0, 1)
	})
	return t.Render()
}

// splitWindow renders a slot as a day and a local time range.
func splitWindow(s view.Slot) (day, window string) {
	start, err := time.Parse(time.RFC3339, s.Start)
	if err != nil {
		return s.Start, s.End
	}
	end, err := time.Parse(time.RFC3339, s.End)
	if err != nil {
		return start.Local().Format("Mon 2 Jan"), start.Local().Format("15:04")
	}
	return start.Local().Format("Mon 2 Jan"),
		start.Local().Format("15:04") + "–" + end.Local().Format("15:04")
}

// slotWindow renders an order's slot on one line.
func slotWindow(s *view.Slot) string {
	if s == nil || s.Start == "" {
		return ""
	}
	day, window := splitWindow(*s)
	return day + " " + window
}

// formatStamp renders an RFC3339 timestamp locally, leaving anything else
// alone rather than guessing.
func formatStamp(s string) string {
	if s == "" {
		return ""
	}
	when, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return when.Local().Format("Mon 2 Jan 2006, 15:04")
}

// basketCheckReport renders the result of `tescoctl basket check`. A sound basket
// gets one line; problems get one paragraph each, since each carries the tpnc
// and the command needed to act on it.
func basketCheckReport(r *render.Renderer, c view.BasketCheck) string {
	if len(c.Problems) == 0 {
		return r.Styles.Muted.Render(fmt.Sprintf("checked %s — no problems found", lines(c.Checked)))
	}

	var b strings.Builder
	verb := "need"
	if len(c.Problems) == 1 {
		verb = "needs"
	}
	b.WriteString(r.Styles.Title.Render(fmt.Sprintf("%d of %s %s attention",
		len(c.Problems), lines(c.Checked), verb)))
	b.WriteString("\n")
	for _, p := range c.Problems {
		b.WriteString("\n")
		b.WriteString(r.Styles.Header.Render(p.TPNC))
		if p.Title != "" {
			b.WriteString(" " + p.Title)
		}
		b.WriteString("\n  ")
		b.WriteString(p.Detail)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// lines pluralises a count of basket lines.
func lines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

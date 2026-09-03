package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/endigma/tescoctl/internal/tesco"
	"github.com/endigma/tescoctl/internal/view"
	"github.com/urfave/cli/v3"
)

func basketCmd() *cli.Command {
	return &cli.Command{
		Name:  "basket",
		Usage: "Show the current trolley",
		Commands: []*cli.Command{
			basketAddCmd(),
			basketRemoveCmd(),
			basketCheckCmd(),
		},
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if err := a.requireAuth(ctx); err != nil {
				return err
			}
			basket, err := a.c.Basket(ctx)
			if err != nil {
				return err
			}
			return a.emitBasket(ctx, basket)
		}),
	}
}

// basketCheckCmd verifies a basket line by line, which needs a product lookup
// each and so is a command rather than something every `tescoctl basket` pays for.
func basketCheckCmd() *cli.Command {
	return &cli.Command{
		Name:  "check",
		Usage: "Verify every line in the trolley against the catalogue",
		Description: "Looks up each line to find items Tesco no longer sells, and lines\n" +
			"tesco.com cannot display. Costs one request per line, so it is not run\n" +
			"by `tescoctl basket`. Exits non-zero when it finds something.",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if err := a.requireAuth(ctx); err != nil {
				return err
			}
			basket, err := a.c.Basket(ctx)
			if err != nil {
				return err
			}
			return a.checkBasket(ctx, basket)
		}),
	}
}

// checkBasket inspects each line against the catalogue and reports what is
// wrong with it, exiting non-zero when anything is.
func (a *app) checkBasket(ctx context.Context, basket *tesco.Basket) error {
	v := view.FromBasket(basket, tesco.CheckoutURL)
	report := view.BasketCheck{Checked: len(v.Items)}

	for _, item := range v.Items {
		product, err := a.c.Product(ctx, item.TPNC)
		if err != nil {
			report.Problems = append(report.Problems, view.BasketProblem{
				TPNC: item.TPNC, Title: item.Title, Kind: view.ProblemUnknown,
				Detail: "could not be looked up: " + err.Error(),
			})
			continue
		}
		if product.IsForSale != nil && !*product.IsForSale {
			report.Problems = append(report.Problems, view.BasketProblem{
				TPNC: item.TPNC, Title: item.Title, Kind: view.ProblemUnavailable,
				Detail: "no longer for sale — it will not be delivered; remove it with " +
					"`tescoctl basket rm " + item.TPNC + "`",
			})
		}
		// A fractional line is either a legitimate catchweight or the corruption
		// that stops tesco.com rendering the basket at all. Only the product can
		// tell them apart.
		if item.Quantity != nil && tesco.FractionalPieces(*item.Quantity, item.Unit) &&
			!product.HasWeight(*item.Quantity) {
			report.Problems = append(report.Problems, view.BasketProblem{
				TPNC: item.TPNC, Title: item.Title, Kind: view.ProblemUnrenderable,
				Detail: "is at " + quantity(item.Quantity, "") + " " + item.Unit +
					", which tesco.com cannot display — the whole basket page fails while " +
					"this line is there; remove it with `tescoctl basket rm " + item.TPNC + "`",
			})
		}
	}

	if err := a.r.Emit(report, func() string { return basketCheckReport(a.r, report) }); err != nil {
		return err
	}
	if len(report.Problems) > 0 {
		return errQuiet
	}
	return nil
}

func basketAddCmd() *cli.Command {
	return &cli.Command{
		Name:      "add",
		Usage:     "Add a product to the trolley, or set its quantity",
		ArgsUsage: "<tpnc>",
		Flags: []cli.Flag{
			&cli.FloatFlag{
				Name:    "qty",
				Aliases: []string{"q"},
				Usage:   "quantity to set (whole numbers: lines are written in pieces)",
				Value:   1,
			},
			&cli.FloatFlag{
				Name:    "weight",
				Aliases: []string{"w"},
				Usage:   "`kg` for a product sold by weight — see tescoctl product for the weights on offer",
			},
		},
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if cmd.IsSet("weight") {
				if cmd.IsSet("qty") {
					return errors.New("--qty and --weight are alternatives: a line is written " +
						"either in pieces or in kilos, not both")
				}
				return a.setWeight(ctx, cmd.Args().First(), cmd.Float("weight"))
			}
			return a.setQuantity(ctx, cmd.Args().First(), cmd.Float("qty"))
		}),
	}
}

func basketRemoveCmd() *cli.Command {
	return &cli.Command{
		Name:      "rm",
		Aliases:   []string{"remove"},
		Usage:     "Remove a product from the trolley",
		ArgsUsage: "<tpnc>",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			return a.setQuantity(ctx, cmd.Args().First(), 0)
		}),
	}
}

// setWeight adds a catchweight product at one of its selectable weights.
func (a *app) setWeight(ctx context.Context, tpnc string, weight float64) error {
	tpnc = strings.TrimSpace(tpnc)
	if tpnc == "" {
		return errors.New("a TPNC is required — find one with `tescoctl search`")
	}
	if weight <= 0 {
		return errors.New("--weight must be positive; remove a line with `tescoctl basket rm`")
	}
	if err := a.requireAuth(ctx); err != nil {
		return err
	}
	basket, err := a.c.SetWeight(ctx, tpnc, weight, "")
	if err != nil {
		return err
	}
	return a.emitBasket(ctx, basket)
}

// setQuantity backs both add and rm — Tesco uses one mutation for both, with
// zero meaning removal.
func (a *app) setQuantity(ctx context.Context, tpnc string, qty float64) error {
	tpnc = strings.TrimSpace(tpnc)
	if tpnc == "" {
		return errors.New("a TPNC is required — find one with `tescoctl search`")
	}
	if err := a.requireAuth(ctx); err != nil {
		return err
	}
	basket, err := a.c.SetQuantity(ctx, tpnc, qty, "")
	if err != nil {
		return err
	}
	return a.emitBasket(ctx, basket)
}

// emitBasket prints the trolley, then warns about any line whose quantity is a
// fraction of a whole-unit item. tesco.com cannot render such a line and fails
// to load the entire basket page without saying which line is at fault, so this
// warning is the only place the tpnc needed to remove it can come from.
//
// A catchweight line looks identical: Tesco reports a 1.8kg joint as "1.8 pcs",
// the same shape as the corruption. Telling them apart needs the product, so a
// fractional line is confirmed against its CatchWeightList before being called
// broken. Only fractional lines are looked up, and a healthy basket has none.
func (a *app) emitBasket(ctx context.Context, basket *tesco.Basket) error {
	v := view.FromBasket(basket, tesco.CheckoutURL)
	if err := a.r.Emit(v, func() string { return basketTable(a.r, v) }); err != nil {
		return err
	}
	for _, item := range v.Items {
		if item.Quantity == nil || !tesco.FractionalPieces(*item.Quantity, item.Unit) {
			continue
		}
		if a.weightIsOffered(ctx, item.TPNC, *item.Quantity) {
			continue
		}
		a.r.Note("warning: %s (%s) is in the basket at %s %s — tesco.com cannot display the basket "+
			"while this line is there; remove it with `tescoctl basket rm %s`",
			item.TPNC, item.Title, quantity(item.Quantity, ""), item.Unit, item.TPNC)
	}
	return nil
}

// weightIsOffered reports whether a fractional line is a legitimate catchweight
// line rather than a corrupt one. A lookup that fails answers false: the warning
// is about an unreadable basket, and a maybe is worth printing.
func (a *app) weightIsOffered(ctx context.Context, tpnc string, quantity float64) bool {
	product, err := a.c.Product(ctx, tpnc)
	if err != nil {
		return false
	}
	return product.HasWeight(quantity)
}

func favouritesCmd() *cli.Command {
	return &cli.Command{
		Name:    "favourites",
		Aliases: []string{"favs"},
		Usage:   "List your usual items",
		Flags:   pageFlags(30),
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if err := a.requireAuth(ctx); err != nil {
				return err
			}
			listing, err := a.c.Favourites(ctx, cmd.Int("page"), cmd.Int("limit"))
			if err != nil {
				return err
			}
			return a.emitListing(listing)
		}),
	}
}

func ordersCmd() *cli.Command {
	return &cli.Command{
		Name:  "orders",
		Usage: "List your orders",
		Flags: append(pageFlags(10), &cli.BoolFlag{
			Name:  "pending",
			Usage: "list upcoming orders instead of past ones",
		}),
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			if err := a.requireAuth(ctx); err != nil {
				return err
			}
			contexts := tesco.PreviousOrders
			if cmd.Bool("pending") {
				contexts = tesco.PendingOrders
			}
			orders, err := a.c.Orders(ctx, contexts, cmd.Int("page"), cmd.Int("limit"))
			if err != nil {
				return err
			}
			items := view.FromOrders(orders)
			return a.r.Emit(items, func() string { return orderTable(a.r, items) })
		}),
	}
}

func orderCmd() *cli.Command {
	return &cli.Command{
		Name:      "order",
		Usage:     "Show one order and its lines",
		ArgsUsage: "<order id>",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			id := strings.TrimSpace(cmd.Args().First())
			if id == "" {
				return errors.New("an order id is required — list them with `tescoctl orders`")
			}
			if err := a.requireAuth(ctx); err != nil {
				return err
			}
			order, err := a.c.Order(ctx, id)
			if err != nil {
				return err
			}
			v := view.FromOrder(*order)
			return a.r.Emit(v, func() string { return orderDetail(a.r, v) })
		}),
	}
}

func slotsCmd() *cli.Command {
	return &cli.Command{
		Name:  "slots",
		Usage: "List delivery slots",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "from",
				Usage: "start date, YYYY-MM-DD (default: today)",
			},
			&cli.StringFlag{
				Name:  "to",
				Usage: "end date, YYYY-MM-DD (default: a week after --from)",
			},
			&cli.BoolFlag{
				Name:  "all",
				Usage: "include slots that are full or unavailable",
			},
		},
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			// Validate arguments before auth: a malformed date is the user's
			// immediate mistake, and reporting a missing session instead would
			// send them off to log in only to hit the same error.
			start, err := parseDay(cmd.String("from"), time.Now())
			if err != nil {
				return fmt.Errorf("--from: %w", err)
			}
			end, err := parseDay(cmd.String("to"), start.AddDate(0, 0, 7))
			if err != nil {
				return fmt.Errorf("--to: %w", err)
			}
			if !end.After(start) {
				return errors.New("--to must be after --from")
			}

			if err := a.requireAuth(ctx); err != nil {
				return err
			}

			slots, err := a.c.Slots(ctx, start, end)
			if err != nil {
				return err
			}
			items := view.FromSlots(slots)
			if !cmd.Bool("all") {
				items = availableSlots(items)
			}
			return a.r.Emit(items, func() string { return slotTable(a.r, items) })
		}),
	}
}

// availableSlots keeps only bookable windows. Tesco returns the full grid
// including sold-out slots, which is rarely what you want to read.
func availableSlots(slots []view.Slot) []view.Slot {
	out := make([]view.Slot, 0, len(slots))
	for _, s := range slots {
		if strings.EqualFold(s.Status, "AVAILABLE") || s.Status == "" {
			out = append(out, s)
		}
	}
	return out
}

// parseDay accepts a YYYY-MM-DD date, falling back to fallback when empty.
func parseDay(s string, fallback time.Time) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return fallback, nil
	}
	day, err := time.ParseInLocation(time.DateOnly, s, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not a YYYY-MM-DD date", s)
	}
	return day, nil
}

// quantity renders a line quantity, dropping the decimal for whole numbers so
// "2" does not print as "2.00" while a 0.35kg loose item still reads correctly.
func quantity(q *float64, unit string) string {
	if q == nil {
		return ""
	}
	s := strconv.FormatFloat(*q, 'f', -1, 64)
	if unit != "" && !strings.EqualFold(unit, "pcs") {
		return s + unit
	}
	return s
}

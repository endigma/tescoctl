package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/endigma/tescoctl/internal/tesco"
	"github.com/endigma/tescoctl/internal/view"
	"github.com/urfave/cli/v3"
)

func pageFlags(defaultLimit int) []cli.Flag {
	return []cli.Flag{
		&cli.IntFlag{
			Name:    "limit",
			Aliases: []string{"n"},
			Usage:   "results per page",
			Value:   defaultLimit,
		},
		&cli.IntFlag{
			Name:    "page",
			Aliases: []string{"p"},
			Usage:   "page number, 1-based",
			Value:   1,
		},
	}
}

func searchCmd() *cli.Command {
	return &cli.Command{
		Name:      "search",
		Usage:     "Search the Tesco catalogue",
		ArgsUsage: "<query>",
		Flags:     pageFlags(20),
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			query := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
			if query == "" {
				return errors.New("a search query is required, e.g. `tescoctl search oat milk`")
			}
			listing, err := a.c.Search(ctx, query, cmd.Int("page"), cmd.Int("limit"))
			if err != nil {
				return err
			}
			return a.emitListing(listing)
		}),
	}
}

func productCmd() *cli.Command {
	return &cli.Command{
		Name:      "product",
		Usage:     "Show one product by TPNC",
		ArgsUsage: "<tpnc>",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			tpnc := strings.TrimSpace(cmd.Args().First())
			if tpnc == "" {
				return errors.New("a TPNC is required — find one with `tescoctl search`")
			}
			p, err := a.c.Product(ctx, tpnc)
			if err != nil {
				return err
			}
			detail := view.FromProductDetail(*p)
			return a.r.Emit(detail, func() string { return productDetail(a.r, detail) })
		}),
	}
}

func categoriesCmd() *cli.Command {
	return &cli.Command{
		Name:    "categories",
		Aliases: []string{"cats"},
		Usage:   "List the category tree and its browse facets",
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			nodes, err := a.c.Taxonomy(ctx)
			if err != nil {
				return err
			}
			cats := view.FromTaxonomy(nodes)
			return a.r.Emit(cats, func() string { return categoryTree(a.r, cats) })
		}),
	}
}

func browseCmd() *cli.Command {
	return &cli.Command{
		Name:      "browse",
		Usage:     "List products in a category",
		ArgsUsage: "<facet|category name>",
		Flags:     pageFlags(20),
		Action: action(func(ctx context.Context, cmd *cli.Command, a *app) error {
			arg := strings.TrimSpace(strings.Join(cmd.Args().Slice(), " "))
			if arg == "" {
				return errors.New("a category is required — list them with `tescoctl categories`")
			}

			facet, err := resolveFacet(ctx, a, arg)
			if err != nil {
				return err
			}

			listing, err := a.c.Category(ctx, facet, cmd.Int("page"), cmd.Int("limit"))
			if err != nil {
				return err
			}
			return a.emitListing(listing)
		}),
	}
}

// resolveFacet accepts either a facet id or a category name. Names are matched
// against the live taxonomy rather than encoded locally: the encoding is
// recoverable but the taxonomy is authoritative, and a typo should fail with a
// list of real categories instead of a 200 with zero results.
func resolveFacet(ctx context.Context, a *app, arg string) (string, error) {
	if tesco.IsFacet(arg) {
		return arg, nil
	}

	nodes, err := a.c.Taxonomy(ctx)
	if err != nil {
		return "", fmt.Errorf("looking up category %q: %w", arg, err)
	}

	var names []string
	for _, n := range nodes {
		if strings.EqualFold(n.Name, arg) {
			return n.ID, nil
		}
		names = append(names, n.Name)
		for _, child := range n.Children {
			if strings.EqualFold(child.Name, arg) {
				return child.ID, nil
			}
		}
	}
	return "", fmt.Errorf("no category named %q; top-level categories are: %s",
		arg, strings.Join(names, ", "))
}

// emitListing prints a page of products, then says so when it is only a page.
//
// Nothing in the products themselves reveals a truncated listing — a full page
// and a first page of five hundred look identical — so the total from Tesco's
// own list info is the only signal, and it is worth surfacing rather than
// leaving the reader to guess whether --page would show more.
func (a *app) emitListing(listing tesco.Listing) error {
	items := view.FromProducts(listing.Products)
	if err := a.r.Emit(items, func() string { return productTable(a.r, items) }); err != nil {
		return err
	}
	a.noteTruncation(len(items), listing)
	return nil
}

// noteTruncation reports how much of a result set is on screen. It says nothing
// when Tesco reported no total, or when everything fits.
func (a *app) noteTruncation(shown int, listing tesco.Listing) {
	if listing.Total <= 0 || shown >= listing.Total {
		return
	}
	page := listing.Page
	if page < 1 {
		page = 1
	}
	seen := (page-1)*shown + shown
	if seen > listing.Total {
		seen = listing.Total
	}
	a.r.Note("showing %d of %d (page %d) — use --page and --limit for the rest",
		seen, listing.Total, page)
}

package checkoutsession

import (
	"fmt"
	"math/big"

	"github.com/IQVO/checkout/internal/domain/shared"
)

func contract(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrContractViolation, fmt.Sprintf(format, args...))
}

// numberLinesAndGroups assigns lineNo 1..n in cart line order and checks the
// groups partition the lines (gap S1): a group whose item set cannot be formed
// from the cart is a contract bug.
func numberLinesAndGroups(c CartSnapshot) ([]Line, []Group, error) {
	if len(c.Lines) == 0 || len(c.Lines) > MaxLines {
		return nil, nil, contract("a cart needs 1..%d lines, has %d", MaxLines, len(c.Lines))
	}
	lines := make([]Line, len(c.Lines))
	for i, l := range c.Lines {
		lines[i] = Line{LineNo: i + 1, CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
		if err := lines[i].validate(); err != nil {
			return nil, nil, err
		}
	}
	groups, err := numberGroups(c.Groups, len(lines))
	if err != nil {
		return nil, nil, err
	}
	return lines, groups, nil
}

func numberGroups(in []CartGroup, lineCount int) ([]Group, error) {
	if len(in) == 0 {
		return nil, contract("the cart has no shipment group")
	}
	covered := map[int]bool{}
	groups := make([]Group, len(in))
	last := 0
	for i, g := range in {
		if g.GroupNo <= last || !shared.ValidID(g.OfferID) || !g.Tier.Valid() || len(g.LineNos) == 0 {
			return nil, contract("group %d is malformed or out of order", g.GroupNo)
		}
		last = g.GroupNo
		if err := claimLines(g, lineCount, covered); err != nil {
			return nil, err
		}
		groups[i] = Group{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: g.Tier, LineNos: append([]int(nil), g.LineNos...)}
	}
	if len(covered) != lineCount {
		return nil, contract("the groups cover %d of %d lines", len(covered), lineCount)
	}
	return groups, nil
}

func claimLines(g CartGroup, lineCount int, covered map[int]bool) error {
	for _, n := range g.LineNos {
		if n < 1 || n > lineCount || covered[n] {
			return contract("group %d names line %d that is unknown or already grouped", g.GroupNo, n)
		}
		covered[n] = true
	}
	return nil
}

// allocate sets Group.AmountMinor: the group's item totals plus its delivery
// charge plus a pro-rata share of the tax (remainder to the first group) so
// the amounts sum to the quote total exactly (the allocations sent to
// customer-payments). A quote that does not add up is a contract bug.
func allocate(lines []Line, groups []Group, q PriceQuote) error {
	if err := checkQuote(lines, groups, q); err != nil {
		return err
	}
	totals := map[string]int64{}
	for _, l := range q.Lines {
		totals[l.CatalogItemID] = l.LineTotalMinor
	}
	charges := map[int]int64{}
	for _, c := range q.Charges {
		charges[c.GroupNo] = c.ChargeMinor
	}
	var sumBase int64
	for i := range groups {
		base := charges[groups[i].GroupNo]
		for _, n := range groups[i].LineNos {
			base += totals[lines[n-1].CatalogItemID]
		}
		groups[i].AmountMinor = base
		sumBase += base
	}
	var given int64
	for i := range groups {
		share := taxShare(q.TaxMinor, groups[i].AmountMinor, sumBase)
		groups[i].AmountMinor += share
		given += share
	}
	groups[0].AmountMinor += q.TaxMinor - given
	var sum int64
	for _, g := range groups {
		sum += g.AmountMinor
	}
	if sum != q.TotalMinor {
		return contract("quote total %d is not items + delivery + tax (%d)", q.TotalMinor, sum)
	}
	return nil
}

func taxShare(tax, base, sumBase int64) int64 {
	if sumBase == 0 {
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(tax), big.NewInt(base))
	return n.Div(n, big.NewInt(sumBase)).Int64()
}

func checkQuote(lines []Line, groups []Group, q PriceQuote) error {
	if !shared.ValidID(q.QuoteID) || !validCurrency(q.Currency) || q.TotalMinor < 0 || q.TaxMinor < 0 {
		return contract("the price quote is malformed")
	}
	if len(q.Lines) != len(lines) || len(q.Charges) != len(groups) {
		return contract("the quote prices %d lines and %d groups, the cart has %d and %d",
			len(q.Lines), len(q.Charges), len(lines), len(groups))
	}
	if err := checkQuoteLines(lines, q.Lines); err != nil {
		return err
	}
	return checkQuoteCharges(groups, q.Charges)
}

func validCurrency(c string) bool {
	if len(c) != 3 {
		return false
	}
	for _, r := range c {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

func checkQuoteLines(lines []Line, priced []QuoteLine) error {
	items := map[string]bool{}
	for _, l := range priced {
		if l.LineTotalMinor < 0 || items[l.CatalogItemID] {
			return contract("quote line %q is negative or repeated", l.CatalogItemID)
		}
		items[l.CatalogItemID] = true
	}
	for _, l := range lines {
		if !items[l.CatalogItemID] {
			return contract("the quote does not price %q", l.CatalogItemID)
		}
	}
	return nil
}

func checkQuoteCharges(groups []Group, charges []QuoteCharge) error {
	seen := map[int]bool{}
	for _, c := range charges {
		if c.ChargeMinor < 0 || seen[c.GroupNo] {
			return contract("charge of group %d is negative or repeated", c.GroupNo)
		}
		seen[c.GroupNo] = true
	}
	for _, g := range groups {
		if !seen[g.GroupNo] {
			return contract("the quote has no delivery charge for group %d", g.GroupNo)
		}
	}
	return nil
}

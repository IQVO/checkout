package checkoutsession

import (
	"time"

	"github.com/IQVO/checkout/internal/domain/shared"
)

// Line is one session line: the cart line order numbered 1..n (gap S1).
type Line struct {
	LineNo        int
	CatalogItemID string
	Quantity      int
}

// Group is one shipment group of the session: the quote's groupNo, the
// selected offer and tier, the lines it ships and the share of the quote total
// that belongs to it (the allocation sent to customer-payments).
type Group struct {
	GroupNo     int
	OfferID     string
	Tier        Tier
	LineNos     []int
	AmountMinor int64
}

// RequotedOffer is an offer customer-ordering proposed after a definitive 409.
// The delivery-by bounds are ISO dates (YYYY-MM-DD).
type RequotedOffer struct {
	OfferID           string
	GroupNo           int
	Tier              Tier
	DeliverByEarliest string
	DeliverByLatest   string
	ExpiresAt         time.Time
}

// StoredPlacement is the PII-free place-order request, stored when the session
// enters PLACING so ResolveInDoubtPlacements can replay it byte for byte
// (decision H9). PlacingSince drives the 20 h alert and the 22 h give-up.
type StoredPlacement struct {
	RequestBody  []byte
	RequestHash  string
	PlacingSince time.Time
}

// VoidOwed marks an authorization that must still be voided (decision H5).
type VoidOwed struct {
	AuthorizationID string
}

func (l Line) validate() error {
	if l.LineNo < 1 || l.LineNo > MaxLines {
		return shared.Invalid("lineNo %d is out of range", l.LineNo)
	}
	if !shared.ValidID(l.CatalogItemID) {
		return shared.Invalid("catalogItemId must be a valid id")
	}
	if l.Quantity < 1 || l.Quantity > MaxQuantity {
		return shared.Invalid("quantity %d is out of range", l.Quantity)
	}
	return nil
}

func (r RequotedOffer) validate() error {
	if !shared.ValidID(r.OfferID) || r.GroupNo < 1 || !r.Tier.Valid() || r.ExpiresAt.IsZero() {
		return shared.Invalid("requoted offer %q is incomplete", r.OfferID)
	}
	for _, d := range []string{r.DeliverByEarliest, r.DeliverByLatest} {
		if _, err := time.Parse(time.DateOnly, d); err != nil {
			return shared.Invalid("requoted offer %q has an invalid date %q", r.OfferID, d)
		}
	}
	return nil
}

func cloneGroups(in []Group) []Group {
	out := make([]Group, len(in))
	for i, g := range in {
		g.LineNos = append([]int(nil), g.LineNos...)
		out[i] = g
	}
	return out
}

func clonePlacement(p *StoredPlacement) *StoredPlacement {
	if p == nil {
		return nil
	}
	c := *p
	c.RequestBody = append([]byte(nil), p.RequestBody...)
	return &c
}

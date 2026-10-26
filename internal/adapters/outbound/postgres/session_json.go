package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/IQVO/checkout/internal/domain/checkoutsession"
)

type lineJSON struct {
	LineNo        int    `json:"lineNo"`
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

type groupJSON struct {
	GroupNo     int    `json:"groupNo"`
	OfferID     string `json:"offerId"`
	Tier        string `json:"tier"`
	LineNos     []int  `json:"lineNos"`
	AmountMinor int64  `json:"amountMinor"`
}

type requotedJSON struct {
	OfferID           string    `json:"offerId"`
	GroupNo           int       `json:"groupNo"`
	Tier              string    `json:"tier"`
	DeliverByEarliest string    `json:"deliverByEarliest"`
	DeliverByLatest   string    `json:"deliverByLatest"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

func marshalLines(in []checkoutsession.Line) ([]byte, error) {
	out := make([]lineJSON, len(in))
	for i, l := range in {
		out[i] = lineJSON(l)
	}
	return json.Marshal(out)
}

func unmarshalLines(b []byte) ([]checkoutsession.Line, error) {
	var in []lineJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	out := make([]checkoutsession.Line, len(in))
	for i, l := range in {
		out[i] = checkoutsession.Line(l)
	}
	return out, nil
}

func marshalGroups(in []checkoutsession.Group) ([]byte, error) {
	out := make([]groupJSON, len(in))
	for i, g := range in {
		out[i] = groupJSON{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: string(g.Tier), LineNos: g.LineNos, AmountMinor: g.AmountMinor}
	}
	return json.Marshal(out)
}

func unmarshalGroups(b []byte) ([]checkoutsession.Group, error) {
	var in []groupJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, err
	}
	out := make([]checkoutsession.Group, len(in))
	for i, g := range in {
		out[i] = checkoutsession.Group{GroupNo: g.GroupNo, OfferID: g.OfferID, Tier: checkoutsession.Tier(g.Tier), LineNos: g.LineNos, AmountMinor: g.AmountMinor}
	}
	return out, nil
}

func marshalRequoted(in []checkoutsession.RequotedOffer) ([]byte, error) {
	out := make([]requotedJSON, len(in))
	for i, o := range in {
		out[i] = requotedJSON{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: string(o.Tier), DeliverByEarliest: o.DeliverByEarliest,
			DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt.UTC()}
	}
	return json.Marshal(out)
}

func unmarshalRequoted(b []byte) ([]checkoutsession.RequotedOffer, error) {
	var in []requotedJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return nil, fmt.Errorf("requoted offers: %w", err)
	}
	out := make([]checkoutsession.RequotedOffer, len(in))
	for i, o := range in {
		out[i] = checkoutsession.RequotedOffer{OfferID: o.OfferID, GroupNo: o.GroupNo, Tier: checkoutsession.Tier(o.Tier),
			DeliverByEarliest: o.DeliverByEarliest, DeliverByLatest: o.DeliverByLatest, ExpiresAt: o.ExpiresAt.UTC()}
	}
	return out, nil
}

package clientapi

import (
	"testing"

	"github.com/mrjvadi/torncity/internal/gateway/groups"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/companies"
)

// Every typed value of the companies screens reaches the web as an input: the
// command has an input entry, and the action's fixed arguments fit it.
func TestCompaniesAskActionsBecomeInputs(t *testing.T) {
	policy, err := groups.LoadPolicy("../../configs/commands.yml")
	if err != nil {
		t.Fatal(err)
	}
	c := presentation.Ctx{Lang: "fa"}
	ref := presentation.CompanyRef{Code: "Q7M2K9B", Name: "Nilou", Type: presentation.Named{Code: "farm", Name: "farm"}}
	job := presentation.JobRef{CareerCode: "farming"}
	good := presentation.Good{Item: presentation.Named{Code: "phone"}, Design: "Sparrow", DesignNo: 4}
	pay := presentation.PaymentChoice{Usable: []string{"cash"}}
	slot := companies.SlotLine{Slot: "core", Min: 1, Max: 4, Component: presentation.Named{Code: "chip"}, Qty: 2}
	draft := companies.RecruitDraftView{Ref: ref, No: 15, Section: companies.RecruitSectionPay, Level: 2, MaxLevel: 5, Positions: 2, MaxPositions: 4,
		Presets: companies.RecruitPresets{Salary: []int64{500}, Housing: []int64{0}, Signing: []int64{0}, Relocation: []int64{0}}}
	owned := companies.TechView{Ref: ref, Tech: presentation.Named{Code: "circuits"}, State: companies.TechOwned, Mode: companies.TechPrivate}
	cases := map[string]struct {
		resp *presentation.Response
		want int
	}{
		"manage": {companies.CompanyManage(c, companies.CompanyManageView{Ref: ref, Owner: true}), 4},
		"type": {companies.CompanyTypeDetail(c, companies.CompanyTypeView{Type: presentation.Named{Code: "farm"},
			Payment: &pay}), 1},
		"openings":  {companies.CompanyOpenings(c, companies.CompanyOpeningsView{Ref: ref, Careers: []presentation.JobRef{job}, Room: 1}), 1},
		"suppliers": {companies.Suppliers(c, companies.SuppliersView{Ref: ref, Offers: []companies.SupplyOffer{{Component: presentation.Named{Code: "chip"}, Stock: 5}}}), 1},
		"design": {companies.Design(c, companies.DesignView{Ref: ref, No: 4, Status: companies.DesignDraft, Choosing: "core",
			Slots: []companies.SlotLine{slot}}), 1},
		"design name": {companies.Design(c, companies.DesignView{Ref: ref, No: 4, Status: companies.DesignDraft}), 1},
		"tech":        {companies.Tech(c, owned), 1},
		"sell":        {companies.Sell(c, companies.SellView{Ref: ref, Good: good, Have: 5, Qty: 2, Reference: 100}), 1},
		"buy": {companies.CompanyBuy(c, companies.BuyView{Line: companies.GoodsLine{No: 7, Good: good, Left: 3, Price: 10, Company: ref},
			Qty: 1, Payment: &pay}), 1},
		"draft": {companies.RecruitDraft(c, draft), 4},
	}
	for name, tc := range cases {
		asks := 0
		for _, a := range tc.resp.Actions {
			if a.Ask {
				asks++
			}
		}
		if asks != tc.want {
			t.Errorf("%s: the screen lists %d typed values, want %d", name, asks, tc.want)
		}
		out := ScreenOf(tc.resp, "", policy, nil)
		inputs := 0
		for _, a := range out.Actions {
			if a.Input != nil {
				inputs++
			}
		}
		if inputs != asks {
			t.Errorf("%s: %d typed values reach the web as inputs, the screen lists %d", name, inputs, asks)
		}
	}
}

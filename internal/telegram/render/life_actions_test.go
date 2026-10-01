package render

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mrjvadi/torncity/internal/commands"
	"github.com/mrjvadi/torncity/internal/gateway/routing"
	"github.com/mrjvadi/torncity/internal/presentation"
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// lifeCase is one life screen with views to try: the fully populated one and
// variants that switch branches the populated one cannot show together.
type lifeCase struct {
	screen string
	build  func(c presentation.Ctx, v any) *presentation.Response
	tweaks []func(v any)
}

// onlyTweaks lists the screens whose plain populated view is not a state the core can send.
var onlyTweaks = map[string]bool{life.ScreenPropertyRefusal: true}

func lifeCases() []lifeCase {
	cs := []lifeCase{
		{life.ScreenProfile, func(c presentation.Ctx, v any) *presentation.Response { return life.Profile(c, v.(life.ProfileView)) }, []func(any){
			func(v any) {
				x := v.(*life.ProfileView)
				x.Travelling, x.Jail, x.Hospital = false, nil, nil
			},
			func(v any) { x := v.(*life.ProfileView); x.Travelling, x.Jail = false, nil },
			func(v any) {
				x := v.(*life.ProfileView)
				x.Travelling, x.Jail, x.Hospital, x.Village, x.Work.Job = false, nil, nil, nil, nil
			},
		}},
		{life.ScreenDashboard, func(c presentation.Ctx, v any) *presentation.Response {
			return life.Dashboard(c, v.(life.DashboardView))
		}, []func(any){
			func(v any) { x := v.(*life.DashboardView); x.Travelling, x.Jail = false, nil },
		}},
		{life.ScreenCityMap, func(c presentation.Ctx, v any) *presentation.Response { return life.CityMap(c, v.(life.CityMapView)) }, []func(any){
			func(v any) { x := v.(*life.CityMapView); x.Travelling = false },
			func(v any) { x := v.(*life.CityMapView); x.Travelling, x.NoCity, x.Walking = false, false, nil },
			func(v any) {
				x := v.(*life.CityMapView)
				x.Travelling, x.NoCity, x.Walking = false, false, nil
				x.Places[0].Here = false
			},
		}},
		{life.ScreenCities, func(c presentation.Ctx, v any) *presentation.Response { return life.Cities(c, v.(life.MapView)) }, []func(any){
			func(v any) { x := v.(*life.MapView); x.Travelling = false },
			func(v any) { x := v.(*life.MapView); x.Travelling, x.OriginCode, x.Origin = false, "", "" },
		}},
		{life.ScreenTravelOptions, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelOptions(c, v.(life.TravelOptionsView))
		}, nil},
		{life.ScreenTravelCheckout, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelCheckout(c, v.(life.TravelCheckoutView))
		}, []func(any){func(v any) { x := v.(*life.TravelCheckoutView); x.Payment.Usable = nil }}},
		{life.ScreenTravelStarted, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelStarted(c, v.(life.TravelStartedView))
		}, nil},
		{life.ScreenTravelStatus, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelStatus(c, v.(life.TravelStatusView))
		}, nil},
		{life.ScreenTravelArrived, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelArrived(c, v.(life.TravelArrivedView))
		}, nil},
		{life.ScreenTravelHere, func(c presentation.Ctx, v any) *presentation.Response {
			return life.TravelHere(c, v.(life.TravelHereView))
		}, nil},
		{life.ScreenWalkStarted, func(c presentation.Ctx, v any) *presentation.Response {
			return life.WalkStarted(c, v.(life.WalkStartedView))
		}, nil},
		{life.ScreenNotHere, func(c presentation.Ctx, v any) *presentation.Response { return life.NotHere(c, v.(life.NotHereView)) }, []func(any){
			func(v any) { x := v.(*life.NotHereView); x.Walking = false },
			func(v any) { x := v.(*life.NotHereView); x.Walking, x.Then, x.ThenArgs = false, "", nil },
		}},
		{life.ScreenLife, func(c presentation.Ctx, v any) *presentation.Response { return life.Life(c, v.(life.LifeView)) }, []func(any){
			func(v any) { x := v.(*life.LifeView); x.SleepIn = 0 },
			func(v any) { x := v.(*life.LifeView); x.SleepIn = 0; x.Spots[0].Way = nil },
		}},
		{life.ScreenCard, func(c presentation.Ctx, v any) *presentation.Response { return life.Card(c, v.(life.CardView)) }, []func(any){
			func(v any) { x := v.(*life.CardView); x.Self = false },
		}},
		{life.ScreenHistory, func(c presentation.Ctx, v any) *presentation.Response { return life.History(c, v.(life.HistoryView)) }, []func(any){
			func(v any) { x := v.(*life.HistoryView); x.Self = false; x.Page = 2; x.Pages = 3 },
			func(v any) { x := v.(*life.HistoryView); x.Page = 1; x.Pages = 3 },
		}},
		{life.ScreenAvatars, func(c presentation.Ctx, v any) *presentation.Response { return life.Avatars(c, v.(life.AvatarsView)) }, nil},
		{life.ScreenSleepPay, func(c presentation.Ctx, v any) *presentation.Response { return life.SleepPay(c, v.(life.SleepPayView)) }, nil},
		{life.ScreenLifeRefusal, func(c presentation.Ctx, v any) *presentation.Response {
			return life.LifeRefusal(c, v.(life.LifeRefusalView))
		}, []func(any){
			func(v any) { v.(*life.LifeRefusalView).Kind = life.LifeRefusedBioLength },
			func(v any) { v.(*life.LifeRefusalView).Kind = life.LifeRefusedNoAvatar },
			func(v any) { v.(*life.LifeRefusalView).Kind = life.LifeRefusedNoPlayer },
		}},
		{life.ScreenSettings, func(c presentation.Ctx, v any) *presentation.Response { return life.Settings(c, v.(life.SettingsView)) }, []func(any){
			func(v any) {
				x := v.(*life.SettingsView)
				x.Language = "fa"
				x.Languages = []string{"fa", "en"}
				x.PresenceVisibility = "contacts"
			},
		}},
		{life.ScreenDevices, func(c presentation.Ctx, v any) *presentation.Response { return life.Devices(c, v.(life.DevicesView)) }, nil},
		{life.ScreenDeviceLink, func(c presentation.Ctx, v any) *presentation.Response {
			return life.DeviceLink(c, v.(life.DeviceLinkView))
		}, nil},
		{life.ScreenAchievements, func(c presentation.Ctx, v any) *presentation.Response {
			return life.Achievements(c, v.(life.AchievementsView))
		}, nil},
		{life.ScreenRefusal, func(c presentation.Ctx, v any) *presentation.Response { return life.Refusal(c, v.(life.RefusalView)) }, []func(any){
			func(v any) { v.(*life.RefusalView).Kind = life.RefusalJobRequirements },
			func(v any) { v.(*life.RefusalView).Kind = life.RefusalNotAtWorkplace },
			func(v any) { v.(*life.RefusalView).Kind = life.RefusalCannotAfford },
		}},
		{life.ScreenInventory, func(c presentation.Ctx, v any) *presentation.Response {
			return life.Inventory(c, v.(life.InventoryView))
		}, []func(any){
			func(v any) { x := v.(*life.InventoryView); x.Page, x.Pages = 2, 3 },
		}},
		{life.ScreenItemDetail, func(c presentation.Ctx, v any) *presentation.Response {
			return life.ItemDetail(c, v.(life.ItemDetailView))
		}, []func(any){
			func(v any) { x := v.(*life.ItemDetailView); x.CoolingFor = 0; x.Piece = false },
		}},
		{life.ScreenItemUsed, func(c presentation.Ctx, v any) *presentation.Response { return life.ItemUsed(c, v.(life.ItemUsedView)) }, nil},
		{life.ScreenItemGiven, func(c presentation.Ctx, v any) *presentation.Response {
			return life.ItemGiven(c, v.(life.ItemGivenView))
		}, nil},
		{life.ScreenDropConfirm, func(c presentation.Ctx, v any) *presentation.Response {
			return life.DropConfirm(c, v.(life.ItemDroppedView))
		}, nil},
		{life.ScreenItemDropped, func(c presentation.Ctx, v any) *presentation.Response {
			return life.ItemDropped(c, v.(life.ItemDroppedView))
		}, nil},
		{life.ScreenItemRefusal, func(c presentation.Ctx, v any) *presentation.Response {
			return life.ItemRefusal(c, v.(life.ItemRefusalView))
		}, nil},
		{life.ScreenPropertyMarket, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyMarket(c, v.(life.PropertyMarketView))
		}, []func(any){func(v any) { v.(*life.PropertyMarketView).NoCity = false }}},
		{life.ScreenPropertyType, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyType(c, v.(life.PropertyTypeView))
		}, []func(any){
			func(v any) { x := v.(*life.PropertyTypeView); x.Blocked = "" },
			func(v any) { x := v.(*life.PropertyTypeView); x.Blocked, x.Way = "", nil },
		}},
		{life.ScreenPropertyOffer, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyOffer(c, v.(life.PropertyOfferView))
		}, []func(any){
			func(v any) { x := v.(*life.PropertyOfferView); x.Blocked = "" },
			func(v any) { x := v.(*life.PropertyOfferView); x.Blocked, x.Way = "", nil },
			func(v any) {
				x := v.(*life.PropertyOfferView)
				x.Blocked, x.Way, x.Offer.Kind = "", nil, life.OfferRent
			},
		}},
		{life.ScreenPropertyMine, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyMine(c, v.(life.PropertyMineView))
		}, []func(any){func(v any) { v.(*life.PropertyMineView).RestIn = 0 }}},
		{life.ScreenProperty, func(c presentation.Ctx, v any) *presentation.Response { return life.Property(c, v.(life.PropertyView)) }, []func(any){
			func(v any) { x := v.(*life.PropertyView); x.Property.Tenant = nil },
			func(v any) { x := v.(*life.PropertyView); x.Property.Tenant, x.Property.Offer = nil, nil },
			func(v any) {
				x := v.(*life.PropertyView)
				x.Property.Tenant, x.Property.Offer, x.Property.Debt = nil, nil, 0
			},
		}},
		{life.ScreenPropertyLeave, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyLeave(c, v.(life.PropertyLeaveView))
		}, nil},
		{life.ScreenPropertyRefusal, func(c presentation.Ctx, v any) *presentation.Response {
			return life.PropertyRefusal(c, v.(life.PropertyRefusalView))
		}, []func(any){
			func(v any) { v.(*life.PropertyRefusalView).Back = presentation.RefOfAddress(life.AddrPropertyMarket) },
			func(v any) { v.(*life.PropertyRefusalView).Back = presentation.Ref{} },
		}},
	}
	return cs
}

// TestLifeActions: every action the core lists for a life screen names a
// command the game serves to players, and every button Telegram draws under
// the screen is one of them (Telegram leaves some out for a group).
func TestLifeActions(t *testing.T) {
	for _, tc := range lifeCases() {
		tc := tc
		t.Run(tc.screen, func(t *testing.T) {
			var spec presentation.Spec
			for _, s := range presentation.Specs() {
				if s.Name == tc.screen {
					spec = s
				}
			}
			if spec.Name == "" {
				t.Fatalf("screen %s is not defined", tc.screen)
			}
			variants := []func(any){nil}
			if onlyTweaks[tc.screen] {
				variants = nil
			}
			variants = append(variants, tc.tweaks...)
			for i, tweak := range variants {
				ptr := reflect.New(spec.View)
				ptr.Elem().Set(fill(spec.View, 0))
				if tweak != nil {
					tweak(ptr.Interface())
				}
				view := ptr.Elem().Interface()
				resp := tc.build(presentation.Ctx{Lang: "fa"}, view)
				if resp.Screen != tc.screen {
					t.Fatalf("answers as %q", resp.Screen)
				}
				neutral := map[string]bool{}
				for _, a := range resp.Actions {
					if !commands.FromPlayerCommand(a.Command) {
						t.Errorf("variant %d: action %s names %q, which the game does not serve to players", i, a.Key(), a.Command)
						continue
					}
					cmd, _, err := routing.ParseCallbackData(a.Address())
					if err != nil || cmd != a.Command {
						t.Errorf("variant %d: action %s address %q parses to %q (%v)", i, a.Key(), a.Address(), cmd, err)
					}
					neutral[a.Address()] = true
				}
				for _, shared := range []bool{false, true} {
					out := directRender(t, tc.screen, screens.Context{Msgs: keyTranslator{}, Lang: "fa", Shared: shared}, view)
					if out.Keyboard == nil {
						continue
					}
					for _, row := range out.Keyboard.Rows {
						for _, b := range row {
							data := strings.Replace(b.CallbackData, "ask:", "", 1)
							if strings.HasPrefix(b.CallbackData, "ask:") {
								data = strings.Replace(data, ".", ":", 1)
							}
							if data != "" && !neutral[data] {
								t.Errorf("variant %d (shared=%v): Telegram offers %q (%s) but the core lists no such action", i, shared, b.CallbackData, strings.TrimSpace(b.Text))
							}
						}
					}
				}
			}
		})
	}
}

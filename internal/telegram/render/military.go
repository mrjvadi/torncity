package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/military"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The military, war and defence area (docs/adr/0039): a country's ministry of
// defence and forces, stationing, procurement, the war board and its flows,
// the war room and operations, and the defence licences, with the notices of
// the armed forces and the war. Each is drawn for Telegram by the renderer
// internal/telegram/screens has always had, from the view the core now sends
// as data.
func init() {
	Register(military.ScreenMinistry, screens.Ministry)
	Register(military.ScreenForces, screens.Forces)
	Register(military.ScreenBranch, screens.Branch)
	Register(military.ScreenStation, screens.Station)
	Register(military.ScreenProcure, screens.Procure)
	Register(military.ScreenArmsBuy, screens.ArmsBuy)
	Register(military.ScreenMilitaryRefusal, screens.MilitaryRefusal)
	Register(military.ScreenWarBoard, screens.WarBoard)
	Register(military.ScreenWarDeclare, screens.Declare)
	Register(military.ScreenWarDecision, screens.WarDecision)
	Register(military.ScreenWarRoom, screens.WarRoom)
	Register(military.ScreenWarTarget, screens.WarTarget)
	Register(military.ScreenWarLaunch, screens.WarLaunch)
	Register(military.ScreenStrikeReport, screens.StrikeReport)
	Register(military.ScreenWarRefusal, screens.WarRefusal)
	Register(military.ScreenWarBlocked, screens.WarBlocked)
	Register(military.ScreenCompanyDefence, screens.CompanyDefence)
	Register(military.ScreenLicences, screens.Licences)
	Register(military.ScreenKitPurchase, screens.KitPurchase)
	Register(military.ScreenStateRetrofit, screens.StateRetrofit)
	Register(military.ScreenMoveArrivedNotice, screens.MoveArrivedNotice)
	Register(military.ScreenLicenceNotice, screens.LicenceNotice)
	Register(military.ScreenWarNotice, screens.WarNotice)
}

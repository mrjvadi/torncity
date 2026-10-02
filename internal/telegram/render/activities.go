package render

import (
	"github.com/mrjvadi/torncity/internal/presentation/life"
	"github.com/mrjvadi/torncity/internal/presentation/village"
	"github.com/mrjvadi/torncity/internal/telegram/screens"
)

// The Activities hub, the work home and the health home (docs/adr/0038).
func init() {
	Register(life.ScreenActivitiesHub, screens.ActivitiesHub)
	Register(life.ScreenEconomyHub, screens.EconomyHub)
	Register(life.ScreenSocietyHub, screens.SocietyHub)
	Register(life.ScreenHealthHome, screens.HealthHome)
	Register(village.ScreenWorkHome, screens.WorkHome)
}

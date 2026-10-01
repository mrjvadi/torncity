package military

// Addresses of commands other areas own, which the military screens point to.
const (
	// AddrHome is the player's profile.
	AddrHome = "player:profile.get"
	// AddrGovCity is a city's government, which the ministry sits under.
	AddrGovCity = "gov:city"
	// AddrHospital is the hospital, where a strike may have put the player.
	AddrHospital = "health:hospital"
	// AddrCompanyManage and AddrLab are a company's page and its laboratory.
	AddrCompanyManage = "company:manage"
	AddrLab           = "company:lab"
	// AddrTravelOptions is the choice of transport to one city.
	AddrTravelOptions = "travel:options"
)

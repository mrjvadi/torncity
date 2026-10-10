package handlers

// StaffFillers names, for each staff role that no generated workplace's shift and no daily service seat fills, the code
// that fills its posts. The content audit (docs/adr/0061) reads it: a role of a function a player can reach that is on
// neither this list nor a workplace or a daily service has nobody to fill it. Add a role here only with the code that
// really seats someone in it.
var StaffFillers = map[string]string{
	"scholar":      "a scholar's post at the research desk (village_research_desk.go)",
	"storekeeper":  "a keeper seat held by an NPC of the labour pool (village_storage.go)",
	"market_clerk": "the market day's clerk, an NPC of the labour pool (village_trade.go)",
	"shopkeeper":   "the village shop's keeper, an NPC of the labour pool (village_shop.go)",
}

// FunctionRuntimes names, for each function or settlement building that is not a generated workplace or a plain
// producer, the code that reads it. A building that is on neither this list nor a workplace only adds a percentage.
var FunctionRuntimes = map[string]string{
	"dwelling":        "housing capacity of the lot rules (village_lot.go)",
	"private_cottage": "housing capacity (village_lot.go, population)",
	"private_house":   "housing capacity (village_lot.go, population)",
	"cottage":         "housing capacity (population)",
	"housing_block":   "housing capacity (population)",
	"private_shed":    "personal storage (village_lot.go)",
	"road":            "the road network (roads.go)",
	"civic_hall":      "the seat of the head: offices, permissions, charter (village_governance.go)",
	"barter_post":     "the market day and the stalls' book (village_trade.go)",
	"general_store":   "the village shop (village_shop.go)",
	"granary":         "storage rooms and keepers (village_storage.go)",
	"storehouse":      "storage rooms and keepers (village_storage.go)",
	"library":         "the research desk (village_research_desk.go)",
	"laboratory":      "the research desk (village_research_desk.go)",
	"academy":         "the research desk (village_research_desk.go)",
	"teaching_circle": "a class building of the education handler (education.go)",
	"school":          "a class building of the education handler (education.go)",
	"health_house":    "the health service day (village_service.go)",
	"watch_hut":       "the watch post's service day (village_service.go)",
	"training_ground": "the training ground of the settlement gym (training.go)",
	"barracks":        "the military base (military_base.go)",
}

package screens

import (
	"time"

	"github.com/mrjvadi/torncity/internal/application"
	"github.com/mrjvadi/torncity/internal/telegram/presenter"
)

// Missions join the snapshot harness as an area of their own —
// testdata/snapshots/<language>/missions.txt.
func init() { snapshotAreas["missions"] = missionSnapshots }

func missionSnapshots(c Context, who people, add func(string, *presenter.Response)) {
	cityHall := MissionBoardRef{Code: "city_hall", Name: "Civic noticeboard", Place: Named{Code: "city_hall", Name: "Civic centre"}}
	police := MissionBoardRef{Code: "police", Name: "Police board", Place: Named{Code: "police_station", Name: "Police station"}}
	first := Named{Code: "first_steps", Name: "First steps"}
	courier := Named{Code: "city_courier", Name: "City courier"}
	drive := Named{Code: "medicine_drive", Name: "Medicine drive"}
	study := Named{Code: "study_up", Name: "Back to school"}
	bandage := Named{Code: "bandage", Name: "Bandage"}
	sandwich := Named{Code: "sandwich", Name: "Sandwich"}

	add("Boards · the city's", MissionBoard(c, MissionBoardView{CityCode: "ostmarch", City: "Ostmarch",
		Boards: []MissionBoardRef{cityHall, police}}))
	add("Board · at the civic centre", MissionBoard(c, MissionBoardView{CityCode: "ostmarch", City: "Ostmarch",
		Board: &cityHall, Here: true, Missions: []MissionLine{
			{Mission: first, Reward: MissionReward{Cash: 300, XP: 40, Items: []LootLine{{Item: sandwich, Qty: 2}}}},
			{Mission: study, Reward: MissionReward{Cash: 500, XP: 60}, Blocked: "requires"},
			{Mission: courier, Reward: MissionReward{Cash: 250, XP: 25}, Blocked: "cooldown", Wait: 14 * time.Minute, Repeatable: true},
			{Mission: drive, Reward: MissionReward{Cash: 450, XP: 30}, Blocked: "active", Repeatable: true},
		}}))
	add("Board · read in a group, elsewhere", MissionBoard(group(c), MissionBoardView{CityCode: "ostmarch", City: "Ostmarch",
		Board: &police, Missions: []MissionLine{{Mission: Named{Code: "road_watch", Name: "Road watch"},
			Reward: MissionReward{Cash: 600, XP: 50}, Repeatable: true}}}))

	add("Mission · open to take", Mission(c, MissionView{Mission: first, Board: cityHall, MinLevel: 1,
		Objectives: []MissionObjective{
			{Kind: "work_shift", Count: 2},
			{Kind: "buy_item", Target: MissionTarget{Kind: TargetItem, Code: "bandage", Name: "Bandage"}, Count: 1}},
		Reward: MissionReward{Cash: 300, XP: 40, Items: []LootLine{{Item: sandwich, Qty: 2}}}, Max: 5}))
	add("Mission · daily, on its cooldown", Mission(c, MissionView{Mission: courier, Board: cityHall, MinLevel: 2,
		Objectives: []MissionObjective{{Kind: "travel", Count: 1}}, Reward: MissionReward{Cash: 250, XP: 25},
		Repeatable: true, Cooldown: 24 * time.Minute, TimeLimit: 12 * time.Minute, Blocked: "cooldown", Wait: 14 * time.Minute, Max: 5}))
	add("Mission · after another", Mission(c, MissionView{Mission: study, Board: cityHall,
		Objectives: []MissionObjective{{Kind: "course", Count: 1}}, Reward: MissionReward{Cash: 500, XP: 60},
		Requires: []Named{first}, Blocked: "requires", Max: 5}))
	add("Mission · too many in hand", Mission(c, MissionView{Mission: drive, Board: cityHall,
		Objectives: []MissionObjective{{Kind: "deliver", Target: MissionTarget{Kind: TargetItem, Code: "bandage", Name: "Bandage"}, Count: 3}},
		Reward:     MissionReward{Cash: 450, XP: 30}, Repeatable: true, Cooldown: 24 * time.Minute, Blocked: "too_many", Max: 5}))
	add("Mission · giving it up", Mission(c, MissionView{Mission: drive, No: 17, Abandoning: true}))

	objectives := []MissionObjective{
		{Kind: "work_shift", Target: MissionTarget{Kind: TargetCareerCategory, Code: "transport"}, Count: 2, Done: 1},
		{Kind: "travel", Target: MissionTarget{Kind: TargetCity, Code: "brennhaven", Name: "Brennhaven"}, Count: 1, Done: 1},
		{Kind: "use_item", Target: MissionTarget{Kind: TargetItemCategory, Code: "medicine"}, Count: 2},
		{Kind: "crime", Target: MissionTarget{Kind: TargetCrimeCategory, Code: "petty_theft", Name: "Petty theft"}, Count: 3},
		{Kind: "sell_item", Count: 3, Done: 2},
		{Kind: "course", Target: MissionTarget{Kind: TargetCourse, Code: "first_aid", Name: "First aid"}, Count: 1},
	}
	add("Mine · two in hand, one done lately", MissionsMine(c, MissionsMineView{Max: 5,
		Notice: &MissionNotice{Kind: MissionNoticeDelivered, Mission: drive, Qty: 2},
		Active: []MissionProgressLine{
			{No: 17, Mission: drive, Status: application.MissionActive, Deliver: true,
				Objectives: []MissionObjective{{Kind: "deliver", Target: MissionTarget{Kind: TargetItem, Code: "bandage", Name: "Bandage"}, Count: 3, Done: 2}}},
			{No: 18, Mission: Named{Code: "road_watch", Name: "Road watch"}, Status: application.MissionActive, Left: 9 * time.Minute,
				Objectives: objectives},
		},
		Recent: []MissionProgressLine{{Mission: first, Status: application.MissionCompleted, Cash: 300},
			{Mission: courier, Status: application.MissionExpired}}}))
	add("Mine · completed, a cap kept some back", MissionsMine(c, MissionsMineView{Max: 5,
		Notice: &MissionNotice{Kind: MissionNoticeCompleted, Mission: drive, Cash: 150, Withheld: 300, XP: 30}}))
	add("Mine · just taken", MissionsMine(c, MissionsMineView{Max: 5, Notice: &MissionNotice{Kind: MissionNoticeAccepted, Mission: first},
		Active: []MissionProgressLine{{No: 19, Mission: first, Status: application.MissionActive,
			Objectives: []MissionObjective{{Kind: "work_shift", Count: 2}, {Kind: "buy_item", Target: MissionTarget{Kind: TargetItem, Code: "bandage", Name: "Bandage"}, Count: 1}}}}}))

	add("Notice · a mission completed", MissionCompletedNotice(sent(c), MissionCompletedView{Mission: first, Cash: 300, XP: 40,
		Items: []LootLine{{Item: sandwich, Qty: 2}}}))
	add("Notice · completed, capped", MissionCompletedNotice(sent(c), MissionCompletedView{Mission: drive, Cash: 150, Withheld: 300,
		XP: 30, Items: []LootLine{{Item: bandage, Qty: 1}}}))

	for _, kind := range []string{MissionRefusedNotFound, MissionRefusedNotHere, MissionRefusedNotActive, MissionRefusedExpired,
		MissionRefusedNothingToDeliver} {
		add("Refused · "+kind, MissionRefusal(c, MissionRefusalView{Kind: kind}))
	}
	add("Refused · level", MissionRefusal(c, MissionRefusalView{Kind: MissionRefusedBlocked, Blocked: "level", Level: 2}))
	add("Not here · take it at the board", NotHere(c, NotHereView{Need: "place.need.board",
		NeedArgs: map[string]any{"board": c.boardName(cityHall)}, Place: cityHall.Place,
		Here: Named{Code: "bazaar", Name: "Bazaar"}, Walk: 10 * time.Second, Then: "mission.board", ThenArgs: []string{"city_hall"}}))
	add("Pay · held for review", PaySent(c, PaySentView{PayeeName: who.friend, PayeeCode: friendCode, Method: PayCard,
		Amount: 80000, Fee: 400, Held: true}))
}

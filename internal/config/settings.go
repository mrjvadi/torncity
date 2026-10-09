package config

import (
	"fmt"
	"strings"
	"time"
)

// This file holds the two things that have to stay in step: the shape of the
// yaml, and the table that maps every yaml key to the Config field behind it.
//
// The table is deliberately the only route into a field. Defaults, file
// values, environment overrides and validation all walk it, so a field cannot
// be file-settable but not environment-settable, or settable but never
// checked. Adding a value to the project means adding one line here and one
// line to configs/config.yml, and everything else follows.

// fileConfig mirrors configs/config.yml exactly.
//
// It is separate from Config for two reasons. Durations arrive as strings such
// as "30s", which no numeric Go type will decode on its own, and converting
// them in one named place is what lets a bad one be reported as
// "gateway.poll_timeout: \"3zz\" is not a duration" rather than as a line
// number alone. And every field is a pointer, so "the key is absent" and "the
// key was set to zero" are different facts: the first keeps the default, the
// second is a validation error, and conflating them would turn a typo into an
// unbounded timeout.
type fileConfig struct {
	Gateway       gatewaySettings       `yaml:"gateway"`
	Lease         leaseSettings         `yaml:"lease"`
	RateLimit     ratelimitSettings     `yaml:"ratelimit"`
	Telegram      telegramSettings      `yaml:"telegram"`
	Groups        groupsSettings        `yaml:"groups"`
	Menu          menuSettings          `yaml:"menu"`
	Dedup         dedupSettings         `yaml:"dedup"`
	NATS          natsSettings          `yaml:"nats"`
	Worker        workerSettings        `yaml:"worker"`
	Scheduler     schedulerSettings     `yaml:"scheduler"`
	Notifier      notifierSettings      `yaml:"notifier"`
	Game          gameSettings          `yaml:"game"`
	Travel        travelSettings        `yaml:"travel"`
	Player        playerSettings        `yaml:"player"`
	Economy       economySettings       `yaml:"economy"`
	Governance    governanceSettings    `yaml:"governance"`
	Crime         crimeSettings         `yaml:"crime"`
	Trade         tradeSettings         `yaml:"trade"`
	Company       companySettings       `yaml:"company"`
	Military      militarySettings      `yaml:"military"`
	Diplomacy     diplomacySettings     `yaml:"diplomacy"`
	War           warSettings           `yaml:"war"`
	Missions      missionsSettings      `yaml:"missions"`
	Factions      factionsSettings      `yaml:"factions"`
	AntiCheat     antiCheatSettings     `yaml:"anticheat"`
	Input         inputSettings         `yaml:"input"`
	Announce      announceSettings      `yaml:"announce"`
	Notifications notificationsSettings `yaml:"notifications"`
	WorldGen      worldgenSettings      `yaml:"worldgen"`
	Settlement    settlementSettings    `yaml:"settlement"`
	Growth        growthSettings        `yaml:"growth"`
	Bag           bagSettings           `yaml:"bag"`
	Merchant      merchantSettings      `yaml:"merchant"`
	Premium       premiumSettings       `yaml:"premium"`

	Legislature  legislatureSettings  `yaml:"legislature"`
	Currency     currencySettings     `yaml:"currency"`
	Labor        laborSettings        `yaml:"labor"`
	Education    educationSettings    `yaml:"education"`
	Training     trainingSettings     `yaml:"training"`
	City         citySettings         `yaml:"city"`
	Property     propertySettings     `yaml:"property"`
	Achievements achievementsSettings `yaml:"achievements"`
	Postgres     postgresSettings     `yaml:"postgres"`

	Panel panelSettings `yaml:"panel"`

	Client   clientSettings   `yaml:"client"`
	Realtime realtimeSettings `yaml:"realtime"`

	StateSync stateSyncSettings `yaml:"state_sync"`
}

type educationSettings struct {
	TeacherWageBPS     *int64 `yaml:"teacher_wage_bps"`
	TeacherMinWage     *int64 `yaml:"teacher_min_wage"`
	TeacherMaxStudents *int64 `yaml:"teacher_max_students"`
}

type trainingSettings struct {
	EnergyCost          *int64 `yaml:"energy_cost"`
	StaminaGain         *int64 `yaml:"stamina_gain"`
	StrengthXP          *int64 `yaml:"strength_xp"`
	DiminishStamina     *int64 `yaml:"diminish_stamina"`
	StaminaPerMaxEnergy *int64 `yaml:"stamina_per_max_energy"`
	MaxEnergyBonusCap   *int64 `yaml:"max_energy_bonus_cap"`
	YardBPS             *int64 `yaml:"yard_bps"`
	GroundBPS           *int64 `yaml:"ground_bps"`
	GymBPS              *int64 `yaml:"gym_bps"`
	GroundFee           *int64 `yaml:"ground_fee"`
	GymFee              *int64 `yaml:"gym_fee"`
}

type laborSettings struct {
	ShiftMinutes           *int64  `yaml:"shift_minutes"`
	ShiftRealMinutes       *int64  `yaml:"shift_real_minutes"`
	ReferenceCrew          *int64  `yaml:"reference_crew"`
	BaseWage               *int64  `yaml:"base_wage"`
	MinWageVillage         *int64  `yaml:"min_wage_village"`
	MinWageTown            *int64  `yaml:"min_wage_town"`
	MinWageCity            *int64  `yaml:"min_wage_city"`
	ParticipationBPS       *int64  `yaml:"participation_bps"`
	BaseHousing            *int64  `yaml:"base_housing"`
	NPCProductivityBPS     *int64  `yaml:"npc_productivity_bps"`
	FeeBPS                 *int64  `yaml:"fee_bps"`
	BudgetSlackBPS         *int64  `yaml:"budget_slack_bps"`
	NPCShiftsPerSlotDay    *int64  `yaml:"npc_shifts_per_slot_day"`
	HungryOutputBPS        *int64  `yaml:"hungry_output_bps"`
	HungryShiftHunger      *int64  `yaml:"hungry_shift_hunger"`
	RepairMaterialShareBPS *int64  `yaml:"repair_material_share_bps"`
	RepairShiftsFull       *int64  `yaml:"repair_shifts_full"`
	WornOutputBPS          *int64  `yaml:"worn_output_bps"`
	ClosedBPS              *int64  `yaml:"closed_bps"`
	WornBPS                *int64  `yaml:"worn_bps"`
	RepairBelowBPS         *int64  `yaml:"repair_below_bps"`
	DecayBPSPerDay         *int64  `yaml:"decay_bps_per_day"`
	JourneymanShifts       *int64  `yaml:"journeyman_shifts"`
	MasterShifts           *int64  `yaml:"master_shifts"`
	ApprenticeBPS          *int64  `yaml:"apprentice_bps"`
	JourneymanBPS          *int64  `yaml:"journeyman_bps"`
	MasterBPS              *int64  `yaml:"master_bps"`
	TightBalancedBPS       *int64  `yaml:"tight_balanced_bps"`
	TightTightBPS          *int64  `yaml:"tight_tight_bps"`
	TightShortBPS          *int64  `yaml:"tight_short_bps"`
	WageSlackBPS           *int64  `yaml:"wage_slack_bps"`
	WageBalancedBPS        *int64  `yaml:"wage_balanced_bps"`
	WageTightBPS           *int64  `yaml:"wage_tight_bps"`
	WageShortBPS           *int64  `yaml:"wage_short_bps"`
	HirePresets            []int64 `yaml:"hire_presets"`
	WagePresets            []int64 `yaml:"wage_presets"`
}

type postgresSettings struct {
	MaxConns                 *int    `yaml:"max_conns"`
	IdleInTransactionTimeout *string `yaml:"idle_in_transaction_timeout"`
}

type legislatureSettings struct {
	VoteWindow *string `yaml:"vote_window"`
	ListSize   *int    `yaml:"list_size"`
}

type citySettings struct {
	Period *string `yaml:"period"`
}

type propertySettings struct {
	ForeclosurePeriods *int    `yaml:"foreclosure_periods"`
	EvictionPeriods    *int    `yaml:"eviction_periods"`
	MaxOwned           *int    `yaml:"max_owned"`
	MaxPrice           *int64  `yaml:"max_price"`
	MaxRent            *int64  `yaml:"max_rent"`
	RestCooldown       *string `yaml:"rest_cooldown"`
}

type achievementsSettings struct {
	PlayerDailyCap  *int64 `yaml:"player_daily_cap"`
	EconomyDailyCap *int64 `yaml:"economy_daily_cap"`
}

type gatewaySettings struct {
	PollTimeout           *string `yaml:"poll_timeout"`
	PollErrorBackoff      *string `yaml:"poll_error_backoff"`
	ShutdownTimeout       *string `yaml:"shutdown_timeout"`
	SendAttempts          *int    `yaml:"send_attempts"`
	RedirectCooldown      *string `yaml:"redirect_cooldown"`
	WebAppPrivateCooldown *string `yaml:"webapp_private_cooldown"`
}

type leaseSettings struct {
	TTL            *string `yaml:"ttl"`
	RenewDivisor   *int    `yaml:"renew_divisor"`
	ReleaseTimeout *string `yaml:"release_timeout"`
}

type ratelimitSettings struct {
	DefaultRate  *int `yaml:"default_rate"`
	DefaultBurst *int `yaml:"default_burst"`
}

type telegramSettings struct {
	RequestTimeout   *string `yaml:"request_timeout"`
	MaxPollTimeout   *string `yaml:"max_poll_timeout"`
	PollTimeoutGrace *string `yaml:"poll_timeout_grace"`
	DefaultFloodWait *string `yaml:"default_flood_wait"`
}

type groupsSettings struct {
	CallbackAlertMaxRunes *int    `yaml:"callback_alert_max_runes"`
	DeepLinkTTL           *string `yaml:"deep_link_ttl"`
}

type menuSettings struct {
	Commands []string `yaml:"commands"`
}

type dedupSettings struct {
	TTL *string `yaml:"ttl"`
}

type natsSettings struct {
	CommandMaxAge   *string  `yaml:"command_max_age"`
	EventMaxAge     *string  `yaml:"event_max_age"`
	DuplicateWindow *string  `yaml:"duplicate_window"`
	AckWait         *string  `yaml:"ack_wait"`
	MaxDeliver      *int     `yaml:"max_deliver"`
	NakDelay        *string  `yaml:"nak_delay"`
	Backoff         []string `yaml:"backoff"`
}

type workerSettings struct {
	PollInterval    *string `yaml:"poll_interval"`
	BatchSize       *int    `yaml:"batch_size"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	NoisyAttempts   *int    `yaml:"noisy_attempts"`
}

type schedulerSettings struct {
	TickInterval    *string `yaml:"tick_interval"`
	BatchSize       *int    `yaml:"batch_size"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	NoisyAttempts   *int    `yaml:"noisy_attempts"`
	ClaimTimeout    *string `yaml:"claim_timeout"`
}

type notifierSettings struct {
	SendBudget      *string `yaml:"send_budget"`
	ReceiptMargin   *string `yaml:"receipt_margin"`
	MaxAge          *string `yaml:"max_age"`
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
}

type gameSettings struct {
	ShutdownTimeout *string `yaml:"shutdown_timeout"`
	IdempotencyTTL  *string `yaml:"idempotency_ttl"`

	ContentReloadInterval *string `yaml:"content_reload_interval"`
	TimeScale             *int    `yaml:"time_scale"`
	ClockEpoch            *string `yaml:"clock_epoch"`
	ClockLegacyScale      *int    `yaml:"clock_legacy_scale"`
	ClockCutover          *string `yaml:"clock_cutover"`
	TravelTimeScale       *int    `yaml:"travel_time_scale"`
	CommandTimeout        *string `yaml:"command_timeout"`
}

type bagSettings struct {
	CarryBase      *int64 `yaml:"carry_base"`
	BaseComfortKG  *int64 `yaml:"base_comfort_kg"`
	BaseHardKG     *int64 `yaml:"base_hard_kg"`
	FullShareBPS   *int64 `yaml:"full_share_bps"`
	TornSpaceBPS   *int64 `yaml:"torn_space_bps"`
	RepairShareBPS *int64 `yaml:"repair_share_bps"`
	WearPerDay     *int64 `yaml:"wear_per_day"`
}

type merchantSettings struct {
	RestockHour      *int64  `yaml:"restock_hour"`
	MarkupMinBPS     *int64  `yaml:"markup_min_bps"`
	MarkupMaxBPS     *int64  `yaml:"markup_max_bps"`
	StockDays        *int64  `yaml:"stock_days"`
	FoodShareBPS     *int64  `yaml:"food_share_bps"`
	OtherShareBPS    *int64  `yaml:"other_share_bps"`
	PlayerDayFood    *int64  `yaml:"player_day_food"`
	PlayerDayOther   *int64  `yaml:"player_day_other"`
	SupplyValueFood  *int64  `yaml:"supply_value_per_resident_day"`
	BuildingBoostBPS *int64  `yaml:"building_boost_bps"`
	CapPresets       []int64 `yaml:"cap_presets"`
	BuyPresets       []int64 `yaml:"buy_presets"`
	TaxDefaultBPS    *int64  `yaml:"tax_default_bps"`
	TaxMaxBPS        *int64  `yaml:"tax_max_bps"`
	TaxPresets       []int64 `yaml:"tax_presets"`
	OutputDays       *int64  `yaml:"output_days"`
}

type premiumSettings struct {
	NilUnitSup  *int64  `yaml:"nil_unit_sup"`
	NilExamples []int64 `yaml:"nil_examples"`
}

type travelSettings struct {
	ArrivalXP     *int     `yaml:"arrival_xp"`
	CityLocations []string `yaml:"city_locations"`
	WorldReach    []string `yaml:"world_reach"`
	// TimeScale is the legacy spelling of game.time_scale; see Game.
	TimeScale *int `yaml:"time_scale"`
}

type playerSettings struct {
	DefaultLanguage *string `yaml:"default_language"`
	DefaultTimezone *string `yaml:"default_timezone"`
}

type economySettings struct {
	StartingCash     *int64  `yaml:"starting_cash"`
	BankMinAmount    *int64  `yaml:"bank_min_amount"`
	BankMaxAmount    *int64  `yaml:"bank_max_amount"`
	BankQuickAmounts []int64 `yaml:"bank_quick_amounts"`
}

type inputSettings struct {
	TTL       *string `yaml:"ttl"`
	Cooldown  *string `yaml:"cooldown"`
	MaxLength *int    `yaml:"max_length"`
}

type announceSettings struct {
	Window       *string `yaml:"window"`
	MaxPerWindow *int    `yaml:"max_per_window"`

	VillageMergeWindow   *string `yaml:"village_merge_window"`
	VillageMinGap        *string `yaml:"village_min_gap"`
	VillageFlushInterval *string `yaml:"village_flush_interval"`
	VillageLiteracyStep  *int    `yaml:"village_literacy_step_percent"`
}

type notificationsSettings struct {
	InboxPageSize         *int    `yaml:"inbox_page_size"`
	EditThrottle          *string `yaml:"edit_throttle"`
	ReminderDelay         *string `yaml:"reminder_delay"`
	ReminderCheckInterval *string `yaml:"reminder_check_interval"`
	Retention             *string `yaml:"retention"`
	PruneInterval         *string `yaml:"prune_interval"`
	HungerAlertCooldown   *string `yaml:"hunger_alert_cooldown"`
	VitalsMinInterval     *string `yaml:"vitals_min_interval"`
}

type governanceSettings struct {
	FineStepDivisor   *int `yaml:"fine_step_divisor"`
	CoarseStepDivisor *int `yaml:"coarse_step_divisor"`
	AllocationStepBPS *int `yaml:"allocation_step_bps"`
}

type worldgenSettings struct {
	CellCount              *int     `yaml:"cell_count"`
	NeighborK              *int     `yaml:"neighbor_k"`
	PlateCount             *int     `yaml:"plate_count"`
	OceanicPlateFraction   *int     `yaml:"oceanic_plate_fraction_permille"`
	LandFraction           *int     `yaml:"land_fraction_permille"`
	NoiseOctaves           *int     `yaml:"noise_octaves"`
	NoiseBaseFrequency     *float64 `yaml:"noise_base_frequency"`
	NoisePersistence       *int     `yaml:"noise_persistence_permille"`
	WarpAmplitude          *float64 `yaml:"warp_amplitude"`
	WarpFrequency          *float64 `yaml:"warp_frequency"`
	BoundaryInfluenceSteps *int     `yaml:"boundary_influence_steps"`
	MoistureBands          *int     `yaml:"moisture_bands"`
	RiverFlowThreshold     *int     `yaml:"river_flow_threshold"`
	LakeMinDepth           *int     `yaml:"lake_min_depth"`
	LakeMinAreaCells       *int     `yaml:"lake_min_area_cells"`

	PlanetRadiusKm              *float64 `yaml:"planet_radius_km"`
	ChunkBaseLOD                *int     `yaml:"chunk_base_lod"`
	ChunkTileEdge               *int     `yaml:"chunk_tile_edge"`
	ChunkDetailFrequency        *float64 `yaml:"chunk_detail_frequency"`
	ChunkDetailAmplitude        *int     `yaml:"chunk_detail_amplitude"`
	ChunkStreamFrequency        *float64 `yaml:"chunk_stream_frequency"`
	ChunkStreamAmplitude        *int     `yaml:"chunk_stream_amplitude"`
	ChunkDepositTilesPerDeposit *int     `yaml:"chunk_deposit_tiles_per_deposit"`
}

type growthSettings struct {
	Capabilities  *string `yaml:"capabilities"`
	CacheTTL      *string `yaml:"cache_ttl"`
	RuinedBPS     *int    `yaml:"ruined_bps"`
	FlushInterval *string `yaml:"flush_interval"`
}

type settlementSettings struct {
	ProtectionWindow            *string  `yaml:"protection_window"`
	ResidenceCooldown           *string  `yaml:"residence_cooldown"`
	TimezoneCooldown            *string  `yaml:"timezone_cooldown"`
	HomeCityCode                *string  `yaml:"home_city_code"`
	PropertyHubMinStage         *string  `yaml:"property_hub_min_stage"`
	MinSpawnDistanceKm          *float64 `yaml:"min_spawn_distance_km"`
	ThreatRadiusKm              *float64 `yaml:"threat_radius_km"`
	SearchMaxCells              *int     `yaml:"search_max_cells"`
	SearchMaxAttempts           *int     `yaml:"search_max_attempts"`
	SpawnCircleRadiusKm         *float64 `yaml:"spawn_circle_radius_km"`
	SpawnCircleCapacity         *int     `yaml:"spawn_circle_capacity"`
	SpawnCircleFillBandKm       *float64 `yaml:"spawn_circle_fill_band_km"`
	SpawnCircleMaxAdvance       *int     `yaml:"spawn_circle_max_advance"`
	BuildingAreaPerCell         *int     `yaml:"building_area_per_cell"`
	BuildingStoreyTimberPerCell *int     `yaml:"building_storey_timber_per_cell"`
	BuildingStoreyStonePerCell  *int     `yaml:"building_storey_stone_per_cell"`
	BuildingStoreyStoneFrom     *int     `yaml:"building_storey_stone_from"`
	BuildingStoreyShiftsPerCell *int     `yaml:"building_storey_shifts_per_cell"`
	BuildingStoreyKnowledge     []string `yaml:"building_storey_knowledge"`
	BuildingSalvageBPS          *int     `yaml:"building_salvage_bps"`
	UseChangeFeeBPS             *int     `yaml:"use_change_fee_bps"`
	BuildingLookRerolls         *int     `yaml:"building_look_rerolls"`
	BuildingTemplatesMax        *int     `yaml:"building_templates_max"`
	ExcludedBiomes              []string `yaml:"excluded_biomes"`
	MaxAbsLatitudeDeg           *float64 `yaml:"max_abs_latitude_deg"`
	BiomePenalties              []string `yaml:"biome_penalties"`
	VillageGridLots             *int     `yaml:"village_grid_lots"`
	MinBuildableLotShareBps     *int     `yaml:"min_buildable_lot_share_bps"`
	GridShiftMaxLots            *int     `yaml:"grid_shift_max_lots"`
	AutoRoadCost                *int64   `yaml:"auto_road_cost"`
	LotAccessCrossingCost       *int64   `yaml:"lot_access_crossing_cost"`
	LotAccessMaxCrossing        *int     `yaml:"lot_access_max_crossing"`
	StreetPitch                 *int     `yaml:"street_pitch"`
	StreetPlanMinGrid           *int     `yaml:"street_plan_min_grid"`
	RoadFrontageDepthLots       *int     `yaml:"road_frontage_depth_lots"`
	RoadPlanMaxLots             *int     `yaml:"road_plan_max_lots"`
	RoadOpenLotsMax             *int     `yaml:"road_open_lots_max"`
	RoadForeignBufferTiles      *int     `yaml:"road_foreign_buffer_tiles"`
	RoadSteepSlopeM             *int     `yaml:"road_steep_slope_m"`
	RoadCorridorRingTiles       *int     `yaml:"road_corridor_ring_tiles"`
	RoadTrackCostBPS            *int     `yaml:"road_track_cost_bps"`

	FoundingDraftTTL          *string `yaml:"founding_draft_ttl"`
	FoundingNameMin           *int    `yaml:"founding_name_min"`
	FoundingNameMax           *int    `yaml:"founding_name_max"`
	FoundingMottoMax          *int    `yaml:"founding_motto_max"`
	FoundingCurrencyNameMin   *int    `yaml:"founding_currency_name_min"`
	FoundingCurrencyNameMax   *int    `yaml:"founding_currency_name_max"`
	FoundingCurrencyCodeLen   *int    `yaml:"founding_currency_code_len"`
	FoundingCurrencySymbolMax *int    `yaml:"founding_currency_symbol_max"`

	TeachPeriod                      *string `yaml:"teach_period"`
	TeachRateBPS                     *int64  `yaml:"teach_rate_bps"`
	BaseSchoolCapacityBPS            *int64  `yaml:"base_school_capacity_bps"`
	ScarcityKBPS                     *int64  `yaml:"scarcity_k_bps"`
	ScarcityFloorBPS                 *int64  `yaml:"scarcity_floor_bps"`
	ScarcityCapBPS                   *int64  `yaml:"scarcity_cap_bps"`
	SellerBandBPS                    *int64  `yaml:"seller_band_bps"`
	DemolitionSalvageBPS             *int64  `yaml:"demolition_salvage_bps"`
	MaterialMarkupBPS                *int64  `yaml:"material_markup_bps"`
	StockBaseCapacity                *int64  `yaml:"stock_base_capacity"`
	BuildHomesPerCrew                *int64  `yaml:"build_homes_per_crew"`
	CharterMaxOffices                *int64  `yaml:"charter_max_offices"`
	CharterMaxSeats                  *int64  `yaml:"charter_max_seats"`
	CharterMaxPermissions            *int64  `yaml:"charter_max_permissions"`
	CharterTitleMin                  *int64  `yaml:"charter_title_min"`
	CharterTitleMax                  *int64  `yaml:"charter_title_max"`
	CharterElectionTermDays          *int64  `yaml:"charter_election_term_days"`
	CharterCandidacyHours            *int64  `yaml:"charter_candidacy_hours"`
	CharterVotingHours               *int64  `yaml:"charter_voting_hours"`
	CharterRecallMinTenureDays       *int64  `yaml:"charter_recall_min_tenure_days"`
	CharterRecallSignatureBPS        *int64  `yaml:"charter_recall_signature_bps"`
	CharterRecallMinSignatures       *int64  `yaml:"charter_recall_min_signatures"`
	CharterRecallVoteHours           *int64  `yaml:"charter_recall_vote_hours"`
	CharterRecallCooldownDays        *int64  `yaml:"charter_recall_cooldown_days"`
	CharterAmendVoteHours            *int64  `yaml:"charter_amend_vote_hours"`
	CharterAmendQuorumBPS            *int64  `yaml:"charter_amend_quorum_bps"`
	CharterAmendVoteMinResidents     *int64  `yaml:"charter_amend_vote_min_residents"`
	CharterActingDays                *int64  `yaml:"charter_acting_days"`
	CharterActingSpendCap            *int64  `yaml:"charter_acting_spend_cap"`
	CharterMinResidencyDays          *int64  `yaml:"charter_min_residency_days"`
	StorageSpoilKeptBPS              *int64  `yaml:"storage_spoil_kept_bps"`
	StorageSpoilUnkeptBPS            *int64  `yaml:"storage_spoil_unkept_bps"`
	StorageKeeperRuleAt              *string `yaml:"storage_keeper_rule_at"`
	StorageKeeperGraceDays           *int64  `yaml:"storage_keeper_grace_days"`
	ResearchFreeSlots                *int64  `yaml:"research_free_slots"`
	ResearchSpeedFloorBPS            *int64  `yaml:"research_speed_floor_bps"`
	ResearchScholarFloorBPS          *int64  `yaml:"research_scholar_floor_bps"`
	ResearchSkillBPSPerLevel         *int64  `yaml:"research_skill_bps_per_level"`
	ResearchScholarCapBPS            *int64  `yaml:"research_scholar_cap_bps"`
	ResearchNPCScholarLevel          *int64  `yaml:"research_npc_scholar_level"`
	ResearchLiteracyBonusBPS         *int64  `yaml:"research_literacy_bonus_bps"`
	ResearchCatchUpBPS               *int64  `yaml:"research_catch_up_bps"`
	ResearchEraBaseDepth             *int64  `yaml:"research_era_base_depth"`
	ResearchEraShareBPS              *int64  `yaml:"research_era_share_bps"`
	ResearchAheadPerStepBPS          *int64  `yaml:"research_ahead_per_step_bps"`
	ResearchAheadCapBPS              *int64  `yaml:"research_ahead_cap_bps"`
	ResearchSharePerPartnerBPS       *int64  `yaml:"research_share_per_partner_bps"`
	ResearchShareCapBPS              *int64  `yaml:"research_share_cap_bps"`
	ResearchBreakthroughNeedPerDepth *int64  `yaml:"research_breakthrough_need_per_depth"`
	ResearchBreakthroughMaxBPS       *int64  `yaml:"research_breakthrough_max_bps"`
	ResearchExperiencePerShift       *int64  `yaml:"research_experience_per_shift"`
	ResearchScholarXP                *int64  `yaml:"research_scholar_xp"`
	MaterialBuyMax                   *int64  `yaml:"material_buy_max"`
	MaterialBuyPresets               []int64 `yaml:"material_buy_presets"`

	FoundingGrant   *int64  `yaml:"founding_grant"`
	DonationMin     *int64  `yaml:"donation_min"`
	DonationMax     *int64  `yaml:"donation_max"`
	DonationPresets []int64 `yaml:"donation_presets"`

	CitizenLotPrice           *int64  `yaml:"citizen_lot_price"`
	CitizenLotPriceMin        *int64  `yaml:"citizen_lot_price_min"`
	CitizenLotPriceMax        *int64  `yaml:"citizen_lot_price_max"`
	CitizenPermitFee          *int64  `yaml:"citizen_permit_fee"`
	CitizenPermitFeeMax       *int64  `yaml:"citizen_permit_fee_max"`
	CitizenTaxBPS             *int    `yaml:"citizen_tax_bps"`
	CitizenTaxBPSMax          *int    `yaml:"citizen_tax_bps_max"`
	CitizenTaxPeriod          *string `yaml:"citizen_tax_period"`
	CitizenMaterialMarkupBPS  *int    `yaml:"citizen_material_markup_bps"`
	CitizenMaxLotsPerPlayer   *int    `yaml:"citizen_max_lots_per_player"`
	CitizenPrivateShareMaxBPS *int    `yaml:"citizen_private_share_max_bps"`
	CitizenHomeRestCooldown   *string `yaml:"citizen_home_rest_cooldown"`
	CitizenHomeRestHealth     *int    `yaml:"citizen_home_rest_health"`
	CitizenHomeRestHappiness  *int    `yaml:"citizen_home_rest_happiness"`
}

type crimeSettings struct {
	NerveMax                     *int    `yaml:"nerve_max"`
	NerveRegenAmount             *int    `yaml:"nerve_regen_amount"`
	NerveRegenInterval           *string `yaml:"nerve_regen_interval"`
	HeatMax                      *int    `yaml:"heat_max"`
	HeatDecayPerHour             *int    `yaml:"heat_decay_per_hour"`
	ProtectMinLevel              *int    `yaml:"protect_min_level"`
	ProtectMinAge                *string `yaml:"protect_min_age"`
	ActiveWindow                 *string `yaml:"active_window"`
	ArrivalLinger                *string `yaml:"arrival_linger"`
	VictimCooldown               *string `yaml:"victim_cooldown"`
	ThiefCooldown                *string `yaml:"thief_cooldown"`
	ReportWindow                 *string `yaml:"report_window"`
	InvestigationDuration        *string `yaml:"investigation_duration"`
	InvestigationBaseBPS         *int    `yaml:"investigation_base_bps"`
	InvestigationPerHeatBPS      *int    `yaml:"investigation_per_heat_bps"`
	InvestigationWitnessBonusBPS *int    `yaml:"investigation_witness_bonus_bps"`
	InvestigationEffortWeightBPS *int    `yaml:"investigation_effort_weight_bps"`
	NPCDailyCap                  *int64  `yaml:"npc_daily_cap"`
	GearMaxSuccessBPS            *int    `yaml:"gear_max_success_bps"`
	GearMaxCatchBPS              *int    `yaml:"gear_max_catch_bps"`
	GearMaxWitnessBPS            *int    `yaml:"gear_max_witness_bps"`
	GearMaxSolveBPS              *int    `yaml:"gear_max_solve_bps"`
	GearMaxRewardBPS             *int    `yaml:"gear_max_reward_bps"`
	GearMaxNerve                 *int    `yaml:"gear_max_nerve"`
}

type tradeSettings struct {
	MarketOrderTTL             *string  `yaml:"market_order_ttl"`
	MarketMaxOpenOrders        *int     `yaml:"market_max_open_orders"`
	VillageStallsPost          *int     `yaml:"village_stalls_post"`
	VillageStallsHall          *int     `yaml:"village_stalls_hall"`
	VillageStallsPerPlayerPost *int     `yaml:"village_stalls_per_player_post"`
	VillageStallsPerPlayerHall *int     `yaml:"village_stalls_per_player_hall"`
	MarketDayEveryDays         *int     `yaml:"market_day_every_days"`
	MarketMaxQuantity          *int     `yaml:"market_max_quantity"`
	MarketMaxPrice             *int64   `yaml:"market_max_price"`
	AuctionDurations           []string `yaml:"auction_durations"`
	AuctionMaxReserve          *int64   `yaml:"auction_max_reserve"`
	AuctionStepBPS             *int     `yaml:"auction_step_bps"`
	AuctionMinStep             *int64   `yaml:"auction_min_step"`
	AuctionMaxOpen             *int     `yaml:"auction_max_open"`
	AuctionReservesBPS         []int64  `yaml:"auction_reserves_bps"`
}

type companySettings struct {
	Period                 *string `yaml:"period"`
	MaxPerPlayer           *int    `yaml:"max_per_player"`
	NameMinLength          *int    `yaml:"name_min_length"`
	NameMaxLength          *int    `yaml:"name_max_length"`
	FoundingShares         *int64  `yaml:"founding_shares"`
	InsolvencyPeriods      *int    `yaml:"insolvency_periods"`
	NPCCityPeriodCap       *int64  `yaml:"npc_city_period_cap"`
	MaxOpenings            *int    `yaml:"max_openings"`
	PriceStepBPS           *int    `yaml:"price_step_bps"`
	CitizenShiftsPerPeriod *int    `yaml:"citizen_shifts_per_period"`
	CitizenProductivityBPS *int    `yaml:"citizen_productivity_bps"`
	CitizenLabourShareBPS  *int    `yaml:"citizen_labour_share_bps"`
	MaxRunningOrders       *int    `yaml:"max_running_orders"`
	MaxDesigns             *int    `yaml:"max_designs"`
	MaxListings            *int    `yaml:"max_listings"`
	DesignMinSkill         *int    `yaml:"design_min_skill"`
	QuickOrderUnits        *int    `yaml:"quick_order_units"`
	ReverseTime            *string `yaml:"reverse_time"`
	ImprovementTime        *string `yaml:"improvement_time"`
	ImprovementCost        *int64  `yaml:"improvement_cost"`
	RetrofitTime           *string `yaml:"retrofit_time"`
	ObsolescenceDecayBPS   *int    `yaml:"obsolescence_decay_bps"`
	ObsolescenceFloorBPS   *int    `yaml:"obsolescence_floor_bps"`
	RecruitCheckEvery      *string `yaml:"recruit_check_every"`
	RecruitChecks          *int    `yaml:"recruit_checks"`
	RecruitMaxCampaigns    *int    `yaml:"recruit_max_campaigns"`
	RecruitMaxPositions    *int    `yaml:"recruit_max_positions"`
	RecruitMaxCandidates   *int    `yaml:"recruit_max_candidates"`
	RecruitPatience        *string `yaml:"recruit_patience"`
	RecruitMaxStaff        *int    `yaml:"recruit_max_staff"`
}

type militarySettings struct {
	Period               *string `yaml:"period"`
	ReadinessLossBPS     *int    `yaml:"readiness_loss_bps"`
	ReadinessRecoveryBPS *int    `yaml:"readiness_recovery_bps"`
	ReferenceRadarKM     *int    `yaml:"reference_radar_km"`
	LicenceRevokeNotice  *string `yaml:"licence_revoke_notice"`
	EndedLicencesShown   *int    `yaml:"ended_licences_shown"`
}

type warSettings struct {
	DeclarationNotice *string `yaml:"declaration_notice"`
	ProposalTTL       *string `yaml:"proposal_ttl"`
	EndedShownFor     *string `yaml:"ended_shown_for"`
	BoardOperations   *int    `yaml:"board_operations"`
	NoticeCap         *int    `yaml:"notice_cap"`
}

type missionsSettings struct {
	MaxActive       *int   `yaml:"max_active"`
	PlayerDailyCap  *int64 `yaml:"player_daily_cap"`
	EconomyDailyCap *int64 `yaml:"economy_daily_cap"`
}

type factionsSettings struct {
	NameMinLength *int `yaml:"name_min_length"`
	NameMaxLength *int `yaml:"name_max_length"`
	MaxMembers    *int `yaml:"max_members"`
	MaxPending    *int `yaml:"max_pending"`
	MinFounders   *int `yaml:"min_founders"`
	ListSize      *int `yaml:"list_size"`
}

type antiCheatSettings struct {
	Window                *string `yaml:"window"`
	OneWayCount           *int    `yaml:"one_way_count"`
	OneWayMinTotal        *int64  `yaml:"one_way_min_total"`
	OneWayRatioBPS        *int    `yaml:"one_way_ratio_bps"`
	OffMarketBPS          *int    `yaml:"off_market_bps"`
	OffMarketMinValue     *int64  `yaml:"off_market_min_value"`
	SinglePartnerMinCount *int    `yaml:"single_partner_min_count"`
	SinglePartnerShareBPS *int    `yaml:"single_partner_share_bps"`
	CommandsPerMinute     *int    `yaml:"commands_per_minute"`
	WashTradeCount        *int    `yaml:"wash_trade_count"`
	HoldAbove             *int64  `yaml:"hold_above"`
}

type diplomacySettings struct {
	SanctionNotice      *string `yaml:"sanction_notice"`
	SanctionMinDuration *string `yaml:"sanction_min_duration"`
	TreatyOfferTTL      *string `yaml:"treaty_offer_ttl"`
	EndedShownFor       *string `yaml:"ended_shown_for"`
	HistoryPageSize     *int    `yaml:"history_page_size"`
}

// setting is one configurable value, from its yaml key to the field it fills.
type setting struct {
	section string
	key     string

	// fromFile copies the decoded yaml into the Config, doing nothing when
	// the key was absent so the default survives.
	fromFile func(*Config, *fileConfig) error

	// fromEnv reads the same field out of an environment variable.
	fromEnv func(*Config, string) error

	// check is the field-level sanity Validate runs on it.
	check func(*Config) error
}

// envName is the override variable: TORN_<SECTION>_<FIELD>, upper-cased, with
// the yaml key's underscores kept. See the package doc.
func (s setting) envName() string {
	return "TORN_" + strings.ToUpper(s.section) + "_" + strings.ToUpper(s.key)
}

func (s setting) name() string { return s.section + "." + s.key }

// durationSetting wires a duration field. Its check rejects zero and below,
// because every Go API that takes a duration reads zero as "no limit".
func durationSetting(section, key string, field func(*Config) *time.Duration, raw func(*fileConfig) *string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		d, err := parseDuration(name, *p)
		if err != nil {
			return err
		}
		*field(c) = d
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		d, err := parseDuration(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = d
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %s", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

// limitSetting wires a whole-number field: a count, a size, a divisor. Its
// check rejects zero and below, because a limit of zero means the loop it
// bounds does nothing at all.
func limitSetting(section, key string, field func(*Config) *int, raw func(*fileConfig) *int) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := parseInt(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %d", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

// floatSetting wires a float64 field — used only where a whole number
// cannot express the value (a noise frequency, a domain-warp amplitude).
// Its check rejects a negative value; zero is allowed, since an amplitude
// of zero is a legitimate "no warp" rather than a typo the way a duration
// or limit of zero usually is.
func floatSetting(section, key string, field func(*Config) *float64, raw func(*fileConfig) *float64) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := parseFloat(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v < 0 {
			return fmt.Errorf("%w: %s is %v", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

// moneySetting wires an amount of money in minor units. It is int64, like
// every money value in the project, and never passes through a float: the
// yaml decoder fills an int64 directly and the environment is parsed with
// strconv.ParseInt. Its check rejects zero and below, for the same reason
// limitSetting does: an amount of zero means the thing it pays does nothing.
func moneySetting(section, key string, field func(*Config) *int64, raw func(*fileConfig) *int64) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		v, err := parseInt64(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = v
		return nil
	}
	s.check = func(c *Config) error {
		if v := *field(c); v <= 0 {
			return fmt.Errorf("%w: %s is %d", ErrNotPositive, name, v)
		}
		return nil
	}
	return s
}

func stringSetting(section, key string, field func(*Config) *string, raw func(*fileConfig) *string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		p := raw(f)
		if p == nil {
			return nil
		}
		*field(c) = *p
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		*field(c) = text
		return nil
	}
	s.check = func(c *Config) error {
		if strings.TrimSpace(*field(c)) == "" {
			return fmt.Errorf("%w: %s", ErrEmpty, name)
		}
		return nil
	}
	return s
}

// stringListSetting wires a list of words. From the environment it is
// comma-separated. Its check rejects an empty list and an empty entry.
func stringListSetting(section, key string, field func(*Config) *[]string, raw func(*fileConfig) []string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}
		*field(c) = append([]string(nil), items...)
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		parts := strings.Split(text, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			out = append(out, strings.TrimSpace(p))
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("%w: %s[%d]", ErrEmpty, name, i)
			}
		}
		return nil
	}
	return s
}

// durationListSetting wires a schedule. The ordering invariant lives in
// Validate; this check covers only what is wrong with the entries themselves.
func durationListSetting(section, key string, field func(*Config) *[]time.Duration, raw func(*fileConfig) []string) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}

		out := make([]time.Duration, 0, len(items))
		for i, item := range items {
			d, err := parseDuration(fmt.Sprintf("%s[%d]", name, i), item)
			if err != nil {
				return err
			}
			out = append(out, d)
		}
		*field(c) = out
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		out, err := parseDurationList(s.envName(), text)
		if err != nil {
			return err
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if v <= 0 {
				return fmt.Errorf("%w: %s[%d] is %s", ErrNotPositive, name, i, v)
			}
		}
		return nil
	}
	return s
}

// moneyListSetting wires a list of amounts in minor units, such as the quick
// amounts on the bank's buttons. From the environment it is comma-separated.
// Its check rejects an empty list and any amount of zero or below.
func moneyListSetting(section, key string, field func(*Config) *[]int64, raw func(*fileConfig) []int64) setting {
	s := setting{section: section, key: key}
	name := s.name()

	s.fromFile = func(c *Config, f *fileConfig) error {
		items := raw(f)
		if items == nil {
			return nil
		}
		*field(c) = append([]int64(nil), items...)
		return nil
	}
	s.fromEnv = func(c *Config, text string) error {
		parts := strings.Split(text, ",")
		out := make([]int64, 0, len(parts))
		for i, p := range parts {
			v, err := parseInt64(fmt.Sprintf("%s[%d]", s.envName(), i), strings.TrimSpace(p))
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		*field(c) = out
		return nil
	}
	s.check = func(c *Config) error {
		values := *field(c)
		if len(values) == 0 {
			return fmt.Errorf("%w: %s", ErrEmptyList, name)
		}
		for i, v := range values {
			if v <= 0 {
				return fmt.Errorf("%w: %s[%d] is %d", ErrNotPositive, name, i, v)
			}
		}
		return nil
	}
	return s
}

// aliasSetting marks a legacy key that still fills a field a current key
// owns. It reads the file and the environment like the setting it wraps, and
// checks nothing of its own: the current key's setting checks the field.
func aliasSetting(s setting) setting {
	s.check = func(*Config) error { return nil }
	return s
}

// settings is the whole configurable surface of this project, in the order
// configs/config.yml declares it.
var settings = append(append(append(coreSettings, panelSettingsTable()...), clientSettingsTable()...), stateSyncSettingsTable()...)

// coreSettings are the game's own settings; the panel's are in panel.go.
var coreSettings = []setting{
	durationSetting("gateway", "poll_timeout",
		func(c *Config) *time.Duration { return &c.Gateway.PollTimeout },
		func(f *fileConfig) *string { return f.Gateway.PollTimeout }),
	durationSetting("gateway", "poll_error_backoff",
		func(c *Config) *time.Duration { return &c.Gateway.PollErrorBackoff },
		func(f *fileConfig) *string { return f.Gateway.PollErrorBackoff }),
	durationSetting("gateway", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Gateway.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Gateway.ShutdownTimeout }),
	limitSetting("gateway", "send_attempts",
		func(c *Config) *int { return &c.Gateway.SendAttempts },
		func(f *fileConfig) *int { return f.Gateway.SendAttempts }),
	durationSetting("gateway", "redirect_cooldown",
		func(c *Config) *time.Duration { return &c.Gateway.RedirectCooldown },
		func(f *fileConfig) *string { return f.Gateway.RedirectCooldown }),
	durationSetting("gateway", "webapp_private_cooldown",
		func(c *Config) *time.Duration { return &c.Gateway.WebAppPrivateCooldown },
		func(f *fileConfig) *string { return f.Gateway.WebAppPrivateCooldown }),

	durationSetting("lease", "ttl",
		func(c *Config) *time.Duration { return &c.Lease.TTL },
		func(f *fileConfig) *string { return f.Lease.TTL }),
	limitSetting("lease", "renew_divisor",
		func(c *Config) *int { return &c.Lease.RenewDivisor },
		func(f *fileConfig) *int { return f.Lease.RenewDivisor }),
	durationSetting("lease", "release_timeout",
		func(c *Config) *time.Duration { return &c.Lease.ReleaseTimeout },
		func(f *fileConfig) *string { return f.Lease.ReleaseTimeout }),

	limitSetting("ratelimit", "default_rate",
		func(c *Config) *int { return &c.RateLimit.DefaultRate },
		func(f *fileConfig) *int { return f.RateLimit.DefaultRate }),
	limitSetting("ratelimit", "default_burst",
		func(c *Config) *int { return &c.RateLimit.DefaultBurst },
		func(f *fileConfig) *int { return f.RateLimit.DefaultBurst }),

	durationSetting("telegram", "request_timeout",
		func(c *Config) *time.Duration { return &c.Telegram.RequestTimeout },
		func(f *fileConfig) *string { return f.Telegram.RequestTimeout }),
	durationSetting("telegram", "max_poll_timeout",
		func(c *Config) *time.Duration { return &c.Telegram.MaxPollTimeout },
		func(f *fileConfig) *string { return f.Telegram.MaxPollTimeout }),
	durationSetting("telegram", "poll_timeout_grace",
		func(c *Config) *time.Duration { return &c.Telegram.PollTimeoutGrace },
		func(f *fileConfig) *string { return f.Telegram.PollTimeoutGrace }),
	durationSetting("telegram", "default_flood_wait",
		func(c *Config) *time.Duration { return &c.Telegram.DefaultFloodWait },
		func(f *fileConfig) *string { return f.Telegram.DefaultFloodWait }),
	limitSetting("groups", "callback_alert_max_runes",
		func(c *Config) *int { return &c.Groups.CallbackAlertMaxRunes },
		func(f *fileConfig) *int { return f.Groups.CallbackAlertMaxRunes }),
	durationSetting("groups", "deep_link_ttl",
		func(c *Config) *time.Duration { return &c.Groups.DeepLinkTTL },
		func(f *fileConfig) *string { return f.Groups.DeepLinkTTL }),
	stringListSetting("menu", "commands",
		func(c *Config) *[]string { return &c.Menu.Commands },
		func(f *fileConfig) []string { return f.Menu.Commands }),

	durationSetting("dedup", "ttl",
		func(c *Config) *time.Duration { return &c.Dedup.TTL },
		func(f *fileConfig) *string { return f.Dedup.TTL }),

	durationSetting("nats", "command_max_age",
		func(c *Config) *time.Duration { return &c.NATS.CommandMaxAge },
		func(f *fileConfig) *string { return f.NATS.CommandMaxAge }),
	durationSetting("nats", "event_max_age",
		func(c *Config) *time.Duration { return &c.NATS.EventMaxAge },
		func(f *fileConfig) *string { return f.NATS.EventMaxAge }),
	durationSetting("nats", "duplicate_window",
		func(c *Config) *time.Duration { return &c.NATS.DuplicateWindow },
		func(f *fileConfig) *string { return f.NATS.DuplicateWindow }),
	durationSetting("nats", "ack_wait",
		func(c *Config) *time.Duration { return &c.NATS.AckWait },
		func(f *fileConfig) *string { return f.NATS.AckWait }),
	limitSetting("nats", "max_deliver",
		func(c *Config) *int { return &c.NATS.MaxDeliver },
		func(f *fileConfig) *int { return f.NATS.MaxDeliver }),
	durationSetting("nats", "nak_delay",
		func(c *Config) *time.Duration { return &c.NATS.NakDelay },
		func(f *fileConfig) *string { return f.NATS.NakDelay }),
	durationListSetting("nats", "backoff",
		func(c *Config) *[]time.Duration { return &c.NATS.Backoff },
		func(f *fileConfig) []string { return f.NATS.Backoff }),

	durationSetting("worker", "poll_interval",
		func(c *Config) *time.Duration { return &c.Worker.PollInterval },
		func(f *fileConfig) *string { return f.Worker.PollInterval }),
	limitSetting("worker", "batch_size",
		func(c *Config) *int { return &c.Worker.BatchSize },
		func(f *fileConfig) *int { return f.Worker.BatchSize }),
	durationSetting("worker", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Worker.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Worker.ShutdownTimeout }),
	limitSetting("worker", "noisy_attempts",
		func(c *Config) *int { return &c.Worker.NoisyAttempts },
		func(f *fileConfig) *int { return f.Worker.NoisyAttempts }),

	durationSetting("scheduler", "tick_interval",
		func(c *Config) *time.Duration { return &c.Scheduler.TickInterval },
		func(f *fileConfig) *string { return f.Scheduler.TickInterval }),
	limitSetting("scheduler", "batch_size",
		func(c *Config) *int { return &c.Scheduler.BatchSize },
		func(f *fileConfig) *int { return f.Scheduler.BatchSize }),
	durationSetting("scheduler", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Scheduler.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Scheduler.ShutdownTimeout }),
	limitSetting("scheduler", "noisy_attempts",
		func(c *Config) *int { return &c.Scheduler.NoisyAttempts },
		func(f *fileConfig) *int { return f.Scheduler.NoisyAttempts }),
	durationSetting("scheduler", "claim_timeout",
		func(c *Config) *time.Duration { return &c.Scheduler.ClaimTimeout },
		func(f *fileConfig) *string { return f.Scheduler.ClaimTimeout }),

	durationSetting("notifier", "send_budget",
		func(c *Config) *time.Duration { return &c.Notifier.SendBudget },
		func(f *fileConfig) *string { return f.Notifier.SendBudget }),
	durationSetting("notifier", "receipt_margin",
		func(c *Config) *time.Duration { return &c.Notifier.ReceiptMargin },
		func(f *fileConfig) *string { return f.Notifier.ReceiptMargin }),
	durationSetting("notifier", "max_age",
		func(c *Config) *time.Duration { return &c.Notifier.MaxAge },
		func(f *fileConfig) *string { return f.Notifier.MaxAge }),
	durationSetting("notifier", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Notifier.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Notifier.ShutdownTimeout }),

	durationSetting("game", "shutdown_timeout",
		func(c *Config) *time.Duration { return &c.Game.ShutdownTimeout },
		func(f *fileConfig) *string { return f.Game.ShutdownTimeout }),
	durationSetting("game", "idempotency_ttl",
		func(c *Config) *time.Duration { return &c.Game.IdempotencyTTL },
		func(f *fileConfig) *string { return f.Game.IdempotencyTTL }),
	durationSetting("game", "content_reload_interval",
		func(c *Config) *time.Duration { return &c.Game.ContentReloadInterval },
		func(f *fileConfig) *string { return f.Game.ContentReloadInterval }),

	// The legacy spelling of the game clock comes BEFORE game.time_scale, so
	// the current key wins wherever both are written.
	aliasSetting(limitSetting("travel", "time_scale",
		func(c *Config) *int { return &c.Game.TimeScale },
		func(f *fileConfig) *int { return f.Travel.TimeScale })),
	limitSetting("game", "time_scale",
		func(c *Config) *int { return &c.Game.TimeScale },
		func(f *fileConfig) *int { return f.Game.TimeScale }),

	limitSetting("travel", "arrival_xp",
		func(c *Config) *int { return &c.Travel.ArrivalXP },
		func(f *fileConfig) *int { return f.Travel.ArrivalXP }),

	stringListSetting("travel", "city_locations",
		func(c *Config) *[]string { return &c.Travel.CityLocations },
		func(f *fileConfig) []string { return f.Travel.CityLocations }),
	stringListSetting("travel", "world_reach",
		func(c *Config) *[]string { return &c.Travel.WorldReach },
		func(f *fileConfig) []string { return f.Travel.WorldReach }),

	stringSetting("player", "default_language",
		func(c *Config) *string { return &c.Player.DefaultLanguage },
		func(f *fileConfig) *string { return f.Player.DefaultLanguage }),
	stringSetting("player", "default_timezone",
		func(c *Config) *string { return &c.Player.DefaultTimezone },
		func(f *fileConfig) *string { return f.Player.DefaultTimezone }),

	moneySetting("economy", "starting_cash",
		func(c *Config) *int64 { return &c.Economy.StartingCash },
		func(f *fileConfig) *int64 { return f.Economy.StartingCash }),
	moneySetting("economy", "bank_min_amount",
		func(c *Config) *int64 { return &c.Economy.BankMinAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMinAmount }),
	moneySetting("economy", "bank_max_amount",
		func(c *Config) *int64 { return &c.Economy.BankMaxAmount },
		func(f *fileConfig) *int64 { return f.Economy.BankMaxAmount }),
	moneyListSetting("economy", "bank_quick_amounts",
		func(c *Config) *[]int64 { return &c.Economy.BankQuickAmounts },
		func(f *fileConfig) []int64 { return f.Economy.BankQuickAmounts }),

	limitSetting("governance", "fine_step_divisor",
		func(c *Config) *int { return &c.Governance.FineStepDivisor },
		func(f *fileConfig) *int { return f.Governance.FineStepDivisor }),
	limitSetting("governance", "coarse_step_divisor",
		func(c *Config) *int { return &c.Governance.CoarseStepDivisor },
		func(f *fileConfig) *int { return f.Governance.CoarseStepDivisor }),
	limitSetting("governance", "allocation_step_bps",
		func(c *Config) *int { return &c.Governance.AllocationStepBPS },
		func(f *fileConfig) *int { return f.Governance.AllocationStepBPS }),

	limitSetting("worldgen", "cell_count",
		func(c *Config) *int { return &c.WorldGen.CellCount },
		func(f *fileConfig) *int { return f.WorldGen.CellCount }),
	limitSetting("worldgen", "neighbor_k",
		func(c *Config) *int { return &c.WorldGen.NeighborK },
		func(f *fileConfig) *int { return f.WorldGen.NeighborK }),
	limitSetting("worldgen", "plate_count",
		func(c *Config) *int { return &c.WorldGen.PlateCount },
		func(f *fileConfig) *int { return f.WorldGen.PlateCount }),
	limitSetting("worldgen", "oceanic_plate_fraction_permille",
		func(c *Config) *int { return &c.WorldGen.OceanicPlateFraction },
		func(f *fileConfig) *int { return f.WorldGen.OceanicPlateFraction }),
	limitSetting("worldgen", "land_fraction_permille",
		func(c *Config) *int { return &c.WorldGen.LandFraction },
		func(f *fileConfig) *int { return f.WorldGen.LandFraction }),
	limitSetting("worldgen", "noise_octaves",
		func(c *Config) *int { return &c.WorldGen.NoiseOctaves },
		func(f *fileConfig) *int { return f.WorldGen.NoiseOctaves }),
	floatSetting("worldgen", "noise_base_frequency",
		func(c *Config) *float64 { return &c.WorldGen.NoiseBaseFrequency },
		func(f *fileConfig) *float64 { return f.WorldGen.NoiseBaseFrequency }),
	limitSetting("worldgen", "noise_persistence_permille",
		func(c *Config) *int { return &c.WorldGen.NoisePersistence },
		func(f *fileConfig) *int { return f.WorldGen.NoisePersistence }),
	floatSetting("worldgen", "warp_amplitude",
		func(c *Config) *float64 { return &c.WorldGen.WarpAmplitude },
		func(f *fileConfig) *float64 { return f.WorldGen.WarpAmplitude }),
	floatSetting("worldgen", "warp_frequency",
		func(c *Config) *float64 { return &c.WorldGen.WarpFrequency },
		func(f *fileConfig) *float64 { return f.WorldGen.WarpFrequency }),
	limitSetting("worldgen", "boundary_influence_steps",
		func(c *Config) *int { return &c.WorldGen.BoundaryInfluenceSteps },
		func(f *fileConfig) *int { return f.WorldGen.BoundaryInfluenceSteps }),
	limitSetting("worldgen", "moisture_bands",
		func(c *Config) *int { return &c.WorldGen.MoistureBands },
		func(f *fileConfig) *int { return f.WorldGen.MoistureBands }),
	limitSetting("worldgen", "river_flow_threshold",
		func(c *Config) *int { return &c.WorldGen.RiverFlowThreshold },
		func(f *fileConfig) *int { return f.WorldGen.RiverFlowThreshold }),
	limitSetting("worldgen", "lake_min_depth",
		func(c *Config) *int { return &c.WorldGen.LakeMinDepth },
		func(f *fileConfig) *int { return f.WorldGen.LakeMinDepth }),
	limitSetting("worldgen", "lake_min_area_cells",
		func(c *Config) *int { return &c.WorldGen.LakeMinAreaCells },
		func(f *fileConfig) *int { return f.WorldGen.LakeMinAreaCells }),
	floatSetting("worldgen", "planet_radius_km",
		func(c *Config) *float64 { return &c.WorldGen.PlanetRadiusKm },
		func(f *fileConfig) *float64 { return f.WorldGen.PlanetRadiusKm }),
	limitSetting("worldgen", "chunk_base_lod",
		func(c *Config) *int { return &c.WorldGen.ChunkBaseLOD },
		func(f *fileConfig) *int { return f.WorldGen.ChunkBaseLOD }),
	limitSetting("worldgen", "chunk_tile_edge",
		func(c *Config) *int { return &c.WorldGen.ChunkTileEdge },
		func(f *fileConfig) *int { return f.WorldGen.ChunkTileEdge }),
	floatSetting("worldgen", "chunk_detail_frequency",
		func(c *Config) *float64 { return &c.WorldGen.ChunkDetailFrequency },
		func(f *fileConfig) *float64 { return f.WorldGen.ChunkDetailFrequency }),
	limitSetting("worldgen", "chunk_detail_amplitude",
		func(c *Config) *int { return &c.WorldGen.ChunkDetailAmplitude },
		func(f *fileConfig) *int { return f.WorldGen.ChunkDetailAmplitude }),
	floatSetting("worldgen", "chunk_stream_frequency",
		func(c *Config) *float64 { return &c.WorldGen.ChunkStreamFrequency },
		func(f *fileConfig) *float64 { return f.WorldGen.ChunkStreamFrequency }),
	limitSetting("worldgen", "chunk_stream_amplitude",
		func(c *Config) *int { return &c.WorldGen.ChunkStreamAmplitude },
		func(f *fileConfig) *int { return f.WorldGen.ChunkStreamAmplitude }),
	limitSetting("worldgen", "chunk_deposit_tiles_per_deposit",
		func(c *Config) *int { return &c.WorldGen.ChunkDepositTilesPerDeposit },
		func(f *fileConfig) *int { return f.WorldGen.ChunkDepositTilesPerDeposit }),

	stringSetting("growth", "capabilities",
		func(c *Config) *string { return &c.Growth.Capabilities },
		func(f *fileConfig) *string { return f.Growth.Capabilities }),
	durationSetting("growth", "cache_ttl",
		func(c *Config) *time.Duration { return &c.Growth.CacheTTL },
		func(f *fileConfig) *string { return f.Growth.CacheTTL }),
	limitSetting("growth", "ruined_bps",
		func(c *Config) *int { return &c.Growth.RuinedBPS },
		func(f *fileConfig) *int { return f.Growth.RuinedBPS }),
	durationSetting("growth", "flush_interval",
		func(c *Config) *time.Duration { return &c.Growth.FlushInterval },
		func(f *fileConfig) *string { return f.Growth.FlushInterval }),
	limitSetting("game", "clock_legacy_scale",
		func(c *Config) *int { return &c.Game.ClockLegacyScale },
		func(f *fileConfig) *int { return f.Game.ClockLegacyScale }),
	stringSetting("game", "clock_cutover",
		func(c *Config) *string { return &c.Game.ClockCutover },
		func(f *fileConfig) *string { return f.Game.ClockCutover }),
	limitSetting("game", "travel_time_scale",
		func(c *Config) *int { return &c.Game.TravelTimeScale },
		func(f *fileConfig) *int { return f.Game.TravelTimeScale }),
	stringSetting("game", "clock_epoch",
		func(c *Config) *string { return &c.Game.ClockEpoch },
		func(f *fileConfig) *string { return f.Game.ClockEpoch }),
	moneySetting("bag", "carry_base",
		func(c *Config) *int64 { return &c.Bag.CarryBase },
		func(f *fileConfig) *int64 { return f.Bag.CarryBase }),
	moneySetting("bag", "base_comfort_kg",
		func(c *Config) *int64 { return &c.Bag.BaseComfortKG },
		func(f *fileConfig) *int64 { return f.Bag.BaseComfortKG }),
	moneySetting("bag", "base_hard_kg",
		func(c *Config) *int64 { return &c.Bag.BaseHardKG },
		func(f *fileConfig) *int64 { return f.Bag.BaseHardKG }),
	moneySetting("bag", "full_share_bps",
		func(c *Config) *int64 { return &c.Bag.FullShareBPS },
		func(f *fileConfig) *int64 { return f.Bag.FullShareBPS }),
	moneySetting("bag", "torn_space_bps",
		func(c *Config) *int64 { return &c.Bag.TornSpaceBPS },
		func(f *fileConfig) *int64 { return f.Bag.TornSpaceBPS }),
	moneySetting("bag", "repair_share_bps",
		func(c *Config) *int64 { return &c.Bag.RepairShareBPS },
		func(f *fileConfig) *int64 { return f.Bag.RepairShareBPS }),
	moneySetting("bag", "wear_per_day",
		func(c *Config) *int64 { return &c.Bag.WearPerDay },
		func(f *fileConfig) *int64 { return f.Bag.WearPerDay }),
	moneySetting("merchant", "restock_hour",
		func(c *Config) *int64 { return &c.Merchant.RestockHour },
		func(f *fileConfig) *int64 { return f.Merchant.RestockHour }),
	moneySetting("merchant", "markup_min_bps",
		func(c *Config) *int64 { return &c.Merchant.MarkupMinBPS },
		func(f *fileConfig) *int64 { return f.Merchant.MarkupMinBPS }),
	moneySetting("merchant", "markup_max_bps",
		func(c *Config) *int64 { return &c.Merchant.MarkupMaxBPS },
		func(f *fileConfig) *int64 { return f.Merchant.MarkupMaxBPS }),
	moneySetting("merchant", "stock_days",
		func(c *Config) *int64 { return &c.Merchant.StockDays },
		func(f *fileConfig) *int64 { return f.Merchant.StockDays }),
	moneySetting("merchant", "food_share_bps",
		func(c *Config) *int64 { return &c.Merchant.FoodShareBPS },
		func(f *fileConfig) *int64 { return f.Merchant.FoodShareBPS }),
	moneySetting("merchant", "other_share_bps",
		func(c *Config) *int64 { return &c.Merchant.OtherShareBPS },
		func(f *fileConfig) *int64 { return f.Merchant.OtherShareBPS }),
	moneySetting("merchant", "player_day_food",
		func(c *Config) *int64 { return &c.Merchant.PlayerDayFood },
		func(f *fileConfig) *int64 { return f.Merchant.PlayerDayFood }),
	moneySetting("merchant", "player_day_other",
		func(c *Config) *int64 { return &c.Merchant.PlayerDayOther },
		func(f *fileConfig) *int64 { return f.Merchant.PlayerDayOther }),
	moneySetting("merchant", "supply_value_per_resident_day",
		func(c *Config) *int64 { return &c.Merchant.SupplyValueFood },
		func(f *fileConfig) *int64 { return f.Merchant.SupplyValueFood }),
	moneySetting("merchant", "building_boost_bps",
		func(c *Config) *int64 { return &c.Merchant.BuildingBoostBPS },
		func(f *fileConfig) *int64 { return f.Merchant.BuildingBoostBPS }),
	moneyListSetting("merchant", "cap_presets",
		func(c *Config) *[]int64 { return &c.Merchant.CapPresets },
		func(f *fileConfig) []int64 { return f.Merchant.CapPresets }),
	moneyListSetting("merchant", "buy_presets",
		func(c *Config) *[]int64 { return &c.Merchant.BuyPresets },
		func(f *fileConfig) []int64 { return f.Merchant.BuyPresets }),
	moneySetting("merchant", "tax_default_bps",
		func(c *Config) *int64 { return &c.Merchant.TaxDefaultBPS },
		func(f *fileConfig) *int64 { return f.Merchant.TaxDefaultBPS }),
	moneySetting("merchant", "tax_max_bps",
		func(c *Config) *int64 { return &c.Merchant.TaxMaxBPS },
		func(f *fileConfig) *int64 { return f.Merchant.TaxMaxBPS }),
	moneyListSetting("merchant", "tax_presets",
		func(c *Config) *[]int64 { return &c.Merchant.TaxPresets },
		func(f *fileConfig) []int64 { return f.Merchant.TaxPresets }),
	moneySetting("merchant", "output_days",
		func(c *Config) *int64 { return &c.Merchant.OutputDays },
		func(f *fileConfig) *int64 { return f.Merchant.OutputDays }),
	moneyListSetting("premium", "nil_examples",
		func(c *Config) *[]int64 { return &c.Premium.NilExamples },
		func(f *fileConfig) []int64 { return f.Premium.NilExamples }),
	moneySetting("premium", "nil_unit_sup",
		func(c *Config) *int64 { return &c.Premium.NilUnitSup },
		func(f *fileConfig) *int64 { return f.Premium.NilUnitSup }),
	durationSetting("settlement", "protection_window",
		func(c *Config) *time.Duration { return &c.Settlement.ProtectionWindow },
		func(f *fileConfig) *string { return f.Settlement.ProtectionWindow }),
	durationSetting("settlement", "timezone_cooldown",
		func(c *Config) *time.Duration { return &c.Settlement.TimezoneCooldown },
		func(f *fileConfig) *string { return f.Settlement.TimezoneCooldown }),
	durationSetting("settlement", "residence_cooldown",
		func(c *Config) *time.Duration { return &c.Settlement.ResidenceCooldown },
		func(f *fileConfig) *string { return f.Settlement.ResidenceCooldown }),
	stringSetting("settlement", "home_city_code",
		func(c *Config) *string { return &c.Settlement.HomeCityCode },
		func(f *fileConfig) *string { return f.Settlement.HomeCityCode }),
	stringSetting("settlement", "property_hub_min_stage",
		func(c *Config) *string { return &c.Settlement.PropertyHubMinStage },
		func(f *fileConfig) *string { return f.Settlement.PropertyHubMinStage }),
	floatSetting("settlement", "min_spawn_distance_km",
		func(c *Config) *float64 { return &c.Settlement.MinSpawnDistanceKm },
		func(f *fileConfig) *float64 { return f.Settlement.MinSpawnDistanceKm }),
	floatSetting("settlement", "threat_radius_km",
		func(c *Config) *float64 { return &c.Settlement.ThreatRadiusKm },
		func(f *fileConfig) *float64 { return f.Settlement.ThreatRadiusKm }),
	limitSetting("settlement", "search_max_cells",
		func(c *Config) *int { return &c.Settlement.SearchMaxCells },
		func(f *fileConfig) *int { return f.Settlement.SearchMaxCells }),
	limitSetting("settlement", "search_max_attempts",
		func(c *Config) *int { return &c.Settlement.SearchMaxAttempts },
		func(f *fileConfig) *int { return f.Settlement.SearchMaxAttempts }),
	floatSetting("settlement", "spawn_circle_radius_km",
		func(c *Config) *float64 { return &c.Settlement.SpawnCircleRadiusKm },
		func(f *fileConfig) *float64 { return f.Settlement.SpawnCircleRadiusKm }),
	limitSetting("settlement", "spawn_circle_capacity",
		func(c *Config) *int { return &c.Settlement.SpawnCircleCapacity },
		func(f *fileConfig) *int { return f.Settlement.SpawnCircleCapacity }),
	floatSetting("settlement", "spawn_circle_fill_band_km",
		func(c *Config) *float64 { return &c.Settlement.SpawnCircleFillBandKm },
		func(f *fileConfig) *float64 { return f.Settlement.SpawnCircleFillBandKm }),
	limitSetting("settlement", "spawn_circle_max_advance",
		func(c *Config) *int { return &c.Settlement.SpawnCircleMaxAdvance },
		func(f *fileConfig) *int { return f.Settlement.SpawnCircleMaxAdvance }),
	limitSetting("settlement", "building_area_per_cell",
		func(c *Config) *int { return &c.Settlement.BuildingAreaPerCell },
		func(f *fileConfig) *int { return f.Settlement.BuildingAreaPerCell }),
	limitSetting("settlement", "building_storey_timber_per_cell",
		func(c *Config) *int { return &c.Settlement.BuildingStoreyTimberPerCell },
		func(f *fileConfig) *int { return f.Settlement.BuildingStoreyTimberPerCell }),
	limitSetting("settlement", "building_storey_stone_per_cell",
		func(c *Config) *int { return &c.Settlement.BuildingStoreyStonePerCell },
		func(f *fileConfig) *int { return f.Settlement.BuildingStoreyStonePerCell }),
	limitSetting("settlement", "building_storey_stone_from",
		func(c *Config) *int { return &c.Settlement.BuildingStoreyStoneFrom },
		func(f *fileConfig) *int { return f.Settlement.BuildingStoreyStoneFrom }),
	limitSetting("settlement", "building_storey_shifts_per_cell",
		func(c *Config) *int { return &c.Settlement.BuildingStoreyShiftsPerCell },
		func(f *fileConfig) *int { return f.Settlement.BuildingStoreyShiftsPerCell }),
	limitSetting("settlement", "building_salvage_bps",
		func(c *Config) *int { return &c.Settlement.BuildingSalvageBPS },
		func(f *fileConfig) *int { return f.Settlement.BuildingSalvageBPS }),
	limitSetting("settlement", "use_change_fee_bps",
		func(c *Config) *int { return &c.Settlement.UseChangeFeeBPS },
		func(f *fileConfig) *int { return f.Settlement.UseChangeFeeBPS }),
	limitSetting("settlement", "building_look_rerolls",
		func(c *Config) *int { return &c.Settlement.BuildingLookRerolls },
		func(f *fileConfig) *int { return f.Settlement.BuildingLookRerolls }),
	limitSetting("settlement", "building_templates_max",
		func(c *Config) *int { return &c.Settlement.BuildingTemplatesMax },
		func(f *fileConfig) *int { return f.Settlement.BuildingTemplatesMax }),
	stringListSetting("settlement", "building_storey_knowledge",
		func(c *Config) *[]string { return &c.Settlement.BuildingStoreyKnowledge },
		func(f *fileConfig) []string { return f.Settlement.BuildingStoreyKnowledge }),
	stringListSetting("settlement", "excluded_biomes",
		func(c *Config) *[]string { return &c.Settlement.ExcludedBiomes },
		func(f *fileConfig) []string { return f.Settlement.ExcludedBiomes }),
	floatSetting("settlement", "max_abs_latitude_deg",
		func(c *Config) *float64 { return &c.Settlement.MaxAbsLatitudeDeg },
		func(f *fileConfig) *float64 { return f.Settlement.MaxAbsLatitudeDeg }),
	stringListSetting("settlement", "biome_penalties",
		func(c *Config) *[]string { return &c.Settlement.BiomePenalties },
		func(f *fileConfig) []string { return f.Settlement.BiomePenalties }),
	limitSetting("settlement", "village_grid_lots",
		func(c *Config) *int { return &c.Settlement.VillageGridLots },
		func(f *fileConfig) *int { return f.Settlement.VillageGridLots }),
	limitSetting("settlement", "min_buildable_lot_share_bps",
		func(c *Config) *int { return &c.Settlement.MinBuildableLotShareBps },
		func(f *fileConfig) *int { return f.Settlement.MinBuildableLotShareBps }),
	limitSetting("settlement", "grid_shift_max_lots",
		func(c *Config) *int { return &c.Settlement.GridShiftMaxLots },
		func(f *fileConfig) *int { return f.Settlement.GridShiftMaxLots }),
	moneySetting("settlement", "auto_road_cost",
		func(c *Config) *int64 { return &c.Settlement.AutoRoadCost },
		func(f *fileConfig) *int64 { return f.Settlement.AutoRoadCost }),
	moneySetting("settlement", "lot_access_crossing_cost",
		func(c *Config) *int64 { return &c.Settlement.LotAccessCrossingCost },
		func(f *fileConfig) *int64 { return f.Settlement.LotAccessCrossingCost }),
	limitSetting("settlement", "lot_access_max_crossing",
		func(c *Config) *int { return &c.Settlement.LotAccessMaxCrossing },
		func(f *fileConfig) *int { return f.Settlement.LotAccessMaxCrossing }),
	limitSetting("settlement", "street_pitch",
		func(c *Config) *int { return &c.Settlement.StreetPitch },
		func(f *fileConfig) *int { return f.Settlement.StreetPitch }),
	limitSetting("settlement", "street_plan_min_grid",
		func(c *Config) *int { return &c.Settlement.StreetPlanMinGrid },
		func(f *fileConfig) *int { return f.Settlement.StreetPlanMinGrid }),
	limitSetting("settlement", "road_frontage_depth_lots",
		func(c *Config) *int { return &c.Settlement.RoadFrontageDepthLots },
		func(f *fileConfig) *int { return f.Settlement.RoadFrontageDepthLots }),
	limitSetting("settlement", "road_plan_max_lots",
		func(c *Config) *int { return &c.Settlement.RoadPlanMaxLots },
		func(f *fileConfig) *int { return f.Settlement.RoadPlanMaxLots }),
	limitSetting("settlement", "road_open_lots_max",
		func(c *Config) *int { return &c.Settlement.RoadOpenLotsMax },
		func(f *fileConfig) *int { return f.Settlement.RoadOpenLotsMax }),
	limitSetting("settlement", "road_foreign_buffer_tiles",
		func(c *Config) *int { return &c.Settlement.RoadForeignBufferTiles },
		func(f *fileConfig) *int { return f.Settlement.RoadForeignBufferTiles }),
	limitSetting("settlement", "road_steep_slope_m",
		func(c *Config) *int { return &c.Settlement.RoadSteepSlopeM },
		func(f *fileConfig) *int { return f.Settlement.RoadSteepSlopeM }),
	limitSetting("settlement", "road_corridor_ring_tiles",
		func(c *Config) *int { return &c.Settlement.RoadCorridorRingTiles },
		func(f *fileConfig) *int { return f.Settlement.RoadCorridorRingTiles }),
	limitSetting("settlement", "road_track_cost_bps",
		func(c *Config) *int { return &c.Settlement.RoadTrackCostBPS },
		func(f *fileConfig) *int { return f.Settlement.RoadTrackCostBPS }),
	durationSetting("settlement", "founding_draft_ttl",
		func(c *Config) *time.Duration { return &c.Settlement.FoundingDraftTTL },
		func(f *fileConfig) *string { return f.Settlement.FoundingDraftTTL }),
	limitSetting("settlement", "founding_name_min",
		func(c *Config) *int { return &c.Settlement.FoundingNameMin },
		func(f *fileConfig) *int { return f.Settlement.FoundingNameMin }),
	limitSetting("settlement", "founding_name_max",
		func(c *Config) *int { return &c.Settlement.FoundingNameMax },
		func(f *fileConfig) *int { return f.Settlement.FoundingNameMax }),
	limitSetting("settlement", "founding_motto_max",
		func(c *Config) *int { return &c.Settlement.FoundingMottoMax },
		func(f *fileConfig) *int { return f.Settlement.FoundingMottoMax }),
	limitSetting("settlement", "founding_currency_name_min",
		func(c *Config) *int { return &c.Settlement.FoundingCurrencyNameMin },
		func(f *fileConfig) *int { return f.Settlement.FoundingCurrencyNameMin }),
	limitSetting("settlement", "founding_currency_name_max",
		func(c *Config) *int { return &c.Settlement.FoundingCurrencyNameMax },
		func(f *fileConfig) *int { return f.Settlement.FoundingCurrencyNameMax }),
	limitSetting("settlement", "founding_currency_code_len",
		func(c *Config) *int { return &c.Settlement.FoundingCurrencyCodeLen },
		func(f *fileConfig) *int { return f.Settlement.FoundingCurrencyCodeLen }),
	limitSetting("settlement", "founding_currency_symbol_max",
		func(c *Config) *int { return &c.Settlement.FoundingCurrencySymbolMax },
		func(f *fileConfig) *int { return f.Settlement.FoundingCurrencySymbolMax }),
	durationSetting("settlement", "teach_period",
		func(c *Config) *time.Duration { return &c.Settlement.TeachPeriod },
		func(f *fileConfig) *string { return f.Settlement.TeachPeriod }),
	moneySetting("settlement", "teach_rate_bps",
		func(c *Config) *int64 { return &c.Settlement.TeachRateBPS },
		func(f *fileConfig) *int64 { return f.Settlement.TeachRateBPS }),
	moneySetting("settlement", "base_school_capacity_bps",
		func(c *Config) *int64 { return &c.Settlement.BaseSchoolCapacityBPS },
		func(f *fileConfig) *int64 { return f.Settlement.BaseSchoolCapacityBPS }),
	moneySetting("settlement", "scarcity_k_bps",
		func(c *Config) *int64 { return &c.Settlement.ScarcityKBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ScarcityKBPS }),
	moneySetting("settlement", "scarcity_floor_bps",
		func(c *Config) *int64 { return &c.Settlement.ScarcityFloorBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ScarcityFloorBPS }),
	moneySetting("settlement", "scarcity_cap_bps",
		func(c *Config) *int64 { return &c.Settlement.ScarcityCapBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ScarcityCapBPS }),
	moneySetting("settlement", "seller_band_bps",
		func(c *Config) *int64 { return &c.Settlement.SellerBandBPS },
		func(f *fileConfig) *int64 { return f.Settlement.SellerBandBPS }),
	moneySetting("settlement", "demolition_salvage_bps",
		func(c *Config) *int64 { return &c.Settlement.DemolitionSalvageBPS },
		func(f *fileConfig) *int64 { return f.Settlement.DemolitionSalvageBPS }),
	moneySetting("settlement", "material_markup_bps",
		func(c *Config) *int64 { return &c.Settlement.MaterialMarkupBPS },
		func(f *fileConfig) *int64 { return f.Settlement.MaterialMarkupBPS }),
	moneySetting("settlement", "stock_base_capacity",
		func(c *Config) *int64 { return &c.Settlement.StockBaseCapacity },
		func(f *fileConfig) *int64 { return f.Settlement.StockBaseCapacity }),
	moneySetting("settlement", "build_homes_per_crew",
		func(c *Config) *int64 { return &c.Settlement.BuildHomesPerCrew },
		func(f *fileConfig) *int64 { return f.Settlement.BuildHomesPerCrew }),
	moneySetting("settlement", "charter_max_offices",
		func(c *Config) *int64 { return &c.Settlement.CharterMaxOffices },
		func(f *fileConfig) *int64 { return f.Settlement.CharterMaxOffices }),
	moneySetting("settlement", "charter_max_seats",
		func(c *Config) *int64 { return &c.Settlement.CharterMaxSeats },
		func(f *fileConfig) *int64 { return f.Settlement.CharterMaxSeats }),
	moneySetting("settlement", "charter_max_permissions",
		func(c *Config) *int64 { return &c.Settlement.CharterMaxPermissions },
		func(f *fileConfig) *int64 { return f.Settlement.CharterMaxPermissions }),
	moneySetting("settlement", "charter_title_min",
		func(c *Config) *int64 { return &c.Settlement.CharterTitleMin },
		func(f *fileConfig) *int64 { return f.Settlement.CharterTitleMin }),
	moneySetting("settlement", "charter_title_max",
		func(c *Config) *int64 { return &c.Settlement.CharterTitleMax },
		func(f *fileConfig) *int64 { return f.Settlement.CharterTitleMax }),
	moneySetting("settlement", "charter_election_term_days",
		func(c *Config) *int64 { return &c.Settlement.CharterElectionTermDays },
		func(f *fileConfig) *int64 { return f.Settlement.CharterElectionTermDays }),
	moneySetting("settlement", "charter_candidacy_hours",
		func(c *Config) *int64 { return &c.Settlement.CharterCandidacyHours },
		func(f *fileConfig) *int64 { return f.Settlement.CharterCandidacyHours }),
	moneySetting("settlement", "charter_voting_hours",
		func(c *Config) *int64 { return &c.Settlement.CharterVotingHours },
		func(f *fileConfig) *int64 { return f.Settlement.CharterVotingHours }),
	moneySetting("settlement", "charter_recall_min_tenure_days",
		func(c *Config) *int64 { return &c.Settlement.CharterRecallMinTenureDays },
		func(f *fileConfig) *int64 { return f.Settlement.CharterRecallMinTenureDays }),
	moneySetting("settlement", "charter_recall_signature_bps",
		func(c *Config) *int64 { return &c.Settlement.CharterRecallSignatureBPS },
		func(f *fileConfig) *int64 { return f.Settlement.CharterRecallSignatureBPS }),
	moneySetting("settlement", "charter_recall_min_signatures",
		func(c *Config) *int64 { return &c.Settlement.CharterRecallMinSignatures },
		func(f *fileConfig) *int64 { return f.Settlement.CharterRecallMinSignatures }),
	moneySetting("settlement", "charter_recall_vote_hours",
		func(c *Config) *int64 { return &c.Settlement.CharterRecallVoteHours },
		func(f *fileConfig) *int64 { return f.Settlement.CharterRecallVoteHours }),
	moneySetting("settlement", "charter_recall_cooldown_days",
		func(c *Config) *int64 { return &c.Settlement.CharterRecallCooldownDays },
		func(f *fileConfig) *int64 { return f.Settlement.CharterRecallCooldownDays }),
	moneySetting("settlement", "charter_amend_vote_hours",
		func(c *Config) *int64 { return &c.Settlement.CharterAmendVoteHours },
		func(f *fileConfig) *int64 { return f.Settlement.CharterAmendVoteHours }),
	moneySetting("settlement", "charter_amend_quorum_bps",
		func(c *Config) *int64 { return &c.Settlement.CharterAmendQuorumBPS },
		func(f *fileConfig) *int64 { return f.Settlement.CharterAmendQuorumBPS }),
	moneySetting("settlement", "charter_amend_vote_min_residents",
		func(c *Config) *int64 { return &c.Settlement.CharterAmendVoteMinResidents },
		func(f *fileConfig) *int64 { return f.Settlement.CharterAmendVoteMinResidents }),
	moneySetting("settlement", "charter_acting_days",
		func(c *Config) *int64 { return &c.Settlement.CharterActingDays },
		func(f *fileConfig) *int64 { return f.Settlement.CharterActingDays }),
	moneySetting("settlement", "charter_acting_spend_cap",
		func(c *Config) *int64 { return &c.Settlement.CharterActingSpendCap },
		func(f *fileConfig) *int64 { return f.Settlement.CharterActingSpendCap }),
	moneySetting("settlement", "charter_min_residency_days",
		func(c *Config) *int64 { return &c.Settlement.CharterMinResidencyDays },
		func(f *fileConfig) *int64 { return f.Settlement.CharterMinResidencyDays }),
	moneySetting("settlement", "storage_spoil_kept_bps",
		func(c *Config) *int64 { return &c.Settlement.StorageSpoilKeptBPS },
		func(f *fileConfig) *int64 { return f.Settlement.StorageSpoilKeptBPS }),
	moneySetting("settlement", "storage_spoil_unkept_bps",
		func(c *Config) *int64 { return &c.Settlement.StorageSpoilUnkeptBPS },
		func(f *fileConfig) *int64 { return f.Settlement.StorageSpoilUnkeptBPS }),
	stringSetting("settlement", "storage_keeper_rule_at",
		func(c *Config) *string { return &c.Settlement.StorageKeeperRuleAt },
		func(f *fileConfig) *string { return f.Settlement.StorageKeeperRuleAt }),
	moneySetting("settlement", "storage_keeper_grace_days",
		func(c *Config) *int64 { return &c.Settlement.StorageKeeperGraceDays },
		func(f *fileConfig) *int64 { return f.Settlement.StorageKeeperGraceDays }),
	moneySetting("settlement", "research_free_slots",
		func(c *Config) *int64 { return &c.Settlement.ResearchFreeSlots },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchFreeSlots }),
	moneySetting("settlement", "research_speed_floor_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchSpeedFloorBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchSpeedFloorBPS }),
	moneySetting("settlement", "research_scholar_floor_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchScholarFloorBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchScholarFloorBPS }),
	moneySetting("settlement", "research_skill_bps_per_level",
		func(c *Config) *int64 { return &c.Settlement.ResearchSkillBPSPerLevel },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchSkillBPSPerLevel }),
	moneySetting("settlement", "research_scholar_cap_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchScholarCapBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchScholarCapBPS }),
	moneySetting("settlement", "research_npc_scholar_level",
		func(c *Config) *int64 { return &c.Settlement.ResearchNPCScholarLevel },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchNPCScholarLevel }),
	moneySetting("settlement", "research_literacy_bonus_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchLiteracyBonusBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchLiteracyBonusBPS }),
	moneySetting("settlement", "research_catch_up_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchCatchUpBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchCatchUpBPS }),
	moneySetting("settlement", "research_era_base_depth",
		func(c *Config) *int64 { return &c.Settlement.ResearchEraBaseDepth },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchEraBaseDepth }),
	moneySetting("settlement", "research_era_share_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchEraShareBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchEraShareBPS }),
	moneySetting("settlement", "research_ahead_per_step_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchAheadPerStepBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchAheadPerStepBPS }),
	moneySetting("settlement", "research_ahead_cap_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchAheadCapBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchAheadCapBPS }),
	moneySetting("settlement", "research_share_per_partner_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchSharePerPartnerBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchSharePerPartnerBPS }),
	moneySetting("settlement", "research_share_cap_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchShareCapBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchShareCapBPS }),
	moneySetting("settlement", "research_breakthrough_need_per_depth",
		func(c *Config) *int64 { return &c.Settlement.ResearchBreakthroughNeedPerDepth },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchBreakthroughNeedPerDepth }),
	moneySetting("settlement", "research_breakthrough_max_bps",
		func(c *Config) *int64 { return &c.Settlement.ResearchBreakthroughMaxBPS },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchBreakthroughMaxBPS }),
	moneySetting("settlement", "research_experience_per_shift",
		func(c *Config) *int64 { return &c.Settlement.ResearchExperiencePerShift },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchExperiencePerShift }),
	moneySetting("settlement", "research_scholar_xp",
		func(c *Config) *int64 { return &c.Settlement.ResearchScholarXP },
		func(f *fileConfig) *int64 { return f.Settlement.ResearchScholarXP }),
	moneySetting("settlement", "material_buy_max",
		func(c *Config) *int64 { return &c.Settlement.MaterialBuyMax },
		func(f *fileConfig) *int64 { return f.Settlement.MaterialBuyMax }),
	moneyListSetting("settlement", "material_buy_presets",
		func(c *Config) *[]int64 { return &c.Settlement.MaterialBuyPresets },
		func(f *fileConfig) []int64 { return f.Settlement.MaterialBuyPresets }),
	moneySetting("settlement", "founding_grant",
		func(c *Config) *int64 { return &c.Settlement.FoundingGrant },
		func(f *fileConfig) *int64 { return f.Settlement.FoundingGrant }),
	moneySetting("settlement", "donation_min",
		func(c *Config) *int64 { return &c.Settlement.DonationMin },
		func(f *fileConfig) *int64 { return f.Settlement.DonationMin }),
	moneySetting("settlement", "donation_max",
		func(c *Config) *int64 { return &c.Settlement.DonationMax },
		func(f *fileConfig) *int64 { return f.Settlement.DonationMax }),
	moneyListSetting("settlement", "donation_presets",
		func(c *Config) *[]int64 { return &c.Settlement.DonationPresets },
		func(f *fileConfig) []int64 { return f.Settlement.DonationPresets }),
	moneySetting("settlement", "citizen_lot_price",
		func(c *Config) *int64 { return &c.Settlement.CitizenLotPrice },
		func(f *fileConfig) *int64 { return f.Settlement.CitizenLotPrice }),
	moneySetting("settlement", "citizen_lot_price_min",
		func(c *Config) *int64 { return &c.Settlement.CitizenLotPriceMin },
		func(f *fileConfig) *int64 { return f.Settlement.CitizenLotPriceMin }),
	moneySetting("settlement", "citizen_lot_price_max",
		func(c *Config) *int64 { return &c.Settlement.CitizenLotPriceMax },
		func(f *fileConfig) *int64 { return f.Settlement.CitizenLotPriceMax }),
	moneySetting("settlement", "citizen_permit_fee",
		func(c *Config) *int64 { return &c.Settlement.CitizenPermitFee },
		func(f *fileConfig) *int64 { return f.Settlement.CitizenPermitFee }),
	moneySetting("settlement", "citizen_permit_fee_max",
		func(c *Config) *int64 { return &c.Settlement.CitizenPermitFeeMax },
		func(f *fileConfig) *int64 { return f.Settlement.CitizenPermitFeeMax }),
	limitSetting("settlement", "citizen_tax_bps",
		func(c *Config) *int { return &c.Settlement.CitizenTaxBPS },
		func(f *fileConfig) *int { return f.Settlement.CitizenTaxBPS }),
	limitSetting("settlement", "citizen_tax_bps_max",
		func(c *Config) *int { return &c.Settlement.CitizenTaxBPSMax },
		func(f *fileConfig) *int { return f.Settlement.CitizenTaxBPSMax }),
	durationSetting("settlement", "citizen_tax_period",
		func(c *Config) *time.Duration { return &c.Settlement.CitizenTaxPeriod },
		func(f *fileConfig) *string { return f.Settlement.CitizenTaxPeriod }),
	limitSetting("settlement", "citizen_material_markup_bps",
		func(c *Config) *int { return &c.Settlement.CitizenMaterialMarkupBPS },
		func(f *fileConfig) *int { return f.Settlement.CitizenMaterialMarkupBPS }),
	limitSetting("settlement", "citizen_max_lots_per_player",
		func(c *Config) *int { return &c.Settlement.CitizenMaxLotsPerPlayer },
		func(f *fileConfig) *int { return f.Settlement.CitizenMaxLotsPerPlayer }),
	limitSetting("settlement", "citizen_private_share_max_bps",
		func(c *Config) *int { return &c.Settlement.CitizenPrivateShareMaxBPS },
		func(f *fileConfig) *int { return f.Settlement.CitizenPrivateShareMaxBPS }),
	durationSetting("settlement", "citizen_home_rest_cooldown",
		func(c *Config) *time.Duration { return &c.Settlement.CitizenHomeRestCooldown },
		func(f *fileConfig) *string { return f.Settlement.CitizenHomeRestCooldown }),
	limitSetting("settlement", "citizen_home_rest_health",
		func(c *Config) *int { return &c.Settlement.CitizenHomeRestHealth },
		func(f *fileConfig) *int { return f.Settlement.CitizenHomeRestHealth }),
	limitSetting("settlement", "citizen_home_rest_happiness",
		func(c *Config) *int { return &c.Settlement.CitizenHomeRestHappiness },
		func(f *fileConfig) *int { return f.Settlement.CitizenHomeRestHappiness }),

	moneySetting("education", "teacher_wage_bps",
		func(c *Config) *int64 { return &c.Education.TeacherWageBPS },
		func(f *fileConfig) *int64 { return f.Education.TeacherWageBPS }),
	moneySetting("education", "teacher_min_wage",
		func(c *Config) *int64 { return &c.Education.TeacherMinWage },
		func(f *fileConfig) *int64 { return f.Education.TeacherMinWage }),
	moneySetting("education", "teacher_max_students",
		func(c *Config) *int64 { return &c.Education.TeacherMaxStudents },
		func(f *fileConfig) *int64 { return f.Education.TeacherMaxStudents }),
	moneySetting("training", "energy_cost",
		func(c *Config) *int64 { return &c.Training.EnergyCost },
		func(f *fileConfig) *int64 { return f.Training.EnergyCost }),
	moneySetting("training", "stamina_gain",
		func(c *Config) *int64 { return &c.Training.StaminaGain },
		func(f *fileConfig) *int64 { return f.Training.StaminaGain }),
	moneySetting("training", "strength_xp",
		func(c *Config) *int64 { return &c.Training.StrengthXP },
		func(f *fileConfig) *int64 { return f.Training.StrengthXP }),
	moneySetting("training", "diminish_stamina",
		func(c *Config) *int64 { return &c.Training.DiminishStamina },
		func(f *fileConfig) *int64 { return f.Training.DiminishStamina }),
	moneySetting("training", "stamina_per_max_energy",
		func(c *Config) *int64 { return &c.Training.StaminaPerMaxEnergy },
		func(f *fileConfig) *int64 { return f.Training.StaminaPerMaxEnergy }),
	moneySetting("training", "max_energy_bonus_cap",
		func(c *Config) *int64 { return &c.Training.MaxEnergyBonusCap },
		func(f *fileConfig) *int64 { return f.Training.MaxEnergyBonusCap }),
	moneySetting("training", "yard_bps",
		func(c *Config) *int64 { return &c.Training.YardBPS },
		func(f *fileConfig) *int64 { return f.Training.YardBPS }),
	moneySetting("training", "ground_bps",
		func(c *Config) *int64 { return &c.Training.GroundBPS },
		func(f *fileConfig) *int64 { return f.Training.GroundBPS }),
	moneySetting("training", "gym_bps",
		func(c *Config) *int64 { return &c.Training.GymBPS },
		func(f *fileConfig) *int64 { return f.Training.GymBPS }),
	moneySetting("training", "ground_fee",
		func(c *Config) *int64 { return &c.Training.GroundFee },
		func(f *fileConfig) *int64 { return f.Training.GroundFee }),
	moneySetting("training", "gym_fee",
		func(c *Config) *int64 { return &c.Training.GymFee },
		func(f *fileConfig) *int64 { return f.Training.GymFee }),

	moneySetting("labor", "shift_real_minutes",
		func(c *Config) *int64 { return &c.Labor.ShiftRealMinutes },
		func(f *fileConfig) *int64 { return f.Labor.ShiftRealMinutes }),
	moneySetting("labor", "shift_minutes",
		func(c *Config) *int64 { return &c.Labor.ShiftMinutes },
		func(f *fileConfig) *int64 { return f.Labor.ShiftMinutes }),
	moneySetting("labor", "reference_crew",
		func(c *Config) *int64 { return &c.Labor.ReferenceCrew },
		func(f *fileConfig) *int64 { return f.Labor.ReferenceCrew }),
	moneySetting("labor", "base_wage",
		func(c *Config) *int64 { return &c.Labor.BaseWage },
		func(f *fileConfig) *int64 { return f.Labor.BaseWage }),
	moneySetting("labor", "min_wage_village",
		func(c *Config) *int64 { return &c.Labor.MinWageVillage },
		func(f *fileConfig) *int64 { return f.Labor.MinWageVillage }),
	moneySetting("labor", "min_wage_town",
		func(c *Config) *int64 { return &c.Labor.MinWageTown },
		func(f *fileConfig) *int64 { return f.Labor.MinWageTown }),
	moneySetting("labor", "min_wage_city",
		func(c *Config) *int64 { return &c.Labor.MinWageCity },
		func(f *fileConfig) *int64 { return f.Labor.MinWageCity }),
	moneySetting("labor", "participation_bps",
		func(c *Config) *int64 { return &c.Labor.ParticipationBPS },
		func(f *fileConfig) *int64 { return f.Labor.ParticipationBPS }),
	moneySetting("labor", "base_housing",
		func(c *Config) *int64 { return &c.Labor.BaseHousing },
		func(f *fileConfig) *int64 { return f.Labor.BaseHousing }),
	moneySetting("labor", "npc_productivity_bps",
		func(c *Config) *int64 { return &c.Labor.NPCProductivityBPS },
		func(f *fileConfig) *int64 { return f.Labor.NPCProductivityBPS }),
	moneySetting("labor", "fee_bps",
		func(c *Config) *int64 { return &c.Labor.FeeBPS },
		func(f *fileConfig) *int64 { return f.Labor.FeeBPS }),
	moneySetting("labor", "budget_slack_bps",
		func(c *Config) *int64 { return &c.Labor.BudgetSlackBPS },
		func(f *fileConfig) *int64 { return f.Labor.BudgetSlackBPS }),
	moneySetting("labor", "hungry_output_bps",
		func(c *Config) *int64 { return &c.Labor.HungryOutputBPS },
		func(f *fileConfig) *int64 { return f.Labor.HungryOutputBPS }),
	moneySetting("labor", "decay_bps_per_day",
		func(c *Config) *int64 { return &c.Labor.DecayBPSPerDay },
		func(f *fileConfig) *int64 { return f.Labor.DecayBPSPerDay }),
	moneySetting("labor", "repair_below_bps",
		func(c *Config) *int64 { return &c.Labor.RepairBelowBPS },
		func(f *fileConfig) *int64 { return f.Labor.RepairBelowBPS }),
	moneySetting("labor", "worn_bps",
		func(c *Config) *int64 { return &c.Labor.WornBPS },
		func(f *fileConfig) *int64 { return f.Labor.WornBPS }),
	moneySetting("labor", "closed_bps",
		func(c *Config) *int64 { return &c.Labor.ClosedBPS },
		func(f *fileConfig) *int64 { return f.Labor.ClosedBPS }),
	moneySetting("labor", "worn_output_bps",
		func(c *Config) *int64 { return &c.Labor.WornOutputBPS },
		func(f *fileConfig) *int64 { return f.Labor.WornOutputBPS }),
	moneySetting("labor", "repair_shifts_full",
		func(c *Config) *int64 { return &c.Labor.RepairShiftsFull },
		func(f *fileConfig) *int64 { return f.Labor.RepairShiftsFull }),
	moneySetting("labor", "repair_material_share_bps",
		func(c *Config) *int64 { return &c.Labor.RepairMaterialShareBPS },
		func(f *fileConfig) *int64 { return f.Labor.RepairMaterialShareBPS }),
	moneySetting("labor", "hungry_shift_hunger",
		func(c *Config) *int64 { return &c.Labor.HungryShiftHunger },
		func(f *fileConfig) *int64 { return f.Labor.HungryShiftHunger }),
	moneySetting("currency", "desk_slippage_bps",
		func(c *Config) *int64 { return &c.Currency.DeskSlippageBPS },
		func(f *fileConfig) *int64 { return f.Currency.DeskSlippageBPS }),
	moneyListSetting("currency", "desk_presets",
		func(c *Config) *[]int64 { return &c.Currency.DeskPresets },
		func(f *fileConfig) []int64 { return f.Currency.DeskPresets }),
	moneySetting("currency", "fx_reserve_fee_bps",
		func(c *Config) *int64 { return &c.Currency.FXReserveFeeBPS },
		func(f *fileConfig) *int64 { return f.Currency.FXReserveFeeBPS }),
	moneySetting("currency", "fx_max_move_bps",
		func(c *Config) *int64 { return &c.Currency.FXMaxMoveBPS },
		func(f *fileConfig) *int64 { return f.Currency.FXMaxMoveBPS }),
	moneySetting("currency", "fx_min_trades",
		func(c *Config) *int64 { return &c.Currency.FXMinTrades },
		func(f *fileConfig) *int64 { return f.Currency.FXMinTrades }),
	moneySetting("currency", "fx_window_periods",
		func(c *Config) *int64 { return &c.Currency.FXWindowPeriods },
		func(f *fileConfig) *int64 { return f.Currency.FXWindowPeriods }),
	moneySetting("currency", "fx_min_order_sup",
		func(c *Config) *int64 { return &c.Currency.FXMinOrderSUP },
		func(f *fileConfig) *int64 { return f.Currency.FXMinOrderSUP }),
	moneySetting("currency", "fx_book_limit",
		func(c *Config) *int64 { return &c.Currency.FXBookLimit },
		func(f *fileConfig) *int64 { return f.Currency.FXBookLimit }),
	moneySetting("currency", "fx_max_open_orders",
		func(c *Config) *int64 { return &c.Currency.FXMaxOpenOrders },
		func(f *fileConfig) *int64 { return f.Currency.FXMaxOpenOrders }),
	moneySetting("currency", "fx_convert_slippage_bps",
		func(c *Config) *int64 { return &c.Currency.FXConvertSlippageBPS },
		func(f *fileConfig) *int64 { return f.Currency.FXConvertSlippageBPS }),
	durationSetting("currency", "fx_order_ttl",
		func(c *Config) *time.Duration { return &c.Currency.FXOrderTTL },
		func(f *fileConfig) *string { return f.Currency.FXOrderTTL }),
	durationSetting("currency", "fx_period",
		func(c *Config) *time.Duration { return &c.Currency.FXPeriod },
		func(f *fileConfig) *string { return f.Currency.FXPeriod }),
	moneyListSetting("currency", "fx_unit_presets",
		func(c *Config) *[]int64 { return &c.Currency.FXUnitPresets },
		func(f *fileConfig) []int64 { return f.Currency.FXUnitPresets }),
	moneySetting("currency", "reserve_gold_haircut_bps",
		func(c *Config) *int64 { return &c.Currency.ReserveGoldHaircutBPS },
		func(f *fileConfig) *int64 { return f.Currency.ReserveGoldHaircutBPS }),
	moneySetting("currency", "reserve_withdraw_notice_hours",
		func(c *Config) *int64 { return &c.Currency.ReserveWithdrawNoticeHours },
		func(f *fileConfig) *int64 { return f.Currency.ReserveWithdrawNoticeHours }),
	moneySetting("currency", "reserve_policy_rate_bps",
		func(c *Config) *int64 { return &c.Currency.ReservePolicyRateBPS },
		func(f *fileConfig) *int64 { return f.Currency.ReservePolicyRateBPS }),
	moneySetting("currency", "intervention_cap_bps",
		func(c *Config) *int64 { return &c.Currency.InterventionCapBPS },
		func(f *fileConfig) *int64 { return f.Currency.InterventionCapBPS }),
	moneySetting("currency", "intervention_pot_floor_bps",
		func(c *Config) *int64 { return &c.Currency.InterventionPotFloorBPS },
		func(f *fileConfig) *int64 { return f.Currency.InterventionPotFloorBPS }),
	moneySetting("currency", "wind_down_days",
		func(c *Config) *int64 { return &c.Currency.WindDownDays },
		func(f *fileConfig) *int64 { return f.Currency.WindDownDays }),
	moneySetting("currency", "macro_m_norm_bps",
		func(c *Config) *int64 { return &c.Currency.MacroMNormBPS },
		func(f *fileConfig) *int64 { return f.Currency.MacroMNormBPS }),
	moneySetting("currency", "macro_kappa_bps",
		func(c *Config) *int64 { return &c.Currency.MacroKappaBPS },
		func(f *fileConfig) *int64 { return f.Currency.MacroKappaBPS }),
	moneySetting("currency", "macro_pi_max_bps",
		func(c *Config) *int64 { return &c.Currency.MacroPiMaxBPS },
		func(f *fileConfig) *int64 { return f.Currency.MacroPiMaxBPS }),
	moneySetting("currency", "macro_w_tradable_bps",
		func(c *Config) *int64 { return &c.Currency.MacroWTradableBPS },
		func(f *fileConfig) *int64 { return f.Currency.MacroWTradableBPS }),
	durationSetting("currency", "intervention_delay",
		func(c *Config) *time.Duration { return &c.Currency.InterventionDelay },
		func(f *fileConfig) *string { return f.Currency.InterventionDelay }),
	moneySetting("currency", "charter_r0",
		func(c *Config) *int64 { return &c.Currency.CharterR0 },
		func(f *fileConfig) *int64 { return f.Currency.CharterR0 }),
	moneySetting("currency", "charter_fee",
		func(c *Config) *int64 { return &c.Currency.CharterFee },
		func(f *fileConfig) *int64 { return f.Currency.CharterFee }),
	moneySetting("currency", "charter_min_deposit",
		func(c *Config) *int64 { return &c.Currency.CharterMinDeposit },
		func(f *fileConfig) *int64 { return f.Currency.CharterMinDeposit }),
	moneySetting("currency", "mint_fee_bps",
		func(c *Config) *int64 { return &c.Currency.MintFeeBPS },
		func(f *fileConfig) *int64 { return f.Currency.MintFeeBPS }),
	moneySetting("currency", "auto_charter_share_bps",
		func(c *Config) *int64 { return &c.Currency.AutoCharterShareBPS },
		func(f *fileConfig) *int64 { return f.Currency.AutoCharterShareBPS }),
	moneySetting("currency", "auto_charter_floor",
		func(c *Config) *int64 { return &c.Currency.AutoCharterFloor },
		func(f *fileConfig) *int64 { return f.Currency.AutoCharterFloor }),
	moneySetting("labor", "npc_shifts_per_slot_day",
		func(c *Config) *int64 { return &c.Labor.NPCShiftsPerSlotDay },
		func(f *fileConfig) *int64 { return f.Labor.NPCShiftsPerSlotDay }),
	moneySetting("labor", "journeyman_shifts",
		func(c *Config) *int64 { return &c.Labor.JourneymanShifts },
		func(f *fileConfig) *int64 { return f.Labor.JourneymanShifts }),
	moneySetting("labor", "master_shifts",
		func(c *Config) *int64 { return &c.Labor.MasterShifts },
		func(f *fileConfig) *int64 { return f.Labor.MasterShifts }),
	moneySetting("labor", "apprentice_bps",
		func(c *Config) *int64 { return &c.Labor.ApprenticeBPS },
		func(f *fileConfig) *int64 { return f.Labor.ApprenticeBPS }),
	moneySetting("labor", "journeyman_bps",
		func(c *Config) *int64 { return &c.Labor.JourneymanBPS },
		func(f *fileConfig) *int64 { return f.Labor.JourneymanBPS }),
	moneySetting("labor", "master_bps",
		func(c *Config) *int64 { return &c.Labor.MasterBPS },
		func(f *fileConfig) *int64 { return f.Labor.MasterBPS }),
	moneySetting("labor", "tight_balanced_bps",
		func(c *Config) *int64 { return &c.Labor.TightBalancedBPS },
		func(f *fileConfig) *int64 { return f.Labor.TightBalancedBPS }),
	moneySetting("labor", "tight_tight_bps",
		func(c *Config) *int64 { return &c.Labor.TightTightBPS },
		func(f *fileConfig) *int64 { return f.Labor.TightTightBPS }),
	moneySetting("labor", "tight_short_bps",
		func(c *Config) *int64 { return &c.Labor.TightShortBPS },
		func(f *fileConfig) *int64 { return f.Labor.TightShortBPS }),
	moneySetting("labor", "wage_slack_bps",
		func(c *Config) *int64 { return &c.Labor.WageSlackBPS },
		func(f *fileConfig) *int64 { return f.Labor.WageSlackBPS }),
	moneySetting("labor", "wage_balanced_bps",
		func(c *Config) *int64 { return &c.Labor.WageBalancedBPS },
		func(f *fileConfig) *int64 { return f.Labor.WageBalancedBPS }),
	moneySetting("labor", "wage_tight_bps",
		func(c *Config) *int64 { return &c.Labor.WageTightBPS },
		func(f *fileConfig) *int64 { return f.Labor.WageTightBPS }),
	moneySetting("labor", "wage_short_bps",
		func(c *Config) *int64 { return &c.Labor.WageShortBPS },
		func(f *fileConfig) *int64 { return f.Labor.WageShortBPS }),
	moneyListSetting("labor", "hire_presets",
		func(c *Config) *[]int64 { return &c.Labor.HirePresets },
		func(f *fileConfig) []int64 { return f.Labor.HirePresets }),
	moneyListSetting("labor", "wage_presets",
		func(c *Config) *[]int64 { return &c.Labor.WagePresets },
		func(f *fileConfig) []int64 { return f.Labor.WagePresets }),

	durationSetting("legislature", "vote_window",
		func(c *Config) *time.Duration { return &c.Legislature.VoteWindow },
		func(f *fileConfig) *string { return f.Legislature.VoteWindow }),
	limitSetting("legislature", "list_size",
		func(c *Config) *int { return &c.Legislature.ListSize },
		func(f *fileConfig) *int { return f.Legislature.ListSize }),
	durationSetting("city", "period",
		func(c *Config) *time.Duration { return &c.City.Period },
		func(f *fileConfig) *string { return f.City.Period }),
	limitSetting("property", "foreclosure_periods",
		func(c *Config) *int { return &c.Property.ForeclosurePeriods },
		func(f *fileConfig) *int { return f.Property.ForeclosurePeriods }),
	limitSetting("property", "eviction_periods",
		func(c *Config) *int { return &c.Property.EvictionPeriods },
		func(f *fileConfig) *int { return f.Property.EvictionPeriods }),
	limitSetting("property", "max_owned",
		func(c *Config) *int { return &c.Property.MaxOwned },
		func(f *fileConfig) *int { return f.Property.MaxOwned }),
	moneySetting("property", "max_price",
		func(c *Config) *int64 { return &c.Property.MaxPrice },
		func(f *fileConfig) *int64 { return f.Property.MaxPrice }),
	moneySetting("property", "max_rent",
		func(c *Config) *int64 { return &c.Property.MaxRent },
		func(f *fileConfig) *int64 { return f.Property.MaxRent }),
	durationSetting("property", "rest_cooldown",
		func(c *Config) *time.Duration { return &c.Property.RestCooldown },
		func(f *fileConfig) *string { return f.Property.RestCooldown }),
	limitSetting("postgres", "max_conns",
		func(c *Config) *int { return &c.Postgres.MaxConns },
		func(f *fileConfig) *int { return f.Postgres.MaxConns }),
	durationSetting("postgres", "idle_in_transaction_timeout",
		func(c *Config) *time.Duration { return &c.Postgres.IdleInTransactionTimeout },
		func(f *fileConfig) *string { return f.Postgres.IdleInTransactionTimeout }),
	durationSetting("game", "command_timeout",
		func(c *Config) *time.Duration { return &c.Game.CommandTimeout },
		func(f *fileConfig) *string { return f.Game.CommandTimeout }),
	moneySetting("achievements", "player_daily_cap",
		func(c *Config) *int64 { return &c.Achievements.PlayerDailyCap },
		func(f *fileConfig) *int64 { return f.Achievements.PlayerDailyCap }),
	moneySetting("achievements", "economy_daily_cap",
		func(c *Config) *int64 { return &c.Achievements.EconomyDailyCap },
		func(f *fileConfig) *int64 { return f.Achievements.EconomyDailyCap }),

	limitSetting("crime", "nerve_max",
		func(c *Config) *int { return &c.Crime.NerveMax },
		func(f *fileConfig) *int { return f.Crime.NerveMax }),
	limitSetting("crime", "nerve_regen_amount",
		func(c *Config) *int { return &c.Crime.NerveRegenAmount },
		func(f *fileConfig) *int { return f.Crime.NerveRegenAmount }),
	durationSetting("crime", "nerve_regen_interval",
		func(c *Config) *time.Duration { return &c.Crime.NerveRegenInterval },
		func(f *fileConfig) *string { return f.Crime.NerveRegenInterval }),
	limitSetting("crime", "heat_max",
		func(c *Config) *int { return &c.Crime.HeatMax },
		func(f *fileConfig) *int { return f.Crime.HeatMax }),
	limitSetting("crime", "heat_decay_per_hour",
		func(c *Config) *int { return &c.Crime.HeatDecayPerHour },
		func(f *fileConfig) *int { return f.Crime.HeatDecayPerHour }),
	limitSetting("crime", "protect_min_level",
		func(c *Config) *int { return &c.Crime.ProtectMinLevel },
		func(f *fileConfig) *int { return f.Crime.ProtectMinLevel }),
	durationSetting("crime", "protect_min_age",
		func(c *Config) *time.Duration { return &c.Crime.ProtectMinAge },
		func(f *fileConfig) *string { return f.Crime.ProtectMinAge }),
	durationSetting("crime", "active_window",
		func(c *Config) *time.Duration { return &c.Crime.ActiveWindow },
		func(f *fileConfig) *string { return f.Crime.ActiveWindow }),
	durationSetting("crime", "arrival_linger",
		func(c *Config) *time.Duration { return &c.Crime.ArrivalLinger },
		func(f *fileConfig) *string { return f.Crime.ArrivalLinger }),
	durationSetting("crime", "victim_cooldown",
		func(c *Config) *time.Duration { return &c.Crime.VictimCooldown },
		func(f *fileConfig) *string { return f.Crime.VictimCooldown }),
	durationSetting("crime", "thief_cooldown",
		func(c *Config) *time.Duration { return &c.Crime.ThiefCooldown },
		func(f *fileConfig) *string { return f.Crime.ThiefCooldown }),
	durationSetting("crime", "report_window",
		func(c *Config) *time.Duration { return &c.Crime.ReportWindow },
		func(f *fileConfig) *string { return f.Crime.ReportWindow }),
	durationSetting("crime", "investigation_duration",
		func(c *Config) *time.Duration { return &c.Crime.InvestigationDuration },
		func(f *fileConfig) *string { return f.Crime.InvestigationDuration }),
	limitSetting("crime", "investigation_base_bps",
		func(c *Config) *int { return &c.Crime.InvestigationBaseBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationBaseBPS }),
	limitSetting("crime", "investigation_per_heat_bps",
		func(c *Config) *int { return &c.Crime.InvestigationPerHeatBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationPerHeatBPS }),
	limitSetting("crime", "investigation_witness_bonus_bps",
		func(c *Config) *int { return &c.Crime.InvestigationWitnessBonusBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationWitnessBonusBPS }),
	limitSetting("crime", "investigation_effort_weight_bps",
		func(c *Config) *int { return &c.Crime.InvestigationEffortWeightBPS },
		func(f *fileConfig) *int { return f.Crime.InvestigationEffortWeightBPS }),
	moneySetting("crime", "npc_daily_cap",
		func(c *Config) *int64 { return &c.Crime.NPCDailyCap },
		func(f *fileConfig) *int64 { return f.Crime.NPCDailyCap }),
	limitSetting("crime", "gear_max_success_bps",
		func(c *Config) *int { return &c.Crime.GearMaxSuccessBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxSuccessBPS }),
	limitSetting("crime", "gear_max_catch_bps",
		func(c *Config) *int { return &c.Crime.GearMaxCatchBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxCatchBPS }),
	limitSetting("crime", "gear_max_witness_bps",
		func(c *Config) *int { return &c.Crime.GearMaxWitnessBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxWitnessBPS }),
	limitSetting("crime", "gear_max_solve_bps",
		func(c *Config) *int { return &c.Crime.GearMaxSolveBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxSolveBPS }),
	limitSetting("crime", "gear_max_reward_bps",
		func(c *Config) *int { return &c.Crime.GearMaxRewardBPS },
		func(f *fileConfig) *int { return f.Crime.GearMaxRewardBPS }),
	limitSetting("crime", "gear_max_nerve",
		func(c *Config) *int { return &c.Crime.GearMaxNerve },
		func(f *fileConfig) *int { return f.Crime.GearMaxNerve }),

	durationSetting("trade", "market_order_ttl",
		func(c *Config) *time.Duration { return &c.Trade.MarketOrderTTL },
		func(f *fileConfig) *string { return f.Trade.MarketOrderTTL }),
	limitSetting("trade", "market_max_open_orders",
		func(c *Config) *int { return &c.Trade.MarketMaxOpenOrders },
		func(f *fileConfig) *int { return f.Trade.MarketMaxOpenOrders }),
	limitSetting("trade", "village_stalls_post",
		func(c *Config) *int { return &c.Trade.VillageStallsPost },
		func(f *fileConfig) *int { return f.Trade.VillageStallsPost }),
	limitSetting("trade", "village_stalls_hall",
		func(c *Config) *int { return &c.Trade.VillageStallsHall },
		func(f *fileConfig) *int { return f.Trade.VillageStallsHall }),
	limitSetting("trade", "village_stalls_per_player_post",
		func(c *Config) *int { return &c.Trade.VillageStallsPerPlayerPost },
		func(f *fileConfig) *int { return f.Trade.VillageStallsPerPlayerPost }),
	limitSetting("trade", "village_stalls_per_player_hall",
		func(c *Config) *int { return &c.Trade.VillageStallsPerPlayerHall },
		func(f *fileConfig) *int { return f.Trade.VillageStallsPerPlayerHall }),
	limitSetting("trade", "market_day_every_days",
		func(c *Config) *int { return &c.Trade.MarketDayEveryDays },
		func(f *fileConfig) *int { return f.Trade.MarketDayEveryDays }),
	limitSetting("trade", "market_max_quantity",
		func(c *Config) *int { return &c.Trade.MarketMaxQuantity },
		func(f *fileConfig) *int { return f.Trade.MarketMaxQuantity }),
	moneySetting("trade", "market_max_price",
		func(c *Config) *int64 { return &c.Trade.MarketMaxPrice },
		func(f *fileConfig) *int64 { return f.Trade.MarketMaxPrice }),
	durationListSetting("trade", "auction_durations",
		func(c *Config) *[]time.Duration { return &c.Trade.AuctionDurations },
		func(f *fileConfig) []string { return f.Trade.AuctionDurations }),
	moneySetting("trade", "auction_max_reserve",
		func(c *Config) *int64 { return &c.Trade.AuctionMaxReserve },
		func(f *fileConfig) *int64 { return f.Trade.AuctionMaxReserve }),
	limitSetting("trade", "auction_step_bps",
		func(c *Config) *int { return &c.Trade.AuctionStepBPS },
		func(f *fileConfig) *int { return f.Trade.AuctionStepBPS }),
	moneySetting("trade", "auction_min_step",
		func(c *Config) *int64 { return &c.Trade.AuctionMinStep },
		func(f *fileConfig) *int64 { return f.Trade.AuctionMinStep }),
	limitSetting("trade", "auction_max_open",
		func(c *Config) *int { return &c.Trade.AuctionMaxOpen },
		func(f *fileConfig) *int { return f.Trade.AuctionMaxOpen }),
	moneyListSetting("trade", "auction_reserves_bps",
		func(c *Config) *[]int64 { return &c.Trade.AuctionReservesBPS },
		func(f *fileConfig) []int64 { return f.Trade.AuctionReservesBPS }),

	durationSetting("company", "period",
		func(c *Config) *time.Duration { return &c.Company.Period },
		func(f *fileConfig) *string { return f.Company.Period }),
	limitSetting("company", "max_per_player",
		func(c *Config) *int { return &c.Company.MaxPerPlayer },
		func(f *fileConfig) *int { return f.Company.MaxPerPlayer }),
	limitSetting("company", "name_min_length",
		func(c *Config) *int { return &c.Company.NameMinLength },
		func(f *fileConfig) *int { return f.Company.NameMinLength }),
	limitSetting("company", "name_max_length",
		func(c *Config) *int { return &c.Company.NameMaxLength },
		func(f *fileConfig) *int { return f.Company.NameMaxLength }),
	moneySetting("company", "founding_shares",
		func(c *Config) *int64 { return &c.Company.FoundingShares },
		func(f *fileConfig) *int64 { return f.Company.FoundingShares }),
	limitSetting("company", "insolvency_periods",
		func(c *Config) *int { return &c.Company.InsolvencyPeriods },
		func(f *fileConfig) *int { return f.Company.InsolvencyPeriods }),
	moneySetting("company", "npc_city_period_cap",
		func(c *Config) *int64 { return &c.Company.NPCCityPeriodCap },
		func(f *fileConfig) *int64 { return f.Company.NPCCityPeriodCap }),
	limitSetting("company", "max_openings",
		func(c *Config) *int { return &c.Company.MaxOpenings },
		func(f *fileConfig) *int { return f.Company.MaxOpenings }),
	limitSetting("company", "price_step_bps",
		func(c *Config) *int { return &c.Company.PriceStepBPS },
		func(f *fileConfig) *int { return f.Company.PriceStepBPS }),
	limitSetting("company", "citizen_shifts_per_period",
		func(c *Config) *int { return &c.Company.CitizenShiftsPerPeriod },
		func(f *fileConfig) *int { return f.Company.CitizenShiftsPerPeriod }),
	limitSetting("company", "citizen_productivity_bps",
		func(c *Config) *int { return &c.Company.CitizenProductivityBPS },
		func(f *fileConfig) *int { return f.Company.CitizenProductivityBPS }),
	limitSetting("company", "citizen_labour_share_bps",
		func(c *Config) *int { return &c.Company.CitizenLabourShareBPS },
		func(f *fileConfig) *int { return f.Company.CitizenLabourShareBPS }),
	limitSetting("company", "max_running_orders",
		func(c *Config) *int { return &c.Company.MaxRunningOrders },
		func(f *fileConfig) *int { return f.Company.MaxRunningOrders }),
	limitSetting("company", "max_designs",
		func(c *Config) *int { return &c.Company.MaxDesigns },
		func(f *fileConfig) *int { return f.Company.MaxDesigns }),
	limitSetting("company", "max_listings",
		func(c *Config) *int { return &c.Company.MaxListings },
		func(f *fileConfig) *int { return f.Company.MaxListings }),
	limitSetting("company", "design_min_skill",
		func(c *Config) *int { return &c.Company.DesignMinSkill },
		func(f *fileConfig) *int { return f.Company.DesignMinSkill }),
	limitSetting("company", "quick_order_units",
		func(c *Config) *int { return &c.Company.QuickOrderUnits },
		func(f *fileConfig) *int { return f.Company.QuickOrderUnits }),
	durationSetting("company", "reverse_time",
		func(c *Config) *time.Duration { return &c.Company.ReverseTime },
		func(f *fileConfig) *string { return f.Company.ReverseTime }),
	durationSetting("company", "improvement_time",
		func(c *Config) *time.Duration { return &c.Company.ImprovementTime },
		func(f *fileConfig) *string { return f.Company.ImprovementTime }),
	moneySetting("company", "improvement_cost",
		func(c *Config) *int64 { return &c.Company.ImprovementCost },
		func(f *fileConfig) *int64 { return f.Company.ImprovementCost }),
	durationSetting("company", "retrofit_time",
		func(c *Config) *time.Duration { return &c.Company.RetrofitTime },
		func(f *fileConfig) *string { return f.Company.RetrofitTime }),
	limitSetting("company", "obsolescence_decay_bps",
		func(c *Config) *int { return &c.Company.ObsolescenceDecayBPS },
		func(f *fileConfig) *int { return f.Company.ObsolescenceDecayBPS }),
	limitSetting("company", "obsolescence_floor_bps",
		func(c *Config) *int { return &c.Company.ObsolescenceFloorBPS },
		func(f *fileConfig) *int { return f.Company.ObsolescenceFloorBPS }),
	durationSetting("company", "recruit_check_every",
		func(c *Config) *time.Duration { return &c.Company.RecruitCheckEvery },
		func(f *fileConfig) *string { return f.Company.RecruitCheckEvery }),
	limitSetting("company", "recruit_checks",
		func(c *Config) *int { return &c.Company.RecruitChecks },
		func(f *fileConfig) *int { return f.Company.RecruitChecks }),
	limitSetting("company", "recruit_max_campaigns",
		func(c *Config) *int { return &c.Company.RecruitMaxCampaigns },
		func(f *fileConfig) *int { return f.Company.RecruitMaxCampaigns }),
	limitSetting("company", "recruit_max_positions",
		func(c *Config) *int { return &c.Company.RecruitMaxPositions },
		func(f *fileConfig) *int { return f.Company.RecruitMaxPositions }),
	limitSetting("company", "recruit_max_candidates",
		func(c *Config) *int { return &c.Company.RecruitMaxCandidates },
		func(f *fileConfig) *int { return f.Company.RecruitMaxCandidates }),
	durationSetting("company", "recruit_patience",
		func(c *Config) *time.Duration { return &c.Company.RecruitPatience },
		func(f *fileConfig) *string { return f.Company.RecruitPatience }),
	limitSetting("company", "recruit_max_staff",
		func(c *Config) *int { return &c.Company.RecruitMaxStaff },
		func(f *fileConfig) *int { return f.Company.RecruitMaxStaff }),

	durationSetting("military", "period",
		func(c *Config) *time.Duration { return &c.Military.Period },
		func(f *fileConfig) *string { return f.Military.Period }),
	limitSetting("military", "readiness_loss_bps",
		func(c *Config) *int { return &c.Military.ReadinessLossBPS },
		func(f *fileConfig) *int { return f.Military.ReadinessLossBPS }),
	limitSetting("military", "readiness_recovery_bps",
		func(c *Config) *int { return &c.Military.ReadinessRecoveryBPS },
		func(f *fileConfig) *int { return f.Military.ReadinessRecoveryBPS }),
	limitSetting("military", "reference_radar_km",
		func(c *Config) *int { return &c.Military.ReferenceRadarKM },
		func(f *fileConfig) *int { return f.Military.ReferenceRadarKM }),
	durationSetting("military", "licence_revoke_notice",
		func(c *Config) *time.Duration { return &c.Military.LicenceRevokeNotice },
		func(f *fileConfig) *string { return f.Military.LicenceRevokeNotice }),
	limitSetting("military", "ended_licences_shown",
		func(c *Config) *int { return &c.Military.EndedLicencesShown },
		func(f *fileConfig) *int { return f.Military.EndedLicencesShown }),

	durationSetting("diplomacy", "sanction_notice",
		func(c *Config) *time.Duration { return &c.Diplomacy.SanctionNotice },
		func(f *fileConfig) *string { return f.Diplomacy.SanctionNotice }),
	durationSetting("diplomacy", "sanction_min_duration",
		func(c *Config) *time.Duration { return &c.Diplomacy.SanctionMinDuration },
		func(f *fileConfig) *string { return f.Diplomacy.SanctionMinDuration }),
	durationSetting("diplomacy", "treaty_offer_ttl",
		func(c *Config) *time.Duration { return &c.Diplomacy.TreatyOfferTTL },
		func(f *fileConfig) *string { return f.Diplomacy.TreatyOfferTTL }),
	durationSetting("diplomacy", "ended_shown_for",
		func(c *Config) *time.Duration { return &c.Diplomacy.EndedShownFor },
		func(f *fileConfig) *string { return f.Diplomacy.EndedShownFor }),
	limitSetting("diplomacy", "history_page_size",
		func(c *Config) *int { return &c.Diplomacy.HistoryPageSize },
		func(f *fileConfig) *int { return f.Diplomacy.HistoryPageSize }),

	durationSetting("war", "declaration_notice",
		func(c *Config) *time.Duration { return &c.War.DeclarationNotice },
		func(f *fileConfig) *string { return f.War.DeclarationNotice }),
	durationSetting("war", "proposal_ttl",
		func(c *Config) *time.Duration { return &c.War.ProposalTTL },
		func(f *fileConfig) *string { return f.War.ProposalTTL }),
	durationSetting("war", "ended_shown_for",
		func(c *Config) *time.Duration { return &c.War.EndedShownFor },
		func(f *fileConfig) *string { return f.War.EndedShownFor }),
	limitSetting("war", "board_operations",
		func(c *Config) *int { return &c.War.BoardOperations },
		func(f *fileConfig) *int { return f.War.BoardOperations }),
	limitSetting("war", "notice_cap",
		func(c *Config) *int { return &c.War.NoticeCap },
		func(f *fileConfig) *int { return f.War.NoticeCap }),

	limitSetting("missions", "max_active",
		func(c *Config) *int { return &c.Missions.MaxActive },
		func(f *fileConfig) *int { return f.Missions.MaxActive }),
	moneySetting("missions", "player_daily_cap",
		func(c *Config) *int64 { return &c.Missions.PlayerDailyCap },
		func(f *fileConfig) *int64 { return f.Missions.PlayerDailyCap }),
	moneySetting("missions", "economy_daily_cap",
		func(c *Config) *int64 { return &c.Missions.EconomyDailyCap },
		func(f *fileConfig) *int64 { return f.Missions.EconomyDailyCap }),

	limitSetting("factions", "name_min_length",
		func(c *Config) *int { return &c.Factions.NameMinLength },
		func(f *fileConfig) *int { return f.Factions.NameMinLength }),
	limitSetting("factions", "name_max_length",
		func(c *Config) *int { return &c.Factions.NameMaxLength },
		func(f *fileConfig) *int { return f.Factions.NameMaxLength }),
	limitSetting("factions", "max_members",
		func(c *Config) *int { return &c.Factions.MaxMembers },
		func(f *fileConfig) *int { return f.Factions.MaxMembers }),
	limitSetting("factions", "max_pending",
		func(c *Config) *int { return &c.Factions.MaxPending },
		func(f *fileConfig) *int { return f.Factions.MaxPending }),
	limitSetting("factions", "min_founders",
		func(c *Config) *int { return &c.Factions.MinFounders },
		func(f *fileConfig) *int { return f.Factions.MinFounders }),
	limitSetting("factions", "list_size",
		func(c *Config) *int { return &c.Factions.ListSize },
		func(f *fileConfig) *int { return f.Factions.ListSize }),

	durationSetting("anticheat", "window",
		func(c *Config) *time.Duration { return &c.AntiCheat.Window },
		func(f *fileConfig) *string { return f.AntiCheat.Window }),
	limitSetting("anticheat", "one_way_count",
		func(c *Config) *int { return &c.AntiCheat.OneWayCount },
		func(f *fileConfig) *int { return f.AntiCheat.OneWayCount }),
	moneySetting("anticheat", "one_way_min_total",
		func(c *Config) *int64 { return &c.AntiCheat.OneWayMinTotal },
		func(f *fileConfig) *int64 { return f.AntiCheat.OneWayMinTotal }),
	limitSetting("anticheat", "one_way_ratio_bps",
		func(c *Config) *int { return &c.AntiCheat.OneWayRatioBPS },
		func(f *fileConfig) *int { return f.AntiCheat.OneWayRatioBPS }),
	limitSetting("anticheat", "off_market_bps",
		func(c *Config) *int { return &c.AntiCheat.OffMarketBPS },
		func(f *fileConfig) *int { return f.AntiCheat.OffMarketBPS }),
	moneySetting("anticheat", "off_market_min_value",
		func(c *Config) *int64 { return &c.AntiCheat.OffMarketMinValue },
		func(f *fileConfig) *int64 { return f.AntiCheat.OffMarketMinValue }),
	limitSetting("anticheat", "single_partner_min_count",
		func(c *Config) *int { return &c.AntiCheat.SinglePartnerMinCount },
		func(f *fileConfig) *int { return f.AntiCheat.SinglePartnerMinCount }),
	limitSetting("anticheat", "single_partner_share_bps",
		func(c *Config) *int { return &c.AntiCheat.SinglePartnerShareBPS },
		func(f *fileConfig) *int { return f.AntiCheat.SinglePartnerShareBPS }),
	limitSetting("anticheat", "commands_per_minute",
		func(c *Config) *int { return &c.AntiCheat.CommandsPerMinute },
		func(f *fileConfig) *int { return f.AntiCheat.CommandsPerMinute }),
	limitSetting("anticheat", "wash_trade_count",
		func(c *Config) *int { return &c.AntiCheat.WashTradeCount },
		func(f *fileConfig) *int { return f.AntiCheat.WashTradeCount }),
	moneySetting("anticheat", "hold_above",
		func(c *Config) *int64 { return &c.AntiCheat.HoldAbove },
		func(f *fileConfig) *int64 { return f.AntiCheat.HoldAbove }),

	durationSetting("input", "ttl",
		func(c *Config) *time.Duration { return &c.Input.TTL },
		func(f *fileConfig) *string { return f.Input.TTL }),
	durationSetting("input", "cooldown",
		func(c *Config) *time.Duration { return &c.Input.Cooldown },
		func(f *fileConfig) *string { return f.Input.Cooldown }),
	limitSetting("input", "max_length",
		func(c *Config) *int { return &c.Input.MaxLength },
		func(f *fileConfig) *int { return f.Input.MaxLength }),

	durationSetting("announce", "window",
		func(c *Config) *time.Duration { return &c.Announce.Window },
		func(f *fileConfig) *string { return f.Announce.Window }),
	limitSetting("announce", "max_per_window",
		func(c *Config) *int { return &c.Announce.MaxPerWindow },
		func(f *fileConfig) *int { return f.Announce.MaxPerWindow }),
	durationSetting("announce", "village_merge_window",
		func(c *Config) *time.Duration { return &c.Announce.VillageMergeWindow },
		func(f *fileConfig) *string { return f.Announce.VillageMergeWindow }),
	durationSetting("announce", "village_min_gap",
		func(c *Config) *time.Duration { return &c.Announce.VillageMinGap },
		func(f *fileConfig) *string { return f.Announce.VillageMinGap }),
	limitSetting("announce", "village_literacy_step_percent",
		func(c *Config) *int { return &c.Announce.VillageLiteracyStep },
		func(f *fileConfig) *int { return f.Announce.VillageLiteracyStep }),
	durationSetting("announce", "village_flush_interval",
		func(c *Config) *time.Duration { return &c.Announce.VillageFlushInterval },
		func(f *fileConfig) *string { return f.Announce.VillageFlushInterval }),

	limitSetting("notifications", "inbox_page_size",
		func(c *Config) *int { return &c.Notifications.InboxPageSize },
		func(f *fileConfig) *int { return f.Notifications.InboxPageSize }),
	durationSetting("notifications", "edit_throttle",
		func(c *Config) *time.Duration { return &c.Notifications.EditThrottle },
		func(f *fileConfig) *string { return f.Notifications.EditThrottle }),
	durationSetting("notifications", "reminder_delay",
		func(c *Config) *time.Duration { return &c.Notifications.ReminderDelay },
		func(f *fileConfig) *string { return f.Notifications.ReminderDelay }),
	durationSetting("notifications", "reminder_check_interval",
		func(c *Config) *time.Duration { return &c.Notifications.ReminderCheckInterval },
		func(f *fileConfig) *string { return f.Notifications.ReminderCheckInterval }),
	durationSetting("notifications", "retention",
		func(c *Config) *time.Duration { return &c.Notifications.Retention },
		func(f *fileConfig) *string { return f.Notifications.Retention }),
	durationSetting("notifications", "prune_interval",
		func(c *Config) *time.Duration { return &c.Notifications.PruneInterval },
		func(f *fileConfig) *string { return f.Notifications.PruneInterval }),
	durationSetting("notifications", "hunger_alert_cooldown",
		func(c *Config) *time.Duration { return &c.Notifications.HungerAlertCooldown },
		func(f *fileConfig) *string { return f.Notifications.HungerAlertCooldown }),
	durationSetting("notifications", "vitals_min_interval",
		func(c *Config) *time.Duration { return &c.Notifications.VitalsMinInterval },
		func(f *fileConfig) *string { return f.Notifications.VitalsMinInterval }),
}

type currencySettings struct {
	CharterR0                  *int64  `yaml:"charter_r0"`
	CharterFee                 *int64  `yaml:"charter_fee"`
	CharterMinDeposit          *int64  `yaml:"charter_min_deposit"`
	MintFeeBPS                 *int64  `yaml:"mint_fee_bps"`
	AutoCharterShareBPS        *int64  `yaml:"auto_charter_share_bps"`
	AutoCharterFloor           *int64  `yaml:"auto_charter_floor"`
	DeskSlippageBPS            *int64  `yaml:"desk_slippage_bps"`
	DeskPresets                []int64 `yaml:"desk_presets"`
	FXReserveFeeBPS            *int64  `yaml:"fx_reserve_fee_bps"`
	FXMaxMoveBPS               *int64  `yaml:"fx_max_move_bps"`
	FXMinTrades                *int64  `yaml:"fx_min_trades"`
	FXWindowPeriods            *int64  `yaml:"fx_window_periods"`
	FXMinOrderSUP              *int64  `yaml:"fx_min_order_sup"`
	FXBookLimit                *int64  `yaml:"fx_book_limit"`
	FXMaxOpenOrders            *int64  `yaml:"fx_max_open_orders"`
	FXConvertSlippageBPS       *int64  `yaml:"fx_convert_slippage_bps"`
	FXOrderTTL                 *string `yaml:"fx_order_ttl"`
	FXPeriod                   *string `yaml:"fx_period"`
	FXUnitPresets              []int64 `yaml:"fx_unit_presets"`
	ReserveGoldHaircutBPS      *int64  `yaml:"reserve_gold_haircut_bps"`
	ReserveWithdrawNoticeHours *int64  `yaml:"reserve_withdraw_notice_hours"`
	ReservePolicyRateBPS       *int64  `yaml:"reserve_policy_rate_bps"`
	InterventionCapBPS         *int64  `yaml:"intervention_cap_bps"`
	InterventionPotFloorBPS    *int64  `yaml:"intervention_pot_floor_bps"`
	WindDownDays               *int64  `yaml:"wind_down_days"`
	MacroMNormBPS              *int64  `yaml:"macro_m_norm_bps"`
	MacroKappaBPS              *int64  `yaml:"macro_kappa_bps"`
	MacroPiMaxBPS              *int64  `yaml:"macro_pi_max_bps"`
	MacroWTradableBPS          *int64  `yaml:"macro_w_tradable_bps"`
	InterventionDelay          *string `yaml:"intervention_delay"`
}

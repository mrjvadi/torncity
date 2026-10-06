package screens

import "github.com/mrjvadi/torncity/internal/presentation/military"

const AddrMinistry = military.AddrMinistry
const AddrForces = military.AddrForces
const AddrBranch = military.AddrBranch
const AddrStation = military.AddrStation
const AddrProcure = military.AddrProcure
const AddrArmsBuy = military.AddrArmsBuy
const MilitaryConfirm = military.MilitaryConfirm

type ForceClassLine = military.ForceClassLine
type BranchForces = military.BranchForces
type PeriodLine = military.PeriodLine
type MinistryView = military.MinistryView
type ForcesView = military.ForcesView
type GarrisonLine = military.GarrisonLine
type AssetGroup = military.AssetGroup
type MoveLine = military.MoveLine
type BranchView = military.BranchView
type StationView = military.StationView
type ProcureOffer = military.ProcureOffer

const ProcureBlockedExport = military.ProcureBlockedExport
const ProcureBlockedEmbargo = military.ProcureBlockedEmbargo

type ProcureView = military.ProcureView
type ArmsBuyView = military.ArmsBuyView

const MilitaryRefusedNotFound = military.MilitaryRefusedNotFound
const MilitaryRefusedNotHolder = military.MilitaryRefusedNotHolder
const MilitaryRefusedNotArms = military.MilitaryRefusedNotArms
const MilitaryRefusedExport = military.MilitaryRefusedExport
const MilitaryRefusedFunds = military.MilitaryRefusedFunds
const MilitaryRefusedStock = military.MilitaryRefusedStock
const MilitaryRefusedCity = military.MilitaryRefusedCity
const MilitaryRefusedNoCountry = military.MilitaryRefusedNoCountry
const MilitaryRefusedLicenceState = military.MilitaryRefusedLicenceState

type MilitaryRefusalView = military.MilitaryRefusalView
type MilitaryNoticeView = military.MilitaryNoticeView

const AddrWarBoard = military.AddrWarBoard
const AddrWarDeclare = military.AddrWarDeclare
const AddrWarJoin = military.AddrWarJoin
const AddrWarPropose = military.AddrWarPropose
const AddrWarAnswer = military.AddrWarAnswer
const AddrWarResume = military.AddrWarResume
const AddrWarRoom = military.AddrWarRoom
const AddrWarTarget = military.AddrWarTarget
const AddrWarLaunch = military.AddrWarLaunch
const WarConfirm = military.WarConfirm
const WarAllUnits = military.WarAllUnits

type ProposalLine = military.ProposalLine
type WarLine = military.WarLine
type JoinLine = military.JoinLine
type OccupationLine = military.OccupationLine
type DamageLine = military.DamageLine
type OperationLine = military.OperationLine
type WarBoardView = military.WarBoardView
type DeclareView = military.DeclareView
type WarDecisionView = military.WarDecisionView
type RoomTarget = military.RoomTarget
type WarRoomView = military.WarRoomView
type ForceOption = military.ForceOption
type WarTargetView = military.WarTargetView
type Estimate = military.Estimate
type LaunchView = military.LaunchView
type StrikeReportView = military.StrikeReportView
type WarNoticeView = military.WarNoticeView

const WarRefusedNotHolder = military.WarRefusedNotHolder
const WarRefusedNoCountry = military.WarRefusedNoCountry
const WarRefusedNotFound = military.WarRefusedNotFound
const WarRefusedSelf = military.WarRefusedSelf
const WarRefusedAtWar = military.WarRefusedAtWar
const WarRefusedState = military.WarRefusedState
const WarRefusedNotEnemy = military.WarRefusedNotEnemy
const WarRefusedNotYet = military.WarRefusedNotYet
const WarRefusedNoForces = military.WarRefusedNoForces
const WarRefusedNoMunition = military.WarRefusedNoMunition
const WarRefusedOpen = military.WarRefusedOpen
const WarRefusedNoAlly = military.WarRefusedNoAlly
const WarRefusedStock = military.WarRefusedStock

type WarRefusalView = military.WarRefusalView
type WarBlockedView = military.WarBlockedView

const AddrCompanyDefence = military.AddrCompanyDefence
const AddrLicences = military.AddrLicences
const AddrLicence = military.AddrLicence
const LicenceApprove = military.LicenceApprove
const LicenceReject = military.LicenceReject
const LicenceRevoke = military.LicenceRevoke
const CompanyRefusedDefence = military.CompanyRefusedDefence

type LicenceEntry = military.LicenceEntry
type CompanyDefenceView = military.CompanyDefenceView
type LicencesView = military.LicencesView
type LicenceNoticeView = military.LicenceNoticeView

type Notice = military.Notice

const NoticeStationStarted = military.NoticeStationStarted
const NoticeBuyDone = military.NoticeBuyDone
const NoticeDeclareDone = military.NoticeDeclareDone
const NoticeResumeDone = military.NoticeResumeDone
const NoticeLaunchDone = military.NoticeLaunchDone
const NoticeJoinDone = military.NoticeJoinDone
const NoticeProposeDone = military.NoticeProposeDone
const NoticeAnswerAccept = military.NoticeAnswerAccept
const NoticeAnswerDecline = military.NoticeAnswerDecline

var StationQtyChoices = military.StationQtyChoices
var BuyQtyChoices = military.BuyQtyChoices

type InjuryLine = military.InjuryLine

// MilitaryAttributeLine is the attribute line of a design as the military views carry it.
type MilitaryAttributeLine = military.AttributeLine

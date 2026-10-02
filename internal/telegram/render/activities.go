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

	// Crime, health, work, study, missions and skills (docs/adr/0039).
	Register(life.ScreenCrimeHub, screens.CrimeHub)
	Register(life.ScreenCrimeList, screens.CrimeList)
	Register(life.ScreenCrimeDetail, screens.CrimeDetail)
	Register(life.ScreenCrimeResult, screens.CrimeResult)
	Register(life.ScreenCrimeStarted, screens.CrimeStarted)
	Register(life.ScreenCrimeRecord, screens.CrimeRecord)
	Register(life.ScreenJail, screens.Jail)
	Register(life.ScreenBailed, screens.Bailed)
	Register(life.ScreenReportConfirm, screens.ReportConfirm)
	Register(life.ScreenCaseFiled, screens.CaseFiled)
	Register(life.ScreenCases, screens.Cases)
	Register(life.ScreenCrimeRefusal, screens.CrimeRefusal)
	Register(life.ScreenHospital, screens.Hospital)
	Register(life.ScreenTreatConfirm, screens.TreatConfirm)
	Register(life.ScreenTreated, screens.Treated)
	Register(life.ScreenClinicDesk, screens.ClinicDesk)
	Register(life.ScreenHealthRefusal, screens.HealthRefusal)
	Register(life.ScreenJobStatus, screens.JobStatus)
	Register(life.ScreenJobOpenings, screens.JobOpenings)
	Register(life.ScreenJobDetail, screens.JobDetail)
	Register(life.ScreenJobHired, screens.JobHired)
	Register(life.ScreenShiftStarted, screens.ShiftStarted)
	Register(life.ScreenShiftWorked, screens.ShiftWorked)
	Register(life.ScreenJobPromoted, screens.JobPromoted)
	Register(life.ScreenJobQuitConfirm, screens.JobQuitConfirm)
	Register(life.ScreenJobQuit, screens.JobQuit)
	Register(life.ScreenEducation, screens.Education)
	Register(life.ScreenCourseDetail, screens.CourseDetail)
	Register(life.ScreenEnrolled, screens.Enrolled)
	Register(life.ScreenCourseCompleted, screens.CourseCompleted)
	Register(life.ScreenMissionBoard, screens.MissionBoard)
	Register(life.ScreenMission, screens.Mission)
	Register(life.ScreenMissionsMine, screens.MissionsMine)
	Register(life.ScreenMissionRefusal, screens.MissionRefusal)
	Register(life.ScreenSkills, screens.Skills)
}

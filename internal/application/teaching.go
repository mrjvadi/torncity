package application

// Teaching and training money (docs/research/2026-10-03-activities-audit.md
// section 7; ADR 0009 section 2 lists each reason).
const (
	// ReasonCourseFee moves a course fee from the student to the treasury of the
	// settlement whose school teaches it.
	ReasonCourseFee Reason = "course_fee"
	// ReasonTuition moves a course fee from the student to a teacher teaching at
	// home; the settlement's income tax on it goes to the treasury in the same
	// transaction.
	ReasonTuition Reason = "tuition"
	// ReasonTeacherWage pays a player teacher from the treasury when a class ends.
	ReasonTeacherWage Reason = "teacher_wage"
	// ReasonTeacherWageNPC pays an NPC teacher from the treasury into the sink
	// when a class ends: an NPC is not a player, so the wage leaves the economy.
	ReasonTeacherWageNPC Reason = "teacher_wage_npc"
	// ReasonTrainingFee moves a training session's fee from the player to the
	// treasury of the settlement whose training ground it is.
	ReasonTrainingFee Reason = "training_fee"
	// ReasonTrainerWage pays the NPC trainer of a training ground one session's
	// wage from the treasury into the sink (an NPC is not a player).
	ReasonTrainerWage Reason = "trainer_wage"
)

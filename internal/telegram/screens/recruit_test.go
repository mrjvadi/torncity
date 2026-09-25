package screens

import (
	"strconv"
	"testing"

	"github.com/mrjvadi/torncity/internal/domain/player"
	"github.com/mrjvadi/torncity/internal/telegram/keyboards"
)

// TestRecruitAddressesFitTelegram holds every recruitment button to
// Telegram's 64 bytes at its longest: the largest public number a table
// can hand out, the longest skill code, a city code as long as the
// content's, and the longest field and act.
func TestRecruitAddressesFitTelegram(t *testing.T) {
	no := strconv.FormatInt(1<<62, 10)
	skill := ""
	for _, s := range player.SkillCodes() {
		if len(s) > len(skill) {
			skill = string(s)
		}
	}
	city := "aldrin_hollow_x"
	for _, parts := range [][]string{
		{AddrRecruitNew, "Q7M2K9B", skill, "100"},
		{AddrRecruitDraft, no, RecruitSectionCities},
		{AddrRecruitSet, no, RecruitFieldRelocation, "3"},
		{AddrRecruitSet, no, RecruitFieldPositions, "100"},
		{AddrRecruitSet, no, RecruitFieldCity, city, "1"},
		{AddrRecruitSet, no, RecruitFieldSkill, skill},
		{AddrRecruitPost, no, RecruitConfirm},
		{AddrRecruitDecide, no, RecruitReject},
		{AddrRecruitCancel, no, RecruitConfirm},
		{AddrSpecialist, no, SpecialistDismiss, RecruitConfirm},
		{AddrAsk, commandRecruitAmount, no, RecruitFieldRelocation},
	} {
		if keyboards.Data(parts...) == "" {
			t.Errorf("%v does not fit a button's %d bytes", parts, keyboards.MaxCallbackDataBytes)
		}
	}
}

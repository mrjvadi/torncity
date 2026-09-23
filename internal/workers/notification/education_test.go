package notification

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func courseRoute(t *testing.T) Route {
	t.Helper()
	for _, r := range Routes() {
		if r.Domain == "education" && r.Event == "completed" {
			return r
		}
	}
	t.Fatal("no education.completed route")
	return Route{}
}

// A finished course is announced to its student in their own language, by
// the course's name and the skills it trained — never by a code.
func TestCourseCompletedNoticeGoesToTheStudent(t *testing.T) {
	for _, lang := range []string{"fa", "en"} {
		t.Run(lang, func(t *testing.T) {
			p := englishPlayer()
			p.Language = lang
			r := newRig(t, p, link(botA, 1001))
			env := arrivalEvent(t, "req-course", r.now, nil)
			env.Metadata.Command = "education.complete"
			raw, err := json.Marshal(map[string]any{
				"enrollment_id": "e1",
				"player_id":     playerID,
				"course":        "first_aid",
				"course_name":   "First Aid",
				"certified":     true,
				"skills":        []map[string]any{{"skill": "medicine", "xp": 150, "level": 1}},
			})
			if err != nil {
				t.Fatal(err)
			}
			env.Payload = raw
			if err := r.w.Handle(context.Background(), courseRoute(t), env); err != nil {
				t.Fatal(err)
			}
			if len(r.sender.sent) != 1 {
				t.Fatalf("sent %d notices, want 1", len(r.sender.sent))
			}
			text := r.sender.sent[0].notice.Response.Text
			if strings.Contains(text, "first_aid") || strings.Contains(text, "medicine") {
				t.Errorf("notice shows a code: %q", text)
			}
			if lang == "en" && (!strings.Contains(text, "First Aid") || !strings.Contains(text, "Medicine")) {
				t.Errorf("notice = %q, want the course and the skill by name", text)
			}
		})
	}
}

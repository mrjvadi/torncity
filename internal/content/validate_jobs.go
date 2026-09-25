package content

import (
	"errors"
	"fmt"
)

// Validation failures for careers and courses. Like the city and route
// failures, each is wrapped with the offending entry.
var (
	// ErrUnknownRank means a tier named a rank that is not on the ladder.
	ErrUnknownRank = errors.New("content: unknown rank")

	// ErrInvalidDuration means a duration did not parse or was negative.
	ErrInvalidDuration = errors.New("content: invalid duration")

	// ErrEmptyCareerCode means a career arrived without its code.
	ErrEmptyCareerCode = errors.New("content: career code is required")

	// ErrDuplicateCareerCode means two careers claimed one code.
	ErrDuplicateCareerCode = errors.New("content: duplicate career code")

	// ErrMissingDisplayName means a career, a tier or a course has no
	// authored name. The name is the fallback a screen shows when the
	// catalogue has no translation, so without one a player would read a key.
	ErrMissingDisplayName = errors.New("content: display name is required")

	// ErrInvalidCareerContent means the domain refused a career: a rank that
	// does not rise, a skill that does not exist, a figure out of bounds.
	ErrInvalidCareerContent = errors.New("content: invalid career")

	// ErrUnknownCareerCity means a career's cities list names a city no
	// content file declares.
	ErrUnknownCareerCity = errors.New("content: career references an unknown city")

	// ErrUnknownCertification means a tier requires a certification that no
	// certifying course issues — a requirement nobody could ever meet.
	ErrUnknownCertification = errors.New("content: required certification is issued by no course")

	// ErrEmptyCourseCode means a course arrived without its code.
	ErrEmptyCourseCode = errors.New("content: course code is required")

	// ErrDuplicateCourseCode means two courses claimed one code.
	ErrDuplicateCourseCode = errors.New("content: duplicate course code")

	// ErrInvalidCourseContent means the domain refused a course.
	ErrInvalidCourseContent = errors.New("content: invalid course")

	// ErrUnknownCourseCity means a course is taught in a city no content file
	// declares.
	ErrUnknownCourseCity = errors.New("content: course references an unknown city")

	// ErrUnknownPrerequisite means a course requires a certification that no
	// certifying course issues.
	ErrUnknownPrerequisite = errors.New("content: prerequisite is issued by no course")

	// ErrPrerequisiteCycle means courses require each other in a circle, so
	// none of them could ever be taken.
	ErrPrerequisiteCycle = errors.New("content: course prerequisites form a cycle")
)

// validateJobs checks the careers and the courses together, because a career
// names certifications by the course that issues them and a check of either
// list alone could not see a dangling reference between the two.
func (p *Pack) validateJobs(problems *[]error) {
	cities := make(map[string]bool, len(p.Cities))
	for _, c := range p.Cities {
		cities[c.Code] = true
	}
	certifying := p.validateCourses(cities, problems)
	p.validateCareers(cities, certifying, problems)
}

// validateCourses checks every course and returns the codes of the courses
// that issue a certification.
func (p *Pack) validateCourses(cities map[string]bool, problems *[]error) map[string]bool {
	seen := make(map[string]bool, len(p.Courses))
	certifying := make(map[string]bool, len(p.Courses))
	for _, c := range p.Courses {
		if c.Code != "" && c.Certifies {
			certifying[c.Code] = true
		}
	}

	for i, c := range p.Courses {
		where := fmt.Sprintf("courses[%d]", i)
		switch {
		case c.Code == "":
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyCourseCode, where))
			continue
		case seen[c.Code]:
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateCourseCode, c.Code, where))
			continue
		}
		seen[c.Code] = true

		if c.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: course %q (%s)", ErrMissingDisplayName, c.Code, where))
		}
		if c.City != "" && !cities[c.City] {
			*problems = append(*problems, fmt.Errorf("%w: course %q is taught in %q",
				ErrUnknownCourseCity, c.Code, c.City))
		}
		for _, pre := range c.Prerequisites {
			if !certifying[pre] {
				*problems = append(*problems, fmt.Errorf("%w: course %q requires %q",
					ErrUnknownPrerequisite, c.Code, pre))
			}
		}
		course, err := c.Course()
		if err != nil {
			*problems = append(*problems, err)
			continue
		}
		if err := course.Validate(); err != nil {
			*problems = append(*problems, fmt.Errorf("%w: %s: %w", ErrInvalidCourseContent, where, err))
		}
	}

	if cycle := prerequisiteCycle(p.Courses); cycle != "" {
		*problems = append(*problems, fmt.Errorf("%w: through %q", ErrPrerequisiteCycle, cycle))
	}
	return certifying
}

// validateCareers checks every career against the cities and the certifying
// courses.
func (p *Pack) validateCareers(cities, certifying map[string]bool, problems *[]error) {
	seen := make(map[string]bool, len(p.Careers))
	for i, c := range p.Careers {
		where := fmt.Sprintf("careers[%d]", i)
		switch {
		case c.Code == "":
			*problems = append(*problems, fmt.Errorf("%w: %s", ErrEmptyCareerCode, where))
			continue
		case seen[c.Code]:
			*problems = append(*problems, fmt.Errorf("%w: %q (%s)", ErrDuplicateCareerCode, c.Code, where))
			continue
		}
		seen[c.Code] = true

		if c.Name == "" {
			*problems = append(*problems, fmt.Errorf("%w: career %q (%s)", ErrMissingDisplayName, c.Code, where))
		}
		for _, city := range c.Cities {
			if !cities[city] {
				*problems = append(*problems, fmt.Errorf("%w: career %q is offered in %q",
					ErrUnknownCareerCity, c.Code, city))
			}
		}
		if c.PaidBy != "" && c.PaidBy != PaidByDefenceFund {
			*problems = append(*problems, fmt.Errorf("%w: %s: career %q is paid by %q, not the base employer or %q",
				ErrInvalidCareerContent, where, c.Code, c.PaidBy, PaidByDefenceFund))
		}
		for j, t := range c.Tiers {
			for _, cert := range t.RequiredCertifications {
				if !certifying[cert] {
					*problems = append(*problems, fmt.Errorf("%w: career %q tier %d requires %q",
						ErrUnknownCertification, c.Code, j, cert))
				}
			}
		}

		career, err := c.Career()
		if err != nil {
			*problems = append(*problems, err)
			continue
		}
		if err := career.Validate(); err != nil {
			*problems = append(*problems, fmt.Errorf("%w: %s: %w", ErrInvalidCareerContent, where, err))
		}
	}
}

// prerequisiteCycle returns a course on a prerequisite cycle, or "".
func prerequisiteCycle(courses []CourseDef) string {
	edges := make(map[string][]string, len(courses))
	for _, c := range courses {
		edges[c.Code] = c.Prerequisites
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(courses))
	var visit func(code string) string
	visit = func(code string) string {
		switch state[code] {
		case visiting:
			return code
		case done:
			return ""
		}
		state[code] = visiting
		for _, next := range edges[code] {
			if found := visit(next); found != "" {
				return found
			}
		}
		state[code] = done
		return ""
	}
	for _, c := range courses {
		if found := visit(c.Code); found != "" {
			return found
		}
	}
	return ""
}

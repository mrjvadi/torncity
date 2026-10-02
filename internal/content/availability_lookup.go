package content

import "sort"

// The availability index (docs/audit/2026-10-01-rules-audit.md): the tags of
// availability.yml looked up by what they tag, so a handler can tell a player
// that a service is not offered where they stand, and where it is.

// buildAvailabilityIndex indexes the pack's tags by kind and code.
func (s *Snapshot) buildAvailabilityIndex(p *Pack) {
	s.availability = make(map[string]AvailabilityDef, len(p.Availability))
	for _, a := range p.Availability {
		s.availability[a.Kind+"/"+a.Code] = a
	}
	s.staffRoles = make(map[string]StaffRoleDef, len(p.StaffRoles))
	for _, r := range p.StaffRoles {
		s.staffRoles[r.Code] = r
	}
}

// StaffRole is a role someone must fill for a service to run (availability.yml
// staff_roles): the building it works in and what its holder must have.
func (s *Snapshot) StaffRole(code string) (StaffRoleDef, bool) {
	r, ok := s.staffRoles[code]
	return r, ok
}

// StaffRoles lists every staff role (availability.yml staff_roles), by code.
func (s *Snapshot) StaffRoles() []StaffRoleDef {
	out := make([]StaffRoleDef, 0, len(s.staffRoles))
	for _, r := range s.staffRoles {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// AvailabilityTag is the tag of one entry, by kind ("finance_service",
// "place", "building"...) and code.
func (s *Snapshot) AvailabilityTag(kind, code string) (AvailabilityDef, bool) {
	a, ok := s.availability[kind+"/"+code]
	return a, ok
}

// stageRank orders the settlement stages; an unknown stage has none.
var stageRank = map[string]int{StageVillage: 1, StageTown: 2, StageCity: 3, StageCountry: 4}

// StageRank is the rank of a settlement stage (village 1, town 2, city 3,
// country 4), 0 for support, undecided or anything unknown.
func StageRank(stage string) int { return stageRank[stage] }

// ServiceTag is the tag that says where a service is offered: the finance
// service of that name, or else the tag of the place that offers the service
// (places.yml lists the services each place has), taking the smallest stage
// when several do. ok is false when nothing tags the service, which means it
// is offered everywhere.
func (s *Snapshot) ServiceTag(service string) (AvailabilityDef, bool) {
	if a, ok := s.AvailabilityTag("finance_service", service); ok {
		return a, true
	}
	var tags []AvailabilityDef
	for _, pl := range s.places {
		offers := false
		for _, sv := range pl.Services {
			if string(sv) == service {
				offers = true
			}
		}
		if !offers {
			continue
		}
		if a, ok := s.AvailabilityTag("place", pl.Code); ok {
			tags = append(tags, a)
		}
	}
	if len(tags) == 0 {
		return AvailabilityDef{}, false
	}
	sort.SliceStable(tags, func(i, j int) bool { return stageRank[tags[i].Stage] < stageRank[tags[j].Stage] })
	return tags[0], true
}

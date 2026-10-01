package screens

// WarProposalName names a kind of proposal, a ceasefire or a peace.
func (c Context) WarProposalName(code string) string { return c.named("war.proposal."+code, code) }

// WarChanceName names how likely an operation is to succeed, in an estimate.
func (c Context) WarChanceName(code string) string { return c.named("war.chance."+code, code) }
